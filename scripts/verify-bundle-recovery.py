#!/usr/bin/env python3
"""Execute the bundle recovery guide against synthetic data in fresh Compose projects.

Requires four explicit local test images; never accepts an existing project or .env.
Keeps private evidence under a new output directory. No live services or paid calls.
"""
import argparse
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import tarfile
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def guide_blocks(text):
    return dict(re.findall(r'<!-- gw167:(\w+) -->\n```sh\n(.*?)\n```', text, re.S))


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def verify(output, images, https=False):
    output = output.resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    project = 'gw167-test-' + uuid.uuid4().hex
    recovery = project + '-recovery'
    # Deliberately discard all inherited application and Compose configuration.
    env = {key: os.environ[key] for key in ('PATH', 'HOME', 'DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_CONFIG') if key in os.environ}
    env.update(GW_BACKUP=str(output / 'backup'), GW_RESTORE=str(output / 'recovery'), GW_RECOVERY_PROJECT=recovery)
    backup = Path(env['GW_BACKUP'])
    backup.mkdir(mode=0o700)
    projects = [project, recovery]
    logs = (output / 'commands.log').open('w')
    versions = {}

    def run(args, cwd=None, data=None, check=True, command_env=None):
        # Output is retained privately; no keys or resolved config are logged.
        result = subprocess.run(args, cwd=cwd, env=command_env or env, input=data, capture_output=True)
        if args[:3] != ['docker', 'compose', 'config']:
            logs.write('COMMAND ' + ' '.join(args) + '\n')
        logs.flush()
        if check and result.returncode:
            raise RuntimeError('Command failed (private evidence retained): ' + ' '.join(args[:3]) + '\n' + result.stderr.decode()[-2000:])
        return result

    def dc(directory, *args, data=None):
        return run(['docker', 'compose', *args], cwd=directory, data=data).stdout

    source = None
    try:
        versions['docker'] = run(['docker', 'version', '--format', '{{.Server.Version}}']).stdout.decode().strip()
        versions['compose'] = run(['docker', 'compose', 'version', '--short']).stdout.decode().strip()
        package = load('package_release', ROOT / 'scripts/package-release.py')
        archive = package.package('v0.0.0-recovery-test', output / 'package')
        with tarfile.open(archive) as tar:
            tar.extractall(output / 'source')  # Our freshly generated allowlisted bundle only.
        source = output / 'source/media-gateway-v0.0.0-recovery-test'
        initialize = load('initialize_bundle', source / 'initialize.py')
        initialize.initialize(source, project_name=project, public_url='https://gateway.example.test' if https else None)
        values = dict(line.split('=', 1) for line in (source / '.env').read_text().splitlines() if line and not line.startswith('#'))
        values.update({key.upper() + '_IMAGE': value for key, value in images.items()})
        api_port = port()
        values.update(COMPOSE_FILE='compose.yaml:compose.release.yaml:' + ('compose.https.yaml:' if https else '') + 'compose.synthetic.yaml',
                      COMPOSE_PROFILES='ui,monitoring', MOCK_DELAY='10ms', PROVIDER_POLL_INTERVAL='20ms',
                      DISPATCH_INTERVAL='20ms', WORKER_SHUTDOWN_TIMEOUT='5s', WORKER_STOP_GRACE_PERIOD='20s',
                      AUTH_PUBLIC_URL=f'http://127.0.0.1:{api_port}', UI_PUBLIC_URL=f'http://127.0.0.1:{api_port}')
        (source / '.env').write_text(''.join(key + '=' + value + '\n' for key, value in values.items()))
        (source / 'compose.synthetic.yaml').write_text(
            'services:\n  gateway:\n    ports: !override\n      - "127.0.0.1:' + str(api_port) + ':8080"\n'
            '  ui:\n    ports: !reset []\n' +
            ('  https:\n    ports: !reset []\n' if https else '') +
            '  prometheus:\n    ports: !reset []\n')
        dc(source, 'create', '--no-build', '--pull', 'never')
        dc(source, 'up', '-d', '--no-deps', '--wait', 'postgres', 'redis')
        dc(source, 'up', '-d', '--no-deps', '--wait', 'gateway')
        dc(source, 'up', '-d', '--no-deps', 'worker')
        admin = json.loads(dc(source, 'exec', '-T', 'gateway', '/gateway', 'admin', 'init', '--email', 'recovery@example.test'))
        token = admin['secret']
        base = f'http://127.0.0.1:{api_port}'

        def request(path, body=None, expected=200, method=None):
            headers = {'Authorization': 'Bearer ' + token, 'User-Agent': 'Gateway-Bundle-Recovery-Verification'}
            if body is not None:
                headers['Content-Type'] = 'application/json'
            req = urllib.request.Request(base + path, json.dumps(body).encode() if body is not None else None, headers, method=method)
            try:
                response = urllib.request.urlopen(req, timeout=10)
            except urllib.error.HTTPError as error:
                response = error
            data = response.read()
            assert response.status == expected, (path, response.status, expected)
            return data

        def generation():
            models = json.loads(request('/v1/models'))['data']
            model = next(item['id'] for item in models if item['id'].startswith('mock/') and 'image' in item['id'])
            task = json.loads(request('/v1/images/generations', {'model': model, 'prompt': 'Synthetic recovery verification'}, 202))
            deadline = time.monotonic() + 40
            while True:
                state = json.loads(request('/v1/generations/' + task['id']))
                if state['status'] == 'completed':
                    break
                assert state['status'] != 'failed' and time.monotonic() < deadline
                time.sleep(.1)
            content = request('/v1/generations/' + task['id'] + '/content')
            assert b'<svg' in content
            return task['id'], hashlib.sha256(content).hexdigest()

        saved = [generation() for _ in range(3)]
        provider = json.loads(request('/v1/system/providers', {'api_type': 'bytedance', 'name': 'Disabled synthetic credential'}, 201))
        provider = json.loads(request('/v1/system/providers/' + provider['id'],
                                      {'api_key': 'synthetic-recovery-credential-ONLY-167', 'enabled': False,
                                       'version': provider['version'], 'connection': {'manual_catalog': True, 'models': [], 'headers': []}}, method='PUT'))
        assert provider['configured'] and not provider['enabled'] and provider['connection']['manual_catalog']
        provider_before = json.loads(request('/v1/system/providers/' + provider['id']))
        dc(source, 'exec', '-T', 'redis', 'redis-cli', 'SET', 'gw167:synthetic', 'recover-this-value')
        blocks = guide_blocks((source / 'backup-restore.md').read_text())
        assert set(blocks) == {'inventory', 'quiesce', 'snapshot', 'verify', 'restore', 'startup', 'rollback'}

        def block(name, cwd):
            result = run(['sh', '-eu', '-c', blocks[name]], cwd=cwd)
            (output / (name + '.log')).write_bytes(result.stdout + result.stderr)

        block('inventory', source)
        inventory = json.loads((backup / 'inventory.json').read_text())
        expected = {'postgres-data', 'redis-data', 'media-data', 'prometheus-data', 'grafana-data'}
        if https:
            expected.update({'https-data', 'https-config'})
        assert set(inventory['volumes']) == expected
        # Synthetic markers prove recovery of each optional volume without public
        # certificate issuance or starting monitoring/public listeners.
        marker = b'GW-167 synthetic volume recovery marker\x00\xff\n'
        for logical, actual in inventory['volumes'].items():
            if logical in ('postgres-data', 'redis-data'):
                continue
            run(['docker', 'run', '--rm', '-i', '--network', 'none', '--entrypoint', 'sh',
                 '--mount', 'type=volume,src=' + actual + ',dst=/snapshot', inventory['database']['image'],
                 '-c', 'cat > /snapshot/gw167-synthetic.bin && chmod 640 /snapshot/gw167-synthetic.bin'], data=marker)
        block('quiesce', source)
        block('snapshot', source)
        block('verify', source)
        # Exercise compatible same-schema application rollback on synthetic data.
        dc(source, 'up', '-d', '--no-deps', '--wait', 'postgres', 'redis')
        pg_before = dc(source, 'ps', '--quiet', 'postgres').strip()
        block('rollback', source)
        assert dc(source, 'ps', '--quiet', 'postgres').strip() == pg_before
        for task_id, digest in saved:
            assert hashlib.sha256(request('/v1/generations/' + task_id + '/content')).hexdigest() == digest
        block('quiesce', source)
        dc(source, 'stop')
        # Tampering is refused before creation of any recovery directory/container.
        schema = (backup / 'schema.txt').read_bytes()
        (backup / 'schema.txt').write_bytes(schema + b'corrupt')
        refused = run(['sh', '-eu', '-c', blocks['restore']], cwd=output, check=False)
        assert refused.returncode and not Path(env['GW_RESTORE']).exists()
        (backup / 'schema.txt').write_bytes(schema)
        # A checksum-valid custom config must still be unable to alias the
        # original data through binds, driver options or inherited mounts.
        original_bundle = (backup / 'bundle.tar.gz').read_bytes()
        original_manifest = (backup / 'sha256.json').read_bytes()
        negative_cases = {
            'original-bind': 'services:\n  postgres:\n    volumes:\n      - "' + str(source) + ':/original-data"\n',
            'bind-volume-driver': 'volumes:\n  media-data:\n    driver_opts:\n      type: none\n      o: bind\n      device: "' + str(source) + '"\n',
            'additional-fixed-volume': 'services:\n  postgres:\n    volumes:\n      - added-source-volume:/aliased-source\nvolumes:\n  added-source-volume:\n    name: ' + inventory['volumes']['media-data'] + '\n',
            'fixed-network': 'networks:\n  default:\n    name: ' + inventory['networks']['default'] + '\n',
            'inherited-container-volumes': 'services:\n  postgres:\n    volumes_from:\n      - "container:' + dc(source, 'ps', '--all', '--quiet', 'postgres').decode().strip() + '"\n',
        }
        negative_results = {}
        for label, override in negative_cases.items():
            stream = io.BytesIO()
            with tarfile.open(fileobj=io.BytesIO(original_bundle), mode='r:gz') as old, tarfile.open(fileobj=stream, mode='w:gz') as new:
                for member in old:
                    if member.isfile():
                        content = old.extractfile(member).read()
                        if member.name == 'bundle/.env':
                            content = content.replace(values['COMPOSE_FILE'].encode(), (values['COMPOSE_FILE'] + ':compose.negative.yaml').encode())
                            member.size = len(content)
                        new.addfile(member, io.BytesIO(content))
                    else:
                        new.addfile(member)
                member = tarfile.TarInfo('bundle/compose.negative.yaml')
                member.size = len(override.encode())
                member.mode = 0o600
                new.addfile(member, io.BytesIO(override.encode()))
            (backup / 'bundle.tar.gz').write_bytes(stream.getvalue())
            manifest = json.loads(original_manifest)
            manifest['bundle.tar.gz'] = hashlib.sha256(stream.getvalue()).hexdigest()
            (backup / 'sha256.json').write_text(json.dumps(manifest))
            negative_env = dict(env, GW_RESTORE=str(output / ('negative-' + label)), GW_RECOVERY_PROJECT=project + '-' + label)
            projects.append(negative_env['GW_RECOVERY_PROJECT'])
            result = run(['sh', '-eu', '-c', blocks['restore']], cwd=output, command_env=negative_env, check=False)
            assert result.returncode, 'Unsafe configuration was accepted: ' + label
            expected_error = {'original-bind': 'Recovery bind must be inside', 'bind-volume-driver': 'Custom volume storage',
                              'additional-fixed-volume': 'Fixed-name volume', 'fixed-network': 'Fixed-name network',
                              'inherited-container-volumes': 'Review inherited container volumes'}[label]
            assert expected_error in result.stderr.decode(), (label, result.stderr.decode()[-1000:])
            for kind, args in [('containers', ['docker', 'ps', '-aq']), ('volumes', ['docker', 'volume', 'ls', '-q']), ('networks', ['docker', 'network', 'ls', '-q'])]:
                assert not run(args + ['--filter', 'label=com.docker.compose.project=' + negative_env['GW_RECOVERY_PROJECT']]).stdout.strip(), (label, kind)
            negative_results[label] = 'refused before container/volume creation'
            (output / ('negative-' + label + '.log')).write_bytes(result.stdout + result.stderr)
        (backup / 'bundle.tar.gz').write_bytes(original_bundle)
        (backup / 'sha256.json').write_bytes(original_manifest)
        (output / 'negative-guards.json').write_text(json.dumps(negative_results, indent=2) + '\n')
        block('restore', output)
        recovered = Path(env['GW_RESTORE']) / 'bundle'
        assert (recovered / '.env').read_bytes() == (source / '.env').read_bytes()
        recovery_env = dict(env, COMPOSE_PROJECT_NAME=recovery,
                            COMPOSE_FILE=values['COMPOSE_FILE'] + ':compose.recovery.json')

        def rdc(*args):
            return run(['docker', 'compose', '--project-name', recovery, *args], cwd=recovered, command_env=recovery_env).stdout

        config = json.loads(rdc('config', '--format', 'json'))
        for logical, original in inventory['volumes'].items():
            actual = config['volumes'][logical]['name']
            assert actual != original
            if logical not in ('postgres-data', 'redis-data'):
                content = run(['docker', 'run', '--rm', '--network', 'none', '--entrypoint', 'cat',
                               '--mount', 'type=volume,src=' + actual + ',dst=/snapshot,readonly',
                               inventory['database']['image'], '/snapshot/gw167-synthetic.bin']).stdout
                assert content == marker
        assert rdc('exec', '-T', 'redis', 'redis-cli', 'GET', 'gw167:synthetic').strip() == b'recover-this-value'
        running = rdc('ps', '--status', 'running', '--services').decode().split()
        assert set(running) == {'postgres', 'redis'}, running
        env.update(GW_RECOVERY_API_PORT=str(api_port), GW_RECOVERY_UI_PORT=str(port()))
        block('startup', output)
        recovery_env['COMPOSE_FILE'] += ':compose.recovery-check.yaml'
        # Original management key and encryption secret must both work unchanged.
        assert json.loads(request('/v1/me'))
        provider_after = json.loads(request('/v1/system/providers/' + provider['id']))
        for field in ('configured', 'enabled', 'version', 'credential_hint', 'hints', 'connection'):
            assert provider_after.get(field) == provider_before.get(field), field
        for task_id, digest in saved:
            assert hashlib.sha256(request('/v1/generations/' + task_id + '/content')).hexdigest() == digest
        rdc('up', '-d', '--no-deps', 'worker')
        new_id, new_hash = generation()
        # Existing-project refusal must not change its recovered data.
        refused = run(['sh', '-eu', '-c', blocks['restore']], cwd=output, check=False)
        assert refused.returncode
        assert hashlib.sha256(request('/v1/generations/' + new_id + '/content')).hexdigest() == new_hash
        summary = {'result': 'passed', 'source': run(['git', 'rev-parse', 'HEAD'], cwd=ROOT).stdout.decode().strip(),
                   'guide_source_sha256': hashlib.sha256((ROOT / 'deploy/ghcr/backup-restore.md').read_bytes()).hexdigest(),
                   'guide_bundled_sha256': hashlib.sha256((source / 'backup-restore.md').read_bytes()).hexdigest(),
                   'versions': versions, 'images': images, 'projects': projects, 'mode': 'https' if https else 'local',
                   'restored_tasks_and_downloads': len(saved), 'new_mock_generation': True,
                   'original_admin_key': True, 'encrypted_provider_settings': True, 'compatible_application_rollback': True,
                   'unchanged_original_env': True, 'migration_history_equal': True,
                   'redis_value_recovered': True, 'separate_volumes': sorted(expected),
                   'optional_volume_markers_recovered': len(expected) - 2, 'tamper_and_existing_target_refusals': True, 'unsafe_storage_and_network_refusals': negative_results,
                   'limits': ['Synthetic data only', 'No S3 objects or external bind mounts',
                              'HTTPS/monitoring volumes verified with markers, not live TLS/monitoring services',
                              'Same-version PostgreSQL 17 restore; no major upgrade or live cutover']}
        (output / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
        print(json.dumps(summary, indent=2))
    finally:
        # Stop/remove only uniquely generated projects, then explicitly remove
        # only their labelled volumes. Never down -v or prune global resources.
        for name in projects:
            ids = run(['docker', 'ps', '-aq', '--filter', 'label=com.docker.compose.project=' + name], check=False).stdout.decode().split()
            if ids:
                run(['docker', 'rm', '-f', *ids], check=False)
            volumes = run(['docker', 'volume', 'ls', '-q', '--filter', 'label=com.docker.compose.project=' + name], check=False).stdout.decode().split()
            if volumes:
                run(['docker', 'volume', 'rm', *volumes], check=False)
            networks = run(['docker', 'network', 'ls', '-q', '--filter', 'label=com.docker.compose.project=' + name], check=False).stdout.decode().split()
            if networks:
                run(['docker', 'network', 'rm', *networks], check=False)
        leftovers = {}
        for name in projects:
            for kind, args in [('containers', ['docker', 'ps', '-aq']), ('volumes', ['docker', 'volume', 'ls', '-q']), ('networks', ['docker', 'network', 'ls', '-q'])]:
                found = run(args + ['--filter', 'label=com.docker.compose.project=' + name], check=False).stdout.decode().split()
                if found:
                    leftovers[name + ':' + kind] = found
        (output / 'cleanup.json').write_text(json.dumps({'leftovers': leftovers}, indent=2) + '\n')
        logs.close()
        assert not leftovers, 'Disposable resources remain; inspect private cleanup evidence'


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True, type=Path, help='New private evidence directory')
    for image in ('gateway', 'ui', 'postgres', 'redis'):
        parser.add_argument('--' + image + '-image', required=True, help='Explicit local test image reference')
    parser.add_argument('--https', action='store_true', help='Include HTTPS config/volume recovery (no public TLS listener)')
    args = parser.parse_args()
    verify(args.output, {name: getattr(args, name + '_image') for name in ('gateway', 'ui', 'postgres', 'redis')}, https=args.https)
