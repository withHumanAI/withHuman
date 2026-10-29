import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import {
  APPROVAL_CEILING_MS, FAIL_CLOSED_TEXT, KEY_REFUSED_TEXT, NO_CLI_TEXT, STOPPED_TEXT,
  createHandler, operatorLimitMs, readSettings,
} from "../withhuman/hook.js";
import { event, fixture } from "./support.mjs";

function handler(f, { dir, settings = {}, config = {} } = {}) {
  return createHandler({ getSettings: () => readSettings(settings), getConfig: () => config, getDataDir: () => f.dataDir, dir });
}

test("an unconnected plugin lets calls run", async (t) => {
  const f = fixture(t);
  const dir = f.plugin({ installed: false });
  assert.equal(await handler(f, { dir })(...event()), undefined);
  assert.equal(existsSync(f.log), false);
});

test("the CLI install's credential runs the hook and the approved parameters run", async (t) => {
  const f = fixture(t);
  const dir = f.plugin();
  const result = await handler(f, { dir })(...event({ command: "rm -rf build" }));
  assert.deepEqual(result, { params: { command: "rm -rf build" } });
  const seen = f.seen();
  assert.deepEqual(seen.argv, ["hook", "openclaw"]);
  assert.equal(seen.AAP_CONFIG_DIR, join(f.dir, "aap"));
  assert.equal(seen.AAP_INSTANCE_TOKEN, undefined);
  assert.equal(seen.payload.tool_name, "exec");
  assert.equal(seen.payload.tool_call_id, "call-1");
  assert.equal(seen.payload.session_key, "agent:main:main");
  assert.equal(seen.payload.timeout_ms, APPROVAL_CEILING_MS);
});

test("setup's key is passed to the CLI and wins over the CLI install", async (t) => {
  const f = fixture(t);
  const dir = f.plugin();
  const settings = { agentKey: "whi_key", url: "https://withhuman.example/" };
  await handler(f, { dir, settings })(...event());
  const seen = f.seen();
  assert.equal(seen.AAP_INSTANCE_TOKEN, "whi_key");
  assert.equal(seen.AAP_BASE_URL, "https://withhuman.example/api/aap");
  assert.equal(seen.AAP_CONFIG_DIR, join(f.dataDir, "aap"));
});

test("the CLI sees only the environment it needs", async (t) => {
  const f = fixture(t);
  const dir = f.plugin();
  process.env.OPENAI_API_KEY = "sk-secret";
  process.env.AAP_EXTRA = "kept";
  t.after(() => {
    delete process.env.OPENAI_API_KEY;
    delete process.env.AAP_EXTRA;
  });
  await handler(f, { dir })(...event());
  const { env } = f.seen();
  assert.ok(!env.includes("OPENAI_API_KEY"));
  assert.ok(env.includes("AAP_EXTRA"));
  assert.ok(env.includes("PATH"));
});

test("a denial blocks the call with the reviewer's reason", async (t) => {
  const f = fixture(t);
  f.fake({ FAKE_HOOK_MODE: "deny" });
  const result = await handler(f, { dir: f.plugin() })(...event());
  assert.deepEqual(result, { block: true, blockReason: "Denied by a reviewer." });
});

test("an ungated call runs unchanged", async (t) => {
  const f = fixture(t);
  f.fake({ FAKE_HOOK_MODE: "ungated" });
  assert.equal(await handler(f, { dir: f.plugin() })(...event()), undefined);
});

test("an approval that has expired blocks the call", async (t) => {
  const f = fixture(t);
  f.fake({ FAKE_HOOK_MODE: "stale" });
  assert.deepEqual(await handler(f, { dir: f.plugin() })(...event()), { block: true, blockReason: FAIL_CLOSED_TEXT });
});

test("parameters changed while waiting block the call", async (t) => {
  const f = fixture(t);
  f.fake({ FAKE_HOOK_MODE: "slow", FAKE_HOOK_SLEEP_MS: 200 });
  const [call, ctx] = event({ command: "ls" });
  const pending = handler(f, { dir: f.plugin() })(call, ctx);
  call.params.command = "rm -rf /";
  assert.deepEqual(await pending, { block: true, blockReason: FAIL_CLOSED_TEXT });
});

test("unreadable output fails closed", async (t) => {
  const f = fixture(t);
  f.fake({ FAKE_HOOK_MODE: "garbage" });
  assert.deepEqual(await handler(f, { dir: f.plugin() })(...event()), { block: true, blockReason: FAIL_CLOSED_TEXT });
});

test("a missing CLI blocks a connected plugin's calls", async (t) => {
  const f = fixture(t);
  const dir = f.plugin({ installed: false });
  const saved = process.env.PATH;
  process.env.PATH = "/nonexistent";
  t.after(() => { process.env.PATH = saved; });
  const result = await handler(f, { dir, settings: { agentKey: "whi_key" } })(...event());
  assert.deepEqual(result, { block: true, blockReason: NO_CLI_TEXT });
});

test("a refused setup key tells the person to run setup again", async (t) => {
  const f = fixture(t);
  f.fake({ FAKE_HOOK_MODE: "refused" });
  const result = await handler(f, { dir: f.plugin(), settings: { agentKey: "whi_key" } })(...event());
  assert.deepEqual(result, { block: true, blockReason: KEY_REFUSED_TEXT });
});

test("an aborted call stops the CLI, which withdraws the request", async (t) => {
  const f = fixture(t);
  f.fake({ FAKE_HOOK_MODE: "wait" });
  const [call, ctx] = event();
  const controller = new AbortController();
  ctx.abortSignal = controller.signal;
  const pending = handler(f, { dir: f.plugin() })(call, ctx);
  // Stop once the fake CLI is waiting for a decision.
  while (!existsSync(f.log)) await new Promise((resolve) => setTimeout(resolve, 50));
  await new Promise((resolve) => setTimeout(resolve, 100));
  controller.abort();
  assert.deepEqual(await pending, { block: true, blockReason: "Withdrawn before anyone decided." });
  assert.ok(existsSync(f.log + ".withdrawn"));
});

test("an operator's lower limit expires the request before it", async (t) => {
  const f = fixture(t);
  f.fake({ FAKE_HOOK_MODE: "expired" });
  const config = { plugins: { entries: { withhuman: { hooks: { timeouts: { before_tool_call: 60_000 } } } } } };
  const result = await handler(f, { dir: f.plugin(), config })(...event());
  assert.equal(f.seen().payload.timeout_ms, 57_000 + 30_000);
  assert.match(result.blockReason, /within the 60-second limit/);
});

test("an operator's limit stops the CLI just before it", async (t) => {
  const f = fixture(t);
  f.fake({ FAKE_HOOK_MODE: "wait" });
  const config = { plugins: { entries: { withhuman: { hooks: { timeoutMs: 3_000 } } } } };
  const result = await handler(f, { dir: f.plugin(), config })(...event());
  assert.deepEqual(result, { block: true, blockReason: "Withdrawn before anyone decided." });
});

test("a CLI that ignores the stop is killed", { timeout: 60_000 }, async (t) => {
  const f = fixture(t);
  f.fake({ FAKE_HOOK_MODE: "stubborn" });
  const config = { plugins: { entries: { withhuman: { hooks: { timeoutMs: 1_000 } } } } };
  const result = await handler(f, { dir: f.plugin(), config })(...event());
  assert.deepEqual(result, { block: true, blockReason: STOPPED_TEXT });
});

test("operatorLimitMs prefers the per-hook limit", () => {
  assert.equal(operatorLimitMs({}), 0);
  assert.equal(operatorLimitMs({ plugins: { entries: { withhuman: { hooks: { timeoutMs: 5000 } } } } }), 5000);
  assert.equal(operatorLimitMs({ plugins: { entries: { withhuman: { hooks: { timeoutMs: 5000, timeouts: { before_tool_call: 9000 } } } } } }), 9000);
});
