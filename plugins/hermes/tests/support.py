"""Shared setup: import the plugin from its directory and point it at the
fake hook through an aap.json, as `withhuman agent install hermes` does."""

import importlib
import json
import os
import shutil
import stat
import sys
import tempfile
import unittest
from pathlib import Path

PLUGIN_DIR = Path(__file__).resolve().parents[1] / "withhuman"
FAKE_HOOK = Path(__file__).resolve().parent / "fake_hook.py"


class PluginTestCase(unittest.TestCase):
    def setUp(self):
        os.environ["PYTHONDONTWRITEBYTECODE"] = "1"
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp)
        self.binary = self.tmp / "withhuman"
        self.binary.write_text(f"#!{sys.executable}\n" + FAKE_HOOK.read_text(encoding="utf-8"), encoding="utf-8")
        self.binary.chmod(self.binary.stat().st_mode | stat.S_IXUSR)
        self.log = self.tmp / "hook.json"
        self.set_env(FAKE_HOOK_LOG=str(self.log), FAKE_HOOK_MODE="approve")

    def set_env(self, **values):
        for key, value in values.items():
            previous = os.environ.get(key)
            os.environ[key] = value
            self.addCleanup(self._restore, key, previous)

    @staticmethod
    def _restore(key, previous):
        if previous is None:
            os.environ.pop(key, None)
        else:
            os.environ[key] = previous

    def install_plugin(self, parent):
        """Copy the plugin into parent/withhuman with its aap.json."""
        target = Path(parent) / "withhuman"
        shutil.copytree(PLUGIN_DIR, target, ignore=shutil.ignore_patterns("__pycache__"))
        (target / "aap.json").write_text(
            json.dumps({"binary": str(self.binary), "configDir": str(self.tmp / "aap")}), encoding="utf-8")
        return target

    def load_plugin(self):
        """Import a fresh copy of the installed plugin as a module."""
        root = self.tmp / "import"
        self.install_plugin(root)
        sys.path.insert(0, str(root))
        self.addCleanup(sys.path.remove, str(root))
        sys.modules.pop("withhuman", None)
        self.addCleanup(sys.modules.pop, "withhuman", None)
        return importlib.import_module("withhuman")

    def hook_saw(self):
        return json.loads(self.log.read_text(encoding="utf-8"))
