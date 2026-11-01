# Media Gateway @VERSION@

Ready-made Linux AMD64/ARM64 images. Requirements: Docker Engine (or Docker
Desktop), Compose **2.24.4+** and Python **3.9+**. No source checkout or compiler.
Run the commands below from this extracted directory, on the machine that will
host Gateway. For private packages, authenticate to GHCR first (see below).
On Windows, first read the included `docs/windows-wsl.md` guide for Docker Desktop
and Ubuntu in WSL 2, then run these commands in Ubuntu with Desktop running.

The included guides work offline. Links to source-only guides point to the
matching release tag in the project repository rather than missing local files.

## Quick start

### Local installation

```sh
python3 initialize.py
docker compose pull
docker compose up -d --wait
```

Open **http://localhost:8088**. Console and API share this origin.

### Server installation

Point your domain's A record (and AAAA, if used) at the server. Allow inbound TCP
**80 and 443** and keep those ports free; outbound HTTPS must also be available.
Replace the example domain, then run:

```sh
python3 initialize.py --public-url https://gateway.example.com
docker compose pull
docker compose up -d --wait
```

Open **https://gateway.example.com**. The included [Caddy proxy](https://caddyserver.com/docs/automatic-https)
obtains and renews certificates and redirects HTTP to HTTPS. Certificate issuance requires working
public DNS and reachable ports; allow a little time after services start. The
proxy uses the same pinned UI image, so no extra image or build is needed.

### Create the first owner

```sh
docker compose exec -T gateway /gateway admin setup-code
```

Enter this private code in **Create owner account**, with your name, email and
password (15–128 characters). You are immediately signed in as Administrator.
No email provider or Google account is required. Configure providers, email and
storage in **Administration** afterward; `mock` works for free without keys.
The setup code stops working after the first account is created. Keep it out
of URLs and public messages.

The initializer creates `.env` with independent random secrets and private file
permissions. It sets the public URLs, enables browser login and selects the
right Compose files automatically. Running it again leaves an existing `.env`
unchanged. Keep this file and the named volumes for restarts and updates.
PostgreSQL, Redis and worker have no published ports. Server mode exposes only
80/443; local mode binds the API and console to loopback.

## Check or troubleshoot installation

```sh
docker compose ps
curl --fail http://localhost:8088/healthz
```

On a server, use `curl --fail https://gateway.example.com/healthz`. Expect HTTP
200 and a working **Create owner account** form (or sign-in after setup).
`docker compose up --wait` checks service health; verify the HTTPS URL as well,
since that also checks public routing and certificate issuance.

| Symptom | Action |
| --- | --- |
| Cannot connect to Docker | Start Docker; check `docker info` and your user's Docker permissions. |
| Compose rejects `!reset` | Upgrade Compose to 2.24.4 or newer. |
| Pull denied | Check package visibility or authenticate to GHCR as described below. |
| Port already in use | Use another local port or existing-proxy mode below; server mode needs 80/443. |
| HTTPS does not open | Check A/AAAA records, firewall and `docker compose logs --tail=100 https`. For a DNS proxy, use end-to-end HTTPS (Cloudflare: Full (strict)), or DNS-only during setup. |
| Gateway or UI unhealthy | Check `docker compose logs --tail=100 gateway ui postgres redis`; keep secrets private when sharing logs. |
| No owner form | Use this release's images with the generated `.env`; an existing database retains its accounts and never reopens owner setup. |
| Setup code unavailable after setup | Sign in with the existing owner. The one-time code is not a password-recovery method; see `docs/system-administration.md`. |

## Other installation options

**Existing TLS reverse proxy:** keep the generated public URLs but omit the
included HTTPS service:

```sh
python3 initialize.py --public-url https://gateway.example.com --behind-proxy
```

Then run the same `docker compose pull`, `up -d --wait` and owner-setup commands.
Proxy your domain to **http://127.0.0.1:8088**, preserving the original Host.
Route all requests through the UI service so its authentication proxy applies.
This mode is for a proxy on this host; a containerized proxy needs a deliberately
configured shared network or host route. Keep the backend/database private.

**Second installation on the same machine:** extract into another directory and
use a different project name and local ports:

```sh
python3 initialize.py --project-name gateway-test1 --api-port 18080 --ui-port 18088
```

Open `http://localhost:18088`. For a second public domain on a host already using
80/443, combine these options with `--public-url` and `--behind-proxy`, then add
a route to the existing proxy at `http://127.0.0.1:18088`. Distinct project names
give installations separate database, Redis, media and certificate volumes and
networks; keep their `.env` files independent. They still share the host/Docker.

**API-only:** clear `COMPOSE_PROFILES`, set both public URLs to the API origin
(locally `http://localhost:8080`), and leave out `compose.https.yaml`. Instead of
browser owner setup, run `docker compose exec -T gateway /gateway admin init
--email admin@example.com`. This creates a management API key, closes browser
setup and creates no password. See `docs/frontend-separation.md` for recovery.
`CLIENT_API_KEY` is a separate service credential, not a browser/Playground login.

## Image access and configuration

Images are pinned by digest; `images.json` records the version and immutable
references. Maintainers publish new images and a matching bundle for each
release. Users download the bundle and pull images, without building them.
No Docker Hub account is needed. Public GHCR packages allow anonymous pulls;
private packages require GitHub access and a token with `read:packages`.
Run `docker login ghcr.io --username YOUR_GITHUB_USER --password-stdin` and feed
the token through standard input; never put it in a command argument. Repository
visibility is independent of package visibility.

`.env` accepts the variables documented in `docs/configuration.md`. Generate
credentials only on a fresh installation; do not change encryption/signing keys
or database passwords on an existing installation without a migration plan.
The default local media limit is 2 GiB (`STORAGE_MAX_BYTES`); select suitable
storage/capacity before normal use.

## Third-party notices

The Gateway's own code is licensed under Apache License 2.0; see the root
`LICENSE` and `NOTICE`. Redis 7.4.11 is a separate third-party component with its
own upstream terms. The installation bundle includes its complete license,
copyright and contribution notices, bundled dependency notices and provenance
under `licenses/redis/`. The Redis image includes the same files under
`/licenses/redis/`. Keep these notices with redistributed release artifacts.
See that directory's `README.md` for the exact upstream source and file hashes.
These retained third-party terms do not change the Gateway's Apache-2.0 license.

## Monitoring

Set `COMPOSE_PROFILES=ui,monitoring` in `.env`, then run
`docker compose up -d --wait`. Administrators can open `/monitoring/` through the
console. Prometheus is localhost-only; Grafana has no published port. Preserve
the readable modes of the packaged monitoring directories and all named volumes.

## Updates

Complete [Backup, verification and recovery](backup-restore.md) **before**
upgrading. It includes database/volume discovery, a maintenance snapshot, a
separate-project recovery drill and application rollback. Extract the next
bundle to a new directory.
Copy the existing `.env` into it, preserving secrets, public URLs, mode and
`COMPOSE_PROJECT_NAME`. Replace only the four `*_IMAGE` values with those in the
new `.env.example`. Do not rerun initialization. Use the same Compose project
name as before (`docker compose ls`), or Docker will create fresh volumes.
Keep PostgreSQL 17 and Redis 7.4; major upgrades need a separate data migration.

```sh
docker compose pull
python3 stop-worker-for-release.py --
docker compose stop gateway ui
docker compose up -d --no-deps --wait postgres redis
docker compose up -d --no-deps --wait gateway
docker compose up -d --no-deps --wait ui
docker compose up -d --no-deps worker
```

In included-HTTPS mode also run `docker compose up -d --no-deps --wait https`
to update the proxy image. For API-only mode omit both UI/proxy commands. For
monitoring, also update Prometheus/Grafana after backing up their volumes.
Stop on any error; never use `down -v`. Inspect `docker compose ps`, the public
`/healthz`, sign-in and a free mock generation/download after an update. Keep
the old bundle and images for rollback; image rollback does not undo migrations.
