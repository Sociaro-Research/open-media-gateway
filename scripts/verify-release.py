#!/usr/bin/env python3
"""Test a fresh source-free Compose installation using four local test images."""
import argparse
import http.cookiejar
import importlib.util
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def verify(version, images, https=False):
    package = load("package_release", Path(__file__).with_name("package-release.py"))
    project = "gateway-release-test-" + uuid.uuid4().hex
    with tempfile.TemporaryDirectory(prefix="gateway-release-test-") as tmp:
        tmp = Path(tmp)
        archive = package.package(version, tmp)
        with tarfile.open(archive) as tar:
            tar.extractall(tmp)  # Only our allowlisted, freshly generated archive.
        directory = tmp / ("media-gateway-" + version)
        assert not (directory / "go.mod").exists() and not (directory / "web").exists()
        initializer = load("release_initialize", directory / "initialize.py")
        initializer.initialize(directory, public_url="https://gateway.example.test" if https else None,
                               project_name=project)
        api_port, ui_port = port(), port()
        while ui_port == api_port:
            ui_port = port()
        overrides = {name.upper() + "_IMAGE": ref for name, ref in images.items()}
        overrides.update(PORT=str(api_port), UI_PORT=str(ui_port), COMPOSE_PROJECT_NAME=project,
                         AUTH_PUBLIC_URL="http://localhost:" + str(ui_port), UI_PUBLIC_URL="http://localhost:" + str(ui_port),
                         MOCK_DELAY="10ms", PROVIDER_POLL_INTERVAL="20ms", DISPATCH_INTERVAL="20ms")
        tls_port = port()
        while tls_port in (api_port, ui_port):
            tls_port = port()
        if https:
            # Test only this temporary bundle, with a private CA and loopback
            # ports. Public ACME/DNS are intentionally outside this fixture.
            (directory / "Caddyfile.https").write_text((directory / "Caddyfile.https").read_text().replace(
                "reverse_proxy ui:8080", "tls internal\n\treverse_proxy ui:8080"))
            (directory / "compose.test.yaml").write_text(
                "services:\n  https:\n    ports: !override\n"
                f"      - '127.0.0.1:{ui_port}:8080'\n      - '127.0.0.1:{tls_port}:8443'\n")
            overrides.update(COMPOSE_FILE="compose.yaml:compose.release.yaml:compose.https.yaml:compose.test.yaml",
                             GATEWAY_HOST="localhost", AUTH_PUBLIC_URL=f"https://localhost:{tls_port}",
                             UI_PUBLIC_URL=f"https://localhost:{tls_port}")
        text = (directory / ".env").read_text()
        for name, value in overrides.items():
            lines = text.splitlines()
            lines = [line for line in lines if not line.startswith(name + "=")]
            text = "\n".join(lines) + "\n" + name + "=" + value + "\n"
        (directory / ".env").write_text(text)
        secrets = [line.partition("=")[2] for line in text.splitlines() if line.partition("=")[0] in initializer.SECRET_NAMES]
        # Ignore all inherited deployment/Compose configuration.
        env = {key: os.environ[key] for key in ("PATH", "HOME", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG") if key in os.environ}

        def compose(*args):
            result = subprocess.run(["docker", "compose", "--project-name", project, *args], cwd=directory, env=env, capture_output=True, text=True)
            if result.returncode:
                message = result.stderr
                for secret in secrets:
                    message = message.replace(secret, "[redacted]")
                raise RuntimeError("Compose " + args[0] + " failed: " + message)
            return result.stdout

        config = json.loads(compose("config", "--format", "json"))
        for name in ("gateway", "worker", "ui"):
            assert "build" not in config["services"][name], "Installation requires sources"
        for name in ("postgres", "redis", "worker"):
            assert not config["services"][name].get("ports"), "Private service publishes a port"
        if https:
            for name in ("gateway", "ui"):
                assert not config["services"][name].get("ports"), "HTTPS mode exposes a proxy bypass"
            assert config["services"]["https"]["image"] == images["ui"]
        assert config["services"]["worker"]["environment"]["MIGRATE"] == "off"
        key = None
        cookies = http.cookiejar.CookieJar()
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))
        base = f"https://localhost:{tls_port}" if https else f"http://localhost:{ui_port}"

        def request(path, body=None, expected=200, origin=None):
            headers = {"User-Agent": "Sociaro-GHCR-Release-Verification"}
            if key:
                headers["Authorization"] = "Bearer " + key
            if body is not None:
                headers["Content-Type"] = "application/json"
                headers["Origin"] = base
            req = urllib.request.Request((origin or base) + path,
                                         json.dumps(body).encode() if body is not None else None, headers)
            try:
                response = opener.open(req, timeout=10)
            except urllib.error.HTTPError as error:
                response = error
            data = response.read()
            assert response.status == expected, (path, response.status, expected)
            return data

        def generation():
            models = json.loads(request("/v1/models"))["data"]
            model = next(m["id"] for m in models if m["id"].startswith("mock/") and "image" in m["id"])
            job = json.loads(request("/v1/images/generations", {"model": model, "prompt": "Release installation verification"}, 202))
            deadline = time.monotonic() + 30
            while True:
                task = json.loads(request("/v1/generations/" + job["id"]))
                if task["status"] == "completed":
                    break
                assert task["status"] != "failed", "Mock generation failed"
                assert time.monotonic() < deadline, "Mock generation timed out"
                time.sleep(.1)
            data = request("/v1/generations/" + job["id"] + "/content")
            assert b"<svg" in data, "Downloaded mock image is not intact"
            return job["id"], data

        try:
            compose("up", "--no-build", "--pull", "never", "-d", "--wait", "--wait-timeout", "120")
            redis_uid = compose("exec", "-T", "redis", "sh", "-c", "awk '/^Uid:/ {print $2}' /proc/1/status").strip()
            assert redis_uid.isdigit() and int(redis_uid) != 0, "Redis must drop root privileges"
            if https:
                ca = compose("exec", "-T", "https", "cat", "/data/caddy/pki/authorities/local/root.crt")
                opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies),
                    urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))
                redirect = urllib.request.Request(f"http://localhost:{ui_port}/healthz", headers={
                    "Host": "localhost", "User-Agent": "Sociaro-GHCR-Release-Verification"})
                try:
                    urllib.request.build_opener(NoRedirect()).open(redirect, timeout=10)
                except urllib.error.HTTPError as error:
                    assert error.code == 308 and error.headers["Location"] == "https://localhost/healthz", "HTTP redirect is incorrect"
                else:
                    raise AssertionError("HTTP did not redirect to HTTPS")
            else:
                request("/healthz", origin="http://127.0.0.1:" + str(api_port))
            assert json.loads(request("/auth/methods"))["setup_required"] is True
            code = compose("exec", "-T", "gateway", "/gateway", "admin", "setup-code").strip()
            secrets.append(code)
            password = uuid.uuid4().hex + "-owner-password"
            secrets.append(password)
            request("/auth/setup", {"code": code, "name": "Release owner", "email": "release@example.test",
                                    "password": password}, 201)
            assert json.loads(request("/auth/methods"))["setup_required"] is False
            request("/auth/setup", {"code": code}, 409)
            request("/v1/system/settings")  # Real session has Administrator access.
            session_cookie = next(cookie for cookie in cookies if cookie.name == "media_gateway_session")
            assert session_cookie.secure == https, "Session cookie does not match the configured origin"
            key = json.loads(request("/v1/api-keys", {"name": "Release verification", "scopes": ["generation"]}, 201))["secret"]
            secrets.append(key)
            request("/metrics", expected=404)
            request("/")
            request("/healthz")
            first, data = generation()
            compose("restart", "--timeout", "30")
            compose("up", "--no-build", "--pull", "never", "-d", "--wait", "--wait-timeout", "120")
            assert request("/v1/generations/" + first + "/content") == data, "Restart lost a result or credentials"
            generation()
            key = None
            request("/v1/system/settings")  # Session survives the restart too.
            cookies.clear()
            request("/auth/email/login", {"email": "release@example.test", "password": password})
            request("/v1/system/settings")  # Password login works without email delivery.
            print("PASS: " + ("HTTPS (private test CA)" if https else "local") +
                  " source-free installation, owner/session without email, private services, UI/API, mock download and restart persistence")
        finally:
            # This UUID project was created above. Never touch any existing volumes.
            compose("down", "--volumes", "--remove-orphans")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default="v0.0.0-test")
    for name in ("gateway", "ui", "postgres", "redis"):
        parser.add_argument("--" + name, required=True)
    args = parser.parse_args()
    for https in (False, True):
        verify(args.version, {name: getattr(args, name) for name in ("gateway", "ui", "postgres", "redis")}, https=https)
