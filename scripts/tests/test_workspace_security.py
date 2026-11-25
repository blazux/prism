"""Keep cross-build library discovery separate from the security decision."""

import importlib.util
from pathlib import Path
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location(
    "workspace_security", ROOT / "scripts" / "check-workspace-security.py"
)
SECURITY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SECURITY)


class SystemDecoderTests(unittest.TestCase):
    def test_loader_can_resolve_soname_when_discovery_tools_miss_it(self):
        decoder = mock.Mock()
        decoder.BrotliDecoderErrorString.return_value = b"_ERROR_FORMAT_BLOCK_SWITCH"
        with mock.patch.object(SECURITY.ctypes.util, "find_library", return_value=None), \
                mock.patch.object(SECURITY.ctypes, "CDLL", return_value=decoder) as load:
            SECURITY.check_system_decoder()
        load.assert_called_once_with("libbrotlidec.so.1")
        decoder.BrotliDecoderErrorString.assert_called_once_with(-17)

    def test_missing_library_still_fails(self):
        with mock.patch.object(SECURITY.ctypes.util, "find_library", return_value=None), \
                mock.patch.object(SECURITY.ctypes, "CDLL", side_effect=OSError("not found")):
            with self.assertRaisesRegex(SystemExit, "Cannot load system Brotli decoder"):
                SECURITY.check_system_decoder()

    def test_discovered_library_is_checked_without_substitution(self):
        decoder = mock.Mock()
        decoder.BrotliDecoderErrorString.return_value = b"INVALID_ARGUMENTS"
        with mock.patch.object(SECURITY.ctypes.util, "find_library", return_value="/lib/decoder.so"), \
                mock.patch.object(SECURITY.ctypes, "CDLL", return_value=decoder) as load:
            with self.assertRaisesRegex(SystemExit, "lacks the block-switch fix"):
                SECURITY.check_system_decoder()
        load.assert_called_once_with("/lib/decoder.so")

    def test_unpatched_decoder_is_rejected_even_after_discovery_fallback(self):
        for result in (None, b"INVALID_ARGUMENTS", b"NO_ERROR"):
            with self.subTest(error_name=result):
                decoder = mock.Mock()
                decoder.BrotliDecoderErrorString.return_value = result
                with mock.patch.object(SECURITY.ctypes.util, "find_library", return_value=None), \
                        mock.patch.object(SECURITY.ctypes, "CDLL", return_value=decoder):
                    with self.assertRaisesRegex(SystemExit, "lacks the block-switch fix"):
                        SECURITY.check_system_decoder()

    def test_distribution_backport_and_upstream_error_names_are_accepted(self):
        for result in (b"BLOCK_SWITCH", b"_ERROR_FORMAT_BLOCK_SWITCH"):
            with self.subTest(error_name=result):
                decoder = mock.Mock()
                decoder.BrotliDecoderErrorString.return_value = result
                with mock.patch.object(SECURITY.ctypes.util, "find_library", return_value="libbrotlidec.so.1"), \
                        mock.patch.object(SECURITY.ctypes, "CDLL", return_value=decoder):
                    SECURITY.check_system_decoder()


class RuntimeSecurityTests(unittest.TestCase):
    def installed(self, **changes):
        versions = {"Brotli": "1.2.1", "brotlicffi": "1.2.1.0", "Pillow": "13.0.0",
                    "SoupSieve": "4.0.0", "playwright": "1.65.2"}
        versions.update(changes)
        def installed(name):
            if name not in versions:
                raise SECURITY.importlib.metadata.PackageNotFoundError(name)
            return versions[name]
        return mock.patch.object(SECURITY.importlib.metadata, "version", side_effect=installed)

    def test_image_codec_and_selector_floors_are_enforced(self):
        for name, old in [("Pillow", "12.3.0"), ("SoupSieve", "2.10"),
                          ("Brotli", "1.1.0"), ("playwright", "1.62.1"),
                          ("anyio", "4.12.1"), ("jupyter-server", "2.17.0"),
                          ("onnx", "1.18.0"), ("uv", "0.10.12")]:
            with self.subTest(package=name), self.installed(**{name: old}):
                with self.assertRaisesRegex(SystemExit, "Unpatched Python package"):
                    SECURITY.check_python_packages()

    def test_supported_python_packages_are_reported(self):
        with self.installed():
            packages = SECURITY.check_python_packages()
        self.assertEqual(packages["Pillow"], "13.0.0")
        self.assertEqual(packages["SoupSieve"], "4.0.0")

    def test_python_too_old_to_install_security_updates_is_rejected(self):
        with mock.patch.object(SECURITY.sys, "version_info", (3, 10, 12)):
            with self.assertRaisesRegex(SystemExit, "Python 3.11"):
                SECURITY.check_python_packages()

    def test_agent_and_browser_node_versions_and_decoders_are_checked(self):
        safe = {"node": "26.13.1", "brotli": "1.2.1", "blockSwitchError": -17}
        for label in ("workspace", "Playwright"):
            SECURITY.check_node_runtime(safe, label)
            with self.assertRaisesRegex(SystemExit, "Outdated"):
                SECURITY.check_node_runtime({**safe, "node": "12.22.9"}, label)
            with self.assertRaisesRegex(SystemExit, "Unpatched"):
                SECURITY.check_node_runtime({**safe, "blockSwitchError": None}, label)

    def test_npm_with_patched_bundled_dependencies_is_required(self):
        SECURITY.check_npm_version("12.2.3")
        for old in ("11.20.0", "12.1.0"):
            with self.subTest(version=old), self.assertRaisesRegex(SystemExit, "Outdated workspace npm"):
                SECURITY.check_npm_version(old)


if __name__ == "__main__":
    unittest.main()
