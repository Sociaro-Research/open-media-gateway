#!/usr/bin/env python3
"""One-time root-side WireGuard setup; accepts and emits public keys only."""
import argparse
import base64
import datetime
import os
from pathlib import Path
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / 'tools/release'))
from release_config import load_config, key_path, network_config


def configuration(private, public, config=None):
    config = network_config(config or load_config())
    return ('[Interface]\nAddress = ' + config['wireguard_server_address'] + '\nListenPort = ' + str(config['wireguard_listen_port']) + '\nMTU = 1280\n'
            'PrivateKey = ' + private + '\n'
            'PostUp = iptables -w -I INPUT 1 -i %i -s ' + config['wireguard_client_address'] + ' -p tcp --dport 22 -j ACCEPT\n'
            'PostUp = iptables -w -I INPUT 2 -i %i -j DROP\n'
            'PostUp = iptables -w -I DOCKER-USER 1 -i %i -j DROP\n'
            'PostDown = iptables -w -D DOCKER-USER -i %i -j DROP || true\n'
            'PostDown = iptables -w -D INPUT -i %i -j DROP || true\n'
            'PostDown = iptables -w -D INPUT -i %i -s ' + config['wireguard_client_address'] + ' -p tcp --dport 22 -j ACCEPT || true\n\n'
            '[Peer]\nPublicKey = ' + public + '\nAllowedIPs = ' + config['wireguard_client_address'] + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--client-public-key', required=True)
    args = parser.parse_args()
    public = args.client_public_key.strip()
    if os.getuid() != 0 or len(base64.b64decode(public, validate=True)) != 32:
        raise RuntimeError('Root and a valid client public key are required.')
    settings = network_config(load_config())
    os.umask(0o077)
    config = Path('/etc/wireguard/wg-deploy.conf')
    dropin = Path('/etc/systemd/system/wg-quick@wg-deploy.service.d/docker.conf')
    if config.exists() or dropin.exists():
        raise RuntimeError('Deployment tunnel already configured; refusing to replace its keys.')
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    backup = Path('/opt/media-gateway/backups') / (stamp + '-actions-network')
    backup.mkdir(mode=0o700)
    (backup / 'iptables-before.rules').write_bytes(subprocess.check_output(['iptables-save']))
    (backup / 'previous-config-absent').write_text('No wg-deploy configuration existed.\n')
    private = subprocess.check_output(['wg', 'genkey'], text=True).strip()
    server_public = subprocess.check_output(['wg', 'pubkey'], input=private, text=True).strip()
    config.parent.mkdir(mode=0o700, exist_ok=True)
    config.write_text(configuration(private, public, settings))
    config.chmod(0o600)
    del private
    dropin.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
    # Reapply interface-specific Docker forwarding restrictions after Docker restarts.
    dropin.write_text('[Unit]\nRequires=docker.service\nAfter=docker.service\nPartOf=docker.service\n')
    subprocess.run(['systemctl', 'daemon-reload'], check=True, stdout=subprocess.DEVNULL)
    subprocess.run(['systemctl', 'enable', '--now', 'wg-quick@wg-deploy'], check=True, stdout=subprocess.DEVNULL)
    print(server_public)
    print('Network configuration backup: ' + str(backup), file=__import__('sys').stderr)


if __name__ == '__main__':
    main()
