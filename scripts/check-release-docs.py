#!/usr/bin/env python3
"""Check release documentation against the release workflow's version tag."""
import os
from pathlib import Path
import re
import sys

VERSION = r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?"


def check(root, release_version=""):
    # Only inspect the top-level info block, never a schema's version property.
    source = (root / "docs/openapi.yaml").read_text()
    info = re.search(r"^info:\n((?:[ \t].*\n|\n)+)", source, re.MULTILINE)
    versions = re.findall(r"^  version: ([^\n]+)$", info[1], re.MULTILINE) if info else []
    if len(versions) != 1 or not re.fullmatch(VERSION, versions[0]):
        raise ValueError("OpenAPI info.version must be a single unquoted release version")
    if release_version:
        if not re.fullmatch("v" + VERSION, release_version):
            raise ValueError("RELEASE_VERSION must be a version tag such as vX.Y.Z")
        if versions[0] != release_version[1:]:
            raise ValueError(f"OpenAPI info.version {versions[0]} does not match {release_version}")
    for name in ("README.md", "SECURITY.md"):
        text = (root / name).read_text().lower()
        if "early preview" in text or "first stable release" in text:
            raise ValueError(f"{name} still describes the project as awaiting its first release")
    return versions[0]


if __name__ == "__main__":
    try:
        version = check(Path(__file__).resolve().parents[1], os.environ.get("RELEASE_VERSION", ""))
    except ValueError as error:
        sys.exit(str(error))
    print(f"Release documentation passed: OpenAPI {version}")
