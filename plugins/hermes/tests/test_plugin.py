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
        self.assertEqual(saw["AAP_CONFIG_DIR"], str(self.tmp / "aap"))
        self.assertIsNone(saw["AAP_INSTANCE_TOKEN"])

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
        self.set_env(PATH=str(self.tmp / "empty"))
        self.assertEqual(self.call(plugin), {"action": "block", "message": plugin.NO_CLI_TEXT})

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

    def test_the_cli_sees_a_minimal_environment(self):
        self.set_env(OPENAI_API_KEY="sk-test", SOME_PLUGIN_SECRET="secret", HTTPS_PROXY="http://proxy.example:3128",
                     LC_ALL="C")
        plugin = self.load_plugin()
        self.call(plugin)
        seen = set(self.hook_saw()["env"])
        self.assertNotIn("OPENAI_API_KEY", seen)
        self.assertNotIn("SOME_PLUGIN_SECRET", seen)
        self.assertTrue({"PATH", "HOME", "HTTPS_PROXY", "LC_ALL", "AAP_CONFIG_DIR"} <= seen, seen)


class HookLimitTest(PluginTestCase):
    """Hermes's hook limit left on: the request expires before it."""

    def call(self, plugin):
        return plugin.pre_tool_call(tool_name="terminal", args={"command": "ls"}, session_id="session-1")

    def plugin_under_limit(self, limit):
        plugin = self.load_plugin()
        plugin.hook_limit = lambda: limit
        return plugin

    def test_the_request_expires_before_the_limit(self):
        plugin = self.plugin_under_limit(30)
        self.call(plugin)
        # The CLI expires the request 30 seconds before timeout_ms: 27 seconds in.
        self.assertEqual(self.hook_saw()["payload"]["timeout_ms"], 57 * 1000)

    def test_an_expired_request_names_the_limit(self):
        self.set_env(FAKE_HOOK_MODE="expired")
        plugin = self.plugin_under_limit(30)
        self.assertEqual(self.call(plugin), {"action": "block", "message": plugin.HOOK_LIMIT_TEXT.format(limit=30)})

    def test_the_cli_is_stopped_before_the_limit(self):
        self.set_env(FAKE_HOOK_MODE="wait")
        plugin = self.plugin_under_limit(2)
        started = time.monotonic()
        self.assertEqual(self.call(plugin), {"action": "block", "message": "Withdrawn before anyone decided."})
        self.assertLess(time.monotonic() - started, 2)


class HermesKeyTest(PluginTestCase):
    """Connected by `hermes withhuman setup`: the key is in this profile's secrets."""

    def call(self, plugin):
        return plugin.pre_tool_call(tool_name="terminal", args={"command": "ls"},
                                    session_id="session-1", tool_call_id="call-1")

    def test_off_until_connected(self):
        self.put_cli_on_path()
        plugin = self.load_plugin(cli_installed=False)
        self.assertIsNone(self.call(plugin))
        self.assertFalse(self.log.exists())

    def test_passes_the_profile_key_and_address_to_the_cli(self):
        self.put_cli_on_path()
        self.set_env(WITHHUMAN_AGENT_TOKEN="whc_test")
        plugin = self.load_plugin(cli_installed=False)
        self.assertEqual(self.call(plugin), {"action": "modify", "args": {"command": "ls"}})
        saw = self.hook_saw()
        self.assertEqual(saw["AAP_INSTANCE_TOKEN"], "whc_test")
        self.assertEqual(saw["AAP_BASE_URL"], "https://app.withhuman.ai/api/aap")
        self.assertEqual(saw["AAP_CONFIG_DIR"], str(self.hermes_home / "plugin-data" / "withhuman" / "aap"))

    def test_a_refused_key_says_to_run_setup(self):
        self.put_cli_on_path()
        self.set_env(WITHHUMAN_AGENT_TOKEN="whc_test", FAKE_HOOK_MODE="refused")
        plugin = self.load_plugin(cli_installed=False)
        self.assertEqual(self.call(plugin), {"action": "block", "message": plugin.KEY_REFUSED_TEXT})

    def test_a_missing_cli_blocks_once_connected(self):
        self.set_env(WITHHUMAN_AGENT_TOKEN="whc_test", PATH=str(self.tmp / "empty"))
        plugin = self.load_plugin(cli_installed=False)
        self.assertEqual(self.call(plugin), {"action": "block", "message": plugin.NO_CLI_TEXT})


if __name__ == "__main__":
    unittest.main()
