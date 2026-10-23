#!/usr/bin/env python3
"""Package only allowlisted runtime configuration; never copy a local .env."""
import argparse
import hashlib
import json
import posixpath
from pathlib import Path
import re
import shutil
import tarfile
import tempfile
from urllib.parse import quote, unquote, urlsplit, urlunsplit

ROOT = Path(__file__).resolve().parents[1]
VERSION = re.compile(r"v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?\Z")
IMAGES = ("gateway", "ui", "postgres", "redis")
SOURCE_URL = "https://github.com/Sociaro-Research/open-media-gateway"
FILES = {
    "compose.yaml": "compose.yaml",
    "compose.release.yaml": "compose.release.yaml",
    "compose.https.yaml": "compose.https.yaml",
    "deploy/ghcr/Caddyfile.https": "Caddyfile.https",
    "deploy/ghcr/initialize.py": "initialize.py",
    "deploy/ghcr/README.md": "README.md",
    "scripts/stop-worker-for-release.py": "stop-worker-for-release.py",
    "LICENSE": "LICENSE",
    "NOTICE": "NOTICE",
    "deploy/redis/licenses/README.md": "licenses/redis/README.md",
    "deploy/redis/licenses/LICENSE.txt": "licenses/redis/LICENSE.txt",
    "deploy/redis/licenses/REDISCONTRIBUTIONS.txt": "licenses/redis/REDISCONTRIBUTIONS.txt",
    "deploy/redis/licenses/SOURCE-NOTICES.txt": "licenses/redis/SOURCE-NOTICES.txt",
    "deploy/redis/licenses/provenance.json": "licenses/redis/provenance.json",
    "deploy/redis/licenses/deps/fpconv/LICENSE.txt": "licenses/redis/deps/fpconv/LICENSE.txt",
    "deploy/redis/licenses/deps/hdr_histogram/COPYING.txt": "licenses/redis/deps/hdr_histogram/COPYING.txt",
    "deploy/redis/licenses/deps/hdr_histogram/LICENSE.txt": "licenses/redis/deps/hdr_histogram/LICENSE.txt",
    "deploy/redis/licenses/deps/hiredis/COPYING": "licenses/redis/deps/hiredis/COPYING",
    "deploy/redis/licenses/deps/jemalloc/COPYING": "licenses/redis/deps/jemalloc/COPYING",
    "deploy/redis/licenses/deps/lua/COPYRIGHT": "licenses/redis/deps/lua/COPYRIGHT",
    "docs/configuration.md": "docs/configuration.md",
    "docs/windows-wsl.md": "docs/windows-wsl.md",
    "docs/frontend-separation.md": "docs/frontend-separation.md",
    "docs/system-administration.md": "docs/system-administration.md",
    "docs/email-authentication.md": "docs/email-authentication.md",
    "docs/openai-api.md": "docs/openai-api.md",
    "monitoring/prometheus.yml": "monitoring/prometheus.yml",
    "monitoring/alerts.yml": "monitoring/alerts.yml",
    "monitoring/grafana/provisioning/dashboards/gateway.yml": "monitoring/grafana/provisioning/dashboards/gateway.yml",
    "monitoring/grafana/provisioning/datasources/prometheus.yml": "monitoring/grafana/provisioning/datasources/prometheus.yml",
    "monitoring/grafana/dashboards/gateway-overview.json": "monitoring/grafana/dashboards/gateway-overview.json",
}


def rewrite_bundle_documentation(directory, version):
    """Keep shipped links local; point source-only guides at this release tag."""
    for document in directory.rglob('*.md'):
        relative = document.relative_to(directory).as_posix()

        def replace(match):
            target, separator, title = match.group(2).strip().partition(' "')
            target = target.strip('<>')
            parsed = urlsplit(target)
            if not target or target.startswith(('#', '/')) or parsed.scheme or parsed.netloc:
                return match.group(0)
            path = posixpath.normpath(posixpath.join(posixpath.dirname(relative), unquote(parsed.path)))
            if path == '..' or path.startswith('../'):
                raise ValueError('Bundled documentation target escapes its source root')
            if (directory / path).exists():
                return match.group(0)
            if not (ROOT / path).is_file():
                raise ValueError('Bundled documentation target is missing from source: ' + path)
            address = SOURCE_URL + '/blob/' + version + '/' + quote(path, safe='/')
            address = urlunsplit((*urlsplit(address)[:3], parsed.query, parsed.fragment))
            suffix = separator + title if separator else ''
            return match.group(1) + address + suffix + match.group(3)

        chunks = re.split(r'(```.*?```)', document.read_text(), flags=re.S)
        for index in range(0, len(chunks), 2):
            chunks[index] = re.sub(r'(?<!!)(\[[^\]\n]*\]\()([^\n)]+)(\))', replace, chunks[index])
        document.write_text(''.join(chunks))


def package(version, output, image_refs=None):
    if not VERSION.fullmatch(version):
        raise ValueError("Expected a release version such as v0.1.0 or v0.1.0-rc.1")
    refs = {name: f"ghcr.io/sociaro-research/open-media-gateway/{name}:{version}" for name in IMAGES}
    if image_refs is not None:
        if set(image_refs) != set(IMAGES):
            raise ValueError("Provide digest references for all four images")
        for name, ref in image_refs.items():
            if not re.fullmatch(r"ghcr\.io/sociaro-research/open-media-gateway/" + name + r"@sha256:[a-f0-9]{64}", ref):
                raise ValueError("Invalid immutable image reference")
        refs = image_refs
    output.mkdir(parents=True, exist_ok=True)
    archive = output / ("media-gateway-" + version + ".tar.gz")
    if archive.exists():
        raise FileExistsError(archive)
    with tempfile.TemporaryDirectory(prefix="gateway-release-") as temporary:
        directory = Path(temporary) / ("media-gateway-" + version)
        directory.mkdir()
        for source, target in FILES.items():
            (directory / target).parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / source, directory / target)
        template = (ROOT / "deploy/ghcr/env.example").read_text().replace("@VERSION@", version)
        for name, ref in refs.items():
            template = re.sub(r"(?m)^" + name.upper() + "_IMAGE=.*$", name.upper() + "_IMAGE=" + ref, template)
        (directory / ".env.example").write_text(template)
        (directory / "images.json").write_text(json.dumps({"version": version, "images": refs}, indent=2) + "\n")
        (directory / "README.md").write_text((directory / "README.md").read_text().replace("@VERSION@", version))
        rewrite_bundle_documentation(directory, version)
        # Readable modes are deliberate: Grafana's UID 472 must traverse mounted directories.
        with tarfile.open(archive, "w:gz") as tar:
            for path in [directory, *sorted(directory.rglob("*"))]:
                info = tar.gettarinfo(str(path), str(path.relative_to(directory.parent)))
                info.uid = info.gid = 0
                info.uname = info.gname = ""
                info.mode = 0o755 if path.is_dir() else 0o644
                info.mtime = 0
                if path.is_file():
                    with path.open("rb") as stream:
                        tar.addfile(info, stream)
                else:
                    tar.addfile(info)
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    (output / (archive.name + ".sha256")).write_text(digest + "  " + archive.name + "\n")
    return archive


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", type=Path, default=ROOT / "dist/releases")
    parser.add_argument("--images", type=Path, help="JSON map of image names to GHCR digest references")
    args = parser.parse_args()
    print(package(args.version, args.output, json.loads(args.images.read_text()) if args.images else None))
