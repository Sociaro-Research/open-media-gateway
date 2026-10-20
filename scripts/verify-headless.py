#!/usr/bin/env python3
"""Build without web/, bootstrap a fresh test database, generate and download mock media."""
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

root = Path(__file__).resolve().parents[1]
dsn = os.environ['GATEWAY_TEST_DATABASE_URL']
redis = os.environ['GATEWAY_TEST_REDIS_URL']
for value in (dsn, redis):
    if urllib.parse.urlsplit(value).hostname not in ('localhost', '127.0.0.1', '::1'):
        raise SystemExit('Headless verification requires explicitly isolated local test services')
name = 'gateway_headless_' + uuid.uuid4().hex
parts = urllib.parse.urlsplit(dsn)
fresh = urllib.parse.urlunsplit(parts._replace(path='/' + name))
redis = urllib.parse.urlunsplit(urllib.parse.urlsplit(redis)._replace(path='/15'))
subprocess.run(['psql', dsn, '-v', 'ON_ERROR_STOP=1', '-c', 'CREATE DATABASE ' + name], check=True, stdout=subprocess.DEVNULL)
processes = []
def available_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]

try:
    with tempfile.TemporaryDirectory(prefix='gateway-headless-') as tmp:
        tmp = Path(tmp)
        for folder in ('cmd', 'internal', 'plugins', 'docs'):
            shutil.copytree(root / folder, tmp / folder)
        for file in ('go.mod', 'go.sum'):
            shutil.copyfile(root / file, tmp / file)
        assert not (tmp / 'web').exists()
        for target in ('gateway', 'worker'):
            subprocess.run(['go', 'build', '-o', str(tmp / target), './cmd/' + target], cwd=tmp, check=True)
        ports = set()
        while len(ports) < 4:
            ports.add(available_port())
        port, gateway_metrics_port, worker_metrics_port, prometheus_port = sorted(ports)
        env = {k: os.environ[k] for k in ('PATH', 'HOME', 'GOCACHE', 'GOMODCACHE', 'GOPATH', 'TMPDIR') if k in os.environ}
        env.update(DATABASE_URL=fresh, REDIS_URL=redis, PORT=str(port),
                   CLIENT_API_KEY='isolated-headless-service-key',
                   WEBHOOK_SIGNING_SECRET='isolated-headless-webhook-secret-32',
                   MEDIA_URL_SIGNING_SECRET='isolated-headless-media-secret-32',
                   PROVIDER_CREDENTIALS_ENCRYPTION_KEY='isolated-headless-encryption-secret-32',
                   STORAGE_LOCAL_PATH=str(tmp / 'media'), MEDIA_PROVIDER='mock', MOCK_DELAY='10ms',
                   MOCK_RESULT_BASE_URL='embedded', PROVIDER_POLL_INTERVAL='20ms', DISPATCH_INTERVAL='20ms')
        account = json.loads(subprocess.check_output([str(tmp / 'gateway'), 'admin', 'init', '--email', 'headless@example.test'], env=env, cwd=tmp))
        refused = subprocess.run([str(tmp / 'gateway'), 'admin', 'init', '--email', 'other@example.test'], env=env, cwd=tmp, capture_output=True)
        assert refused.returncode != 0, 'Second bootstrap succeeded'
        key = account['secret']
        with (tmp / 'server.log').open('w+') as log:
            for target in ('gateway', 'worker'):
                process_env = env | {'METRICS_ADDR': '127.0.0.1:' + str(gateway_metrics_port if target == 'gateway' else worker_metrics_port)}
                processes.append(subprocess.Popen([str(tmp / target)], env=process_env, cwd=tmp, stdout=log, stderr=log))
            base = 'http://127.0.0.1:' + str(port)
            def request(path, body=None, expected=200, token=key):
                headers = {'User-Agent': 'Gateway-Headless-Verification', 'Authorization': 'Bearer ' + token}
                if body is not None:
                    headers['Content-Type'] = 'application/json'
                req = urllib.request.Request(base + path, None if body is None else json.dumps(body).encode(), headers)
                try:
                    response = urllib.request.urlopen(req, timeout=5)
                except urllib.error.HTTPError as error:
                    response = error
                data = response.read()
                assert response.status == expected, (path, response.status, data[:200])
                return data
            deadline = time.monotonic() + 20
            while True:
                try:
                    request('/healthz')
                    break
                except (OSError, AssertionError):
                    if time.monotonic() > deadline:
                        log.seek(0)
                        raise RuntimeError('Headless server did not start: ' + log.read()[-2000:])
                    time.sleep(.1)
            for path in ('/', '/docs', '/ui/app.js', '/privacy', '/metrics'):
                request(path, expected=404)
            for path in ('/v1/system/settings', '/v1/system/email', '/v1/system/login-providers', '/v1/api-keys', '/v1/organizations'):
                request(path)
            request('/v1/system/settings', expected=403, token=env['CLIENT_API_KEY'])
            models = json.loads(request('/v1/models'))['data']
            model = next(m['id'] for m in models if m['id'].startswith('mock/') and 'image' in m['id'])
            job = json.loads(request('/v1/images/generations', {'model': model, 'prompt': 'Headless verification'}, 202))
            deadline = time.monotonic() + 20
            while True:
                task = json.loads(request('/v1/generations/' + job['id']))
                if task['status'] == 'completed':
                    break
                assert task['status'] != 'failed', task
                if time.monotonic() > deadline:
                    raise RuntimeError('Mock generation timed out')
                time.sleep(.1)
            content = request('/v1/generations/' + job['id'] + '/content')
            assert content.startswith(b'<svg') and b'Mock Image' in content, 'Downloaded mock SVG is invalid'
            recovery = json.loads(subprocess.check_output([str(tmp / 'gateway'), 'admin', 'issue-key', '--user-id', account['key']['user_id']], env=env, cwd=tmp))
            request('/v1/system/settings', token=recovery['secret'])
            def metrics(metrics_port):
                req = urllib.request.Request('http://127.0.0.1:' + str(metrics_port) + '/metrics', headers={'User-Agent': 'Gateway-Monitoring-Verification'})
                with urllib.request.urlopen(req, timeout=5) as response:
                    return response.read().decode()
            gateway_metrics = metrics(gateway_metrics_port)
            worker_metrics = metrics(worker_metrics_port)
            for value in ('media_gateway_dependency_up{dependency="postgres",service="gateway"} 1',
                          'media_gateway_dependency_up{dependency="redis_media",service="gateway"} 1',
                          'media_gateway_http_requests_total{method="POST",route="POST /v1/images/generations",service="gateway",status="202"} 1',
                          'media_gateway_active_tasks{service="gateway",status="queued"} 0',
                          'media_gateway_active_tasks{service="gateway",status="running"} 0'):
                assert value in gateway_metrics, 'Missing gateway metric: ' + value
            assert 'media_gateway_worker_claims_total{outcome="success",service="worker"}' in worker_metrics
            assert 'media_gateway_dependency_up{dependency="redis_webhook",service="worker"} 1' in worker_metrics
            for value in (key, recovery['secret'], job['id'], 'Headless verification', 'headless@example.test'):
                assert value not in gateway_metrics + worker_metrics, 'Private data exposed in metrics'
            summary = {'dedicated_listeners': True, 'public_metrics_status': 404, 'mock_generation_and_download': True,
                       'postgres_and_redis_up': True, 'active_tasks_after_completion': 0, 'private_data_absent': True,
                       'prometheus_scrape': 'unexecuted (GATEWAY_TEST_PROMETHEUS_BIN not supplied)'}
            prometheus_binary = os.environ.get('GATEWAY_TEST_PROMETHEUS_BIN')
            if prometheus_binary:
                prometheus_binary = str(Path(prometheus_binary).resolve(strict=True))
                prometheus_config = {
                    'global': {'scrape_interval': '1s', 'scrape_timeout': '500ms', 'evaluation_interval': '1s'},
                    'rule_files': [str(root / 'monitoring/alerts.yml')],
                    'scrape_configs': [{'job_name': target, 'static_configs': [{'targets': ['127.0.0.1:' + str(metrics_port)]}]} for target, metrics_port in
                                       [('gateway', gateway_metrics_port), ('worker', worker_metrics_port)]]}
                (tmp / 'prometheus.json').write_text(json.dumps(prometheus_config))
                processes.append(subprocess.Popen([prometheus_binary, '--config.file=' + str(tmp / 'prometheus.json'),
                                                    '--storage.tsdb.path=' + str(tmp / 'prometheus-data'),
                                                    '--web.listen-address=127.0.0.1:' + str(prometheus_port)], cwd=tmp, stdout=log, stderr=log))
                def query(expression):
                    url = 'http://127.0.0.1:' + str(prometheus_port) + '/api/v1/query?' + urllib.parse.urlencode({'query': expression})
                    req = urllib.request.Request(url, headers={'User-Agent': 'Gateway-Prometheus-Verification'})
                    with urllib.request.urlopen(req, timeout=5) as response:
                        result = json.load(response)
                    assert result['status'] == 'success', result
                    return result['data']['result']
                deadline = time.monotonic() + 20
                while True:
                    try:
                        targets = query('up{job=~"gateway|worker"}')
                        assert len(targets) == 2 and all(float(target['value'][1]) == 1 for target in targets)
                        admissions = query('media_gateway_http_requests_total{route="POST /v1/images/generations",status="202"}')
                        assert len(admissions) == 1 and float(admissions[0]['value'][1]) == 1
                        heartbeats = query('media_gateway_worker_loop_last_success_timestamp_seconds')
                        assert len(heartbeats) == 2 and all(float(value['value'][1]) > 0 for value in heartbeats)
                        break
                    except (OSError, AssertionError):
                        if time.monotonic() > deadline:
                            log.seek(0)
                            raise RuntimeError('Prometheus did not collect expected metrics: ' + log.read()[-3000:])
                        time.sleep(.1)
                summary['prometheus_scrape'] = 'passed: gateway and worker up=1, admission counter and both worker heartbeats queried'
                print('PASS: real Prometheus scraped gateway/worker and returned generation and worker progress metrics')
            output = os.environ.get('GATEWAY_PERF_OUTPUT')
            if output:
                Path(output).mkdir(parents=True, exist_ok=True)
                (Path(output) / 'monitoring-summary.json').write_text(json.dumps(summary, indent=2) + '\n')
            print('PASS: private dedicated metrics, PostgreSQL/Redis snapshots, bounded route labels and drained active tasks')
            print('PASS: build with no web directory, atomic bootstrap, management APIs without browser auth, recovery key, mock generation and SVG download')
finally:
    for process in processes:
        process.terminate()
    for process in processes:
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
    subprocess.run(['psql', dsn, '-v', 'ON_ERROR_STOP=1', '-c', 'DROP DATABASE ' + name + ' WITH (FORCE)'], check=True, stdout=subprocess.DEVNULL)
