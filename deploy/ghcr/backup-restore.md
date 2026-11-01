# Backup, verification and recovery

Use this guide from the **old, currently installed release bundle**, before
changing images or configuration. The source/server workflow also has its own
[deployment guide](../../docs/deployment.md); these instructions cover the ready-made
bundle without a source checkout. Commands require Docker Compose and Python 3.9+.
Run each numbered step in order in one POSIX shell. Stop on any failure.

## What must be recoverable

A usable recovery point includes PostgreSQL, local media/reference files, Redis,
any enabled HTTPS/monitoring volumes, the complete installed bundle and its
`.env`, overrides and image versions. PostgreSQL contains accounts, tasks,
provider/storage settings and migration history. **Preserve the original
`PROVIDER_CREDENTIALS_ENCRYPTION_KEY`**: a database dump alone cannot decrypt saved
credentials. Preserve all signing keys, passwords, public URLs and proxy settings
too. Never rerun `initialize.py` or substitute the new release's `.env`.

The standard bundle uses PostgreSQL user/database `media_gateway`, PostgreSQL 17
and Redis 7.4. The commands below discover the actual running user/database,
Compose project, mounted volume names and image IDs. Do not guess Docker's volume
prefix from a directory name. Use the same `-p`, `-f`, `--env-file` and profile
selection used to start the installation. For custom flags, replace the `DC`
list in **both** Python blocks and pass those flags to the worker stop helper.
The default `DC` reads `COMPOSE_FILE`, `COMPOSE_PROFILES` and
`COMPOSE_PROJECT_NAME` from `.env`. Clear unrelated exported Compose/image
variables first; do not print `.env`, `docker inspect` or resolved Compose config
into logs. Do not use `set -x`.

Account-specific S3 settings live in the database; their **objects do not**.
Check every account's Storage configuration, including previously used buckets
and connections retained by saved results. Take a coordinated, versioned backup
of those buckets/prefixes with the storage administrator, and record its recovery
point with this backup. Pause direct uploads and external writers first. A copy
of `media-data` does not include S3 objects, and this procedure does not copy or
restore them. Custom bind mounts outside the bundle are rejected below; back
those up separately and adapt the drill before upgrading. Keep external proxy
configuration/certificates in the same recovery plan.

## 1. Prepare and discover (no downtime yet)

Check free disk space on both the Docker data disk and backup destination.
Allow space for images, all data, a second installation and the new release.
Keep the backup **outside** the installed directory, preferably on another disk;
then copy the verified backup to protected off-host storage. It contains secrets
and private media. Restrict access and encrypt it according to your policy.

```sh
# Run from the existing extracted bundle. Choose an existing backup parent.
set -eu
umask 077
export GW_BACKUP="$(cd .. && pwd)/gateway-backup-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -m 700 "$GW_BACKUP"
```

<!-- gw167:inventory -->
```sh
python3 - <<'PY'
import json, os, pathlib, subprocess
DC = ['docker', 'compose']  # Add the installation's explicit flags here if used.
def dc(*args):
    return subprocess.check_output(DC + list(args), text=True)
app = pathlib.Path.cwd().resolve()
backup = pathlib.Path(os.environ['GW_BACKUP']).resolve()
assert backup.is_dir() and backup != app and app not in backup.parents
assert (app / '.env').is_file(), 'Run from the installed bundle, not the new one'
config = json.loads(dc('config', '--format', 'json'))  # Keep credentials private.
ids = dc('ps', '--all', '--quiet').split()
assert ids, 'No installation containers found; check project and Compose flags'
containers = json.loads(subprocess.check_output(['docker', 'inspect', *ids]))
volumes, images, database, running_services = {}, {}, None, []
for c in containers:
    labels = c['Config']['Labels']
    service = labels['com.docker.compose.service']
    assert labels['com.docker.compose.project'] == config['name']
    assert service in config['services'], 'Reconcile orphan services before backup'
    images[service] = c['Image']
    if c['State']['Running']:
        running_services.append(service)
    for mount in c['Mounts']:
        if mount['Type'] == 'bind':
            # Docker Desktop reports host paths under /host_mnt. Compare the
            # actual mount with Compose before archiving the host directory.
            actual = mount['Source']
            if actual.startswith('/host_mnt/'):
                actual = actual[len('/host_mnt'):]
            source = pathlib.Path(actual).resolve()
            expected = [v['source'] for v in config['services'][service].get('volumes', [])
                        if v['type'] == 'bind' and v['target'] == mount['Destination']]
            assert len(expected) == 1 and source == pathlib.Path(expected[0]).resolve(), 'Reconcile changed bind mounts first'
            assert source == app or app in source.parents, 'Back up external bind mounts separately'
        elif mount['Type'] == 'volume':
            matches = [key for key, value in config.get('volumes', {}).items()
                       if value['name'] == mount['Name']]
            assert len(matches) == 1, 'Unmapped/anonymous volume: adapt the backup plan'
            volumes[matches[0]] = mount['Name']
    if service == 'postgres':
        env = dict(item.split('=', 1) for item in c['Config']['Env'] if '=' in item)
        database = {'user': env['POSTGRES_USER'], 'name': env['POSTGRES_DB'],
                    'image': c['Image']}
assert database and 'postgres-data' in volumes and 'media-data' in volumes
assert images.get('gateway') and images.get('worker')
record = {'project': config['name'], 'database': database, 'volumes': volumes,
          'images': images, 'running_services': running_services, 'compose': DC, 'app': str(app),
          'configured_volumes': {k: v['name'] for k, v in config.get('volumes', {}).items()},
          'networks': {k: v['name'] for k, v in config.get('networks', {}).items()}}
(backup / 'inventory.json').write_text(json.dumps(record, indent=2) + '\n')
print('Discovered project:', config['name'])
print('Database user/name:', database['user'], database['name'])
print('Volume mapping:', json.dumps(volumes, sort_keys=True))
PY
```

Compare the printed project/volumes with `docker compose ls` and your installation.
The inventory records IDs without printing container environment values. Keep the
old images available; the next step also saves them for offline recovery.

## 2. Take a consistent maintenance snapshot

Schedule downtime. Disable admission at the external proxy/firewall, pause all
API clients/direct uploads and let in-flight requests finish. Stopping only the
UI does not stop clients using the API port. Keep PostgreSQL, Redis and media
available while workers drain. For workers predating the drain helper, first let
all active operations finish; a longer stop timeout cannot add drain support.

<!-- gw167:quiesce -->
```sh
python3 stop-worker-for-release.py --
docker compose stop gateway ui
```

Also stop enabled `https` and `webhook-receiver` services if present. The snapshot
block stops **all services of this project** after dumping PostgreSQL, including
Redis, HTTPS and monitoring, before archiving their volumes. An unclean worker
exit must abort the snapshot/update; investigate before retrying. Do not allow
any writer back until the snapshot and verification complete.

<!-- gw167:snapshot -->
```sh
python3 - <<'PY'
import json, os, pathlib, subprocess, tarfile
b = pathlib.Path(os.environ['GW_BACKUP']).resolve()
m = json.loads((b / 'inventory.json').read_text())
assert pathlib.Path.cwd().resolve() == pathlib.Path(m['app'])
DC = m['compose']
def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)
db = m['database']
assert not (b / 'database.dump').exists(), 'Use a new backup directory after failure'
# The worker guard above must succeed before reaching this block.
running = subprocess.check_output(DC + ['ps', '--status', 'running', '--services'], text=True).split()
assert not set(running) & {'gateway', 'worker', 'webhook-receiver'}, 'Stop all application writers first'
with (b / 'schema.txt').open('w') as out:
    run(DC + ['exec', '-T', 'postgres', 'psql', '-X', '-v', 'ON_ERROR_STOP=1',
              '-U', db['user'], '-d', db['name'], '-Atc',
              'SELECT version_id, is_applied FROM goose_db_version ORDER BY id'], stdout=out)
with (b / 'database.dump').open('wb') as out:
    run(DC + ['exec', '-T', 'postgres', 'pg_dump', '-Fc',
              '-U', db['user'], '-d', db['name']], stdout=out)
with (b / 'database.dump').open('rb') as data:
    run(DC + ['exec', '-T', 'postgres', 'pg_restore', '--list'], stdin=data, stdout=subprocess.DEVNULL)
run(DC + ['stop'])  # Cold snapshots of Redis, certificates and monitoring data.
for logical, actual in m['volumes'].items():
    if logical == 'postgres-data':
        continue  # Use the portable logical dump, never copy a running PGDATA.
    with (b / (logical + '.tar')).open('wb') as out:
        run(['docker', 'run', '--rm', '--network', 'none', '--entrypoint', 'tar',
             '--mount', 'type=volume,src=' + actual + ',dst=/snapshot,readonly',
             db['image'], '-cf', '-', '-C', '/snapshot', '.'], stdout=out)
with tarfile.open(b / 'bundle.tar.gz', 'w:gz') as archive:
    archive.add('.', arcname='bundle')  # Includes original .env and local overrides.
with (b / 'images.tar').open('wb') as out:
    run(['docker', 'image', 'save', *sorted(set(m['images'].values()))], stdout=out)
print('Snapshot saved; installation remains stopped pending verification')
PY
```

## 3. Verify before updating

An archive listing checks readability, not a successful recovery. Run the drill
in step 5 as well. Save a separate S3/bind-mount recovery record where applicable.

<!-- gw167:verify -->
```sh
python3 - <<'PY'
import hashlib, json, os, pathlib, subprocess, tarfile
b = pathlib.Path(os.environ['GW_BACKUP'])
m = json.loads((b / 'inventory.json').read_text())
assert (b / 'database.dump').stat().st_size > 0
with (b / 'database.dump').open('rb') as data:
    subprocess.run(['docker', 'run', '--rm', '-i', '--network', 'none',
                    '--entrypoint', 'pg_restore', m['database']['image'], '--list'],
                   stdin=data, stdout=subprocess.DEVNULL, check=True)
for name in ['bundle.tar.gz', 'images.tar'] + [key + '.tar' for key in m['volumes'] if key != 'postgres-data']:
    with tarfile.open(b / name) as archive:
        assert archive.getmembers(), 'Empty archive: ' + name
with tarfile.open(b / 'bundle.tar.gz') as archive:
    assert 'bundle/.env' in archive.getnames()
    assert 'bundle/compose.yaml' in archive.getnames()
hashes = {}
for p in sorted(b.iterdir()):
    if p.is_file() and p.name != 'sha256.json':
        digest = hashlib.sha256()
        with p.open('rb') as data:
            for chunk in iter(lambda: data.read(1024 * 1024), b''):
                digest.update(chunk)
        hashes[p.name] = digest.hexdigest()
(b / 'sha256.json').write_text(json.dumps(hashes, indent=2) + '\n')
print('Archives readable; SHA-256 manifest written')
PY
```

Keep the directory private and copy it off-host. On any failure, keep the failed
backup as evidence, recover the old installation and start a new backup attempt.
Do not update with an incomplete backup. A successful check leaves the old
installation stopped. Either proceed with [Updates](README.md#updates) using the
same project/configuration, or restart the unchanged installation in this order:
PostgreSQL/Redis, gateway, UI/HTTPS (if enabled), worker and monitoring. Use the
`--no-deps --wait` sequence in the README and check health before reopening traffic.

## 4. Application rollback is not database recovery

Record the pre-update `schema.txt` and the failed release's migration state before
choosing a rollback. Gateway startup with `MIGRATE=auto` applies migrations.
Use release-specific compatibility evidence; an older binary is not necessarily
compatible with the current schema, saved settings or accepted jobs. Do not run
down migrations or overwrite the database merely to roll back application images.
PostgreSQL/Redis major upgrades require a separate tested migration plan.

For a compatible application rollback, block traffic, use the **running release's**
worker drain helper, stop its gateway/UI/HTTPS, and return to the retained old
bundle and original `.env`/overrides with the **same project name and volumes**.
Do not run this against an incompatible schema. With PostgreSQL and Redis still
healthy, run from that old bundle (adapt custom Compose flags as in step 1):

<!-- gw167:rollback -->
```sh
python3 - <<'PY'
import json, os, pathlib, subprocess
b = pathlib.Path(os.environ['GW_BACKUP']).resolve()
m = json.loads((b / 'inventory.json').read_text())
assert pathlib.Path.cwd().resolve() == pathlib.Path(m['app']), 'Use the retained old bundle'
DC = m['compose']
config = json.loads(subprocess.check_output(DC + ['config', '--format', 'json']))
assert config['name'] == m['project']
assert all(config['volumes'][k]['name'] == v for k, v in m['volumes'].items())
running = subprocess.check_output(DC + ['ps', '--status', 'running', '--services'], text=True).split()
assert {'postgres', 'redis'} <= set(running), 'Recover existing database/cache service health first'
assert not set(running) & {'gateway', 'worker', 'ui', 'https'}, 'Stop application services first'
subprocess.run(['docker', 'image', 'load', '-i', str(b / 'images.tar')], check=True, stdout=subprocess.DEVNULL)
override = pathlib.Path('compose.rollback.json')
assert not override.exists(), 'Keep any earlier rollback evidence; choose a new override filename'
override.write_text(json.dumps({'services': {k: {'image': v} for k, v in m['images'].items()}}))
# For custom explicit -f flags, add this override after all original files instead.
files = next(line.split('=', 1)[1] for line in pathlib.Path('.env').read_text().splitlines() if line.startswith('COMPOSE_FILE='))
env = dict(os.environ, COMPOSE_FILE=files + ':compose.rollback.json')
for service in ('gateway', 'ui', 'https', 'worker'):
    if service in m['running_services']:
        subprocess.run(DC + ['up', '-d', '--no-deps', '--wait', service], env=env, check=True)
PY
```

This pins the saved application images and starts only previously running
application services, without replacing database/cache containers. Check health, existing sign-in, saved media and a free mock generation
before reopening. Image rollback does not revert schema changes or newer writes.

If compatibility is unknown or broken, keep traffic closed and diagnose forward
or approve a separate data-recovery operation. A database restore discards writes
after the recovery point and must be paired with the matching media/S3 objects,
Redis and encryption configuration. Preserve the failed state first. The drill
below restores into **new volumes** and performs no in-place deletion. A live
cutover, DNS/routing changes and disposal of old data are separate decisions.

## 5. Restore into a new, isolated installation

Use an empty directory outside the original installation. The commands refuse
existing containers/volumes for the new project. They reject external/fixed-name
volumes or networks, custom storage drivers/options and inherited container
volumes; adapt such configurations with an administrator first. Match
PostgreSQL major version (17 here), extensions, database user/name, Redis version,
application images and configuration to this snapshot. Do not restore a dump
into an older PostgreSQL version. Do not use `pg_restore --clean`, drop a live
database or run `docker compose down -v`.

Run from the directory containing the **verified backup**, with no application
clients or external writers attached to the recovery environment. The example
chooses a distinct Compose project and uses the saved images. It starts only
PostgreSQL and Redis; gateway/worker/HTTPS/monitoring remain stopped until reviewed.

```sh
export GW_RESTORE="$(pwd)/gateway-recovery-$(date -u +%Y%m%dT%H%M%SZ)"
export GW_RECOVERY_PROJECT="gateway-recovery-$(date -u +%Y%m%d%H%M%S)"
```

<!-- gw167:restore -->
```sh
python3 - <<'PY'
import hashlib, json, os, pathlib, subprocess, tarfile
b = pathlib.Path(os.environ['GW_BACKUP']).resolve()
target = pathlib.Path(os.environ['GW_RESTORE']).resolve()
project = os.environ['GW_RECOVERY_PROJECT']
m = json.loads((b / 'inventory.json').read_text())
assert project != m['project'], 'Recovery must use a new Compose project'
assert not target.exists(), 'Recovery directory must be new'
assert not subprocess.check_output(['docker', 'ps', '-aq', '--filter', 'label=com.docker.compose.project=' + project]).strip()
assert not subprocess.check_output(['docker', 'volume', 'ls', '-q', '--filter', 'label=com.docker.compose.project=' + project]).strip()
for name, expected in json.loads((b / 'sha256.json').read_text()).items():
    digest = hashlib.sha256()
    with (b / name).open('rb') as data:
        for chunk in iter(lambda: data.read(1024 * 1024), b''):
            digest.update(chunk)
    assert digest.hexdigest() == expected, 'Backup checksum mismatch: ' + name
# Extract only a trusted, self-created backup, never an untrusted archive.
target.mkdir(mode=0o700)
with tarfile.open(b / 'bundle.tar.gz') as archive:
    archive.extractall(target)
app = target / 'bundle'
env = {key: os.environ[key] for key in ('PATH', 'HOME', 'DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_CONFIG') if key in os.environ}
env['COMPOSE_PROJECT_NAME'] = project  # Overrides the preserved .env, without editing secrets.
DC = ['docker', 'compose', '--project-name', project]  # Add original file/env flags if needed.
def dc(*args, **kwargs):
    return subprocess.run(DC + list(args), cwd=app, env=env, check=True, **kwargs)
config = json.loads(dc('config', '--format', 'json', stdout=subprocess.PIPE).stdout)
assert config['name'] == project
existing_volumes = subprocess.check_output(['docker', 'volume', 'ls', '-q'], text=True).split()
for volume in config.get('volumes', {}).values():
    assert not volume.get('external'), 'Recovery must not reuse external volumes'
    assert not volume.get('driver_opts') and volume.get('driver', 'local') == 'local', 'Custom volume storage needs an adapted recovery drill'
    assert volume['name'] not in m['configured_volumes'].values(), 'Fixed-name volume would reuse source data'
    assert volume['name'] not in existing_volumes, 'Recovery volume already exists'
assert set(m['volumes']) <= set(config.get('volumes', {}))
existing_networks = subprocess.check_output(['docker', 'network', 'ls', '--format', '{{.Name}}'], text=True).split()
for network in config.get('networks', {}).values():
    assert not network.get('external'), 'Recovery must not reuse external networks'
    assert not network.get('driver_opts') and network.get('driver', 'bridge') == 'bridge', 'Custom networks need an adapted recovery drill'
    assert network['name'] not in m['networks'].values(), 'Fixed-name network would reuse the source network'
    assert network['name'] not in existing_networks, 'Recovery network already exists'
for service in config['services'].values():
    for mount in service.get('volumes', []):
        if mount['type'] == 'bind':
            source = pathlib.Path(mount['source']).resolve()
            assert source == app or app in source.parents, 'Recovery bind must be inside the recovered bundle'
    assert not service.get('network_mode'), 'Review custom host/container networking before recovery'
    assert not service.get('volumes_from'), 'Review inherited container volumes before recovery'
# Pin every saved service to the actual original image, including optional profiles.
(app / 'compose.recovery.json').write_text(json.dumps({'services': {
    service: {'image': image} for service, image in m['images'].items()}}))
files = env.get('COMPOSE_FILE') or next(line.split('=', 1)[1] for line in (app / '.env').read_text().splitlines() if line.startswith('COMPOSE_FILE='))
env['COMPOSE_FILE'] = files + ':compose.recovery.json'
subprocess.run(['docker', 'image', 'load', '-i', str(b / 'images.tar')], check=True, stdout=subprocess.DEVNULL)
# Create containers/volumes but start no application or public listener.
dc('create', '--no-build', '--pull', 'never')
dc('up', '-d', '--no-deps', '--wait', 'postgres')
db = m['database']
with (b / 'database.dump').open('rb') as data:
    dc('exec', '-T', 'postgres', 'pg_restore', '--exit-on-error', '--single-transaction',
       '--no-owner', '--no-privileges', '-U', db['user'], '-d', db['name'], stdin=data)
for logical in m['volumes']:
    if logical == 'postgres-data':
        continue
    actual = config['volumes'][logical]['name']
    with (b / (logical + '.tar')).open('rb') as data:
        subprocess.run(['docker', 'run', '--rm', '-i', '--network', 'none', '--entrypoint', 'tar',
                        '--mount', 'type=volume,src=' + actual + ',dst=/snapshot',
                        db['image'], '-xf', '-', '-C', '/snapshot'], stdin=data, check=True)
with (target / 'restored-schema.txt').open('w') as out:
    dc('exec', '-T', 'postgres', 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-U', db['user'],
       '-d', db['name'], '-Atc', 'SELECT version_id, is_applied FROM goose_db_version ORDER BY id', stdout=out)
assert (target / 'restored-schema.txt').read_bytes() == (b / 'schema.txt').read_bytes()
dc('up', '-d', '--no-deps', '--wait', 'redis')
(target / 'compose-files.txt').write_text(env['COMPOSE_FILE'])
print('Database, volumes and original configuration restored into:', project)
print('Application services remain stopped; review isolated routing/storage before startup')
PY
```

The preserved `.env` still contains the **original** project and endpoints. For
all later recovery commands use the explicit new project and saved file list;
never run bare `docker compose up` there. Before starting applications, review
restored provider/email/payment/webhook/S3 settings and pending jobs. Use an
isolated host/network with outbound access blocked when checking real data.
Shared S3 writes and real integrations must stay disconnected during the drill;
use separately restored objects and synthetic settings. Starting a worker can
resume paid jobs and outgoing webhooks. This guide does not authorize that.

For the synthetic drill, choose unused loopback ports, then run this block. It
writes a final Compose override setting `MIGRATE: off` explicitly: setting it in
`.env` alone cannot override the base Compose value `auto`. It starts only the
gateway with public URLs pointing at its API port; UI/HTTPS/monitoring/worker
stay stopped. Custom Compose file/env flags
must point at their recovered copies, never the original installation.

```sh
export GW_RECOVERY_API_PORT=18080
export GW_RECOVERY_UI_PORT=18088
```

<!-- gw167:startup -->
```sh
python3 - <<'PY'
import json, os, pathlib, subprocess
root = pathlib.Path(os.environ['GW_RESTORE']).resolve()
app = root / 'bundle'
project = os.environ['GW_RECOVERY_PROJECT']
api, ui = int(os.environ['GW_RECOVERY_API_PORT']), int(os.environ['GW_RECOVERY_UI_PORT'])
assert 1024 <= api <= 65535 and 1024 <= ui <= 65535 and api != ui
env = {key: os.environ[key] for key in ('PATH', 'HOME', 'DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_CONFIG') if key in os.environ}
env.update(COMPOSE_PROJECT_NAME=project, COMPOSE_FILE=(root / 'compose-files.txt').read_text())
DC = ['docker', 'compose', '--project-name', project]
config = json.loads(subprocess.check_output(DC + ['config', '--format', 'json'], cwd=app, env=env))
text = ('services:\n  gateway:\n    environment:\n      MIGRATE: "off"\n'
        f'      AUTH_PUBLIC_URL: "http://127.0.0.1:{api}"\n      UI_PUBLIC_URL: "http://127.0.0.1:{api}"\n'
        f'    ports: !override\n      - "127.0.0.1:{api}:8080"\n')
if 'ui' in config['services']:
    text += f'  ui:\n    ports: !override\n      - "127.0.0.1:{ui}:8080"\n'
if 'https' in config['services']:
    text += '  https:\n    profiles: ["recovery-disabled"]\n    ports: !reset []\n'
(app / 'compose.recovery-check.yaml').write_text(text)
env['COMPOSE_FILE'] += ':compose.recovery-check.yaml'
config = json.loads(subprocess.check_output(DC + ['config', '--format', 'json'], cwd=app, env=env))
assert config['name'] == project and config['services']['gateway']['environment']['MIGRATE'] == 'off'
subprocess.run(DC + ['up', '-d', '--no-deps', '--wait', 'gateway'], cwd=app, env=env, check=True)
print('Isolated gateway started without migrations on loopback port', api)
print('For later commands, use --project-name', project, 'and COMPOSE_FILE=' + env['COMPOSE_FILE'])
PY
```

Validate the recovery before migration/traffic: compare migration history,
important record counts, local media/reference hashes, original account/API-key
access and decryption of saved provider/storage settings. Check saved downloads.
For synthetic data only, use the printed project/file selection to start the
worker with `up -d --no-deps worker` and run a new free mock generation. Review
and adapt the UI's public URLs if using the browser. Match the original public
URLs again only for an approved live cutover. Check optional HTTPS certificates
and monitoring state when those modes are used. A listing/checksum pass alone is
insufficient. Keep the original installation and backup until recovery has been
accepted; no cleanup command here deletes them.
