// `openclaw withhuman setup` and `openclaw withhuman test`.
//
// Setup connects this OpenClaw to a withHuman agent, by browser sign-in or a
// pasted agent key, saves the key and the address in the plugin's settings,
// adds withHuman's MCP servers, installs the withHuman skills and sends a
// test request. Sign-in is the withhuman CLI's enrollment: the person
// confirms a code in the browser and withHuman issues an instance
// credential for the agent they pick.

import { createHash, randomUUID } from "node:crypto";
import { chmodSync, createWriteStream, mkdirSync, mkdtempSync, renameSync, rmSync } from "node:fs";
import { hostname, platform as osPlatform, arch as osArch } from "node:os";
import { join } from "node:path";
import { Writable } from "node:stream";
import { pipeline } from "node:stream/promises";
import { createInterface } from "node:readline/promises";
import { execFile } from "node:child_process";

import { DEFAULT_URL, cliEnv, downloadedCLI, findCLI, keyConnection, runHook } from "./hook.js";

export const INSTALL_DOCS = "https://withhuman.ai/docs/web-app/agents#cli-enrollment";
// The CLI setup downloads when none is installed or the installed one is
// older. The version and its hashes are pinned here, so the copy that runs
// is the one reviewed with the plugin; change them together.
export const CLI_RELEASE = {
  version: "1.0.57",
  downloads: "https://downloads.withhuman.ai/cli",
  sha256: {
    "darwin-amd64": "7280b92ba39f507ea19e5dd19334ba74b1a048af49112a17e7a828eb6a65c683",
    "darwin-arm64": "b82eb81ce7631439a29b7d5d17ee65247cb34502881f513e16cc7e26e82561c5",
    "linux-amd64": "8439565f5a8b31537fa77ea99e1b74f7a099840884a05d0008e1a24bdf8b34cd",
    "linux-arm64": "78f68dd942dcb850f22b07d1a8823cae04b187bd6a02601c23f81199905cbb25",
  },
};
// The gateway holds a call waiting for a person for five minutes, then
// answers; OpenClaw waits a little longer so that answer arrives.
export const MCP_TIMEOUT_SECONDS = 600;
const TEST_CEILING_MS = 10 * 60 * 1000;
const SIGN_IN_MS = 15 * 60 * 1000;
const POLL_MS = 3_000;
// Cloudflare in front of withHuman refuses some default user agents.
export const USER_AGENT = "withhuman-openclaw-plugin";

export class SetupError extends Error {}
class PendingError extends Error {}

/** A withHuman address: an HTTPS origin, or HTTP on this machine. */
export function origin(value) {
  let url;
  try {
    url = new URL(value.trim());
  } catch {
    throw new SetupError("The withHuman address must look like https://app.withhuman.ai.");
  }
  const local = ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
  if (!url.hostname || url.username || url.password || url.search || url.hash || !["", "/"].includes(url.pathname) ||
      !(url.protocol === "https:" || (url.protocol === "http:" && local))) {
    throw new SetupError("The withHuman address must look like https://app.withhuman.ai.");
  }
  return url.origin;
}

/**
 * Run setup. host is what setup needs from OpenClaw: the saved settings,
 * where to keep files, and how to save settings, add MCP servers and
 * install skills. io is the terminal.
 */
export async function setup(host, io, { url, agent } = {}) {
  let binary;
  try {
    binary = await ensureCLI(host.dataDir, io, host.release);
  } catch (error) {
    if (error instanceof SetupError) {
      io.print(error.message);
      return 1;
    }
    throw error;
  }
  const current = host.settings().url || DEFAULT_URL;
  let address, key, described;
  try {
    const shown = current === DEFAULT_URL ? "cloud" : current;
    address = origin(url || (await io.ask(`withHuman address (default is ${shown}): `)) || current);
    io.print("Connect OpenClaw to withHuman:");
    io.print("  1. Sign in with your browser");
    io.print("  2. Paste an agent key");
    const choice = (await io.ask("Choose 1 or 2 [1]: ")) || "1";
    if (choice === "1") [key, described] = await signIn(address, agent, io);
    else if (choice === "2") [key, described] = await pasteKey(address, io);
    else {
      io.print("Choose 1 or 2.");
      return 1;
    }
  } catch (error) {
    if (error instanceof SetupError) {
      io.print(error.message);
      return 1;
    }
    if (error?.code === "ABORT_ERR" || error?.name === "AbortError") {
      io.print("\nSetup cancelled. Nothing was changed.");
      return 1;
    }
    throw error;
  }

  await host.saveSettings({ url: address, agentKey: key });
  io.print(`Connected to withHuman as ${described}.`);
  await addMCPServers(host, io, address, key);
  if (yes(await io.ask(`Install the withHuman skills (${host.skills.join(", ")})? [Y/n]: `))) {
    await host.installSkills(io);
  }
  io.print("");
  if (yes(await io.ask("Send a test request? [Y/n]: "))) {
    return test(keyConnection(binary, key, address, host.dataDir), io);
  }
  return 0;
}

function yes(answer) {
  return (answer || "y").toLowerCase().startsWith("y");
}

/** Send a test request and report the decision. */
export async function test(connection, io) {
  io.print("Sent a test request to withHuman. If your rules hold it, approve it there.");
  io.print("Waiting for the decision. Press Ctrl-C to stop.");
  const attempt = randomUUID();
  const controller = new AbortController();
  const stop = () => controller.abort();
  process.once("SIGINT", stop);
  let verdict;
  try {
    verdict = await runHook(connection, {
      tool_name: "withhuman_test",
      params: { note: "Test request from openclaw withhuman setup. Nothing runs." },
      tool_call_id: attempt,
      session_id: "withhuman-test-" + attempt,
      cwd: process.cwd(),
      timeout_ms: TEST_CEILING_MS,
    }, { signal: controller.signal, ceilingMs: TEST_CEILING_MS });
  } finally {
    process.removeListener("SIGINT", stop);
  }
  if (controller.signal.aborted) {
    io.print("\nStopped. The test request was withdrawn.");
    return 1;
  }
  if (verdict.decision === "allow") {
    io.print("Approved. withHuman is working.");
    return 0;
  }
  io.print(verdict.reason || "The test request was not approved.");
  return 1;
}

async function signIn(address, agent, io) {
  const host = hostname() || "openclaw";
  const started = await post(address + "/auth/agent_enrollments", {
    runtime: "openclaw",
    requested_agent_slug: agent || "",
    instance_name: `${host}-${randomUUID().replaceAll("-", "").slice(0, 8)}`,
    instance_metadata: { hostname: host, os: osPlatform() },
  });
  const code = started.user_code;
  const uri = started.verification_uri;
  if (!code || uri !== address + "/connect/authorize" || !started.enrollment_code) {
    throw new SetupError("withHuman returned an unexpected answer. Check the address and try again.");
  }
  io.print(`Open ${uri}?user_code=${encodeURIComponent(code)}`);
  io.print(`and confirm the code ${code}. Waiting for you to connect the agent...`);
  const deadline = Date.now() + SIGN_IN_MS;
  while (Date.now() < deadline) {
    await io.sleep(POLL_MS);
    let issued;
    try {
      issued = await post(address + "/auth/agent_enrollments/token", { enrollment_code: started.enrollment_code });
    } catch (error) {
      if (error instanceof PendingError) continue;
      throw error;
    }
    const key = issued.credential?.token;
    if (!key) throw new SetupError("withHuman returned no agent key. Try again.");
    const name = issued.agent?.name || issued.agent?.slug || "your agent";
    const instance = issued.agent_instance?.name || "";
    return [key, instance ? `agent "${name}" (instance ${instance})` : `agent "${name}"`];
  }
  throw new SetupError("The sign-in expired. Run `openclaw withhuman setup` again.");
}

async function pasteKey(address, io) {
  io.print("Get an agent key in withHuman: Agents, your agent, New instance, Generate an API key.");
  const key = (await io.askSecret("Agent key: ")).trim();
  if (!key) throw new SetupError("No key entered. Nothing was changed.");
  let response;
  try {
    response = await request(address + "/api/v1/agent_instances/self", { headers: { Authorization: "Bearer " + key } });
  } catch {
    throw new SetupError(`Could not reach withHuman at ${address}.`);
  }
  if (response.status === 401 || response.status === 403) {
    throw new SetupError("withHuman didn't accept that key. Use an agent key, not a personal API key.");
  }
  if (!response.ok) throw new SetupError(`withHuman answered HTTP ${response.status}. Check the address and try again.`);
  const identity = await json(response, address);
  return [key, `agent "${identity.agent_slug || "unknown"}"`];
}

/**
 * Add the MCP servers withHuman offers its agents (hosted withHuman's
 * gateway) as remote servers that send this plugin's key. Only servers on
 * the withHuman address's own origin are added, so the key goes nowhere
 * else.
 */
async function addMCPServers(host, io, address, key) {
  let offered;
  try {
    offered = (await get(address + "/api/runtime-config")).agent_mcp_servers || [];
  } catch {
    io.print("Could not read which MCP servers withHuman offers. Run setup again to add them.");
    return;
  }
  const servers = offered.filter((server) => typeof server?.name === "string" && typeof server?.url === "string" &&
    server.url.startsWith(address + "/"));
  if (servers.length === 0) return;
  await host.addMCPServers(servers.map((server) => ({ name: server.name, url: server.url, timeoutSeconds: MCP_TIMEOUT_SECONDS })), key);
  for (const server of servers) io.print(`Added ${server.label || server.name} as the MCP server ${server.name}.`);
}

// fetch without following redirects: a redirect could carry a key to
// another host.
function request(url, init = {}) {
  return fetch(url, {
    ...init,
    redirect: "manual",
    signal: AbortSignal.timeout(30_000),
    headers: { "User-Agent": USER_AGENT, ...init.headers },
  });
}

async function json(response, address) {
  try {
    const body = await response.json();
    return body && typeof body === "object" ? body : {};
  } catch {
    throw new SetupError(`Could not reach withHuman at ${address}.`);
  }
}

async function get(url) {
  let response;
  try {
    response = await request(url);
  } catch {
    throw new SetupError(`Could not reach withHuman at ${url}.`);
  }
  if (!response.ok) throw new SetupError(`withHuman answered HTTP ${response.status}.`);
  return json(response, url);
}

async function post(url, body) {
  const address = url.split("/auth/")[0];
  let response;
  try {
    response = await request(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  } catch {
    throw new SetupError(`Could not reach withHuman at ${address}.`);
  }
  if (response.status === 404 && url.endsWith("/token")) throw new PendingError();
  if (!response.ok) throw new SetupError(`withHuman answered HTTP ${response.status}. Check the address and try again.`);
  return json(response, address);
}

/**
 * The CLI to use, downloading the pinned one when none is installed or the
 * installed one is older.
 */
export async function ensureCLI(dataDir, io, release = CLI_RELEASE) {
  const binary = findCLI(dataDir);
  if (binary && !older(await cliVersion(binary), release.version)) return binary;
  if (binary) io.print(`The withHuman CLI at ${binary} is older than ${release.version}.`);
  return downloadCLI(dataDir, io.print, release);
}

/**
 * Replace the CLI setup downloaded when the plugin now pins a newer one, as
 * after `openclaw plugins update`, and download it when a connected plugin
 * has none. A CLI the person installed themselves is theirs to upgrade.
 */
export async function refreshDownloadedCLI(dataDir, { connected = false, say, release = CLI_RELEASE } = {}) {
  const path = downloadedCLI(dataDir);
  if (findCLI(dataDir) === path) {
    if (older(await cliVersion(path), release.version)) await downloadCLI(dataDir, say, release);
  } else if (connected && findCLI(dataDir) === null) {
    await downloadCLI(dataDir, say, release);
  }
}

export async function downloadCLI(dataDir, say = () => {}, release = CLI_RELEASE) {
  const target = platformName();
  if (!release.sha256[target]) {
    throw new SetupError(`Setup can't download the withHuman CLI for ${target}. ` +
      `Install it yourself, then run setup again: ${INSTALL_DOCS}`);
  }
  const url = `${release.downloads}/${release.version}/withhuman-${target}`;
  say(`Downloading the withHuman CLI ${release.version} for ${target} from ${url}`);
  const path = downloadedCLI(dataDir);
  mkdirSync(join(dataDir, "bin"), { recursive: true });
  const partialDir = mkdtempSync(join(dataDir, "bin", ".withhuman-"));
  const partial = join(partialDir, "withhuman");
  try {
    let response;
    try {
      response = await fetch(url, { redirect: "manual", signal: AbortSignal.timeout(120_000), headers: { "User-Agent": USER_AGENT } });
    } catch (error) {
      throw new SetupError(`Could not download the withHuman CLI (${error.message}). ` +
        `Install it yourself, then run setup again: ${INSTALL_DOCS}`);
    }
    if (!response.ok || !response.body) {
      throw new SetupError(`Could not download the withHuman CLI (HTTP ${response.status}). ` +
        `Install it yourself, then run setup again: ${INSTALL_DOCS}`);
    }
    const digest = createHash("sha256");
    const file = createWriteStream(partial, { mode: 0o600 });
    const hashing = new Writable({
      write(chunk, _encoding, done) {
        digest.update(chunk);
        file.write(chunk, done);
      },
      final(done) {
        file.end(done);
      },
    });
    await pipeline(response.body, hashing);
    if (digest.digest("hex") !== release.sha256[target]) {
      throw new SetupError("The downloaded withHuman CLI didn't match its checksum, so setup didn't use it. Try again later.");
    }
    chmodSync(partial, 0o755);
    renameSync(partial, path);
  } finally {
    rmSync(partialDir, { recursive: true, force: true });
  }
  say(`Checked it and saved it to ${path}`);
  return path;
}

export function platformName() {
  const arch = { x64: "amd64", arm64: "arm64" }[osArch()] ?? osArch();
  return `${osPlatform()}-${arch}`;
}

/** The released version a CLI reports, or null for a development build. */
export function cliVersion(binary) {
  return new Promise((resolve) => {
    execFile(binary, ["version"], { timeout: 10_000, env: cliEnv() }, (error, stdout) => {
      const match = error ? null : /(\d+)\.(\d+)\.(\d+)/.exec(stdout);
      resolve(match ? match.slice(1).map(Number) : null);
    });
  });
}

/**
 * Whether a released version is older than minimum. A development build
 * has no version and counts as new enough.
 */
export function older(version, minimum) {
  if (!version) return false;
  const want = minimum.split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    if (version[i] !== want[i]) return version[i] < want[i];
  }
  return false;
}

/** The terminal: questions on stdin, answers and messages on stdout. */
export function terminal({ input = process.stdin, output = process.stdout } = {}) {
  let muted = false;
  const mutable = new Writable({
    write(chunk, encoding, done) {
      if (!muted) output.write(chunk, encoding);
      done();
    },
  });
  const rl = createInterface({ input, output: mutable, terminal: Boolean(input.isTTY) });
  // Ctrl-C or the end of input cancels the question being asked.
  const cancel = new AbortController();
  rl.on("SIGINT", () => cancel.abort());
  rl.on("close", () => cancel.abort());
  const question = (prompt) => rl.question(prompt, { signal: cancel.signal });
  return {
    print: (line) => output.write(line + "\n"),
    ask: async (prompt) => (await question(prompt)).trim(),
    askSecret: async (prompt) => {
      output.write(prompt);
      muted = true;
      try {
        return await question("");
      } finally {
        muted = false;
        output.write("\n");
      }
    },
    sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
    close: () => rl.close(),
  };
}
