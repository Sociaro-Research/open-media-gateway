"""Offline provenance, freshness, failure and workflow contracts; no GitHub/network/services."""
import base64
import copy
import contextlib
import datetime as dt
import importlib.util
import json
import os
import re
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import urllib.error

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('prior_ci', ROOT / 'scripts/ci/validate-prior-run.py')
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
SHA = 'a' * 40
NOW = dt.datetime(2026, 11, 12, 12, tzinfo=dt.timezone.utc)


class Acceptance(unittest.TestCase):
    def setUp(self):
        self.definition = (ROOT / MODULE.WORKFLOW).read_bytes()
        self.run = {'id': 123, 'path': MODULE.WORKFLOW, 'event': 'workflow_dispatch',
                    'head_branch': 'main', 'head_sha': SHA, 'status': 'completed',
                    'conclusion': 'success', 'repository': {'id': 15, 'full_name': MODULE.REPOSITORY},
                    'head_repository': {'id': 15, 'full_name': MODULE.REPOSITORY}, 'workflow_id': 42,
                    'run_attempt': 1, 'pull_requests': [], 'created_at': '2026-11-12T10:59:59Z',
                    'run_started_at': '2026-11-12T11:00:00Z', 'updated_at': '2026-11-12T11:30:00Z'}
        self.jobs = []
        for i, (name, steps) in enumerate(MODULE.REQUIRED.items(), 1):
            self.jobs.append({'id': i, 'run_id': 123, 'run_attempt': 1, 'head_sha': SHA,
                              'name': name, 'status': 'completed', 'conclusion': 'success',
                              'started_at': '2026-11-12T11:00:01Z', 'completed_at': '2026-11-12T11:29:59Z',
                              'steps': [{'name': s, 'status': 'completed', 'conclusion': 'success'} for s in sorted(steps)]})
        self.workflow = {'id': 42, 'path': MODULE.WORKFLOW}
        self.contents = {'encoding': 'base64', 'content': base64.b64encode(self.definition).decode()}
        self.calls = []

    def read(self, path):
        self.calls.append(path)
        if path.endswith('/actions/runs/123'):
            return copy.deepcopy(self.run)
        if path.endswith('/actions/workflows/ci.yml'):
            return self.workflow
        if '/contents/' in path:
            self.assertTrue(path.endswith('?ref=' + SHA))
            return self.contents
        self.assertIn('/actions/runs/123/attempts/1/jobs?per_page=100&page=', path)
        page = int(path.rsplit('=', 1)[1])
        return {'total_count': len(self.jobs), 'jobs': self.jobs[(page-1)*100:page*100]}

    def validate(self, **kwargs):
        options = dict(run_id='123', expected_sha=SHA, current_run_id='456',
                       repository=MODULE.REPOSITORY, workflow_bytes=self.definition, read=self.read, now=NOW)
        options.update(kwargs)
        return MODULE.validate(**options)

    def test_accepts_exact_completed_scope(self):
        receipt = self.validate()
        self.assertTrue(receipt['reuse'])
        self.assertEqual(receipt['source_sha'], SHA)
        self.assertEqual(len(receipt['jobs']), 5)
        self.assertEqual(receipt['max_age_seconds'], 21600)
        self.assertEqual(sum(p.endswith('/actions/runs/123') for p in self.calls), 2)

    def test_rejects_wrong_sha_workflow_ref_event_and_outcome(self):
        for key, values in {'head_sha': ['b'*40], 'path': ['.github/workflows/other.yml'],
                            'event': ['pull_request', 'pull_request_target', 'push', 'schedule'],
                            'head_branch': ['feature'], 'status': ['queued', 'in_progress'],
                            'conclusion': ['failure', 'cancelled', 'skipped', None],
                            'id': [True, 124], 'run_attempt': [None, True, 0]}.items():
            for value in values:
                with self.subTest(key=key, value=value):
                    original = self.run[key]
                    self.run[key] = value
                    with self.assertRaises(MODULE.ReuseError):
                        self.validate()
                    self.run[key] = original

    def test_rejects_pr_context_fork_and_repository_id_mismatch(self):
        self.run['pull_requests'] = [{'number': 1}]
        with self.assertRaises(MODULE.ReuseError): self.validate()
        self.run['pull_requests'] = []
        for key in ['repository', 'head_repository']:
            original = self.run[key]
            for repo in [{'id': 15, 'full_name': 'someone/fork'}, {'id': 99, 'full_name': MODULE.REPOSITORY}, None]:
                self.run[key] = repo
                with self.assertRaises(MODULE.ReuseError): self.validate()
            self.run[key] = original

    def test_own_run_invalid_ids_and_untrusted_current_context(self):
        for opts in [{'current_run_id': '123'}, {'current_run_id': ''}, {'run_id': '0'},
                     {'run_id': '123\n'}, {'run_id': '../123'}, {'run_id': '1;true'},
                     {'repository': 'someone/fork'}, {'expected_sha': 'main'}]:
            with self.subTest(opts=opts), self.assertRaises(MODULE.ReuseError): self.validate(**opts)

    def test_binds_workflow_identity_and_exact_definition(self):
        self.workflow['id'] = 99
        with self.assertRaises(MODULE.ReuseError): self.validate()
        self.workflow['id'] = 42
        for data in [{'encoding': 'base64', 'content': base64.b64encode(b'other workflow').decode()},
                     {'encoding': 'base64', 'content': None}, {'encoding': 'base64', 'content': '%%%'}]:
            self.contents = data
            with self.assertRaises(MODULE.ReuseError): self.validate()

    def test_stale_future_and_malformed_times(self):
        for key in ['created_at', 'run_started_at', 'updated_at']:
            original = self.run[key]
            for bad in [None, '', 'yesterday', '2026-13-12T11:00:00Z', '2026-11-12T11:00:00', '2026-11-13T11:00:00Z']:
                self.run[key] = bad
                with self.subTest(key=key, bad=bad), self.assertRaises(MODULE.ReuseError): self.validate()
            self.run[key] = original
        self.run.update(created_at='2026-11-12T05:59:58Z', run_started_at='2026-11-12T05:59:59Z')
        with self.assertRaises(MODULE.ReuseError): self.validate()
        self.run['run_started_at'] = '2026-11-12T06:00:00Z'
        self.assertTrue(self.validate()['reuse'])  # Explicit inclusive six-hour boundary.

    def test_failed_skipped_or_incomplete_job_and_step_rejected(self):
        for job_key in ['status', 'conclusion']:
            for value in ['failure', 'cancelled', 'skipped', None]:
                original = self.jobs[0][job_key]; self.jobs[0][job_key] = value
                with self.assertRaises(MODULE.ReuseError): self.validate()
                self.jobs[0][job_key] = original
        for key in ['status', 'conclusion']:
            original = self.jobs[1]['steps'][0][key]
            for bad in ['skipped', 'failure', 'cancelled', None]:
                self.jobs[1]['steps'][0][key] = bad
                with self.assertRaises(MODULE.ReuseError): self.validate()
            self.jobs[1]['steps'][0][key] = original

    def test_all_required_jobs_steps_and_job_provenance(self):
        for job in self.jobs:
            for key, value in [('run_id', 789), ('run_attempt', 2), ('head_sha', 'b'*40),
                               ('started_at', '2026-11-12T10:00:00Z'), ('completed_at', None)]:
                original = job[key];job[key] = value
                with self.assertRaises(MODULE.ReuseError): self.validate()
                job[key] = original
            saved = job['steps'];job['steps'] = []
            with self.assertRaises(MODULE.ReuseError): self.validate()
            job['steps'] = saved[1:]
            with self.assertRaises(MODULE.ReuseError): self.validate()
            job['steps'] = saved
        self.jobs.pop()
        with self.assertRaises(MODULE.ReuseError): self.validate()

    def test_only_explicit_conditional_security_steps_may_skip(self):
        security = next(j for j in self.jobs if j['name'] == 'Secret history and dependency security')
        security['steps'].extend({'name': s, 'status': 'completed', 'conclusion': 'skipped'} for s in MODULE.OPTIONAL_STEPS)
        self.assertTrue(self.validate()['reuse'])
        self.jobs[1]['steps'].append({'name': 'extra acceptance', 'status': 'completed', 'conclusion': 'skipped'})
        with self.assertRaises(MODULE.ReuseError): self.validate()

    def test_duplicate_jobs_steps_and_empty_inventory(self):
        self.jobs.append(copy.deepcopy(self.jobs[0]))
        with self.assertRaises(MODULE.ReuseError): self.validate()
        self.jobs.pop();self.jobs[0]['steps'].append(copy.deepcopy(self.jobs[0]['steps'][0]))
        with self.assertRaises(MODULE.ReuseError): self.validate()
        self.jobs = []
        with self.assertRaises(MODULE.ReuseError): self.validate()

    def test_pagination_fully_consumed_and_partial_inventory_refused(self):
        while len(self.jobs) < 101:
            job = copy.deepcopy(self.jobs[0]);job['id'] = len(self.jobs)+1;job['name'] = 'extra '+str(job['id']);self.jobs.append(job)
        self.assertTrue(self.validate()['reuse'])
        self.assertTrue(any(p.endswith('page=2') for p in self.calls))
        original = self.read
        for response in [{'total_count': 101, 'jobs': self.jobs[:4]}, {'total_count': 101, 'jobs': []},
                         {'total_count': 1001, 'jobs': self.jobs[:100]}, {'total_count': 4, 'jobs': None}]:
            def read(path): return response if '/jobs?' in path else original(path)
            with self.assertRaises(MODULE.ReuseError): self.validate(read=read)

    def test_inconsistent_second_page_and_run_changed_during_validation(self):
        original = self.read
        reads = 0
        def read(path):
            nonlocal reads
            value = original(path)
            if path.endswith('/actions/runs/123'):
                reads += 1
                if reads == 2: value['run_attempt'] = 2
            return value
        with self.assertRaises(MODULE.ReuseError): self.validate(read=read)

    def test_second_page_cannot_change_total_count(self):
        while len(self.jobs) < 101:
            job = copy.deepcopy(self.jobs[0]);job['id'] = len(self.jobs)+1;job['name'] = 'extra '+str(job['id']);self.jobs.append(job)
        original = self.read
        def read(path):
            value = original(path)
            if '/jobs?' in path and path.endswith('page=2'): value['total_count'] = 102
            return value
        with self.assertRaisesRegex(MODULE.ReuseError, 'Inconsistent job inventory'): self.validate(read=read)

    def test_missing_api_result_is_fail_closed(self):
        with self.assertRaises(MODULE.ReuseError): self.validate(read=lambda path: None)


class CommandAndTransport(unittest.TestCase):
    def test_no_input_does_not_read_api_and_outputs_false(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(MODULE, 'api_reader', side_effect=AssertionError('network forbidden')):
            with patch.dict(os.environ, {'GITHUB_OUTPUT': tmp+'/output'}), patch.object(MODULE.sys, 'argv', ['validator', '--receipt', tmp+'/receipt']):
                self.assertEqual(MODULE.main(), 0)
            self.assertEqual(Path(tmp+'/output').read_text(), 'reuse=false\n')
            self.assertFalse(json.loads(Path(tmp+'/receipt').read_text())['reuse'])

    def test_invalid_input_never_outputs_reuse_or_secret(self):
        with tempfile.TemporaryDirectory() as tmp:
            with patch.dict(os.environ, {'GITHUB_OUTPUT': tmp+'/output', 'GH_TOKEN': 'secret-fixture-token',
                                         'GITHUB_REPOSITORY': MODULE.REPOSITORY, 'GITHUB_SHA': SHA,
                                         'GITHUB_RUN_ID': '456'}), patch.object(MODULE.sys, 'argv', ['validator', '--run-id', '../bad', '--receipt', tmp+'/receipt']):
                self.assertEqual(MODULE.main(), 1)
            self.assertFalse(Path(tmp+'/output').exists())
            self.assertFalse(Path(tmp+'/receipt').exists())

    def test_http_errors_and_malformed_json_do_not_disclose_token(self):
        for error in [urllib.error.HTTPError('https://api.github.com', 403, 'secret-fixture-token', {}, None),
                      urllib.error.URLError('secret-fixture-token')]:
            with patch.object(urllib.request.OpenerDirector, 'open', side_effect=error):
                with self.assertRaises(MODULE.ReuseError) as raised:
                    MODULE.api_reader('secret-fixture-token')('/repos/'+MODULE.REPOSITORY+'/actions/runs/123')
                self.assertNotIn('secret-fixture-token', str(raised.exception))
        response = SimpleNamespace(status=200, read=lambda size: b'{not-json')
        with patch.object(urllib.request.OpenerDirector, 'open', return_value=contextlib.nullcontext(response)):
            with self.assertRaises(MODULE.ReuseError):
                MODULE.api_reader('secret-fixture-token')('/repos/'+MODULE.REPOSITORY+'/actions/runs/123')
        self.assertIsNone(MODULE.NoRedirect().redirect_request(None, None, None, None, None, None))


class WorkflowContracts(unittest.TestCase):
    def test_required_scope_names_match_actual_workflow_jobs_and_steps(self):
        ci = (ROOT / MODULE.WORKFLOW).read_text()
        jobs = {}
        for match in re.finditer(r'^  [a-z][a-z-]*:\n(.*?)(?=^  [a-z][a-z-]*:\n|\Z)', ci, re.M | re.S):
            body = match[1]
            name = re.search(r'^    name: (.+)$', body, re.M)
            if name: jobs[name[1]] = set(re.findall(r'^      - name: (.+)$', body, re.M))
        for name, steps in MODULE.REQUIRED.items():
            self.assertIn(name, jobs)
            self.assertLessEqual(steps, jobs[name])
        self.assertIn("run: python3 -m unittest discover -s scripts/tests -p 'test_ci_reuse.py' -v", ci)

    def test_release_native_and_publish_bodies_are_unchanged(self):
        # The dispatch parameter cannot condition or replace native artifact gates.
        release = (ROOT / '.github/workflows/release.yml').read_text()
        self.assertIn('needs: [version, checks]', release)
        for text in ['ubuntu-24.04-arm', '--severity HIGH,CRITICAL --exit-code 1',
                     'scripts/verify-release.py', 'scripts/publish-release.py']:
            self.assertIn(text, release)
        native = release[release.index('  images:\n'):]
        self.assertNotIn('prior_ci_run_id', native)
        self.assertNotIn('reuse', native)
        self.assertIn('Inspect the exact native UI binary', native)
        self.assertIn('go run scripts/security/check-go-binary.go /tmp/gateway-release-caddy golang.org/x/crypto/openpgp', native)
        self.assertNotIn('skip_ci', release)

    def test_ci_fresh_security_and_test_jobs_use_only_validated_output(self):
        ci = (ROOT / MODULE.WORKFLOW).read_text()
        security = ci.split('  security:\n', 1)[1].split('  go:\n', 1)[0]
        self.assertIn('gitleaks git', security)
        self.assertIn('--log-opts=--all', security)
        self.assertIn('trivy fs --scanners vuln --severity HIGH,CRITICAL --exit-code 1', security)
        self.assertIn('run: make vulncheck', security)
        for job in ['go', 'profile-name', 'container']:
            self.assertIn(f"  {job}:\n    needs: prior-ci\n    if: needs.prior-ci.outputs.reuse != 'true'", ci)
        self.assertIn('prior_ci_run_id: ${{ inputs.prior_ci_run_id }}', (ROOT / '.github/workflows/release.yml').read_text())
        self.assertIn('actions: read', ci)


if __name__ == '__main__':
    unittest.main()
