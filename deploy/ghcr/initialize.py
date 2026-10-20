#!/usr/bin/env python3
"""Create a fresh installation .env without overwriting existing credentials."""
import argparse
import ipaddress
import os
from pathlib import Path
import re
import secrets
from urllib.parse import urlsplit

SECRET_NAMES = (
    "POSTGRES_PASSWORD", "REDIS_PASSWORD", "CLIENT_API_KEY", "SESSION_SIGNING_SECRET",
    "WEBHOOK_SIGNING_SECRET", "MEDIA_URL_SIGNING_SECRET", "PROVIDER_CREDENTIALS_ENCRYPTION_KEY",
)


def server_origin(value):
    """Only a bare HTTPS DNS origin can enter Compose and Caddy configuration."""
    try:
        url = urlsplit(value)
        host = (url.hostname or "").encode("idna").decode("ascii").lower()
        valid = (url.scheme == "https" and url.netloc.lower() == host
                 and url.path in ("", "/") and not url.query and not url.fragment
                 and "." in host and len(host) <= 253
                 and all(re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label)
                         for label in host.split("."))
                 and not host.endswith((".localhost", ".local")))
        try:
            ipaddress.ip_address(host)
        except ValueError:
            pass
        else:
            valid = False
        if valid:
            return "https://" + host, host
    except (ValueError, UnicodeError):
        pass
    raise ValueError("Use an HTTPS domain without a port or path, e.g. https://gateway.example.com")


def initialize(directory, public_url=None, behind_proxy=False, project_name="media-gateway",
               api_port=8080, ui_port=8088):
    if public_url == "":
        raise ValueError("--public-url needs an HTTPS domain")
    if not re.fullmatch(r"[a-z0-9][a-z0-9_-]{0,62}", project_name):
        raise ValueError("Project name must be 1-63 lowercase letters, digits, hyphens or underscores")
    if any(not isinstance(port, int) or not 1 <= port <= 65535 for port in (api_port, ui_port)) or api_port == ui_port:
        raise ValueError("API and UI ports must be different numbers from 1 to 65535")
    if behind_proxy and not public_url:
        raise ValueError("--behind-proxy requires --public-url")
    origin, host = server_origin(public_url) if public_url else (f"http://localhost:{ui_port}", "")
    overrides = {"COMPOSE_PROJECT_NAME": project_name, "PORT": str(api_port), "UI_PORT": str(ui_port),
                 "AUTH_PUBLIC_URL": origin, "UI_PUBLIC_URL": origin, "GATEWAY_HOST": host}
    if public_url and not behind_proxy:
        overrides["COMPOSE_FILE"] = "compose.yaml:compose.release.yaml:compose.https.yaml"
    template = (directory / ".env.example").read_text()
    if "@VERSION@" in template:
        raise ValueError("Use the generated installation bundle, not the source template")
    lines = []
    seen = set()
    for line in template.splitlines():
        name, separator, value = line.partition("=")
        if separator and name in SECRET_NAMES:
            if value or name in seen:
                raise ValueError("Installation template must not contain credentials")
            seen.add(name)
            line = name + "=" + secrets.token_hex(32)
        if separator and name in overrides:
            line = name + "=" + overrides[name]
        lines.append(line)
    if seen != set(SECRET_NAMES):
        raise ValueError("Installation template is missing required secrets")
    # Exclusive creation protects credentials even if two setup commands overlap.
    destination = directory / ".env"
    fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as output:
        output.write("\n".join(lines) + "\n")
    return destination


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Prepare a new installation; never overwrite an existing .env.")
    parser.add_argument("--public-url", help="HTTPS domain; enables the included certificate-managing proxy")
    parser.add_argument("--behind-proxy", action="store_true", help="Use an existing reverse proxy instead")
    parser.add_argument("--project-name", default="media-gateway", help="Unique name for this installation's volumes/networks")
    parser.add_argument("--api-port", type=int, default=8080, help="Loopback API port (local/existing-proxy mode)")
    parser.add_argument("--ui-port", type=int, default=8088, help="Loopback console port (local/existing-proxy mode)")
    args = parser.parse_args()
    try:
        initialize(Path(__file__).resolve().parent, **vars(args))
    except FileExistsError:
        raise SystemExit(".env already exists; kept unchanged. Edit it in place for upgrades.")
    except FileNotFoundError:
        raise SystemExit("Missing installation template. Download and extract a release bundle before running initialize.py.")
    except ValueError as error:
        raise SystemExit(str(error))
    print("Created private .env with independent random secrets.")
    print("Run: docker compose pull && docker compose up -d --wait")
    print("Then: docker compose exec -T gateway /gateway admin setup-code")
    print("Open " + (server_origin(args.public_url)[0] if args.public_url else f"http://localhost:{args.ui_port}")
          + " and complete Create owner account.")
