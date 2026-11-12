#!/usr/bin/env python3
"""Fail-closed acceptance of an explicit, recent standalone CI run; never execute artifacts."""
import argparse
import base64
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import urllib.error
import urllib.request

REPOSITORY = 'Sociaro-Research/open-media-gateway'
WORKFLOW = '.github/workflows/ci.yml'
MAX_AGE = dt.timedelta(hours=6)
REQUIRED = {
    'Validate prior CI result': {'Test CI reuse guard offline',
                                 'Validate explicit prior run or require full CI', 'Test UI partition coverage offline'},
    'Secret history and dependency security': {
        'Check out complete history', 'Install checksum-pinned scanners',
        'Scan complete Git history for secrets', 'Check all dependency versions for known vulnerabilities'},
    'Go checks': {'Verify code and integration tests',
                  'Verify backend-only build and first-administrator workflow',
                  'Verify release stop safety gates'},
    'Persisted profile and legal browser checks': {
        'Verify real name saves and navigation in Chromium and WebKit',
        'Verify persisted legal settings in Chromium and WebKit'},
    'Container build': {'Validate Compose configuration',
                        'Validate Prometheus configuration and alert behavior',
                        'Build image', 'Build independent UI image',
                        'Build hardened database and cache images',
                        'Scan built application and UI images',
                        'Validate UI configuration and extract its runtime',
                        'Verify installation without source files',
                        'Package exact UI runtime for browser jobs',
                        'Verify real Grafana provisioning and browser dashboard'},
    'UI performance and billing': {'Check out repository', 'Run isolated UI partition'},
    'UI media and navigation': {'Check out repository', 'Run isolated UI partition'},
    'UI console and security': {'Check out repository', 'Run isolated UI partition'},
}
# These are conditional only because a standalone full CI already runs govulncheck in make verify.
OPTIONAL_STEPS = {'Set up Go for fresh vulnerability assessment',
                  'Assess Go vulnerabilities after CI reuse'}


class ReuseError(ValueError):
    pass


def require(condition, message):
    if not condition:
        raise ReuseError(message)


def timestamp(value):
    require(isinstance(value, str) and bool(re.fullmatch(
        r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z', value)), 'Malformed UTC timestamp')
    try:
        return dt.datetime.fromisoformat(value.replace('Z', '+00:00'))
    except ValueError:
        raise ReuseError('Malformed UTC timestamp') from None


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args):
        return None


def api_reader(token):
    opener = urllib.request.build_opener(NoRedirect())

    def read(path):
        require(path.startswith('/repos/' + REPOSITORY + '/'), 'Unexpected API scope')
        request = urllib.request.Request('https://api.github.com' + path, headers={
            'Accept': 'application/vnd.github+json', 'Authorization': 'Bearer ' + token,
            'X-GitHub-Api-Version': '2022-11-28', 'User-Agent': 'Gateway-CI-Reuse-Validator'})
        try:
            with opener.open(request, timeout=30) as response:
                require(response.status == 200, 'GitHub API response was not successful')
                body = response.read(4 * 1024 * 1024 + 1)
                require(len(body) <= 4 * 1024 * 1024, 'GitHub API response too large')
                return json.loads(body)
        except (urllib.error.URLError, OSError, ValueError):
            # Never echo tokens, headers, server bodies, or arbitrary exception messages.
            raise ReuseError('GitHub API unavailable, denied, or malformed; reuse refused') from None
    return read


def validate(run_id, expected_sha, current_run_id, repository, workflow_bytes, read, now=None):
    require(repository == REPOSITORY, 'Reuse requires the trusted repository')
    require(bool(re.fullmatch(r'[1-9][0-9]{0,19}', run_id)), 'Expected a numeric prior CI run ID')
    require(bool(re.fullmatch(r'[1-9][0-9]{0,19}', current_run_id)), 'Missing current run identity')
    require(run_id != current_run_id, 'The current run cannot certify itself')
    require(bool(re.fullmatch(r'[0-9a-f]{40}', expected_sha)), 'Expected an exact source SHA')
    now = now or dt.datetime.now(dt.timezone.utc)
    root = '/repos/' + REPOSITORY
    run = read(root + '/actions/runs/' + run_id)
    require(isinstance(run, dict), 'Malformed workflow run')
    require(type(run.get('id')) is int and run['id'] == int(run_id), 'Run identity mismatch')
    require(run.get('event') == 'workflow_dispatch' and run.get('head_branch') == 'main',
            'Only standalone manual CI on main is reusable')
    require(run.get('path') == WORKFLOW and not run.get('pull_requests'), 'Unexpected workflow or PR context')
    require(run.get('head_sha') == expected_sha, 'CI source SHA differs from release source')
    require(run.get('status') == 'completed' and run.get('conclusion') == 'success', 'CI is not completed success')
    repos = [run.get('repository'), run.get('head_repository')]
    require(all(isinstance(r, dict) and r.get('full_name') == REPOSITORY for r in repos),
            'Untrusted run repository')
    require(all(type(r.get('id')) is int and r['id'] > 0 for r in repos) and repos[0]['id'] == repos[1]['id'],
            'Run repository IDs differ')
    workflow = read(root + '/actions/workflows/ci.yml')
    require(isinstance(workflow, dict) and workflow.get('path') == WORKFLOW
            and type(workflow.get('id')) is int and workflow['id'] == run.get('workflow_id'),
            'Workflow identity mismatch')
    definition = read(root + '/contents/' + WORKFLOW + '?ref=' + expected_sha)
    require(isinstance(definition, dict) and definition.get('encoding') == 'base64', 'Missing workflow definition')
    require(isinstance(definition.get('content'), str), 'Missing workflow content')
    try:
        contents = base64.b64decode(''.join(definition['content'].split()), validate=True)
    except (KeyError, TypeError, ValueError):
        raise ReuseError('Malformed workflow definition') from None
    require(contents == workflow_bytes, 'CI workflow definition differs from checked-out source')
    started = timestamp(run.get('run_started_at'))
    created = timestamp(run.get('created_at'))
    updated = timestamp(run.get('updated_at'))
    require(created <= started <= updated <= now and now - started <= MAX_AGE, 'CI is stale or has inconsistent times')
    attempt = run.get('run_attempt')
    require(type(attempt) is int and attempt > 0, 'Missing run attempt')
    jobs, total = [], None
    # Read all pages from the exact successful attempt. Never accept a partial/default latest inventory.
    for page in range(1, 11):
        response = read(root + f'/actions/runs/{run_id}/attempts/{attempt}/jobs?per_page=100&page={page}')
        require(isinstance(response, dict) and type(response.get('total_count')) is int
                and isinstance(response.get('jobs'), list), 'Malformed job inventory')
        count = response['total_count']
        require(0 < count <= 1000 and (total is None or total == count), 'Inconsistent job inventory')
        total = count
        items = response['jobs']
        require(0 < len(items) <= 100 and len(jobs) + len(items) <= total, 'Incomplete job inventory')
        jobs.extend(items)
        if len(jobs) == total:
            break
        require(len(items) == 100, 'Incomplete paginated job inventory')
    require(len(jobs) == total, 'Incomplete paginated job inventory')
    names, identifiers = {}, set()
    for job in jobs:
        require(isinstance(job, dict), 'Malformed job')
        require(type(job.get('run_id')) is int and type(job.get('run_attempt')) is int
                and job['run_id'] == int(run_id) and job['run_attempt'] == attempt
                and job.get('head_sha') == expected_sha, 'Job provenance mismatch')
        require(type(job.get('id')) is int and job['id'] > 0 and job['id'] not in identifiers, 'Duplicate or missing job ID')
        identifiers.add(job['id'])
        require(job.get('status') == 'completed' and job.get('conclusion') == 'success', 'Failed, skipped or incomplete job')
        begin, end = timestamp(job.get('started_at')), timestamp(job.get('completed_at'))
        require(started <= begin <= end <= updated, 'Inconsistent job timestamps')
        name = job.get('name')
        require(isinstance(name, str) and name not in names, 'Duplicate or missing job name')
        names[name] = job
        steps = job.get('steps')
        require(isinstance(steps, list) and bool(steps), 'Missing job steps')
        seen = set()
        for step in steps:
            require(isinstance(step, dict) and isinstance(step.get('name'), str), 'Malformed job step')
            require(step['name'] not in seen, 'Duplicate job step')
            seen.add(step['name'])
            optional = name == 'Secret history and dependency security' and step['name'] in OPTIONAL_STEPS
            require(step.get('status') == 'completed' and
                    (step.get('conclusion') == 'success' or optional and step.get('conclusion') == 'skipped'),
                    'Failed, skipped or incomplete acceptance step')
        require(REQUIRED.get(name, set()) <= seen, 'Required acceptance steps missing')
    require(REQUIRED.keys() <= names.keys(), 'Required CI jobs missing')
    # Detect a rerun started while inventory was being read; never mix attempts/outcomes.
    current = read(root + '/actions/runs/' + run_id)
    require(current == run, 'Prior CI run changed during validation')
    return {'reuse': True, 'run_id': int(run_id), 'attempt': attempt, 'source_sha': expected_sha,
            'repository': REPOSITORY, 'workflow': WORKFLOW, 'workflow_id': workflow['id'],
            'workflow_sha256': hashlib.sha256(contents).hexdigest(), 'max_age_seconds': int(MAX_AGE.total_seconds()),
            'validated_at': now.isoformat(), 'run_started_at': run['run_started_at'],
            'jobs': [{'id': names[n]['id'], 'name': n} for n in REQUIRED]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run-id', default='')
    parser.add_argument('--receipt', required=True)
    args = parser.parse_args()
    try:
        receipt = {'reuse': False, 'reason': 'No prior run requested; full CI required'}
        if args.run_id:
            token = os.environ.get('GH_TOKEN', '')
            require(bool(token), 'Missing GitHub API token')
            receipt = validate(args.run_id, os.environ.get('GITHUB_SHA', ''),
                               os.environ.get('GITHUB_RUN_ID', ''), os.environ.get('GITHUB_REPOSITORY', ''),
                               Path(WORKFLOW).read_bytes(), api_reader(token))
        Path(args.receipt).write_text(json.dumps(receipt, indent=2) + '\n')
        # Emit the bypass decision only after every provenance/scope/freshness check succeeded.
        with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
            output.write('reuse=' + str(receipt['reuse']).lower() + '\n')
        print('Validated prior CI acceptance' if receipt['reuse'] else receipt['reason'])
    except (ReuseError, KeyError, TypeError, OSError) as error:
        print('CI reuse refused: ' + (str(error) if isinstance(error, ReuseError) else 'invalid metadata or local output'), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
