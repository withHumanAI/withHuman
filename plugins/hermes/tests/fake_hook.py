"""A stand-in for `withhuman hook hermes`. FAKE_HOOK_MODE picks the answer;
the payload, the AAP variables and the names of every variable it saw are
written to FAKE_HOOK_LOG. The plugin gives the CLI a minimal environment, so
the FAKE_* settings come from a file the tests write."""

import json
import os
import signal
import sys
import time
from pathlib import Path

# The tests write this file and bake its path into each copy of the fake CLI.
settings_file = Path(globals().get("FAKE_HOOK_SETTINGS", "fake-hook.json"))
settings = json.loads(settings_file.read_text(encoding="utf-8")) if settings_file.exists() else {}


def setting(name, default=None):
    return settings.get(name, default)


if sys.argv[1:] == ["version"]:
    print(setting("FAKE_CLI_VERSION", "withhuman 1.0.43"))
    sys.exit(0)

payload = json.loads(sys.stdin.read())
log_path = setting("FAKE_HOOK_LOG")
with open(log_path, "w", encoding="utf-8") as log:
    json.dump({"payload": payload, "env": sorted(os.environ),
               **{name: os.environ.get(name) for name in ("AAP_CONFIG_DIR", "AAP_INSTANCE_TOKEN", "AAP_BASE_URL")}},
              log)

mode = setting("FAKE_HOOK_MODE", "approve")


def answer(verdict):
    print(json.dumps(verdict), flush=True)
    sys.exit(0)


def withdraw(*_args):
    Path(log_path + ".withdrawn").touch()
    answer({"action": "block", "message": "Withdrawn before anyone decided."})


if mode == "approve":
    answer({"action": "modify", "args": payload["tool_input"]})
elif mode == "deny":
    answer({"action": "block", "message": "Denied by a reviewer."})
elif mode == "refused":
    answer({"action": "block", "message": "The approval provider refused this runtime's credential, so no tool call can run. The call was not run."})
elif mode == "garbage":
    print("not json")
elif mode == "expired":
    answer({"action": "block", "message": "The approval window or permission to start this call expired. "
                                          "The call was not run."})
elif mode == "slow":
    time.sleep(float(setting("FAKE_HOOK_SLEEP")))
    answer({"action": "modify", "args": payload["tool_input"]})
elif mode == "wait":
    signal.signal(signal.SIGTERM, withdraw)
    while True:
        time.sleep(0.1)
elif mode == "stubborn":
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    while True:
        time.sleep(0.1)
