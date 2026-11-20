#!/usr/bin/env python3
"""Exercise workspace restoration without package installs or a real daemon."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
START = ROOT / "scripts/workspace-start.sh"


class WorkspaceStartTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="prism-workspace-start-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.workspace = self.root / "workspace with spaces"
        self.workspace.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.log = self.root / "calls.jsonl"
        stub = self.bin / "stub"
        stub.write_text(
            f"#!{sys.executable}\n"
            "import json, os, pathlib, sys\n"
            "name = pathlib.Path(sys.argv[0]).name\n"
            "with open(os.environ['TEST_LOG'], 'a') as out:\n"
            "    out.write(json.dumps({'name': name, 'args': sys.argv[1:], "
            "'pid': os.getpid(), 'constraint': os.getenv('PIP_CONSTRAINT')}) + '\\n')\n"
            "key = name + (' ' + sys.argv[1] if len(sys.argv) > 1 else '')\n"
            "failures = json.loads(os.getenv('TEST_FAILURES', '{}'))\n"
            "sys.exit(failures.get(key, failures.get(name, 0)))\n"
        )
        stub.chmod(0o755)
        for name in ["apt-get", "python3", "python", "crontab", "cron", "tail"]:
            (self.bin / name).symlink_to(stub)
        self.env = {
            **os.environ,
            "PATH": str(self.bin) + os.pathsep + os.environ.get("PATH", ""),
            "WORKSPACE_DIR": str(self.workspace),
            "TEST_LOG": str(self.log),
            "PIP_CONSTRAINT": "/etc/prism/test-constraints.txt",
        }

    def run_start(self, python="python3", failures=None):
        self.env["TEST_FAILURES"] = json.dumps(failures or {})
        proc = subprocess.Popen(
            ["sh", str(START), python], env=self.env,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        stdout, stderr = proc.communicate(timeout=5)
        calls = [json.loads(line) for line in self.log.read_text().splitlines()]
        return proc, stdout, stderr, calls

    def save_manifests(self):
        (self.workspace / ".apt-packages").write_text("jq\ncurl\n")
        (self.workspace / ".pip-packages").write_text("requests\nBrotli>=1.2.1\n")
        (self.workspace / ".crontab").write_text("* * * * * echo ready\n")

    def test_missing_manifests_start_cron_then_exec_keepalive(self):
        proc, _, stderr, calls = self.run_start()
        self.assertEqual(proc.returncode, 0, stderr)
        self.assertEqual([c["name"] for c in calls], ["cron", "tail"])
        self.assertEqual(calls[-1]["args"], ["-f", "/dev/null"])
        self.assertEqual(calls[-1]["pid"], proc.pid)

    def test_saved_packages_and_jobs_are_restored(self):
        self.save_manifests()
        proc, _, stderr, calls = self.run_start()
        self.assertEqual(proc.returncode, 0, stderr)
        self.assertEqual(
            [(c["name"], c["args"]) for c in calls],
            [
                ("apt-get", ["update", "-qq"]),
                ("apt-get", ["install", "-y", "jq", "curl"]),
                ("python3", ["-m", "pip", "install", "-q", "requests", "Brotli>=1.2.1"]),
                ("crontab", [str(self.workspace / ".crontab")]),
                ("cron", []),
                ("tail", ["-f", "/dev/null"]),
            ],
        )
        self.assertTrue(all(c["constraint"] == self.env["PIP_CONSTRAINT"] for c in calls))

    def test_gpu_uses_its_python_environment(self):
        (self.workspace / ".pip-packages").write_text("requests\n")
        proc, _, stderr, calls = self.run_start(python="python")
        self.assertEqual(proc.returncode, 0, stderr)
        self.assertEqual(calls[0]["name"], "python")
        self.assertEqual(calls[0]["args"], ["-m", "pip", "install", "-q", "requests"])

    def test_empty_package_lists_skip_install_but_restore_empty_crontab(self):
        for name in [".apt-packages", ".pip-packages", ".crontab"]:
            (self.workspace / name).touch()
        proc, _, stderr, calls = self.run_start()
        self.assertEqual(proc.returncode, 0, stderr)
        self.assertEqual([c["name"] for c in calls], ["crontab", "cron", "tail"])

    def test_restore_errors_are_reported_and_do_not_prevent_startup(self):
        self.save_manifests()
        proc, _, stderr, calls = self.run_start(
            failures={"apt-get update": 23, "python3": 24, "crontab": 25}
        )
        self.assertEqual(proc.returncode, 0, stderr)
        self.assertNotIn(["install", "-y", "jq", "curl"], [c["args"] for c in calls])
        self.assertIn("Could not restore all saved APT packages", stderr)
        self.assertIn("Could not restore all saved Python packages", stderr)
        self.assertIn("Could not restore the saved crontab", stderr)
        self.assertEqual([c["name"] for c in calls][-2:], ["cron", "tail"])

    def test_install_failure_still_starts_workspace(self):
        (self.workspace / ".apt-packages").write_text("jq\n")
        proc, _, stderr, calls = self.run_start(failures={"apt-get install": 23})
        self.assertEqual(proc.returncode, 0, stderr)
        self.assertIn("Could not restore all saved APT packages", stderr)
        self.assertEqual([c["name"] for c in calls][-2:], ["cron", "tail"])

    def test_cron_failure_exits_instead_of_claiming_running(self):
        proc, _, _, calls = self.run_start(failures={"cron": 37})
        self.assertEqual(proc.returncode, 37)
        self.assertEqual([c["name"] for c in calls], ["cron"])

    def test_images_keep_default_command_replaceable_and_nvidia_entrypoint(self):
        for file, python in [("Dockerfile.agent", "python3"), ("Dockerfile.agent.gpu", "python")]:
            with self.subTest(file=file):
                instructions = (ROOT / file).read_text().splitlines()
                self.assertFalse(any(line.startswith("ENTRYPOINT ") for line in instructions))
                commands = [json.loads(line[4:]) for line in instructions if line.startswith("CMD ")]
                self.assertEqual(commands, [["/usr/local/bin/prism-workspace-start", python]])


if __name__ == "__main__":
    unittest.main()
