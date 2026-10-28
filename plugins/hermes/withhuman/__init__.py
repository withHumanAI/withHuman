"""withHuman for Hermes Agent: every tool call waits for withHuman's decision.

``register`` adds one ``pre_tool_call`` hook. For each call it runs the
withhuman CLI's ``hook hermes`` with the payload Hermes gives shell hooks,
plus ``timeout_ms``, and returns the directive the CLI prints: ``modify``
with the approved arguments, or ``block`` with the reason. The CLI holds
the agent's credential and asks withHuman; this plugin holds no secrets.

Hermes must not abandon the wait, so ``withhuman agent install hermes``
sets ``plugins.hook_callback_timeout`` to 0. The plugin keeps its own
ceiling instead, and stops the CLI when Hermes interrupts the turn, which
withdraws the request.
"""

from __future__ import annotations

import json
import logging
import os
import signal
import subprocess
from pathlib import Path

logger = logging.getLogger(__name__)

# How long a call can wait for a decision. The CLI expires the request 30
# seconds sooner, so withHuman's expiry reaches the model first.
APPROVAL_CEILING_SECONDS = 7 * 24 * 60 * 60 + 30
# Time the CLI gets to print its verdict after the ceiling or a stop.
EXIT_GRACE_SECONDS = 30
POLL_SECONDS = 0.5

FAIL_CLOSED_TEXT = "withHuman could not get an approval decision. The call was not run."
STOPPED_TEXT = "The tool call was stopped before anyone decided. The call was not run."

# Keys Hermes sends at the top level of a shell hook payload; the rest go in "extra".
_TOP_LEVEL_KEYS = {"tool_name", "args", "session_id"}


def register(ctx):
    ctx.register_hook("pre_tool_call", pre_tool_call)


def pre_tool_call(tool_name=None, args=None, **kwargs):
    payload = {
        "hook_event_name": "pre_tool_call",
        "tool_name": tool_name,
        "tool_input": args if isinstance(args, dict) else None,
        "session_id": kwargs.get("session_id") or kwargs.get("parent_session_id") or "",
        "cwd": _cwd(),
        "timeout_ms": APPROVAL_CEILING_SECONDS * 1000,
        "extra": {key: value for key, value in kwargs.items() if key not in _TOP_LEVEL_KEYS},
    }
    binary, config_dir = _installed()
    env = dict(os.environ)
    if config_dir:
        env["AAP_CONFIG_DIR"] = config_dir
    try:
        process = subprocess.Popen(
            [binary, "hook", "hermes"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            env=env, text=True,
        )
    except OSError as exc:
        logger.warning("withhuman: could not start %s, blocking the call: %s", binary, exc)
        return _block(FAIL_CLOSED_TEXT)
    stdout, stderr, stopped = _wait(process, json.dumps(payload, default=str))
    if stderr.strip():
        logger.warning("withhuman: %s", stderr.strip())
    return _verdict(stdout, STOPPED_TEXT if stopped else FAIL_CLOSED_TEXT)


def _wait(process, stdin):
    """Wait for the CLI, stopping it on a Hermes interrupt or at the ceiling.
    Returns its output and whether it was stopped."""
    interrupted = _interrupt_check()
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


def _installed():
    """The CLI and configuration directory ``withhuman agent install`` wrote
    beside this file, or the withhuman on PATH with its default directory."""
    try:
        config = json.loads((Path(__file__).parent / "aap.json").read_text(encoding="utf-8"))
    except (OSError, ValueError):
        config = {}
    if not isinstance(config, dict):
        config = {}
    binary = config.get("binary") if isinstance(config.get("binary"), str) else ""
    config_dir = config.get("configDir") if isinstance(config.get("configDir"), str) else ""
    return binary or "withhuman", config_dir


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
