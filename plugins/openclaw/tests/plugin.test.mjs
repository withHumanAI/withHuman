import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { test } from "node:test";

import plugin, { dataDir, host } from "../withhuman/index.js";
import { HOOK_TIMEOUT_MS } from "../withhuman/hook.js";
import { PLUGIN_DIR } from "./support.mjs";

test("the published package carries every plugin file", () => {
  const onDisk = readdirSync(PLUGIN_DIR, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile())
    .map((entry) => relative(PLUGIN_DIR, join(entry.parentPath, entry.name)))
    .filter((path) => path !== "package.json")
    .sort();
  const { files } = JSON.parse(readFileSync(join(PLUGIN_DIR, "package.json"), "utf8"));
  assert.deepEqual([...files].sort(), onDisk);
});

/** A plugin API that records what the plugin registers and writes. */
function fakeAPI({ mode = "cli-metadata", pluginConfig = {}, config = {} } = {}) {
  const api = {
    registrationMode: mode,
    pluginConfig,
    config,
    hooks: [],
    clis: [],
    on: (name, handler, options) => api.hooks.push({ name, handler, options }),
    registerCli: (registrar, options) => api.clis.push({ registrar, options }),
    written: config,
    runtime: {
      state: { resolveStateDir: () => "/state" },
      config: {
        mutateConfigFile: async ({ afterWrite, mutate }) => {
          assert.deepEqual(afterWrite, { mode: "auto" });
          mutate(api.written);
          return {};
        },
      },
    },
  };
  return api;
}

test("register adds one before_tool_call hook with its own long timeout", () => {
  const api = fakeAPI();
  // The runtime throws during CLI metadata registration; register must not touch it.
  const runtime = api.runtime;
  Object.defineProperty(api, "runtime", { get: () => { throw new Error("runtime unavailable"); } });
  plugin.register(api);
  assert.deepEqual(api.hooks.map((h) => [h.name, h.options.timeoutMs]), [["before_tool_call", HOOK_TIMEOUT_MS]]);
  assert.deepEqual(api.clis[0].options.descriptors.map((d) => d.name), ["withhuman"]);
  assert.equal(dataDir({ runtime }), join("/state", "withhuman"));
});

test("setup saves the key and address in the plugin's entry and enables it", async () => {
  const api = fakeAPI({ config: { plugins: { entries: { withhuman: { config: { url: "old" }, hooks: { timeoutMs: 5000 } } } } } });
  await host(api, "/data").saveSettings({ url: "https://app.withhuman.ai", agentKey: "whi_key" });
  assert.deepEqual(api.written.plugins.entries.withhuman, {
    enabled: true,
    config: { url: "https://app.withhuman.ai", agentKey: "whi_key" },
    hooks: { timeoutMs: 5000 },
  });
});

test("setup adds streamable HTTP MCP servers that send the key", async () => {
  const api = fakeAPI({ config: { mcp: { servers: { other: { command: "x" } } } } });
  await host(api, "/data").addMCPServers([{ name: "withhuman-gateway", url: "https://app.withhuman.ai/mcp", timeoutSeconds: 600 }], "whi_key");
  assert.deepEqual(api.written.mcp.servers, {
    other: { command: "x" },
    "withhuman-gateway": {
      url: "https://app.withhuman.ai/mcp",
      transport: "streamable-http",
      headers: { Authorization: "Bearer whi_key" },
      requestTimeoutMs: 600_000,
    },
  });
});
