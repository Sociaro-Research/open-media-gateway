"""CI orchestration tests without Docker, network or production access."""
import importlib.util
import contextlib
from pathlib import Path
import tempfile
import subprocess
import unittest
from unittest.mock import patch, MagicMock

SOURCE = Path(__file__).resolve().parents[1]

def load(name):
    spec=importlib.util.spec_from_file_location(name,SOURCE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

ci=load('release_ci')
checks=load('release_checks')
window=load('release_ui')
installer=load('install')

class LocalCI(unittest.TestCase):
    def test_variants_preserve_engine_chooser_and_screenshot_environment(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);workflow=root/'.github/workflows/ci.yml';workflow.parent.mkdir(parents=True)
            workflow.write_text('UI_BROWSER_ENGINE=webkit node scripts/ui/references.cjs\nUI_SCREENSHOT_DIR=shots UI_VOICE_UPLOAD=chooser node scripts/ui/reference-intent.cjs\nnode scripts/ui/unsafe.cjs; echo secret\n')
            selected=checks.project_ui_checks(root)
            self.assertIn(('references.cjs','UI_BROWSER_ENGINE=webkit'),selected)
            self.assertIn(('reference-intent.cjs','UI_SCREENSHOT_DIR=shots','UI_VOICE_UPLOAD=chooser'),selected)
            self.assertFalse(any(check[0]=='unsafe.cjs' for check in selected))

    def test_remote_docker_is_refused_before_any_check(self):
        with patch.dict(ci.os.environ,{'DOCKER_HOST':'tcp://production:2375'},clear=True),patch.object(ci.subprocess,'run'),patch.object(ci.subprocess,'check_output',return_value='tcp://production:2375'),patch.object(ci,'print'):
            execute=MagicMock()
            with self.assertRaisesRegex(RuntimeError,'remote Docker'):ci.verify(Path('/tmp/project'),execute,{},'node')
            execute.assert_not_called()

    def test_failed_image_scan_stops_install_and_removes_only_owned_images(self):
        commands=[]
        def execute(args,*_):
            commands.append(args)
            if len(args)>1 and args[1]=='image':raise RuntimeError('scanner failed')
        with patch.dict(ci.os.environ,{},clear=True),patch.object(ci.subprocess,'run') as run,patch.object(ci.subprocess,'check_output',return_value='unix:///tmp/docker.sock'):
            with self.assertRaisesRegex(RuntimeError,'scanner failed'):ci.verify(Path('/tmp/project'),execute,{},'node')
        self.assertFalse(any('scripts/verify-release.py' in command for command in commands))
        removals=[call.args[0] for call in run.call_args_list if call.args[0][:3]==['docker','image','rm']]
        self.assertEqual(len(removals),1)
        self.assertTrue(all(tag.startswith('gateway-local-ci-') for tag in removals[0][3:]))

    def test_full_ci_plan_checks_four_images_and_isolated_install(self):
        execute=MagicMock()
        with patch.dict(ci.os.environ,{},clear=True),patch.object(ci.subprocess,'run') as run,patch.object(ci.subprocess,'check_output',side_effect=['unix:///tmp/docker.sock','70']):
            ci.verify(Path('/tmp/project'),execute,{'COMPOSE_PROJECT_NAME':'existing-installation'},'node')
        commands=[call.args[0] for call in execute.call_args_list]
        builds=[command for command in commands if command[:2]==['docker','build']]
        self.assertEqual(len(builds),4)
        install=next(command for command in commands if 'scripts/verify-release.py' in command)
        for service in ('gateway','ui','postgres','redis'):self.assertIn('--'+service,install)
        grafana=next(command for command in commands if command[0]=='docker' and 'up' in command)
        self.assertTrue(grafana[grafana.index('-p')+1].startswith('gateway-local-ci-'))
        compose_calls=[call for call in execute.call_args_list if call.args[0][:2]==['docker','compose']]
        self.assertTrue(all('COMPOSE_PROJECT_NAME' not in call.args[2] for call in compose_calls))
        cleanups=[call.args[0] for call in run.call_args_list if 'down' in call.args[0]]
        self.assertEqual(len(cleanups),1)
        self.assertEqual(cleanups[0][-2:],['down','--volumes'])
        self.assertTrue(cleanups[0][cleanups[0].index('-p')+1].startswith('gateway-local-ci-'))

    def test_webkit_variants_do_not_inherit_chrome_executable(self):
        @contextlib.contextmanager
        def services(env):yield env
        with tempfile.TemporaryDirectory() as tmp,patch.object(checks,'browser_environment',return_value=('node',{'UI_BROWSER_PATH':'/fixture/chrome'})),patch.object(checks,'caddy_runtime',side_effect=lambda project,env:env),patch.object(checks,'isolated_services',services),patch.object(checks,'project_ui_checks',return_value=[('references.cjs','UI_BROWSER_ENGINE=webkit'),('references.cjs',)]),patch.object(checks,'execute') as execute,patch.object(Path,'is_file',return_value=True):
            checks.verify(tmp,native_only=True)
        browser_calls=[call for call in execute.call_args_list if call.args[0][0]=='node']
        self.assertNotIn('UI_BROWSER_PATH',browser_calls[0].args[2])
        self.assertEqual(browser_calls[0].args[2]['UI_BROWSER_ENGINE'],'webkit')
        self.assertEqual(browser_calls[1].args[2]['UI_BROWSER_PATH'],'/fixture/chrome')

    def test_go_profile_workflows_use_selected_node_and_owned_services_in_both_engines(self):
        inherited = {'UI_BROWSER_PATH': '/fixture/chrome', 'UI_BROWSER_ENGINE': 'webkit',
                     'GATEWAY_PROFILE_BROWSER_NODE': '/wrong/contact-node',
                     'GATEWAY_PROFILE_NAME_BROWSER_NODE': '/wrong/name-node'}
        original = dict(inherited)
        active = []
        commands = []

        @contextlib.contextmanager
        def services(env):
            active.append(True)
            try:
                yield dict(env, GATEWAY_TEST_DATABASE_URL='postgres://owned-loopback/fixture',
                           GATEWAY_TEST_REDIS_URL='redis://owned-loopback/0')
            finally:
                active.pop()

        def execute(args, project, env):
            self.assertTrue(active, 'All profile checks must stay inside owned service lifetime.')
            commands.append((args, dict(env)))

        with tempfile.TemporaryDirectory() as tmp, patch.object(checks, 'browser_environment', return_value=('/fixture/selected-node', inherited)), patch.object(checks, 'caddy_runtime', side_effect=lambda project, env: env), patch.object(checks, 'isolated_services', services), patch.object(checks, 'project_ui_checks', return_value=[]), patch.object(checks, 'edited_ui_checks', return_value=set()), patch.object(checks, 'execute', side_effect=execute):
            checks.verify(tmp, native_only=True)

        go_checks = [(args, env) for args, env in commands if args[0] in ('make', 'go') and args[:2] != ['make', 'build-ui']]
        self.assertEqual([args for args, _ in go_checks], [
            ['make', 'verify-ci', 'TEST_FLAGS=-count=1'],
            ['go', 'test', '-race', '-count=1', '-run',
             '^TestProfileNameBrowserReconciliation$', './internal/api'],
        ])
        for _, env in go_checks:
            self.assertEqual(env['GATEWAY_PROFILE_BROWSER_NODE'], '/fixture/selected-node')
            self.assertEqual(env['GATEWAY_PROFILE_NAME_BROWSER_NODE'], '/fixture/selected-node')
            self.assertEqual(env['GATEWAY_TEST_DATABASE_URL'], 'postgres://owned-loopback/fixture')
            self.assertEqual(env['GATEWAY_TEST_REDIS_URL'], 'redis://owned-loopback/0')
        self.assertEqual(go_checks[0][1]['UI_BROWSER_ENGINE'], 'chromium')
        self.assertEqual(go_checks[0][1]['UI_BROWSER_PATH'], '/fixture/chrome')
        self.assertEqual(go_checks[1][1]['UI_BROWSER_ENGINE'], 'webkit')
        self.assertNotIn('UI_BROWSER_PATH', go_checks[1][1])
        self.assertEqual({key: inherited[key] for key in original}, original)
        self.assertFalse(active)

    def test_failed_webkit_profile_check_stops_later_checks_and_releases_owned_services(self):
        closed = []
        commands = []

        @contextlib.contextmanager
        def services(env):
            try:
                yield dict(env, GATEWAY_TEST_DATABASE_URL='postgres://owned-loopback/fixture',
                           GATEWAY_TEST_REDIS_URL='redis://owned-loopback/0')
            finally:
                closed.append(True)

        def execute(args, project, env):
            commands.append(args)
            if args[0] == 'go':
                raise checks.CheckFailure('release check', 1)

        with tempfile.TemporaryDirectory() as tmp, patch.object(checks, 'browser_environment', return_value=('/fixture/selected-node', {})), patch.object(checks, 'caddy_runtime', side_effect=lambda project, env: env), patch.object(checks, 'isolated_services', services), patch.object(checks, 'project_ui_checks', return_value=[]), patch.object(checks, 'edited_ui_checks', return_value=set()), patch.object(checks, 'execute', side_effect=execute):
            with self.assertRaises(checks.CheckFailure):
                checks.verify(tmp, native_only=True)
        self.assertEqual(len(commands), 2)
        self.assertEqual(commands[0], ['make', 'verify-ci', 'TEST_FLAGS=-count=1'])
        self.assertEqual(commands[1][0], 'go')
        self.assertEqual(closed, [True])

    def test_extended_only_skips_native_suites_and_services(self):
        module=MagicMock()
        with tempfile.TemporaryDirectory() as tmp,patch.object(checks.importlib.util,'module_from_spec',return_value=module),patch.object(checks.importlib.util,'spec_from_file_location') as spec,patch.object(checks,'browser_environment',return_value=('node',{})),patch.object(checks,'caddy_runtime',side_effect=lambda project,env:env),patch.object(checks,'isolated_services') as services,patch.object(checks,'project_ui_checks') as suites,patch.object(checks,'execute') as execute:
            checks.verify(tmp,extended_only=True)
            module.preflight.assert_called_once()
            module.verify.assert_called_once()
            services.assert_not_called()
            suites.assert_not_called()
            execute.assert_not_called()

    def test_timed_out_check_is_reported_as_failure(self):
        with patch.object(checks.subprocess,'run',side_effect=subprocess.TimeoutExpired(['node','private-argument'],600)):
            with self.assertRaises(checks.CheckFailure) as caught:
                checks.execute(['node','scripts/ui/smoke.cjs'],Path('/tmp/project'),{})
        self.assertEqual(caught.exception.exit_code,124)
        self.assertNotIn('private-argument',str(caught.exception))

    def test_finder_locale_cannot_break_owned_postgres_initialization(self):
        class Redis:
            def poll(self):return None
            def terminate(self):pass
            def wait(self,timeout=None):return 0
        calls=[]
        def run(args,**kwargs):
            calls.append((args,kwargs))
            if args[0].endswith('initdb'):Path(args[args.index('-D')+1]).mkdir()
        with patch.object(checks,'postgres_tools',return_value=Path('/fixture/postgres')),patch.object(checks.shutil,'which',return_value='/fixture/redis'),patch.object(checks,'available_port',side_effect=[15432,16379]),patch.object(checks,'wait_port'),patch.object(checks.subprocess,'run',side_effect=run),patch.object(checks.subprocess,'Popen',return_value=Redis()):
            inherited={'PATH':'/bin','LANG':'invalid','LC_ALL':'invalid','LC_CTYPE':'UTF-8'}
            with checks.isolated_services(inherited) as env:
                self.assertEqual(env['LC_ALL'],'C')
                self.assertEqual(env['LANG'],'C')
            self.assertEqual(inherited['LC_ALL'],'invalid')
        self.assertIn('--encoding=UTF8',calls[0][0])
        self.assertIn('--no-locale',calls[0][0])
        self.assertEqual(calls[0][1]['env']['LC_ALL'],'C')

    def test_local_check_failure_does_not_claim_publication(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(window,'ROOT',Path(tmp)):
            root=Path(tmp);(root/'logs').mkdir()
            (root/'logs/window-v2-fixture.log').write_text('[2/6] Local checks (no server access)\nSTOPPED: fixture error\n')
            status=window.Runner().status()
            self.assertEqual(status['state'],'failed')
            self.assertEqual(status['title'],'Checks stopped')

    def test_local_check_running_title_before_first_log_output(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(window,'ROOT',Path(tmp)):
            root=Path(tmp);(root/'release.lock').mkdir()
            runner=window.Runner();runner.mode='native'
            self.assertEqual(runner.status()['title'],'Checks running')

    def test_buttons_run_only_the_selected_mode(self):
        for mode,flags in [('native',['--check-only','--native-only']),('check',['--check-only']),('deploy',[]),('native-deploy',['--native-only']),('deploy-only',['--deploy-only']),('extended',['--check-only','--extended-only']),('extended-deploy',['--extended-only'])]:
            with tempfile.TemporaryDirectory() as tmp,patch.object(window,'ROOT',Path(tmp)),patch.object(window.subprocess,'Popen') as popen,patch.object(window.threading,'Thread'):
                (Path(tmp)/'logs').mkdir();runner=window.Runner();runner.start(mode)
                command=popen.call_args.args[0]
                self.assertEqual(command[3:],flags)
                self.assertNotIn('message',command)

    def test_unknown_window_mode_never_starts_process(self):
        with patch.object(window.subprocess,'Popen') as popen:
            with self.assertRaises(ValueError):window.Runner().start('push')
            popen.assert_not_called()

class Installation(unittest.TestCase):
    def test_update_preserves_keys_logs_config_and_previous_source(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);destination=root/'Release Tool';source=destination/'Source'
            (source/'logs').mkdir(parents=True);(source/'release.py').write_text('previous runner')
            (source/'project-path.txt').write_text('/old/checkout')
            (source/'logs/previous.log').write_text('old evidence')
            (destination/'ssh').mkdir();(destination/'ssh/key').write_text('synthetic fixture')
            with patch.object(installer.subprocess,'run') as run:
                installer.install(destination,root/'checkout')
            self.assertEqual((destination/'ssh/key').read_text(),'synthetic fixture')
            self.assertEqual((source/'logs/previous.log').read_text(),'old evidence')
            self.assertEqual((source/'project-path.txt').read_text(),str((root/'checkout').resolve())+'\n')
            backups=list((source/'backups').glob('local-ci-update-*'))
            self.assertEqual(len(backups),1)
            self.assertEqual((backups[0]/'release.py').read_text(),'previous runner')
            self.assertEqual((backups[0]/'project-path.txt').read_text(),'/old/checkout')
            self.assertEqual(run.call_count,2)
            self.assertTrue((source/'tests/test_ci.py').is_file())
            self.assertTrue((source/'install.py').is_file())

    def test_active_release_blocks_install_before_any_write(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);(root/'Source/release.lock').mkdir(parents=True)
            with self.assertRaisesRegex(RuntimeError,'active'):installer.install(root,root/'checkout')
            self.assertFalse((root/'Source/backups').exists())

if __name__=='__main__':unittest.main()
