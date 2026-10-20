# GHCR releases

## Install

Start with the [Quick start](../README.md#quick-start). Download a version's
archive and checksum from [GitHub Releases](https://github.com/Sociaro-Research/open-media-gateway/releases),
verify and extract them, then follow **that bundle's README**. The
[current bundle template](../deploy/ghcr/README.md) covers local installation,
managed HTTPS, existing proxies, independent instances, troubleshooting and updates.

Requirements: Docker Engine (or Docker Desktop), Compose 2.24.4+ and Python 3.9+.
Images support Linux AMD64 and ARM64; no Go, Node, source checkout or local image
build is needed. Public packages support anonymous pulls; private ones need the
access described below.

The release workflow publishes four packages under `ghcr.io/sociaro-research/open-media-gateway/`:
`gateway` (also the worker), `ui`, `postgres` and `redis`. The HTTPS service reuses
the pinned UI image's Caddy binary. PostgreSQL 17 / Redis 7.4 use the project's
hardened runtime; optional Prometheus/Grafana use their upstream images.

## Publish a version

1. Commit and push the intended release to `main`; complete project release
   checks. Do not include unfinished changes. Update the changelog for the release.
2. Create an annotated version tag on that commit and push it, for example:
   `git tag -a v0.1.0 -m 'Media Gateway v0.1.0'` and
   `git push origin v0.1.0`. Use a new version for every changed release.
3. Start **Publish GHCR release → Run workflow** in Actions on that version tag.
   Pushing a tag does not start publication automatically. Retry failed jobs from their
   original run to reuse the verified digests. **Run workflow** accepts an existing
   version tag; selecting `main` is rejected. Once version manifests exist, a
   rebuilt image requires a new version rather than replacing those manifests.

The workflow requires a version tag on `main` history and runs the existing
security, Go/integration, container and browser CI. It then builds and scans all
four images natively on each architecture, tests a clean source-free Compose
installation, browser owner setup without email, free mock generation/download,
HTTPS routing with a test certificate authority and restart persistence. Only verified images are uploaded; version manifests and
GitHub release downloads appear after both architectures pass.
The publication job also pulls the resulting GHCR digests and repeats the
installation check before attaching the downloads to a GitHub release.

Authentication uses the repository's `GITHUB_TOKEN`, with package-write access
only in publishing jobs. No Docker Hub credentials or additional personal token
are required. Organization policy must allow Actions and package creation.
Images carry source, revision and version metadata; runtime Go dependency
notices are included under `/licenses`. CycloneDX inventories are release assets.

For each changed release, publish new versioned images **and the matching
installation bundle** through this workflow. The user procedure stays the same;
only the selected version and its immutable image references change. Publishing
images does not require editing installation commands for every release.

Version tags are not overwritten. The installation archive pins all four image
manifests by digest and includes `images.json` and a SHA-256 checksum. There is
no floating `latest` image. Preview tags produce GitHub prereleases. Temporary
`build-...` package tags retain the tested architecture images; they are not
installation versions. Keep digests used by existing releases when cleaning them.

## Package access

New GHCR packages are private by default. The workflow preserves repository and
package visibility. For private installation, use a GitHub token with
`read:packages` and pass it to `docker login ghcr.io --password-stdin`.

To permit anonymous installation, an owner must make each of the four packages
public in its package settings. This is independent of repository visibility;
it publishes the binaries without making the source repository public. Verify
the released bundle from a machine without stored registry credentials afterward.
See [GitHub's package access documentation](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility).

## Updates and recovery

Keep the same Compose project name, secrets and volumes. Back up PostgreSQL,
media and configuration before upgrading. Copy `.env` into the new bundle and
replace only its four `*_IMAGE` references. Follow the bundled guarded worker
stop procedure before restarting dependencies and application services.
Do not run `down -v` on an installation. Image rollback does not undo database
migrations. This workflow publishes artifacts; it does not deploy any installation.
