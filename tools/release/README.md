# Local checks and deployment

Use **Gateway Checks and Deploy.app** in `~/Documents/Release Tool/`.
Commit, pull and push in GitHub Desktop yourself. The launcher never stages,
commits, pushes, merges or rebases project changes. Its source is maintained here;
private keys, logs and machine configuration stay outside version control.

## Install or update

From the repository root:

```sh
python3 tools/release/install.py
```

The installer backs up previous scripts and launchers under the installed
`Source/backups/`, preserves keys/cache/logs, updates `Source/project-path.txt`,
and builds the new app. The old `gateway commit and deploy.app` is also updated
so it cannot accidentally invoke the former commit/push runner. The apps resolve
`Source/` beside themselves; keep both in the same parent directory. Moving the
checkout requires rerunning the installer. No server change is part of installation.

## Available actions

Each checkbox is independent. Select actions, then choose **Run selected actions**:

- **Local checks**: whitespace/release policy, Go modules, formatting, vet,
  vulnerabilities, race/integration tests with disposable PostgreSQL/Redis,
  binary/UI builds, headless administration, project/launcher regression tests,
  and browser suites including CI's WebKit and chooser variants.
- **Additional CI checks**: checksum-pinned Gitleaks/Trivy,
  Git-history/dependency scanning, Compose/Prometheus validation, four local image
  builds/scans, UI runtime validation, source-free installation and real Grafana.
  This action does not run the native suites. Select both checkboxes for full CI.
- **Deploy to server**: deploy the exact clean, already-pushed `main` commit
  after any selected checks. Select this checkbox alone to deploy without repeating
  local CI. No earlier CI result is automatically certified or reused; the operator
  chooses whether the earlier result is sufficient for the intended commit.

Deployment always retains whitespace/release policy, clean-checkout, branch,
remote SHA and source-drift gates, SSH identity checks, server backups, worker
drain and post-deployment health checks. Fetch updates remote-tracking refs only.

CLI equivalents (installed runner):

```sh
python3 "$HOME/Documents/Release Tool/Source/release.py" --check-only --native-only
python3 "$HOME/Documents/Release Tool/Source/release.py" --check-only
python3 "$HOME/Documents/Release Tool/Source/release.py"
# Additional CI checks only:
python3 "$HOME/Documents/Release Tool/Source/release.py" --check-only --extended-only
# Deploy without repeating local CI:
python3 "$HOME/Documents/Release Tool/Source/release.py" --deploy-only
# Native checks followed by deployment:
python3 "$HOME/Documents/Release Tool/Source/release.py" --native-only
```

Deployment prompts in a terminal; `--yes` confirms the displayed deployment scope.
Browser checks stop after ten minutes each; other check commands stop after
thirty minutes. A timed-out check fails the run (exit 124).
Retries run the selected actions again. Deployment-only explicitly skips local CI;
server safety gates remain mandatory. Checks can run on uncommitted work, while
deployment requires a clean, pushed commit. These are local native-architecture results, not AMD64/ARM64
release-publication certification. Dedicated load/soak/recovery campaigns remain
opt-in; see [performance testing](../../docs/performance-testing.md).

## Dependencies

Use the Go version from `go.mod`, Python 3.9+, PostgreSQL 17 tools, Redis,
Node with Playwright, Chromium and WebKit, and Docker Desktop with Compose.
The standard Homebrew paths and bundled Codex Node/Playwright are detected.
Override `POSTGRES_BIN`, `RELEASE_NODE`, `NODE_PATH`, or `UI_BROWSER_PATH` when needed.
For example, after making Homebrew writable for your macOS user:

```sh
brew install go postgresql@17 redis
brew install --cask docker-desktop
# Open Docker Desktop and finish its first-run setup before full CI.
# With Playwright already installed in your Node environment:
node /path/to/node_modules/playwright/cli.js install chromium webkit
```

The pinned Caddy test runtime and checksum-pinned scanners download from official
releases. Scanner databases and Go modules require network access. Tests use
owned temporary databases and loopback ports; inherited test database URLs are
replaced. Full CI refuses remote Docker engines and uses unique test project/image
names. Only its own disposable resources are removed. Real Grafana checks need
free localhost ports 13000 and 18088; an occupied port stops the check.

## SSH and deployment safety

Deployment bindings are operator-owned private configuration. See
[deployment configuration](../../docs/deployment-configuration.md). The installed
runner reads `../deployment.json`; source runs read `.local/deployment.json`.
`DEPLOY_CONFIG` selects an explicit profile file. Missing profiles or verified
trust pins stop before network access. SSH always uses strict Ed25519 host-key
checking; there is no target or identity discovery fallback. Administration,
restricted deployment and GitHub keys remain separate existing files.
For GitHub fetch and remote-SHA reads, the optional `github_ssh_key_file` selects
an existing separate key. Command-scoped Git options select SSH only for the
profile's expected GitHub repository; origin and Desktop credentials remain
unchanged. GitHub's universal Ed25519 host pin is retained in
`github_known_hosts`, originally obtained from the official HTTPS
`https://api.github.com/meta` endpoint. Without a configured GitHub key, the
checkout's existing authentication is used.
Installation preserves the private profile, pins and key directory; it does not
provision credentials or change server access. Local-only checks need no profile.

Deployment retains server locking, verified application/PostgreSQL backups,
configuration and volumes, exact source hashes, safe removal of obsolete tracked
files, pre-stop image builds and proxy validation, a clean worker drain gate,
service/migration/auth/docs checks and an explicit free mock generation/download.
See [deployment and recovery](../../docs/deployment.md). No automatic rollback
or database restoration is performed. Logs may show a partial deployment after
SSH interruption: inspect the server backup/log before retrying. Do not remove a
stale local `release.lock` until the old process has been checked.

Browser evidence uses the shared repository helper so old `/tmp` files owned by
another macOS user cannot block a run. Each suite/variant receives its own dated
output directory.

The status page binds only loopback and uses a random URL and same-origin POST
checks. Closing it does not stop a running process. Logs and caches stay beside
the installed scripts; screenshots/performance evidence go under ignored
`dist/checks/<UTC>/`. When invoked from this repository, runner logs/cache use
ignored `.local/release-tool/`. Keep all evidence private.

The launcher is an independent offline administration page, so its native
checkboxes, launch button and status panel do not load the product's authenticated UI component
bundle. It has responsive wrapping, 44px controls, visible keyboard focus and
live status/error announcements.

## GitHub Actions

Automatic push/PR/tag triggers are removed. Existing CI, publication, deployment
and performance workflows remain manual or reusable as applicable. They consume
Actions minutes only if deliberately invoked (including a workflow calling CI).
Publishing versioned GHCR images/bundles remains a separate manual operation;
the local deployment tool does not publish release artifacts.

## Offline regression tests

```sh
python3 -m unittest discover -s tools/release/tests -p 'test_*.py' -v
```

The tests create commits and pushes only inside disposable fixture repositories;
SSH and production commands are mocked. Local Git changes, check failure, source
drift, archive/config/backup/worker-stop gates and browser command discovery are
covered without contacting production.
