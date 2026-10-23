# Local development

Build the current source checkout for development and integration testing. For
installation without a build, use the [release Quick Start](../README.md#quick-start).

Do not expose this development configuration to the public internet.

## Source setup

For the current source checkout, use Docker Engine or Docker Desktop with
**Compose 2.24.4+** and **Python 3.9+** in a Linux/macOS/WSL shell. No provider
account or paid key is required. Docker builds the Go binaries and UI for you.

```sh
git clone https://github.com/Sociaro-Research/open-media-gateway.git
cd open-media-gateway
cp .env.example .env
```

For a **new local installation**, replace the example secrets and enable browser
owner setup. Run this once after copying `.env.example`; do not run it over an
existing installation, where changing encryption keys would make saved credentials
unreadable. The script writes private values to `.env` without printing them.

```sh
python3 - <<'PY'
from pathlib import Path
import secrets

path = Path('.env')
values = {name: secrets.token_hex(32) for name in (
    'CLIENT_API_KEY', 'SESSION_SIGNING_SECRET', 'WEBHOOK_SIGNING_SECRET',
    'MEDIA_URL_SIGNING_SECRET', 'PROVIDER_CREDENTIALS_ENCRYPTION_KEY',
)}
values.update(AUTH_PUBLIC_URL='http://localhost:8088',
              UI_PUBLIC_URL='http://localhost:8088', MEDIA_PROVIDER='mock')
lines = []
for line in path.read_text().splitlines():
    name, separator, _ = line.partition('=')
    lines.append(name + '=' + values[name] if separator and name in values else line)
path.chmod(0o600)
path.write_text('\n'.join(lines) + '\n')
PY
docker compose --profile ui up --build -d --wait
docker compose exec -T gateway /gateway admin setup-code
```

Open [http://localhost:8088](http://localhost:8088), enter the private setup code in
**Create owner account**, and choose your name, email and password. This creates
the first Administrator without email delivery or Google. Keep `.env` and Docker
volumes; they hold your credentials and data. The default console/API ports bind to
loopback. See [Native checks](#native-checks) below for tests and headless operation.

## Native checks

Use the [source setup](#source-setup) for the console, including its `ui`
Compose profile, local origins and independent secrets. The API-only/headless
configuration has different authentication requirements; follow
[headless operation](frontend-separation.md) when omitting the console.

Native checks require the Go version in `go.mod`, Python 3.9+, PostgreSQL tools
and Redis. Browser checks additionally require Node, Playwright and the engines
used by each suite. Go, Node and native database tools are not needed merely to
run the container-based source Quick start.

For full Go checks with disposable services:

```sh
python3 scripts/with-test-services.py -- make verify-ci TEST_FLAGS=-count=1
```

`make verify-ci` requires isolated PostgreSQL/Redis URLs and runs module and
format checks, vet, known-vulnerability checks, race/integration tests and binary
builds. Plain `make verify` skips integration tests when those URLs are absent;
it does not establish a full pass. The wrapper creates fresh loopback-only
services and supplies their URLs. Vulnerability checks query the official Go
vulnerability database and can require network access. GitHub Actions workflows
are manual/reusable; pushes and pull requests do not start hosted checks.

Read the [testing guide](testing.md) for required checks and evidence
boundaries. Use the [testing runbook](performance-testing.md)
to reproduce reliability and performance scenarios.

## Project layout

```text
cmd/gateway                  HTTP API and authentication
web/public                   independent browser UI and rendered documentation
cmd/worker                   queue processing, polling, result archival
cmd/webhook-receiver         local webhook test receiver
internal/core                provider-neutral media contract
internal/api                 HTTP endpoints and authentication
internal/postgres            repositories and goose migrations
internal/storage             durable media storage boundary
plugins/provider/mock        free end-to-end test adapter
plugins/provider/bytedance   BytePlus ModelArk adapter
plugins/provider/alibaba     Alibaba native DashScope adapter
plugins/provider/openai      OpenAI media adapter
plugins/provider/agnes       Agnes AI adapter
plugins/provider/sonilo      Sonilo music and sound effects adapter
```
