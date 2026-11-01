"""Offline release tests: real temporary Git repositories, mocked server commands."""
import argparse
import contextlib
import hashlib
import importlib.util
import os
import io
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

SCRIPTS = Path(__file__).resolve().parents[1]


def load(name):
    spec = importlib.util.spec_from_file_location(name, SCRIPTS / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


local = load('release')
server = load('release_remote')
smoke = load('release_smoke')
checks = load('release_checks')
window = load('release_ui')
smoke.ORIGIN = 'https://app.example.test'


def setUpModule():
    # Never let fixture Git init/add/commit/config commands target a caller's repo.
    global _fixture_git_environment
    clean = {key: value for key, value in os.environ.items() if not key.startswith('GIT_')}
    _fixture_git_environment = patch.dict(os.environ, clean, clear=True)
    _fixture_git_environment.start()


def tearDownModule():
    _fixture_git_environment.stop()


class GitHubTransport(unittest.TestCase):
    def test_read_commands_use_copied_key_without_persistent_git_config(self):
        with tempfile.TemporaryDirectory(prefix='release tool ') as tmp:
            root=Path(tmp);source=root/'Source';source.mkdir();(root/'ssh').mkdir()
            (root/'ssh/github_ed25519').write_text('synthetic key')
            (source/'github_known_hosts').write_text('synthetic public host key')
            remote = 'git@github.com:example/project.git'
            config = {'repository_url': remote, 'github_ssh_key_file': root/'ssh/github_ed25519'}
            with patch.object(local,'deployment_config',return_value=config),patch.object(local,'TOOL_DIR',source),patch.object(local,'git',return_value=remote.replace('git@github.com:','https://github.com/')):
                for verb in ('fetch','ls-remote'):
                    args=local.github_transport(('git',verb,'origin'))
                    self.assertIn('url.'+remote+'.insteadOf='+remote.replace('git@github.com:','https://github.com/'),args)
                    command=next(a for a in args if a.startswith('core.sshCommand='))
                    self.assertIn('StrictHostKeyChecking=yes',command)
                    self.assertIn('UserKnownHostsFile="'+str(source/'github_known_hosts')+'"',command)
                    self.assertEqual(args[-2:],(verb,'origin'))
                self.assertEqual(local.github_transport(('git','status')),('git','status'))

    def test_other_repositories_keep_their_transport(self):
        with patch.object(local,'deployment_config',return_value={'repository_url': '/fixture/origin.git'}),patch.object(local,'git',return_value='/fixture/origin.git'):
            self.assertEqual(local.github_transport(('git','fetch','origin')),('git','fetch','origin'))


class LocalRelease(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'checkout'
        self.remote = Path(self.temp.name) / 'origin.git'
        self.root.mkdir()
        subprocess.run(['git', 'init', '--bare', '-q', str(self.remote)], check=True)
        self.git('init', '-q', '-b', 'main')
        self.git('config', 'user.email', 'release@example.test')
        self.git('config', 'user.name', 'Release Test')
        self.git('config', 'commit.gpgsign', 'false')
        (self.root / 'app.txt').write_text('original\n')
        self.git('add', '.')
        self.git('commit', '-qm', 'Initial')
        self.git('remote', 'add', 'origin', str(self.remote))
        self.git('push', '-q', 'origin', 'main')
        self.initial = self.git('rev-parse', 'HEAD').strip()
        self.calls = []
        self.upload = None
        self.fail = None
        self.drift = False
        self.check_mutation = None
        self.progress = {}
        self.real_run = local.run
        self.addCleanup(patch.stopall)
        patch.object(local, 'ROOT', self.root).start()
        patch.object(local, 'deployment_config', return_value={'repository_url': str(self.remote), 'admin_ssh_host': 'root@192.0.2.10'}).start()
        patch.object(local, 'confirm', lambda *args: None).start()
        patch.object(local, 'ssh_args', return_value=['ssh', 'fixture']).start()
        patch.object(local, 'run', self.fake_run).start()

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, text=True, stderr=subprocess.DEVNULL)

    def fake_run(self, *args, capture=False, stdin=None):
        self.calls.append(args)
        if args[:2] == self.fail:
            raise subprocess.CalledProcessError(1, args)
        if args[0] == 'ssh':
            if capture:
                return self.initial + '\n'
            self.upload = stdin.read()
            return None
        if args[0] == local.sys.executable:
            if len(args) > 1 and args[1].endswith('release_checks.py'):
                if self.fail == ('checks',):
                    raise subprocess.CalledProcessError(1, args)
                if self.drift:
                    (self.root / 'app.txt').write_text('changed during tests\n')
                if self.check_mutation:
                    self.check_mutation()
            return None
        if args[0] == 'make':
            if self.drift and args[1] == 'verify':
                (self.root / 'app.txt').write_text('changed during tests\n')
            return None
        return self.real_run(*args, capture=capture, stdin=stdin)

    def release(self, check_only=False, native_only=False, deploy_only=False, extended_only=False):
        with contextlib.redirect_stdout(io.StringIO()):
            local.release(argparse.Namespace(yes=True, check_only=check_only, native_only=native_only, deploy_only=deploy_only, extended_only=extended_only), self.progress)

    def assert_no_git_writes(self):
        forbidden = {'add', 'commit', 'push', 'merge', 'rebase', 'reset', 'write-tree', 'stash', 'checkout'}
        self.assertFalse(any(c[0] == 'git' and c[1] in forbidden for c in self.calls))

    def test_deploys_exact_published_commit_without_git_writes(self):
        self.release()
        header, data = self.upload.split(b'\n', 1)
        self.assertEqual(json.loads(header)['files']['app.txt'], hashlib.sha256(b'original\n').hexdigest())
        with tarfile.open(fileobj=io.BytesIO(data)) as archive:
            self.assertEqual(archive.extractfile('app.txt').read(), b'original\n')
        self.assert_no_git_writes()
        self.assertTrue(self.progress['deployment_started'])

    def test_dirty_checkout_is_preserved_and_refused(self):
        for staged in (False, True):
            (self.root/'app.txt').write_text('my edits\n')
            if staged:self.git('add','app.txt')
            before=self.git('status','--porcelain')
            with self.assertRaisesRegex(RuntimeError,'GitHub Desktop'):self.release()
            self.assertEqual(self.git('status','--porcelain'),before)
            self.assertIsNone(self.upload)
        self.assert_no_git_writes()

    def test_unpushed_commit_refused(self):
        (self.root/'app.txt').write_text('new\n');self.git('add','.');self.git('commit','-qm','User commit')
        with self.assertRaisesRegex(RuntimeError,'HEAD must equal'):self.release()
        self.assertIsNone(self.upload)
        self.assert_no_git_writes()

    def test_failed_checks_never_upload(self):
        self.fail=('checks',)
        with self.assertRaises(subprocess.CalledProcessError):self.release()
        self.assertIsNone(self.upload)
        self.assert_no_git_writes()

    def test_concurrent_edit_stops_deployment(self):
        self.drift=True
        with self.assertRaisesRegex(RuntimeError,'Files changed during checks'):self.release()
        self.assertIsNone(self.upload)
        self.assertIn('changed during tests',(self.root/'app.txt').read_text())
        self.assert_no_git_writes()

    def test_head_change_with_identical_tree_stops(self):
        self.check_mutation=lambda:self.git('commit','--allow-empty','-qm','User commit during checks')
        with self.assertRaisesRegex(RuntimeError,'HEAD changed'):self.release()
        self.assertIsNone(self.upload)
        self.assert_no_git_writes()

    def test_check_only_allows_edits_without_network_or_index_writes(self):
        (self.root/'app.txt').write_text('uncommitted\n')
        self.release(check_only=True, native_only=True)
        self.assertFalse(any(c[0]=='ssh' or c[:2]==('git','fetch') for c in self.calls))
        self.assertIsNone(self.upload)
        self.assert_no_git_writes()
        self.assertTrue(any('--native-only' in c for c in self.calls))

    def test_additional_ci_only_check_and_deployment(self):
        self.release(check_only=True,extended_only=True)
        self.assertIsNone(self.upload)
        self.assertTrue(any('--extended-only' in c for c in self.calls))
        self.calls.clear()
        self.release(extended_only=True)
        self.assertIsNotNone(self.upload)
        self.assertTrue(any('--extended-only' in c for c in self.calls))
        self.assert_no_git_writes()

    def test_deploy_only_skips_ci_but_keeps_release_and_remote_gates(self):
        self.fail=('checks',)
        self.release(deploy_only=True)
        self.assertIsNotNone(self.upload)
        self.assertFalse(any(len(c)>1 and str(c[1]).endswith('release_checks.py') for c in self.calls))
        self.assertIn(('make','release-check'),self.calls)
        self.assertTrue(any(c[:2]==('git','ls-remote') for c in self.calls))
        self.assert_no_git_writes()

    def test_deploy_only_refuses_dirty_checkout(self):
        (self.root/'app.txt').write_text('uncommitted\n')
        with self.assertRaisesRegex(RuntimeError,'clean checkout'):self.release(deploy_only=True)
        self.assertIsNone(self.upload)

    def test_deploy_only_refuses_unpushed_commit(self):
        self.git('commit','--allow-empty','-qm','Unpushed')
        with self.assertRaisesRegex(RuntimeError,'HEAD must equal'):self.release(deploy_only=True)
        self.assertIsNone(self.upload)

    def test_wrong_branch_refused(self):
        self.git('checkout','-qb','feature')
        with self.assertRaisesRegex(RuntimeError,'Switch to main'):self.release()
        self.assert_no_git_writes()

    def test_failure_guidance_reports_partial_deployment(self):
        self.assertIn('not started',local.failure_guidance({}))
        self.assertIn('may be partial',local.failure_guidance({'deployment_started':True}))


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


class IsolatedChecks(unittest.TestCase):
    def test_additional_ci_browser_checks_are_included_without_shell_execution(self):
        with tempfile.TemporaryDirectory() as folder:
            project=Path(folder);workflow=project/'.github/workflows/ci.yml';workflow.parent.mkdir(parents=True)
            workflow.write_text('  node scripts/ui/new-check.cjs\n  node scripts/ui/new-check.cjs\n  node scripts/ui/unsafe.cjs; touch PWNED\n')
            selected=checks.project_ui_checks(project)
            self.assertEqual(selected.count(('new-check.cjs',)),1)
            self.assertIn(('separation.cjs',),selected)
            self.assertFalse(any('unsafe' in entry[0] for entry in selected))

    def test_edited_suites_run_first_without_dropping_required_checks(self):
        with tempfile.TemporaryDirectory() as folder:
            project=Path(folder)
            def git(*args):
                return subprocess.check_output(['git', *args], cwd=project, stderr=subprocess.DEVNULL)
            git('init', '-q');git('config', 'user.email', 'release@example.test');git('config', 'user.name', 'Release Test')
            workflow=project/'.github/workflows/ci.yml';workflow.parent.mkdir(parents=True)
            workflow.write_text('  node scripts/ui/email-delivery.cjs\n  node scripts/ui/new-check.cjs --fixture\n')
            tests=project/'scripts/ui';tests.mkdir(parents=True)
            for name in ['branding.cjs', 'profile.cjs', 'email-delivery.cjs']:(tests/name).write_text('// original\n')
            git('add', '.');git('commit', '-qm', 'Fixture')
            baseline=checks.project_ui_checks(project)
            (tests/'branding.cjs').write_text('// staged edit\n');git('add', 'scripts/ui/branding.cjs')
            (tests/'email-delivery.cjs').write_text('// unstaged edit\n')
            (tests/'new-check.cjs').write_text('// new test\n')
            selected=checks.project_ui_checks(project)
            self.assertEqual(selected[:3], [('branding.cjs',), ('email-delivery.cjs',), ('new-check.cjs', '--fixture')])
            self.assertCountEqual(selected, baseline)
            self.assertIn(('smoke.cjs', '--generations-only'), selected)
            self.assertIn(('separation.cjs',), selected)

    def test_git_inspection_failure_keeps_all_browser_checks(self):
        with tempfile.TemporaryDirectory() as folder:
            project=Path(folder);(project/'.git').mkdir()
            with patch.object(checks.subprocess, 'check_output', side_effect=subprocess.CalledProcessError(1, ['git'])):
                self.assertEqual(checks.project_ui_checks(project), checks.UI_CHECKS)

    def test_check_order_and_service_environment(self):
        env={'GATEWAY_TEST_DATABASE_URL':'isolated','GATEWAY_TEST_REDIS_URL':'isolated'}
        @contextlib.contextmanager
        def services(_):yield env
        with patch.object(checks,'browser_environment',return_value=('node',{})), patch.object(checks,'caddy_runtime',return_value={}), patch.object(checks,'isolated_services',services), patch.object(checks,'execute') as execute, patch.object(Path,'is_file',return_value=True):
            checks.verify('/tmp/test-project', native_only=True)
        args=[call.args for call in execute.call_args_list]
        self.assertEqual(args[0][0],['make','verify-ci','TEST_FLAGS=-count=1'])
        self.assertEqual(args[1][0],['go','test','-race','-count=1','-run',
                                   '^TestProfileNameBrowserReconciliation$','./internal/api'])
        self.assertEqual(args[2][0],['/usr/bin/python3','scripts/verify-headless.py'])
        self.assertTrue(all(call[2].get('GATEWAY_TEST_DATABASE_URL') == 'isolated' for call in args))
        self.assertIn('separation.cjs',args[4][0][1])

    def test_edited_browser_check_precedes_go_and_is_not_repeated(self):
        @contextlib.contextmanager
        def services(_):yield {}
        with patch.object(checks,'browser_environment',return_value=('node',{})), patch.object(checks,'caddy_runtime',return_value={}), patch.object(checks,'isolated_services',services), patch.object(checks,'project_ui_checks',return_value=[('profile.cjs',),('smoke.cjs','--generations-only')]), patch.object(checks,'edited_ui_checks',return_value={'scripts/ui/profile.cjs'}), patch.object(checks,'execute') as execute, patch.object(Path,'is_file',return_value=True):
            checks.verify('/tmp/test-project', native_only=True)
        commands=[call.args[0] for call in execute.call_args_list]
        self.assertEqual(commands[0],['node',str(Path('/tmp/test-project/scripts/ui/profile.cjs').resolve())])
        self.assertEqual(commands[1],['make','verify-ci','TEST_FLAGS=-count=1'])
        self.assertEqual(commands.count(commands[0]),1)
        self.assertIn(['node',str(Path('/tmp/test-project/scripts/ui/smoke.cjs').resolve()),'--generations-only'],commands)

    def test_failed_edited_browser_check_stops_before_go(self):
        @contextlib.contextmanager
        def services(_):yield {}
        with patch.object(checks,'browser_environment',return_value=('node',{})), patch.object(checks,'caddy_runtime',return_value={}), patch.object(checks,'isolated_services',services), patch.object(checks,'project_ui_checks',return_value=[('profile.cjs',)]), patch.object(checks,'edited_ui_checks',return_value={'scripts/ui/profile.cjs'}), patch.object(checks,'execute',side_effect=checks.CheckFailure('scripts/ui/profile.cjs',1)) as execute, patch.object(Path,'is_file',return_value=True):
            with self.assertRaises(checks.CheckFailure):checks.verify('/tmp/test-project', native_only=True)
        self.assertEqual(execute.call_count,1)

    def test_production_endpoints_are_replaced_and_owned_services_stopped_on_failure(self):
        class RedisProcess:
            stopped=False
            def poll(self):return None
            def terminate(self):self.stopped=True
            def wait(self,timeout=None):return 0
        process=RedisProcess();calls=[]
        def run(args,**kwargs):
            calls.append(args)
            if args[0].endswith('initdb'):
                Path(args[args.index('-D')+1]).mkdir()
            if args[0].endswith('pg_ctl') and args[-1]=='start':
                (Path(args[args.index('-D')+1])/'postmaster.pid').write_text('test')
        with patch.object(checks,'postgres_tools',return_value=Path('/test/postgres')), patch.object(checks.shutil,'which',return_value='/test/redis'), patch.object(checks,'available_port',side_effect=[15432,16379]), patch.object(checks,'wait_port'), patch.object(checks.subprocess,'run',side_effect=run), patch.object(checks.subprocess,'Popen',return_value=process):
            with self.assertRaisesRegex(RuntimeError,'test failure'):
                with checks.isolated_services({'PATH':'/bin','GATEWAY_TEST_DATABASE_URL':'postgres://production/db','GATEWAY_TEST_REDIS_URL':'redis://production/0'}) as env:
                    self.assertIn('@127.0.0.1:15432/',env['GATEWAY_TEST_DATABASE_URL'])
                    self.assertEqual(env['GATEWAY_TEST_REDIS_URL'],'redis://127.0.0.1:16379/0')
                    raise RuntimeError('test failure')
        self.assertTrue(process.stopped)
        self.assertEqual(calls[-1][-1],'stop')
        self.assertFalse(Path(calls[0][calls[0].index('-D')+1]).exists())

    def caddy_archive(self):
        buffer=io.BytesIO()
        with tarfile.open(fileobj=buffer,mode='w:gz') as archive:
            member=tarfile.TarInfo('caddy');member.size=7
            archive.addfile(member,io.BytesIO(b'runtime'))
        return buffer.getvalue()

    def test_official_caddy_checksum_is_checked_before_install(self):
        data=self.caddy_archive()
        digest=hashlib.sha512(data).hexdigest()
        name='caddy_2.11.4_mac_arm64.tar.gz'
        def urlopen(request,timeout):
            self.assertTrue(request.full_url.startswith('https://github.com/caddyserver/caddy/releases/download/v2.11.4/'))
            return io.BytesIO((digest+'  '+name+'\n').encode() if request.full_url.endswith('checksums.txt') else data)
        with tempfile.TemporaryDirectory() as folder, patch.object(checks.platform,'system',return_value='Darwin'), patch.object(checks.platform,'machine',return_value='arm64'), patch.object(checks.urllib.request,'urlopen',side_effect=urlopen):
            checks.download_caddy('2.11.4',Path(folder))
            self.assertEqual((Path(folder)/'caddy').read_bytes(),b'runtime')
            self.assertTrue((Path(folder)/'caddy').stat().st_mode & 0o111)

    def test_bad_caddy_checksum_is_not_installed(self):
        data=self.caddy_archive()
        def urlopen(request,timeout):
            return io.BytesIO(b'wrong  caddy_2.11.4_mac_arm64.tar.gz\n' if request.full_url.endswith('checksums.txt') else data)
        with tempfile.TemporaryDirectory() as folder, patch.object(checks.platform,'system',return_value='Darwin'), patch.object(checks.platform,'machine',return_value='arm64'), patch.object(checks.urllib.request,'urlopen',side_effect=urlopen):
            with self.assertRaisesRegex(RuntimeError,'checksum mismatch'):
                checks.download_caddy('2.11.4',Path(folder))
            self.assertFalse((Path(folder)/'caddy').exists())


class CheckDiagnostics(unittest.TestCase):
    def test_safe_labels_exclude_arguments_and_external_paths(self):
        root=Path('/tmp/test-project')
        self.assertEqual(checks.check_label(['make','verify-ci','TEST_FLAGS=-count=1'],root),'make verify-ci')
        self.assertEqual(checks.check_label(['node',str(root/'scripts/ui/branding.cjs'),'secret'],root),'scripts/ui/branding.cjs')
        self.assertEqual(checks.check_label(['python3','scripts/verify-headless.py'],root),'scripts/verify-headless.py')
        for command in [['node','/tmp/secret.cjs'],['make','secret'],['node','https://secret.invalid/token']]:
            self.assertEqual(checks.check_label(command,root),'release check')

    def test_execution_failure_contains_only_safe_check_name(self):
        output=io.StringIO()
        command=['node','scripts/ui/branding.cjs','secret-token']
        with patch.object(checks.subprocess,'run',side_effect=subprocess.CalledProcessError(2,command)), contextlib.redirect_stdout(output), self.assertRaises(checks.CheckFailure) as caught:
            checks.execute(command,Path('/tmp/project'),{})
        self.assertEqual(caught.exception.exit_code,2)
        self.assertIn('scripts/ui/branding.cjs',str(caught.exception))
        self.assertNotIn('secret-token',str(caught.exception)+output.getvalue())

    def test_failed_check_writes_structured_result(self):
        with tempfile.TemporaryDirectory() as directory:
            target=Path(directory)/'result.json'
            with patch.object(checks,'verify',side_effect=checks.CheckFailure('scripts/ui/branding.cjs',1)), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(checks.main(['--project',directory,'--failure-file',str(target)]),1)
            self.assertEqual(json.loads(target.read_text()),{'check':'scripts/ui/branding.cjs','exit_code':1})

    def test_success_does_not_create_failure_result(self):
        with tempfile.TemporaryDirectory() as directory:
            target=Path(directory)/'result.json'
            with patch.object(checks,'verify'):
                self.assertEqual(checks.main(['--project',directory,'--failure-file',str(target)]),0)
            self.assertFalse(target.exists())

    def status_for(self, text, running=False):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);(root/'logs').mkdir();(root/'logs/window-v2-run.log').write_text(text)
            if running:(root/'release.lock').mkdir()
            with patch.object(window,'ROOT',root):
                return window.Runner().status()

    def test_window_shows_failed_check_and_publication_boundary(self):
        status=self.status_for('[2/6] Checks\nSTOPPED: Release check failed: scripts/ui/branding.cjs (exit 1).\nDeployment was not started.\n')
        self.assertEqual(status['state'],'failed')
        self.assertIn('scripts/ui/branding.cjs',status['detail'])
        self.assertIn('Deployment was not started',status['detail'])

    def test_window_names_current_check_without_shadowing_later_stages(self):
        text='[2/6] Checks\nCHECK: make verify-ci\nCHECK PASSED: make verify-ci (10.0s)\nCHECK: scripts/ui/branding.cjs\n'
        status=self.status_for(text,True)
        self.assertIn('scripts/ui/branding.cjs',status['detail'])
        self.assertNotIn('make verify-ci',status['detail'])
        status=self.status_for(text+'[3/6] Verify remote\n',True)
        self.assertNotIn('branding.cjs',status['detail'])
        self.assertIn('3 of 6',status['detail'])


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


if __name__ == '__main__':
    unittest.main()
