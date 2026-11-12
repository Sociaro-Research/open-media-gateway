"""Offline contracts for exact UI coverage, serial execution and built-runtime transport."""
from collections import Counter
import copy
import contextlib
import hashlib
import importlib.util
import io
import json
import os
import re
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch, Mock

ROOT = Path(__file__).resolve().parents[2]


def load(name, file):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts/ci' / file)
    module = importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module


runner = load('ui_runner', 'run-ui-job.py')
runtime = load('ui_runtime', 'ui-runtime.py')


class Manifest(unittest.TestCase):
    def test_exact_original_67_variants_and_three_partitions(self):
        self.assertEqual(hashlib.sha256((ROOT/'scripts/tests/fixtures/ci-ui-baseline.txt').read_bytes()).hexdigest(), 'bc74079cbad3138bc73e08846fd49934abf4ecdcbfb82b00e2732d902988c35a')
        commands = runner.load_commands()
        self.assertEqual(Counter(c['job'] for c in commands), {'ui-performance': 5, 'ui-media': 30, 'ui-console': 32})
        self.assertEqual(sum('scripts/ui/performance.cjs' in c['argv'] for c in commands), 2)
        self.assertEqual(sum(c['env'].get('UI_VOICE_UPLOAD') == 'chooser' for c in commands), 2)
        self.assertEqual(sum(c['env'].get('UI_MEMBER_ROLE_ONLY') == '1' for c in commands), 2)
        self.assertEqual(sum(c['env'].get('UI_BROWSER') == 'webkit' for c in commands), 2)
        self.assertIn(['node', '--test', 'scripts/ui/auto-update-test.cjs'], [c['argv'] for c in commands])
        self.assertIn(['node', 'scripts/ui/smoke.cjs', '--generations-only'], [c['argv'] for c in commands])

    def test_coverage_rejects_removed_duplicate_or_changed_variant(self):
        manifest = json.loads((ROOT / 'scripts/ci/ui-commands.json').read_text())
        changes = [lambda m: m['commands'].pop(),
                   lambda m: m['commands'].__setitem__(0, m['commands'][1]),
                   lambda m: m['commands'][0]['env'].update(UI_BROWSER_ENGINE='unexpected')]
        for change in changes:
            with tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp);(root/'scripts/ci').mkdir(parents=True);(root/'scripts/tests/fixtures').mkdir(parents=True)
                (root/'scripts/tests/fixtures/ci-ui-baseline.txt').write_bytes((ROOT/'scripts/tests/fixtures/ci-ui-baseline.txt').read_bytes())
                mutated = copy.deepcopy(manifest);change(mutated)
                (root/'scripts/ci/ui-commands.json').write_text(json.dumps(mutated))
                with self.assertRaises(ValueError): runner.load_commands(root)

    def test_performance_cannot_move_or_merge_with_another_runner(self):
        manifest = json.loads((ROOT/'scripts/ci/ui-commands.json').read_text())
        target = next(c for c in manifest['commands'] if 'scripts/ui/performance.cjs' in c['argv'])
        target['job'] = 'ui-console'
        original = Path.read_text
        def read(path, *args, **kwargs):
            if path == ROOT/'scripts/ci/ui-commands.json': return json.dumps(manifest)
            return original(path, *args, **kwargs)
        with patch.object(Path, 'read_text', read), self.assertRaisesRegex(ValueError, 'Performance'): runner.load_commands()


class Execution(unittest.TestCase):
    def test_timeout_cleanup_escalates_only_the_owned_process_group(self):
        with patch.object(os, 'killpg') as kill, patch.object(runner.time, 'monotonic', side_effect=[0, 6]):
            runner.terminate_group(123)
        self.assertEqual(kill.call_args_list, [unittest.mock.call(123, runner.signal.SIGTERM),
                                               unittest.mock.call(123, runner.signal.SIGKILL)])
        with patch.object(os, 'killpg', side_effect=ProcessLookupError) as kill:
            runner.terminate_group(123)
        self.assertEqual(kill.call_count, 1)

    def test_failed_partition_retains_not_run_inventory_without_starting_later_commands(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(runner.signal, 'signal'), patch.object(runner, 'execute', return_value={'status':'failed','exit':7}) as execute:
            with patch('sys.argv', ['runner','--job','ui-performance','--output',tmp]):
                self.assertEqual(runner.main(), 7)
            self.assertEqual(execute.call_count, 1)
            report=json.loads((Path(tmp)/'ui-performance/results.json').read_text())
            self.assertEqual([c['status'] for c in report['commands']], ['failed']+['not_run']*4)

    def test_records_exit_elapsed_variants_and_unique_evidence(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(runner, 'terminate_group') as cleanup:
            process = Mock(pid=123);process.wait.return_value = 0
            command = {'id': 'example', 'job': 'ui-console', 'argv': ['node','scripts/ui/example.cjs'],
                       'env': {'UI_BROWSER': 'webkit', 'UI_VOICE_UPLOAD': 'chooser'}}
            with patch.object(subprocess, 'Popen', return_value=process) as popen:
                with contextlib.redirect_stdout(io.StringIO()):
                    one = runner.execute(command, Path(tmp)/'one', root=Path(tmp))
                    two = runner.execute(command, Path(tmp)/'two', root=Path(tmp))
            self.assertEqual(one['exit'], 0);self.assertEqual(one['status'], 'passed')
            self.assertGreaterEqual(one['elapsed_seconds'], 0)
            self.assertIn('started_at', one);self.assertIn('completed_at', one)
            self.assertNotEqual(one['outputs'], two['outputs'])
            self.assertEqual(popen.call_args.kwargs['env']['UI_BROWSER'], 'webkit')
            self.assertEqual(popen.call_args.kwargs['env']['UI_VOICE_UPLOAD'], 'chooser')
            self.assertTrue(popen.call_args.kwargs['start_new_session'])
            self.assertEqual(cleanup.call_count, 2)

    def test_failure_timeout_and_cancellation_never_pass_and_cleanup_group(self):
        for outcome, expected in [(7, 7), (subprocess.TimeoutExpired('node', 1800), 124), (KeyboardInterrupt(), 130)]:
            with self.subTest(expected=expected), tempfile.TemporaryDirectory() as tmp:
                process = Mock(pid=123)
                process.wait.side_effect = [outcome, 0] if isinstance(outcome, BaseException) else [outcome, 0]
                command = {'id': 'example', 'job': 'ui-console', 'argv': ['node','scripts/ui/example.cjs'], 'env': {}}
                with patch.object(subprocess, 'Popen', return_value=process), patch.object(runner, 'terminate_group') as cleanup:
                    with contextlib.redirect_stdout(io.StringIO()):
                        result = runner.execute(command, Path(tmp)/'case', root=Path(tmp))
                self.assertEqual(result['exit'], expected);self.assertNotEqual(result['status'], 'passed')
                cleanup.assert_called_once_with(123)
                self.assertEqual(json.loads((Path(tmp)/'case/command.json').read_text())['exit'], expected)


class BuiltRuntime(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup);self.base = Path(self.tmp.name)
        self.stage = self.base/'stage';self.stage.mkdir();self.root = self.base/'checkout';self.root.mkdir()
        self.sha = 'a'*40;self.image = 'sha256:'+'b'*64
        for name, data in {'caddy':b'not executed', 'Caddyfile':b':8080 {}', 'gateway-ui.tar.gz':b'owned UI archive',
                           'public/ui/index.html':b'<html>fixture</html>'}.items():
            target=self.stage/name;target.parent.mkdir(parents=True, exist_ok=True);target.write_bytes(data)
        (self.root/'web/public/ui').mkdir(parents=True)
        (self.root/'web/public/ui/index.html').write_bytes((self.stage/'public/ui/index.html').read_bytes())
        (self.root/'web/Caddyfile').write_bytes((self.stage/'Caddyfile').read_bytes())
        self.bundle = self.base/'runtime.tar.gz'
        self.digest = runtime.pack(self.stage, self.bundle, self.sha, self.image)

    def restore(self, **kwargs):
        options=dict(bundle=self.bundle, expected_sha=self.digest, source_sha=self.sha, image_id=self.image,
                     root=self.root, runtime=self.base/'restored');options.update(kwargs);return runtime.restore(**options)

    def test_roundtrip_preserves_exact_binary_archive_and_source_binding(self):
        manifest=self.restore()
        self.assertEqual(manifest['image_id'], self.image)
        self.assertEqual((self.base/'restored/caddy').read_bytes(), b'not executed')
        self.assertEqual((self.base/'restored/caddy').stat().st_mode&0o777, 0o755)
        self.assertEqual((self.root/'dist/gateway-ui.tar.gz').read_bytes(), b'owned UI archive')

    def test_wrong_transport_source_image_and_checkout_are_refused_before_write(self):
        for options in [{'expected_sha':'c'*64}, {'source_sha':'c'*40}, {'image_id':'sha256:'+'c'*64}]:
            with self.assertRaises(ValueError): self.restore(**options)
            self.assertFalse((self.base/'restored').exists())
        (self.root/'web/public/ui/index.html').write_bytes(b'changed source')
        with self.assertRaises(ValueError): self.restore()
        self.assertFalse((self.base/'restored').exists())

    def test_internal_digest_inventory_and_unsafe_members_fail_closed(self):
        with tarfile.open(self.bundle, 'r:gz') as archive:
            original = [(member, archive.extractfile(member).read()) for member in archive]
        for mutation in ['content', 'extra', 'traversal', 'link', 'duplicate']:
            bad = self.base/(mutation+'.tar.gz')
            with tarfile.open(bad,'w:gz') as archive:
                for member, data in original:
                    if mutation=='content' and member.name=='caddy': data=b'tampered';member=copy.copy(member);member.size=len(data)
                    archive.addfile(member,io.BytesIO(data))
                if mutation!='content':
                    member=tarfile.TarInfo({'extra':'extra','traversal':'../escape','link':'linked','duplicate':'caddy'}[mutation]);member.size=1
                    if mutation=='link': member.type=tarfile.SYMTYPE;member.linkname='/tmp/escape'
                    archive.addfile(member,io.BytesIO(b'x') if member.isfile()else None)
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                self.restore(bundle=bad,expected_sha=hashlib.sha256(bad.read_bytes()).hexdigest())
            self.assertFalse((self.base/'restored').exists())


class Workflow(unittest.TestCase):
    def test_three_required_isolated_jobs_keep_artifacts_tools_and_real_grafana(self):
        ci=(ROOT/'.github/workflows/ci.yml').read_text();action=(ROOT/'.github/actions/ui-checks/action.yml').read_text()
        for job in runner.JOBS:
            self.assertIn(f'  {job}:\n',ci)
            block=re.search(rf'^  {job}:\n(.*?)(?=^  [a-z][a-z-]*:\n|\Z)',ci,re.M|re.S)[1]
            self.assertIn('needs: [prior-ci, container]',block)
            self.assertIn(f'partition: {job}',block)
        self.assertNotIn('      - name: Verify independent UI and current workflows',ci)
        self.assertIn('make build-ui',ci)
        self.assertIn('Verify real Grafana provisioning and browser dashboard',ci)
        self.assertIn('Stop isolated monitoring checks',ci)
        for dependency in ['actions/setup-go@v7','node-version: \'24\'', 'playwright@1.62.1',
                           '--with-deps chromium chrome webkit','template-video-fixtures.py',
                           'needs.container.outputs.ui_runtime_sha256']:
            self.assertIn(dependency,action+ci)
        self.assertIn('if: always()',action)
        self.assertIn('cp .local/ci-ui-runtime/runtime.json ui-results/runtime.json', action)
        self.assertIn('python3 scripts/check-ui-updates.py',action)


if __name__=='__main__': unittest.main()
