"""``hermes withhuman setup`` and ``hermes withhuman test``.

Setup connects this Hermes profile to a withHuman agent, by browser sign-in
or a pasted agent key, saves the key in the profile's secrets and the
address in the plugin's settings, offers to turn off Hermes's hook callback
timeout, and sends a test request. Sign-in is the withhuman CLI's enrollment: the
person confirms a code in the browser and withHuman issues an instance
credential for the agent they pick.
"""

from __future__ import annotations

import getpass
import hashlib
import json
import logging
import os
import platform
import re
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

from . import (DEFAULT_URL, KEY_ENV, cli_env, current_connection, downloaded_cli, find_cli, hermes_key_connection,
               hook_limit, run_hook)

logger = logging.getLogger(__name__)

INSTALL_DOCS = "https://withhuman.ai/docs/web-app/agents#cli-enrollment"
# The CLI setup downloads when none is installed or the installed one is
# older. The version and its hashes are pinned here, so the copy that runs is
# the one reviewed with the plugin; change them together.
CLI_VERSION = "1.0.43"
CLI_SHA256 = {
    "darwin-amd64": "43e9cd9b34a86033424fc8181a4cac00c96ec6d6b98c94d505c8ec2688c43eeb",
    "darwin-arm64": "783c94f88a8f7f23d80b506802930d75748be819d45c2db474a57bc07bf37898",
    "linux-amd64": "dfc1141e545fdc2a9f2d46e6ae7829dea8314119762c0db2877e90d63d2f648e",
    "linux-arm64": "a39304e0dc28a1c80c3518b82e196f9411cb2939cf596c5e135a774b6c99e99e",
}
CLI_DOWNLOADS = "https://downloads.withhuman.ai/cli"
# withhuman.ai publishes core's skills in the well-known format Hermes reads.
SKILLS_URL = "https://withhuman.ai/.well-known/skills"
SKILLS = ("withhuman", "withhuman-pipelines")
# The gateway holds a call waiting for a person for five minutes, then
# answers; Hermes waits a little longer so that answer arrives.
MCP_TIMEOUT_SECONDS = 600
TEST_CEILING_SECONDS = 10 * 60


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
    try:
        binary = _ensure_cli()
    except SetupError as exc:
        print(exc)
        return 1
    saved = ctx.get_config("url")
    current = saved if isinstance(saved, str) and saved else DEFAULT_URL
    try:
        shown = "cloud" if current == DEFAULT_URL else current
        origin = _origin(url or _ask(f"withHuman address (default is {shown}): ") or current)
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
    _add_mcp_servers(origin)
    if (_ask(f"Install the withHuman skills ({', '.join(SKILLS)})? [Y/n]: ") or "y").lower().startswith("y"):
        _install_skills()
    limit = hook_limit()
    if limit:
        print()
        print(f"Hermes stops waiting for a plugin's hook after {limit} seconds. "
              f"With that limit on, each request must be approved within {limit} seconds.")
        print("Turning the limit off is highly recommended. It applies to every plugin's hooks.")
        prompt = "Turn the limit off for this Hermes profile? [Y/n]: "
        if (_ask(prompt) or "y").lower().startswith("y"):
            _save_setting("plugins.hook_callback_timeout", "0")
        else:
            print(f"Kept the limit. Calls that wait for a person are blocked after {limit} seconds.")
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


def _add_mcp_servers(origin):
    """Add the MCP servers withHuman offers its agents (hosted withHuman's
    gateway) as remote servers that send this profile's key. Only servers on
    the withHuman address's own host are added, so the key goes nowhere else."""
    try:
        offered = _get(origin + "/api/runtime-config").get("agent_mcp_servers") or []
    except SetupError:
        print("Could not read which MCP servers withHuman offers. Run setup again to add them.")
        return
    from hermes_cli.mcp_config import _save_mcp_server

    for server in offered:
        name, url = server.get("name"), server.get("url")
        if not isinstance(name, str) or not isinstance(url, str) or not url.startswith(origin + "/"):
            continue
        entry = {"url": url, "headers": {"Authorization": "Bearer ${" + KEY_ENV + "}"},
                 "timeout": MCP_TIMEOUT_SECONDS}
        if _save_mcp_server(name, entry):
            print(f"Added {server.get('label') or name} as the MCP server {name}.")


def _install_skills():
    """Install the skills through Hermes's own skill installer, which scans
    them and records where they came from, so `hermes skills update` keeps
    them current. A skill already installed is left as it is."""
    from hermes_cli.skills_hub import do_install

    for name in SKILLS:
        try:
            installed = do_install(f"well-known:{SKILLS_URL}/{name}", skip_confirm=True)
        except Exception as exc:  # Hermes's installer raises on network and scan errors
            installed = False
            print(f"Could not install the {name} skill: {exc}")
        if installed is False:
            print(f"Install it later with `hermes skills install well-known:{SKILLS_URL}/{name}`.")


def _get(url):
    try:
        with _opener().open(url, timeout=30) as response:
            body = json.loads(response.read())
    except urllib.error.HTTPError as exc:
        raise SetupError(f"withHuman answered HTTP {exc.code}.") from None
    except (OSError, ValueError):
        raise SetupError(f"Could not reach withHuman at {url}.") from None
    return body if isinstance(body, dict) else {}


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


# Cloudflare in front of withHuman refuses Python's default user agent.
USER_AGENT = "withhuman-hermes-plugin"


def _opener():
    opener = urllib.request.build_opener(_NoRedirect)
    opener.addheaders = [("User-Agent", USER_AGENT)]
    return opener


def _origin(value):
    """A withHuman address: an HTTPS origin, or HTTP on this machine."""
    parsed = urllib.parse.urlsplit(value.strip())
    local = parsed.hostname in ("localhost", "127.0.0.1", "::1")
    if not parsed.hostname or parsed.username or parsed.query or parsed.fragment or parsed.path not in ("", "/") \
            or not (parsed.scheme == "https" or (parsed.scheme == "http" and local)):
        raise SetupError("The withHuman address must look like https://app.withhuman.ai.")
    return f"{parsed.scheme}://{parsed.netloc}"


def _ensure_cli():
    """The CLI to use, downloading the pinned one when none is installed or
    the installed one is older."""
    binary = find_cli()
    if binary and not _older(_cli_version(binary), CLI_VERSION):
        return binary
    if binary:
        print(f"The withHuman CLI at {binary} is older than {CLI_VERSION}.")
    return download_cli()


def refresh_downloaded_cli(connected=False):
    """Replace the CLI setup downloaded when the plugin now pins a newer
    one, as after `hermes plugins update`, and download it when a connected
    plugin has none. A CLI the person installed themselves is theirs to
    upgrade."""
    path = downloaded_cli()
    if path.is_file():
        if _older(_cli_version(str(path)), CLI_VERSION):
            download_cli(say=logger.info)
    elif connected and find_cli() is None:
        download_cli(say=logger.info)


def download_cli(say=print):
    target = platform_name()
    if target not in CLI_SHA256:
        raise SetupError(f"Setup can't download the withHuman CLI for {target}. "
                         f"Install it yourself, then run setup again: {INSTALL_DOCS}")
    url = f"{CLI_DOWNLOADS}/{CLI_VERSION}/withhuman-{target}"
    say(f"Downloading the withHuman CLI {CLI_VERSION} for {target} from {url}")
    path = downloaded_cli()
    path.parent.mkdir(parents=True, exist_ok=True)
    digest = hashlib.sha256()
    fd, partial = tempfile.mkstemp(dir=path.parent, prefix=".withhuman-")
    try:
        with os.fdopen(fd, "wb") as out, _opener().open(url, timeout=60) as response:
            while chunk := response.read(1 << 16):
                digest.update(chunk)
                out.write(chunk)
        if digest.hexdigest() != CLI_SHA256[target]:
            raise SetupError("The downloaded withHuman CLI didn't match its checksum, so setup didn't use it. "
                             "Try again later.")
        os.chmod(partial, 0o755)
        os.replace(partial, path)
    except (OSError, urllib.error.URLError) as exc:
        raise SetupError(f"Could not download the withHuman CLI ({exc}). "
                         f"Install it yourself, then run setup again: {INSTALL_DOCS}") from None
    finally:
        if os.path.exists(partial):
            os.unlink(partial)
    say(f"Checked it and saved it to {path}")
    return str(path)


def platform_name():
    system = platform.system().lower()
    machine = platform.machine().lower()
    arch = {"x86_64": "amd64", "amd64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(machine, machine)
    return f"{system}-{arch}"


def _cli_version(binary):
    try:
        out = subprocess.run([binary, "version"], capture_output=True, text=True, timeout=10, env=cli_env()).stdout
    except (OSError, subprocess.SubprocessError):
        return None
    match = re.search(r"(\d+)\.(\d+)\.(\d+)", out)
    return tuple(int(part) for part in match.groups()) if match else None


def _older(version, minimum):
    """Whether a released version is older than minimum. A development build
    has no version and counts as new enough."""
    return version is not None and version < tuple(int(part) for part in minimum.split("."))


def _ask(prompt):
    return input(prompt).strip()


def _save_setting(key, value):
    """Save as `hermes config set` does: upper-case names go to this profile's .env."""
    from hermes_cli.config import set_config_value

    set_config_value(key, value)
