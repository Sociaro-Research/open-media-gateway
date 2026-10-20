#!/usr/bin/env python3
"""Restricted SSH entry point. No shell, forwarding, arbitrary commands or paths."""
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import uuid

BASE = Path('/opt/media-gateway')
JOBS = BASE / 'actions-jobs'
SHA = r'[0-9a-f]{40}'
HASH = r'[0-9a-f]{64}'
JOB = r'[0-9a-f]{32}'
MAX_UPLOAD = 512 * 1024 * 1024


def parse_command(command):
    # Full matches deliberately reject shell syntax and extra arguments.
    if command == 'status':
        return ['status']
    for pattern in (rf'start ({SHA}) ({HASH}) ({SHA})', rf'poll ({JOB}) ([0-9]{{1,12}})'):
        match = re.fullmatch(pattern, command)
        if match:
            return [command.split(' ', 1)[0], *match.groups()]
    raise RuntimeError('Only status, start and poll deployment commands are allowed.')


def write_json(path, value):
    temporary = path.with_suffix('.tmp')
    temporary.write_text(json.dumps(value) + '\n')
    temporary.replace(path)


def run_job(job_id, sha, digest, previous):
    folder = JOBS / job_id
    try:
        spec = importlib.util.spec_from_file_location('remote', Path(__file__).with_name('remote.py'))
        remote = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(remote)
        # remote.main owns the same nonblocking server lock as the local launcher.
        sys.argv = [str(spec.origin), sha, digest, previous]
        remote.main()
    except Exception as error:
        # Never echo a command, environment or exception carrying credentials.
        message = str(error) if isinstance(error, RuntimeError) else 'Server deployment failed; inspect the protected server log.'
        print('DEPLOYMENT STOPPED: ' + message, flush=True)
        print('No automatic rollback. Preserve the printed application/database backup.', flush=True)
        write_json(folder / 'status.json', {'state': 'failed', 'sha': sha})
        return 1
    else:
        write_json(folder / 'status.json', {'state': 'succeeded', 'sha': sha})
        return 0
    finally:
        (folder / 'upload').unlink(missing_ok=True)


def dispatch(command, source):
    action, *args = parse_command(command)
    if action == 'status':
        marker = BASE / 'current-release'
        if not marker.exists():
            marker = BASE / 'deployed-commit'
        sha = marker.read_text().strip()
        if not re.fullmatch(SHA, sha):
            raise RuntimeError('A valid previous release marker is required.')
        return {'sha': sha}
    if action == 'poll':
        job_id, offset = args
        folder = JOBS / job_id
        state = json.loads((folder / 'status.json').read_text())
        with (folder / 'log').open('rb') as log:
            log.seek(int(offset))
            chunk = log.read(128 * 1024)
            state.update(offset=log.tell(), output=chunk.decode('utf-8', errors='replace'),
                         more=bool(log.read(1)))
        return state
    sha, digest, previous = args
    job_id = uuid.uuid4().hex
    JOBS.mkdir(mode=0o700, exist_ok=True)
    folder = JOBS / job_id
    folder.mkdir(mode=0o700)
    try:
        with (folder / 'upload').open('wb') as target:
            total = 0
            while chunk := source.read(1024 * 1024):
                total += len(chunk)
                if total > MAX_UPLOAD:
                    raise RuntimeError('Release upload exceeds 512 MiB.')
                target.write(chunk)
        write_json(folder / 'status.json', {'state': 'running', 'sha': sha})
        # Pin this process to the installed version. A later deployment can update
        # the current symlink without changing this worker's Python files.
        with (folder / 'upload').open('rb') as payload, (folder / 'log').open('ab') as log:
            subprocess.Popen([sys.executable, str(Path(__file__).resolve()), '_run', job_id, sha, digest, previous],
                             stdin=payload, stdout=log, stderr=log, start_new_session=True,
                             close_fds=True, cwd=BASE)
    except Exception:
        (folder / 'upload').unlink(missing_ok=True)
        write_json(folder / 'status.json', {'state': 'failed', 'sha': sha})
        raise
    return {'job': job_id, 'sha': sha}


def main():
    os.umask(0o077)
    if len(sys.argv) == 6 and sys.argv[1] == '_run':
        return run_job(*sys.argv[2:])
    result = dispatch(os.environ.get('SSH_ORIGINAL_COMMAND', ''), sys.stdin.buffer)
    print(json.dumps(result), flush=True)
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except Exception:
        print('Deployment SSH request rejected or unavailable.', file=sys.stderr)
        sys.exit(1)
