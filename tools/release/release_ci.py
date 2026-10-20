#!/usr/bin/env python3
"""Local security, container and installation checks; no publish or deployment."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import uuid


def preflight():
    # Refuse a remote engine: all test containers must stay on this Mac.
    try:
        context = os.environ.get('DOCKER_CONTEXT')
        endpoint = (os.environ.get('DOCKER_HOST') if not context else None) or subprocess.check_output(
            ['docker', 'context', 'inspect', *([context] if context else []), '--format', '{{.Endpoints.docker.Host}}'], text=True).strip()
    except (OSError, subprocess.CalledProcessError):
        raise RuntimeError('Full local CI requires a running local Docker Desktop. Native checks remain available with --check-only --native-only.') from None
    if not endpoint.startswith('unix://'):
        raise RuntimeError('Local CI requires a local Unix-socket Docker engine; remote Docker endpoints are refused.')
    try:
        subprocess.run(['docker', 'info'], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except (OSError, subprocess.CalledProcessError):
        raise RuntimeError('Start Docker Desktop before full CI.') from None


def verify(project, execute, env, node):
    preflight()
    with tempfile.TemporaryDirectory(prefix='gateway-local-ci-') as temporary:
        directory = Path(temporary)
        tools = directory / 'security'
        execute([sys.executable, 'scripts/security/install-tools.py', '--bin-dir', str(tools)], project, env)
        execute([str(tools/'gitleaks'), 'git', '--config', '.gitleaks.toml', '--redact=100', '--log-opts=--all', '.'], project, env)
        execute([str(tools/'trivy'), 'fs', '--scanners', 'vuln', '--severity', 'HIGH,CRITICAL', '--exit-code', '1', '--skip-dirs', '.git,.local,dist', '.'], project, env)
        env = {key: value for key, value in env.items() if not key.startswith('COMPOSE_')}
        execute(['docker', 'compose', '--profile', 'ui', '--profile', 'monitoring', 'config', '--quiet'], project, env)
        for command in [('check', 'config', '/etc/prometheus/prometheus.yml'), ('test', 'rules', 'alerts.test.yml')]:
            execute(['docker','run','--rm','--network','none','--entrypoint','/bin/promtool',
                     '-v',str(project/'monitoring')+':/etc/prometheus:ro','-w','/etc/prometheus',
                     'prom/prometheus:v3.15.0',*command],project,env)
        prefix = 'gateway-local-ci-' + uuid.uuid4().hex[:12]
        images = {service: prefix + '-' + service for service in ('gateway','ui','postgres','redis')}
        try:
            for service, context in [('gateway','.'),('ui','web'),('postgres','deploy/postgres'),('redis','deploy/redis')]:
                execute(['docker','build','--tag',images[service],context],project,env)
                execute([str(tools/'trivy'),'image','--scanners','vuln','--severity','HIGH,CRITICAL','--exit-code','1',images[service]],project,env)
            uid = subprocess.check_output(['docker','run','--rm','--network','none','--entrypoint','gosu',images['postgres'],'postgres','id','-u'], text=True).strip()
            if uid != '70':
                raise RuntimeError('PostgreSQL runtime user must have UID 70.')
            execute(['docker','run','--rm','--cap-drop','ALL','--security-opt','no-new-privileges',images['ui'],'caddy','validate','--config','/etc/caddy/Caddyfile','--adapter','caddyfile'],project,env)
            container = prefix + '-extract'
            try:
                execute(['docker','create','--name',container,images['ui']],project,env)
                execute(['docker','cp',container+':/usr/bin/caddy',str(directory/'caddy')],project,env)
                execute(['go','run','scripts/security/check-go-binary.go',str(directory/'caddy'),'golang.org/x/crypto/openpgp'],project,env)
            finally:
                subprocess.run(['docker','rm',container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            execute([sys.executable,'scripts/verify-release.py',*sum(([ '--'+s, i] for s,i in images.items()),[])],project,env)
            compose=['docker','compose','-p',prefix,'--profile','monitoring','-f','compose.yaml','-f','monitoring/grafana/compose.test.yaml']
            try:
                execute(compose+['up','-d','--wait','grafana'],project,env)
                monitoring=dict(env,GRAFANA_TEST_UPSTREAM='127.0.0.1:13000',GRAFANA_TEST_UI_PORT='18088')
                execute([node,'scripts/ui/monitoring.cjs'],project,monitoring)
            finally:
                # Only our unique disposable test project; never application volumes.
                subprocess.run(compose+['down','--volumes'],cwd=project,env=env,check=True)
        finally:
            subprocess.run(['docker','image','rm',*images.values()],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    print('PASS: local security, four container images, source-free installation and real Grafana checks.',flush=True)
