#!/usr/bin/env python3
"""Check workspace runtimes and package security floors; no exploit input."""
import ctypes
import ctypes.util
import importlib.metadata
import json
import re
import subprocess
import sys


def version(value):
    return tuple(int(part) for part in re.findall(r"\d+", value)[:3])


def check_system_decoder():
    library = ctypes.util.find_library("brotlidec")
    # Discovery invokes external tools which can fail under cross-build
    # emulation. The Linux loader can still resolve the installed SONAME,
    # including its architecture-specific library directory.
    try:
        decoder = ctypes.CDLL(library or "libbrotlidec.so.1")
    except OSError as error:
        raise SystemExit(f"Cannot load system Brotli decoder: {error}") from error
    error_string = decoder.BrotliDecoderErrorString
    error_string.argtypes = [ctypes.c_int]
    error_string.restype = ctypes.c_char_p
    # The security fix adds this public error code. This also recognizes
    # distribution backports whose upstream version number is unchanged.
    # Brotli 1.0.x returns the bare name; 1.1.x+ includes the category prefix.
    if error_string(-17) not in (b"BLOCK_SWITCH", b"_ERROR_FORMAT_BLOCK_SWITCH"):
        raise SystemExit("System Brotli decoder lacks the block-switch fix")


def check_python_packages():
    if sys.version_info < (3, 11):
        raise SystemExit("Python 3.11 or newer is required for patched image codecs")
    floors = {"Brotli": (1, 2, 1), "brotlicffi": (1, 2, 1),
              "Pillow": (13, 0, 0), "SoupSieve": (4, 0, 0),
              "playwright": (1, 64, 1), "aiohttp": (3, 14, 4),
              "anyio": (4, 14, 2), "black": (26, 10, 1),
              "filelock": (4, 2, 1), "fsspec": (2026, 6, 0), "datasets": (5, 0, 1),
              "jupyter-server": (2, 21, 0), "jupyterlab": (4, 6, 4),
              "mistune": (3, 3, 4), "msgpack": (1, 2, 3),
              "nbconvert": (7, 17, 2), "notebook": (7, 6, 3),
              "onnx": (1, 21, 0), "tornado": (6, 5, 9),
              "urllib3": (2, 8, 0), "uv": (0, 13, 4)}
    required = {"Pillow", "SoupSieve", "playwright"}
    packages = {}
    for name, minimum in floors.items():
        try:
            installed = importlib.metadata.version(name)
        except importlib.metadata.PackageNotFoundError:
            if name in required:
                raise SystemExit(f"Missing workspace package: {name}")
            continue
        if version(installed) < minimum:
            raise SystemExit(f"Unpatched Python package: {name} {installed}")
        packages[name] = installed
    return packages


def node_runtime(node):
    return json.loads(subprocess.check_output([
        str(node), "-p",
        "JSON.stringify({node:process.versions.node,brotli:process.versions.brotli,"
        "blockSwitchError:require('node:zlib').constants.BROTLI_DECODER_ERROR_FORMAT_BLOCK_SWITCH})",
    ], text=True))


def check_node_runtime(runtime, label):
    if version(runtime.get("node", "0")) < (24, 0, 0):
        raise SystemExit(f"Outdated {label} Node runtime: {runtime}")
    # Node backports this fix without necessarily changing process.versions.brotli.
    if runtime.get("blockSwitchError") != -17:
        raise SystemExit(f"Unpatched {label} Node decoder: {runtime}")


def check_npm_version(installed):
    if version(installed) < (12, 2, 3):
        raise SystemExit(f"Outdated workspace npm: {installed}")


def main():
    check_system_decoder()
    packages = check_python_packages()
    from playwright._impl._driver import compute_driver_executable

    node, _ = compute_driver_executable()
    driver = node_runtime(node)
    runtime = node_runtime("node")
    check_node_runtime(driver, "Playwright")
    check_node_runtime(runtime, "workspace")
    npm = subprocess.check_output(["npm", "--version"], text=True).strip()
    check_npm_version(npm)
    print(json.dumps({"native_block_switch_fix": True, "python": packages,
                      "python_runtime": sys.version.split()[0], "node": runtime,
                      "npm": npm, "driver": driver}, sort_keys=True))


if __name__ == "__main__":
    main()
