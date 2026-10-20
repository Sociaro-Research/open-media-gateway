#!/usr/bin/env python3
"""Server-only public HTTP checks. The service credential stays in memory."""
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ORIGIN = ''  # Set from the deployed service configuration before HTTP access.
COMPOSE = ['docker', 'compose', '--profile', 'ui', '-f', 'compose.yaml', '-f', 'compose.staging.yaml']


def verify_frontend(request):
    status, data = request('/site.json', auth=False)
    require(status == 200, 'Public appearance metadata is unavailable.')
    site = json.loads(data)
    require(site.get('api_url') == ORIGIN and site.get('ui_url') == ORIGIN,
            'Console and API must retain the configured shared public origin.')
    status, body = request('/organizations', auth=False)
    require(status == 200 and b'Sign in to get started' in body and b'/ui/payments.js' not in body,
            'Anonymous console must show only the sign-in page.')
    for path in ('/privacy', '/terms'):
        require(request(path, auth=False)[0] == 200, 'Public legal page unavailable: ' + path)
    for name in ('site.js', 'theme.js'):
        status, body = request('/ui/' + name, auth=False)
        require(status == 200 and hashlib.sha256(body).digest() == hashlib.sha256((Path('web/public/ui') / name).read_bytes()).digest(),
                'The UI image does not contain this release: ' + name)
    require(request('/auth/session', auth=False)[0] == 401, 'Anonymous session preflight must reject access.')
    require(request('/auth/session')[0] == 401, 'A service API key must not establish a browser session.')
    require(request('/ui/app.js', auth=False)[0] == 401, 'Private console scripts must require a session.')
    require(request('/ui/index.html', auth=False)[0] == 404, 'Raw console HTML must not bypass the session gate.')
    docs = site.get('docs_url')
    if docs:
        require(urllib.parse.urlsplit(docs).scheme == 'https' and docs != ORIGIN, 'Invalid documentation origin.')
        require(request('/', auth=False, origin=docs)[0] == 200, 'Standalone documentation is unavailable.')
        status, body = request('/openapi.json', auth=False, origin=docs)
        require(status == 200, 'Documentation OpenAPI contract unavailable.')
        contract = json.loads(body)
        require(contract['servers'][0]['url'] == ORIGIN and '/auth/session' in contract['paths'], 'Documentation points at an obsolete API contract.')
        require(request('/ui/docs.js', auth=False, origin=docs)[0] == 200, 'Documentation client unavailable.')
        for path in ('/v1/info', '/auth/session', '/ui/app.js', '/media/test'):
            require(request(path, origin=docs)[0] == 404, 'Docs origin exposes a private route: ' + path)
        require(request('/site.json', {}, auth=False, origin=docs)[0] == 404, 'Docs origin must reject mutations.')
    print('Independent UI release assets, legal pages, session guards and documentation verified.', flush=True)


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def output(args):
    return subprocess.check_output(args, text=True).strip()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def verify_cost(task, query=output):
    require(task.get('provider') == 'mock', 'The completed task must use mock.')
    require((task.get('cost') or {}).get('expected_usd') == 0, 'Mock expected cost must be zero.')
    # Public responses intentionally hide provider actual_usd. Verify the persisted
    # zero cost instead of treating a missing public field as evidence of zero.
    task_id = str(uuid.UUID(task['id']))
    sql = ("SELECT count(*) = 1 AND bool_and(provider='mock' AND status='succeeded' "
           "AND cost->>'actual_source'='free_provider' AND (cost->>'actual_usd')::numeric=0) "
           "FROM tasks WHERE id='" + task_id + "';")
    result = query(COMPOSE + ['exec', '-T', 'postgres', 'psql', '-X', '-At', '-v', 'ON_ERROR_STOP=1',
                              '-U', 'media_gateway', '-d', 'media_gateway', '-c', sql])
    require(result == 't', 'Mock zero actual cost was not confirmed in PostgreSQL.')


def main(sha):
    global ORIGIN
    ua = 'Sociaro-Release-Verification/' + sha
    container = output(COMPOSE + ['ps', '-q', 'gateway'])
    environment = json.loads(output(['docker', 'inspect', '--format', '{{json .Config.Env}}', container]))
    values = dict(item.split('=', 1) for item in environment if '=' in item)
    origin = values.get('AUTH_PUBLIC_URL', '')
    if not origin:
        callback = urllib.parse.urlsplit(values.get('GOOGLE_OAUTH_REDIRECT_URL', ''))
        origin = urllib.parse.urlunsplit((callback.scheme, callback.netloc, '', '', ''))
    parsed = urllib.parse.urlsplit(origin)
    require(parsed.scheme == 'https' and parsed.hostname and not parsed.username and not parsed.password
            and not parsed.path and not parsed.query and not parsed.fragment,
            'A valid public HTTPS origin must be configured before smoke HTTP access.')
    require(not values.get('UI_PUBLIC_URL') or values['UI_PUBLIC_URL'] == origin,
            'Console and API must use the same configured public origin.')
    ORIGIN = origin
    key = next(x.split('=', 1)[1] for x in environment if x.startswith('CLIENT_API_KEY='))
    require(bool(key), 'Service key is missing.')
    opener = urllib.request.build_opener(NoRedirect())

    def request(path, body=None, auth=True, origin=ORIGIN):
        headers = {'User-Agent': ua}
        if auth:
            headers['Authorization'] = 'Bearer ' + key
        if body is not None:
            headers['Content-Type'] = 'application/json'
        req = urllib.request.Request(origin + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
        try:
            with opener.open(req, timeout=25) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.read()

    status, _ = request('/healthz', auth=False)
    require(status == 200, 'Health check failed: HTTP ' + str(status))
    print('healthz: 200', flush=True)
    verify_frontend(request)
    status, _ = request('/v1/system/payment-notifications')
    require(status == 403, 'Service key unexpectedly has system administrator access.')
    print('Login and system administration guards verified.', flush=True)
    status, body = request('/v1/images/generations', {
        'model': 'mock/mock-image-v1',
        'prompt': 'Release smoke test ' + sha + ', free mock only',
        'max_expected_cost_usd': 0, 'options': {},
    })
    require(status == 202, 'Mock submission failed: HTTP ' + str(status))
    task_id = str(uuid.UUID(json.loads(body)['id']))
    print('Mock task: ' + task_id, flush=True)
    for _ in range(25):
        status, body = request('/v1/generations/' + task_id)
        require(status == 200, 'Mock status check failed: HTTP ' + str(status))
        task = json.loads(body)
        if task['status'] == 'completed':
            break
        require(task['status'] not in ('failed', 'cancelled'), 'Mock generation failed.')
        time.sleep(1)
    else:
        raise RuntimeError('Mock generation did not finish within the polling window.')
    verify_cost(task)
    print('Mock generation succeeded; PostgreSQL confirms actual_usd=0.', flush=True)
    status, body = request('/v1/generations/' + task_id + '/media-access')
    require(status == 200, 'Media access failed: HTTP ' + str(status))
    url = urllib.parse.urljoin(ORIGIN, json.loads(body)['download_url'])
    require(urllib.parse.urlparse(url).scheme == 'https', 'Expected an HTTPS media URL.')
    # Signed media URLs authorize themselves; never forward the API key.
    with urllib.request.urlopen(urllib.request.Request(url, headers={'User-Agent': ua}), timeout=30) as response:
        data = response.read()
        require(response.status == 200 and bool(data), 'Mock download is empty or failed.')
        print('Mock download: %d bytes; sha256: %s' % (len(data), hashlib.sha256(data).hexdigest()), flush=True)
    print('Production smoke passed.', flush=True)


if __name__ == '__main__':
    main(sys.argv[1])
