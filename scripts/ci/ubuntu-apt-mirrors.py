#!/usr/bin/env python3
"""Use existing Ubuntu archive fallbacks when the Azure mirror trickles packages."""
from pathlib import Path
from urllib.parse import urlsplit


def configure(path):
    if not path.exists():
        return False
    lines = path.read_text().splitlines()
    retained = [line for line in lines
                if urlsplit(line.strip()).hostname != "azure.archive.ubuntu.com"]
    if retained == lines:
        return False
    if not any(urlsplit(line.strip()).hostname == "archive.ubuntu.com"
               for line in retained):
        raise RuntimeError("No existing official Ubuntu archive fallback; leaving mirrors unchanged")
    path.write_text("\n".join(retained) + "\n")
    return True


if __name__ == "__main__":
    changed = configure(Path("/etc/apt/apt-mirrors.txt"))
    print("Using existing Ubuntu archive fallbacks." if changed else "Ubuntu mirrors unchanged.")
