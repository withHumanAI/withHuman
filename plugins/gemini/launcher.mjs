// withHuman for Gemini CLI: the extension's BeforeTool hook and its MCP
// gateway relay both run this file.
//
//   node launcher.mjs hook   reads one call on stdin and prints Gemini's verdict
//   node launcher.mjs mcp    relays the withHuman MCP gateway over stdio
//
// The hook runs the withhuman CLI's `hook gemini` and prints its verdict:
// {} to let Gemini's own policy decide, or a deny with the reason. The
// extension is connected in one of two ways. Its agent key setting
// (WITHHUMAN_AGENT_TOKEN, with WITHHUMAN_URL) reaches the CLI as AAP's
// AAP_INSTANCE_TOKEN and AAP_BASE_URL. Otherwise `withhuman agent install
// gemini` saved a credential the CLI reads. Until one of them is there, the
// extension is off and tool calls run unchecked.
//
// Gemini runs a call when its hook crashes, times out or exits 1, so every
// path prints a verdict, and an unexpected failure denies and exits 2.
// The CLI runs the release pinned in ../cli.pin, downloaded and checked on
// first use; a withhuman on PATH is the fallback when that fails.

import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, closeSync, existsSync, mkdirSync, openSync, readFileSync, realpathSync, renameSync, writeSync } from "node:fs";
import { homedir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const RUNTIME = "gemini";
const GATEWAY = "withhuman-gateway";
const DEFAULT_URL = "https://app.withhuman.ai";
const DOWNLOADS = process.env.WITHHUMAN_CLI_BASE || "https://downloads.withhuman.ai";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const STATE = join(ROOT, ".withhuman");

const FAIL_CLOSED_TEXT = "withHuman could not get an approval decision. The call was not run.";
const NO_CLI_TEXT = "The withHuman CLI could not be downloaded, so this call was not run. Ask the user to install it: https://withhuman.ai/docs/getting-started/quickstart";
const WINDOWS_TEXT = "withHuman runs on macOS and Linux only, so this call was not run. Ask the user to remove the agent key with `gemini extensions config withhuman`.";
const KEY_REFUSED_TEXT = "withHuman refused this Gemini CLI key. It may have expired or been revoked. Ask the user to update it with `gemini extensions config withhuman`. The call was not run.";
// The start of the CLI's text for a refused credential.
const REFUSED_PREFIX = "The approval provider refused this runtime's credential";

// The CLI gets only these variables from Gemini's environment.
const ENV_NAMES = new Set([
  "PATH", "HOME", "USER", "LOGNAME", "LANG", "TZ", "TMPDIR", "TMP", "TEMP",
  "XDG_CONFIG_HOME", "WITHHUMAN_CONFIG_DIR", "WITHHUMAN_CLI_BASE",
  "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
  "SSL_CERT_FILE", "SSL_CERT_DIR",
]);
const ENV_PREFIXES = ["LC_"];

// A setting's value. Gemini leaves a placeholder it could not fill as
// written, which counts as unset.
function setting(env, name) {
  const value = (env[name] || "").trim();
  return value.startsWith("${") ? "" : value;
}

export function key(env = process.env) {
  return setting(env, "WITHHUMAN_AGENT_TOKEN");
}

export function origin(env = process.env) {
  return (setting(env, "WITHHUMAN_URL") || DEFAULT_URL).replace(/\/+$/, "");
}

// The CLI's configuration directory, as Go's os.UserConfigDir finds it.
export function cliConfigDir(env = process.env, platform = process.platform) {
  if (env.WITHHUMAN_CONFIG_DIR) return env.WITHHUMAN_CONFIG_DIR;
  const home = env.HOME || homedir();
  if (platform === "darwin") return join(home, "Library", "Application Support", "withhuman");
  return join(env.XDG_CONFIG_HOME || join(home, ".config"), "withhuman");
}

// Whether the extension is connected: an agent key in its settings, or a
// credential `withhuman agent install gemini` saved.
export function connected(env = process.env) {
  return key(env) !== "" || existsSync(join(cliConfigDir(env), "aap", "credentials", `${RUNTIME}.json`));
}

export function cliEnv(env = process.env) {
  const out = {};
  for (const [name, value] of Object.entries(env)) {
    if (ENV_NAMES.has(name) || ENV_PREFIXES.some((prefix) => name.startsWith(prefix))) out[name] = value;
  }
  return out;
}

// The pinned release: its version and one SHA-256 per platform.
export function pin(text = readFileSync(join(ROOT, "plugins", "cli.pin"), "utf8")) {
  const out = { version: "", sha256: {} };
  for (const line of text.split("\n")) {
    const [name, value] = line.trim().split(/\s+/);
    if (!name || name.startsWith("#") || !value) continue;
    if (name === "version") out.version = value;
    else out.sha256[name] = value;
  }
  return out;
}

export function platformName(platform = process.platform, arch = process.arch) {
  const cpu = { x64: "amd64", arm64: "arm64" }[arch] || arch;
  return `${platform}-${cpu}`;
}

// The CLI to run: the pinned copy, downloaded on first use, else one on PATH.
export async function findCLI(env = process.env) {
  const release = pin();
  const pinned = join(STATE, "cli", release.version, "withhuman");
  if (existsSync(pinned)) return pinned;
  try {
    await download(release, pinned);
    return pinned;
  } catch (error) {
    process.stderr.write(`withhuman: could not download the CLI: ${error.message}\n`);
  }
  for (const dir of (env.PATH || "").split(delimiter)) {
    const candidate = join(dir, "withhuman");
    if (dir && existsSync(candidate)) return candidate;
  }
  return null;
}

async function download(release, target) {
  const name = platformName();
  const expected = release.sha256[name];
  if (!release.version || !expected) throw new Error(`no release pinned for ${name}`);
  const response = await fetch(`${DOWNLOADS}/cli/${release.version}/withhuman-${name}`, { redirect: "error", signal: AbortSignal.timeout(60_000) });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  const body = Buffer.from(await response.arrayBuffer());
  if (createHash("sha256").update(body).digest("hex") !== expected) throw new Error("checksum mismatch");
  mkdirSync(dirname(target), { recursive: true });
  // Another hook may be downloading too: write aside, then rename over.
  const partial = `${target}.${process.pid}.partial`;
  const fd = openSync(partial, "w", 0o755);
  try {
    writeSync(fd, body);
  } finally {
    closeSync(fd);
  }
  chmodSync(partial, 0o755);
  renameSync(partial, target);
}

// The CLI's environment for this connection.
export function connectionEnv(env = process.env) {
  const out = cliEnv(env);
  const agentKey = key(env);
  if (agentKey) {
    out.AAP_INSTANCE_TOKEN = agentKey;
    out.AAP_BASE_URL = `${origin(env)}/api/aap`;
    out.AAP_CONFIG_DIR = join(STATE, "aap");
  }
  return out;
}

// The verdict Gemini reads from what the CLI printed.
export function verdict(stdout, usingKey) {
  let parsed;
  try {
    parsed = JSON.parse(stdout);
  } catch {
    return deny(FAIL_CLOSED_TEXT);
  }
  if (parsed && typeof parsed === "object" && !Array.isArray(parsed) && Object.keys(parsed).length === 0) return {};
  if (parsed?.decision === "deny" && typeof parsed.reason === "string" && parsed.reason) {
    if (usingKey && parsed.reason.startsWith(REFUSED_PREFIX)) return deny(KEY_REFUSED_TEXT);
    return deny(parsed.reason);
  }
  return deny(FAIL_CLOSED_TEXT);
}

function deny(reason) {
  return { decision: "deny", reason };
}

function run(binary, args, env, input) {
  return new Promise((resolve) => {
    let child;
    try {
      child = spawn(binary, args, { env, stdio: ["pipe", "pipe", "inherit"] });
    } catch {
      resolve(null);
      return;
    }
    let stdout = "";
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk) => (stdout += chunk));
    child.on("error", () => resolve(null));
    child.on("close", () => resolve(stdout));
    child.stdin.on("error", () => {});
    child.stdin.end(input);
  });
}

async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  return Buffer.concat(chunks).toString("utf8");
}

export async function hook(env = process.env) {
  const input = await readStdin();
  if (!connected(env)) return {};
  if (process.platform === "win32") return deny(WINDOWS_TEXT);
  const binary = await findCLI(env);
  if (!binary) return deny(NO_CLI_TEXT);
  const stdout = await run(binary, ["hook", RUNTIME], connectionEnv(env), input);
  return stdout === null ? deny(FAIL_CLOSED_TEXT) : verdict(stdout, key(env) !== "");
}

// The gateway relay: the CLI's `mcp` with this connection, over our stdio.
export async function mcp(env = process.env) {
  if (!connected(env)) {
    process.stderr.write("withHuman is not connected. Set the agent key with `gemini extensions config withhuman`.\n");
    return 1;
  }
  const binary = await findCLI(env);
  if (!binary) {
    process.stderr.write(`${NO_CLI_TEXT}\n`);
    return 1;
  }
  const relayEnv = cliEnv(env);
  if (key(env)) {
    relayEnv.WITHHUMAN_AGENT_TOKEN = key(env);
    relayEnv.WITHHUMAN_URL = origin(env);
  }
  return new Promise((resolve) => {
    const child = spawn(binary, ["mcp", RUNTIME, GATEWAY], { env: relayEnv, stdio: "inherit" });
    for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) process.on(signal, () => child.kill(signal));
    child.on("error", () => resolve(1));
    child.on("close", (code) => resolve(code ?? 1));
  });
}

async function main(mode) {
  if (mode === "hook") {
    try {
      process.stdout.write(`${JSON.stringify(await hook())}\n`);
      return 0;
    } catch (error) {
      process.stderr.write(`withhuman: ${error?.message ?? error}\n`);
      process.stdout.write(`${JSON.stringify(deny(FAIL_CLOSED_TEXT))}\n`);
      return 2;
    }
  }
  if (mode === "mcp") return mcp();
  process.stderr.write("usage: launcher.mjs hook|mcp\n");
  return 2;
}

// Run as a script, not imported by the tests. Node resolves this module's
// own path through symlinks (a linked extension, macOS's /var), so the
// script path is compared the same way.
if (process.argv[1] && realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = await main(process.argv[2]);
}
