#!/usr/bin/env python3
"""Deploy an exact CI-tested main commit through the restricted SSH entry point."""
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import time
from urllib.parse import urlsplit

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / 'tools/release'))
from release_config import load_config, key_path, network_config

ROOT = Path(__file__).resolve().parents[2]
PROTECTED = {'.env', 'Caddyfile', 'compose.staging.yaml'}


def git(*args, root=ROOT):
    return subprocess.check_output(['git', *args], cwd=root)


def prepare(sha, previous, destination, root=ROOT):
    for value in (sha, previous):
        if not re.fullmatch(r'[0-9a-f]{40}', value):
            raise RuntimeError('A full Git commit SHA is required.')
    if git('rev-parse', 'HEAD', root=root).decode().strip() != sha:
        raise RuntimeError('HEAD differs from the tested release.')
    if git('status', '--porcelain', '--untracked-files=all', root=root).strip():
        raise RuntimeError('The release checkout must be clean.')
    subprocess.run(['git', 'merge-base', '--is-ancestor', previous, sha], cwd=root, check=True)
    # Build the manifest from Git objects, never from mutable working files.
    entries = git('ls-tree', '-r', '-z', sha, root=root).split(b'\0')
    files = {}
    for entry in filter(None, entries):
        metadata, name = entry.split(b'\t', 1)
        mode, kind, object_id = metadata.split()
        path = name.decode()
        if mode not in (b'100644', b'100755') or kind != b'blob':
            raise RuntimeError('Release trees cannot contain links or submodules.')
        if path in PROTECTED:
            raise RuntimeError('Server-only configuration must not be tracked: ' + path)
        files[path] = hashlib.sha256(git('cat-file', 'blob', object_id.decode(), root=root)).hexdigest()
    deleted = {}
    for name in filter(None, git('diff', '--name-only', '--diff-filter=D', '--no-renames', '-z', previous, sha, root=root).split(b'\0')):
        path = name.decode()
        if path in PROTECTED:
            raise RuntimeError('A release must not delete protected server configuration.')
        deleted[path] = hashlib.sha256(git('show', previous + ':' + path, root=root)).hexdigest()
    archive = git('archive', '--format=tar', sha, root=root)
    digest = hashlib.sha256(archive).hexdigest()
    # export-ignore attributes must not silently omit tracked release source.
    with tarfile.open(fileobj=io.BytesIO(archive)) as source:
        if {p.name for p in source.getmembers() if p.isfile()} != set(files):
            raise RuntimeError('Git archive and tracked release manifest differ.')
    with destination.open('wb') as target:
        target.write(json.dumps({'files': files, 'deleted': deleted}).encode() + b'\n')
        target.write(archive)
    return digest


def ssh(key, command, source=None):
    config = load_config(ROOT)
    args = ['ssh', '-i', str(key), '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes',
            '-o', 'StrictHostKeyChecking=yes', '-o', 'HostKeyAlgorithms=ssh-ed25519',
            '-o', 'HostKeyAlias=' + config['ssh_host_key_alias'],
            '-o', 'UserKnownHostsFile=' + '"' + str(config['known_hosts_file']) + '"',
            '-o', 'ConnectTimeout=20', '-o', 'ServerAliveInterval=15',
            '-o', 'ServerAliveCountMax=4', config['tunnel_ssh_host'], command]
    result = subprocess.run(args, stdin=source if source is not None else subprocess.DEVNULL,
                            stdout=subprocess.PIPE, check=True)
    return json.loads(result.stdout)


def poll(key, job, sha, timeout=2400):
    deadline = time.monotonic() + timeout
    offset = 0
    while time.monotonic() < deadline:
        try:
            result = ssh(key, 'poll ' + job + ' ' + str(offset))
        except (subprocess.CalledProcessError, json.JSONDecodeError):
            print('SSH status temporarily unavailable; the server deployment continues.', flush=True)
            time.sleep(10)
            continue
        if result.get('sha') != sha:
            raise RuntimeError('The server job does not match the requested release.')
        # Prevent server/build output from being interpreted as Actions commands.
        for line in result.get('output', '').splitlines():
            print('server | ' + line, flush=True)
        offset = result['offset']
        if result.get('more'):
            continue
        if result['state'] == 'succeeded':
            return
        if result['state'] == 'failed':
            raise RuntimeError('Deployment failed. Inspect the server log and printed backup; no automatic rollback was performed.')
        time.sleep(10)
    raise RuntimeError('Status wait timed out. The server job continues; inspect job ' + job + ' before retrying.')



def success_summary(public_url=""):
    """An optional public origin affects the display link, never the SSH target."""
    message = 'Deployment and production checks passed.'
    if not public_url:
        return message + '\n'
    try:
        parsed = urlsplit(public_url)
        port = parsed.port
        valid = (re.fullmatch(r'https://[A-Za-z0-9.\-:\[\]]+/?', public_url)
                 and parsed.hostname and not parsed.username and not parsed.password
                 and (port is None or 1 <= port <= 65535))
    except ValueError:
        valid = False
    if not valid:
        raise RuntimeError('DEPLOY_PUBLIC_URL must be an HTTPS origin without credentials, path, query or fragment.')
    return message + ' [Open application](' + public_url + ').\n'


def main():
    os.umask(0o077)
    config = load_config(ROOT)
    summary_text = success_summary(config['public_origin'])
    sha = os.environ.get('RELEASE_SHA', '')
    if os.environ.get('GITHUB_REF') != 'refs/heads/main' or os.environ.get('GITHUB_EVENT_NAME') != 'workflow_dispatch':
        raise RuntimeError('Production deployment requires a manual run on main.')
    secret = os.environ.pop('DEPLOY_SSH_KEY', '')
    configured_key = None if secret.strip() else key_path(config, 'restricted_ssh_key_file')
    with tempfile.TemporaryDirectory(prefix='gateway-actions-') as temporary:
        key = Path(temporary) / 'key'
        if configured_key is None:
            key.write_text(secret.rstrip() + '\n')
            key.chmod(0o600)
        else:
            key = configured_key
        del secret
        previous = ssh(key, 'status')['sha']
        if sys.argv[1:] == ['--check-connection']:
            print('Private deployment SSH verified; current release: ' + previous, flush=True)
            return
        payload = Path(temporary) / 'upload'
        digest = prepare(sha, previous, payload)
        with payload.open('rb') as source:
            job = ssh(key, 'start ' + sha + ' ' + digest + ' ' + previous, source)['job']
        if not re.fullmatch(r'[0-9a-f]{32}', job):
            raise RuntimeError('Invalid server job identifier.')
        print('Server job: ' + job + '; release: ' + sha, flush=True)
        summary = os.environ.get('GITHUB_STEP_SUMMARY')
        if summary:
            with open(summary, 'a') as target:
                target.write('Release: `' + sha + '`\n\nServer job: `' + job + '`\n\n')
        poll(key, job, sha)
        if ssh(key, 'status')['sha'] != sha:
            raise RuntimeError('Verified server release marker does not match.')
        if summary:
            with open(summary, 'a') as target:
                target.write(summary_text)
        print('Verified production release: ' + sha, flush=True)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        message = str(error) if isinstance(error, RuntimeError) else 'Deployment command failed; inspect the preceding safe diagnostic output.'
        print('DEPLOYMENT STOPPED: ' + message, file=sys.stderr)
        sys.exit(1)
