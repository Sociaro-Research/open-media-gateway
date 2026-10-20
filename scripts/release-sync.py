#!/usr/bin/env python3
"""Integrate fast-forward upstream updates before release checks, preserving work."""
import argparse
import datetime
import json
from pathlib import Path
import subprocess
import sys
import uuid


def synchronize(project):
    project = Path(project).resolve()

    def git(*args, check=True):
        return subprocess.run(['git', *args], cwd=project, check=check,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def value(*args):
        return git(*args).stdout.decode().strip()

    def require(condition, message):
        if not condition:
            raise RuntimeError(message)

    require(value('branch', '--show-current') == 'main', 'Release synchronization requires main.')
    require(not value('ls-files', '-u'), 'Resolve Git conflicts before release synchronization.')
    for state in ('MERGE_HEAD', 'rebase-merge', 'rebase-apply', 'CHERRY_PICK_HEAD', 'REVERT_HEAD'):
        path = Path(value('rev-parse', '--git-path', state))
        require(not (project / path).exists(), 'Finish the active Git operation first.')
    head, upstream = value('rev-parse', 'HEAD'), value('rev-parse', 'origin/main')
    if git('merge-base', '--is-ancestor', upstream, head, check=False).returncode == 0:
        return False
    require(git('merge-base', '--is-ancestor', head, upstream, check=False).returncode == 0,
            'Local main and origin/main diverged. Integrate the branches before publishing; no files were changed.')
    # Protect ignored local data as well: Git may otherwise overwrite ignored
    # files during a checkout even though they do not appear in status.
    def paths(*args):
        return {p for p in git(*args).stdout.decode(errors='surrogateescape').split('\0') if p}
    untracked = paths('ls-files', '--others', '--exclude-standard', '-z')
    ignored = paths('ls-files', '--others', '--ignored', '--exclude-standard', '-z')
    incoming = paths('ls-tree', '-r', '--name-only', '-z', upstream)
    collisions = {p for p in untracked | ignored if any(p == q or p.startswith(q + '/') or q.startswith(p + '/') for q in incoming)}
    require(not collisions, 'Upstream conflicts with untracked files (including ignored data); no files were changed:\n' +
            '\n'.join('  ' + json.dumps(p, ensure_ascii=False) for p in sorted(collisions)))
    dirty = bool(value('status', '--porcelain'))
    if not dirty:
        git('merge', '--ff-only', upstream)
        print('UPSTREAM: updated main before release checks.', flush=True)
        return True

    # Preview tracked work without changing the checkout, index or stash list.
    preview = value('stash', 'create', 'Sociaro Release upstream preview')
    if preview:
        merged = git('merge-tree', '--write-tree', upstream, preview, check=False)
        overlap = paths('diff', '--name-only', '-z', head, upstream) & paths('diff', '--name-only', '-z', head)
        require(merged.returncode == 0,
                'Upstream conflicts with local edits; checkout and index are unchanged. Review overlapping paths:\n' +
                '\n'.join('  ' + json.dumps(p, ensure_ascii=False) for p in sorted(overlap)))

    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '-' + uuid.uuid4().hex[:8]
    old_stash = git('rev-parse', '--verify', 'refs/stash', check=False).stdout.strip()
    git('stash', 'push', '--include-untracked', '-m', 'Sociaro Release upstream backup ' + stamp)
    backup = value('rev-parse', 'refs/stash')
    require(backup.encode() != old_stash, 'Could not preserve release changes; synchronization stopped.')
    backup_ref = 'refs/sociaro-release/upstream/' + stamp
    git('update-ref', backup_ref, backup)
    print('UPSTREAM BACKUP: ' + backup_ref, flush=True)
    try:
        git('merge', '--ff-only', upstream)
        restored = git('stash', 'apply', '--index', backup, check=False)
        require(restored.returncode == 0,
                'Upstream updated, but local changes need review. Work is preserved at ' + backup_ref +
                '; no commit, push or deployment was started. Do not discard the backup.')
    except (subprocess.CalledProcessError, OSError):
        # If fast-forward itself failed, restore the original work at its old HEAD.
        if value('rev-parse', 'HEAD') == head:
            git('stash', 'apply', '--index', backup, check=False)
        raise RuntimeError('Upstream synchronization stopped. Original work is preserved at ' + backup_ref + '.') from None
    # Remove only our top stash entry; retain the durable backup ref and every
    # pre-existing stash. A concurrent stash operation cannot cause a wrong drop.
    if value('rev-parse', 'refs/stash') == backup:
        git('stash', 'drop', 'stash@{0}')
    print('UPSTREAM: updated main and restored local edits before release checks.', flush=True)
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--project', type=Path, required=True)
    args = parser.parse_args()
    try:
        synchronize(args.project)
    except (RuntimeError, subprocess.CalledProcessError, OSError) as error:
        # Do not forward arbitrary Git output or file contents into release logs.
        message = str(error) if isinstance(error, RuntimeError) else 'Git synchronization failed; no publication was started.'
        print('UPSTREAM STOPPED: ' + message, flush=True)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
