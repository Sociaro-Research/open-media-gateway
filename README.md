# Media Gateway

[OpenAI-style asynchronous API and migration guide](docs/openai-api.md)

> **Status: early preview.** The project is ready for local development and
> integration testing. The API may change before the first stable release. Do
> not expose the development configuration to the public internet.

The included privacy and terms pages describe the development service. Operators
should review and replace them for their own public installation; they are not
universal production policies or a legal review.

Media Gateway is a provider-neutral asynchronous API for generating images,
video, speech, and music. A client submits one media task; the Gateway records
its state in PostgreSQL, schedules processing through Redis, translates the
request for a selected provider, archives the temporary provider result, and
delivers signed webhooks.

## Quick start

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
loopback. See [Development](#development) for native tests and headless operation.

### Release bundle installation

For a version published in [GitHub Releases](https://github.com/Sociaro-Research/open-media-gateway/releases),
use its matching installation bundle and bundled README. The bundle runs Linux
AMD64/ARM64 images from GHCR without a source checkout, Go, Node or image build.
Docker with Compose 2.24.4+ and Python 3.9+ are still required. The commands below
use a version placeholder; select the version from the release being installed.

#### 1. Download and extract

Download `media-gateway-vX.Y.Z.tar.gz` and its `.sha256` file from the same
release into an empty directory. Replace `vX.Y.Z` below with that release's version.
For a remote installation, download them onto the server and run all commands there.

```sh
sha256sum -c media-gateway-vX.Y.Z.tar.gz.sha256
tar -xzf media-gateway-vX.Y.Z.tar.gz
cd media-gateway-vX.Y.Z
```

On macOS, use `shasum -a 256 -c` instead of `sha256sum -c`. Stop if verification fails.
Private GHCR packages require [registry access](docs/ghcr-release.md#package-access).

#### 2. Start locally or on a server

**Local** — console and API at [http://localhost:8088](http://localhost:8088):

```sh
python3 initialize.py
docker compose pull
docker compose up -d --wait
```

**Server** — first point your domain's DNS at the server and allow inbound TCP
**80 and 443**; these ports must be available. Replace the example domain:

```sh
python3 initialize.py --public-url https://gateway.example.com
docker compose pull
docker compose up -d --wait
```

The server mode includes HTTPS certificate issuance/renewal and HTTP redirects.
Both modes generate private secrets and enable browser login automatically;
PostgreSQL and Redis stay internal. An [existing reverse proxy or second instance](deploy/ghcr/README.md#other-installation-options)
only needs initializer options.

#### 3. Create your owner account

```sh
docker compose exec -T gateway /gateway admin setup-code
```

Open your console, enter that private code in **Create owner account**, and choose
your name, email and password. You are signed in as Administrator immediately;
Resend and Google are optional. Configure providers, email and storage in
**Administration** afterward. The free `mock` provider needs no external account.

Keep `.env` and Docker volumes: they contain your installation's credentials and
data. For [checks, troubleshooting and updates](deploy/ghcr/README.md), use the
bundled README. [Publishing images](docs/ghcr-release.md#publish-a-version) is a
maintainer task; users install the next version's bundle without building images.

Supported adapters:

- `mock` — free local results for all four media types;
- `bytedance` — Seedream and Seedance models available through BytePlus ModelArk;
- `alibaba` — Alibaba Cloud Model Studio through the native DashScope API,
  including image generation/editing, video generation/editing, HTTP TTS, and
  Fun-Music.

Provider-specific URLs, payloads, statuses, and result parsing stay behind the
adapter boundary in `plugins/provider/*`. Clients and the built-in UI use one
common contract.

## OpenAI media provider

`openai` adds GPT Image generation/editing, text-to-speech, and GPT Audio spoken
responses. Configure a project API key under Providers and enable **Available for generation**
for the personal or shared connection. No additional SDK is required.
See [setup, supported models, retirements and cost limitations](docs/openai-provider.md)
and the [provider module guide](docs/provider-modules.md).

## Agnes AI media provider

`agnes` adds Agnes Image 2.0, 2.1 and 2.5 Flash generation/editing/composition,
plus Agnes Video 2.5 and 2.5 Flash with keyframes and media references. Configure
an inference key under Providers and enable availability. The model catalog is
public; it cannot verify your key. See [setup, capabilities and pricing limits](docs/agnes-provider.md).

## Sonilo music and sound effects

`sonilo` adds music and sound effects from text or video, finished videos with
generated music/effects, combined soundtracks and narration ducking. Configure
a Sonilo Platform key under Providers and enable availability. Account service
discovery verifies the key and filters supported operations. See
[setup, controls, tariffs and verification limits](docs/sonilo-provider.md).

## Developer API and console documentation

Open `/docs` on your Gateway deployment for the public API reference, quickstart,
cURL/Python/JavaScript examples, webhooks, errors, and all request/response schemas.
Download the contract at `/openapi.yaml` or `/openapi.json` (OpenAPI 3.1).

Sign in with the password created during owner setup, or with Google or verified
email when configured, and open **API keys** to issue and revoke personal keys.
Save the secret when it is first shown. Send it as
`Authorization: Bearer YOUR_GATEWAY_API_KEY` from your application. The console
uses a real API key for generation too; browser sessions manage account settings.
Both clients see the key owner’s providers and task history. Keys default to generation access; provider and key management are
explicit permissions. The deployment `CLIENT_API_KEY` remains a separate system
credential. See [upgrade notes](docs/configuration.md#personal-keys-and-upgrading).

## What is included

- one asynchronous API for image, video, speech, and music generation;
- provider and model discovery with provider-neutral capability metadata;
- encrypted provider credentials and enable/disable controls in the admin UI;
- PostgreSQL task state, Redis queues, recovery, and idempotent task creation;
- durable local or S3-compatible media storage with signed preview/download URLs and HTTP Range;
- signed, retried lifecycle webhooks;
- raw provider usage metrics, estimated costs where tariffs are known, and
  optional read-only cloud billing credentials for charge reconciliation.

## Create, poll and download a mock image

Create a key in **API keys** after signing in, then set `GATEWAY_API_KEY` to its
secret for the examples below. Use your console's HTTPS origin on a server.
`CLIENT_API_KEY` is a separate service credential, not a browser password.

```sh
export GATEWAY_API_KEY='YOUR_GATEWAY_API_KEY'
```

The `mock` adapter returns fixed test media after a short delay; it does not
render the prompt; the image fixture is an SVG and no paid provider request is made. Real provider models need
your own credentials and can incur charges. This example uses only `mock`.

The following shell example also needs `curl` and Python 3. It polls up to 60
times, then downloads the completed image with the same API key. Keep the
Gateway base URL free of a trailing slash.

```sh
(
set -eu
GATEWAY_URL=http://localhost:8088
generation=$(curl --fail --silent --show-error -X POST "$GATEWAY_URL/v1/images/generations" \
  -H "Authorization: Bearer $GATEWAY_API_KEY" \
  -H "Idempotency-Key: mock-image-$(python3 -c 'import uuid; print(uuid.uuid4())')" \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"A cinematic lighthouse at dawn","model":"mock/mock-image-v1"}')
status_url=$(printf '%s' "$generation" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status_url"])')
status=queued
attempt=0
while [ "$attempt" -lt 60 ]; do
  generation=$(curl --fail --silent --show-error \
    -H "Authorization: Bearer $GATEWAY_API_KEY" "$GATEWAY_URL$status_url") || break
  status=$(printf '%s' "$generation" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
  case "$status" in completed|failed) break ;; esac
  attempt=$((attempt + 1))
  sleep 2
done
if [ "$status" = completed ]; then
  content_url=$(printf '%s' "$generation" | python3 -c 'import json,sys; print(json.load(sys.stdin)["content_url"])')
  curl --fail --location --show-error \
    -H "Authorization: Bearer $GATEWAY_API_KEY" \
    "$GATEWAY_URL$content_url" --output mock-image.svg
else
  printf 'Generation did not complete (status: %s). Inspect status_url before retrying.\n' "$status" >&2
  exit 1
fi
)
```

Poll the returned `status_url` until the generation reaches `completed` or `failed`. The
complete HTTP contract is in [docs/openapi.yaml](docs/openapi.yaml). Authenticated
task responses include retained `prompt` and `options` only when the current
viewer has content access. Webhook `data` uses the public generation object;
request content follows the same retention policy.

## Execution, file transfers and request limits

Provider operations run concurrently; asynchronous jobs release their active claim
between polls. There is no fixed task lifecycle pool. Administrators configure
per-key and per-user request limits, with organization overrides. See
[request limits](docs/request-limits.md) and [worker scheduling](docs/concurrent-workers.md).

S3 reference uploads and file downloads transfer bytes directly between clients
and storage. The gateway authorizes access and signs URLs; only archival of
provider results uses the worker's temporary storage. See
[direct file transfers](docs/direct-storage.md) for the API flow and required CORS.

## Documentation

See the [documentation index](docs/README.md) for all user and contributor guides.

- [Local CI and deployment launcher](tools/release/README.md)
- [Configuration and deployment](docs/configuration.md)
- [Architecture and trust boundaries](docs/architecture.md)
- [Direct S3 uploads and downloads](docs/direct-storage.md)
- [Worker scheduling and recovery](docs/concurrent-workers.md)
- [Browser monitoring with Grafana, private Prometheus and alerts](docs/monitoring.md)
- [System and organization request limits](docs/request-limits.md)
- [Email authentication and email design](docs/email-authentication.md)
- [Webhook delivery and verification](docs/webhooks.md)
- [Adding a provider](docs/provider-development.md)
- [OpenAPI specification](docs/openapi.yaml)
- [Cost estimates, budgets, and billing reconciliation](docs/cost-accounting.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)
- [Support](SUPPORT.md)
- [First public release checklist](docs/open-source-release-checklist.md)

## Development

Use the [source Quick start](#quick-start) for the console, including its `ui`
Compose profile, local origins and independent secrets. The API-only/headless
configuration has different authentication requirements; follow
[headless operation](docs/frontend-separation.md) when omitting the console.

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

Read the [testing guide](docs/testing.md) for required checks and evidence
boundaries. Use the [testing runbook](docs/performance-testing.md)
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
```

## License

Licensed under the [Apache License 2.0](LICENSE). By submitting a contribution,
you agree that it will be licensed under the same terms.

ByteDance, BytePlus, Alibaba Cloud, DashScope, and other product names are
trademarks of their respective owners. This project is independent and is not
endorsed by those providers.

Configure account storage under **Storage** in the console: choose local storage
or connect Amazon S3, Cloudflare R2, Backblaze B2 or a custom S3 endpoint. Connection
checks verify object write/read/delete access before saving. Existing files remain
in their original storage. See [storage setup](docs/configuration.md#account-storage-local--s3).
