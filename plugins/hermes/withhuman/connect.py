"""``hermes withhuman setup`` and ``hermes withhuman test``.

Setup connects this Hermes profile to a withHuman agent, by browser sign-in
or a pasted agent key, saves the key in the profile's secrets and the
address in the plugin's settings, turns off Hermes's hook callback timeout,
and sends a test request. Sign-in is the withhuman CLI's enrollment: the
person confirms a code in the browser and withHuman issues an instance
credential for the agent they pick.
"""

from __future__ import annotations

import getpass
import json
import os
import platform
import shutil
import socket
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

from . import DEFAULT_URL, KEY_ENV, current_connection, hermes_key_connection, run_hook

INSTALL_DOCS = "https://withhuman.ai/docs/web-app/agents#cli-enrollment"
TEST_CEILING_SECONDS = 10 * 60
TIMEOUT_NOTICE = (
    "Hermes stops waiting for a plugin's hook after 30 seconds, and approvals often take longer.\n"
    "Setup turns that limit off for this Hermes profile. It applies to every plugin's hooks."
)


def setup_parser(parser) -> None:
    actions = parser.add_subparsers(dest="withhuman_action")
    setup = actions.add_parser("setup", help="Connect this Hermes profile to withHuman")
    setup.add_argument("--url", help="withHuman address, for self-hosted withHuman")
    setup.add_argument("--agent", help="agent slug to connect to")
    actions.add_parser("test", help="Send a test request to withHuman")


def run(args, ctx) -> int:
    action = getattr(args, "withhuman_action", None)
    if action == "setup":
        return setup(ctx, getattr(args, "url", None), getattr(args, "agent", None))
    if action == "test":
        connection = current_connection()
        if connection is None:
            print("withHuman isn't set up for this profile. Run `hermes withhuman setup`.")
            return 1
        return test(connection)
    print("usage: hermes withhuman {setup,test}")
    return 1


def setup(ctx, url, agent) -> int:
    binary = _find_cli()
    if not binary:
        print("Install the withHuman CLI first, then run `hermes withhuman setup` again.")
        print(f"How to install it: {INSTALL_DOCS}")
        return 1
    saved = ctx.get_config("url")
    current = saved if isinstance(saved, str) and saved else DEFAULT_URL
    try:
        origin = _origin(url or _ask(f"withHuman address [{current}]: ") or current)
        print("Connect Hermes to withHuman:")
        print("  1. Sign in with your browser")
        print("  2. Paste an agent key")
        choice = _ask("Choose 1 or 2 [1]: ") or "1"
        if choice == "1":
            key, described = _sign_in(origin, agent)
        elif choice == "2":
            key, described = _paste_key(origin)
        else:
            print("Choose 1 or 2.")
            return 1
    except SetupError as exc:
        print(exc)
        return 1
    except (KeyboardInterrupt, EOFError):
        print("\nSetup cancelled. Nothing was changed.")
        return 1

    _save_setting(KEY_ENV, key)
    ctx.set_config("url", origin)
    print(f"Connected to withHuman as {described}.")
    print()
    print(TIMEOUT_NOTICE)
    _save_setting("plugins.hook_callback_timeout", "0")
    print()
    if (_ask("Send a test request? [Y/n]: ") or "y").lower().startswith("y"):
        return test(hermes_key_connection(binary, key, origin))
    return 0


def test(connection) -> int:
    print("Sent a test request to withHuman. If your rules hold it, approve it there.")
    print("Waiting for the decision. Press Ctrl-C to stop.")
    attempt = str(uuid.uuid4())
    payload = {
        "hook_event_name": "pre_tool_call",
        "tool_name": "withhuman_test",
        "tool_input": {"note": "Test request from hermes withhuman setup. Nothing runs."},
        "session_id": "withhuman-test-" + attempt,
        "cwd": os.getcwd(),
        "timeout_ms": TEST_CEILING_SECONDS * 1000,
        "extra": {"tool_call_id": attempt},
    }
    try:
        verdict = run_hook(connection, payload, lambda: False)
    except KeyboardInterrupt:
        # The terminal sent the CLI the same Ctrl-C, and it withdraws the request.
        print("\nStopped. The test request was withdrawn.")
        return 1
    if verdict.get("action") == "modify":
        print("Approved. withHuman is working.")
        return 0
    print(verdict.get("message", "The test request was not approved."))
    return 1


class SetupError(Exception):
    pass


def _sign_in(origin, agent):
    hostname = socket.gethostname() or "hermes"
    started = _post(origin + "/auth/agent_enrollments", {
        "runtime": "hermes",
        "requested_agent_slug": agent or "",
        "instance_name": f"{hostname}-{uuid.uuid4().hex[:8]}",
        "instance_metadata": {"hostname": hostname, "os": platform.system().lower()},
    })
    code, uri = started.get("user_code"), started.get("verification_uri")
    if not code or uri != origin + "/connect/authorize" or not started.get("enrollment_code"):
        raise SetupError("withHuman returned an unexpected answer. Check the address and try again.")
    print(f"Open {uri}?user_code={urllib.parse.quote(code)}")
    print(f"and confirm the code {code}. Waiting for you to connect the agent...")
    deadline = time.monotonic() + 15 * 60
    while time.monotonic() < deadline:
        time.sleep(3)
        try:
            issued = _post(origin + "/auth/agent_enrollments/token", {"enrollment_code": started["enrollment_code"]})
        except PendingError:
            continue
        key = (issued.get("credential") or {}).get("token")
        agent_info = issued.get("agent") or {}
        instance = (issued.get("agent_instance") or {}).get("name", "")
        if not key:
            raise SetupError("withHuman returned no agent key. Try again.")
        name = agent_info.get("name") or agent_info.get("slug") or "your agent"
        return key, f'agent "{name}" (instance {instance})' if instance else f'agent "{name}"'
    raise SetupError("The sign-in expired. Run `hermes withhuman setup` again.")


def _paste_key(origin):
    print("Get an agent key in withHuman: Agents, your agent, New instance, Generate an API key.")
    key = getpass.getpass("Agent key: ").strip()
    if not key:
        raise SetupError("No key entered. Nothing was changed.")
    request = urllib.request.Request(origin + "/api/v1/agent_instances/self",
                                     headers={"Authorization": "Bearer " + key})
    try:
        with _opener().open(request, timeout=30) as response:
            identity = json.loads(response.read())
    except urllib.error.HTTPError as exc:
        if exc.code in (401, 403):
            raise SetupError("withHuman didn't accept that key. Use an agent key, not a personal API key.") from None
        raise SetupError(f"withHuman answered HTTP {exc.code}. Check the address and try again.") from None
    except (OSError, ValueError):
        raise SetupError(f"Could not reach withHuman at {origin}.") from None
    return key, f'agent "{identity.get("agent_slug", "unknown")}"'


class PendingError(Exception):
    pass


def _post(url, body):
    request = urllib.request.Request(url, data=json.dumps(body).encode(), method="POST",
                                     headers={"Content-Type": "application/json"})
    try:
        with _opener().open(request, timeout=30) as response:
            return json.loads(response.read())
    except urllib.error.HTTPError as exc:
        if exc.code == 404 and url.endswith("/token"):
            raise PendingError() from None
        raise SetupError(f"withHuman answered HTTP {exc.code}. Check the address and try again.") from None
    except (OSError, ValueError):
        raise SetupError(f"Could not reach withHuman at {url.split('/auth/')[0]}.") from None


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    # A redirect could carry a key to another host; refuse to follow one.
    def redirect_request(self, *args, **kwargs):
        return None


def _opener():
    return urllib.request.build_opener(_NoRedirect)


def _origin(value):
    """A withHuman address: an HTTPS origin, or HTTP on this machine."""
    parsed = urllib.parse.urlsplit(value.strip())
    local = parsed.hostname in ("localhost", "127.0.0.1", "::1")
    if not parsed.hostname or parsed.username or parsed.query or parsed.fragment or parsed.path not in ("", "/") \
            or not (parsed.scheme == "https" or (parsed.scheme == "http" and local)):
        raise SetupError("The withHuman address must look like https://app.withhuman.ai.")
    return f"{parsed.scheme}://{parsed.netloc}"


def _find_cli():
    from . import _installed

    binary = _installed().get("binary") or shutil.which("withhuman")
    return binary if binary and os.path.exists(binary) else None


def _ask(prompt):
    return input(prompt).strip()


def _save_setting(key, value):
    """Save as `hermes config set` does: upper-case names go to this profile's .env."""
    from hermes_cli.config import set_config_value

    set_config_value(key, value)
