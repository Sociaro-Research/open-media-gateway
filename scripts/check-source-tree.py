#!/usr/bin/env python3
"""Keep private development records and broken documentation out of source releases."""
from pathlib import Path
import re
import subprocess
from urllib.parse import unquote


def source_files(root):
    names = subprocess.check_output(
        ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"], cwd=root
    ).decode().split("\0")
    # Pending removals are intentionally absent from the next source tree.
    return sorted(name for name in set(names) if name and
                  ((root / name).is_file() or (root / name).is_symlink()))


def private_artifact(name):
    path = Path(name)
    if any(part in (".local", "node_modules", "__pycache__", ".pytest_cache") for part in path.parts):
        return True
    if name == "README.en.md" or name.startswith(("docs/audits/", "deploy/macos/")):
        return True
    if path.parent == Path("docs") and re.search(r"(?:-plan|-audit|testing-report)\.md$", path.name):
        return True
    # Recorded upstream responses are legitimate inputs to regression tests.
    return "testdata" not in path.parts and path.suffix.lower() in (
        ".log", ".patch", ".pyc", ".pyo", ".tmp", ".bak", ".orig", ".rej"
    )


def check(root):
    root = Path(root)
    errors = []
    files = source_files(root)
    for name in files:
        path = root / name
        if path.is_symlink():
            errors.append(f"{name}: source release must not contain symlinks")
            continue
        if private_artifact(name):
            errors.append(f"{name}: keep private records/build output outside the public tree")
        if path.suffix != ".md":
            continue
        text = re.sub(r"```.*?```", "", path.read_text(), flags=re.S)
        for match in re.finditer(r"(?<!!)\[[^\]\n]*\]\(([^)\n]+)\)", text):
            target = match.group(1).strip().split(' "', 1)[0].strip("<>")
            if not target or target.startswith(("#", "/")) or re.match(r"[a-zA-Z][a-zA-Z0-9+.-]*:", target):
                continue
            relative = unquote(target.split("#", 1)[0])
            if relative and not (path.parent / relative).exists():
                errors.append(f"{name}: missing documentation target {relative}")
    return files, errors


if __name__ == "__main__":
    files, errors = check(Path(__file__).resolve().parents[1])
    if errors:
        print("Source release check failed:")
        for error in errors:
            print("  " + error)
        raise SystemExit(1)
    print(f"Source tree passed: {len(files)} files; no private records or broken local Markdown links.")
