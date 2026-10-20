#!/usr/bin/env python3
"""Stop the worker before migrations; refuse a release after an unclean exit."""
import argparse
import json
import math
import re
import subprocess
import sys


def duration_seconds(value):
    """Read the positive Go duration used by the worker configuration."""
    value = str(value).removeprefix('+')
    factors = {'h': 3600, 'm': 60, 's': 1, 'ms': .001,
               'us': .000001, 'µs': .000001, 'μs': .000001, 'ns': .000000001}
    total, end = 0., 0
    for match in re.finditer(r'(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|μs|ms|s|m|h)', value):
        if match.start() != end:
            raise ValueError('Invalid WORKER_SHUTDOWN_TIMEOUT')
        total += float(match[1]) * factors[match[2]]
        end = match.end()
    if end != len(value) or not math.isfinite(total) or total <= 0:
        raise ValueError('Invalid WORKER_SHUTDOWN_TIMEOUT')
    return total


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--timeout', type=int,
                        help='Optional Docker stop timeout in seconds; otherwise drain budget plus 60 seconds')
    parser.add_argument('compose_args', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.timeout is not None and args.timeout <= 0:
        parser.error('--timeout must be positive')
    flags = args.compose_args
    if flags[:1] == ['--']:
        flags = flags[1:]
    compose = ['docker', 'compose', *flags]
    config = json.loads(subprocess.check_output([*compose, 'config', '--format', 'json'],
                                               stdin=subprocess.DEVNULL, text=True))
    worker = config['services']['worker']
    drain = duration_seconds(worker.get('environment', {}).get('WORKER_SHUTDOWN_TIMEOUT', '25m'))
    # The running release can have a longer budget than the incoming release.
    # Capture configuration privately; never print its credentials.
    containers = subprocess.check_output([*compose, 'ps', '--all', '--quiet', 'worker'],
                                         stdin=subprocess.DEVNULL, text=True).split()
    for container in containers:
        environment = json.loads(subprocess.check_output(
            ['docker', 'inspect', '--format', '{{json .Config.Env}}', container],
            stdin=subprocess.DEVNULL, text=True))
        values = dict(item.split('=', 1) for item in environment if '=' in item)
        drain = max(drain, duration_seconds(values.get('WORKER_SHUTDOWN_TIMEOUT', '25m')))
    timeout = args.timeout if args.timeout is not None else math.ceil(drain + 60)
    if timeout < math.ceil(drain + 10):
        raise RuntimeError('Release stop timeout must exceed WORKER_SHUTDOWN_TIMEOUT by at least 10 seconds; worker was not stopped.')
    subprocess.run([*compose, 'stop', '--timeout', str(timeout), 'worker'],
                   stdin=subprocess.DEVNULL, check=True)
    stopped = subprocess.check_output([*compose, 'ps', '--all', '--quiet', 'worker'],
                                      stdin=subprocess.DEVNULL, text=True).split()
    for container in sorted(set(containers + stopped)):
        state = subprocess.check_output(
            ['docker', 'inspect', '--format', '{{.State.Status}} {{.State.ExitCode}}', container],
            stdin=subprocess.DEVNULL, text=True).strip()
        if state != 'exited 0':
            raise RuntimeError(f'Worker did not stop cleanly ({state}); release stopped before migrations. '
                               'Inspect worker logs and recover pending work before retrying.')
    print('Worker stopped cleanly; active operations drained.')


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
