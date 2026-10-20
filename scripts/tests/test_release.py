"""Release archives and initial secrets must be safe without a Docker daemon."""
import hashlib
import importlib.util
import json
import posixpath
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest
from urllib.parse import unquote, urlsplit
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]


def load(name, file):
    spec = importlib.util.spec_from_file_location(name, ROOT / file)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


package = load("package_release", "scripts/package-release.py")
initialize = load("release_initialize", "deploy/ghcr/initialize.py")
publish = load("publish_release", "scripts/publish-release.py")


class ReleaseTests(unittest.TestCase):
    def test_source_only_bundle_links_are_versioned_and_code_examples_are_untouched(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / 'source'
            (root / 'docs').mkdir(parents=True)
            (root / 'docs/provider.md').write_text('# Provider\n')
            directory = Path(tmp) / 'bundle'
            (directory / 'docs').mkdir(parents=True)
            (directory / 'docs/local.md').write_text('# Local\n')
            document = directory / 'docs/guide.md'
            document.write_text('[Provider](provider.md#setup) [Local](local.md)\n'
                                '```sh\n[Example](not-a-file.md)\n```\n')
            with patch.object(package, 'ROOT', root):
                package.rewrite_bundle_documentation(directory, 'v0.0.0-test')
            self.assertIn(package.SOURCE_URL + '/blob/v0.0.0-test/docs/provider.md#setup', document.read_text())
            self.assertIn('[Local](local.md)', document.read_text())
            self.assertIn('[Example](not-a-file.md)', document.read_text())

    def test_bundled_markdown_has_no_missing_relative_file_links(self):
        with tempfile.TemporaryDirectory() as tmp:
            archive = package.package('v0.0.0-test', Path(tmp))
            with tarfile.open(archive) as tar:
                prefix = 'media-gateway-v0.0.0-test/'
                names = {member.name.removeprefix(prefix) for member in tar.getmembers() if member.isfile()}
                missing = []
                for name in names:
                    if not name.endswith('.md'):
                        continue
                    text = tar.extractfile(prefix + name).read().decode()
                    text = re.sub(r'```.*?```', '', text, flags=re.S)
                    for match in re.finditer(r'(?<!!)\[[^\]\n]*\]\(([^)\n]+)\)', text):
                        target = match.group(1).strip().split(' "', 1)[0].strip('<>')
                        parsed = urlsplit(target)
                        if not target or target.startswith(('#', '/')) or parsed.scheme or parsed.netloc:
                            continue
                        resolved = posixpath.normpath(posixpath.join(posixpath.dirname(name), unquote(parsed.path)))
                        if resolved not in names:
                            missing.append((name, target))
                self.assertEqual(missing, [])

    def test_embedded_font_bytes_and_notice_match_recorded_upstream_version(self):
        directory = ROOT / 'internal/payments/invoicefonts'
        provenance = json.loads((directory / 'provenance.json').read_text())
        self.assertEqual(provenance['font_version'], 'Liberation Sans 1.07.4')
        for name, record in provenance['files'].items():
            self.assertEqual(hashlib.sha256((directory / name).read_bytes()).hexdigest(), record['sha256'])
        notice = (directory / provenance['notice']['path']).read_bytes()
        self.assertEqual(hashlib.sha256(notice).hexdigest(), provenance['notice']['sha256'])
        self.assertIn(b'LIBERATION FONT SOFTWARE', notice)
        self.assertIn(b'GNU GENERAL PUBLIC LICENSE', notice)
        self.assertIn(b'As a special exception', notice)

    def test_only_allowlisted_monitoring_files_enter_release_bundle(self):
        monitoring = (
            'monitoring/prometheus.yml', 'monitoring/alerts.yml',
            'monitoring/grafana/provisioning/dashboards/gateway.yml',
            'monitoring/grafana/provisioning/datasources/prometheus.yml',
            'monitoring/grafana/dashboards/gateway-overview.json',
        )
        private = (
            '.env', 'deployment.json', 'deployment-private/administration-key',
            'monitoring/grafana/provisioning/.env',
            'monitoring/grafana/provisioning/deployment.json',
            'monitoring/grafana/provisioning/local/ssh-key',
            'monitoring/grafana/dashboards/operator-notes.txt',
        )
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / 'source'
            for name in set(package.FILES) | set(monitoring) | {'deploy/ghcr/env.example'}:
                target = source / name
                target.parent.mkdir(parents=True, exist_ok=True)
                if name.endswith('.md'):
                    target.write_text('# Synthetic packaging guide\n')
                else:
                    shutil.copyfile(ROOT / name, target)
            for name in private:
                target = source / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text('synthetic private sentinel; not a real credential\n')
            with patch.object(package, 'ROOT', source):
                archive = package.package('v0.0.0-test', Path(tmp) / 'output')
            with tarfile.open(archive) as tar:
                names = {member.name.removeprefix('media-gateway-v0.0.0-test/')
                         for member in tar.getmembers() if member.isfile()}
                for name in monitoring:
                    self.assertIn(name, names)
                    self.assertEqual(tar.extractfile('media-gateway-v0.0.0-test/' + name).read(),
                                     (source / name).read_bytes())
                for name in private:
                    self.assertNotIn(name, names)

    def test_docker_context_policy_excludes_private_deployment_bindings_at_any_depth(self):
        # Static policy regression; no Docker daemon or real private file reads.
        rules = [line.strip() for line in (ROOT / '.dockerignore').read_text().splitlines()
                 if line.strip() and not line.lstrip().startswith('#')]
        for rule in ('deployment.json', '**/deployment.json',
                     'deployment-private/', '**/deployment-private/'):
            self.assertIn(rule, rules)
        self.assertFalse(any(rule.startswith('!') and 'deployment' in rule for rule in rules))
        self.assertIn('!.env.example', rules)

    def test_docker_context_policy_excludes_nested_environment_files_but_keeps_examples(self):
        rules = [line.strip() for line in (ROOT / '.dockerignore').read_text().splitlines()
                 if line.strip() and not line.lstrip().startswith('#')]
        for rule in ('**/.env', '**/.env.*', '!**/.env.example'):
            self.assertIn(rule, rules)
        self.assertLess(rules.index('**/.env.*'), rules.index('!**/.env.example'))

    def test_source_free_bundle_has_pinned_images_and_readable_monitoring(self):
        refs = {name: "ghcr.io/sociaro-research/open-media-gateway/" + name + "@sha256:" + "a" * 64 for name in package.IMAGES}
        with tempfile.TemporaryDirectory() as tmp:
            archive = package.package("v0.1.0-rc.1", Path(tmp), refs)
            self.assertEqual((archive.parent / (archive.name + ".sha256")).read_text().split()[0], hashlib.sha256(archive.read_bytes()).hexdigest())
            with tarfile.open(archive) as tar:
                names = [m.name.removeprefix("media-gateway-v0.1.0-rc.1/") for m in tar.getmembers()]
                self.assertNotIn(".env", names)
                self.assertNotIn("go.mod", names)
                self.assertFalse(any(name.startswith(("web/", "internal/", ".git/")) for name in names))
                self.assertIn("monitoring/grafana/provisioning/datasources/prometheus.yml", names)
                self.assertIn("compose.https.yaml", names)
                self.assertIn("Caddyfile.https", names)
                template = tar.extractfile("media-gateway-v0.1.0-rc.1/.env.example").read().decode()
                for ref in refs.values():
                    self.assertIn(ref, template)
                for name in initialize.SECRET_NAMES:
                    self.assertRegex(template, r"(?m)^" + name + r"=$")
                for member in tar.getmembers():
                    self.assertEqual(member.mode, 0o755 if member.isdir() else 0o644)
            with self.assertRaises(FileExistsError):
                package.package("v0.1.0-rc.1", Path(tmp), refs)

    def test_rejects_invalid_versions_and_image_references(self):
        with tempfile.TemporaryDirectory() as tmp:
            for version in ("main", "../../etc", "v01.1.1", "v1.2.3\n", "v1.2.3;echo nope"):
                with self.assertRaises(ValueError):
                    package.package(version, Path(tmp))
            with self.assertRaises(ValueError):
                package.package("v1.2.3", Path(tmp), {"gateway": "untrusted/image:latest"})

    def test_secrets_are_independent_private_and_never_overwritten(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            (directory / ".env.example").write_text((ROOT / "deploy/ghcr/env.example").read_text().replace("@VERSION@", "v0.1.0"))
            destination = initialize.initialize(directory)
            original = destination.read_bytes()
            self.assertEqual(stat.S_IMODE(destination.stat().st_mode), 0o600)
            values = [re.search(r"(?m)^" + name + "=([a-f0-9]{64})$", original.decode()).group(1) for name in initialize.SECRET_NAMES]
            self.assertEqual(len(set(values)), len(values))
            with self.assertRaises(FileExistsError):
                initialize.initialize(directory)
            self.assertEqual(destination.read_bytes(), original)

    def test_initializer_rejects_incomplete_or_credentialed_templates(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            for template in ("POSTGRES_PASSWORD=", "POSTGRES_PASSWORD=unsafe", "POSTGRES_PASSWORD=@VERSION@"):
                (directory / ".env.example").write_text(template)
                with self.assertRaises(ValueError):
                    initialize.initialize(directory)
                self.assertFalse((directory / ".env").exists())

    def installation(self, directory, **options):
        (directory / ".env.example").write_text((ROOT / "deploy/ghcr/env.example").read_text().replace("@VERSION@", "v0.1.0"))
        initialize.initialize(directory, **options)
        return dict(line.split("=", 1) for line in (directory / ".env").read_text().splitlines()
                    if line and not line.startswith("#"))

    def test_server_mode_selects_https_and_one_canonical_origin(self):
        with tempfile.TemporaryDirectory() as tmp:
            values = self.installation(Path(tmp), public_url="https://Gateway.Example.com/")
            self.assertEqual(values["AUTH_PUBLIC_URL"], "https://gateway.example.com")
            self.assertEqual(values["AUTH_PUBLIC_URL"], values["UI_PUBLIC_URL"])
            self.assertEqual(values["GATEWAY_HOST"], "gateway.example.com")
            self.assertEqual(values["COMPOSE_FILE"], "compose.yaml:compose.release.yaml:compose.https.yaml")

    def test_existing_proxy_mode_keeps_private_ports_and_has_no_extra_listener(self):
        with tempfile.TemporaryDirectory() as tmp:
            values = self.installation(Path(tmp), public_url="https://gateway.example.com", behind_proxy=True,
                                       project_name="gateway-test1", api_port=18080, ui_port=18088)
            self.assertEqual(values["COMPOSE_FILE"], "compose.yaml:compose.release.yaml")
            self.assertEqual(values["UI_PUBLIC_URL"], "https://gateway.example.com")
            self.assertEqual((values["PORT"], values["UI_PORT"]), ("18080", "18088"))
            self.assertEqual(values["COMPOSE_PROJECT_NAME"], "gateway-test1")

    def test_local_custom_ports_update_browser_origin(self):
        with tempfile.TemporaryDirectory() as tmp:
            values = self.installation(Path(tmp), api_port=18080, ui_port=18088)
            self.assertEqual(values["GATEWAY_HOST"], "")
            self.assertEqual(values["AUTH_PUBLIC_URL"], "http://localhost:18088")
            self.assertEqual(values["AUTH_PUBLIC_URL"], values["UI_PUBLIC_URL"])

    def test_invalid_server_inputs_do_not_leave_an_env_file(self):
        urls = ("", "http://gateway.example.com", "https://localhost", "https://127.0.0.1", "https://[::1]",
                "https://gateway.local", "https://a.localhost", "https://user:password@gateway.example.com",
                "https://gateway.example.com:443", "https://gateway.example.com/path",
                "https://gateway.example.com?token=x", "https://gateway.example.com#fragment",
                "https://*.example.com", "https://bad_host.example.com", "https://-bad.example.com",
                "https://gateway.example.com\nATTACK=value", "https://{$UNSAFE}.example.com")
        options = [{"public_url": url} for url in urls] + [
            {"behind_proxy": True}, {"project_name": "../../other"}, {"project_name": "UPPER"},
            {"project_name": ""}, {"api_port": 0}, {"ui_port": 65536}, {"api_port": 8088}]
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            for option in options:
                with self.subTest(option=option), self.assertRaises(ValueError):
                    self.installation(directory, **option)
                self.assertFalse((directory / ".env").exists())

    def test_separate_installations_have_independent_secrets(self):
        with tempfile.TemporaryDirectory() as left, tempfile.TemporaryDirectory() as right:
            first = self.installation(Path(left))
            second = self.installation(Path(right), project_name="gateway-test1", api_port=18080, ui_port=18088)
            self.assertNotEqual(first["COMPOSE_PROJECT_NAME"], second["COMPOSE_PROJECT_NAME"])
            self.assertTrue(set(first[name] for name in initialize.SECRET_NAMES).isdisjoint(
                second[name] for name in initialize.SECRET_NAMES))

    def test_cli_in_an_extracted_bundle_and_retry_keep_secrets_private(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            (directory / ".env.example").write_text((ROOT / "deploy/ghcr/env.example").read_text().replace("@VERSION@", "v0.1.0"))
            script = directory / "initialize.py"
            script.write_bytes((ROOT / "deploy/ghcr/initialize.py").read_bytes())
            command = [sys.executable, str(script), "--public-url", "https://gateway.example.com"]
            first = subprocess.run(command, capture_output=True, text=True, check=True)
            original = (directory / ".env").read_bytes()
            retry = subprocess.run(command, capture_output=True, text=True)
            self.assertNotEqual(retry.returncode, 0)
            self.assertEqual((directory / ".env").read_bytes(), original)
            self.assertIn("https://gateway.example.com", first.stdout)
            values = dict(line.split("=", 1) for line in original.decode().splitlines() if "=" in line)
            for name in initialize.SECRET_NAMES:
                self.assertNotIn(values[name], first.stdout + first.stderr + retry.stdout + retry.stderr)

    def test_cli_explains_a_missing_bundle_instead_of_showing_a_traceback(self):
        with tempfile.TemporaryDirectory() as tmp:
            script = Path(tmp) / "initialize.py"
            script.write_bytes((ROOT / "deploy/ghcr/initialize.py").read_bytes())
            result = subprocess.run([sys.executable, str(script)], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("release bundle", result.stderr)
            self.assertNotIn("Traceback", result.stderr)
            self.assertFalse((Path(tmp) / ".env").exists())

    def test_publication_refuses_to_replace_an_existing_version(self):
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp)
            for name in publish.NAMES:
                for arch in ("amd64", "arm64"):
                    (evidence / (name + "-" + arch + ".digest")).write_text(publish.ROOT + "/" + name + "@sha256:" + "a" * 64)
            with patch.object(publish, "manifest", return_value={"manifests": [{"digest": "sha256:" + "b" * 64}]}), patch.object(publish, "command") as command:
                with self.assertRaisesRegex(RuntimeError, "Refusing to replace"):
                    publish.publish("v0.1.0", evidence, evidence / "output")
                command.assert_not_called()


if __name__ == "__main__":
    unittest.main()
