// Shared setup: a temporary directory with a fake withhuman CLI, and a copy
// of the plugin pointed at it through an aap.json, as `withhuman agent
// install openclaw` writes.

import { chmodSync, cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export const PLUGIN_DIR = join(dirname(fileURLToPath(import.meta.url)), "..", "withhuman");
const FAKE_HOOK = join(dirname(fileURLToPath(import.meta.url)), "fake-hook.mjs");

export function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "withhuman-openclaw-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const settingsPath = join(dir, "fake-hook.json");
  const log = join(dir, "hook.json");
  const fake = { FAKE_HOOK_LOG: log, FAKE_HOOK_MODE: "approve" };
  const write = () => writeFileSync(settingsPath, JSON.stringify(fake));
  write();
  const binary = join(dir, "withhuman");
  makeCLI(binary, settingsPath);
  return {
    dir,
    binary,
    log,
    dataDir: join(dir, "data"),
    /** Change the fake CLI's settings. */
    fake(values) {
      Object.assign(fake, values);
      write();
    },
    /** What the fake CLI saw on its last run. */
    seen: () => JSON.parse(readFileSync(log, "utf8")),
    /** Copy the plugin into dir/plugin, with an aap.json when installed. */
    plugin({ installed = true, configDir = join(dir, "aap") } = {}) {
      const target = join(dir, "plugin");
      cpSync(PLUGIN_DIR, target, { recursive: true });
      if (installed) writeFileSync(join(target, "aap.json"), JSON.stringify({ binary, configDir }));
      return target;
    },
  };
}

/** Write a copy of the fake CLI at path. */
export function makeCLI(path, settingsPath) {
  writeFileSync(path, `#!${process.execPath}\nglobalThis.FAKE_HOOK_SETTINGS = ${JSON.stringify(settingsPath)};\n` +
    readFileSync(FAKE_HOOK, "utf8"));
  chmodSync(path, 0o755);
}

/** A before_tool_call event and context. */
export function event(params = { command: "ls" }) {
  return [
    { toolName: "exec", params, toolCallId: "call-1", runId: "run-1", toolKind: "core" },
    { agentId: "main", sessionId: "session-1", sessionKey: "agent:main:main" },
  ];
}
