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


if __name__ == "__main__":
    unittest.main()
