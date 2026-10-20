#!/usr/bin/env python3
"""Server-side deployment, shared safety gates ported from the local launcher."""
import datetime
import fcntl
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import subprocess
import sys
import tarfile
import tempfile

BASE = Path('/opt/media-gateway')
APP = BASE / 'app'
PROTECTED = ('.env', 'Caddyfile', 'compose.staging.yaml')
COMPOSE = ['docker', 'compose', '--profile', 'ui', '-f', 'compose.yaml', '-f', 'compose.staging.yaml']


def deployed_commit():
    # The manual frontend upgrade introduced this canonical marker. Prefer it
    # over the legacy tool marker; update both after the next verified release.
    current = BASE / 'current-release'
    return (current if current.exists() else BASE / 'deployed-commit').read_text().strip()


def command(args, *, stdin=subprocess.DEVNULL, stdout=None):
    return subprocess.run(args, cwd=APP, stdin=stdin, stdout=stdout, check=True)


def capture(args):
    return command(args, stdout=subprocess.PIPE).stdout.decode().strip()


def config_hashes():
    return {p: hashlib.sha256((APP / p).read_bytes()).hexdigest() for p in PROTECTED}


def safe_path(name):
    if not isinstance(name, str):
        raise RuntimeError('Release paths must be strings.')
    path = PurePosixPath(name)
    if (path.is_absolute() or '..' in path.parts or not path.parts or
            str(path) != name.rstrip('/') or path.parts[0] in (*PROTECTED, '.git')):
        raise RuntimeError('Unsafe archive/deletion path: ' + name)
    return path


def check_target(name):
    path = safe_path(name)
    target = APP
    for part in path.parts:
        target = target / part
        if target.is_symlink():
            raise RuntimeError('Release target contains a server symlink: ' + name)
    if not target.resolve().is_relative_to(APP.resolve()):
        raise RuntimeError('Release target escapes application directory.')
    return target


def backup(sha):
    require_backup_space()
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    folder = BASE / 'backups' / (stamp + '-' + sha[:12])
    folder.mkdir(parents=True, mode=0o700)
    print('Backup: ' + str(folder), flush=True)
    command(['tar', '-czf', str(folder / 'application.tar.gz'), '-C', str(BASE), 'app'])
    with (folder / 'database.dump').open('wb') as out:
        command(COMPOSE + ['exec', '-T', 'postgres', 'pg_dump', '-U', 'media_gateway', '-d', 'media_gateway', '-Fc'], stdout=out)
    for name in ('application.tar.gz', 'database.dump'):
        if (folder / name).stat().st_size == 0:
            raise RuntimeError('Empty backup: ' + name)
    command(['tar', '-tzf', str(folder / 'application.tar.gz')], stdout=subprocess.DEVNULL)
    with (folder / 'database.dump').open('rb') as source:
        command(COMPOSE + ['exec', '-T', 'postgres', 'pg_restore', '--list'], stdin=source, stdout=subprocess.DEVNULL)
    hashes = config_hashes()
    (folder / 'protected-config.json').write_text(json.dumps(hashes, indent=2) + '\n')
    (folder / 'previous-commit').write_text(deployed_commit() + '\n')
    images = {}
    for service in ('gateway', 'worker', 'ui', 'caddy'):
        container = capture(COMPOSE + ['ps', '-q', service])
        if container:
            images[service] = capture(['docker', 'inspect', '--format', '{{.Image}}', container])
    (folder / 'previous-images.json').write_text(json.dumps(images, indent=2) + '\n')
    print('Application/database backups verified.', flush=True)
    return folder, hashes


def require_backup_space():
    free = shutil.disk_usage(BASE).free
    minimum = 4 * 1024 ** 3
    if free < minimum:
        raise RuntimeError('Insufficient server disk space: %.1f GiB available; at least 4 GiB is required before backup and build. Reclaim disposable build cache or add space, preserving backups, images and volumes.' % (free / 1024 ** 3))


def apply_archive(archive, deleted):
    # Check every deletion before removing any file; retries tolerate an already
    # removed source file, but never discard a server-side source modification.
    for name, digest in deleted.items():
        safe_path(name)
        target = check_target(name)
        if not target.resolve().is_relative_to(APP.resolve()) or target.is_symlink():
            raise RuntimeError('Unsafe obsolete source path: ' + name)
        if target.exists() and (not target.is_file() or hashlib.sha256(target.read_bytes()).hexdigest() != digest):
            raise RuntimeError('Obsolete source was modified on the server: ' + name)
    # Only previously tracked, now-deleted paths are removed; runtime files stay intact.
    for name in deleted:
        safe_path(name)
        target = check_target(name)
        if not target.resolve().is_relative_to(APP.resolve()):
            raise RuntimeError('Deletion escapes application directory.')
        if target.is_file() or target.is_symlink():
            target.unlink()
    # Extract directories/files explicitly; reject links and traversal before reaching here.
    for member in archive.getmembers():
        target = check_target(member.name)
        if not target.resolve().is_relative_to(APP.resolve()):
            raise RuntimeError('Archive target escapes application directory.')
        if member.isdir():
            target.mkdir(parents=True, exist_ok=True)
            # The runner uses umask 077; mounted provisioning must remain readable.
            target.chmod(member.mode & 0o777)
        else:
            target.parent.mkdir(parents=True, exist_ok=True)
            with archive.extractfile(member) as source, target.open('wb') as out:
                shutil.copyfileobj(source, out)
            target.chmod(member.mode & 0o777)


def verify_source(files):
    for name, digest in files.items():
        safe_path(name)
        target = APP / name
        if (not target.is_file() or target.is_symlink() or
                not target.resolve().is_relative_to(APP.resolve()) or
                hashlib.sha256(target.read_bytes()).hexdigest() != digest):
            raise RuntimeError('Release source hash mismatch: ' + name)


def validate_frontend_configuration():
    command(COMPOSE + ['config', '--quiet'])
    config = json.loads(capture(COMPOSE + ['config', '--format', 'json']))
    services = config.get('services', {})
    if not {'gateway', 'worker', 'ui', 'caddy', 'postgres', 'redis'}.issubset(services):
        raise RuntimeError('The deployment must include the independent UI and TLS proxy services.')
    from urllib.parse import urlsplit
    docs_url = services['gateway'].get('environment', {}).get('DOCS_PUBLIC_URL', '')
    docs_host = services['ui'].get('environment', {}).get('DOCS_HOST', '')
    if docs_url and urlsplit(docs_url).hostname != docs_host:
        raise RuntimeError('DOCS_HOST must match DOCS_PUBLIC_URL in the preserved server configuration.')
    command(COMPOSE + ['exec', '-T', 'caddy', 'caddy', 'validate', '--config', '/etc/caddy/Caddyfile', '--adapter', 'caddyfile'])


def record_release(sha, files, folder):
    directory = BASE / 'releases' / sha
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    (directory / 'sha.txt').write_text(sha + '\n')
    (directory / 'tracked-files.txt').write_text('\n'.join(files) + '\n')
    (directory / 'file-hashes.json').write_text(json.dumps(files, indent=2) + '\n')
    report = {'sha': sha, 'backup': str(folder), 'verified_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'checks': ['source hashes', 'Docker builds', 'Caddy configuration', 'healthy gateway and UI',
                         'migrations', 'public UI/docs/auth guards', 'free mock and signed download']}
    (directory / 'deployment.json').write_text(json.dumps(report, indent=2) + '\n')
    for name in ('deployed-commit', 'current-release'):
        marker = BASE / (name + '.tmp')
        marker.write_text(sha + '\n')
        marker.replace(BASE / name)


def verify_migrations():
    expected = sorted(int(p.name.split('_', 1)[0]) for p in (APP / 'internal/postgres/migrations').glob('[0-9]*_*.sql'))
    sql = ('SELECT version_id FROM (SELECT DISTINCT ON (version_id) version_id,is_applied '
           'FROM goose_db_version ORDER BY version_id,id DESC) latest '
           'WHERE is_applied AND version_id > 0 ORDER BY version_id;')
    actual = capture(COMPOSE + ['exec', '-T', 'postgres', 'psql', '-X', '-At', '-v', 'ON_ERROR_STOP=1',
                               '-U', 'media_gateway', '-d', 'media_gateway', '-c', sql])
    if [int(v) for v in actual.splitlines()] != expected:
        raise RuntimeError('Database migrations do not match the released source.')
    print('Migrations verified through ' + str(max(expected)), flush=True)


def deploy(sha, digest, previous, manifest, source):
    if deployed_commit() != previous:
        raise RuntimeError('Another release changed the server. Rerun from the current main.')
    with tempfile.TemporaryDirectory(prefix='release-', dir=BASE) as temporary:
        upload = Path(temporary) / 'release.tar'
        with upload.open('wb') as target:
            shutil.copyfileobj(source, target)
        if hashlib.sha256(upload.read_bytes()).hexdigest() != digest:
            raise RuntimeError('Uploaded archive checksum mismatch.')
        with tarfile.open(upload) as archive:
            if sum(member.size for member in archive.getmembers()) > 512 * 1024 * 1024:
                raise RuntimeError('Expanded release archive exceeds 512 MiB.')
            names = set()
            for member in archive.getmembers():
                safe_path(member.name)
                if member.name.rstrip('/') in names:
                    raise RuntimeError('Duplicate release archive path.')
                names.add(member.name.rstrip('/'))
                check_target(member.name)
                if not (member.isfile() or member.isdir()):
                    raise RuntimeError('Release archives must contain only regular files and directories.')
            deleted, files = manifest['deleted'], manifest['files']
            members = {member.name for member in archive.getmembers() if member.isfile()}
            if members != set(files) or members.intersection(deleted):
                raise RuntimeError('Release manifest does not match its archive.')
            for member in archive.getmembers():
                if member.isfile():
                    with archive.extractfile(member) as content:
                        digest_check = hashlib.sha256()
                        for chunk in iter(lambda: content.read(1024 * 1024), b''):
                            digest_check.update(chunk)
                        if digest_check.hexdigest() != files[member.name]:
                            raise RuntimeError('Release source hash mismatch: ' + member.name)
            for name in deleted:
                safe_path(name)
            folder, hashes = backup(sha)
            apply_archive(archive, deleted)
        verify_source(files)
        if config_hashes() != hashes:
            raise RuntimeError('Protected configuration changed; deployment stopped.')
        print('Building release ' + sha, flush=True)
        validate_frontend_configuration()
        command(COMPOSE + ['build', 'gateway', 'worker', 'ui'])
        command(COMPOSE + ['run', '--rm', '--no-deps', 'ui', 'caddy', 'validate', '--config', '/etc/caddy/Caddyfile', '--adapter', 'caddyfile'])
        command(['python3', 'scripts/stop-worker-for-release.py', '--', *COMPOSE[2:]])
        command(COMPOSE + ['up', '-d', '--no-deps', '--wait', 'gateway'])
        command(COMPOSE + ['up', '-d', '--no-deps', '--wait', 'ui'])
        command(COMPOSE + ['up', '-d', '--no-deps', 'worker'])
        command(COMPOSE + ['up', '-d', '--no-deps', 'caddy'])
        command(COMPOSE + ['exec', '-T', 'caddy', 'caddy', 'reload', '--config', '/etc/caddy/Caddyfile', '--adapter', 'caddyfile'])
        command(COMPOSE + ['ps'])
        running = set(capture(COMPOSE + ['ps', '--status', 'running', '--services']).splitlines())
        if not {'gateway', 'worker', 'ui', 'postgres', 'redis', 'caddy'}.issubset(running):
            raise RuntimeError('Some required services are not running.')
        verify_migrations()
        command(['python3', 'scripts/deployment/smoke.py', sha])
        if config_hashes() != hashes:
            raise RuntimeError('Protected configuration changed during deployment.')
        if deployed_commit() != previous:
            raise RuntimeError('Another deployment changed the release marker during verification.')
        verify_source(files)
        # Record only a fully checked release. Do not roll back live financial data.
        install_runner(sha)
        record_release(sha, files, folder)
        print('DEPLOYED: ' + sha + '\nBACKUP: ' + str(folder), flush=True)


def install_runner(sha):
    """Future SSH sessions use only scripts from a successfully verified release."""
    root = BASE / 'deployer'
    version = root / sha
    version.mkdir(parents=True, exist_ok=True, mode=0o700)
    for name in ('entry.py', 'remote.py', 'smoke.py'):
        shutil.copyfile(APP / 'scripts/deployment' / name, version / name)
        (version / name).chmod(0o600)
    temporary = root / 'current.new'
    temporary.unlink(missing_ok=True)
    temporary.symlink_to(version.name)
    temporary.replace(root / 'current')


def main():
    os.umask(0o077)
    sha, digest, previous = sys.argv[1:]
    with (BASE / 'release.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError('Another server deployment is running.')
        header = sys.stdin.buffer.readline(4 << 20)
        if not header.endswith(b'\n'):
            raise RuntimeError('Release manifest is missing or too large.')
        deploy(sha, digest, previous, json.loads(header), sys.stdin.buffer)


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, OSError, subprocess.CalledProcessError) as error:
        message = 'Server command failed (exit %s).' % error.returncode if isinstance(error, subprocess.CalledProcessError) else str(error)
        print('DEPLOYMENT STOPPED: ' + message + '\nNo automatic rollback. The printed backup remains available.', file=sys.stderr)
        sys.exit(1)
