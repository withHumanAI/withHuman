// A stand-in for `withhuman hook openclaw`. FAKE_HOOK_MODE picks the
// answer; the payload, the AAP variables and the names of every variable it
// saw are written to FAKE_HOOK_LOG. The plugin gives the CLI a minimal
// environment, so the FAKE_* settings come from a file whose path the tests
// bake into each copy of the fake CLI.

import { existsSync, readFileSync, writeFileSync } from "node:fs";

const settingsFile = globalThis.FAKE_HOOK_SETTINGS ?? "fake-hook.json";
const settings = existsSync(settingsFile) ? JSON.parse(readFileSync(settingsFile, "utf8")) : {};
const setting = (name, fallback) => settings[name] ?? fallback;

if (process.argv[2] === "version") {
  console.log(setting("FAKE_CLI_VERSION", "withhuman 1.0.57"));
  process.exit(0);
}

const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const payload = JSON.parse(Buffer.concat(chunks).toString("utf8"));
const log = setting("FAKE_HOOK_LOG");
writeFileSync(log, JSON.stringify({
  payload,
  argv: process.argv.slice(2),
  env: Object.keys(process.env).sort(),
  AAP_CONFIG_DIR: process.env.AAP_CONFIG_DIR,
  AAP_INSTANCE_TOKEN: process.env.AAP_INSTANCE_TOKEN,
  AAP_BASE_URL: process.env.AAP_BASE_URL,
}));

const answer = (verdict) => {
  process.stdout.write(JSON.stringify(verdict) + "\n", () => process.exit(0));
};
const later = new Date(Date.now() + 60_000).toISOString();

switch (setting("FAKE_HOOK_MODE", "approve")) {
  case "approve":
    answer({ decision: "allow", gated: true, expires_at: later });
    break;
  case "ungated":
    answer({ decision: "allow", gated: false });
    break;
  case "stale":
    answer({ decision: "allow", gated: true, expires_at: new Date(Date.now() - 1000).toISOString() });
    break;
  case "deny":
    answer({ decision: "deny", reason: "Denied by a reviewer." });
    break;
  case "refused":
    answer({ decision: "deny", reason: "The approval provider refused this runtime's credential, so no tool call can run. The call was not run." });
    break;
  case "expired":
    answer({ decision: "deny", reason: "The approval window or permission to start this call expired. The call was not run." });
    break;
  case "garbage":
    console.log("not json");
    break;
  case "slow":
    setTimeout(() => answer({ decision: "allow", gated: true, expires_at: later }), setting("FAKE_HOOK_SLEEP_MS"));
    break;
  case "wait":
    process.on("SIGTERM", () => {
      writeFileSync(log + ".withdrawn", "");
      answer({ decision: "deny", reason: "Withdrawn before anyone decided." });
    });
    setInterval(() => {}, 100);
    break;
  case "stubborn":
    process.on("SIGTERM", () => {});
    setInterval(() => {}, 100);
    break;
}
