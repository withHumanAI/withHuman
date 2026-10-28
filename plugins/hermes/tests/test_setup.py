"""`hermes withhuman setup` through a real Hermes checkout, against a fake
withHuman that answers sign-in and key checks. Skipped when Hermes is not
importable."""

import argparse
import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest import mock

from support import PluginTestCase

try:
    from hermes_cli import plugins as hermes_plugins
except ImportError:
    hermes_plugins = None


class FakeWithHuman(BaseHTTPRequestHandler):
    """Sign-in answers pending once, then issues a key; key checks accept
    only the agent key."""

    polls = 0

    def refused(self):
        # Like Cloudflare in front of withHuman.
        if self.headers.get("User-Agent", "").startswith("Python-urllib"):
            self.answer(403, {})
            return True
        return False

    def do_POST(self):
        if self.refused():
            return
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        origin = f"http://127.0.0.1:{self.server.server_port}"
        if self.path == "/auth/agent_enrollments":
            self.server.enrollment = body
            return self.answer(201, {"enrollment_code": "e1", "user_code": "KXT-4QW",
                                     "verification_uri": origin + "/connect/authorize",
                                     "expires_at": "2099-01-01T00:00:00Z"})
        if self.path == "/auth/agent_enrollments/token":
            FakeWithHuman.polls += 1
            if FakeWithHuman.polls == 1:
                return self.answer(404, {"error": {"code": "enrollment_pending"}})
            return self.answer(200, {"credential": {"id": "c1", "token": "whc_signed_in",
                                                    "expires_at": "2099-01-01T00:00:00Z"},
                                     "agent": {"slug": "hermes", "name": "Hermes"},
                                     "agent_instance": {"id": "i1", "name": "box-1", "agent_slug": "hermes"}})
        self.answer(404, {})

    def do_GET(self):
        if self.refused():
            return
        if self.path == "/api/v1/agent_instances/self" and self.headers["Authorization"] == "Bearer whc_pasted":
            return self.answer(200, {"agent_slug": "hermes", "agent_instance_id": "i1"})
        self.answer(401, {"error": {"code": "unauthenticated"}})

    def answer(self, status, body):
        raw = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, *_args):
        pass


@unittest.skipIf(hermes_plugins is None, "Hermes is not importable")
class SetupTest(PluginTestCase):
    def setUp(self):
        super().setUp()
        FakeWithHuman.polls = 0
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), FakeWithHuman)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.addCleanup(self.server.shutdown)
        self.origin = f"http://127.0.0.1:{self.server.server_port}"
        self.put_cli_on_path()
        self.install_plugin(self.hermes_home / "plugins", cli_installed=False)
        (self.hermes_home / "config.yaml").write_text("plugins:\n  enabled:\n    - withhuman\n", encoding="utf-8")
        bundled = self.tmp / "bundled-plugins"
        bundled.mkdir()
        self.set_env(HERMES_BUNDLED_PLUGINS=str(bundled))
        hermes_plugins._reset_plugin_managers_for_tests()
        self.addCleanup(hermes_plugins._reset_plugin_managers_for_tests)
        self.manager = hermes_plugins.get_plugin_manager()
        self.manager.discover_and_load()

    def setup_command(self, answers, key=None):
        command = self.manager._cli_commands["withhuman"]
        args = argparse.Namespace(withhuman_action="setup", url=self.origin, agent=None)
        with mock.patch("builtins.input", side_effect=answers), mock.patch("getpass.getpass", return_value=key or ""):
            return command["handler_fn"](args)

    def saved(self):
        from hermes_cli.config import load_config

        env = (self.hermes_home / ".env").read_text(encoding="utf-8") if (self.hermes_home / ".env").exists() else ""
        return env, load_config()

    def test_sign_in_saves_the_key_turns_off_the_timeout_and_tests(self):
        self.assertEqual(self.setup_command(["1", "y"]), 0)
        env, config = self.saved()
        self.assertIn("WITHHUMAN_AGENT_TOKEN=whc_signed_in", env)
        self.assertEqual(config["plugins"]["hook_callback_timeout"], 0)
        self.assertEqual(config["plugins"]["entries"]["withhuman"]["settings"]["url"], self.origin)
        self.assertEqual(self.server.enrollment["runtime"], "hermes")
        saw = self.hook_saw()
        self.assertEqual(saw["AAP_INSTANCE_TOKEN"], "whc_signed_in")
        self.assertEqual(saw["AAP_BASE_URL"], self.origin + "/api/aap")
        self.assertEqual(saw["payload"]["tool_name"], "withhuman_test")

    def test_a_pasted_key_is_checked_before_it_is_saved(self):
        self.assertEqual(self.setup_command(["2"], key="whk_personal"), 1)
        env, config = self.saved()
        self.assertNotIn("WITHHUMAN_AGENT_TOKEN", env)
        self.assertNotEqual(config["plugins"]["hook_callback_timeout"], 0)
        self.assertEqual(self.setup_command(["2", "n"], key="whc_pasted"), 0)
        env, _ = self.saved()
        self.assertIn("WITHHUMAN_AGENT_TOKEN=whc_pasted", env)

    def test_the_address_defaults_to_cloud(self):
        command = self.manager._cli_commands["withhuman"]
        args = argparse.Namespace(withhuman_action="setup", url=None, agent=None)
        prompts = []

        def answer(prompt):
            prompts.append(prompt)
            raise KeyboardInterrupt

        with mock.patch("builtins.input", side_effect=answer):
            self.assertEqual(command["handler_fn"](args), 1)
        self.assertEqual(prompts, ["withHuman address (default is cloud): "])

    def test_the_command_is_registered(self):
        self.assertIn("withhuman", self.manager._cli_commands)
        self.assertIsNone(self.manager._plugins["withhuman"].error)


if __name__ == "__main__":
    unittest.main()
