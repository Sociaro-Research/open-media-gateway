#!/usr/bin/env python3
"""Test releases, restarts and backup restoration in fresh disposable Compose projects.

Never pass a production configuration. Projects, volumes and loopback ports are
created by this runner; it does not operate on an existing deployment.
"""
import argparse
import contextlib
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


MEDIA = (b'Sociaro disposable Compose recovery result\n' * 26000)[:1024 * 1024]
MEDIA_HASH = hashlib.sha256(MEDIA).hexdigest()


def fixture():
    """An external provider survives application restarts and exposes test gates."""
    state_file = Path('/fixture-state/state.json')
    stored = json.loads(state_file.read_text()) if state_file.exists() else {}
    lock, jobs, opened = threading.Lock(), stored.get('jobs', {}), set(stored.get('open', []))
    changed = threading.Condition(lock)

    def persist():
        temporary = state_file.with_suffix('.tmp')
        temporary.write_text(json.dumps({'jobs': jobs, 'open': sorted(opened)}))
        temporary.replace(state_file)

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def json_reply(self, value):
            body = json.dumps(value).encode()
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', '0'))))
            if self.path == '/control':
                with changed:
                    opened.update(body['open'])
                    persist()
                    changed.notify_all()
                self.json_reply({'ok': True})
                return
            assert self.path == '/submit'
            task_id, prompt = body['TaskID'], body['Prompt']
            phase, kind = prompt.split(':', 1)
            with changed:
                job = jobs.setdefault(task_id, {'phase': phase, 'kind': kind, 'submissions': 0,
                                                'polls': 0, 'partial_transfers': 0})
                job['submissions'] += 1
                persist()
                if kind in ('submit', 'sync'):
                    changed.wait_for(lambda: phase + ':submit' in opened)
            result = {'ID': task_id}
            if kind == 'sync':
                result['ResultURL'] = 'http://fixture:9080/result?id=' + task_id
            self.json_reply(result)

        def do_GET(self):
            url = urllib.parse.urlsplit(self.path)
            if url.path == '/state':
                with lock:
                    value = {key: dict(job) for key, job in jobs.items()}
                self.json_reply(value)
                return
            task_id = urllib.parse.parse_qs(url.query)['id'][0]
            with changed:
                job = jobs[task_id]
                phase, kind = job['phase'], job['kind']
                if url.path == '/poll':
                    job['polls'] += 1
                    persist()
                    ready = kind in ('fast', 'archive') or phase + ':ready' in opened
                    if kind == 'poll':
                        changed.wait_for(lambda: phase + ':poll' in opened)
                elif url.path == '/result':
                    ready = True
                else:
                    raise ValueError('Unknown fixture route')
            if url.path == '/poll':
                self.json_reply({'Status': 'succeeded' if ready else 'running',
                                 'ResultURL': 'http://fixture:9080/result?id=' + task_id if ready else ''})
                return
            self.send_response(200)
            self.send_header('Content-Type', 'video/mp4')
            self.send_header('Content-Length', str(len(MEDIA)))
            self.end_headers()
            try:
                self.wfile.write(MEDIA[:4096])
                self.wfile.flush()
                with changed:
                    job['partial_transfers'] += 1
                    persist()
                    if kind in ('archive', 'sync'):
                        changed.wait_for(lambda: phase + ':archive' in opened)
                self.wfile.write(MEDIA[4096:])
            except (BrokenPipeError, ConnectionResetError):
                pass

    server = ThreadingHTTPServer(('0.0.0.0', 9080), Handler)
    server.daemon_threads = True
    server.serve_forever()


def unused_port():
    with socket.socket() as connection:
        connection.bind(('127.0.0.1', 0))
        return connection.getsockname()[1]


def eventually(label, check, timeout=120):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            value = check()
            if value:
                return value
        except (OSError, AssertionError, subprocess.CalledProcessError) as error:
            last = error
        time.sleep(.25)
    raise RuntimeError(label + (': ' + str(last) if last else ''))


class Campaign:
    def __init__(self, source, output, workspace):
        self.source, self.output, self.workspace = source, output, workspace
        self.prefix = 'gateway-recovery-' + uuid.uuid4().hex[:12]
        self.projects, self.commands, self.results = [], [], {}
        self.command_env = {key: os.environ[key] for key in ('PATH', 'HOME', 'TMPDIR') if key in os.environ}
        self.key = None
        self.log = (output / 'commands.log').open('wb')
        self.ports = [unused_port() for _ in range(4)]
        assert len(set(self.ports)) == 4, 'Port allocation collided; retry with a fresh runner'
        self.base = 'http://127.0.0.1:' + str(self.ports[0])
        self.fixture_base = 'http://127.0.0.1:' + str(self.ports[3])
        self.metadata = {'started_at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
                         'project': self.prefix, 'media_sha256': MEDIA_HASH,
                         'isolation': 'new projects/volumes, synthetic secrets, loopback-only published ports',
                         'host_reboot': 'not executed; application dependencies restart only'}

    def command(self, args, cwd=None, data=None, private=False, binary=False, check=True):
        self.commands.append(args)
        if not private:
            self.log.write((json.dumps(args) + '\n').encode())
        self.log.flush()
        result = subprocess.run(args, cwd=cwd, input=data, env=self.command_env,
                                stdin=subprocess.DEVNULL if data is None else None,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if not private:
            self.log.write((b'' if binary else result.stdout) + result.stderr)
            self.log.flush()
        if check and result.returncode:
            raise RuntimeError('Command failed: ' + json.dumps(args) + '; inspect commands.log')
        return result

    def compose(self, project=None, release='b'):
        project = project or self.prefix
        assert re.fullmatch(re.escape(self.prefix) + r'(?:-restore)?', project)
        configuration = self.restore_workspace if project.endswith('-restore') else self.workspace
        directory = configuration / 'application' if project.endswith('-restore') else self.workspace / release
        return ['docker', 'compose', '--project-name', project, '--project-directory', str(directory),
                '--env-file', str(configuration / 'test.env'), '-f', str(directory / 'compose.yaml'),
                '-f', str(configuration / 'test.compose.yaml'), '--profile', 'ui']

    def dc(self, *args, project=None, release='b', **kwargs):
        return self.command([*self.compose(project, release), *args], **kwargs)

    def sql(self, query, project=None):
        return self.dc('exec', '-T', 'postgres', 'psql', '-U', 'media_gateway', '-d', 'media_gateway',
                       '-At', '-v', 'ON_ERROR_STOP=1', '-c', query, project=project).stdout.decode().strip()

    def task_row(self, task_id):
        uuid.UUID(task_id)
        return json.loads(self.sql("SELECT json_build_object('status',status,'provider_id',provider_task_id,"
                                   "'lease',worker_lease_until,'error',error) FROM tasks WHERE id='" + task_id + "'"))

    def request(self, path, body=None, expected=200, base=None, token=True, host=None):
        headers = {'User-Agent': 'Sociaro-Isolated-Compose-Recovery'}
        if token and self.key:
            headers['Authorization'] = 'Bearer ' + self.key
        if host:
            headers['Host'] = host
        data = None
        if body is not None:
            headers['Content-Type'] = 'application/json'
            data = json.dumps(body).encode()
        request = urllib.request.Request((base or self.base) + path, data, headers)
        try:
            response = urllib.request.urlopen(request, timeout=5)
        except urllib.error.HTTPError as error:
            response = error
        result = response.read()
        assert response.status == expected, (path, response.status, result[:160])
        return result

    def state(self):
        return json.loads(self.request('/state', base=self.fixture_base, token=False))

    def open(self, *gates):
        self.request('/control', {'open': list(gates)}, base=self.fixture_base, token=False)

    def submit(self, phase, kind, provider='mock'):
        result = json.loads(self.request('/v1/generations', {'model': provider + '/test-video',
                                                          'prompt': phase + ':' + kind}, 202))
        uuid.UUID(result['id'])
        return result['id']

    def reconcile(self, ids, failed=()):
        for task_id in ids:
            def terminal():
                task = json.loads(self.request('/v1/generations/' + task_id))
                return task if task['status'] in ('completed', 'failed') else None
            task = eventually('Task did not recover: ' + task_id, terminal, 150)
            if task_id in failed:
                assert task['status'] == 'failed' and 'not resubmitted' in str(task), task
            else:
                assert task['status'] == 'completed', task
                content = self.request('/v1/generations/' + task_id + '/content')
                assert hashlib.sha256(content).hexdigest() == MEDIA_HASH, 'Corrupt result: ' + task_id
            assert self.state()[task_id]['submissions'] == 1, 'Repeated submission: ' + task_id

    def prepare(self):
        self.metadata['docker'] = self.command(['docker', 'version', '--format', '{{.Server.Version}}']).stdout.decode().strip()
        self.metadata['compose'] = self.command(['docker', 'compose', 'version']).stdout.decode().strip()
        assert not self.command(['docker', 'ps', '--all', '--quiet', '--filter',
                                 'label=com.docker.compose.project=' + self.prefix]).stdout.strip()
        for suffix in ('postgres-data', 'redis-data', 'media-data'):
            assert self.command(['docker', 'volume', 'inspect', self.prefix + '_' + suffix], check=False).returncode != 0
        snapshot = self.source / 'recovery-source.json'
        if snapshot.exists():
            self.metadata['source_snapshot'] = json.loads(snapshot.read_text())
        assert shutil.disk_usage(self.workspace).free >= 3 * 1024**3, 'Need at least 3 GiB free before builds'
        # Snapshot source without inherited .env, Git state, customer data or artifacts.
        for release in ('a', 'b'):
            destination = self.workspace / release
            destination.mkdir()
            for name in ('cmd', 'internal', 'plugins', 'web'):
                shutil.copytree(self.source / name, destination / name)
            (destination / 'docs').mkdir()
            for name in ('go.mod', 'go.sum', 'Dockerfile', '.dockerignore', 'compose.yaml', 'LICENSE', 'NOTICE',
                         'docs/spec.go', 'docs/openapi.yaml'):
                shutil.copy2(self.source / name, destination / name)
        body = (self.source / 'internal/reliability/release_test.go').read_text()
        provider = re.search(r'const releaseProviderSource = `([\s\S]+?)`', body).group(1)
        provider = provider.replace('u.Hostname()!="127.0.0.1"', '(u.Hostname()!="127.0.0.1" && u.Hostname()!="fixture")')
        assert 'u.Hostname()!="fixture"' in provider, 'Fixture host guard changed'
        for release in ('a', 'b'):
            (self.workspace / release / 'plugins/provider/mock/definition.go').write_text(provider)
        registry = self.workspace / 'b/plugins/provider/registry.go'
        text = registry.read_text()
        changed = text.replace('mock.Definition(),', 'mock.Definition(), mock.ReleaseDefinition(),', 1)
        assert changed != text, 'Provider registration insertion point changed'
        registry.write_text(changed)
        settings = {'PORT': self.ports[1], 'UI_PORT': self.ports[2],
                    'CLIENT_API_KEY': 'disposable-compose-service-key',
                    'WEBHOOK_SIGNING_SECRET': 'disposable-compose-webhooks-32',
                    'MEDIA_URL_SIGNING_SECRET': 'disposable-compose-media-signing-32',
                    'PROVIDER_CREDENTIALS_ENCRYPTION_KEY': 'disposable-compose-provider-cipher-32',
                    'SESSION_SIGNING_SECRET': 'disposable-compose-browser-signing-32',
                    'MEDIA_PROVIDER': 'mock', 'PROVIDER_POLL_INTERVAL': '100ms', 'DISPATCH_INTERVAL': '100ms',
                    'PROVIDER_HTTP_TIMEOUT': '2m', 'WORKER_SHUTDOWN_TIMEOUT': '90s',
                    'WORKER_STOP_GRACE_PERIOD': '100s', 'DOCS_HOST': 'recovery-docs.invalid',
                    'UI_PUBLIC_URL': self.base, 'AUTH_PUBLIC_URL': self.base,
                    'DOCS_PUBLIC_URL': 'https://recovery-docs.invalid',
                    'RECOVERY_IMAGE': self.prefix + ':a'}
        self.env = settings
        self.write_env()
        script = Path(__file__).resolve()
        caddyfile = self.workspace / 'Caddyfile'
        caddyfile.write_text('{\n admin off\n auto_https off\n}\n:8080 {\n reverse_proxy ui:8080\n}\n')
        quote = json.dumps
        (self.workspace / 'fixture-state').mkdir()
        override = f'''services:
  gateway:
    image: ${{RECOVERY_IMAGE}}
    environment:
      RELEASE_TEST_PROVIDER_URL: http://fixture:9080
      OUTBOUND_ALLOW_PRIVATE_HOSTS: fixture
    mem_limit: 256m
  worker:
    image: ${{RECOVERY_IMAGE}}
    environment:
      RELEASE_TEST_PROVIDER_URL: http://fixture:9080
      OUTBOUND_ALLOW_PRIVATE_HOSTS: fixture
    mem_limit: 256m
  ui:
    image: {self.prefix}:ui
    mem_limit: 128m
  postgres:
    mem_limit: 256m
  redis:
    mem_limit: 128m
  caddy:
    image: caddy:2.11.7-alpine
    ports: ["127.0.0.1:{self.ports[0]}:8080"]
    volumes: [{quote(str(caddyfile) + ':/etc/caddy/Caddyfile:ro')}]
    depends_on:
      ui:
        condition: service_healthy
    mem_limit: 128m
  fixture:
    image: python:3.13-alpine
    command: ["python", "/fixture.py", "--fixture"]
    ports: ["127.0.0.1:{self.ports[3]}:9080"]
    volumes:
      - {quote(str(script) + ':/fixture.py:ro')}
      - {quote(str(self.workspace / 'fixture-state') + ':/fixture-state')}
      - media-data:/observed-media:ro
    mem_limit: 128m
'''
        (self.workspace / 'test.compose.yaml').write_text(override)
        self.dc('config', '--quiet', release='a')
        config = json.loads(self.dc('config', '--format', 'json', release='a', private=True).stdout)
        for name, volume in config['volumes'].items():
            assert not volume.get('external') and volume['name'] == self.prefix + '_' + name, 'Non-isolated volume'
        for service in config['services'].values():
            for port in service.get('ports', []):
                assert port['host_ip'] == '127.0.0.1', 'Public test port refused'
        self.metadata['project_started_empty'] = True
        self.dc('build', 'gateway', 'worker', 'ui', release='a')
        self.env['RECOVERY_IMAGE'] = self.prefix + ':b'
        self.write_env()
        self.dc('build', 'gateway', 'worker')
        for release in ('a', 'b'):
            self.metadata['image_' + release] = self.command(['docker', 'image', 'inspect', '--format', '{{.Id}}',
                                                             self.prefix + ':' + release]).stdout.decode().strip()
        assert self.metadata['image_a'] != self.metadata['image_b'], 'Release images are identical'
        self.env['RECOVERY_IMAGE'] = self.prefix + ':a'
        self.write_env()
        self.projects.append(self.prefix)
        self.dc('up', '-d', '--wait', 'postgres', 'redis', release='a')
        account = json.loads(self.dc('run', '--rm', '--no-deps', 'gateway', 'admin', 'init', '--email',
                                     'compose-recovery@example.test', release='a', private=True).stdout)
        self.key = account['secret']
        self.metadata['test_account_id'] = account['key']['user_id']
        # The gateway image initializes media volume ownership before the
        # fixture mounts that volume for read-only partial-transfer inspection.
        self.dc('up', '-d', '--wait', 'fixture', release='a')
        self.dc('up', '-d', '--wait', 'gateway', 'ui', 'caddy', release='a')
        self.dc('up', '-d', 'worker', release='a')
        self.check_frontend()

    def write_env(self):
        (self.workspace / 'test.env').write_text(''.join(str(k) + '=' + str(v) + '\n' for k, v in self.env.items()))

    def check_frontend(self):
        eventually('Front proxy unavailable', lambda: self.request('/healthz'))
        self.request('/', token=False)
        self.request('/ui/theme.js', token=False)
        self.request('/ui/app.js', expected=401, token=False)
        self.request('/', token=False, host='recovery-docs.invalid')
        self.request('/site.json', token=False, host='recovery-docs.invalid')
        self.request('/openapi.json', token=False, host='recovery-docs.invalid')
        self.request('/v1/models', expected=404, token=False, host='recovery-docs.invalid')
        self.request('/v1/system/settings')
        self.request('/v1/models', expected=401, token=False)
        for service in ('ui', 'caddy'):
            self.dc('exec', '-T', service, 'caddy', 'validate', '--config', '/etc/caddy/Caddyfile', '--adapter', 'caddyfile')

    def stop_guard(self, release='b'):
        return [sys.executable, str(self.source / 'scripts/stop-worker-for-release.py'), '--', *self.compose(release=release)[2:]]

    def clean_drain(self, phase, change_release=False):
        started = time.monotonic()
        ids = [self.submit(phase, 'pending') for _ in range(8)]
        eventually('Pending provider IDs not saved', lambda: all(self.task_row(i)['provider_id'] for i in ids))
        archive = self.submit(phase, 'archive')
        ids.append(archive)
        eventually('Partial archive did not start', lambda: self.state().get(archive, {}).get('partial_transfers', 0))
        eventually('No nonempty private archive file', lambda: self.dc('exec', '-T', 'fixture', 'python', '-c',
                   "from pathlib import Path; print(any(p.stat().st_size > 0 for p in Path('/observed-media').rglob('.media-*.part')))"
                   ).stdout.strip() == b'True')
        submission, synchronous = self.submit(phase, 'submit'), self.submit(phase, 'sync')
        ids += [submission, synchronous]
        eventually('Submissions not accepted upstream', lambda: all(self.state().get(i, {}).get('submissions') == 1
                                                                   for i in (submission, synchronous)))
        lease = self.task_row(submission)['lease']
        stop_log = (self.output / (phase + '-stop.log')).open('wb')
        process = subprocess.Popen(self.stop_guard('a' if change_release else 'b'),
                                   stdin=subprocess.DEVNULL, stdout=stop_log, stderr=subprocess.STDOUT,
                                   env=self.command_env)
        try:
            eventually('Worker did not start drain', lambda: b'worker draining active operations' in
                       self.dc('logs', '--no-color', 'worker').stdout)
            queued = [self.submit(phase, 'pending') for _ in range(4)]
            ids += queued
            eventually('Ownership was not renewed while stopping', lambda: self.task_row(submission)['lease'] > lease, 40)
            assert process.poll() is None, 'Worker exited before provider response'
            assert all(self.task_row(i)['status'] == 'queued' and i not in self.state() for i in queued)
            self.open(phase + ':submit', phase + ':archive')
            assert process.wait(timeout=35) == 0, 'Release guard rejected clean drain'
        finally:
            if process.poll() is None:
                self.open(phase + ':submit', phase + ':archive')
                with contextlib.suppress(subprocess.TimeoutExpired):
                    process.wait(timeout=35)
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=5)
            stop_log.close()
        assert self.task_row(synchronous)['status'] == 'succeeded'
        assert self.state()[synchronous]['polls'] == 0
        assert self.task_row(ids[0])['status'] == 'running', 'Drain waited for full upstream generation'
        if change_release:
            old = self.dc('ps', '--all', '--quiet', 'gateway').stdout.strip()
            self.env['RECOVERY_IMAGE'] = self.prefix + ':b'
            self.write_env()
            self.dc('up', '-d', '--no-deps', '--wait', 'gateway')
            self.dc('up', '-d', '--no-deps', '--wait', 'ui')
            self.dc('up', '-d', '--no-deps', 'worker')
            self.dc('up', '-d', '--no-deps', 'caddy')
            assert self.dc('ps', '--all', '--quiet', 'gateway').stdout.strip() != old
            eventually('Release proxy not ready', lambda: self.request('/healthz'))
            ids += [self.submit(phase, 'fast'), self.submit(phase, 'fast', 'release-probe')]
        else:
            self.dc('stop', 'caddy', 'ui', 'gateway')
            self.dc('stop', 'redis', 'postgres')
            self.dc('up', '-d', '--wait', 'postgres', 'redis')
            self.dc('up', '-d', '--no-deps', '--wait', 'gateway')
            self.dc('up', '-d', '--no-deps', '--wait', 'ui')
            self.dc('up', '-d', '--no-deps', 'caddy', 'worker')
            eventually('Restarted proxy not ready', lambda: self.request('/healthz'))
            ids.append(self.submit(phase, 'fast'))
        self.open(phase + ':ready')
        self.reconcile(ids)
        self.check_frontend()
        self.results[phase] = {'accepted': len(ids), 'succeeded': len(ids), 'failed': 0,
                               'duplicate_submissions': 0, 'intact_downloads': len(ids),
                               'seconds': round(time.monotonic() - started, 2)}
        print('PASS:', phase, self.results[phase], flush=True)

    def abrupt_restart(self):
        phase, started = 'abrupt', time.monotonic()
        ids = [self.submit(phase, 'pending') for _ in range(8)]
        eventually('Durable IDs missing before crash', lambda: all(self.task_row(i)['provider_id'] for i in ids))
        polling = self.submit(phase, 'poll')
        archive = self.submit(phase, 'archive')
        ambiguous = self.submit(phase, 'submit')
        ids += [polling, archive, ambiguous]
        eventually('Crash gates not reached', lambda: self.state().get(polling, {}).get('polls', 0) > 0 and
                   self.state().get(archive, {}).get('partial_transfers', 0) > 0 and
                   self.state().get(ambiguous, {}).get('submissions') == 1)
        too_short = self.command(self.stop_guard()[:2] + ['--timeout', '1', '--', *self.compose()[2:]], check=False)
        assert too_short.returncode != 0, 'Short release stop budget was accepted'
        # Actually kill every application/dependency service, including PG/Redis.
        self.dc('kill', '-s', 'SIGKILL', 'worker', 'gateway', 'ui', 'caddy', 'redis', 'postgres')
        refused = self.command(self.stop_guard(), check=False)
        assert refused.returncode != 0, 'Release continued after forced worker exit'
        self.open(phase + ':submit', phase + ':archive', phase + ':poll', phase + ':ready')
        self.dc('up', '-d', '--wait', 'postgres', 'redis')
        self.dc('up', '-d', '--no-deps', '--wait', 'gateway')
        self.dc('up', '-d', '--no-deps', '--wait', 'ui')
        self.dc('up', '-d', '--no-deps', 'caddy', 'worker')
        eventually('Crash recovery proxy not ready', lambda: self.request('/healthz'))
        ids.append(self.submit(phase, 'fast'))
        self.reconcile(ids, failed=[ambiguous])
        eventually('Crash orphan was not reclaimed', lambda: not self.temporary_files())
        self.check_frontend()
        self.results[phase] = {'accepted': len(ids), 'succeeded': len(ids) - 1,
                               'explicit_ambiguous_failure': 1, 'duplicate_submissions': 0,
                               'intact_downloads': len(ids) - 1,
                               'production_lease_seconds': 60,
                               'orphan_temporary_files': 0,
                               'seconds': round(time.monotonic() - started, 2)}
        print('PASS:', phase, self.results[phase], flush=True)

    def temporary_files(self):
        code = "from pathlib import Path; import json; print(json.dumps([str(p.relative_to('/observed-media')) for p in Path('/observed-media').rglob('.*') if p.is_file() and (p.name.startswith('.media-') or p.name.startswith('.reference-'))]))"
        return json.loads(self.dc('exec', '-T', 'fixture', 'python', '-c', code).stdout)

    def temporary_cleanup(self):
        started = time.monotonic()
        archive = self.submit('temporary-cleanup', 'archive')
        eventually('Cleanup transfer did not start', lambda: self.state().get(archive, {}).get('partial_transfers', 0))
        active = eventually('Cleanup transfer has no private file', self.temporary_files)
        assert len(active) == 1, active
        media_volume = self.prefix + '_media-data'
        seed = "from pathlib import Path; import os,time; root=Path('/media'); names=['.media-v2-101.part','.reference-v2-102.part','.media-103.part','.reference-104']; [(root/name).write_bytes(b'x'*4096) for name in names]; [os.utime(root/name,(time.time()-48*3600,time.time()-48*3600)) for name in names[2:]]; os.utime(root/" + repr(active[0]) + ",(time.time()-48*3600,time.time()-48*3600))"
        first_worker = self.dc('ps', '--quiet', 'worker').stdout.decode().strip()
        assert re.fullmatch(r'[0-9a-f]{64}', first_worker), 'Expected one worker before cleanup test'
        self.dc('up', '-d', '--no-deps', '--scale', 'worker=2', 'worker')
        replicas = self.dc('ps', '--quiet', 'worker').stdout.decode().split()
        assert len(replicas) == 2 and first_worker in replicas, 'Expected two worker replicas'
        second_worker = next(worker for worker in replicas if worker != first_worker)
        # Freeze the owner, including its maintenance loop. Only the second
        # worker can sweep, while the first still owns its kernel file lock.
        self.command(['docker', 'pause', first_worker])
        try:
            self.command(['docker', 'run', '--rm', '--user', '65532:65532', '--mount',
                          'type=volume,src=' + media_volume + ',dst=/media', 'python:3.13-alpine', 'python', '-c', seed])
            # Compose up refuses an already paused replica. Restart only the
            # idle second container to trigger its startup maintenance.
            self.command(['docker', 'restart', '--timeout', '100', second_worker])
            eventually('Startup cleanup did not preserve active/remove orphan files',
                       lambda: self.temporary_files() == active, 20)
        finally:
            self.command(['docker', 'unpause', first_worker])
            self.open('temporary-cleanup:archive')
        self.reconcile([archive])
        eventually('Temporary files remained after completion', lambda: not self.temporary_files())
        self.command(self.stop_guard())
        self.dc('up', '-d', '--no-deps', '--scale', 'worker=1', 'worker')
        self.results['temporary_cleanup'] = {'shared_volume_workers': 2, 'active_transfer_preserved': True,
                                            'new_orphans_removed': 2, 'aged_legacy_orphans_removed': 2,
                                            'intact_downloads': 1, 'orphan_temporary_files': 0,
                                            'seconds': round(time.monotonic() - started, 2)}
        print('PASS: cross-worker temporary cleanup', self.results['temporary_cleanup'], flush=True)

    def storage_pressure(self):
        dockerfile = self.workspace / 'Dockerfile.storage-test'
        dockerfile.write_text((self.workspace / 'b/Dockerfile').read_text() + '''
FROM build AS storage-test-build
RUN CGO_ENABLED=0 GOOS=linux go test -trimpath -c -o /out/storage-tests ./internal/storage
FROM scratch AS storage-test
COPY --from=storage-test-build /out/storage-tests /storage-tests
USER 65532:65532
ENTRYPOINT ["/storage-tests"]
''')
        tag = self.prefix + ':storage-test'
        # Use the same Compose/BuildKit engine as the shipped application build.
        # Plain docker build can fall back to a legacy builder without --chmod.
        override = self.workspace / 'storage-test.compose.yaml'
        override.write_text(json.dumps({'services': {'storage-test': {'image': tag, 'build': {
            'context': str(self.workspace / 'b'), 'dockerfile': str(dockerfile), 'target': 'storage-test'}}}}))
        self.command([*self.compose(), '-f', str(override), 'build', 'storage-test'])
        result = self.command(['docker', 'run', '--rm', '--network', 'none', '--memory', '128m',
                               '--tmpfs', '/pressure:rw,size=8m,uid=65532,gid=65532,mode=0750',
                               '--env', 'GATEWAY_STORAGE_PRESSURE_ROOT=/pressure', tag,
                               '-test.v', '-test.run=^TestTemporaryCleanupFullFilesystem$', '-test.timeout=30s'])
        assert b'--- PASS: TestTemporaryCleanupFullFilesystem' in result.stdout
        (self.output / 'storage-pressure.log').write_bytes(result.stdout + result.stderr)
        self.results['storage_pressure'] = {'filesystem_capacity_mib': 8, 'real_enospc': True,
                                            'orphan_bytes_reclaimed': 4 * 1024**2,
                                            'active_transfer_preserved': True, 'published_results_intact': 2,
                                            'archive_after_reclaim_bytes': 1024**2, 'partial_publications': 0}
        print('PASS: full disposable filesystem', self.results['storage_pressure'], flush=True)

    def backup_restore(self):
        started = time.monotonic()
        pending = [self.submit('restore-pending', 'pending') for _ in range(5)]
        eventually('Pending IDs missing before backup', lambda: all(self.task_row(i)['provider_id'] for i in pending))
        self.command(self.stop_guard())
        self.dc('stop', 'gateway', 'ui', 'caddy')
        backup = self.output / 'backup'
        backup.mkdir(mode=0o700)
        dump = self.dc('exec', '-T', 'postgres', 'pg_dump', '-Fc', '-U', 'media_gateway', '-d', 'media_gateway', binary=True).stdout
        (backup / 'database.dump').write_bytes(dump)
        self.dc('exec', '-T', 'postgres', 'pg_restore', '--list', data=dump)
        media = self.prefix + '_media-data'
        archive = self.command(['docker', 'run', '--rm', '--entrypoint', 'tar',
                                '--mount', 'type=volume,src=' + media + ',dst=/media,readonly',
                                'postgres:17-alpine', '-cf', '-', '-C', '/media', '.'], binary=True).stdout
        (backup / 'media.tar').write_bytes(archive)
        with tarfile.open(backup / 'application.tar.gz', 'w:gz') as tar:
            tar.add(self.workspace / 'b', arcname='application')
            for name in ('test.env', 'test.compose.yaml', 'Caddyfile'):
                tar.add(self.workspace / name, arcname=name)
        with tarfile.open(backup / 'application.tar.gz') as tar:
            assert any(member.name == 'application/compose.yaml' for member in tar)
        images = self.command(['docker', 'image', 'save', self.prefix + ':b', self.prefix + ':ui'], binary=True).stdout
        (backup / 'images.tar').write_bytes(images)
        self.dc('stop', 'postgres', 'redis')
        self.dc('down', '--remove-orphans')
        self.command(['docker', 'image', 'rm', self.prefix + ':b', self.prefix + ':ui'])
        self.command(['docker', 'image', 'load'], data=images)
        restored = self.prefix + '-restore'
        self.projects.append(restored)
        # Extract the actual backed-up application/configuration into a fresh tree.
        extracted = self.workspace / 'restored'
        extracted.mkdir()
        self.restore_workspace = extracted
        with tarfile.open(backup / 'application.tar.gz') as tar:
            tar.extractall(extracted, filter='data')
        assert (extracted / 'application/compose.yaml').read_bytes() == (self.workspace / 'b/compose.yaml').read_bytes()
        override = extracted / 'test.compose.yaml'
        override.write_text(override.read_text().replace(str(self.workspace / 'Caddyfile'), str(extracted / 'Caddyfile')))
        self.dc('up', '-d', '--wait', 'postgres', 'redis', 'fixture', project=restored)
        self.dc('exec', '-T', 'postgres', 'pg_restore', '--exit-on-error', '-U', 'media_gateway',
                '-d', 'media_gateway', data=dump, project=restored)
        restore_volume = restored + '_media-data'
        self.command(['docker', 'volume', 'create', '--label', 'com.docker.compose.project=' + restored,
                      '--label', 'com.docker.compose.volume=media-data', restore_volume])
        self.command(['docker', 'run', '--interactive', '--rm', '--entrypoint', 'tar',
                      '--mount', 'type=volume,src=' + restore_volume + ',dst=/media',
                      'postgres:17-alpine', '-xf', '-', '-C', '/media'], data=archive)
        self.dc('up', '-d', '--no-deps', '--wait', 'gateway', project=restored)
        self.dc('up', '-d', '--no-deps', '--wait', 'ui', project=restored)
        self.dc('up', '-d', '--no-deps', 'caddy', 'worker', project=restored)
        self.check_frontend_for_restore(restored)
        self.open('restore-pending:ready')
        self.reconcile(pending)
        ids = json.loads(self.sql("SELECT json_agg(id) FROM tasks WHERE status='succeeded'", project=restored))
        for task_id in ids:
            assert hashlib.sha256(self.request('/v1/generations/' + task_id + '/content')).hexdigest() == MEDIA_HASH
        task_id = self.submit('restore', 'fast')
        self.reconcile([task_id])
        state = json.loads(self.sql("SELECT json_build_object('all',count(*),'done',count(*) FILTER (WHERE status='succeeded'),"
                                   "'failed',count(*) FILTER (WHERE status='failed'),'pending',count(*) FILTER (WHERE status IN ('queued','running'))) FROM tasks",
                                   project=restored))
        assert state['pending'] == 0 and state['failed'] == 1 and state['done'] == len(ids) + 1, state
        self.results['restore'] = {'restored_intact_downloads': len(ids), 'new_generation': 'passed',
                                   'pending_jobs_resumed': len(pending), 'saved_images_loaded': True,
                                   'original_admin_key': 'authorized', 'database_counts': state,
                                   'separate_new_volumes': True, 'seconds': round(time.monotonic() - started, 2)}
        print('PASS: full backup restoration', self.results['restore'], flush=True)

    def check_frontend_for_restore(self, project):
        self.check_frontend_http()
        for service in ('ui', 'caddy'):
            self.dc('exec', '-T', service, 'caddy', 'validate', '--config', '/etc/caddy/Caddyfile', '--adapter', 'caddyfile', project=project)

    def check_frontend_http(self):
        eventually('Restored proxy unavailable', lambda: self.request('/healthz'))
        self.request('/v1/system/settings')
        self.request('/v1/system/settings', expected=401, token=False)
        self.request('/', token=False)
        self.request('/ui/theme.js', token=False)

    def cleanup(self):
        state_file = self.workspace / 'fixture-state/state.json'
        if state_file.exists():
            shutil.copy2(state_file, self.output / 'provider-fixture-state.json')
        for project in reversed(self.projects):
            self.dc('logs', '--no-color', project=project, check=False)
            self.dc('down', '--remove-orphans', project=project, check=False)
            # Never down -v or enumerate another application's volumes.
            for suffix in ('postgres-data', 'redis-data', 'media-data'):
                volume = project + '_' + suffix
                inspected = self.command(['docker', 'volume', 'inspect', volume], check=False)
                if inspected.returncode == 0:
                    labels = json.loads(inspected.stdout)[0].get('Labels') or {}
                    assert labels.get('com.docker.compose.project') == project, 'Refuse foreign volume deletion'
                    self.command(['docker', 'volume', 'rm', volume])
            assert not self.command(['docker', 'ps', '--all', '--quiet', '--filter',
                                     'label=com.docker.compose.project=' + project]).stdout.strip()
        for tag in (':a', ':b', ':ui', ':storage-test'):
            self.command(['docker', 'image', 'rm', self.prefix + tag], check=False)
        self.metadata['cleanup'] = 'test containers/networks/volumes removed; existing deployments untouched'
        self.metadata['finished_at'] = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
        self.metadata['results'] = self.results
        (self.output / 'results.json').write_text(json.dumps(self.metadata, indent=2) + '\n')
        self.log.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fixture', action='store_true', help=argparse.SUPPRESS)
    parser.add_argument('--output', type=Path, help='New evidence directory, including synthetic backups')
    args = parser.parse_args()
    if args.fixture:
        fixture()
        return
    if sys.version_info < (3, 12):
        parser.error('The recovery controller requires Python 3.12 or newer')
    if not args.output:
        parser.error('--output is required')
    os.umask(0o077)
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False, mode=0o700)
    source = Path(__file__).resolve().parents[2]
    with tempfile.TemporaryDirectory(prefix='gateway-compose-recovery-') as directory:
        campaign = Campaign(source, output, Path(directory))
        try:
            campaign.prepare()
            campaign.clean_drain('release', change_release=True)
            campaign.clean_drain('graceful')
            campaign.abrupt_restart()
            campaign.temporary_cleanup()
            campaign.storage_pressure()
            campaign.backup_restore()
            campaign.metadata['status'] = 'passed'
        except BaseException as error:
            campaign.metadata['status'] = 'failed'
            campaign.metadata['error'] = str(error)
            raise
        finally:
            try:
                campaign.cleanup()
            finally:
                hashes = {str(path.relative_to(output)): hashlib.sha256(path.read_bytes()).hexdigest()
                          for path in sorted(output.rglob('*')) if path.is_file() and path.name != 'manifest-sha256.json'}
                (output / 'manifest-sha256.json').write_text(json.dumps(hashes, indent=2) + '\n')


if __name__ == '__main__':
    main()
