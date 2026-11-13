"""Fork PR event/permission contracts. No workflow dispatch or live GitHub access."""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import tempfile
import unittest
from unittest.mock import patch
import test_ci_reuse

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('pr_prior_ci', ROOT/'scripts/ci/validate-prior-run.py')
MODULE = importlib.util.module_from_spec(SPEC);SPEC.loader.exec_module(MODULE)


class PullRequestBoundary(unittest.TestCase):
    def assert_read_only_permissions(self, ci):
        self.assertIn('permissions:\n  contents: read\n', ci)
        lines = ci.splitlines()
        for index, line in enumerate(lines):
            permission = re.fullmatch(r'( *)permissions:\s*(.*)', line)
            if not permission:
                continue
            self.assertEqual(permission[2], '', 'Use explicit read-only permission mappings')
            entries = []
            for child in lines[index + 1:]:
                if not child.strip() or child.lstrip().startswith('#'):
                    continue
                if len(child) - len(child.lstrip()) <= len(permission[1]):
                    break
                self.assertRegex(child, r'^ +[a-z-]+: (?:read|none)\s*(?:#.*)?$')
                entries.append(child)
            self.assertTrue(entries, 'Missing explicit permission scope')

    def assert_checkout_boundaries(self, ci):
        # Check checkout behavior independently of the action's version or pinned SHA.
        checkouts = list(re.finditer(
            r'^[ \t]*- (?:name: [^\n]+\n[ \t]+)?uses: actions/checkout@[^\s#]+'
            r'[ \t]*(?:#[^\n]*)?\n(.*?)(?=^[ \t]*- |\Z)', ci, re.M | re.S))
        self.assertEqual(len(checkouts), 8)
        for match in checkouts:
            block = match[1]
            self.assertRegex(block, r'(?m)^ +persist-credentials: false\s*(?:#.*)?$')
            self.assertNotRegex(block, r'(?m)^\s+ref:')
            self.assertNotRegex(block, r'(?m)^\s+repository:')
        self.assertNotIn('pull_request.head.sha', ci)

    def test_only_requested_main_pr_events_and_existing_manual_reusable_routes(self):
        ci=(ROOT/'.github/workflows/ci.yml').read_text()
        self.assertIn('  pull_request:\n    branches: [main]\n    types: [opened, synchronize, reopened]',ci)
        self.assertIn('  workflow_dispatch:',ci);self.assertIn('  workflow_call:',ci)
        self.assertNotIn('pull_request_target',ci)
        self.assertNotIn('secrets: inherit',ci)
        self.assertNotIn('secrets.',ci)
        self.assertNotIn('packages: write',ci);self.assertNotIn('contents: write',ci)
        self.assert_read_only_permissions(ci)

    def test_superseded_runs_group_by_pr_number_and_do_not_cancel_manual_run(self):
        ci=(ROOT/'.github/workflows/ci.yml').read_text()
        self.assertIn("github.event_name == 'pull_request' && format('pr-{0}', github.event.pull_request.number) || github.ref",ci)
        self.assertIn('  cancel-in-progress: true',ci)
        self.assertNotIn('github.head_ref',ci)

    def test_fork_checkouts_use_event_merge_sha_without_persisted_credentials(self):
        ci=(ROOT/'.github/workflows/ci.yml').read_text()
        self.assert_checkout_boundaries(ci)
        # Default checkout uses GITHUB_SHA (PR merge commit), not the fork head or mutable branch.
        action=(ROOT/'.github/actions/ui-checks/action.yml').read_text()
        self.assertIn('--source-sha "$GITHUB_SHA"',action)

    def test_checkout_action_updates_preserve_boundary_checks(self):
        ci = (ROOT/'.github/workflows/ci.yml').read_text()
        for ref in ('v6', 'v7', 'v7.0.1', 'f' * 40 + ' # v7'):
            with self.subTest(ref=ref):
                updated, count = re.subn(r'actions/checkout@[^\s#]+', 'actions/checkout@' + ref, ci)
                self.assertEqual(count, 8)
                self.assert_checkout_boundaries(updated)
                self.assert_read_only_permissions(updated)

    def test_updated_checkout_rejects_unsafe_or_missing_configuration(self):
        ci = re.sub(r'actions/checkout@[^\s#]+', 'actions/checkout@v7',
                    (ROOT/'.github/workflows/ci.yml').read_text())
        for replacement in ('persist-credentials: true', '',
                            'persist-credentials: false\n          ref: ${{ github.event.pull_request.head.sha }}',
                            'persist-credentials: false\n          repository: contributor/fork'):
            with self.subTest(replacement=replacement), self.assertRaises(AssertionError):
                self.assert_checkout_boundaries(ci.replace('persist-credentials: false', replacement, 1))
        with self.assertRaises(AssertionError):
            self.assert_checkout_boundaries(ci.replace('actions/checkout@v7', 'actions/setup-node@v4', 1))

    def test_checkout_update_never_allows_write_permissions(self):
        ci = re.sub(r'actions/checkout@[^\s#]+', 'actions/checkout@v7',
                    (ROOT/'.github/workflows/ci.yml').read_text())
        for unsafe in (ci.replace('contents: read', 'contents: write', 1),
                       ci.replace('actions: read', 'actions: write', 1),
                       ci.replace('permissions:\n  contents: read', 'permissions: write-all', 1),
                       ci.replace('      actions: read', '      actions: read\n      id-token: write', 1)):
            with self.subTest(workflow=unsafe), self.assertRaises(AssertionError):
                self.assert_read_only_permissions(unsafe)

    def test_pr_never_exposes_api_token_to_prior_run_validation(self):
        ci=(ROOT/'.github/workflows/ci.yml').read_text()
        pr=ci.split('      - name: Require full validation for pull requests\n',1)[1].split('      - name:',1)[0]
        self.assertIn("if: github.event_name == 'pull_request'",pr)
        self.assertNotIn('GH_TOKEN',pr);self.assertNotIn('--run-id',pr)
        normal=ci.split('      - name: Validate explicit prior run or require full CI\n',1)[1].split('      - uses:',1)[0]
        self.assertIn("if: github.event_name != 'pull_request'",normal)
        self.assertIn('GH_TOKEN: ${{ github.token }}',normal)
        self.assertIn("reuse: ${{ github.event_name != 'pull_request' && steps.prior.outputs.reuse == 'true' }}",ci)

    def test_empty_pr_request_is_full_validation_without_api_or_token(self):
        for repository in [MODULE.REPOSITORY,'contributor/fork']:
            with tempfile.TemporaryDirectory() as tmp, patch.object(MODULE,'api_reader',side_effect=AssertionError('No API on PR')):
                env={'GITHUB_EVENT_NAME':'pull_request','GITHUB_REPOSITORY':repository,
                     'GITHUB_SHA':'a'*40,'GITHUB_OUTPUT':tmp+'/output'}
                with patch.dict(os.environ,env,clear=True),patch('sys.argv',['validator','--receipt',tmp+'/receipt']),contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(MODULE.main(),0)
                self.assertEqual(Path(tmp+'/output').read_text(),'reuse=false\n')
                self.assertFalse(json.loads(Path(tmp+'/receipt').read_text())['reuse'])

    def test_pr_cannot_request_reuse_even_with_matching_merge_sha_or_token(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(MODULE,'api_reader',side_effect=AssertionError('No API on PR')):
            env={'GITHUB_EVENT_NAME':'pull_request','GITHUB_REPOSITORY':MODULE.REPOSITORY,
                 'GITHUB_SHA':'a'*40,'GITHUB_RUN_ID':'456','GH_TOKEN':'fixture-only','GITHUB_OUTPUT':tmp+'/output'}
            with patch.dict(os.environ,env,clear=True),patch('sys.argv',['validator','--run-id','123','--receipt',tmp+'/receipt']),contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(MODULE.main(),1)
            self.assertFalse(Path(tmp+'/output').exists())

    def test_release_workflow_is_not_a_pr_or_publish_path(self):
        release=(ROOT/'.github/workflows/release.yml').read_text()
        trigger=release.split('permissions:',1)[0]
        self.assertNotIn('pull_request',trigger)
        self.assertIn('  workflow_dispatch:',trigger)
        ci=(ROOT/'.github/workflows/ci.yml').read_text()
        self.assertNotIn('scripts/publish-release.py',ci)
        self.assertNotIn('docker push',ci)

    def test_fork_or_same_repo_pr_receipts_never_certify_release_merge_source(self):
        for fork in [True,False]:
            fixture=test_ci_reuse.Acceptance();fixture.setUp()
            fixture.run['event']='pull_request';fixture.run['pull_requests']=[{'number':21}]
            if fork:fixture.run['head_repository']={'id':99,'full_name':'contributor/fork'}
            with self.assertRaises(MODULE.ReuseError):
                MODULE.validate('123',test_ci_reuse.SHA,'456',MODULE.REPOSITORY,fixture.definition,fixture.read,now=test_ci_reuse.NOW)
            self.assertEqual(len(fixture.calls),1)  # Stops before definition/jobs/artifacts are read.
        fixture=test_ci_reuse.Acceptance();fixture.setUp();fixture.run['head_sha']='b'*40
        with self.assertRaisesRegex(MODULE.ReuseError,'source SHA'):
            MODULE.validate('123',test_ci_reuse.SHA,'456',MODULE.REPOSITORY,fixture.definition,fixture.read,now=test_ci_reuse.NOW)

    def test_standalone_may_skip_only_pr_branch_and_must_validate_prior_run(self):
        fixture=test_ci_reuse.Acceptance();fixture.setUp()
        job=next(j for j in fixture.jobs if j['name']=='Validate prior CI result')
        job['steps'].append({'name':'Require full validation for pull requests','status':'completed','conclusion':'skipped'})
        self.assertTrue(MODULE.validate('123',test_ci_reuse.SHA,'456',MODULE.REPOSITORY,fixture.definition,fixture.read,now=test_ci_reuse.NOW)['reuse'])
        next(s for s in job['steps']if s['name']=='Validate explicit prior run or require full CI')['conclusion']='skipped'
        with self.assertRaises(MODULE.ReuseError):
            MODULE.validate('123',test_ci_reuse.SHA,'456',MODULE.REPOSITORY,fixture.definition,fixture.read,now=test_ci_reuse.NOW)


if __name__=='__main__':unittest.main()
