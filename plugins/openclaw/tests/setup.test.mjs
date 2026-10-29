import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { copyFileSync, existsSync, mkdirSync, readFileSync, statSync } from "node:fs";
import { createServer } from "node:http";
import { join } from "node:path";
import { test } from "node:test";

import { downloadedCLI } from "../withhuman/hook.js";
import { downloadCLI, older, origin, platformName, refreshDownloadedCLI, setup } from "../withhuman/setup.js";
import { fixture, makeCLI } from "./support.mjs";

/** A fake withHuman: enrollment, key checks, runtime config and downloads. */
async function fakeWithHuman(t, { servers = [], download = null, pendingPolls = 1 } = {}) {
  const seen = [];
  let polls = 0;
  const server = createServer(async (req, res) => {
    let body = "";
    for await (const chunk of req) body += chunk;
    seen.push({ method: req.method, url: req.url, body: body ? JSON.parse(body) : null, headers: req.headers });
    const send = (status, value) => {
      res.writeHead(status, { "Content-Type": "application/json" });
      res.end(JSON.stringify(value));
    };
    const origin = `http://localhost:${server.address().port}`;
    if (req.url === "/auth/agent_enrollments") {
      return send(201, { user_code: "ABCD-EFGH", verification_uri: origin + "/connect/authorize", enrollment_code: "enroll-1" });
    }
    if (req.url === "/auth/agent_enrollments/token") {
      if (polls++ < pendingPolls) return send(404, { error: { code: "authorization_pending" } });
      return send(200, { credential: { token: "whi_signed_in" }, agent: { name: "OpenClaw bot" }, agent_instance: { name: "laptop" } });
    }
    if (req.url === "/api/v1/agent_instances/self") {
      if (req.headers.authorization !== "Bearer whi_pasted") return send(401, { error: { code: "unauthorized" } });
      return send(200, { agent_slug: "openclaw-bot" });
    }
    if (req.url === "/api/runtime-config") {
      return send(200, { agent_mcp_servers: servers.map((s) => ({ ...s, url: s.url.replace("ORIGIN", origin) })) });
    }
    if (download && req.url === download.path) {
      res.writeHead(200);
      return res.end(download.body);
    }
    send(404, {});
  });
  await new Promise((resolve) => server.listen(0, "localhost", resolve));
  t.after(() => server.close());
  return { origin: `http://localhost:${server.address().port}`, seen };
}

/** A scripted terminal: answers in order, everything printed is kept. */
function scriptedIO(answers) {
  const printed = [];
  const queue = [...answers];
  const next = (prompt) => {
    printed.push(prompt);
    if (queue.length === 0) throw new Error(`unexpected question: ${prompt}`);
    return Promise.resolve(queue.shift());
  };
  return { printed, print: (line) => printed.push(line), ask: next, askSecret: next, sleep: () => Promise.resolve(), close() {} };
}

function fakeHost(f) {
  const host = {
    dataDir: f.dataDir,
    skills: ["@withhuman/withhuman"],
    saved: null,
    mcp: null,
    skillsInstalled: false,
    settings: () => ({ url: "https://app.withhuman.ai" }),
    saveSettings: async (settings) => { host.saved = settings; },
    addMCPServers: async (servers, key) => { host.mcp = { servers, key }; },
    installSkills: async () => { host.skillsInstalled = true; },
  };
  return host;
}

function withCLIOnPath(t, f) {
  const saved = process.env.PATH;
  process.env.PATH = `${f.dir}:${saved}`;
  t.after(() => { process.env.PATH = saved; });
}

test("setup signs in, saves the key and address, and sends a test request", async (t) => {
  const f = fixture(t);
  withCLIOnPath(t, f);
  const server = await fakeWithHuman(t);
  const host = fakeHost(f);
  const io = scriptedIO([server.origin, "1", "n", "y"]);
  assert.equal(await setup(host, io), 0);
  assert.deepEqual(host.saved, { url: server.origin, agentKey: "whi_signed_in" });
  assert.ok(io.printed.includes('Connected to withHuman as agent "OpenClaw bot" (instance laptop).'));
  const enrollment = server.seen.find((r) => r.url === "/auth/agent_enrollments");
  assert.equal(enrollment.body.runtime, "openclaw");
  assert.equal(enrollment.headers["user-agent"], "withhuman-openclaw-plugin");
  // The test request ran through the CLI with the new key.
  assert.equal(f.seen().AAP_INSTANCE_TOKEN, "whi_signed_in");
  assert.equal(f.seen().payload.tool_name, "withhuman_test");
  assert.ok(io.printed.includes("Approved. withHuman is working."));
  assert.equal(host.skillsInstalled, false);
});

test("setup checks a pasted key and refuses one withHuman rejects", async (t) => {
  const f = fixture(t);
  withCLIOnPath(t, f);
  const server = await fakeWithHuman(t);
  const host = fakeHost(f);
  assert.equal(await setup(host, scriptedIO([server.origin, "2", "whi_wrong"])), 1);
  assert.equal(host.saved, null);
  const io = scriptedIO([server.origin, "2", "whi_pasted", "y", "n"]);
  assert.equal(await setup(host, io), 0);
  assert.deepEqual(host.saved, { url: server.origin, agentKey: "whi_pasted" });
  assert.equal(host.skillsInstalled, true);
});

test("setup adds only the MCP servers on the withHuman address", async (t) => {
  const f = fixture(t);
  withCLIOnPath(t, f);
  const server = await fakeWithHuman(t, {
    servers: [
      { name: "withhuman-gateway", label: "withHuman MCP gateway", url: "ORIGIN/api/gateway/v1/mcp" },
      { name: "elsewhere", url: "https://evil.example/mcp" },
    ],
  });
  const host = fakeHost(f);
  assert.equal(await setup(host, scriptedIO([server.origin, "2", "whi_pasted", "n", "n"])), 0);
  assert.deepEqual(host.mcp, {
    servers: [{ name: "withhuman-gateway", url: server.origin + "/api/gateway/v1/mcp", timeoutSeconds: 600 }],
    key: "whi_pasted",
  });
});

test("setup refuses an address that is not an HTTPS origin", () => {
  assert.equal(origin("https://app.withhuman.ai/"), "https://app.withhuman.ai");
  assert.equal(origin("http://localhost:8080"), "http://localhost:8080");
  for (const bad of ["http://withhuman.example", "https://app.withhuman.ai/path", "https://user@app.withhuman.ai", "nonsense"]) {
    assert.throws(() => origin(bad), /must look like/);
  }
});

test("the CLI is downloaded only when it matches its checksum", async (t) => {
  const f = fixture(t);
  const body = readFileSync(f.binary);
  const path = `/cli/9.9.9/withhuman-${platformName()}`;
  const server = await fakeWithHuman(t, { download: { path, body } });
  const good = { version: "9.9.9", downloads: server.origin + "/cli", sha256: { [platformName()]: createHash("sha256").update(body).digest("hex") } };
  await assert.rejects(downloadCLI(f.dataDir, () => {}, { ...good, sha256: { [platformName()]: "0".repeat(64) } }), /checksum/);
  assert.equal(existsSync(downloadedCLI(f.dataDir)), false);
  assert.equal(await downloadCLI(f.dataDir, () => {}, good), downloadedCLI(f.dataDir));
  assert.ok(statSync(downloadedCLI(f.dataDir)).mode & 0o100);
});

test("an older downloaded CLI is replaced, and one the person installed is left alone", async (t) => {
  const f = fixture(t);
  const replacement = join(f.dir, "replacement");
  makeCLI(replacement, join(f.dir, "replacement.json"));
  const body = readFileSync(replacement);
  const path = `/cli/9.9.9/withhuman-${platformName()}`;
  const server = await fakeWithHuman(t, { download: { path, body } });
  const release = { version: "9.9.9", downloads: server.origin + "/cli", sha256: { [platformName()]: createHash("sha256").update(body).digest("hex") } };
  // The fixture's CLI reports an old version.
  f.fake({ FAKE_CLI_VERSION: "withhuman 1.0.1" });
  withCLIOnPath(t, f);
  // Only the person's CLI on PATH: nothing is downloaded.
  await refreshDownloadedCLI(f.dataDir, { connected: true, release });
  assert.equal(existsSync(downloadedCLI(f.dataDir)), false);
  // An old copy setup downloaded is replaced with the pinned one.
  mkdirSync(join(f.dataDir, "bin"), { recursive: true });
  copyFileSync(f.binary, downloadedCLI(f.dataDir));
  await refreshDownloadedCLI(f.dataDir, { connected: true, release });
  assert.deepEqual(readFileSync(downloadedCLI(f.dataDir)), body);
});

test("older compares released versions; a development build is new enough", () => {
  assert.equal(older([1, 0, 9], "1.0.57"), true);
  assert.equal(older([1, 0, 57], "1.0.57"), false);
  assert.equal(older([2, 0, 0], "1.0.57"), false);
  assert.equal(older(null, "1.0.57"), false);
});
