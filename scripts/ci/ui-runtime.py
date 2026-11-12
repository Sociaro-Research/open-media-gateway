#!/usr/bin/env python3
"""Bind browser jobs to the exact UI/Caddy artifact built by their Container job."""
import argparse
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import re
import tarfile

MAX_BYTES = 512 * 1024 * 1024


def digest(data): return hashlib.sha256(data).hexdigest()


def require(value, message):
    if not value: raise ValueError(message)


def pack(staging, output, source_sha, image_id):
    require(bool(re.fullmatch('[0-9a-f]{40}', source_sha)), 'Exact source SHA required')
    require(bool(re.fullmatch('sha256:[0-9a-f]{64}', image_id)), 'Exact built image ID required')
    files = {}
    total = 0
    for path in sorted(staging.rglob('*')):
        require(not path.is_symlink(), 'UI runtime must not contain symlinks')
        if path.is_dir(): continue
        require(path.is_file(), 'Unexpected runtime file type')
        name = path.relative_to(staging).as_posix()
        require(name in ('caddy', 'Caddyfile', 'gateway-ui.tar.gz') or name.startswith('public/'), 'Unexpected runtime path')
        data = path.read_bytes();total += len(data)
        require(total <= MAX_BYTES, 'UI runtime too large')
        files[name] = {'sha256': digest(data), 'size': len(data), 'mode': 0o755 if name == 'caddy' else 0o644}
    require({'caddy', 'Caddyfile', 'gateway-ui.tar.gz', 'public/ui/index.html'} <= files.keys(), 'Incomplete UI runtime')
    manifest = {'source_sha': source_sha, 'image_id': image_id, 'files': files}
    with tarfile.open(output, 'w:gz') as archive:
        for name, meta in files.items():
            member = tarfile.TarInfo(name);member.size = meta['size'];member.mode = meta['mode']
            archive.addfile(member, io.BytesIO((staging / name).read_bytes()))
        data = json.dumps(manifest, sort_keys=True).encode()
        member = tarfile.TarInfo('runtime.json');member.size = len(data);member.mode = 0o644
        archive.addfile(member, io.BytesIO(data))
    return digest(output.read_bytes())


def restore(bundle, expected_sha, source_sha, image_id, root, runtime):
    require(bundle.stat().st_size <= MAX_BYTES, 'UI artifact transport too large')
    require(digest(bundle.read_bytes()) == expected_sha, 'UI artifact transport digest mismatch')
    files = {};total = 0
    with tarfile.open(bundle, 'r:gz') as archive:
        for member in archive:
            path = PurePosixPath(member.name)
            require(not path.is_absolute() and '..' not in path.parts and member.name not in files,
                    'Unsafe or duplicate UI artifact path')
            require(member.isfile(), 'UI artifact links/special files refused')
            total += member.size
            require(0 <= member.size <= 128 * 1024 * 1024 and total <= MAX_BYTES, 'UI artifact too large')
            files[member.name] = archive.extractfile(member).read()
    require('runtime.json' in files, 'Missing UI runtime identity')
    manifest = json.loads(files.pop('runtime.json'))
    require(manifest.get('source_sha') == source_sha and manifest.get('image_id') == image_id,
            'UI artifact source/image identity mismatch')
    require(set(files) == set(manifest['files']), 'UI artifact inventory mismatch')
    require({'caddy', 'Caddyfile', 'gateway-ui.tar.gz', 'public/ui/index.html'} <= files.keys(), 'Incomplete UI runtime')
    for name, data in files.items():
        require(name in ('caddy', 'Caddyfile', 'gateway-ui.tar.gz') or name.startswith('public/'), 'Unexpected runtime path')
        meta = manifest['files'][name]
        require(digest(data) == meta['sha256'] and len(data) == meta['size'], 'UI runtime file digest mismatch')
    # The Dockerfile copies these bytes without transformation. Fail rather than testing a different UI tree.
    public = {f.relative_to(root / 'web/public').as_posix(): f for f in (root / 'web/public').rglob('*') if f.is_file()}
    require(set(public) == {n[7:] for n in files if n.startswith('public/')}, 'Checkout/image UI inventory mismatch')
    for name, path in public.items():
        require(path.read_bytes() == files['public/' + name], 'Checkout/image UI bytes differ')
    require((root / 'web/Caddyfile').read_bytes() == files['Caddyfile'], 'Checkout/image Caddy configuration differs')
    runtime.mkdir(parents=True, exist_ok=False)
    (runtime / 'caddy').write_bytes(files['caddy']);(runtime / 'caddy').chmod(0o755)
    (root / 'dist').mkdir(exist_ok=True)
    (root / 'dist/gateway-ui.tar.gz').write_bytes(files['gateway-ui.tar.gz'])
    (runtime / 'runtime.json').write_text(json.dumps(manifest, indent=2) + '\n')
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    build = sub.add_parser('pack');build.add_argument('--staging', type=Path, required=True)
    build.add_argument('--output', type=Path, required=True)
    read = sub.add_parser('restore');read.add_argument('--bundle', type=Path, required=True)
    read.add_argument('--sha256', required=True);read.add_argument('--root', type=Path, default=Path.cwd())
    read.add_argument('--runtime', type=Path, default=Path('.local/ci-ui-runtime'))
    for cmd in (build, read):
        cmd.add_argument('--source-sha', required=True);cmd.add_argument('--image-id', required=True)
    args = parser.parse_args()
    if args.command == 'pack':
        print(pack(args.staging, args.output, args.source_sha, args.image_id))
    else:
        result = restore(args.bundle, args.sha256, args.source_sha, args.image_id, args.root, args.runtime)
        print(json.dumps({'source_sha': result['source_sha'], 'image_id': result['image_id'], 'files': len(result['files'])}))


if __name__ == '__main__': main()
