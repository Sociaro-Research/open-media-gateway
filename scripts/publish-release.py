#!/usr/bin/env python3
"""Combine verified architecture digests without overwriting released versions."""
import argparse
import importlib.util
import json
from pathlib import Path
import re
import subprocess

ROOT = "ghcr.io/sociaro-research/open-media-gateway"
NAMES = ("gateway", "ui", "postgres", "redis")


def command(*args):
    return subprocess.check_output(["docker", "buildx", "imagetools", *args], text=True)


def manifest(ref):
    result = subprocess.run(["docker", "buildx", "imagetools", "inspect", "--raw", ref], capture_output=True, text=True)
    if result.returncode:
        # Do not treat authentication/network failures as an absent version.
        if "not found" in result.stderr.lower() or "manifest unknown" in result.stderr.lower():
            return None
        raise RuntimeError("Cannot inspect release reference: " + ref + ": " + result.stderr)
    return json.loads(result.stdout)


def publish(version, evidence, output):
    spec = importlib.util.spec_from_file_location("package_release", Path(__file__).with_name("package-release.py"))
    package = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(package)
    if not package.VERSION.fullmatch(version):
        raise ValueError("Expected a version tag")
    inputs = {}
    for name in NAMES:
        refs = [(evidence / (name + "-" + arch + ".digest")).read_text().strip() for arch in ("amd64", "arm64")]
        if not all(re.fullmatch(re.escape(ROOT + "/" + name) + r"@sha256:[a-f0-9]{64}", ref) for ref in refs):
            raise ValueError("Invalid verified image digest: " + name)
        inputs[name] = refs
        existing = manifest(ROOT + "/" + name + ":" + version)
        if existing and {m["digest"] for m in existing.get("manifests", [])} != {r.split("@")[1] for r in refs}:
            raise RuntimeError("Refusing to replace a released version: " + name + ":" + version)
    images = {}
    for name, refs in inputs.items():
        tag = ROOT + "/" + name + ":" + version
        command("create", "--tag", tag, *refs)
        index = manifest(tag)
        platforms = {(m.get("platform", {}).get("os"), m.get("platform", {}).get("architecture")) for m in index["manifests"]}
        if platforms != {("linux", "amd64"), ("linux", "arm64")}:
            raise RuntimeError("Incorrect release platforms: " + name)
        digest = command("inspect", "--format", "{{.Manifest.Digest}}", tag).strip()
        if not re.fullmatch(r"sha256:[a-f0-9]{64}", digest):
            raise RuntimeError("Invalid manifest digest")
        images[name] = ROOT + "/" + name + "@" + digest
        print("Published " + tag)
    output.mkdir(parents=True, exist_ok=True)
    (output / "images.json").write_text(json.dumps(images, indent=2) + "\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--evidence", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    publish(args.version, args.evidence, args.output)
