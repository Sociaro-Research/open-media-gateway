#!/usr/bin/env python3
"""Run local checks and deploy an already-pushed commit; never write Git history."""
import argparse
import contextlib
import datetime
import hashlib
import json
import re
import os
from pathlib import Path
import shlex
import signal
import shutil
import subprocess
import sys
import tempfile
import uuid

TOOL_DIR = Path(__file__).resolve().parent
PROJECT_FILE = TOOL_DIR / 'project-path.txt'
ROOT = Path(os.environ.get('RELEASE_PROJECT') or
            (PROJECT_FILE.read_text().strip() if PROJECT_FILE.exists() else TOOL_DIR.parents[1])).resolve()
STATE_DIR = Path(os.environ.get('RELEASE_STATE_DIR', str(ROOT / '.local/release-tool' if TOOL_DIR == ROOT / 'tools/release' else TOOL_DIR)))
sys.path.insert(0, str(TOOL_DIR))
from release_config import load_config, key_path


def deployment_config():
    return load_config(ROOT, TOOL_DIR)


PROTECTED = {'.env', 'Caddyfile', 'compose.staging.yaml'}


def github_transport(args):
    """Use the copied GitHub key for read-only network commands, without changing origin."""
    if len(args) < 2 or args[0] != 'git' or args[1] not in ('fetch', 'ls-remote'):
        return args
    config = deployment_config()
    remote = config['repository_url'].rstrip('/')
    if remote.startswith('https://github.com/'):
        remote = 'git@github.com:' + remote[len('https://github.com/'):]
    if not remote.startswith('git@github.com:'):
        return args
    origin = git('remote', 'get-url', 'origin').rstrip('/')
    https = remote.replace('git@github.com:', 'https://github.com/')
    if origin not in (remote, https):
        return args
    configured = os.environ.get('RELEASE_GITHUB_SSH_KEY') or config.get('github_ssh_key_file')
    if not configured:
        return args
    key = Path(configured).expanduser()
    require(key.is_file(), 'Configured GitHub SSH key is missing.')
    hosts = TOOL_DIR / 'github_known_hosts'
    require(hosts.is_file(), 'Pinned GitHub host key file is missing. Reinstall the release tool.')
    # SSH parses this option as a list of paths, so quote spaces inside its value too.
    command = shlex.join(['ssh', '-i', str(key), '-o', 'IdentitiesOnly=yes',
                          '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
                          '-o', 'UserKnownHostsFile="' + str(hosts) + '"',
                          '-o', 'ConnectTimeout=15'])
    return ('git', '-c', 'url.' + remote + '.insteadOf=' + https,
            '-c', 'core.sshCommand=' + command, *args[1:])


def run(*args, capture=False, stdin=None):
    return subprocess.run(github_transport(args), cwd=ROOT, check=True, stdin=stdin,
                          stdout=subprocess.PIPE if capture else None,
                          text=capture).stdout


def git(*args):
    return run('git', *args, capture=True).strip()


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def ssh_args():
    config = deployment_config()
    key = key_path(config, 'admin_ssh_key_file')
    return ['ssh', '-i', str(key), '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes',
            '-o', 'StrictHostKeyChecking=yes', '-o', 'HostKeyAlgorithms=ssh-ed25519',
            '-o', 'HostKeyAlias=' + config['ssh_host_key_alias'],
            '-o', 'UserKnownHostsFile="' + str(config['known_hosts_file']) + '"',
            '-o', 'ConnectTimeout=15', '-o', 'ServerAliveInterval=15',
            '-o', 'ServerAliveCountMax=3', config['admin_ssh_host']]


def confirm(message, yes):
    if not yes:
        require(sys.stdin.isatty(), 'Run in a terminal or pass --yes after reviewing changes.')
        require(input(message + ' [y/N] ').strip().lower() in ('y', 'yes'), 'Cancelled.')


def check_release_contents(tree, head):
    """Keep the checked tree immutable and report paths without file contents."""
    changes = []
    if git('rev-parse', 'HEAD') != head:
        changes.append('  HEAD changed during checks.')
    for label, command in (
        ('Staged', ('diff', '--cached', '--name-only', '-z', tree, '--')),
        ('Working tree', ('diff', '--name-only', '-z', '--')),
        ('Untracked', ('ls-files', '--others', '--exclude-standard', '-z')),
    ):
        paths = run('git', *command, capture=True).split('\0')
        changes.extend('  %s: %s' % (label, json.dumps(path, ensure_ascii=False))
                       for path in paths if path)
    require(not changes, 'Files changed during checks. Deployment stopped.\n'
            + '\n'.join(changes)
            + '\nFinish editing, review these files, and rerun the full release checks.')


def failure_guidance(progress):
    if progress.get('deployment_started'):
        return ('Deployment may be partial. No automatic rollback. Inspect the remote log and backup before retrying.')
    return 'Deployment was not started. Git commits, index and working files were not changed by the launcher.'


def run_checks(native_only=False, extended_only=False):
    with tempfile.TemporaryDirectory(prefix='sociaro-check-result-') as directory:
        result = Path(directory) / 'failure.json'
        try:
            run(sys.executable, str(TOOL_DIR / 'release_checks.py'), '--project', str(ROOT),
                '--failure-file', str(result), *(['--native-only'] if native_only else []), *(['--extended-only'] if extended_only else []))
        except subprocess.CalledProcessError:
            # Accept only the check identifier and exit code. Never relay argv or
            # arbitrary child output into the status summary.
            try:
                data = json.loads(result.read_text())
                label, code = data['check'], data['exit_code']
                valid = (isinstance(label, str) and len(label) <= 200 and
                         re.fullmatch(r'(?:make (?:verify-ci|build-ui)|scripts/(?:ui/[\w.-]+\.cjs|verify-headless\.py)|release check)', label, flags=re.ASCII) and
                         type(code) is int and -255 <= code <= 255 and code != 0)
            except (OSError, ValueError, KeyError, TypeError):
                valid = False
            if valid:
                raise RuntimeError('Release check failed: %s (exit %s).' % (label, code)) from None
            raise


def release(args, progress=None):
    if progress is None:
        progress = {}
    require(not git('ls-files', '-u'), 'Resolve Git conflicts first.')
    require(not git('ls-files', '--', *sorted(PROTECTED)), 'Server configuration must not be tracked.')
    if args.check_only:
        print('\n[2/6] Local checks (no server access)', flush=True)
        run('git', 'diff', '--check')
        run('git', 'diff', '--cached', '--check')
        run('make', 'release-check')
        run_checks(args.native_only, getattr(args, 'extended_only', False))
        print('SUCCESS: local checks passed; no deployment requested.', flush=True)
        return
    config = deployment_config()
    ssh_args()  # Validate all administration identity inputs before any network access.
    remote = config['repository_url']
    require(git('branch', '--show-current') == 'main', 'Switch to main before deployment.')
    require(git('remote', 'get-url', 'origin').rstrip('/') in
            (remote, remote.replace('git@github.com:', 'https://github.com/')),
            'Unexpected origin URL; select this repository in GitHub Desktop.')
    require(not git('status', '--porcelain', '--untracked-files=all'),
            'Commit and push your changes in GitHub Desktop first; deployment requires a clean checkout.')
    for state in ('MERGE_HEAD', 'rebase-merge', 'rebase-apply', 'CHERRY_PICK_HEAD', 'REVERT_HEAD'):
        path = Path(git('rev-parse', '--git-path', state))
        require(not (path if path.is_absolute() else ROOT / path).exists(), 'Finish the active Git operation first.')
    sha = git('rev-parse', 'HEAD')
    tree = git('rev-parse', 'HEAD^{tree}')
    confirm(('Deploy without local CI ' if getattr(args, 'deploy_only', False) else 'Run selected local checks and deploy ') + sha + ' to ' + config['admin_ssh_host'] + '?', args.yes)
    print('\n[1/6] Fetch and verify your published commit', flush=True)
    run('git', 'fetch', 'origin')
    require(git('rev-parse', 'origin/main') == sha,
            'HEAD must equal origin/main. Commit, pull and push in GitHub Desktop, then retry.')
    print('\n[2/6] Whitespace, release policy, tests and build', flush=True)
    run('git', 'diff', '--check')
    run('make', 'release-check')
    try:
        if getattr(args, 'deploy_only', False):
            print('NOT RUN: local CI (deployment-only selected explicitly).', flush=True)
        else:
            run_checks(args.native_only, getattr(args, 'extended_only', False))
    except (RuntimeError, subprocess.CalledProcessError, OSError):
        check_release_contents(tree, sha)
        raise
    check_release_contents(tree, sha)
    print('\n[3/6] Verify exact remote SHA and server access', flush=True)
    remote_head = git('ls-remote', 'origin', 'refs/heads/main').split()
    require(remote_head and remote_head[0] == sha, 'Remote main changed; rerun on the intended commit.')
    previous = run(*ssh_args(), "if test -f /opt/media-gateway/current-release; then cat /opt/media-gateway/current-release; else cat /opt/media-gateway/deployed-commit; fi", capture=True).strip()
    require(re.fullmatch(r'[0-9a-f]{40}', previous), 'Invalid deployed commit marker.')
    run('git', 'merge-base', '--is-ancestor', previous, sha)
    # Remove files deleted by this release, rather than leaving stale source in the build.
    deleted = git('diff', '--name-only', '--diff-filter=D', '--no-renames', '-z', previous, sha).split('\0')
    deleted = [p for p in deleted if p]
    deleted_hashes = {p: hashlib.sha256(subprocess.check_output(['git', 'show', previous + ':' + p], cwd=ROOT)).hexdigest() for p in deleted}
    files = git('ls-tree', '-r', '--name-only', '-z', sha).split('\0')
    files = {p: hashlib.sha256(subprocess.check_output(['git', 'show', sha + ':' + p], cwd=ROOT)).hexdigest() for p in files if p}
    manifest = {'deleted': deleted_hashes, 'files': files}
    remote_script = (TOOL_DIR / 'release_remote.py').read_text()
    remote_script = remote_script.replace('SMOKE_SOURCE = \"\"', 'SMOKE_SOURCE = ' + repr((TOOL_DIR / 'release_smoke.py').read_text()), 1)
    with tempfile.TemporaryDirectory(prefix='gateway-release-') as temp:
        archive = Path(temp) / 'release.tar'
        run('git', 'archive', '--format=tar', '-o', str(archive), 'HEAD')
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        require(git('rev-parse', 'HEAD') == sha, 'HEAD changed while preparing the archive.')
        upload = Path(temp) / 'upload.bin'
        with upload.open('wb') as target, archive.open('rb') as source:
            target.write(json.dumps(manifest).encode() + b'\n')
            shutil.copyfileobj(source, target)
        command = shlex.join(['python3', '-c', remote_script, sha, digest, previous])
        print('\n[4/6] Upload; [5/6] backup and deploy; [6/6] production checks', flush=True)
        with upload.open('rb') as stream:
            progress['deployment_started'] = True
            run(*ssh_args(), command, stdin=stream)
    print('\nSUCCESS: deployed ' + sha, flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check-only', action='store_true', help='Run checks on the current checkout without SSH or deployment.')
    parser.add_argument('--yes', action='store_true', help='Approve the displayed scope without a prompt.')
    parser.add_argument('--native-only', action='store_true', help='Run native checks only; omit security and Docker checks explicitly.')
    parser.add_argument('--deploy-only', action='store_true', help='Deploy without local CI; retain release policy and all deployment safety gates.')
    parser.add_argument('--extended-only', action='store_true', help='Run only security, container and installation checks; omit native suites.')
    args = parser.parse_args()
    if args.native_only and args.extended_only:
        parser.error('--native-only and --extended-only are mutually exclusive')
    if args.deploy_only and (args.check_only or args.native_only or args.extended_only):
        parser.error('--deploy-only cannot be combined with check flags')
    os.environ['PATH'] = os.pathsep.join([str(Path.home()/'.docker/bin'), '/Applications/Docker.app/Contents/Resources/bin', os.environ.get('PATH', '')])
    os.umask(0o077)
    os.chdir(ROOT)
    # Runtime state stays outside tracked source and release archives.
    state = STATE_DIR
    state.mkdir(parents=True, exist_ok=True)
    lock = state / 'release.lock'
    try:
        lock.mkdir()
    except FileExistsError:
        parser.exit(1, 'Another release is running. If a previous run was killed, remove the local release.lock directory after checking it.\n')
    log_dir = state / 'logs'
    log_dir.mkdir(exist_ok=True)
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    log_path = log_dir / (stamp + '-' + uuid.uuid4().hex[:6] + '.log')
    print('Release log: ' + str(log_path), flush=True)
    # Tee child-process output too, not only Python's progress messages.
    saved_out, saved_err = os.dup(1), os.dup(2)
    read_fd, write_fd = os.pipe()
    tee = subprocess.Popen(['tee', str(log_path)], stdin=read_fd,
                           preexec_fn=lambda: signal.signal(signal.SIGINT, signal.SIG_IGN))
    os.close(read_fd)
    os.dup2(write_fd, 1)
    os.dup2(write_fd, 2)
    os.close(write_fd)
    code = 0
    progress = {}
    try:
        release(args, progress)
    except (RuntimeError, subprocess.CalledProcessError, OSError, KeyboardInterrupt) as error:
        code = 1
        # CalledProcessError can contain the remote command; do not dump it.
        reason = 'Command failed (exit %s).' % error.returncode if isinstance(error, subprocess.CalledProcessError) else str(error)
        print('\nSTOPPED: ' + (reason or 'Interrupted.') + '\n' + failure_guidance(progress), flush=True)
    finally:
        sys.stdout.flush()
        sys.stderr.flush()
        os.dup2(saved_out, 1)
        os.dup2(saved_err, 2)
        os.close(saved_out)
        os.close(saved_err)
        tee.wait()
        with contextlib.suppress(OSError):
            lock.rmdir()
    return code


if __name__ == '__main__':
    sys.exit(main())
