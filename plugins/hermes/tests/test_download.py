"""Setup's CLI download, against a local file server, without Hermes."""

import hashlib
import os
import threading
import unittest
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from support import PluginTestCase


class DownloadTest(PluginTestCase):
    def setUp(self):
        super().setUp()
        self.plugin = self.load_plugin(cli_installed=False)
        import withhuman.connect as connect

        self.connect = connect
        self.release = self.tmp / "release"
        (self.release / "9.9.9").mkdir(parents=True)
        # The pinned release: the fake CLI, marked so a replaced copy is visible.
        self.cli_bytes = self.binary.read_bytes() + b"\n# pinned release\n"
        name = connect.platform_name()
        (self.release / "9.9.9" / f"withhuman-{name}").write_bytes(self.cli_bytes)
        server = ThreadingHTTPServer(("127.0.0.1", 0), self.handler())
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.shutdown)
        connect.CLI_DOWNLOADS = f"http://127.0.0.1:{server.server_port}"
        connect.CLI_VERSION = "9.9.9"
        connect.CLI_SHA256 = {name: hashlib.sha256(self.cli_bytes).hexdigest()}
        self.set_env(PATH=str(self.tmp / "empty"))

    def handler(self):
        directory = str(self.release)

        class Release(SimpleHTTPRequestHandler):
            def __init__(self, *args, **kwargs):
                super().__init__(*args, directory=directory, **kwargs)

            def do_GET(self):
                # Like Cloudflare in front of downloads.withhuman.ai.
                if self.headers.get("User-Agent", "").startswith("Python-urllib"):
                    self.send_error(403)
                    return
                super().do_GET()

            def log_message(self, *_args):
                pass

        return Release

    def test_downloads_and_checks_the_pinned_cli(self):
        path = Path(self.connect.download_cli())
        self.assertEqual(path, self.hermes_home / "plugin-data" / "withhuman" / "bin" / "withhuman")
        self.assertEqual(path.read_bytes(), self.cli_bytes)
        self.assertTrue(os.access(path, os.X_OK))
        self.assertEqual(self.plugin.find_cli(), str(path))

    def test_refuses_a_download_that_does_not_match(self):
        self.connect.CLI_SHA256 = {self.connect.platform_name(): "0" * 64}
        with self.assertRaises(self.connect.SetupError):
            self.connect.download_cli()
        self.assertEqual(list(self.plugin.downloaded_cli().parent.iterdir()), [])

    def test_downloads_when_the_installed_cli_is_older(self):
        self.put_cli_on_path()
        self.set_env(FAKE_CLI_VERSION="withhuman 9.9.8")
        self.assertEqual(self.connect._ensure_cli(), str(self.plugin.downloaded_cli()))

    def test_keeps_an_installed_cli_that_is_new_enough(self):
        self.put_cli_on_path()
        for version in ("withhuman 9.9.9", "withhuman 10.0.0", "withhuman dev"):
            self.set_env(FAKE_CLI_VERSION=version)
            self.assertEqual(self.connect._ensure_cli(), str(self.binary))
        self.assertFalse(self.plugin.downloaded_cli().exists())


    def downloaded_copy(self, version):
        """A copy setup downloaded earlier, reporting version."""
        path = self.plugin.downloaded_cli()
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(self.binary.read_bytes())
        path.chmod(0o755)
        self.set_env(FAKE_CLI_VERSION=version)
        return path

    def test_refresh_replaces_a_copy_older_than_the_pin(self):
        path = self.downloaded_copy("withhuman 9.9.8")
        self.connect.refresh_downloaded_cli()
        self.assertEqual(path.read_bytes(), self.cli_bytes)

    def test_refresh_leaves_a_copy_at_the_pin(self):
        path = self.downloaded_copy("withhuman 9.9.9")
        before = path.read_bytes()
        self.connect.refresh_downloaded_cli()
        self.assertEqual(path.read_bytes(), before)

    def test_refresh_downloads_for_a_connected_plugin_without_a_cli(self):
        self.connect.refresh_downloaded_cli(connected=True)
        self.assertEqual(self.plugin.downloaded_cli().read_bytes(), self.cli_bytes)

    def test_refresh_needs_a_copy_setup_downloaded(self):
        self.put_cli_on_path()
        self.set_env(FAKE_CLI_VERSION="withhuman 9.9.8")
        self.connect.refresh_downloaded_cli()
        self.assertFalse(self.plugin.downloaded_cli().exists())


if __name__ == "__main__":
    unittest.main()
