# Manual production deployment from GitHub Actions

The [local launcher](../tools/release/README.md) is the primary operator workflow.
This manual fallback consumes GitHub Actions minutes; no push/PR/tag starts it.

Open **Actions → Deploy production → Run workflow**, keep branch **main**, and
run it. Pushing commits does not deploy. No local release utility, version tag,
GHCR login or personal computer is required for a configured repository.

The workflow deploys the exact commit selected when the run starts. It reuses
the complete CI workflow (security, isolated Go/PostgreSQL/Redis, container and
browser checks). CI concurrency is scoped to the caller workflow so a normal
push cannot cancel the deployment's checks. A failed check prevents deployment.
CI and deployment runners use Ubuntu 24.04 explicitly; operating-system upgrades
are reviewed changes rather than automatic `ubuntu-latest` migrations.
Only connectivity and deployment jobs receive `production` environment secrets. The
workflow's GitHub token has read-only repository access.

Deployment transport, public origin and verified identities are supplied by an
operator-owned private profile; no installation endpoint or pin is distributed
with this project. See [deployment configuration](deployment-configuration.md).
The tunnel routes only one explicitly configured server host and authorizes only
one client host; its INPUT rules permit SSH and block Docker forwarding.

For hosted runners, configure the production environment secrets
`PRODUCTION_DEPLOY_PROFILE` (version 1 JSON without private key contents),
`PRODUCTION_DEPLOY_KNOWN_HOSTS` (independently verified Ed25519 entries), and
`PRODUCTION_DEPLOY_WG_SERVER_PUBLIC_KEY`, alongside the two existing credential
secrets described below. The workflow writes these to private temporary files;
missing configuration fails closed. Do not commit these values or obtain pins
with unverified key discovery. The optional `PRODUCTION_PUBLIC_URL` variable is
only the environment display link; the reviewed profile controls transport.

## One-time setup

### Private transport

The production firewall permits SSH only from operator addresses. Hosted runner
addresses change, so direct SSH from a hosted runner is not the deployment path.
For a new installation, an administrator must first:

1. Install `wireguard-tools` from Ubuntu's official package repository on the server.
2. Generate a dedicated client key with `wg genkey` into a mode-0600 private file,
   and derive its public key with `wg pubkey`. Never print the private key or put
   it in a command argument, repository or log.
3. Run `scripts/deployment/install-network.py --client-public-key PUBLIC_KEY`
   as root on the server. The installer creates its own server key, backs up
   network rules, sets up `wg-deploy`, and returns only its server public key.
   It refuses to overwrite an existing tunnel. Preserve the returned public key in the private profile
   trust directory and configure the corresponding environment secret.
4. Permit inbound **UDP 51820 from IPv4** in your server firewall.
   Keep the existing TCP 22 source restrictions. WireGuard authenticates the
   configured peer cryptographically; changing runner IPs needs no rule updates.
5. Store the client private key in environment secret
   **PRODUCTION_DEPLOY_WG_PRIVATE_KEY** in the main-only `production` environment.

The server peer accepts only `192.0.2.2/32`. The runner routes only
`192.0.2.1/32`; it changes no DNS or default route. Interface-specific rules
deny other host ports and all Docker forwarding. The service is ordered after
Docker and restarts with it so its forwarding rule is reapplied. The tunnel is
removed and its temporary configuration deleted when a runner command exits.
SSH continues to verify the pinned public host identity through `HostKeyAlias`.

### Restricted SSH deployment

The server needs Python 3.9+, Docker Compose, the existing running installation,
and a valid `/opt/media-gateway/current-release` marker. An administrator must:

1. Generate a dedicated Ed25519 SSH key for this repository's Actions deployment.
   Do not reuse or replace a personal GitHub or server administration key.
2. Install `scripts/deployment/{entry,remote,smoke}.py` in a private versioned
   directory under `/opt/media-gateway/deployer/`; point its `current` symlink at
   that version. Back up the existing SSH authorization file first.
3. Add the public key to the server's root `authorized_keys` with exactly these
   restrictions (followed by the public key):

   ```text
   restrict,command="/usr/bin/python3 /opt/media-gateway/deployer/current/entry.py"
   ```

   This key accepts only `status`, `start <sha> <archive-sha256> <previous-sha>`
   and `poll <job-id> <offset>`. It provides no interactive shell or forwarding.
   Deployment necessarily executes the released application's build and runtime
   code; protect write access to `main` and the deployment workflow accordingly.
4. Create GitHub environment **production**, restrict deployment branches to
   **main**, and add environment secret **PRODUCTION_DEPLOY_SSH_KEY**, containing
   this dedicated private key. Do not put it in a repository file, artifact or log.
5. Confirm the server's Ed25519 host key independently. The pinned public host key
   is in the private profile's `known_hosts_file`. Never replace it with an unverified
   `ssh-keyscan` result or disable strict host checking.
6. Run **Deploy production** and verify the complete run before relying on it.

After a successful release, the server atomically selects deployment scripts
from that verified release for future SSH sessions. Running jobs retain their
original installed script version. Permanent administration keys and the manual
procedure in [deployment.md](deployment.md) remain available for recovery.

## Safety and result verification

The client requires a clean exact-commit checkout and full Git history. It
rejects links, submodules, tracked server configuration, archive omissions and
deployments older than the current production release. The manifest contains
new file hashes and old content hashes for removed tracked files.

The server validates upload/archive integrity and paths, checks free space,
backs up the full application and PostgreSQL under
`/opt/media-gateway/backups/<UTC>-<SHA>/` with private permissions, and checks
both archives are readable before changing application files. It preserves
`.env`, root `Caddyfile`, `compose.staging.yaml`, untracked runtime data and
Docker volumes. Modified obsolete source files stop deployment rather than
being discarded. Source directory modes are restored for non-root services.

It builds gateway, worker and independent UI; validates both Caddy configs;
uses the existing worker drain gate; replaces services in the documented order;
and checks running services, migrations, public UI assets, authentication/docs
isolation, `/healthz`, and an explicit free mock generation including persisted
zero actual cost and downloading the result. A successful deployment updates
both release markers and writes a report to
`/opt/media-gateway/releases/<SHA>/deployment.json`.

GitHub concurrency serializes manual runs without cancelling an active release.
The server also uses `/opt/media-gateway/release.lock`, shared with the local
launcher. After the upload is accepted, a detached server process owns deployment;
closing the browser or losing an SSH status connection does not interrupt it.
Actions reconnects to read progress. Workflow output prefixes server log lines
so they cannot execute GitHub workflow commands.

## Failure, retries and revocation

Read the failed step and the server job ID in the run summary. Logs and final
state are stored privately under `/opt/media-gateway/actions-jobs/<job-id>/`;
the upload is deleted when the job exits. A failed or timed-out run is never
reported as a verified deployment. After an Actions cancellation or timeout,
inspect the server job before retrying: it may still be completing normally.
If the server itself restarts, an interrupted job requires administrator review.

There is no automatic database rollback: production may have accepted new
financial operations after a migration. Use the printed backup and the manual
recovery procedure deliberately. Do not purge backups, images or volumes to fix
a failure. Retain logs/backups according to the operator's capacity policy.

To revoke deployment access, remove this dedicated public key's line from
`authorized_keys` and remove the environment secret. Rotate only this key when
needed; preserve all unrelated administration keys. To bootstrap a repaired
runner, install a reviewed version under a new version directory and change the
`current` symlink atomically while no deployment is running.

To revoke the private transport, remove `PRODUCTION_DEPLOY_WG_PRIVATE_KEY`, stop
and disable `wg-quick@wg-deploy`, and remove only its UDP rule from your hosting firewall.
Preserve unrelated SSH keys, firewall rules and other network interfaces.
