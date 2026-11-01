"""Production deployment guards: disposable Git trees and mocked server commands."""
import base64
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

SCRIPTS = Path(__file__).resolve().parents[1] / 'deployment'

def load(name):
    spec = importlib.util.spec_from_file_location('deployment_' + name, SCRIPTS / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

server, smoke, client, entry = (load(name) for name in ('remote', 'smoke', 'client', 'entry'))
tunnel, network = (load(name) for name in ('tunnel', 'install-network'))

FIXTURE_CONFIG = {
    'admin_ssh_host': 'root@192.0.2.10', 'tunnel_ssh_host': 'root@192.0.2.1',
    'ssh_host_key_alias': '192.0.2.10', 'known_hosts_file': Path('/tmp/fixture-known-hosts'),
    'wireguard_client_address': '192.0.2.2/32', 'wireguard_server_address': '192.0.2.1/32',
    'wireguard_allowed_route': '192.0.2.1/32', 'wireguard_endpoint': '192.0.2.10:51820',
    'wireguard_listen_port': 51820, 'wireguard_server_public_key_file': Path('/tmp/fixture-wg.pub'),
    'public_origin': 'https://app.example.test',
}
smoke.ORIGIN = 'https://app.example.test'

def setUpModule():
    # Never let fixture Git init/add/commit/config commands target a caller's repo.
    global _fixture_git_environment
    clean = {key: value for key, value in os.environ.items() if not key.startswith('GIT_')}
    _fixture_git_environment = patch.dict(os.environ, clean, clear=True)
    _fixture_git_environment.start()


def tearDownModule():
    _fixture_git_environment.stop()


class RemoteRelease(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.app = self.base / 'app'
        self.app.mkdir()
        for name in server.PROTECTED:
            (self.app / name).write_text('keep configuration\n')
        (self.app / 'old.txt').write_text('old source\n')
        (self.app / 'runtime.txt').write_text('untracked runtime\n')
        (self.base / 'deployed-commit').write_text('previous\n')
        self.addCleanup(patch.stopall)
        patch.object(server, 'BASE', self.base).start()
        patch.object(server, 'APP', self.app).start()
        patch.object(server, 'install_runner', lambda sha: None).start()
        self.calls = []
        patch.object(server, 'command', self.command).start()
        patch.object(server, 'capture', lambda args: 'gateway\nworker\nui\npostgres\nredis\ncaddy').start()
        patch.object(server, 'validate_frontend_configuration', lambda: self.calls.append(['config-validation'])).start()
        patch.object(server, 'verify_migrations', lambda: self.calls.append(['migrations'])).start()
        self.backup_fail = False
        self.build_fail = False
        self.stop_fail = False
        self.saved_hashes = server.config_hashes()
        patch.object(server, 'backup', self.backup).start()

    def command(self, args, **kwargs):
        self.calls.append(args)
        if self.build_fail and 'build' in args:
            raise subprocess.CalledProcessError(1, args)
        if self.stop_fail and args[:2] == ['python3', 'scripts/stop-worker-for-release.py']:
            raise subprocess.CalledProcessError(1, args)

    def backup(self, sha):
        self.calls.append(['backup'])
        self.assertTrue((self.app / 'old.txt').exists())
        self.assertFalse((self.app / 'new.txt').exists())
        if self.backup_fail:
            raise RuntimeError('Backup verification failed')
        return self.base / 'backups/test', self.saved_hashes

    def archive(self, name='new.txt', symlink=False):
        buffer = io.BytesIO()
        with tarfile.open(fileobj=buffer, mode='w') as archive:
            member = tarfile.TarInfo(name)
            if symlink:
                member.type = tarfile.SYMTYPE
                member.linkname = '/tmp/escape'
            else:
                member.size = 4
            archive.addfile(member, None if symlink else io.BytesIO(b'new\n'))
        return buffer.getvalue()

    def deploy(self, data=None, digest=None, previous='previous', manifest=None):
        data = self.archive() if data is None else data
        with contextlib.redirect_stdout(io.StringIO()):
            server.deploy('newsha', digest or hashlib.sha256(data).hexdigest(), previous,
                          manifest or {'deleted': {'old.txt': hashlib.sha256(b'old source\n').hexdigest()},
                                       'files': {'new.txt': hashlib.sha256(b'new\n').hexdigest()}}, io.BytesIO(data))

    def test_success_backs_up_first_preserves_config_and_removes_old_source(self):
        self.deploy()
        self.assertEqual(self.calls[0], ['backup'])
        self.assertFalse((self.app / 'old.txt').exists())
        self.assertEqual((self.app / 'new.txt').read_text(), 'new\n')
        self.assertEqual(server.config_hashes(), self.saved_hashes)
        self.assertTrue((self.app / 'runtime.txt').exists())
        self.assertIn(['python3', 'scripts/deployment/smoke.py', 'newsha'], self.calls)
        self.assertEqual((self.base / 'deployed-commit').read_text(), 'newsha\n')
        self.assertEqual((self.base / 'current-release').read_text(), 'newsha\n')
        self.assertEqual((self.base / 'releases/newsha/tracked-files.txt').read_text(), 'new.txt\n')
        compose = [c[len(server.COMPOSE):] for c in self.calls if c[:len(server.COMPOSE)] == server.COMPOSE]
        self.assertEqual(compose[:6], [['build', 'gateway', 'worker', 'ui'],
                         ['run', '--rm', '--no-deps', 'ui', 'caddy', 'validate', '--config', '/etc/caddy/Caddyfile', '--adapter', 'caddyfile'],
                         ['up', '-d', '--no-deps', '--wait', 'gateway'],
                         ['up', '-d', '--no-deps', '--wait', 'ui'], ['up', '-d', '--no-deps', 'worker'],
                         ['up', '-d', '--no-deps', 'caddy']])

    def test_unclean_worker_stop_prevents_gateway_replacement_and_release_marker(self):
        self.stop_fail = True
        with self.assertRaises(subprocess.CalledProcessError):
            self.deploy()
        self.assertFalse(any(c[:len(server.COMPOSE)] == server.COMPOSE and 'up' in c for c in self.calls))
        self.assertNotIn(['migrations'], self.calls)
        self.assertEqual((self.base / 'deployed-commit').read_text(), 'previous\n')

    def test_manual_release_marker_is_preferred_and_legacy_marker_synchronized(self):
        (self.base / 'current-release').write_text('manual\n')
        self.deploy(previous='manual')
        self.assertEqual((self.base / 'deployed-commit').read_text(), 'newsha\n')
        self.assertEqual((self.base / 'current-release').read_text(), 'newsha\n')

    def test_modified_obsolete_file_stops_before_deletion_or_build(self):
        (self.app / 'old.txt').write_text('server customization\n')
        with self.assertRaisesRegex(RuntimeError, 'modified on the server'):
            self.deploy()
        self.assertEqual((self.app / 'old.txt').read_text(), 'server customization\n')
        self.assertFalse((self.app / 'new.txt').exists())
        self.assertFalse(any('build' in c for c in self.calls))

    def test_nested_ui_caddyfile_is_included_while_root_config_is_protected(self):
        data = self.archive('web/Caddyfile')
        self.deploy(data=data, manifest={'deleted': {}, 'files': {'web/Caddyfile': hashlib.sha256(b'new\n').hexdigest()}})
        self.assertEqual((self.app / 'web/Caddyfile').read_text(), 'new\n')
        self.assertEqual((self.app / 'Caddyfile').read_text(), 'keep configuration\n')

    def test_bad_manifest_stops_before_backup(self):
        with self.assertRaisesRegex(RuntimeError, 'manifest does not match'):
            self.deploy(manifest={'deleted': {}, 'files': {'missing.txt': 'wrong'}})
        self.assertEqual(self.calls, [])

    def test_bad_source_hash_does_not_build_or_mark_success(self):
        with self.assertRaisesRegex(RuntimeError, 'hash mismatch'):
            self.deploy(manifest={'deleted': {}, 'files': {'new.txt': 'wrong'}})
        self.assertFalse(any('build' in c for c in self.calls))
        self.assertFalse((self.base / 'current-release').exists())

    def test_missing_ui_service_is_not_success(self):
        with patch.object(server, 'capture', return_value='gateway\nworker\npostgres\nredis\ncaddy'):
            with self.assertRaisesRegex(RuntimeError, 'not running'):
                self.deploy()
        self.assertFalse((self.base / 'current-release').exists())

    def test_ui_validation_failure_keeps_old_services_running(self):
        original = self.command
        def fail_ui(args, **kwargs):
            if args[:len(server.COMPOSE) + 1] == server.COMPOSE + ['run']:
                raise subprocess.CalledProcessError(1, args)
            return original(args, **kwargs)
        with patch.object(server, 'command', side_effect=fail_ui):
            with self.assertRaises(subprocess.CalledProcessError):
                self.deploy()
        self.assertFalse(any('stop' in c for c in self.calls))

    def test_bad_backup_never_changes_app_or_stops_services(self):
        self.backup_fail = True
        with self.assertRaisesRegex(RuntimeError, 'Backup verification'):
            self.deploy()
        self.assertTrue((self.app / 'old.txt').exists())
        self.assertEqual(self.calls, [['backup']])

    def test_failed_build_does_not_stop_worker_or_mark_success(self):
        self.build_fail = True
        with self.assertRaises(subprocess.CalledProcessError):
            self.deploy()
        self.assertEqual((self.base / 'deployed-commit').read_text(), 'previous\n')
        self.assertFalse(any('stop' in c for c in self.calls))

    def test_corrupt_upload_and_concurrent_release_stop_before_backup(self):
        for kwargs in ({'digest': 'wrong'}, {'previous': 'another-release'}):
            with self.subTest(kwargs=kwargs), self.assertRaises(RuntimeError):
                self.deploy(**kwargs)
        self.assertEqual(self.calls, [])

    def test_protected_paths_traversal_and_symlinks_rejected_before_backup(self):
        for name, symlink in [('.env', False), ('../escape', False), ('/tmp/escape', False), ('link', True)]:
            with self.subTest(name=name), self.assertRaises(RuntimeError):
                self.deploy(data=self.archive(name, symlink))
        self.assertEqual(self.calls, [])

    def test_existing_server_symlink_cannot_redirect_a_source_write(self):
        outside = self.base / 'outside'
        outside.mkdir()
        (self.app / 'web').symlink_to(outside, target_is_directory=True)
        with self.assertRaisesRegex(RuntimeError, 'server symlink'):
            self.deploy(data=self.archive('web/new.txt'))
        self.assertFalse((outside / 'new.txt').exists())
        self.assertEqual(self.calls, [])

    def test_duplicate_and_noncanonical_archive_members_are_rejected_before_backup(self):
        data = io.BytesIO()
        with tarfile.open(fileobj=data, mode='w') as archive:
            for _ in range(2):
                member = tarfile.TarInfo('new.txt')
                member.size = 4
                archive.addfile(member, io.BytesIO(b'new\n'))
        with self.assertRaisesRegex(RuntimeError, 'Duplicate'):
            self.deploy(data=data.getvalue())
        for name in ('./new.txt', 'web//new.txt', 'web/../new.txt'):
            with self.subTest(name=name), self.assertRaises(RuntimeError):
                self.deploy(data=self.archive(name))
        self.assertEqual(self.calls, [])


class SmokeCost(unittest.TestCase):
    def task(self):
        return {'id': '11111111-1111-4111-8111-111111111111', 'provider': 'mock',
                'cost': {'expected_usd': 0, 'customer_expected_usd': 0}}

    def test_public_actual_cost_is_hidden_but_database_confirms_zero(self):
        smoke.verify_cost(self.task(), lambda args: 't')

    def test_missing_or_nonzero_database_cost_is_not_success(self):
        for result in ('', 'f'):
            with self.subTest(result=result), self.assertRaises(RuntimeError):
                smoke.verify_cost(self.task(), lambda args: result)

    def test_paid_provider_and_missing_public_estimate_are_rejected(self):
        for change in ({'provider': 'openai'}, {'cost': {}}):
            with self.subTest(change=change), self.assertRaises(RuntimeError):
                smoke.verify_cost({**self.task(), **change}, lambda args: 't')


class FrontendConfiguration(unittest.TestCase):
    def test_docs_host_mismatch_stops_before_image_build(self):
        config={'services': {name: {} for name in ['gateway','worker','ui','caddy','postgres','redis']}}
        config['services']['gateway']['environment']={'DOCS_PUBLIC_URL':'https://docs.example.test'}
        config['services']['ui']['environment']={'DOCS_HOST':'wrong.example.test'}
        with patch.object(server,'capture',return_value=json.dumps(config)), patch.object(server,'command') as command:
            with self.assertRaisesRegex(RuntimeError,'DOCS_HOST'):
                server.validate_frontend_configuration()
        self.assertEqual(command.call_count,1)

    def test_correct_docs_configuration_validates_external_proxy(self):
        config={'services': {name: {} for name in ['gateway','worker','ui','caddy','postgres','redis']}}
        config['services']['gateway']['environment']={'DOCS_PUBLIC_URL':'https://docs.example.test'}
        config['services']['ui']['environment']={'DOCS_HOST':'docs.example.test'}
        with patch.object(server,'capture',return_value=json.dumps(config)), patch.object(server,'command') as command:
            server.validate_frontend_configuration()
        self.assertIn('validate',command.call_args.args[0])


class FrontendSmoke(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.previous=Path.cwd()
        import os
        os.chdir(self.temp.name);self.addCleanup(os.chdir,self.previous)
        folder=Path('web/public/ui');folder.mkdir(parents=True)
        for name in ['site.js','theme.js']:(folder/name).write_bytes(b'current release')
        self.old_ui=False;self.leak=False

    def request(self,path,body=None,auth=True,origin=smoke.ORIGIN):
        if origin!=smoke.ORIGIN:
            if path=='/openapi.json':return 200,json.dumps({'servers':[{'url':smoke.ORIGIN}],'paths':{'/auth/session':{}}}).encode()
            if path in ['/','/ui/docs.js']:return 200,b'docs'
            return (200 if self.leak else 404),b''
        if path=='/site.json':return 200,json.dumps({'api_url':smoke.ORIGIN,'ui_url':smoke.ORIGIN,'docs_url':'https://docs.example.test'}).encode()
        if path=='/organizations':return 200,b'Sign in to get started'
        if path in ['/privacy','/terms']:return 200,b'legal'
        if path in ['/ui/site.js','/ui/theme.js']:return 200,b'old release' if self.old_ui else b'current release'
        return (404 if path=='/ui/index.html' else 401),b''

    def test_current_ui_and_isolated_docs_pass(self):
        smoke.verify_frontend(self.request)

    def test_stale_ui_image_is_rejected(self):
        self.old_ui=True
        with self.assertRaisesRegex(RuntimeError,'UI image'):
            smoke.verify_frontend(self.request)

    def test_docs_operational_route_leak_is_rejected(self):
        self.leak=True
        with self.assertRaisesRegex(RuntimeError,'private route'):
            smoke.verify_frontend(self.request)


class RemoteDiskAndModes(unittest.TestCase):
    def test_low_disk_stops_before_backup_creation_or_commands(self):
        with tempfile.TemporaryDirectory() as folder:
            base = Path(folder)
            usage = type('Usage', (), {'free': 1024 ** 3})()
            with patch.object(server, 'BASE', base), patch.object(server.shutil, 'disk_usage', return_value=usage), patch.object(server, 'command') as command:
                with self.assertRaisesRegex(RuntimeError, 'Insufficient server disk space'):
                    server.backup('f' * 40)
                self.assertFalse((base / 'backups').exists())
                command.assert_not_called()

    def test_minimum_disk_reserve_is_accepted(self):
        usage = type('Usage', (), {'free': 4 * 1024 ** 3})()
        with patch.object(server.shutil, 'disk_usage', return_value=usage):
            server.require_backup_space()

    def test_archive_directory_mode_survives_private_umask(self):
        with tempfile.TemporaryDirectory() as folder:
            app = Path(folder)
            data = io.BytesIO()
            with tarfile.open(fileobj=data, mode='w') as archive:
                directory = tarfile.TarInfo('monitoring')
                directory.type = tarfile.DIRTYPE
                directory.mode = 0o755
                archive.addfile(directory)
                file = tarfile.TarInfo('monitoring/config.yaml')
                file.mode = 0o644
                file.size = 5
                archive.addfile(file, io.BytesIO(b'test\n'))
            data.seek(0)
            previous = server.os.umask(0o077)
            try:
                with patch.object(server, 'APP', app), tarfile.open(fileobj=data) as archive:
                    server.apply_archive(archive, {})
            finally:
                server.os.umask(previous)
            self.assertEqual((app / 'monitoring').stat().st_mode & 0o777, 0o755)
            self.assertEqual((app / 'monitoring/config.yaml').stat().st_mode & 0o777, 0o644)
            self.assertEqual((app / 'monitoring/config.yaml').read_bytes(), b'test\n')


class ActionsClient(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git('init', '-q', '-b', 'main')
        self.git('config', 'user.name', 'Release Test')
        self.git('config', 'user.email', 'release@example.test')
        self.git('config', 'commit.gpgsign', 'false')
        (self.root / 'old.txt').write_text('old\n')
        self.commit()
        self.previous = self.git('rev-parse', 'HEAD').strip().decode()
        (self.root / 'old.txt').unlink()
        (self.root / 'new.txt').write_text('new\n')
        self.commit()
        self.sha = self.git('rev-parse', 'HEAD').strip().decode()
        self.output = self.root.parent / (self.root.name + '.upload')
        self.addCleanup(lambda: self.output.unlink(missing_ok=True))

    def git(self, *args):
        return client.git(*args, root=self.root)

    def commit(self):
        self.git('add', '--all')
        self.git('commit', '-qm', 'Fixture')

    def prepare(self, sha=None, previous=None):
        return client.prepare(sha or self.sha, previous or self.previous, self.output, self.root)

    def test_exact_commit_upload_includes_previous_hash_and_archive_checksum(self):
        digest = self.prepare()
        header, data = self.output.read_bytes().split(b'\n', 1)
        self.assertEqual(digest, hashlib.sha256(data).hexdigest())
        self.assertEqual(json.loads(header), {'deleted': {'old.txt': hashlib.sha256(b'old\n').hexdigest()},
                                             'files': {'new.txt': hashlib.sha256(b'new\n').hexdigest()}})
        with tarfile.open(fileobj=io.BytesIO(data)) as archive:
            self.assertEqual(archive.extractfile('new.txt').read(), b'new\n')

    def test_dirty_mismatched_or_invalid_commits_never_create_payload(self):
        for sha in ('main', 'a' * 40, self.previous):
            with self.subTest(sha=sha), self.assertRaises(RuntimeError):
                self.prepare(sha)
        (self.root / 'new.txt').write_text('unchecked\n')
        with self.assertRaisesRegex(RuntimeError, 'clean'):
            self.prepare()
        self.assertFalse(self.output.exists())

    def test_older_release_cannot_overwrite_newer_production(self):
        self.git('checkout', '-q', self.previous)
        with self.assertRaises(subprocess.CalledProcessError):
            self.prepare(self.previous, self.sha)
        self.assertFalse(self.output.exists())

    def test_protected_root_config_and_symlinks_cannot_be_published(self):
        for name in sorted(client.PROTECTED):
            (self.root / name).write_text('server configuration\n')
            self.commit()
            self.sha = self.git('rev-parse', 'HEAD').strip().decode()
            with self.assertRaisesRegex(RuntimeError, 'Server-only'):
                self.prepare()
            (self.root / name).unlink()
            self.commit()
        (self.root / 'link').symlink_to('new.txt')
        self.commit()
        self.sha = self.git('rev-parse', 'HEAD').strip().decode()
        with self.assertRaisesRegex(RuntimeError, 'links'):
            self.prepare()

    def test_export_ignore_cannot_silently_omit_source(self):
        (self.root / '.gitattributes').write_text('new.txt export-ignore\n')
        self.commit()
        self.sha = self.git('rev-parse', 'HEAD').strip().decode()
        with self.assertRaisesRegex(RuntimeError, 'manifest differ'):
            self.prepare()

    def test_ssh_uses_pinned_host_key_without_shell_or_secret_environment(self):
        with patch.object(client, 'load_config', return_value=FIXTURE_CONFIG), patch.object(client.subprocess, 'run') as run:
            run.return_value.stdout = b'{"sha":"safe"}'
            self.assertEqual(client.ssh(Path('/tmp/test-key'), 'status'), {'sha': 'safe'})
        args = run.call_args.args[0]
        self.assertIn('StrictHostKeyChecking=yes', args)
        self.assertIn('HostKeyAlgorithms=ssh-ed25519', args)
        self.assertIn('HostKeyAlias=192.0.2.10', args)
        self.assertIn('BatchMode=yes', args)
        self.assertEqual(args[-2:], [FIXTURE_CONFIG['tunnel_ssh_host'], 'status'])
        self.assertNotIn('shell', run.call_args.kwargs)

    def test_poll_reconnects_and_does_not_interpret_workflow_commands(self):
        responses = [subprocess.CalledProcessError(255, ['ssh']),
                     {'sha': self.sha, 'state': 'running', 'offset': 50,
                      'output': '::error::untrusted server text\n', 'more': False},
                     {'sha': self.sha, 'state': 'succeeded', 'offset': 50, 'output': '', 'more': False}]
        output = io.StringIO()
        with patch.object(client, 'ssh', side_effect=responses) as ssh, patch.object(client.time, 'sleep'), contextlib.redirect_stdout(output):
            client.poll(Path('/tmp/test-key'), 'a' * 32, self.sha)
        self.assertIn('server | ::error::untrusted server text', output.getvalue())
        self.assertEqual(ssh.call_args.args[1], 'poll ' + 'a' * 32 + ' 50')

    def test_failed_or_wrong_release_is_never_reported_as_success(self):
        for state in ({'sha': self.sha, 'state': 'failed', 'offset': 0},
                      {'sha': self.previous, 'state': 'succeeded', 'offset': 0}):
            with self.subTest(state=state), patch.object(client, 'ssh', return_value=state), self.assertRaises(RuntimeError):
                client.poll(Path('/tmp/test-key'), 'a' * 32, self.sha)


class DeploymentSummary(unittest.TestCase):
    def test_no_public_origin_omits_the_application_link(self):
        self.assertEqual(client.success_summary(), 'Deployment and production checks passed.\n')

    def test_explicit_public_origin_is_display_only(self):
        url = 'https://gateway.example.test:8443'
        self.assertEqual(client.success_summary(url),
                         'Deployment and production checks passed. [Open application](' + url + ').\n')
        workflow = (SCRIPTS.parents[1] / '.github/workflows/deploy.yml').read_text()
        self.assertIn('url: ${{ vars.PRODUCTION_PUBLIC_URL }}', workflow)
        self.assertIn('DEPLOY_PUBLIC_URL: ${{ vars.PRODUCTION_PUBLIC_URL }}', workflow)

    def test_unsafe_public_links_are_rejected_before_any_ssh(self):
        values = ('http://gateway.example.test', 'https://user:password@example.test',
                  'https://example.test/path', 'https://example.test?token=private',
                  'https://example.test#fragment', 'https://example.test)\nINJECTED',
                  'https://example.test:99999', 'https://[invalid')
        for value in values:
            config = {**FIXTURE_CONFIG, 'public_origin': value}
            with self.subTest(value=value), patch.dict(os.environ, {}, clear=True), patch.object(client, 'load_config', return_value=config) as load_config, patch.object(client, 'ssh') as ssh:
                with self.assertRaisesRegex(RuntimeError, 'HTTPS origin'):
                    client.main()
                load_config.assert_called_once_with(client.ROOT)
                ssh.assert_not_called()


class PrivateDeploymentConfig(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.profile = self.root / 'deployment.json'
        # Synthetic public SSH wire data only; never use an operator's profile
        # or open any private key from these format/lookup regression cases.
        public = base64.b64encode(b'\x00\x00\x00\x0bssh-ed25519\x00\x00\x00 ' + bytes(range(32))).decode()
        (self.root / 'known_hosts').write_text('fixture.example.test ssh-ed25519 ' + public + '\n')
        self.config = {
            'version': 1, 'admin_ssh_host': 'root@192.0.2.10',
            'tunnel_ssh_host': 'root@192.0.2.1',
            'ssh_host_key_alias': 'fixture.example.test',
            'known_hosts_file': 'known_hosts',
            'public_origin': 'https://app.example.test',
            'repository_url': 'git@github.com:example/project.git',
        }

    def write_profile(self):
        self.profile.write_text(json.dumps(self.config))
        self.profile.chmod(0o600)

    def test_explicit_profile_resolves_relative_public_pin_without_private_keys(self):
        self.write_profile()
        with patch.dict(os.environ, {'DEPLOY_CONFIG': str(self.profile)}, clear=True):
            config = client.load_config(root=self.root, tool_dir=self.root / 'Source')
        self.assertEqual(config['known_hosts_file'], (self.root / 'known_hosts').resolve())
        self.assertEqual(config['public_origin'], 'https://app.example.test')

    def test_invalid_profile_origin_stops_client_before_ssh(self):
        for value in ('http://app.example.test', 'https://user:password@example.test',
                      'https://app.example.test/path', 'https://app.example.test?token=fixture',
                      'https://app.example.test#fragment', 'https://app.example.test:99999'):
            self.config['public_origin'] = value
            self.write_profile()
            with self.subTest(value=value), patch.dict(os.environ, {'DEPLOY_CONFIG': str(self.profile)}, clear=True), patch.object(client, 'ssh') as ssh:
                with self.assertRaisesRegex(RuntimeError, 'HTTPS public origin'):
                    client.main()
                ssh.assert_not_called()


class RestrictedSSH(unittest.TestCase):
    def test_only_strict_deploy_protocol_is_accepted(self):
        sha, digest = 'a' * 40, 'b' * 64
        self.assertEqual(entry.parse_command('status'), ['status'])
        self.assertEqual(entry.parse_command('start ' + sha + ' ' + digest + ' ' + sha), ['start', sha, digest, sha])
        self.assertEqual(entry.parse_command('poll ' + 'a' * 32 + ' 0'), ['poll', 'a' * 32, '0'])
        for command in ('sh', 'status; id', 'status\n', 'cat .env', 'poll ../../etc/passwd 0',
                        'poll ' + 'a' * 32 + ' -1', 'start main ' + digest + ' ' + sha,
                        '_run ' + 'a' * 32, 'start ' + sha + ' ' + digest + ' ' + sha + ' extra'):
            with self.subTest(command=command), self.assertRaises(RuntimeError):
                entry.parse_command(command)

    def test_upload_limit_does_not_start_a_process(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(entry, 'JOBS', Path(folder) / 'jobs'), patch.object(entry, 'MAX_UPLOAD', 3), patch.object(entry.subprocess, 'Popen') as spawn:
            with self.assertRaisesRegex(RuntimeError, 'exceeds'):
                entry.dispatch('start ' + 'a' * 40 + ' ' + 'b' * 64 + ' ' + 'c' * 40, io.BytesIO(b'too large'))
            spawn.assert_not_called()
            self.assertFalse(list(Path(folder).rglob('upload')))

    def test_detached_job_finishes_after_start_returns_and_reports_failure(self):
        import time
        for fails in (False, True):
            with self.subTest(fails=fails), tempfile.TemporaryDirectory() as temporary:
                folder = Path(temporary)
                script = folder / 'entry.py'
                source = (SCRIPTS / 'entry.py').read_text().replace("BASE = Path('/opt/media-gateway')", 'BASE = Path(' + repr(str(folder)) + ')')
                script.write_text(source)
                (folder / 'remote.py').write_text('import sys, time\ndef main():\n    time.sleep(0.1)\n    assert sys.stdin.buffer.read() == b"payload"\n' + ('    raise RuntimeError("Fixture failure")\n' if fails else '    print("Verified fixture", flush=True)\n'))
                with patch.object(entry, 'BASE', folder), patch.object(entry, 'JOBS', folder / 'actions-jobs'), patch.object(entry, '__file__', str(script)):
                    result = entry.dispatch('start ' + 'a' * 40 + ' ' + 'b' * 64 + ' ' + 'c' * 40, io.BytesIO(b'payload'))
                    deadline = time.monotonic() + 5
                    while True:
                        state = entry.dispatch('poll ' + result['job'] + ' 0', io.BytesIO())
                        if state['state'] != 'running':
                            break
                        self.assertLess(time.monotonic(), deadline)
                        time.sleep(0.02)
                    self.assertEqual(state['state'], 'failed' if fails else 'succeeded')
                    self.assertIn('Fixture failure' if fails else 'Verified fixture', state['output'])
                    self.assertFalse((folder / 'actions-jobs' / result['job'] / 'upload').exists())


class RunnerInstallation(unittest.TestCase):
    def test_verified_version_is_installed_atomically_and_kept_outside_app(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            app = base / 'app'
            (app / 'scripts/deployment').mkdir(parents=True)
            for name in ('entry.py', 'remote.py', 'smoke.py'):
                (app / 'scripts/deployment' / name).write_text('verified ' + name)
            with patch.object(server, 'BASE', base), patch.object(server, 'APP', app):
                server.install_runner('a' * 40)
            self.assertEqual((base / 'deployer/current').resolve(), (base / 'deployer' / ('a' * 40)).resolve())
            self.assertEqual((base / 'deployer/current/entry.py').read_text(), 'verified entry.py')
            self.assertEqual((base / 'deployer/current/entry.py').stat().st_mode & 0o777, 0o600)


class PrivateNetwork(unittest.TestCase):
    def setUp(self):
        import base64
        self.key = base64.b64encode(bytes(range(32))).decode()
        self.addCleanup(patch.stopall)
        for module in (tunnel, network, client):
            patch.object(module, 'load_config', return_value=FIXTURE_CONFIG).start()

    def test_only_the_server_private_address_is_routed(self):
        config = tunnel.configuration(self.key, self.key)
        self.assertIn('AllowedIPs = 192.0.2.1/32', config)
        self.assertNotIn('0.0.0.0/0', config)
        self.assertNotIn('DNS =', config)
        self.assertIn('MTU = 1280', config)

    def test_invalid_or_injected_credentials_stop_before_privileged_operations(self):
        for value in ('', 'bad', self.key + '\nPostUp = unsafe', 'A' * 44):
            with self.subTest(value=value), self.assertRaises(RuntimeError):
                tunnel.configuration(value, self.key)

    def test_tunnel_cleanup_and_secret_file_removal_after_command_failure(self):
        calls = []
        paths = []
        def run(args, **kwargs):
            calls.append((args, kwargs))
            if args[:4] == ['sudo', '-n', 'wg-quick', 'up']:
                path = Path(args[4])
                paths.append(path)
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                self.assertIn(self.key, path.read_text())
                self.assertNotIn('DEPLOY_SSH_KEY', kwargs['env'])
                self.assertNotIn('DEPLOY_WG_PRIVATE_KEY', kwargs['env'])
            return type('Result', (), {'returncode': 7 if args == ['fixture-command'] else 0})()
        env = {'GITHUB_REF': 'refs/heads/main', 'GITHUB_EVENT_NAME': 'workflow_dispatch',
               'DEPLOY_WG_PRIVATE_KEY': self.key, 'DEPLOY_SSH_KEY': 'fixture-secret'}
        with patch.dict(os.environ, env, clear=True), patch.object(tunnel.Path, 'read_text', return_value=self.key), patch.object(tunnel.subprocess, 'run', side_effect=run):
            self.assertEqual(tunnel.run(['fixture-command']), 7)
            self.assertNotIn('DEPLOY_WG_PRIVATE_KEY', os.environ)
        self.assertEqual(calls[-1][0][:4], ['sudo', '-n', 'wg-quick', 'down'])
        self.assertFalse(paths[0].exists())

    def test_non_main_or_automatic_workflow_cannot_open_tunnel(self):
        for env in ({'GITHUB_REF': 'refs/heads/feature', 'GITHUB_EVENT_NAME': 'workflow_dispatch'},
                    {'GITHUB_REF': 'refs/heads/main', 'GITHUB_EVENT_NAME': 'push'}):
            with self.subTest(env=env), patch.dict(os.environ, env, clear=True), patch.object(tunnel.subprocess, 'run') as run, self.assertRaises(RuntimeError):
                tunnel.run(['fixture-command'])
            run.assert_not_called()

    def test_server_rules_allow_ssh_and_block_other_host_and_docker_access(self):
        config = network.configuration(self.key, self.key)
        self.assertIn('-i %i -s 192.0.2.2/32 -p tcp --dport 22 -j ACCEPT', config)
        self.assertIn('INPUT 2 -i %i -j DROP', config)
        self.assertIn('DOCKER-USER 1 -i %i -j DROP', config)
        self.assertIn('AllowedIPs = 192.0.2.2/32', config)
        self.assertNotIn('iptables -F', config)

    def test_cleanup_after_failed_interface_setup(self):
        calls, paths = [], []
        def run(args, **kwargs):
            calls.append(args)
            if 'up' in args:
                paths.append(Path(args[-1]))
                raise subprocess.CalledProcessError(1, args)
            return type('Result', (), {'returncode': 0})()
        env = {'GITHUB_REF': 'refs/heads/main', 'GITHUB_EVENT_NAME': 'workflow_dispatch', 'DEPLOY_WG_PRIVATE_KEY': self.key}
        with patch.dict(os.environ, env, clear=True), patch.object(tunnel.Path, 'read_text', return_value=self.key), patch.object(tunnel.subprocess, 'run', side_effect=run), self.assertRaises(subprocess.CalledProcessError):
            tunnel.run(['fixture-command'])
        self.assertEqual(calls[-1][:4], ['sudo', '-n', 'wg-quick', 'down'])
        self.assertFalse(paths[0].exists())

    def test_connectivity_preflight_never_builds_or_uploads_release(self):
        env = {'GITHUB_REF': 'refs/heads/main', 'GITHUB_EVENT_NAME': 'workflow_dispatch', 'DEPLOY_SSH_KEY': 'fixture', 'RELEASE_SHA': 'a' * 40}
        with patch.dict(os.environ, env, clear=True), patch.object(client.sys, 'argv', ['client.py', '--check-connection']), patch.object(client, 'ssh', return_value={'sha': 'b' * 40}) as ssh, patch.object(client, 'prepare') as prepare, contextlib.redirect_stdout(io.StringIO()):
            client.main()
        self.assertEqual(ssh.call_args.args[1], 'status')
        prepare.assert_not_called()
