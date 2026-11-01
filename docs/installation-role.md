# Installation roles and the central server

The public central Gateway origin distributed with this release is
**https://console.sociaro.ai**. This is a public destination default, not a
credential, a DNS change or an instruction to deploy a server there.

## Trusted identity and normalization

Gateway derives its role once from the configured public **UI_PUBLIC_URL**. The
existing configuration fallback is **AUTH_PUBLIC_URL** when UI_PUBLIC_URL is
empty. Separate API and UI origins are supported; the public UI origin identifies
the installation. The documentation origin does not determine its role.

An origin matching the normalized central origin identifies the **central**
installation. A different valid origin, including the current private test
installation's origin, identifies an **ordinary** installation. There is no role
flag and no special private-test exception. If both configured origins are
missing, the role is ordinary. Malformed configuration fails startup rather than
guessing a central identity.

Normalization validates the scheme, host and effective port, lowercases and
normalizes the hostname, removes a terminal DNS dot, a root slash and a default
port. Thus `https://CONSOLE.SOCIARO.AI:443/` identifies the same central origin;
`:8443` identifies a different installation. User information, non-root paths,
query strings (including an empty `?`), fragments, invalid ports and malformed
URLs are rejected. Public URLs require HTTPS. Existing HTTP loopback development
origins remain valid ordinary installations.

Request `Host`, `Forwarded`, `X-Forwarded-Host`, browser location and connection
form input never assign the role. Request Host is checked separately for public
routing, with the same origin normalization. Behind a proxy, configure the
trusted API/UI public origins and forward the supported public API host; do not
rely on forwarded headers to select a role.

The public destination is defined in `internal/installation/origin.go` and ships
with the binaries and installation release. Changing that release default is a
separate reviewed change. It is not an environment role override.

## Modes and existing settings

The central installation supports **Service for companies** only. Its setup API
accepts `profile: operator` with `allow_personal: false`. Personal and remote-client
setup choices, personal key issuance and personal provider/storage configuration
are rejected server-side as well as hidden by the UI. New generation/catalog
consumption requires a shared credential bound to an authenticated company; an
older unbound shared key cannot start new central work. New reference uploads,
provider-resource creation/estimates and project writes require a company. Installation-wide template authoring keeps
its existing administrator and shared-library checks.

Role detection does not rewrite stored settings, create a company, grant access,
enable customer onboarding or add funds. For an older installation whose new
central origin matches while its stored policy is personal or mixed, explicitly
apply Service for companies through the normal reviewed setup form. Existing
active-job/drain and optimistic-version checks still apply. Raw settings remain
visible to administrators; the public console policy reports personal providers
as unavailable for central. Incompatible direct settings updates are rejected.

Ordinary installations retain all three modes: **For myself**, **Service for
companies** and **Sociaro**. The Sociaro connection form defaults to the central
origin from server metadata. An existing saved connection retains its original
origin instead of silently moving when a release changes the default.

## Dedicated installation companies

The central server can receive an external installation only through the existing
explicit `customer_workspaces` and shared-resources hosting policy, supported
sign-in and the dedicated-company protocol. The external operator authenticates
with their own central account. A new installation receives a new separate
company, its initiating customer becomes Supermanager, and all generation,
history, balance and result operations use that company. The central owner's
personal account/company is not selected and does not approve the customer's
connection. No credit or payment is created by onboarding.

The durable installation proof, company binding, retries/renewal and legacy-key
compatibility are described in [system administration](system-administration.md#connect-an-account-and-company).
These safeguards remain independent of the role label. Existing grants, keys,
company balances, billing records, media and admitted task snapshots are not
reassigned or revoked by role detection.

Historical reads and deletes, completion of already admitted uploads and
explicit cancellation/disconnection retain their existing owner checks. A central
installation cannot start new remote-client work; authenticated read-only access
to a previously saved remote connection and its results remains available for
compatibility. Resolve any uncertain remote submission in its original company
before changing deployment identity. Never replay it under a different company.

## Metadata contract

The following read-only fields are emitted by `/site.json`,
`/v1/system/settings`, successful `/v1/system/setup-profile` responses and
`/v1/info` under `console`:

| Field | Meaning |
| --- | --- |
| `installation_role` | `central` or `ordinary`, derived from trusted server configuration. |
| `central_origin` | The normalized public central destination, currently `https://console.sociaro.ai`. |

Clients use the server role rather than comparing browser location. A request
cannot set these fields. Settings/profile responses retain their current version
and ordinary settings fields; role metadata does not change write authority.

## Future origin changes and rollout

Moving DNS, TLS or an installation to console.sociaro.ai requires a separately
authorized deployment task. This code performs no domain, live configuration,
credential, onboarding or payment changes.

When a later release changes the central destination, the comparison uses that
release's default and the installation's trusted own origin. An old central
origin may consequently become ordinary; an installation matching the new origin
becomes central and its future setup/admission follows the companies-only rules.
Existing database records and accepted snapshots remain untouched. Saved remote
connections continue to point to their reviewed original origin. New connections
use the new default; another central origin has its own installation proof and
company binding, not an automatic billing transfer. Preserve database and
encryption-key backups, resolve pending submissions and plan any grant/origin
migration explicitly. Application rollback must retain the additive installation
and provider-resource metadata and the existing company ledgers.
