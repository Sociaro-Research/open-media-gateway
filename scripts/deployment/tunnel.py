#!/usr/bin/env python3
"""Run one deployment command through a narrowly routed WireGuard interface."""
import base64
import os
from pathlib import Path
import subprocess
import sys
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / 'tools/release'))
from release_config import load_config, key_path, network_config


def validate_key(value):
    value = value.strip()
    try:
        if len(base64.b64decode(value, validate=True)) != 32:
            raise ValueError()
    except (ValueError, TypeError):
        raise RuntimeError('A valid deployment WireGuard key is required.') from None
    return value


def configuration(private_key, public_key, config=None):
    config = network_config(config or load_config())
    private_key, public_key = validate_key(private_key), validate_key(public_key)
    return ('[Interface]\nPrivateKey = ' + private_key + '\nAddress = ' + config['wireguard_client_address'] + '\nMTU = 1280\n\n'
            '[Peer]\nPublicKey = ' + public_key + '\nEndpoint = ' + config['wireguard_endpoint'] + '\n'
            'AllowedIPs = ' + config['wireguard_server_address'] + '\nPersistentKeepalive = 25\n')


def run(command):
    os.umask(0o077)
    if os.environ.get('GITHUB_REF') != 'refs/heads/main' or os.environ.get('GITHUB_EVENT_NAME') != 'workflow_dispatch':
        raise RuntimeError('The deployment tunnel requires a manual run on main.')
    secret = os.environ.pop('DEPLOY_WG_PRIVATE_KEY', '')
    settings = network_config(load_config())
    public = settings['wireguard_server_public_key_file'].read_text()
    if not secret:
        secret = key_path(settings, 'wireguard_client_private_key_file').read_text()
    config = configuration(secret, public, settings)
    del secret
    # Neither deployment credential is passed to privileged network utilities.
    network_env = {k: v for k, v in os.environ.items() if not k.startswith('DEPLOY_')}
    with tempfile.TemporaryDirectory(prefix='gateway-tunnel-') as temporary:
        path = Path(temporary) / 'wg-deploy.conf'
        path.write_text(config)
        path.chmod(0o600)
        del config
        try:
            subprocess.run(['sudo', '-n', 'wg-quick', 'up', str(path)], check=True, env=network_env)
            return subprocess.run(command, check=False).returncode
        finally:
            subprocess.run(['sudo', '-n', 'wg-quick', 'down', str(path)], check=False, env=network_env,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == '__main__':
    try:
        if len(sys.argv) < 3 or sys.argv[1] != '--':
            raise RuntimeError('Specify a deployment command after --.')
        sys.exit(run(sys.argv[2:]))
    except Exception as error:
        message = str(error) if isinstance(error, RuntimeError) else 'Deployment tunnel failed; inspect the preceding network diagnostic.'
        print('TUNNEL STOPPED: ' + message, file=sys.stderr)
        sys.exit(1)
