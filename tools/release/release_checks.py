#!/usr/bin/env python3
"""Release checks using owned, disposable local services; never production."""
import argparse
import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import re
import platform
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import tarfile
import urllib.request
import importlib.util

TOOL_DIR = Path(__file__).resolve().parent
UI_CHECKS = [('separation.cjs',), ('smoke.cjs', '--generations-only'),
             ('branding.cjs',), ('profile.cjs',), ('report-details.cjs',),
             ('workspaces.cjs',), ('control-pickers.cjs',), ('email-login.cjs',),
             ('provider-connections.cjs',), ('storage-settings.cjs',), ('auto-update-test.cjs',)]


def project_ui_checks(project):
    selected = list(UI_CHECKS)
    workflow = project / '.github/workflows/ci.yml'
    if workflow.exists():
        for line in workflow.read_text().splitlines():
            # Parse a narrow data grammar, never execute YAML as a shell program.
            match = re.fullmatch(r'\s*((?:UI_[A-Z_]+=[\w./-]+ )*)node scripts/ui/([\w.-]+\.cjs)((?: --[\w-]+)*)\s*', line)
            if match:
                settings = tuple(match.group(1).split())
                check = (match.group(2), *match.group(3).split(), *settings)
                if check not in selected:
                    selected.append(check)
    edited = edited_ui_checks(project)
    selected.sort(key=lambda check: 'scripts/ui/' + check[0] not in edited)
    return selected


def edited_ui_checks(project):
    # Git inspection changes ordering only; never remove required checks.
    if not (project / '.git').exists():
        return set()
    try:
        changed = set()
        for command in (['git', 'diff', '--name-only', '-z', 'HEAD', '--', 'scripts/ui'],
                        ['git', 'ls-files', '--others', '--exclude-standard', '-z', '--', 'scripts/ui']):
            changed.update(subprocess.check_output(command, cwd=project, stderr=subprocess.DEVNULL)
                           .decode('utf-8', errors='surrogateescape').split('\0'))
        return changed
    except (OSError, subprocess.CalledProcessError):
        return set()


class CheckFailure(RuntimeError):
    def __init__(self, check, exit_code):
        self.check = check
        self.exit_code = exit_code
        super().__init__('Release check failed: %s (exit %s).' % (check, exit_code))


def check_label(args, project):
    # Publish only a known target or repository-relative check path, never argv,
    # URLs, environment variables or the contents of a failing command.
    if len(args) >= 2 and args[0] == 'make' and args[1] in ('verify-ci', 'build-ui'):
        return 'make ' + args[1]
    if len(args) > 1:
        try:
            path = (Path(project) / args[1]).resolve().relative_to(Path(project).resolve()).as_posix()
        except (ValueError, TypeError):
            return 'release check'
        if re.fullmatch(r'scripts/(?:ui/[\w.-]+\.cjs|verify-headless\.py)', path, flags=re.ASCII):
            return path
    return 'release check'


def execute(args, project, env):
    label = check_label(args, project)
    started = time.monotonic()
    print('CHECK: ' + label, flush=True)
    try:
        subprocess.run(args, cwd=project, env=env, stdin=subprocess.DEVNULL, check=True,
                       timeout=600 if label.startswith('scripts/ui/') else 1800)
    except subprocess.TimeoutExpired:
        raise CheckFailure(label, 124) from None
    except subprocess.CalledProcessError as error:
        raise CheckFailure(label, error.returncode) from None
    print('CHECK PASSED: %s (%.1fs)' % (label, time.monotonic() - started), flush=True)


def available_port():
    with socket.socket() as connection:
        connection.bind(('127.0.0.1', 0))
        return connection.getsockname()[1]


def postgres_tools():
    candidates = [Path(os.environ.get('POSTGRES_BIN', '/opt/homebrew/opt/postgresql@17/bin')),
                  Path('/usr/local/opt/postgresql@17/bin')]
    if shutil.which('initdb'):
        candidates.append(Path(shutil.which('initdb')).resolve().parent)
    for folder in candidates:
        if all((folder / name).is_file() for name in ('initdb', 'pg_ctl', 'createdb', 'psql')):
            return folder
    raise RuntimeError('Install local PostgreSQL tools (Homebrew postgresql@17); production databases are never used for release checks.')


def wait_port(port, process=None):
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        if process is not None and process.poll() is not None:
            raise RuntimeError('An isolated test service failed to start.')
        try:
            with socket.create_connection(('127.0.0.1', port), timeout=.2):
                return
        except OSError:
            time.sleep(.1)
    raise RuntimeError('An isolated test service did not become ready.')


@contextlib.contextmanager
def isolated_services(environment):
    tools = postgres_tools()
    redis = shutil.which('redis-server')
    if not redis:
        raise RuntimeError('Install redis-server (Homebrew redis) for isolated release checks.')
    env = dict(environment)
    # Finder may supply LC_CTYPE=UTF-8 without a valid LANG. Keep the owned
    # services deterministic, like scripts/with-test-services.py, on every launch.
    env.update(LANG='C', LC_ALL='C')
    # Explicitly replace inherited test endpoints, including accidental production values.
    env['PATH'] = str(tools) + os.pathsep + env.get('PATH', '')
    with tempfile.TemporaryDirectory(prefix='sociaro-release-checks-') as folder:
        folder = Path(folder)
        data = folder / 'postgres'
        pg_port, redis_port = available_port(), available_port()
        while redis_port == pg_port:
            redis_port = available_port()
        started = False
        redis_process = None
        try:
            subprocess.run([str(tools / 'initdb'), '-D', str(data), '-A', 'trust', '-U', 'gateway_release', '--encoding=UTF8', '--no-locale'],
                           env=env, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, check=True)
            started = True
            subprocess.run([str(tools / 'pg_ctl'), '-D', str(data), '-l', str(folder / 'postgres.log'),
                            '-o', '-h 127.0.0.1 -p %s -k %s' % (pg_port, folder), '-w', 'start'],
                           env=env, stdin=subprocess.DEVNULL, check=True)
            subprocess.run([str(tools / 'createdb'), '-h', '127.0.0.1', '-p', str(pg_port),
                            '-U', 'gateway_release', 'gateway_release_test'],
                           env=env, stdin=subprocess.DEVNULL, check=True)
            redis_process = subprocess.Popen([redis, '--bind', '127.0.0.1', '--port', str(redis_port),
                                               '--save', '', '--appendonly', 'no', '--dir', str(folder)],
                                              env=env, stdin=subprocess.DEVNULL,
                                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            wait_port(redis_port, redis_process)
            env['GATEWAY_TEST_DATABASE_URL'] = 'postgres://gateway_release@127.0.0.1:%s/gateway_release_test?sslmode=disable' % pg_port
            env['GATEWAY_TEST_REDIS_URL'] = 'redis://127.0.0.1:%s/0' % redis_port
            yield env
        finally:
            if redis_process is not None and redis_process.poll() is None:
                redis_process.terminate()
                try:
                    redis_process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    redis_process.kill()
                    redis_process.wait()
            if started and (data / 'postmaster.pid').exists():
                subprocess.run([str(tools / 'pg_ctl'), '-D', str(data), '-m', 'fast', '-w', 'stop'],
                               env=env, stdin=subprocess.DEVNULL, check=True)


def browser_environment(environment):
    env = dict(environment)
    bundled = Path.home() / '.cache/codex-runtimes/codex-primary-runtime/dependencies/node'
    candidates = [env.get('RELEASE_NODE'), shutil.which('node'), str(bundled / 'bin/node')]
    for candidate in candidates:
        if candidate and Path(candidate).is_file():
            trial = dict(env)
            modules = trial.get('NODE_PATH')
            bundled_modules = bundled / 'node_modules'
            if bundled_modules.is_dir():
                trial['NODE_PATH'] = os.pathsep.join(filter(None, [modules, str(bundled_modules)]))
            result = subprocess.run([candidate, '-e', "require('playwright')"], env=trial,
                                    stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                    stderr=subprocess.DEVNULL)
            if result.returncode == 0:
                chrome = Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
                if not trial.get('UI_BROWSER_PATH') and chrome.is_file():
                    trial['UI_BROWSER_PATH'] = str(chrome)
                return candidate, trial
    raise RuntimeError('Node and Playwright are required for UI checks. Set RELEASE_NODE and NODE_PATH if they are not installed in the standard locations.')


def caddy_runtime(project, environment):
    env = dict(environment)
    dockerfile = (project / 'web/Dockerfile').read_text()
    match = re.search(r'^FROM caddy:(\d+\.\d+\.\d+)-alpine\s*$', dockerfile, re.MULTILINE)
    if not match:
        raise RuntimeError('Cannot determine the pinned UI Caddy version from web/Dockerfile.')
    version = match.group(1)
    binary = env.get('CADDY_BIN')
    if not binary:
        state = Path(os.environ.get('RELEASE_STATE_DIR', str(project / '.local/release-tool' if TOOL_DIR == project / 'tools/release' else TOOL_DIR)))
        folder = state / 'cache/caddy' / version
        folder.mkdir(parents=True, exist_ok=True)
        binary = str(folder / 'caddy')
        if not Path(binary).is_file():
            print('Downloading pinned official Caddy UI-test runtime ' + version, flush=True)
            download_caddy(version, folder)
    actual = subprocess.check_output([binary, 'version'], env=env, stdin=subprocess.DEVNULL, text=True).split()[0]
    if actual != 'v' + version:
        raise RuntimeError('Caddy UI-test runtime must match the version pinned in web/Dockerfile.')
    env['CADDY_BIN'] = binary
    return env


def download_caddy(version, folder):
    systems = {'Darwin': 'mac', 'Linux': 'linux'}
    architectures = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'AMD64': 'amd64'}
    system, architecture = systems.get(platform.system()), architectures.get(platform.machine())
    if not system or not architecture:
        raise RuntimeError('Configure CADDY_BIN for this platform.')
    name = 'caddy_%s_%s_%s.tar.gz' % (version, system, architecture)
    base = 'https://github.com/caddyserver/caddy/releases/download/v' + version + '/'
    def fetch(name):
        request = urllib.request.Request(base + name, headers={'User-Agent': 'Sociaro-Release-UI-Runtime'})
        with urllib.request.urlopen(request, timeout=60) as response:
            return response.read()
    checksum_lines = fetch('caddy_%s_checksums.txt' % version).decode().splitlines()
    checksums = {line.split()[1].lstrip('*'): line.split()[0] for line in checksum_lines if len(line.split()) == 2}
    data = fetch(name)
    expected = checksums.get(name, '')
    algorithm = {64: hashlib.sha256, 128: hashlib.sha512}.get(len(expected))
    if algorithm is None or algorithm(data).hexdigest() != expected.lower():
        raise RuntimeError('Official Caddy download checksum mismatch.')
    with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as archive:
        member = archive.getmember('caddy')
        if not member.isfile():
            raise RuntimeError('The official Caddy archive has no regular runtime file.')
        temporary = folder / 'caddy.tmp'
        with archive.extractfile(member) as source, temporary.open('wb') as target:
            shutil.copyfileobj(source, target)
        temporary.chmod(0o755)
        temporary.replace(folder / 'caddy')


def verify(project, native_only=False, extended_only=False):
    if native_only and extended_only:
        raise ValueError('Native-only and extended-only cannot be combined.')
    project = Path(project).resolve()
    ci = None
    if not native_only:
        spec = importlib.util.spec_from_file_location('release_ci', TOOL_DIR / 'release_ci.py')
        ci = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(ci)
        ci.preflight()
    node, env = browser_environment(os.environ)
    evidence = project / 'dist/checks' / time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())
    evidence.mkdir(parents=True, exist_ok=True)
    env['GATEWAY_PERF_OUTPUT'] = str(evidence)
    env['UI_SCREENSHOT_DIR'] = str(evidence / 'screenshots')
    print('Evidence: ' + str(evidence), flush=True)
    env = caddy_runtime(project, env)
    if extended_only:
        print('NOT RUN: native Go/integration and browser suites (additional CI only).', flush=True)
        ci.verify(project, execute, env, node)
        return
    selected = project_ui_checks(project)
    edited = edited_ui_checks(project)
    early = [check for check in selected if 'scripts/ui/' + check[0] in edited]
    remaining = [check for check in selected if check not in early]
    with isolated_services(env) as env:
        def browser_check(arguments):
            script = project / 'scripts/ui' / arguments[0]
            if not script.is_file():
                raise RuntimeError('Required frontend check is missing: ' + arguments[0])
            variant = dict(env)
            identity = Path(arguments[0]).stem + '-' + hashlib.sha256('\0'.join(arguments).encode()).hexdigest()[:10]
            variant_dir = evidence / identity
            variant_dir.mkdir(parents=True, exist_ok=True)
            variant['UI_EVIDENCE_DIR'] = str(variant_dir)
            variant['UI_SCREENSHOT_DIR'] = str(variant_dir)
            flags = []
            for argument in arguments[1:]:
                if argument.startswith('UI_') and '=' in argument:
                    key, value = argument.split('=', 1)
                    variant[key] = str(variant_dir / value) if key == 'UI_SCREENSHOT_DIR' else value
                else:
                    flags.append(argument)
            if variant.get('UI_BROWSER_ENGINE') == 'webkit' or variant.get('UI_BROWSER') == 'webkit':
                variant.pop('UI_BROWSER_PATH', None)
            execute([node, str(script), *flags], project, variant)
        for arguments in early:
            browser_check(arguments)
        # These Go tests own their HTTP handlers and SQL schemas, but opt in
        # separately from the standalone scripts/ui browser checks above.
        profile_env = dict(env, GATEWAY_PROFILE_BROWSER_NODE=node,
                           GATEWAY_PROFILE_NAME_BROWSER_NODE=node,
                           UI_BROWSER_ENGINE='chromium')
        execute(['make', 'verify-ci', 'TEST_FLAGS=-count=1'], project, profile_env)
        # The name reconciliation fixture also supports WebKit. Reuse the same
        # disposable services and selected Node; never install another runtime.
        webkit_profile_env = dict(profile_env, UI_BROWSER_ENGINE='webkit')
        webkit_profile_env.pop('UI_BROWSER_PATH', None)
        execute(['go', 'test', '-race', '-count=1', '-run',
                 '^TestProfileNameBrowserReconciliation$', './internal/api'],
                project, webkit_profile_env)
        execute(['/usr/bin/python3', 'scripts/verify-headless.py'], project, env)
        execute(['make', 'build-ui'], project, env)
        for arguments in remaining:
            browser_check(arguments)
        for pattern in ('test_release*.py', 'test_stop_worker_release.py'):
            execute([sys.executable, '-m', 'unittest', 'discover', '-s', 'scripts/tests', '-p', pattern], project, env)
        execute([sys.executable, '-m', 'unittest', 'discover', '-s', str(TOOL_DIR / 'tests'), '-p', 'test_*.py'], project, env)
        print('PASS: isolated Go/integration checks, headless administration, independent UI and browser workflows.', flush=True)

    if native_only:
        print('NOT RUN: security scanners, Docker images, source-free install and real Grafana (native-only mode).', flush=True)
    else:
        ci.verify(project, execute, env, node)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--project', required=True)
    parser.add_argument('--failure-file', type=Path)
    parser.add_argument('--native-only', action='store_true', help='Explicitly omit security/container/install checks.')
    parser.add_argument('--extended-only', action='store_true', help='Run only security/container/install checks.')
    args = parser.parse_args(argv)
    if args.native_only and args.extended_only:
        parser.error('Select either native-only or extended-only')
    try:
        verify(args.project, native_only=args.native_only, extended_only=args.extended_only)
    except CheckFailure as error:
        if args.failure_file:
            args.failure_file.write_text(json.dumps({'check': error.check, 'exit_code': error.exit_code}))
        print(str(error), flush=True)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
