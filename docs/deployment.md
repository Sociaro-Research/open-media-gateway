# Manual server deployment

For the configured production repository, use the
[local checks and deployment launcher](../tools/release/README.md). Commit and
push with GitHub Desktop, select the required checks and **Deploy to server**,
then choose **Run selected actions**. Local checks and additional CI checks are
independent; selecting only deployment skips both. Deployment still requires a
clean, already-pushed `main` commit and retains the backup and server checks.
[GitHub Actions deployment](github-actions-deployment.md)
remains an optional manual fallback that consumes Actions minutes. The procedure
below covers administrator-operated installation and recovery.

For installation from published images without a source checkout, see
[GHCR releases](ghcr-release.md). Artifact publication and server deployment
are separate procedures.

For the optional monitoring profile, authenticated Grafana dashboard, private
Prometheus service, metrics listeners and advanced SSH access,
see [Monitoring](monitoring.md). Enable the monitoring profile explicitly after
the normal application release checks; preserve its `prometheus-data` and
`grafana-data` volumes. Pull the pinned Grafana image, start Prometheus and then
start Grafana with `up -d --no-deps --wait grafana`. The normal versioned UI
handles `/monitoring/`; no external routing or separate login credentials are
required. Verify anonymous/non-Administrator denial, the provisioned dashboard
and live queries after release. Back up the Grafana database before a future
Grafana version upgrade; the application archive does not include Docker volumes.

Grafana runs as a non-root user and must be able to traverse and read both
bind-mounted directories under `monitoring/grafana/`. Preserve the directory
modes from the Git archive when extracting a release. A custom release utility
using a restrictive umask must restore those source-directory modes; changing
only file modes can leave provisioning silently empty. Confirm the dashboard
and datasource are present before recording the release as verified.

Deploy only when authorized. The release branch is `main`; the server is
`root@gateway.example.com`, and the application directory is `/opt/media-gateway/app`.
The backend and worker image excludes the frontend. The optional `ui` service
has its own image and serves pages while proxying API requests to the gateway.

## Prepare a release

Follow the publishing checks in [AGENTS.md](../AGENTS.md): use isolated PostgreSQL
and Redis for `make verify-ci`, run `git diff --check` and `make release-check`,
fetch and integrate `origin/main`, commit, push without force, and check the
release's CI results when accessible. If CI status is inaccessible, report that
limitation and run the relevant checks directly before rollout, including both
Docker image builds and UI configuration validation. Keep the committed release checked out as `HEAD` during
transfer. Never publish server credentials or copy a local `.env` to the server.

## Back up before changing the server

Create a unique UTC timestamp directory under `/opt/media-gateway/backups/` with
mode `0700` and `umask 077`. Save:

- A tar archive of the complete application directory, including server configs.
- A PostgreSQL custom-format dump (`pg_dump -Fc -U media_gateway -d media_gateway`
  via `docker compose ... exec -T postgres`).
- The current gateway, worker and UI container image IDs, when present, and the
  release SHA, if known. Retain those images until the release is verified.

Verify the application archive with `tar -tzf` and the database dump with
`pg_restore --list` inside the existing PostgreSQL container. Abort on failure.
Check disk capacity before backups and builds. Do not delete backups, active
images, volumes or other applications to make room automatically.

Transfer the committed source with your server/key, excluding `.env`, the root
`Caddyfile`, and `compose.staging.yaml`. The included `web/Caddyfile` is the
versioned UI configuration and must be updated with the release:

```sh
DEPLOY_HOST=root@gateway.example.com
DEPLOY_SSH_KEY="$HOME/.ssh/gateway_ed25519"
git archive --format=tar HEAD | ssh -i "$DEPLOY_SSH_KEY" \
  -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes \
  "$DEPLOY_HOST" 'tar --anchored --exclude=.env --exclude=./.env --exclude=Caddyfile --exclude=./Caddyfile --exclude=compose.staging.yaml --exclude=./compose.staging.yaml -xf - -C /opt/media-gateway/app'
```

The extraction exclusions are anchored to the archive root: an unanchored
`Caddyfile` exclusion also skips `web/Caddyfile` and breaks the UI image build.

Archive extraction does not remove files deleted or moved in Git. Before building,
compare the previous release's tracked file list with the new commit and remove
only obsolete tracked files whose contents match that previous release. Stop if
an obsolete file contains a server modification. Keep server-only configs and
untracked files. For the first frontend separation upgrade, these obsolete paths
are `internal/api/ui.go` and the former tracked files under `internal/api/ui/`;
leaving them behind makes the Go image fail to build.

After verification, record `git rev-parse HEAD` and `git ls-tree -r --name-only HEAD`
under `/opt/media-gateway/releases/<SHA>/`, outside the overlaid application tree.
Use that record as the previous version on subsequent deployments. Rollback must
likewise remove files introduced by the failed release before restoring old source.

## First upgrade to the separate UI

Preserve all existing secrets and URL values. For the current two-domain setup,
add these non-secret settings to the server `.env`:

```dotenv
UI_PUBLIC_URL=https://gateway.example.com
DOCS_HOST=docs.example.com
```

Keep `AUTH_PUBLIC_URL` and `DOCS_PUBLIC_URL` at their existing values. Do not run
`gateway admin init` on an existing installation; users and keys remain in the
same database.

Merge this into the existing server-only Compose override without changing its
database password configuration, published TLS ports or persistent volumes:

```yaml
services:
  ui:
    restart: unless-stopped
  caddy:
    depends_on:
      ui:
        condition: service_healthy
```

In the server's root `Caddyfile`, change the upstream in both existing public
site blocks from `gateway:8080` to `ui:8080`. Preserve TLS, headers, hostnames and
logging. The UI proxy preserves Host; the gateway enforces public host checks.
Using the existing shared console/API origin preserves browser cookies and OAuth
callbacks. A different frontend origin requires a separate authentication design.

Build and validate all three images before stopping anything. Follow the
[worker-drain safeguards](#later-releases-and-rollback) before replacing the
gateway or applying migrations. Start the gateway and wait for its health check,
then start the UI and wait for its health check. Start the worker next; validate
and reload the preserved external Caddy configuration last. Complete the
[post-deployment checks](#verify-and-record) before declaring the upgrade
successful. The UI's health check probes a public static asset; separately verify
API reachability and session gating. A brief maintenance window is possible during
this first routing transition.

Edit mounted server config files in place. Replacing a bind-mounted file by
rename can leave the container reading its previous inode; if a file is replaced,
recreate the affected Caddy container before validating/reloading.

## Verify and record

Check service health, database migration state, both public domains and:

- Console sign-in shell, legal pages and the current UI assets.
- Anonymous private-script rejection and authenticated console access.
- Documentation loading its real OpenAPI contract; docs-host API/auth routes
  must return 404.
- `/healthz`, `/site.json`, API models and an authenticated management request.
- A free generation with an explicitly selected `mock` model, completion and
  result download. Use a normal scoped test key, revoke it afterwards, and keep
  key material out of logs. Never substitute a paid provider for this check.

Use a descriptive User-Agent for scripted HTTP requests. Record the release SHA,
backup directory, image IDs and check results in the deployment report. Do not
claim real email delivery, payment or third-party provider tests from a mock test.

## Later releases and rollback

### Security maintenance images and origin policy

For installations using the hardened database/cache images, merge these entries
into the existing server-only override. Preserve passwords, volumes, restart
policies and all other environment settings:

```yaml
services:
  postgres:
    image: media-gateway-postgres:17-hardened
    build:
      context: ./deploy/postgres
  redis:
    image: media-gateway-redis:7.4-hardened
    build:
      context: ./deploy/redis
  caddy:
    image: caddy:2.11.7-alpine
```

The PostgreSQL image keeps official PostgreSQL 17 and its entrypoint. It rebuilds
the same gosu 1.19 source commit on Go 1.27.3 with updated x/sys; the upstream
helper was built with Go 1.24.6. Both images apply available Alpine patches.
Build with `docker compose ... build --pull postgres redis` during planned
security maintenance, scan the resulting images and test disposable initialization,
backup restoration and restart before replacing persistent services. Do not
upgrade PostgreSQL or Redis's major version through these files.

After the verified backups and a clean worker drain, stop gateway admission,
replace PostgreSQL/Redis with `up -d --no-deps --wait postgres redis`, then follow
the normal gateway/UI/worker release order. Keep every named volume. Never
overwrite a live database with the disposable restoration test.

Copy the definitions in `deploy/cloudflare-origin.caddy` and
`deploy/access-log-redaction.caddy` into the preserved external Caddyfile. In each
public site block, import `cloudflare_origin_only` and replace its existing `log`
directive with `import redacted_access_log`. The snippets must be present in the
mounted file or separately mounted into Caddy; a source-directory file alone is
not visible in the container. Validate before reload. Confirm public requests
through Cloudflare still succeed, direct-origin requests fail even with forged
forwarding headers, and synthetic API keys/signed-link parameters do not persist
in new logs. Existing logs/backups require separate access/retention assessment.

Cloudflare's shared IP ranges restrict network peers but do not authenticate
your particular zone. Zone-specific authenticated origin pulls, a tunnel or a
provider firewall remain additional account-dependent protections. Refresh the
official IP ranges before changing the policy.

The UI image rebuilds Caddy 2.11.7 on Go 1.27.3 with x/net 0.60.0, retaining
the upstream command and standard modules. This incorporates the October 8 Go
security fixes without changing Caddy's version or the separate TLS proxy.
Database/cache image replacement remains a separate maintenance operation.

The stripped Caddy 2.11.7 binary triggers a conservative OpenPGP module
finding in govulncheck. The actual Go function table contains no OpenPGP functions;
the same inspection detects and rejects a control binary that imports OpenPGP.
CI enforces that check with `scripts/security/check-go-binary.go`; do not blanket
suppress module warnings or reuse the disposition after a binary changes.

For normal releases, rebuild and update `gateway worker ui` with the UI profile
enabled. Before replacing the gateway or applying its migrations, use:

```sh
python3 scripts/stop-worker-for-release.py -- --profile ui -f compose.yaml -f compose.staging.yaml </dev/null
```

The helper reads the incoming and running workers' drain budgets, waits for a clean stop
and verifies every stopped worker container exited with code zero. Its default
Docker stop allowance is the drain budget plus 60 seconds. A failed/forced stop
aborts the release before gateway replacement; inspect the worker logs and
recover pending work before retrying. Do not continue with the next release
commands after that failure. Keep PostgreSQL, Redis and media volumes available
during draining. Ordinary in-flight asynchronous jobs resume in the new worker;
current synchronous requests and result transfers must finish first. The
existing schema and configured providers must remain compatible with accepted
jobs in the next release.

When first upgrading from a worker without this drain behavior, stop new
admissions and let its active operations finish before stopping it. Giving the
old worker more Docker stop time does not change its cancellation behavior.
Subsequent releases use the guarded procedure above.

When first adopting temporary-file cleanup, replace all old gateway and worker
writers before starting the new worker's sweeper. Old writers do not hold the
kernel locks used to protect active local/reference files. See
[storage cleanup and the legacy grace period](configuration.md#storage).

For frontend-only changes, after backup and image validation, rebuild
and replace only `ui`; gateway and worker need no restart. For an API-only
installation, omit UI and route API traffic directly to the gateway instead.

If release checks fail, stop the worker, restore the application archive and its
server configs, retag the recorded old images to the corresponding Compose image
names, and recreate the previous gateway/worker/proxy services. Restore the old
external proxy routing before declaring rollback complete. Stop the new UI if the
previous release did not have one; preserve every volume. Recheck health and API.

Application rollback does not automatically restore the database. Inspect schema
compatibility first; a database restore overwrites newer writes and requires an
explicit maintenance/data-recovery decision. Keep the verified dump available.

### Private result index (migration 065)

This release adds result_objects as a separate index for completed private
result attempts, so normal owner-authorized deletion can find losing objects
without charging shared quota. Migration 065 only creates the table and index;
it does not backfill or delete old data. Follow the normal verified-backup and
worker-drain gates before gateway startup applies migrations.

The previous embedded 001–064 migration set was tested against schema 65 and
accepts it. Application rollback preserves the additive table, migration version
and recorded private keys; do not run the down migration or restore a database
as part of ordinary application rollback. An older application ignores the new
private deletion index until the corrected application returns. Inspect the
actual deployed SHA/schema before rollout; local compatibility checks do not
establish the server's current state.

A populated isolated 64→65 upgrade also preserves old canonical and bare
result/last-frame keys, task history, and known/unknown quota rows. Repeated
normal result deletion remains supported. The private index starts empty; old
private losing objects are not backfilled. New private attempts retain access
to their original S3 connection through the live owner-scoped index when the
current resource mode changes. Preserve the existing storage connection records
and encryption configuration through application rollback.
