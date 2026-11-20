"""Check installation configuration without starting containers or using a daemon."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


class ComposeConfigurationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not shutil.which("docker"):
            raise unittest.SkipTest("Docker CLI is not installed")
        result = subprocess.run(
            ["docker", "compose", "version"],
            capture_output=True,
            text=True,
            timeout=15,
        )
        if result.returncode:
            raise unittest.SkipTest("Docker Compose plugin is not installed")

    def compose(self, overrides=None, extra_file=None, profiles=()):
        # Avoid reading local .env/override files or inheriting operator secrets,
        # COMPOSE_PROFILES and other installation-specific shell configuration.
        env = {"PATH": os.environ.get("PATH", os.defpath)}
        env.update(overrides or {})
        command = [
            "docker",
            "compose",
            "--project-directory",
            str(ROOT),
            "--env-file",
            str(ROOT / ".env.example"),
            "-f",
            str(ROOT / "docker-compose.yml"),
        ]
        if extra_file:
            command.extend(["-f", str(extra_file)])
        for profile in profiles:
            command.extend(["--profile", profile])
        command.extend(["config", "--format", "json"])
        result = subprocess.run(
            command,
            cwd=ROOT,
            env=env,
            capture_output=True,
            text=True,
            timeout=30,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_default_is_cpu_without_optional_services(self):
        config = self.compose()
        self.assertEqual(
            set(config["services"]),
            {"prism-server", "workspace", "postgres", "searxng", "traefik"},
        )
        workspace = config["services"]["workspace"]
        self.assertEqual(workspace["image"], "blazux/prism-workspace:latest")
        self.assertEqual(workspace["build"]["dockerfile"], "Dockerfile.agent")
        self.assertIsNone(workspace.get("command"))
        self.assertIsNone(workspace.get("entrypoint"))
        self.assertFalse(
            workspace.get("deploy", {})
            .get("resources", {})
            .get("reservations", {})
            .get("devices")
        )

    def test_single_and_multi_user_keep_installation_identity(self):
        for mode in ("", "1"):
            with self.subTest(multi_user=mode):
                config = self.compose({"MULTI_USER": mode})
                self.assertEqual(config["name"], "prism")
                for name in ("server", "workspace", "postgres", "searxng", "traefik"):
                    service = "prism-server" if name == "server" else name
                    self.assertEqual(
                        config["services"][service]["container_name"],
                        "prism-" + name,
                    )
                self.assertEqual(
                    config["services"]["prism-server"]["environment"]["MULTI_USER"],
                    mode,
                )
                for volume in ("workspace", "pgdata", "server-private", "notes-vault"):
                    self.assertEqual(
                        config["volumes"][volume]["name"], "prism_" + volume
                    )
                server_mounts = {
                    mount["target"]: mount["source"]
                    for mount in config["services"]["prism-server"]["volumes"]
                }
                workspace_mounts = {
                    mount["target"]: mount["source"]
                    for mount in config["services"]["workspace"]["volumes"]
                }
                self.assertEqual(server_mounts["/workspace"], "workspace")
                self.assertEqual(workspace_mounts["/workspace"], "workspace")
                self.assertEqual(server_mounts["/var/lib/prism"], "server-private")
                ports = config["services"]["prism-server"]["ports"]
                self.assertTrue(
                    any(p["target"] == 8080 and str(p["published"]) == "48080" for p in ports)
                )
                proxy_ports = config["services"]["traefik"]["ports"]
                self.assertTrue(
                    any(p["target"] == 80 and str(p["published"]) == "80" for p in proxy_ports)
                )

    def test_empty_workspace_explicitly_selects_cpu(self):
        workspace = self.compose({"WORKSPACE": ""})["services"]["workspace"]
        self.assertEqual(workspace["image"], "blazux/prism-workspace:latest")
        self.assertEqual(workspace["build"]["dockerfile"], "Dockerfile.agent")

    def test_gpu_keeps_its_own_image_and_existing_dockerfile(self):
        config = self.compose({"WORKSPACE": "gpu"})
        workspace = config["services"]["workspace"]
        self.assertEqual(workspace["image"], "blazux/prism-workspace:gpu")
        self.assertEqual(workspace["build"]["dockerfile"], "Dockerfile.agent.gpu")
        self.assertTrue((ROOT / workspace["build"]["dockerfile"]).is_file())
        self.assertEqual(workspace["container_name"], "prism-workspace")
        self.assertIsNone(workspace.get("command"))
        self.assertIsNone(workspace.get("entrypoint"))

    def test_invalid_workspace_values_do_not_silently_select_gpu(self):
        for value in ("cpu", "false", "typo"):
            with self.subTest(workspace=value):
                workspace = self.compose({"WORKSPACE": value})["services"]["workspace"]
                dockerfile = workspace["build"]["dockerfile"]
                self.assertNotEqual(dockerfile, "Dockerfile.agent.gpu")
                self.assertFalse((ROOT / dockerfile).exists())
                self.assertNotEqual(workspace["image"], "blazux/prism-workspace:latest")
                self.assertNotEqual(workspace["image"], "blazux/prism-workspace:gpu")

    def test_documented_server_settings_reach_container(self):
        environment = self.compose(
            {
                "SERVICE_PORT_START": "22000",
                "SERVICE_PORT_END": "22049",
                "OPENAI_REASONING_EFFORT": "none",
            }
        )["services"]["prism-server"]["environment"]
        self.assertEqual(environment["SERVICE_PORT_START"], "22000")
        self.assertEqual(environment["SERVICE_PORT_END"], "22049")
        self.assertEqual(environment["OPENAI_REASONING_EFFORT"], "none")

    def test_reasoning_default_is_preserved(self):
        environment = self.compose()["services"]["prism-server"]["environment"]
        self.assertEqual(environment["OPENAI_REASONING_EFFORT"], "medium")

    def test_explicit_empty_reasoning_setting_is_preserved(self):
        environment = self.compose({"OPENAI_REASONING_EFFORT": ""})["services"][
            "prism-server"
        ]["environment"]
        self.assertEqual(environment["OPENAI_REASONING_EFFORT"], "")

    def test_gpu_device_override_merges_with_standard_workspace(self):
        override = """services:
  workspace:
    pull_policy: build
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: all
              capabilities: [gpu]
"""
        with tempfile.TemporaryDirectory(prefix="prism-compose-contract-") as temp:
            path = Path(temp) / "gpu.yml"
            path.write_text(override, encoding="utf-8")
            config = self.compose({"WORKSPACE": "gpu"}, extra_file=path)
        workspace = config["services"]["workspace"]
        self.assertEqual(workspace["pull_policy"], "build")
        self.assertEqual(workspace["image"], "blazux/prism-workspace:gpu")
        devices = workspace["deploy"]["resources"]["reservations"]["devices"]
        self.assertEqual(len(devices), 1)
        self.assertEqual(devices[0]["driver"], "nvidia")
        self.assertEqual(devices[0]["capabilities"], ["gpu"])
        self.assertIn(devices[0]["count"], ("all", -1))
        self.assertNotIn("device_ids", devices[0])
        self.assertTrue(any(m["target"] == "/workspace" for m in workspace["volumes"]))
        self.assertEqual(workspace["environment"]["LANG"], "C.UTF-8")

    def test_protonmail_remains_optional_and_keeps_its_volume(self):
        config = self.compose(profiles=("protonmail",))
        bridge = config["services"]["protonmail-bridge"]
        self.assertEqual(bridge["profiles"], ["protonmail"])
        self.assertEqual(bridge["container_name"], "prism-protonmail-bridge")
        self.assertTrue(
            any(m["source"] == "protonmail" and m["target"] == "/root" for m in bridge["volumes"])
        )
        self.assertEqual(config["volumes"]["protonmail"]["name"], "prism_protonmail")


if __name__ == "__main__":
    unittest.main()
