// withHuman for OpenClaw: every tool call waits for withHuman's decision.
//
// register adds one before_tool_call hook (hook.js) and the
// `openclaw withhuman` command (setup.js), and refreshes the CLI that
// setup downloaded in the background.
//
// This file is plain ESM with no dependencies, so it loads from
// plugins.load.paths or a ClawHub install without a build step; the entry
// shape is the one definePluginEntry would produce.

import { spawn } from "node:child_process";
import { join } from "node:path";

import { HOOK_TIMEOUT_MS, PLUGIN_ID, connected, createHandler, currentConnection, readSettings } from "./hook.js";
import { refreshDownloadedCLI, setup, terminal, test } from "./setup.js";

// What setup installs from ClawHub.
export const SKILLS = ["@withhuman/withhuman", "@withhuman/withhuman-pipelines"];

/**
 * Where the plugin keeps the CLI it downloads and the hook's own state.
 * The runtime is only there once the plugin is fully loaded, so this is
 * read when it is needed, never during registration.
 */
export function dataDir(api) {
  return join(api.runtime.state.resolveStateDir(), PLUGIN_ID);
}

export default {
  id: PLUGIN_ID,
  name: "withHuman",
  description: "Human approval for OpenClaw tool calls.",
  register(api) {
    const settings = () => readSettings(api.pluginConfig);
    api.on("before_tool_call", createHandler({
      getSettings: settings,
      getConfig: () => api.config,
      getDataDir: () => dataDir(api),
      logger: api.logger,
    }), { timeoutMs: HOOK_TIMEOUT_MS });

    api.registerCli(({ program }) => {
      const command = program.command(PLUGIN_ID).description("Connect OpenClaw to withHuman");
      command.command("setup")
        .description("Connect this OpenClaw to withHuman")
        .option("--url <url>", "withHuman address, for self-hosted withHuman")
        .option("--agent <slug>", "agent slug to connect to")
        .action(async (options) => {
          const io = terminal();
          try {
            process.exitCode = await setup(host(api, dataDir(api)), io, options);
          } finally {
            io.close();
          }
        });
      command.command("test")
        .description("Send a test request to withHuman")
        .action(async () => {
          const io = terminal();
          try {
            const connection = connected(settings()) ? currentConnection(settings(), dataDir(api)) : null;
            if (!connection) {
              io.print("withHuman isn't set up for this OpenClaw. Run `openclaw withhuman setup`.");
              process.exitCode = 1;
              return;
            }
            process.exitCode = await test(connection, io);
          } finally {
            io.close();
          }
        });
    }, { descriptors: [{ name: PLUGIN_ID, description: "Connect OpenClaw to withHuman", hasSubcommands: true }] });

    if (api.registrationMode !== "full") return;
    // After an update that pins a newer CLI, fetch it without holding up the load.
    refreshDownloadedCLI(dataDir(api), { connected: connected(settings()), say: (line) => api.logger?.info?.(`withhuman: ${line}`) })
      .catch((error) => api.logger?.warn?.(`withhuman: could not update the withHuman CLI: ${error.message}`));
  },
};

/** What setup needs from OpenClaw. */
export function host(api, dir) {
  const mutate = (change) => api.runtime.config.mutateConfigFile({
    afterWrite: { mode: "auto" },
    mutate(draft) {
      change(draft);
    },
  });
  return {
    dataDir: dir,
    skills: SKILLS,
    settings: () => readSettings(api.pluginConfig),
    saveSettings: ({ url, agentKey }) => mutate((draft) => {
      draft.plugins ??= {};
      draft.plugins.entries ??= {};
      const entry = (draft.plugins.entries[PLUGIN_ID] ??= {});
      entry.enabled = true;
      entry.config = { ...entry.config, url, agentKey };
    }),
    // OpenClaw's MCP headers take no secret references, so the entry
    // carries the key itself, like the plugin's settings.
    addMCPServers: (servers, agentKey) => mutate((draft) => {
      draft.mcp ??= {};
      draft.mcp.servers ??= {};
      for (const server of servers) {
        draft.mcp.servers[server.name] = {
          url: server.url,
          transport: "streamable-http",
          headers: { Authorization: `Bearer ${agentKey}` },
          requestTimeoutMs: server.timeoutSeconds * 1000,
        };
      }
    }),
    installSkills: (io) => installSkills(io),
  };
}

/**
 * Install the skills with OpenClaw's own installer, into the shared skills
 * directory, so `openclaw skills update --all --global` keeps them current.
 * A skill already installed is left as it is.
 */
async function installSkills(io) {
  const openclaw = process.argv[1] ? [process.execPath, process.argv[1]] : ["openclaw"];
  for (const name of SKILLS) {
    const code = await new Promise((resolve) => {
      const child = spawn(openclaw[0], [...openclaw.slice(1), "skills", "install", name, "--global"], { stdio: "inherit" });
      child.on("error", () => resolve(1));
      child.on("close", resolve);
    });
    if (code !== 0) io.print(`Install it later with \`openclaw skills install ${name} --global\`.`);
  }
}
