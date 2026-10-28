"""withHuman for Hermes Agent: every tool call waits for withHuman's decision.

``register`` adds one ``pre_tool_call`` hook and the ``hermes withhuman``
command. For each call the hook runs the withhuman CLI's ``hook hermes``
with the payload Hermes gives shell hooks, plus ``timeout_ms``, and returns
the directive the CLI prints: ``modify`` with the approved arguments, or
``block`` with the reason.

The plugin is connected in one of two ways. ``hermes withhuman setup`` keeps
the agent key in this Hermes profile's secrets (``WITHHUMAN_AGENT_TOKEN``)
and the withHuman address in the plugin's settings; the hook passes both to
the CLI. ``withhuman agent install hermes`` instead writes an ``aap.json``
beside this file naming the CLI and its own saved credential. Until one of
them has run, the plugin is off and tool calls run unchecked.

Hermes must not abandon the wait, so setup sets
``plugins.hook_callback_timeout`` to 0. The plugin keeps its own ceiling
instead, and stops the CLI when Hermes interrupts the turn, which withdraws
the request.
"""

from __future__ import annotations

import json
import logging
import os
import shutil
import signal
import subprocess
import threading
from dataclasses import dataclass
from pathlib import Path

logger = logging.getLogger(__name__)

KEY_ENV = "WITHHUMAN_AGENT_TOKEN"
DEFAULT_URL = "https://app.withhuman.ai"

# How long a call can wait for a decision. The CLI expires the request 30
# seconds sooner, so withHuman's expiry reaches the model first.
APPROVAL_CEILING_SECONDS = 7 * 24 * 60 * 60 + 30
# Time the CLI gets to print its verdict after the ceiling or a stop.
EXIT_GRACE_SECONDS = 30
POLL_SECONDS = 0.5

FAIL_CLOSED_TEXT = "withHuman could not get an approval decision. The call was not run."
STOPPED_TEXT = "The tool call was stopped before anyone decided. The call was not run."
NO_CLI_TEXT = ("The withHuman CLI is not installed, so this call was not run. "
               "Ask the user to run `hermes withhuman setup`.")
KEY_REFUSED_TEXT = ("withHuman refused this Hermes key. It may have expired or been revoked. "
                    "Ask the user to run `hermes withhuman setup`. The call was not run.")
# The start of the CLI's text for a refused credential.
_REFUSED_PREFIX = "The approval provider refused this runtime's credential"

# Keys Hermes sends at the top level of a shell hook payload; the rest go in "extra".
_TOP_LEVEL_KEYS = {"tool_name", "args", "session_id"}

_ctx = None


@dataclass(frozen=True)
class Connection:
    """How the hook reaches withHuman: the CLI to run and its environment."""

    binary: str
    env: dict
    # True when the key came from this Hermes profile, not the CLI's own store.
    hermes_key: bool


def register(ctx):
    global _ctx
    _ctx = ctx
    ctx.register_hook("pre_tool_call", pre_tool_call)
    from . import connect

    ctx.register_cli_command(
        name="withhuman", help="Connect Hermes to withHuman",
        setup_fn=connect.setup_parser, handler_fn=lambda args: connect.run(args, ctx),
    )
    # After an update that pins a newer CLI, fetch it without holding up the load.
    threading.Thread(target=_refresh_cli, name="withhuman-cli-refresh", daemon=True).start()


def _refresh_cli():
    from . import connect

    try:
        connect.refresh_downloaded_cli()
    except Exception as exc:  # the old copy keeps working; setup can fetch it later
        logger.warning("withhuman: could not update the withHuman CLI: %s", exc)


def pre_tool_call(tool_name=None, args=None, **kwargs):
    if not connected():
        return None
    connection = current_connection()
    if connection is None:
        return _block(NO_CLI_TEXT)
    payload = {
        "hook_event_name": "pre_tool_call",
        "tool_name": tool_name,
        "tool_input": args if isinstance(args, dict) else None,
        "session_id": kwargs.get("session_id") or kwargs.get("parent_session_id") or "",
        "cwd": _cwd(),
        "timeout_ms": APPROVAL_CEILING_SECONDS * 1000,
        "extra": {key: value for key, value in kwargs.items() if key not in _TOP_LEVEL_KEYS},
    }
    return run_hook(connection, payload, _interrupt_check())


def connected():
    """Whether setup or the withhuman CLI has connected this profile."""
    return bool(_hermes_key()) or bool(_installed())


def current_connection():
    """The connection for this profile, or None when the CLI is missing."""
    installed = _installed()
    binary = find_cli()
    if binary is None:
        return None
    key = _hermes_key()
    if key:
        return hermes_key_connection(binary, key, _setting("url") or DEFAULT_URL)
    env = dict(os.environ)
    if installed.get("configDir"):
        env["AAP_CONFIG_DIR"] = installed["configDir"]
    return Connection(binary=binary, env=env, hermes_key=False)


def find_cli():
    """The withhuman CLI the hook runs: the copy setup downloaded for this
    profile, else the one `withhuman agent install hermes` recorded, else
    the one on PATH."""
    for binary in (str(downloaded_cli()), _installed().get("binary"), shutil.which("withhuman")):
        if binary and os.path.isfile(binary) and os.access(binary, os.X_OK):
            return binary
    return None


def downloaded_cli():
    """Where setup keeps the CLI it downloads, in this profile's plugin data."""
    return _data_dir() / "bin" / "withhuman"


def hermes_key_connection(binary, key, url):
    env = dict(os.environ)
    env["AAP_INSTANCE_TOKEN"] = key
    env["AAP_BASE_URL"] = url.rstrip("/") + "/api/aap"
    env["AAP_CONFIG_DIR"] = str(_data_dir() / "aap")
    return Connection(binary=binary, env=env, hermes_key=True)


def run_hook(connection, payload, interrupted):
    """Run `hook hermes` for one call and return Hermes's directive."""
    try:
        process = subprocess.Popen(
            [connection.binary, "hook", "hermes"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            env=connection.env, text=True,
        )
    except OSError as exc:
        logger.warning("withhuman: could not start %s, blocking the call: %s", connection.binary, exc)
        return _block(FAIL_CLOSED_TEXT)
    stdout, stderr, stopped = _wait(process, json.dumps(payload, default=str), interrupted)
    if stderr.strip():
        logger.warning("withhuman: %s", stderr.strip())
    verdict = _verdict(stdout, STOPPED_TEXT if stopped else FAIL_CLOSED_TEXT)
    if connection.hermes_key and verdict.get("message", "").startswith(_REFUSED_PREFIX):
        return _block(KEY_REFUSED_TEXT)
    return verdict


def _wait(process, stdin, interrupted):
    """Wait for the CLI, stopping it on an interrupt or at the ceiling.
    Returns its output and whether it was stopped."""
    waited, stopped, deadline = 0.0, False, APPROVAL_CEILING_SECONDS + EXIT_GRACE_SECONDS
    while True:
        try:
            stdout, stderr = process.communicate(stdin, timeout=POLL_SECONDS)
            return stdout, stderr, stopped
        except subprocess.TimeoutExpired:
            stdin = None
            waited += POLL_SECONDS
        if not stopped and (interrupted() or waited >= deadline):
            # SIGTERM lets the CLI withdraw the request and print its verdict.
            stopped = True
            process.send_signal(signal.SIGTERM)
            deadline = waited + EXIT_GRACE_SECONDS
        elif stopped and waited >= deadline:
            process.kill()
            stdout, stderr = process.communicate()
            return "", stderr, stopped


def _verdict(stdout, fallback):
    try:
        verdict = json.loads(stdout)
    except ValueError:
        return _block(fallback)
    if not isinstance(verdict, dict):
        return _block(fallback)
    if verdict.get("action") == "modify" and isinstance(verdict.get("args"), dict):
        return {"action": "modify", "args": verdict["args"]}
    if verdict.get("action") == "block" and isinstance(verdict.get("message"), str) and verdict["message"]:
        return {"action": "block", "message": verdict["message"]}
    return _block(fallback)


def _block(message):
    return {"action": "block", "message": message}


def _hermes_key():
    """The agent key setup saved in this profile's secrets."""
    try:
        from agent.secret_scope import get_secret
    except ImportError:
        return (os.environ.get(KEY_ENV) or "").strip()
    return (get_secret(KEY_ENV) or "").strip()


def _setting(name):
    if _ctx is None:
        return None
    value = _ctx.get_config(name)
    return value if isinstance(value, str) and value else None


def _installed():
    """What `withhuman agent install hermes` wrote beside this file."""
    try:
        config = json.loads((Path(__file__).parent / "aap.json").read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {}
    if not isinstance(config, dict):
        return {}
    return {key: value for key in ("binary", "configDir")
            if isinstance(value := config.get(key), str) and value}


def _data_dir():
    try:
        from plugins.plugin_storage import plugin_data_dir
    except ImportError:
        path = Path(os.environ.get("HERMES_HOME") or Path.home() / ".hermes") / "plugin-data" / "withhuman"
        path.mkdir(parents=True, exist_ok=True)
        return path
    return plugin_data_dir("withhuman")


def _interrupt_check():
    """Hermes's per-thread interrupt flag. A plugin loaded outside Hermes
    has none, so its waits end only at the ceiling."""
    try:
        from tools.interrupt import is_interrupted
    except ImportError:
        return lambda: False
    return is_interrupted


def _cwd():
    try:
        return str(Path.cwd())
    except OSError:
        return ""
