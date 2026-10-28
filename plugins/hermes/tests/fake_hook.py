"""A stand-in for `withhuman hook hermes`. FAKE_HOOK_MODE picks the answer;
the payload and AAP_CONFIG_DIR it saw are written to FAKE_HOOK_LOG."""

import json
import os
import signal
import sys
import time

if sys.argv[1:] == ["version"]:
    print(os.environ.get("FAKE_CLI_VERSION", "withhuman 1.0.43"))
    sys.exit(0)

payload = json.loads(sys.stdin.read())
with open(os.environ["FAKE_HOOK_LOG"], "w", encoding="utf-8") as log:
    json.dump({"payload": payload, **{name: os.environ.get(name) for name in
                                      ("AAP_CONFIG_DIR", "AAP_INSTANCE_TOKEN", "AAP_BASE_URL")}}, log)

mode = os.environ.get("FAKE_HOOK_MODE", "approve")


def answer(verdict):
    print(json.dumps(verdict), flush=True)
    sys.exit(0)


def withdraw(*_args):
    answer({"action": "block", "message": "Withdrawn before anyone decided."})


if mode == "approve":
    answer({"action": "modify", "args": payload["tool_input"]})
elif mode == "deny":
    answer({"action": "block", "message": "Denied by a reviewer."})
elif mode == "refused":
    answer({"action": "block", "message": "The approval provider refused this runtime's credential, so no tool call can run. The call was not run."})
elif mode == "garbage":
    print("not json")
elif mode == "slow":
    time.sleep(float(os.environ["FAKE_HOOK_SLEEP"]))
    answer({"action": "modify", "args": payload["tool_input"]})
elif mode == "wait":
    signal.signal(signal.SIGTERM, withdraw)
    while True:
        time.sleep(0.1)
elif mode == "stubborn":
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    while True:
        time.sleep(0.1)
