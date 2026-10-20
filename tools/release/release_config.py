"""Load operator-owned deployment bindings; never discover or learn trust keys."""
import base64
import ipaddress
import json
import os
from pathlib import Path
import re
import stat
from urllib.parse import urlsplit


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def load_config(root=None, tool_dir=None):
    root = Path(root or Path(__file__).resolve().parents[2])
    tool_dir = Path(tool_dir or Path(__file__).resolve().parent)
    default = tool_dir.parent / 'deployment.json' if (tool_dir / 'project-path.txt').exists() else root / '.local/deployment.json'
    path = Path(os.environ.get('DEPLOY_CONFIG') or default).expanduser().resolve()
    try:
        require(stat.S_IMODE(path.stat().st_mode) & 0o077 == 0,
                'Deployment profile must be private (mode 0600).')
        data = json.loads(path.read_text())
    except (OSError, ValueError):
        raise RuntimeError('A valid private deployment.json is required; see docs/deployment-configuration.md.') from None
    require(isinstance(data, dict) and data.get('version') == 1 and not data.get('example'),
            'Deployment profile must be an operator-reviewed version 1 profile, not the example.')
    for name, value in data.items():
        if isinstance(value, str):
            require(not any(ord(c) < 32 for c in value), 'Deployment profile contains a control character.')
    for name in ('admin_ssh_host', 'tunnel_ssh_host'):
        require(isinstance(data.get(name), str) and re.fullmatch(r'[A-Za-z0-9_.-]+@[A-Za-z0-9][A-Za-z0-9.:-]*', data[name]),
                'Deployment profile requires explicit SSH user and host.')
    alias = data.get('ssh_host_key_alias')
    require(isinstance(alias, str) and re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.:-]*', alias),
            'Deployment profile requires a fixed SSH host-key alias.')
    for name in ('known_hosts_file', 'admin_ssh_key_file', 'restricted_ssh_key_file',
                 'github_ssh_key_file', 'wireguard_server_public_key_file', 'wireguard_client_private_key_file'):
        value = data.get(name)
        if value is None:
            continue
        require(isinstance(value, str) and bool(value), 'Deployment profile has an invalid file path.')
        file = Path(value).expanduser()
        data[name] = (file if file.is_absolute() else path.parent / file).resolve()
    hosts = data.get('known_hosts_file')
    require(isinstance(hosts, Path) and hosts.is_file(), 'Verified deployment known_hosts file is missing.')
    # Read public pins only. Private keys are only stat-checked when their role is used.
    entries = [line.split() for line in hosts.read_text().splitlines() if line and not line.startswith('#')]
    require(any(len(e) == 3 and alias in e[0].split(',') and e[1] == 'ssh-ed25519'
                and valid_public_key(e[2]) for e in entries), 'Verified Ed25519 pin for the configured alias is missing.')
    origin = data.get('public_origin')
    try:
        parsed = urlsplit(origin)
        valid = isinstance(origin, str) and parsed.scheme == 'https' and parsed.hostname and not parsed.username and not parsed.password and not parsed.path and not parsed.query and not parsed.fragment and (parsed.port is None or 1 <= parsed.port <= 65535)
    except (ValueError, TypeError):
        valid = False
    require(valid, 'Deployment profile requires an HTTPS public origin without credentials or path.')
    repository = data.get('repository_url')
    require(isinstance(repository, str) and repository and not repository.startswith('-'), 'Deployment profile requires the expected repository URL.')
    return data


def valid_public_key(value):
    try:
        # An SSH Ed25519 wire key includes its type and 32-byte public key.
        raw = base64.b64decode(value, validate=True)
        return len(raw) == 51 and raw[:19] == b'\x00\x00\x00\x0bssh-ed25519\x00\x00\x00 '
    except (ValueError, TypeError):
        return False


def key_path(config, name):
    path = config.get(name)
    require(isinstance(path, Path) and path.is_file(), 'Required private key path is not configured or missing: ' + name)
    return path


def network_config(config):
    try:
        client = ipaddress.IPv4Interface(config['wireguard_client_address'])
        server = ipaddress.IPv4Interface(config['wireguard_server_address'])
        route = ipaddress.IPv4Network(config['wireguard_allowed_route'], strict=True)
        endpoint = config['wireguard_endpoint']
        port = config['wireguard_listen_port']
        require(client.network.prefixlen == server.network.prefixlen == route.prefixlen == 32
                and route.network_address == server.ip and client.ip != server.ip,
                'WireGuard must route only the explicit server host and authorize only the client host.')
        require(type(port) is int and 1 <= port <= 65535 and isinstance(endpoint, str)
                and re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.-]*:[0-9]{1,5}', endpoint)
                and 1 <= int(endpoint.rsplit(':', 1)[1]) <= 65535,
                'WireGuard endpoint and listen port must be explicit and valid.')
    except (KeyError, ValueError, TypeError):
        raise RuntimeError('A complete narrow-route WireGuard profile is required.') from None
    return config
