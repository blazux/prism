#!/usr/bin/env python3
"""Check the Brotli decoders actually used by the workspace; no exploit input."""
import ctypes
import ctypes.util
import importlib.metadata
import json
import re
import subprocess


def version(value):
    return tuple(int(part) for part in re.findall(r"\d+", value)[:3])


def main():
    library = ctypes.util.find_library("brotlidec")
    if not library:
        raise SystemExit("Missing system Brotli decoder")
    decoder = ctypes.CDLL(library)
    error_string = decoder.BrotliDecoderErrorString
    error_string.argtypes = [ctypes.c_int]
    error_string.restype = ctypes.c_char_p
    # The security fix adds this public error code. This also recognizes
    # distribution backports whose upstream version number is unchanged.
    # Brotli 1.0.x returns the bare name; 1.1.x+ includes the category prefix.
    if error_string(-17) not in (b"BLOCK_SWITCH", b"_ERROR_FORMAT_BLOCK_SWITCH"):
        raise SystemExit("System Brotli decoder lacks the block-switch fix")

    packages = {}
    for name in ("Brotli", "brotlicffi"):
        try:
            installed = importlib.metadata.version(name)
        except importlib.metadata.PackageNotFoundError:
            continue
        if version(installed) < (1, 2, 1):
            raise SystemExit(f"Unpatched Python decoder: {name} {installed}")
        packages[name] = installed

    from playwright._impl._driver import compute_driver_executable

    node, _ = compute_driver_executable()
    runtime = json.loads(subprocess.check_output([
        str(node), "-p",
        "JSON.stringify({node:process.versions.node,brotli:process.versions.brotli,"
        "blockSwitchError:require('node:zlib').constants.BROTLI_DECODER_ERROR_FORMAT_BLOCK_SWITCH})",
    ], text=True))
    # Node backports this fix without necessarily changing process.versions.brotli.
    if runtime.get("blockSwitchError") != -17:
        raise SystemExit(f"Unpatched Playwright Node decoder: {runtime}")
    print(json.dumps({"native_block_switch_fix": True, "python": packages,
                      "playwright": importlib.metadata.version("playwright"),
                      "driver": runtime}, sort_keys=True))


if __name__ == "__main__":
    main()
