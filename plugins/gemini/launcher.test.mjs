// Runs the launcher as Gemini does, from a throwaway copy of the plugin
// tree with a fake withhuman CLI, so nothing here touches a real home.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { platformName } from "./launcher.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const call = JSON.stringify({ hook_event_name: "BeforeTool", session_id: "s", cwd: "/work", tool_name: "run_shell_command", tool_input: { command: "ls" } });

// fixture lays out a plugin root with the launcher, a pin and a fake CLI
// that records what it was given and prints the reply file.
function fixture({ install = true } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "withhuman-gemini-"));
  const root = join(dir, "extension");
  mkdirSync(join(root, "plugins", "gemini"), { recursive: true });
  copyFileSync(join(here, "launcher.mjs"), join(root, "plugins", "gemini", "launcher.mjs"));
  const seen = join(dir, "seen");
  mkdirSync(seen);
  const cli = `#!/bin/sh\nenv > '${seen}/env'\necho "$@" > '${seen}/args'\ncat > '${seen}/stdin'\ncat '${dir}/reply'\n`;
  const sha = createHash("sha256").update(cli).digest("hex");
  writeFileSync(join(root, "plugins", "cli.pin"), `# test\nversion 9.9.9\n${platformName()} ${sha}\n`);
  const pinned = join(root, ".withhuman", "cli", "9.9.9", "withhuman");
  if (install) {
    mkdirSync(dirname(pinned), { recursive: true });
    writeFileSync(pinned, cli);
    chmodSync(pinned, 0o755);
  }
  writeFileSync(join(dir, "reply"), "{}\n");
  const env = { PATH: "/usr/bin:/bin", HOME: join(dir, "home"), WITHHUMAN_CONFIG_DIR: join(dir, "config"), PROVIDER_SECRET: "do-not-pass" };
  return { dir, root, seen, cli, pinned, env };
}

function hook(f, env, input = call) {
  const result = spawnSync(process.execPath, [join(f.root, "plugins", "gemini", "launcher.mjs"), "hook"], { input, env: { ...f.env, ...env }, encoding: "utf8" });
  return { status: result.status, verdict: JSON.parse(result.stdout), stderr: result.stderr };
}

function reply(f, text) {
  writeFileSync(join(f.dir, "reply"), text);
}

test("off until connected", () => {
  const f = fixture();
  const out = hook(f, {});
  assert.deepEqual(out.verdict, {});
  assert.equal(existsSync(join(f.seen, "args")), false, "the CLI ran while unconnected");
  // A placeholder Gemini could not fill is not a key.
  assert.deepEqual(hook(f, { WITHHUMAN_AGENT_TOKEN: "${WITHHUMAN_AGENT_TOKEN}" }).verdict, {});
  assert.equal(existsSync(join(f.seen, "args")), false);
});

test("an agent key reaches the CLI as AAP's credential, and nothing else does", () => {
  const f = fixture();
  const out = hook(f, { WITHHUMAN_AGENT_TOKEN: "whc_key", WITHHUMAN_URL: "https://approvals.example.com/" });
  assert.equal(out.status, 0);
  assert.deepEqual(out.verdict, {});
  assert.equal(readFileSync(join(f.seen, "args"), "utf8").trim(), "hook gemini");
  assert.equal(readFileSync(join(f.seen, "stdin"), "utf8"), call);
  const env = readFileSync(join(f.seen, "env"), "utf8");
  assert.match(env, /^AAP_INSTANCE_TOKEN=whc_key$/m);
  assert.match(env, /^AAP_BASE_URL=https:\/\/approvals\.example\.com\/api\/aap$/m);
  assert.match(env, new RegExp(`^AAP_CONFIG_DIR=${realpathSync(f.root)}/\\.withhuman/aap$`, "m"));
  assert.doesNotMatch(env, /PROVIDER_SECRET|WITHHUMAN_AGENT_TOKEN/);
});

test("a saved CLI connection runs the CLI with its own credential", () => {
  const f = fixture();
  mkdirSync(join(f.env.WITHHUMAN_CONFIG_DIR, "aap", "credentials"), { recursive: true });
  writeFileSync(join(f.env.WITHHUMAN_CONFIG_DIR, "aap", "credentials", "gemini.json"), "{}");
  reply(f, '{"decision":"deny","reason":"A reviewer denied this call."}\n');
  assert.deepEqual(hook(f, {}).verdict, { decision: "deny", reason: "A reviewer denied this call." });
  const env = readFileSync(join(f.seen, "env"), "utf8");
  assert.doesNotMatch(env, /AAP_INSTANCE_TOKEN/);
  assert.match(env, /^WITHHUMAN_CONFIG_DIR=/m);
});

test("anything but a verdict fails closed", () => {
  const f = fixture();
  for (const text of ["", "not json", '{"decision":"allow"}', '{"decision":"deny"}', "[]"]) {
    reply(f, text);
    const out = hook(f, { WITHHUMAN_AGENT_TOKEN: "whc_key" });
    assert.equal(out.verdict.decision, "deny", `passed: ${JSON.stringify(text)}`);
  }
});

test("a refused key names the setting to change", () => {
  const f = fixture();
  reply(f, '{"decision":"deny","reason":"The approval provider refused this runtime\'s credential. Run provider agent eject gemini."}\n');
  assert.match(hook(f, { WITHHUMAN_AGENT_TOKEN: "whc_key" }).verdict.reason, /gemini extensions config withhuman/);
});

test("the pinned CLI is downloaded and checked on first use", async () => {
  const f = fixture({ install: false });
  let body = f.cli;
  const server = createServer((req, res) => {
    assert.equal(req.url, `/cli/9.9.9/withhuman-${platformName()}`);
    res.end(body);
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  try {
    const env = { WITHHUMAN_AGENT_TOKEN: "whc_key", WITHHUMAN_CLI_BASE: base };
    // The test's server runs in this process, so the launcher must not block it.
    const run = () =>
      new Promise((resolve) => {
        const child = spawn(process.execPath, [join(f.root, "plugins", "gemini", "launcher.mjs"), "hook"], { env: { ...f.env, ...env } });
        let stdout = "";
        child.stdout.on("data", (chunk) => (stdout += chunk));
        child.on("close", () => resolve(JSON.parse(stdout)));
        child.stdin.end(call);
      });
    // A download that fails its checksum is never run, and with no CLI on
    // PATH the call is denied.
    body = "#!/bin/sh\necho '{}'\n";
    assert.equal((await run()).decision, "deny");
    assert.equal(existsSync(f.pinned), false);
    body = f.cli;
    assert.deepEqual(await run(), {});
    assert.equal(existsSync(f.pinned), true);
  } finally {
    server.close();
  }
});
