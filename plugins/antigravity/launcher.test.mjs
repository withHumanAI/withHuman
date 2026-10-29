// Runs the launcher as Antigravity does (sh, from the plugin root), from a
// throwaway copy of the plugin tree with a fake withhuman CLI.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const step = JSON.stringify({ conversationId: "c", stepIdx: 2, toolCall: { name: "run_command", args: { CommandLine: "ls" } } });
const platform = `${{ darwin: "darwin", linux: "linux" }[process.platform]}-${{ x64: "amd64", arm64: "arm64" }[process.arch]}`;

function fixture({ install = true } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "withhuman-antigravity-"));
  const root = join(dir, "plugin");
  mkdirSync(join(root, "plugins", "antigravity"), { recursive: true });
  copyFileSync(join(here, "launcher.sh"), join(root, "plugins", "antigravity", "launcher.sh"));
  const seen = join(dir, "seen");
  mkdirSync(seen);
  const cli = `#!/bin/sh\nenv > '${seen}/env'\necho "$@" > '${seen}/args'\ncat > '${seen}/stdin'\ncat '${dir}/reply'\n`;
  const sha = createHash("sha256").update(cli).digest("hex");
  writeFileSync(join(root, "plugins", "cli.pin"), `# test\nversion 9.9.9\n${platform} ${sha}\n`);
  const pinned = join(root, ".withhuman", "cli", "9.9.9", "withhuman");
  if (install) {
    mkdirSync(dirname(pinned), { recursive: true });
    writeFileSync(pinned, cli);
    chmodSync(pinned, 0o755);
  }
  writeFileSync(join(dir, "reply"), '{"decision":"ask"}\n');
  // A configuration path with a space, like macOS's Application Support.
  const config = join(dir, "with space", "withhuman");
  const env = { PATH: "/usr/bin:/bin", HOME: join(dir, "home"), WITHHUMAN_CONFIG_DIR: config, PROVIDER_SECRET: "do-not-pass" };
  return { dir, root, seen, cli, pinned, config, env };
}

function connect(f) {
  mkdirSync(join(f.config, "aap", "credentials"), { recursive: true });
  writeFileSync(join(f.config, "aap", "credentials", "antigravity.json"), "{}");
}

function hook(f, env = {}) {
  return new Promise((resolve) => {
    const child = spawn("sh", ["./plugins/antigravity/launcher.sh", "hook"], { cwd: f.root, env: { ...f.env, ...env } });
    let stdout = "";
    child.stdout.on("data", (chunk) => (stdout += chunk));
    child.on("close", (status) => resolve({ status, verdict: JSON.parse(stdout) }));
    child.stdin.end(step);
  });
}

test("off until connected", async () => {
  const f = fixture();
  assert.deepEqual((await hook(f)).verdict, { decision: "ask" });
  assert.equal(existsSync(join(f.seen, "args")), false, "the CLI ran while unconnected");
});

test("a connected plugin runs the CLI with only the variables it needs", async () => {
  const f = fixture();
  connect(f);
  const out = await hook(f);
  assert.equal(out.status, 0);
  assert.deepEqual(out.verdict, { decision: "ask" });
  assert.equal(readFileSync(join(f.seen, "args"), "utf8").trim(), "hook antigravity");
  assert.equal(readFileSync(join(f.seen, "stdin"), "utf8"), step);
  const env = readFileSync(join(f.seen, "env"), "utf8");
  assert.match(env, new RegExp(`^WITHHUMAN_CONFIG_DIR=${f.config}$`, "m"));
  assert.match(env, /^HOME=/m);
  assert.doesNotMatch(env, /PROVIDER_SECRET/);
});

test("the CLI's verdict passes through, and anything else fails closed", async () => {
  const f = fixture();
  connect(f);
  writeFileSync(join(f.dir, "reply"), '{"decision":"deny","reason":"A reviewer denied this call."}\n');
  assert.deepEqual((await hook(f)).verdict, { decision: "deny", reason: "A reviewer denied this call." });
  for (const text of ["", "not json", '{"decision":"allow"}', "{}"]) {
    writeFileSync(join(f.dir, "reply"), text);
    assert.equal((await hook(f)).verdict.decision, "deny", `passed: ${JSON.stringify(text)}`);
  }
});

test("the pinned CLI is downloaded and checked on first use", async () => {
  const f = fixture({ install: false });
  connect(f);
  let body = "#!/bin/sh\necho '{\"decision\":\"ask\"}'\n";
  const server = createServer((req, res) => {
    assert.equal(req.url, `/cli/9.9.9/withhuman-${platform}`);
    res.end(body);
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const env = { WITHHUMAN_CLI_BASE: `http://127.0.0.1:${server.address().port}` };
  try {
    // A download that fails its checksum is never run; with no CLI on PATH
    // the call is denied.
    assert.equal((await hook(f, env)).verdict.decision, "deny");
    assert.equal(existsSync(f.pinned), false);
    body = f.cli;
    assert.deepEqual((await hook(f, env)).verdict, { decision: "ask" });
    assert.equal(existsSync(f.pinned), true);
  } finally {
    server.close();
  }
});
