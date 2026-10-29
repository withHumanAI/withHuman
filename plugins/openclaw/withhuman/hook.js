// The before_tool_call hook: every tool call waits for withHuman's decision.
//
// For each call the hook runs the withhuman CLI's `hook openclaw` with the
// payload built from OpenClaw's event, plus `timeout_ms`, and maps the
// verdict the CLI prints: allow returns nothing (or the approved
// parameters), deny blocks the call with the reason.
//
// The plugin is connected in one of two ways. `openclaw withhuman setup`
// keeps the agent key and the withHuman address in the plugin's settings;
// the hook passes both to the CLI. `withhuman agent install openclaw`
// instead writes an aap.json beside these files naming the CLI and its own
// saved credential. Until one of them has run, the plugin is off and tool
// calls run unchecked.
//
// The hook registers its own timeout, a week plus margins, so a call can
// wait for a person. An operator can lower it in
// plugins.entries.withhuman.hooks; the request then expires just before
// that limit and the CLI is stopped just before it, so no request outlives
// its call. An aborted tool call stops the CLI, which withdraws the request.
//
// The CLI gets a minimal environment: only the variables it needs to find
// its files and reach withHuman, never the provider keys and other
// plugins' secrets in the Gateway process.

import { spawn } from "node:child_process";
import { accessSync, constants, readFileSync, statSync } from "node:fs";
import { delimiter, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { isDeepStrictEqual } from "node:util";

export const PLUGIN_ID = "withhuman";
export const DEFAULT_URL = "https://app.withhuman.ai";

const HERE = dirname(fileURLToPath(import.meta.url));

// How long a call can wait for a decision. The CLI expires the request this
// margin before the ceiling, so withHuman's expiry reaches the model first.
export const CLI_EXPIRY_MARGIN_MS = 30_000;
export const APPROVAL_CEILING_MS = 7 * 24 * 60 * 60 * 1000 + CLI_EXPIRY_MARGIN_MS;
// Time the CLI gets to print its verdict after the ceiling or a stop.
export const EXIT_GRACE_MS = 30_000;
// The hook's own timeout: the ceiling plus the grace, so the CLI's answer
// arrives before OpenClaw gives up on the hook.
export const HOOK_TIMEOUT_MS = APPROVAL_CEILING_MS + EXIT_GRACE_MS + 30_000;
// Under an operator's lower limit the request expires this long before it,
// and the CLI is stopped one second before it.
const LIMIT_MARGIN_MS = 3_000;
const LIMIT_STOP_MS = 1_000;

// What the CLI may see of the Gateway's environment.
const CLI_ENV_NAMES = new Set([
  "PATH", "HOME", "USER", "LOGNAME", "LANG", "TZ", "TMPDIR", "TMP", "TEMP",
  "OPENCLAW_STATE_DIR", "OPENCLAW_CONFIG_PATH", "XDG_CONFIG_HOME", "WITHHUMAN_CONFIG_DIR",
  "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
  "SSL_CERT_FILE", "SSL_CERT_DIR",
]);
const CLI_ENV_PREFIXES = ["LC_", "AAP_"];

export const FAIL_CLOSED_TEXT = "withHuman could not get an approval decision. The call was not run.";
export const STOPPED_TEXT = "The tool call was stopped before anyone decided. The call was not run.";
export const NO_CLI_TEXT =
  "The withHuman CLI is not installed, so this call was not run. Ask the user to run `openclaw withhuman setup`.";
export const KEY_REFUSED_TEXT =
  "withHuman refused this OpenClaw key. It may have expired or been revoked. " +
  "Ask the user to run `openclaw withhuman setup`. The call was not run.";
export const HOOK_LIMIT_TEXT =
  "No one approved this call within the {limit}-second limit set for withHuman's hook, so it was not run.";
// The start of the CLI's text for a refused credential.
const REFUSED_PREFIX = "The approval provider refused this runtime's credential";
// The start of the CLI's text for an expired request.
const EXPIRED_PREFIX = "The approval window or permission to start this call expired";

/** The plugin's settings, as saved by setup. */
export function readSettings(pluginConfig) {
  const config = pluginConfig && typeof pluginConfig === "object" ? pluginConfig : {};
  const text = (value) => (typeof value === "string" ? value.trim() : "");
  return { url: text(config.url) || DEFAULT_URL, agentKey: text(config.agentKey) };
}

/** What `withhuman agent install openclaw` wrote beside this file. */
export function installed(dir = HERE) {
  let config;
  try {
    config = JSON.parse(readFileSync(join(dir, "aap.json"), "utf8"));
  } catch {
    return {};
  }
  const out = {};
  for (const key of ["binary", "configDir"]) {
    if (config && typeof config[key] === "string" && config[key]) out[key] = config[key];
  }
  return out;
}

/** Whether setup or the withhuman CLI has connected this Gateway. */
export function connected(settings, dir = HERE) {
  return Boolean(settings.agentKey) || Boolean(installed(dir).binary);
}

/** The part of this process's environment the CLI may see. */
export function cliEnv(env = process.env) {
  const out = {};
  for (const [name, value] of Object.entries(env)) {
    if (value !== undefined && (CLI_ENV_NAMES.has(name) || CLI_ENV_PREFIXES.some((p) => name.startsWith(p)))) {
      out[name] = value;
    }
  }
  return out;
}

function executable(path) {
  try {
    if (!statSync(path).isFile()) return false;
    accessSync(path, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

/** The first `withhuman` on PATH. */
export function whichCLI(path = process.env.PATH ?? "") {
  for (const dir of path.split(delimiter)) {
    if (dir && executable(join(dir, "withhuman"))) return join(dir, "withhuman");
  }
  return null;
}

/**
 * The CLI the hook runs: the copy setup downloaded, else the one
 * `withhuman agent install openclaw` recorded, else the one on PATH.
 */
export function findCLI(dataDir, dir = HERE) {
  for (const binary of [downloadedCLI(dataDir), installed(dir).binary, whichCLI()]) {
    if (binary && executable(binary)) return binary;
  }
  return null;
}

/** Where setup keeps the CLI it downloads. */
export function downloadedCLI(dataDir) {
  return join(dataDir, "bin", "withhuman");
}

/** How the hook reaches withHuman: the CLI to run and its environment. */
export function keyConnection(binary, key, url, dataDir) {
  return {
    binary,
    env: {
      ...cliEnv(),
      AAP_INSTANCE_TOKEN: key,
      AAP_BASE_URL: url.replace(/\/+$/, "") + "/api/aap",
      AAP_CONFIG_DIR: join(dataDir, "aap"),
    },
    pluginKey: true,
  };
}

/** The connection for this Gateway, or null when the CLI is missing. */
export function currentConnection(settings, dataDir, dir = HERE) {
  const binary = findCLI(dataDir, dir);
  if (!binary) return null;
  if (settings.agentKey) return keyConnection(binary, settings.agentKey, settings.url, dataDir);
  const env = cliEnv();
  const { configDir } = installed(dir);
  if (configDir) env.AAP_CONFIG_DIR = configDir;
  return { binary, env, pluginKey: false };
}

/**
 * The limit an operator set on this plugin's before_tool_call hook, in
 * milliseconds, or 0 when none is set.
 */
export function operatorLimitMs(config) {
  const hooks = config?.plugins?.entries?.[PLUGIN_ID]?.hooks;
  for (const value of [hooks?.timeouts?.before_tool_call, hooks?.timeoutMs]) {
    if (Number.isFinite(value) && value > 0) return Math.floor(value);
  }
  return 0;
}

/**
 * Run `hook openclaw` once and resolve to its verdict. The CLI is stopped
 * after ceilingMs, or when signal aborts, and killed if it doesn't answer.
 * Never rejects.
 */
export function runHook(connection, payload, { signal, ceilingMs = APPROVAL_CEILING_MS, logger } = {}) {
  return new Promise((resolve) => {
    let child;
    try {
      child = spawn(connection.binary, ["hook", "openclaw"], { env: connection.env, stdio: ["pipe", "pipe", "pipe"] });
    } catch (error) {
      logger?.warn?.(`withhuman: could not start ${connection.binary}, blocking the call: ${error}`);
      resolve({ decision: "deny", reason: FAIL_CLOSED_TEXT });
      return;
    }
    let stdout = "";
    let stderr = "";
    let stopped = false;
    let settled = false;
    const timers = [];
    const settle = (verdict) => {
      if (settled) return;
      settled = true;
      for (const timer of timers) clearTimeout(timer);
      signal?.removeEventListener?.("abort", stop);
      resolve(verdict);
    };
    // SIGTERM lets the CLI withdraw the request and print its verdict.
    const stop = () => {
      if (stopped || settled) return;
      stopped = true;
      child.kill("SIGTERM");
      const kill = setTimeout(() => child.kill("SIGKILL"), EXIT_GRACE_MS);
      kill.unref?.();
      timers.push(kill);
    };
    child.stdout.on("data", (chunk) => { stdout += chunk; });
    child.stderr.on("data", (chunk) => { stderr += chunk; });
    child.on("error", (error) => {
      logger?.warn?.(`withhuman: could not run ${connection.binary}, blocking the call: ${error}`);
      settle({ decision: "deny", reason: FAIL_CLOSED_TEXT });
    });
    child.on("close", () => {
      if (stderr.trim()) logger?.warn?.(`withhuman: ${stderr.trim()}`);
      let verdict = parseVerdict(stdout, stopped ? STOPPED_TEXT : FAIL_CLOSED_TEXT);
      if (connection.pluginKey && verdict.decision === "deny" && verdict.reason.startsWith(REFUSED_PREFIX)) {
        verdict = { decision: "deny", reason: KEY_REFUSED_TEXT };
      }
      settle(verdict);
    });
    if (signal?.aborted) stop();
    else signal?.addEventListener?.("abort", stop, { once: true });
    const ceiling = setTimeout(stop, ceilingMs);
    ceiling.unref?.();
    timers.push(ceiling);
    child.stdin.on("error", () => {});
    child.stdin.end(JSON.stringify(payload));
  });
}

function parseVerdict(stdout, fallback) {
  let verdict;
  try {
    verdict = JSON.parse(stdout.trim());
  } catch {
    return { decision: "deny", reason: fallback };
  }
  if (verdict?.decision === "allow" && typeof verdict.gated === "boolean") return verdict;
  if (verdict?.decision === "deny" && typeof verdict.reason === "string" && verdict.reason) return verdict;
  return { decision: "deny", reason: fallback };
}

/**
 * The before_tool_call handler. getSettings and getConfig read the plugin's
 * settings and OpenClaw's config at call time, so an edit applies to the
 * next call.
 */
export function createHandler({ getSettings, getConfig, getDataDir, logger, dir = HERE }) {
  return async (event, ctx) => {
    const settings = getSettings();
    if (!connected(settings, dir)) return undefined;
    const block = (reason) => ({ block: true, blockReason: reason });
    const connection = currentConnection(settings, getDataDir(), dir);
    if (!connection) return block(NO_CLI_TEXT);
    let snapshot;
    try {
      snapshot = structuredClone(event.params ?? {});
    } catch {
      return block(FAIL_CLOSED_TEXT);
    }
    const limitMs = operatorLimitMs(getConfig());
    let timeoutMs = APPROVAL_CEILING_MS;
    let ceilingMs = APPROVAL_CEILING_MS + EXIT_GRACE_MS;
    if (limitMs && limitMs < HOOK_TIMEOUT_MS) {
      // The request expires before the limit and the CLI is stopped just
      // before it, so the request never outlives the call.
      timeoutMs = Math.max(limitMs - LIMIT_MARGIN_MS, 1_000) + CLI_EXPIRY_MARGIN_MS;
      ceilingMs = Math.max(limitMs - LIMIT_STOP_MS, 1_000);
    }
    const payload = {
      tool_name: event.toolName,
      params: snapshot,
      tool_kind: event.toolKind,
      tool_call_id: event.toolCallId,
      run_id: event.runId ?? ctx?.runId,
      agent_id: ctx?.agentId,
      session_id: ctx?.sessionId,
      session_key: ctx?.sessionKey,
      cwd: typeof event.params?.workdir === "string" ? event.params.workdir : undefined,
      timeout_ms: timeoutMs,
    };
    const verdict = await runHook(connection, payload, { signal: ctx?.abortSignal, ceilingMs, logger });
    if (verdict.decision === "deny") {
      if (limitMs && limitMs < HOOK_TIMEOUT_MS && verdict.reason.startsWith(EXPIRED_PREFIX)) {
        return block(HOOK_LIMIT_TEXT.replace("{limit}", String(Math.round(limitMs / 1000))));
      }
      return block(verdict.reason);
    }
    if (!verdict.gated) return undefined;
    // A held call runs with exactly the parameters that were approved, and
    // only while the approval is still valid.
    const expiresAt = Date.parse(verdict.expires_at);
    if (!Number.isFinite(expiresAt) || Date.now() >= expiresAt) return block(FAIL_CLOSED_TEXT);
    if (!isDeepStrictEqual(event.params ?? {}, snapshot)) return block(FAIL_CLOSED_TEXT);
    return { params: snapshot };
  };
}
