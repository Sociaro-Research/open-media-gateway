# Public documentation and authenticated console

The independent UI server in `web/Caddyfile` serves browser pages. The gateway's
`api.WithPublicAPI` serves JSON, OpenAPI and branding resources and enforces origin
isolation. See [Frontend separation](frontend-separation.md) for builds and headless setup.

## Configuration

For a shared public console/API origin and separate documentation:

```dotenv
AUTH_PUBLIC_URL=https://gateway.example.com
UI_PUBLIC_URL=https://gateway.example.com
DOCS_PUBLIC_URL=https://docs.example.com
DOCS_HOST=docs.example.com
```

Public URLs must be HTTPS origins without credentials, paths, queries or fragments;
HTTP is allowed on loopback for development. The documentation origin must differ
from both API and UI origins. `DOCS_HOST` selects its host in the UI file server.
The gateway checks the actual Host header and ignores caller-supplied forwarded
host values. Browser authentication is optional for an API-only installation.

The outer TLS proxy must route both public origins to `ui:8080`, preserving Host.
The UI server proxies the documentation resource allowlist and operational API
routes to `gateway:8080`. Its published local port is 8088. Do not route public
browser pages to the backend's port after upgrading: it deliberately returns 404
for those pages. Preserve the production TLS configuration, environment and
volumes; deployment remains a separate authorized operation under AGENTS.md.

```caddyfile
gateway.example.com, docs.example.com {
    reverse_proxy ui:8080
}
```

Start the UI Compose profile alongside the backend and worker. For an existing
production Compose override, explicitly enable the profile and change only the
application's frontend upstream. A backend-only rollout is still supported.
See [Manual server deployment](deployment.md) for the first upgrade, backup,
ordered service updates and rollback.

## Routing and access

| Surface | Behavior |
| --- | --- |
| Docs host `/`, `/docs`, OpenAPI and appearance resources | Public standalone reference |
| Docs host API, auth, media and console scripts | 404, even with credentials |
| Console `/docs` | Navigates to the configured documentation origin; otherwise local reference |
| Console OpenAPI downloads | API contract with configured API server URL |
| Console pages without a valid session | Only the sign-in shell |
| Console scripts and provider icons | Session preflight against `/auth/session` |
| Console `/monitoring/` | Normal sign-in shell, then provisioned Grafana dashboard for current Administrators |
| Console `/monitoring/*` data, APIs and assets | Cookie-only Administrator preflight against `/auth/monitoring`; forged identity headers and API keys do not grant access |
| Shared styles, branding and sign-in scripts | Public resources |
| Raw `/ui/*.html` and directory listing | 404 |
| Operational API and capability URLs | Proxied unchanged; backend authorization/signatures apply |
| Backend-only `/`, `/docs`, `/ui/*`, legal pages | 404 |

HTML and session-dependent files use no-store caching, and session-gated responses
vary on Cookie. API keys do not establish browser sessions. The backend's public
`/site.json` exposes origins, appearance and asset MIME metadata, without sessions
or provider credentials. Uploaded asset bytes are fetched separately. CSP and
public/private route policy are configured in the UI server. The backend retains
its API cross-origin mutation protection and host checks.

`internal/api/public_sites_test.go` checks backend origin/resource boundaries.
`scripts/ui/separation.cjs` checks the real UI server routing and browser behavior.
After an authorized deployment, verify both HTTPS domains, login, documentation,
API origins and a free mock generation with result download.
