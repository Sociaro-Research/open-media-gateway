#!/usr/bin/env python3
"""Run one explicit UI command partition sequentially, with isolated evidence and timings."""
import argparse
from collections import Counter
import datetime as dt
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
JOBS = ('ui-performance', 'ui-media', 'ui-console')
OUTPUTS = ('UI_EVIDENCE_DIR', 'UI_SCREENSHOT_DIR', 'GATEWAY_PERF_OUTPUT')
VARIANTS = {'UI_BROWSER_ENGINE', 'UI_BROWSER', 'UI_MEMBER_ROLE_ONLY', 'UI_VOICE_UPLOAD', *OUTPUTS}


def signature(argv, env):
    return tuple(argv), tuple(sorted(env.items()))


def load_commands(root=ROOT):
    manifest = json.loads((root / 'scripts/ci/ui-commands.json').read_text())
    if manifest.get('version') != 1:
        raise ValueError('Unsupported UI command manifest')
    commands = manifest['commands']
    expected = []
    for line in (root / 'scripts/tests/fixtures/ci-ui-baseline.txt').read_text().splitlines():
        if not line or line.startswith('#'): continue
        argv, env = shlex.split(line), {}
        while argv and '=' in argv[0]:
            name, value = argv.pop(0).split('=', 1);env[name] = value
        expected.append(signature(argv, env))
    if len(commands) != 67 or Counter(signature(c['argv'], c['env']) for c in commands) != Counter(expected):
        raise ValueError('UI coverage differs from the original 67 commands/variants')
    ids = set()
    for command in commands:
        identifier = command['id']
        if not re.fullmatch(r'[a-z0-9][a-z0-9.-]+', identifier) or identifier in ids:
            raise ValueError('Invalid/duplicate command ID')
        ids.add(identifier)
        if command['job'] not in JOBS or not set(command['env']) <= VARIANTS:
            raise ValueError('Invalid job or environment variant')
    if set(c['job'] for c in commands) != set(JOBS):
        raise ValueError('Every isolated UI job must have commands')
    if any(c['job'] != 'ui-performance' for c in commands if 'scripts/ui/performance.cjs' in c['argv']):
        raise ValueError('Performance measurements must stay on their isolated serial runner')
    return commands


def terminate_group(pid):
    try: os.killpg(pid, signal.SIGTERM)
    except ProcessLookupError: return
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        try: os.killpg(pid, 0)
        except ProcessLookupError: return
        time.sleep(.05)
    try: os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError: pass


def execute(command, directory, root=ROOT, timeout=1800):
    directory.mkdir(parents=True, exist_ok=False)
    env = {k: v for k, v in os.environ.items() if not k.startswith('UI_') and k != 'GATEWAY_PERF_OUTPUT'}
    env.update(command['env'])
    outputs = {name: str(directory / name.lower()) for name in OUTPUTS}
    for name, value in outputs.items():
        Path(value).mkdir();env[name] = value
    result = {**command, 'outputs': outputs, 'source_sha': os.environ.get('GITHUB_SHA', ''),
              'started_at': dt.datetime.now(dt.timezone.utc).isoformat(), 'status': 'running'}
    state = directory / 'command.json'
    started = time.monotonic()
    process = None
    with (directory / 'stdout.log').open('wb') as log:
        try:
            process = subprocess.Popen(command['argv'], cwd=root, env=env, stdout=log,
                                       stderr=subprocess.STDOUT, start_new_session=True)
            result['pid'] = process.pid
            state.write_text(json.dumps(result, indent=2) + '\n')
            print(json.dumps({'command_start': result}), flush=True)
            code = process.wait(timeout=timeout)
            result.update(exit=code, status='passed' if code == 0 else 'failed')
        except subprocess.TimeoutExpired:
            result.update(exit=124, status='timeout')
        except KeyboardInterrupt:
            result.update(exit=130, status='cancelled')
        except OSError as error:
            result.update(exit=127, status='failed', error=type(error).__name__)
        finally:
            if process:
                terminate_group(process.pid)
                process.wait()
            result.update(completed_at=dt.datetime.now(dt.timezone.utc).isoformat(),
                          elapsed_seconds=round(time.monotonic()-started, 6))
            state.write_text(json.dumps(result, indent=2) + '\n')
            print(json.dumps({'command_end': result}), flush=True)
    if result['exit']:
        print((directory / 'stdout.log').read_text(errors='replace')[-8000:], flush=True)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--job', choices=JOBS)
    parser.add_argument('--output', default='ui-results')
    parser.add_argument('--validate-only', action='store_true')
    args = parser.parse_args()
    commands = load_commands()
    if args.validate_only:
        print(json.dumps({job: sum(c['job'] == job for c in commands) for job in JOBS}));return 0
    parser.error('--job is required') if not args.job else None
    output = Path(args.output).resolve() / args.job
    output.mkdir(parents=True, exist_ok=False)
    selected = [c for c in commands if c['job'] == args.job]
    report = {'job': args.job, 'source_sha': os.environ.get('GITHUB_SHA', ''),
              'commands': [{**c, 'status': 'not_run'} for c in selected]}
    def interrupted(signum, frame): raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    for index, command in enumerate(selected):
        result = execute(command, output / command['id'])
        report['commands'][index] = result
        (output / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
        if result['exit']:
            return result['exit'] if result['exit'] > 0 else 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
