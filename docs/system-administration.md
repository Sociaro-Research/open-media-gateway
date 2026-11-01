# System administration

Fresh installations create the first owner through a browser setup screen,
without Google or email delivery. Administrator roles belong to stored user IDs,
not an email allowlist. Ordinary new users have no system privileges.

## First administrator

Set a stable, random `SESSION_SIGNING_SECRET` of at least 32 characters and set
`AUTH_PUBLIC_URL` and `UI_PUBLIC_URL` to the shared console/API origin before
starting gateway and UI. Use HTTPS remotely; local development may use
`http://localhost:8088`. The release bundle initializer configures local defaults.
The source `.env.example` deliberately leaves browser authentication disabled.

On a new database, retrieve the installation code using trusted server access:

```sh
docker compose --profile ui exec -T gateway /gateway admin setup-code
```

Open the console URL. **Create owner account** asks for the setup code, name,
email and a 15–128 character password. Completion creates the Administrator role
and browser session together, consumes the code and closes initial setup. The
owner can immediately configure Resend, Google and generation providers inside
Administration. Email is a login/contact identifier here; control of the server
code authorizes initial ownership, not a mailbox verification claim.

The code is stored encrypted in PostgreSQL and can be retrieved again before
completion using the same command. It is never returned through HTTP, placed in
a URL or written to application logs. Keep the encryption key stable. Do not
share the code publicly. Only one concurrent setup can succeed. Existing
installations with any users do not offer this screen on upgrade, and deleting
an owner's account does not reopen completed setup.

Email login is hidden until delivery is configured, even if its enabled preference is retained. Google availability is independent. Existing sessions and confirmation/reset links remain usable, and the password API retains its existing configured-method policy. Configure delivery or Google before signing out of the initial administrator session; if no browser method is available, use the existing server-side management-key recovery below.

### Headless installation and existing accounts

API-only operators can instead run `gateway admin init --email
operator@example.com`. This atomically creates an administrator and one-time
management key, closes browser setup, and does not create a password or browser
session. Recovery for a current administrator uses `gateway admin issue-key
--user-id UUID`. See [Frontend separation](frontend-separation.md).

For an existing installation without an Administrator, the intended user signs
in through a configured method. On the application server:

```sh
# Run from your source or extracted installation directory.
docker compose exec gateway /gateway admin users --search person@example.com
docker compose exec gateway /gateway admin bootstrap --user-id USER_UUID
```

Check the returned account before using its UUID. Bootstrap succeeds only when no administrator exists. Refresh the console, then open **Administration** in the header.

Administrators can find existing users, grant or revoke administrator access, change global settings, and review the [Audit Log](audit-log.md). The last administrator cannot be removed, including by simultaneous requests. Role changes take effect on subsequent API requests. An already open console may need a refresh to update navigation.

For recovery, a server operator can use `admin grant --user-id USER_UUID` or `admin revoke --user-id USER_UUID`. These commands are audited and retain last-administrator protection. They require the application's database connection and existing migrations.

## Setup scenarios and remote workspaces

After browser owner creation, the setup guide offers **For myself**, **Service
for companies**, or **Sociaro**. Skip, Escape and closing the guide perform no
settings write. Dismissal lasts for the browser session; **Settings → System →
Choose setup** reopens it. Existing installations keep their settings and do not
open the guide automatically on upgrade.

| Scenario | `personal_providers` | `shared_resources` |
| --- | --- | --- |
| For myself | true | false |
| Service for companies | reviewed optional checkbox, initially the existing value | true |
| Sociaro | unchanged | unchanged |

These are independent resource policies. Mixed/custom settings remain valid.
The scenario selector is their only control in Settings; Advanced settings has
no duplicate shared-resource or personal-provider switches. Reopening selects
the current personal/company policy, including the optional personal access in
company mode. A legacy configuration with both policies off remains Custom
until an Administrator explicitly applies a scenario. Choosing Sociaro opens
the separate remote connection flow without rewriting local resource policies.
Unrelated Advanced settings saves omit both resource-policy fields; the API
preserves their confirmed values when omitted. Explicit API updates remain
supported with the usual version and authorization checks.
Presets preserve `generation_ui` (UI visibility only), inline references,
markups, storage limits, companies, credentials, credit, jobs and files. They do
not copy personal keys into shared resources or create a funded company.
Disabling provider access returns `409 setup_busy` while affected queued/running
jobs exist. Transaction admission locks prevent new HTTP work from racing the
switch. Already admitted uploads can complete. Settings require the current
version; a concurrent edit returns 409.

After a setup scenario is applied, the open Settings form adopts the saved
configuration and version. Unsaved Advanced edits remain drafts, and untouched
controls reflect the saved result. A genuine concurrent change still returns
409; the form retrieves current values and preserves edits for review before
another explicit save. It never repeats a conflicting write automatically.

### Connect an account and company

In **Settings → System → Choose setup**, select **Sociaro** and activate
**Open Sociaro workspace**. This scenario is the entry to both a new connection
and an existing remote workspace. It has no separate global-header shortcut.
The header's current-company selector continues to open the local company;
it does not connect to another installation. Company-service and personal
scenarios continue to manage local resource policy independently of the remote
connection. The host's **Allow customer workspace onboarding** gate is unchanged.

For a new connection, enter the trusted public HTTPS server origin and an
installation name, then explicitly consent to sending that name and the browser
origin. Open the displayed trusted sign-in/signup window. That host uses its
existing Google or verified email/password identity flow; this installation
never asks for the remote password. Match the displayed code and reported
installation. Its name/origin are supplied by the requesting installation and
are not proof of domain ownership.

On the host, select a company where you currently hold the **Supermanager** role,
or explicitly create your own company using the existing name/website rules.
The bounded pilot allows one self-service company creation per account; existing
owned companies remain selectable. Approve the installation's generation-only
access explicitly. Denying, closing or merely inspecting the request never
issues a credential. Company creation and consent are atomic and retry-safe.
No paid credit, member allowance or storage configuration is inferred.

The local backend polls with a random 32-byte device secret; the browser sees
only a short verification code and safe metadata. No inbound callback, public
self-hosted domain, CORS expansion or shared client secret is required, so
localhost/LAN clients work through outbound HTTPS. Requests expire after ten
minutes and enforce polling/rate limits. This is a bounded device-style protocol
informed by [RFC 8628](https://www.rfc-editor.org/rfc/rfc8628), not an OAuth
interoperability claim. Device secrets remain encrypted locally. Only their
hashes are stored on the host.

After browser approval and proof of the device secret, the host rechecks current
company ownership, account role and policy. It uses the existing API-key issuer
to create one shared, company-bound, **generation-only** customer key, valid for
30 days. Existing per-key request limits, active-key limits, company funds and
member allowance apply. A lost successful response returns the same credential,
not another key. Delivery ciphertext is inaccessible after request expiry and is
cleared during subsequent starts or explicit revocation/cancellation. The local
backend verifies `/v1/key` before atomically saving its encrypted credential.
The browser never receives that credential; upstream provider keys stay on the
host. Protect database backups and keep the existing encryption key stable.

Every proxy call carries the opaque `context` from the connection the user
reviewed, including its revision, remote origin, company and account binding.
`X-Workspace-Context` is required; GET/HEAD result links can instead use the
`workspace_context` query value. Missing context returns 428; a changed context
returns 409 before any remote dispatch. A request admitted against a snapshot
keeps that original destination/credential even if the saved connection changes
later. Response links carry the admitted snapshot context. The token is a
precondition, not an authentication credential.

The customer's daily catalogue, estimate, asynchronous generation, latest 20
history items, balance and result downloads remain in the local interface.
The host remains authoritative for membership, model grants, prices/markup,
reservations, billing, files and retention. There is no local task mirror or
second local debit. Uncertain submissions retain their frozen payload and
operation key in browser session storage across reload and same-account/company
renewal, including renewed local sign-in. Their storage scope excludes connection
and key revisions; the remote idempotency namespace excludes the replaceable
credential ID. Other hosts/companies retain distinct operation identities.
Switching to another context preserves the earlier retry and blocks a new draft
until the original outcome is resolved. Older unbound client drafts are preserved
for explicit history reconciliation, never blindly replayed in the new namespace.
Resolve that same submission before creating another. The retained draft includes prompt/options,
so use a trusted browser session.

### Hosting and lifecycle

Hosting customer onboarding is an explicit **off-by-default** setting:
**Settings → System → Allow customer workspace onboarding**
(`customer_workspaces`). It also requires `shared_resources`, working browser
sign-in/registration and a public HTTPS console/API origin. Enabling shared mode
alone never enables self-service company creation. Upgrades preserve existing
behavior. This code does not activate an installation, configure a public paid
Sociaro endpoint or assert any provider's resale permission. Hosting activation,
provider terms/catalogue, funding, storage and payment setup remain operator work.

New companies retain the existing $0 funding and allowance defaults. Use the
existing company controls for credit, member limits, model grants, storage and
payments. The connection wizard performs no top-up transaction and introduces no
new identity or billing system. Registration/email delivery, provider rights and
host configuration remain prerequisites.

**Renew or change company** requests a new explicit authorization while preserving
the current local connection until replacement succeeds. In the trusted host
window, the customer may explicitly revoke a prior installation key they own as
the replacement is issued. This is atomic with new issuance. Automatic renewal
and refresh tokens are outside this pilot; reauthorize before the 30-day expiry.

The host's account menu → **Connected installations** lists only that customer's
own grants and allows revocation, including when new onboarding is disabled.
Revoking a role/membership or key is enforced on subsequent requests.
**Cancel connection request** serializes with issuance and revokes any key that
request issued. Cancellation keeps remote company data and funds. Cancellation remains able to revoke a key after the ten-minute request deadline;
expired consent/redemption remain forbidden. If cancellation is uncertain, retry
it: only positive host confirmation permits local proof deletion. Ambiguous 410,
malformed replies and expiry do not authorize discarding or overwriting that proof.

**Disconnect** removes the local saved key and increments a tombstone version,
preventing stale connection replies from restoring it. It preserves remote jobs,
charges and files and does not itself revoke the host key; use **Connected
installations** on the host for revocation or loss of an installation. Keys are
scoped per installation and company. Other local Administrators cannot access a
connection they did not create. Revoking the local Administrator role removes
subsequent access to this local connector.

The advanced existing-company-key form remains available for older compatible
hosts or manually managed keys. It requires exactly the generation scope, shared
resources and matching company binding. It is separate from the primary
signup/login/company wizard.

### Transfer and failure boundaries

The proxy permits only catalogue, current credential/balance, generation
estimate/submission/history/status and authorized result downloads. It has no
arbitrary management, payment or credential-creation proxy. Remote JSON is limited
to 4 MiB and downloads to 512 MiB. Tokens go only to the configured origin; API
redirects are rejected. Storage redirects use a separate uncredentialed request.
Checked DNS/IP dialing blocks loopback, LAN, metadata and private destinations;
private-host exceptions do not apply. Downloads use attachment/sandbox headers,
and incomplete/oversized streams abort rather than returning successful partial
files. Errors, low funds, limits and expired/revoked keys remain visible; there
is no silent fallback to another paid provider.

The current generation form supports prompt-based media. Reference uploads,
all-company synchronization, customer resale chains, embedded payment UI and
background grant renewal are outside the bounded pilot. Models requiring file
inputs need a later reference-transfer feature. Remote company data never merges
into a local company. See [OpenAPI](openapi.yaml) for exact endpoints and limits.

## Settings

The page is titled **Settings**, with the general options under **System**.

Provider availability is controlled in **Shared providers** (shared connections) or **Providers** (personal connections), using **Available for generation**. There is no installation-wide provider allowlist. Model defaults and organization-specific model grants still apply.

All administrators can configure [shared providers and storage](shared-resources.md), enable shared access and manage user budgets. Shared resources belong to the installation, not an administrator’s personal account.

- **Show Playground:** hides the Playground screen for everyone; API generation remains available.
- **Allow personal provider configuration:** controls non-administrators' ability to view and manage personal connections through the console and API. Saved connections are preserved, but personal keys cannot start new generations while this is disabled. Administrators retain access to their own connections.
- **Allow reference files in generation requests:** enabled by default. Allows Base64 image transfer for BytePlus using signed local-storage library references, within the payload limit. External URLs, oversized payloads, video/audio references, and providers without inline transfer continue to use links. S3 references always use direct signed storage URLs. Disabling sends only HTTP(S) links or model-supported registered `asset://` references and rejects direct data URLs, including at dispatch for queued tasks. The worker reads the current setting before generation, without a restart; requests already sent are unaffected. This applies to shared and personal connections. The API field is `allow_inline_references`; omitting it on settings updates preserves its current value.
- **Email delivery:** select Resend and configure its sender and API key. This controls sending account emails and system notifications, independently of sign-in methods. Keys are encrypted and never returned to the console or activity log.
- **Login methods:** add and configure Google or email in expanded cards. Google Client ID and Client Secret are editable; the callback URL is read-only. Switches preserve credentials, users and sessions. Email login is hidden without configured delivery; its enabled preference is preserved, with an explanation beside the switch. Registration and recovery links also require delivery. See [Email authentication](email-authentication.md).

Settings are grouped into the **System**, **Markups**, **Request limits**, **Payments**, **Email delivery** and **Login methods** tabs. Switching tabs preserves unsaved edits. Payment providers and payment notifications share the Payments tab.

Connections remain personal. Disabling personal configuration does not create shared provider credentials. A user without an existing connection cannot generate with that paid provider.

Settings updates require the current version; a concurrent edit returns 409 instead of silently overwriting another administrator's changes. Role and settings changes are recorded with the actor, time, and before/after values.

## API

The console uses the documented `/v1/system/settings`, `/v1/system/users`, `/v1/system/users/{id}/role`, and `/v1/audit/...` endpoints. They require a current Administrator role plus either a verified browser session or a personal API key with `system:manage`. Only Administrators can create such keys. Ordinary generation keys do not inherit system access, even when their owner is an Administrator. Revoking the owner's role also removes system access from existing keys.

System administrators manage shared connections and shared financial reports. Personal connection credentials remain account-scoped. Organizations own reference libraries, generation files and expenses. See [Organizations](organizations.md) for roles, funding and invitations.

## Markup rules

Settings → Markups contains a **Markups** block: one default percentage and
an initially empty list of provider/model exceptions. Organization Settings has
an identical **Markups** block visible only to system administrators.
An empty organization default inherits system rules; explicit zero means zero
markup. Select a provider and **All models** for a provider exception, or select
one of its models for a model exception. Remove an exception to restore inheritance.
Each block saves atomically, without reloading the page, and shows Saved for three
seconds. Duplicate provider/model targets and percentages outside 0–1000 (maximum
two decimals) are rejected. Saved model IDs remain editable after retirement.

The first configured rate wins: company model, company provider, company default,
system model, system provider, system default. Rates replace rather than add to
each other. A company default of zero overrides all system rules, but its own
provider/model exceptions still win. Changes apply to new shared-provider tasks;
the resolved percentage is captured on admission and retained for reconciliation,
usage, budgets and reports. Personal provider credentials remain unmarked.
Catalog prices resolve each model using one consistent policy snapshot per request.
Customers receive final gateway prices only, never the rules or provider costs.

`GET/PUT /v1/system/markups` and `GET/PUT /v1/organizations/{org}/markups`
read/replace the respective configurations. PUT accepts `default_percent`,
`exceptions` (each with `provider`, optional `model`, and `markup_percent`), and
`version`. A stale version returns 409. The system configuration shares the main
settings version; organization pricing has its own version. Existing scalar
markup API fields remain compatible, update the corresponding default only, and
preserve exceptions. Migration 031 preserves all existing rates and adds empty
exception lists. `/v1/system/markups/catalog` provides shared-provider model choices.

## Request limits

The **Request limits** block sets default per-key and per-user generation,
read and key-creation quotas, plus the active-key maximum. Organization Settings
contains the same fields with optional per-field overrides. Only system
administrators can view or change these controls. See [request limits](request-limits.md)
for exact defaults, inheritance, response codes and scheduler behavior.

## Payment providers

System administrators configure Stripe under **Settings → Payment providers**.
Organization administrators use **Top up balance** in their organization. Each
connection chooses whether the service or the organization pays the actual
processing fee; the choice is fixed on each new payment. See [organization
funding](payments.md) for webhook setup, test/live isolation, refunds, reconciliation
and the separate trusted integration API.

## Public legal documents

In **Seller company and VAT**, an Administrator can open separate **Privacy Policy**
and **Terms of Use** dialogs. Each starts with the saved override or bundled template.
Save publishes immediately; Cancel, Close and Escape discard the draft. Closing
is blocked during a save. Conflicting saves preserve the draft and require reopening
the latest document before retrying.

Documents are plain UTF-8 text, limited to 64 KiB each. HTML and URLs are displayed
literally; line breaks are preserved. **Use default template** clears the override
only after Save. Company/VAT saves do not alter either document. The stored
concurrency counter prevents lost updates; it is not a legal acceptance record.
No agreement acceptance or contract-history process is introduced.

The public `GET /legal/privacy` and `GET /legal/terms` endpoints return only the
override text without authentication. Empty text selects the bundled frontend
template. A read failure shows an error and Retry instead of silently showing
a potentially outdated template. Administrator reads and writes use
`/v1/system/legal/{kind}`. Migration 070 creates the two empty overrides.
