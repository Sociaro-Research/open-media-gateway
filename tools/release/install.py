#!/usr/bin/env python3
"""Install the checked-in runner beside the existing private SSH directory."""
import argparse
import datetime
from pathlib import Path
import shutil
import subprocess

SOURCE = Path(__file__).resolve().parent


def install(destination, project):
    destination = Path(destination).expanduser().resolve()
    target = destination / 'Source'
    if (target/'release.lock').exists():
        raise RuntimeError('A release is active; installation refused.')
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
    backup = target / 'backups' / ('local-ci-update-' + stamp)
    backup.mkdir(parents=True, mode=0o700)
    files = list(SOURCE.glob('release*.py')) + list((SOURCE/'tests').glob('test_*.py')) + [SOURCE/'install.py', SOURCE/'README.md', SOURCE/'github_known_hosts', SOURCE/'deployment.example.json', SOURCE/'deployment.schema.json',
            SOURCE/'macos/release.applescript', SOURCE/'macos/build-launcher.sh']
    for source in files:
        relative = source.relative_to(SOURCE)
        installed = target / relative
        if installed.exists():
            saved = backup / relative
            saved.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(installed, saved)
        installed.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, installed)
    config = target / 'project-path.txt'
    if config.exists():shutil.copy2(config,backup/'project-path.txt')
    config.write_text(str(Path(project).resolve()) + '\n')
    for name in ('Gateway Checks and Deploy.app', 'gateway commit and deploy.app'):
        app = destination / name
        if app.exists():shutil.copytree(app, backup/name)
        subprocess.run(['/bin/sh',str(target/'macos/build-launcher.sh'),str(app)],check=True)
    print('Installed: ' + str(destination/'Gateway Checks and Deploy.app'))
    print('Previous files preserved: ' + str(backup))
    print('SSH keys, existing logs and cache were preserved.')


if __name__ == '__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--destination',type=Path,default=Path.home()/'Documents/Release Tool')
    parser.add_argument('--project',type=Path,default=Path((SOURCE/'project-path.txt').read_text().strip()) if (SOURCE/'project-path.txt').exists() else SOURCE.parents[1])
    args=parser.parse_args()
    install(args.destination,args.project)
