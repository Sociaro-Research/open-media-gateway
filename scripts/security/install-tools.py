#!/usr/bin/env python3
"""Install checksum-pinned security scanners in an explicit temporary directory."""
import argparse
import hashlib
import io
import json
import pathlib
import platform
import os
import tarfile
import urllib.request

# Official release asset digests verified on 2026-10-06. Never execute an
# installer fetched from a mutable branch or use an unpinned scanner action.
ASSETS = {
    ("Darwin", "arm64"): [
        ("gitleaks/gitleaks", "v8.30.1", "gitleaks_8.30.1_darwin_arm64.tar.gz", "b40ab0ae55c505963e365f271a8d3846efbc170aa17f2607f13df610a9aeb6a5", "gitleaks"),
        ("aquasecurity/trivy", "v0.75.0", "trivy_0.75.0_macOS-ARM64.tar.gz", "4a77108cccf8e55c8d6823e1e759939a622277e66cd0daa3c1fc621ed69e4568", "trivy"),
    ],
    ("Linux", "x86_64"): [
        ("gitleaks/gitleaks", "v8.30.1", "gitleaks_8.30.1_linux_x64.tar.gz", "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb", "gitleaks"),
        ("aquasecurity/trivy", "v0.75.0", "trivy_0.75.0_Linux-64bit.tar.gz", "c6e65abddb348e25f10549df887045629cf28cc72453cd1c63acb717316b3f3f", "trivy"),
    ],
    ("Linux", "aarch64"): [
        ("gitleaks/gitleaks", "v8.30.1", "gitleaks_8.30.1_linux_arm64.tar.gz", "e4a487ee7ccd7d3a7f7ec08657610aa3606637dab924210b3aee62570fb4b080", "gitleaks"),
        ("aquasecurity/trivy", "v0.75.0", "trivy_0.75.0_Linux-ARM64.tar.gz", "a1ee9f6ffb7d112b64ff726a2a0717c21175c1114361391f4a132956751a13b3", "trivy"),
    ],
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bin-dir", required=True)
    args = parser.parse_args()
    assets = ASSETS.get((platform.system(), platform.machine()))
    if not assets:
        parser.error("unsupported platform; verify official release checksums before adding it")
    directory = pathlib.Path(args.bin_dir).resolve()
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    if directory.stat().st_uid != os.getuid():
        raise RuntimeError("tool directory must belong to the current user")
    directory.chmod(0o700)
    provenance = []
    for repo, version, filename, expected, binary in assets:
        url = f"https://github.com/{repo}/releases/download/{version}/{filename}"
        request = urllib.request.Request(url, headers={"User-Agent": "Sociaro-Gateway-Security-Checks"})
        with urllib.request.urlopen(request, timeout=60) as response:
            data = response.read()
        actual = hashlib.sha256(data).hexdigest()
        if actual != expected:
            raise RuntimeError(f"{binary}: release checksum mismatch; refusing to install")
        with tarfile.open(fileobj=io.BytesIO(data)) as archive:
            member = archive.getmember(binary)
            if not member.isfile():
                raise RuntimeError(f"{binary}: expected a regular binary")
            with archive.extractfile(member) as source:
                target = directory / binary
                if target.is_symlink():
                    raise RuntimeError(f"{binary}: refusing a symlink destination")
                target.write_bytes(source.read())
                target.chmod(0o700)
        provenance.append({"tool": binary, "version": version, "source": url, "sha256": actual})
        print(f"Installed {binary} {version}: SHA-256 verified", flush=True)
    (directory / "provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")


if __name__ == "__main__":
    main()
