"""Preserve workspace package installs and upstream framework constraints."""

import configparser
import importlib.util
from pathlib import Path
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location(
    "workspace_pip", ROOT / "scripts/configure-workspace-pip.py"
)
PIP = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PIP)


class WorkspacePipTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="prism-workspace-pip-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.floors = self.root / "security.txt"
        self.floors.write_text("Pillow>=13.0.0\nSoupSieve>=4.0.0\nBrotli>=1.2.1\n")
        self.config = self.root / "pip.conf"

    def test_floors_apply_without_docker_environment_or_activation(self):
        PIP.configure(self.floors, self.config, [])
        config = configparser.ConfigParser()
        config.read(self.config)
        self.assertTrue(config.getboolean("global", "break-system-packages"))
        self.assertEqual(config.get("global", "constraint"), str(self.floors))

    def test_existing_pip_repository_and_install_settings_survive(self):
        self.config.write_text("[global]\nindex-url=https://packages.example.test/simple\n[install]\ntimeout=90\n")
        PIP.configure(self.floors, self.config, [])
        config = configparser.ConfigParser()
        config.read(self.config)
        self.assertEqual(config.get("global", "index-url"), "https://packages.example.test/simple")
        self.assertEqual(config.getint("install", "timeout"), 90)

    def test_framework_pins_survive_and_security_managed_pins_are_replaced(self):
        upstream = self.root / "nvidia.txt"
        upstream.write_text("torch==2.11.0\nnumpy==2.4.0\npillow==12.3.0\nSOUPSIEVE==2.10\nbrotli==1.1.0\n")
        PIP.configure(self.floors, self.config, [str(upstream)])
        result = self.floors.read_text()
        self.assertIn("torch==2.11.0", result)
        self.assertIn("numpy==2.4.0", result)
        self.assertIn("Pillow>=13.0.0", result)
        self.assertIn("SoupSieve>=4.0.0", result)
        self.assertNotIn("==12.3.0", result)
        self.assertNotIn("==2.10", result)
        self.assertNotIn("==1.1.0", result)

    def test_nested_relative_framework_constraints_and_cycles_are_handled(self):
        upstream = self.root / "nvidia.txt"
        nested = self.root / "cuda.txt"
        upstream.write_text("-c cuda.txt\nPillow==12.3.0\n")
        nested.write_text("--constraint nvidia.txt\ntorch==2.11.0\n")
        PIP.configure(self.floors, self.config, [str(upstream)])
        self.assertEqual(self.floors.read_text().count("torch==2.11.0"), 1)
        self.assertNotIn("Pillow==12.3.0", self.floors.read_text())

    def test_own_constraint_path_is_not_duplicated(self):
        original = self.floors.read_text()
        PIP.configure(self.floors, self.config, [str(self.floors)])
        self.assertEqual(self.floors.read_text(), original)


if __name__ == "__main__":
    unittest.main()
