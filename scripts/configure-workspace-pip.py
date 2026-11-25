#!/usr/bin/env python3
"""Apply workspace package floors without changing CUDA/PyTorch constraints."""

import configparser
import os
from pathlib import Path
import re
import shlex


PACKAGE = re.compile(r"^\s*([A-Za-z0-9][A-Za-z0-9_.-]*)")


def package_name(line):
    match = PACKAGE.match(line)
    return re.sub(r"[-_.]+", "-", match[1]).lower() if match else None


def merge_constraints(floors, upstream):
    content = floors.read_text()
    managed = {package_name(line) for line in content.splitlines()} - {None}
    visited = {floors.resolve()}

    def retained(path):
        path = path.resolve()
        if path in visited:
            return []
        visited.add(path)
        lines = []
        pending = ""
        for physical in path.read_text().splitlines():
            line = pending + physical.strip()
            if line.endswith("\\"):
                pending = line[:-1] + " "
                continue
            pending = ""
            words = shlex.split(line, comments=True)
            if words and words[0] in ("-c", "--constraint", "-r", "--requirement"):
                lines.extend(retained(path.parent / words[1]))
            elif words and words[0].startswith("--constraint="):
                lines.extend(retained(path.parent / words[0].split("=", 1)[1]))
            elif package_name(line) not in managed:
                lines.append(line)
        if pending:
            raise ValueError(f"Unfinished constraint line in {path}")
        return lines

    for path in upstream:
        lines = retained(Path(path))
        if lines:
            content += "\n" + "\n".join(lines) + "\n"
    floors.write_text(content)


def configure(floors, config_path, upstream):
    merge_constraints(floors, upstream)
    config = configparser.ConfigParser(interpolation=None)
    config.read(config_path)
    if not config.has_section("global"):
        config.add_section("global")
    config["global"]["break-system-packages"] = "true"
    config["global"]["constraint"] = str(floors)
    with config_path.open("w") as output:
        config.write(output)


if __name__ == "__main__":
    configure(
        Path("/etc/prism/python-constraints.txt"),
        Path("/etc/pip.conf"),
        shlex.split(os.environ.get("PIP_CONSTRAINT", "")),
    )
