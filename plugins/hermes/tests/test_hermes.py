"""The plugin loaded and dispatched by a real Hermes checkout. Skipped when
Hermes is not importable."""

import threading
import time
import unittest

from support import PluginTestCase

try:
    from hermes_cli import plugins as hermes_plugins
except ImportError:
    hermes_plugins = None


@unittest.skipIf(hermes_plugins is None, "Hermes is not importable")
class HermesTest(PluginTestCase):
    def start_hermes(self, callback_timeout=0):
        home = self.tmp / "hermes-home"
        self.install_plugin(home / "plugins")
        (home / "config.yaml").write_text(
            f"plugins:\n  enabled:\n    - withhuman\n  hook_callback_timeout: {callback_timeout}\n", encoding="utf-8")
        bundled = self.tmp / "bundled-plugins"
        bundled.mkdir()
        self.set_env(HERMES_HOME=str(home), HERMES_BUNDLED_PLUGINS=str(bundled))
        hermes_plugins._reset_plugin_managers_for_tests()
        self.addCleanup(hermes_plugins._reset_plugin_managers_for_tests)
        manager = hermes_plugins.get_plugin_manager()
        manager.discover_and_load()
        return manager

    def dispatch(self):
        return hermes_plugins._dispatch_pre_tool_call_hooks(
            "terminal", {"command": "git push"}, session_id="session-1", tool_call_id="call-1", turn_id="turn-1")

    def test_loads_with_one_pre_tool_call_hook(self):
        manager = self.start_hermes()
        self.assertIsNone(manager._plugins["withhuman"].error)
        self.assertEqual([name for name, callbacks in manager._hooks.items() if callbacks], ["pre_tool_call"])

    def test_approval_runs_the_approved_arguments(self):
        self.start_hermes()
        self.assertEqual(self.dispatch(), (None, {"command": "git push"}))
        self.assertEqual(self.hook_saw()["payload"]["extra"]["tool_call_id"], "call-1")

    def test_denial_blocks_with_the_reason(self):
        self.set_env(FAKE_HOOK_MODE="deny")
        self.start_hermes()
        self.assertEqual(self.dispatch()[0], "Denied by a reviewer.")

    def test_a_callback_timeout_cuts_the_wait_short(self):
        self.set_env(FAKE_HOOK_MODE="slow", FAKE_HOOK_SLEEP="3")
        self.start_hermes(callback_timeout=1)
        block, _ = self.dispatch()
        self.assertIsNotNone(block)
        self.assertNotIn("Denied", block)

    def test_no_callback_timeout_waits_for_the_decision(self):
        self.set_env(FAKE_HOOK_MODE="slow", FAKE_HOOK_SLEEP="3")
        self.start_hermes(callback_timeout=0)
        self.assertEqual(self.dispatch(), (None, {"command": "git push"}))

    def test_an_interrupted_turn_withdraws_the_request(self):
        from tools.interrupt import set_interrupt

        self.set_env(FAKE_HOOK_MODE="wait")
        self.start_hermes()
        result = {}
        worker = threading.Thread(target=lambda: result.setdefault("dispatch", self.dispatch()))
        worker.start()
        time.sleep(1)
        set_interrupt(True, worker.ident)
        self.addCleanup(set_interrupt, False, worker.ident)
        worker.join(15)
        self.assertFalse(worker.is_alive())
        self.assertEqual(result["dispatch"][0], "Withdrawn before anyone decided.")


if __name__ == "__main__":
    unittest.main()
