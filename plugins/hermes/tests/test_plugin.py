"""The plugin against the fake hook, without Hermes."""

import threading
import time
import unittest

from support import PluginTestCase


class PluginTest(PluginTestCase):
    def call(self, plugin, **kwargs):
        return plugin.pre_tool_call(
            tool_name="mcp_stripe_create_refund", args={"amount": 500},
            session_id="session-1", tool_call_id="call-1", turn_id="turn-1", task_id="task-1",
            api_request_id="request-1", **kwargs)

    def test_approval_returns_the_approved_arguments(self):
        plugin = self.load_plugin()
        self.assertEqual(self.call(plugin), {"action": "modify", "args": {"amount": 500}})

    def test_sends_the_shell_hook_payload_with_a_week_ceiling(self):
        plugin = self.load_plugin()
        self.call(plugin)
        saw = self.hook_saw()
        payload = saw["payload"]
        self.assertEqual(payload["hook_event_name"], "pre_tool_call")
        self.assertEqual(payload["tool_name"], "mcp_stripe_create_refund")
        self.assertEqual(payload["tool_input"], {"amount": 500})
        self.assertEqual(payload["session_id"], "session-1")
        self.assertEqual(payload["timeout_ms"], (7 * 24 * 60 * 60 + 30) * 1000)
        self.assertEqual(payload["extra"], {
            "tool_call_id": "call-1", "turn_id": "turn-1", "task_id": "task-1", "api_request_id": "request-1"})
        self.assertEqual(saw["config_dir"], str(self.tmp / "aap"))

    def test_denial_passes_the_reason_through(self):
        self.set_env(FAKE_HOOK_MODE="deny")
        plugin = self.load_plugin()
        self.assertEqual(self.call(plugin), {"action": "block", "message": "Denied by a reviewer."})

    def test_unreadable_answer_blocks(self):
        self.set_env(FAKE_HOOK_MODE="garbage")
        plugin = self.load_plugin()
        self.assertEqual(self.call(plugin), {"action": "block", "message": plugin.FAIL_CLOSED_TEXT})

    def test_missing_cli_blocks(self):
        plugin = self.load_plugin()
        self.binary.unlink()
        self.assertEqual(self.call(plugin), {"action": "block", "message": plugin.FAIL_CLOSED_TEXT})

    def test_interrupt_withdraws_the_request(self):
        self.set_env(FAKE_HOOK_MODE="wait")
        plugin = self.load_plugin()
        stop = threading.Event()
        plugin._interrupt_check = lambda: stop.is_set
        threading.Timer(1, stop.set).start()
        started = time.monotonic()
        verdict = self.call(plugin)
        self.assertEqual(verdict, {"action": "block", "message": "Withdrawn before anyone decided."})
        self.assertLess(time.monotonic() - started, 10)

    def test_a_hook_that_ignores_the_stop_is_killed(self):
        self.set_env(FAKE_HOOK_MODE="stubborn")
        plugin = self.load_plugin()
        plugin._interrupt_check = lambda: lambda: True
        plugin.EXIT_GRACE_SECONDS = 1
        self.assertEqual(self.call(plugin), {"action": "block", "message": plugin.STOPPED_TEXT})

    def test_the_ceiling_stops_the_hook(self):
        self.set_env(FAKE_HOOK_MODE="wait")
        plugin = self.load_plugin()
        plugin.APPROVAL_CEILING_SECONDS = 1
        plugin.EXIT_GRACE_SECONDS = 1
        self.assertEqual(self.call(plugin), {"action": "block", "message": "Withdrawn before anyone decided."})


if __name__ == "__main__":
    unittest.main()
