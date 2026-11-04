# Frontend separation and headless operation

The gateway and worker are backend executables. They do not embed or serve HTML,
CSS or JavaScript. `web/public/` contains the current browser application, legal
pages and rendered API reference. `web/Caddyfile` serves those files and proxies
API requests to the gateway. The UI image contains Caddy and frontend files; the
backend image excludes `web/`. No new application backend or database is needed.

```text
Browser -> UI file server -> Gateway API -> PostgreSQL / Redis / Worker
API client ----------------> Gateway API
```

## Build and run

`make build` checks compilation of the Go commands; it does not leave a `./gateway`
executable. To create the native gateway used below, run:

```sh
go build -o dist/bin/gateway ./cmd/gateway
```

`make build-ui` produces the independent
`dist/gateway-ui.tar.gz` artifact without compiling Go. Build container images
independently with `docker build .` and `docker build web`.

`docker compose up --build` starts the backend, worker and databases without UI.
`make up` or `docker compose --profile ui up --build` also starts the UI at
`http://localhost:8088`; the direct local API remains at `http://localhost:8080`.
The README release examples use the UI origin at port 8088. The direct API
address at port 8080 is available for development when `AUTH_PUBLIC_URL` is unset. For local cookie authentication, use
`AUTH_PUBLIC_URL=http://localhost:8088` and open the UI through that exact origin.

For a native UI server, extract the UI archive and run Caddy:

```sh
mkdir -p dist/ui
tar -xzf dist/gateway-ui.tar.gz -C dist/ui
UI_ROOT="$PWD/dist/ui" GATEWAY_UPSTREAM=127.0.0.1:8080 UI_LISTEN=:8088 \
  caddy run --config dist/ui/Caddyfile --adapter caddyfile
```

Use the Caddy version pinned by `web/Dockerfile` (currently 2.11.7), or a
compatible runtime. `UI_ROOT`, `UI_LISTEN`, `GATEWAY_UPSTREAM` and
`DOCS_HOST` configure delivery; the backend does not read frontend files.

## Initialize an installation without a browser

For an interactive installation, prefer the
[one-time owner setup](system-administration.md#first-administrator): retrieve
`gateway admin setup-code` on the server, then create the owner/password in the
browser without email delivery. The API-only workflow below is an alternative
that closes this browser setup and creates no password.

The trusted command uses only `DATABASE_URL`. It applies migrations and creates
the first administrator and a personal management key in one transaction:

```sh
DATABASE_URL=postgres://... ./dist/bin/gateway admin init \
  --email operator@example.com --name Operator --key-name Bootstrap
```

The JSON output includes the one-time `secret` and user ID. Store the secret
securely; PostgreSQL stores only its hash. No initial password, Google identity,
organization membership or universal credential is created. Repeated bootstrap
is rejected. Existing accounts require explicit `admin bootstrap --user-id UUID`,
followed by `admin issue-key --user-id UUID`. Server-side recovery issues another
key for a current administrator and respects the normal active-key limit. Revoke
an unused key through the API if that limit has been reached.

Use the returned key with `Authorization: Bearer ...`. It has the six documented
scopes, including `system:manage`; administrator roles are checked on every
request. `CLIENT_API_KEY` remains a separate legacy system-owner credential and
does not grant administrator permissions.

| Function | Access without UI |
| --- | --- |
| Generation, result download, references, reports and usage | Existing `/v1/` APIs |
| Providers, storage, organizations, users and system settings | Existing management APIs and authorized scoped keys |
| Branding, email delivery and login provider settings | Management APIs, including when browser authentication is disabled |
| First administrator and administrative key recovery | Trusted `gateway admin` commands |
| Sign-in, email verification, registration and recovery | `/auth/` HTTP endpoints; cookie jars work without a graphical browser |
| Personal name, theme and report-column preferences | `PATCH /v1/me` with a session cookie; API keys remain intentionally restricted |
| Payment checkout | API creates checkout; the provider's external payment interaction is still required |

Browser authentication can remain enabled in a headless installation, independently
of UI delivery. Omitting `SESSION_SIGNING_SECRET` disables cookie login, registration
and cookie-only profile changes. It does not disable key-based management or
background payment notifications. Email and payment providers retain their
external delivery, verification and settlement requirements.

## Connect the existing UI to Sociaro

The Sociaro setup choice uses the existing central sign-in and explicit device-code
approval. It creates or recovers one dedicated company and obtains a normal
company API key with `generation`, `organizations:manage` and `keys:manage`.
New `company-api-v1` consent issues a revocable key without a fixed expiry. Existing
`generation-v1` approvals retain their original scopes and expiry; they are not
silently upgraded. Repeating the same approval/redemption returns its original
company/key. A new reconnect requires explicit approval.

The signed-in local administrator's existing interface uses that key through a
fixed-central-origin proxy. The key is encrypted at rest and returned only to the
local account that connected it; other local users do not inherit that account.
The central APIs retain their ordinary company, membership, role and key checks.
The connection key is an ordinary bearer credential, not an origin-restricted
installation token. It is not sent to provider or storage destinations.

The proxy preserves API methods, query bytes, multipart bodies, range responses
and idempotency keys. Central failure is an error, never a local generation or
storage fallback. Disconnect stops use by this interface and preserves central
company data; remote key revocation remains available centrally.

Profile writes, account support and browser-only managed Playground-key operations
remain subject to their existing session-only guards. The connected interface
already has its company key and does not invoke managed-key creation/restoration.
No separate generator or copied local catalogue is used.

Existing storage and payment transports are unchanged. Signed media uses the
central origin. S3 uploads still use the existing signed browser upload plan and
require the bucket's CORS policy to permit the browser origin; no CORS changes or
relay are performed by connection. Central local-storage uploads use the existing
multipart API. Payment interactions retain the central checkout/return behavior.

## Replace the UI

A replacement frontend uses the documented OpenAPI contract and `/site.json`,
which exposes public origins, appearance tokens and asset metadata. Image bytes
are available from `/branding/assets/{asset}`. Permissions and business rules
remain enforced by the backend. The current frontend renders branding from this
metadata; email rendering keeps its own backend template assets.

Build and publish the replacement independently, then point the public reverse
proxy at its file server. Keep browser pages and proxied APIs under the same
public origin for the existing host-only cookies and cross-origin protection.
`AUTH_PUBLIC_URL` is the public API/callback origin; `UI_PUBLIC_URL` defaults to it
and controls email links, OAuth return navigation and checkout return navigation.
Changing that setting alone does not create a cross-origin cookie/CORS mechanism.
A separately hosted browser client should proxy the API under its own public
origin and preserve the existing authentication contract.

There is no need to split business services, create a second application server,
change database ownership, or move authorization into the frontend. A UI-only
change requires rebuilding only the UI artifact/image. Shared API contract changes
still require coordinated backend and client changes.

## Verification

`make verify-ci` uses explicitly isolated PostgreSQL and Redis and includes the Go
race tests. `python3 scripts/verify-headless.py` requires those test service URLs,
`psql`, Python and Go. It builds from a copy with no `web/` directory, creates a
fresh database, initializes an administrator, checks management APIs with browser
authentication disabled, and generates/downloads an explicit free mock SVG.

`node scripts/ui/separation.cjs` requires Playwright and Caddy (`CADDY_BIN` can
select its path). It uses the actual Caddyfile with an isolated API fixture and
checks session gating, documentation isolation, proxying, branding, keyboard
access and 1440/390/320px layouts in both themes. Existing `scripts/ui/*.cjs`
workflows cover the current frontend against their isolated API fixtures.
