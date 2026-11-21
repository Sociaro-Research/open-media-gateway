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

Email login is hidden until delivery is configured, even if its enabled preference is retained. Google availability is independent. Existing sessions and confirmation/reset links remain usable, and the password API retains its existing configured-method policy. Configure delivery or Google before signing out of the initial administrator session. While no browser method is available, Sign out first asks for confirmation (**Stay signed in** / **Sign out anyway**). If you are already signed out, use the existing server-side management-key recovery below.

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
for companies**, or **Central server**. Skip, Escape and closing the guide perform no
settings write. Dismissal lasts for the browser session; **Settings → System →
Change Mode** reopens it on ordinary installations. Existing installations keep
their settings and do not open the guide automatically on upgrade.

On the central server, the separate **Settings → System → Configure company
service** action appears only when company-service setup is needed. It offers
**Service for companies** only and is hidden once setup is complete.

| Scenario | `personal_providers` | `shared_resources` |
| --- | --- | --- |
| For myself | true | false |
| Service for companies | false | true |
| Central server | unchanged | unchanged |

The two local modes are mutually exclusive, including for Administrators and
existing keys. There is no personal-provider override in company mode. The
selector is their only console control. Explicit API writes must select exactly
one local provider mode; invalid combinations are rejected.

Legacy hybrid rows use company service effectively, with personal provider access
closed, without changing stored flags at startup. Legacy disabled rows remain
fail-closed. The read-only `mode_selection_required` settings field enables an
explicit reviewed Apply for these rows even when the effective company option
is already selected. A mode change retains version and active-work safeguards.
An unrelated quota write requests explicit mode selection rather than changing
legacy provider flags implicitly. Choosing Central server keeps local policy unchanged
and opens the existing remote authorization flow.
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

In **Settings → System → Change Mode**, select **Central server**. The brief disclosure
explains that the installation's name and address will be shared. Activate
**Connect to central server** to start; choosing a radio or opening the dialog does not
create a request or change local resource policy. This scenario also opens an
existing connection. The header's current-company selector remains local.

The backend fixes the destination to the canonical Central server origin and derives
the display label/address from trusted public configuration (`UI_PUBLIC_URL`,
with the existing `AUTH_PUBLIC_URL` fallback). No browser-supplied name, address,
Host or Forwarded header selects the destination or installation identity. With
no configured public address, the display fallback is `http://localhost` and
“Local installation”. These values are display-only, not ownership proof.

Sign in on the central server if necessary, using the existing Google or verified email
flow. The local portal never collects that password. A blocked popup leaves an
**Open central server** link. Central server shows the signed-in account, installation details
and **Check that this code matches the one in your installation**. Compare the
code before explicitly choosing **Connect**; the supplied name/address is not
verified ownership. Sign-in, inspection or closing the dialog grants nothing.
The customer who initiated the connection approves it; a central operator does
not approve on another customer's behalf or become its default billing account.
For a new `company-api-v1` approval, the permission text discloses access to the
company's generation, members, billing and API keys, active until revoked. It
does not add credit or grant system administration. Existing `generation-v1`
approvals retain their separate generation-only permission text and expiry.

Approval shows a plain return instruction. There is no redirect to the reported
installation address or wildcard cross-window messaging. The local UI shows
**Connected** only after redemption, binding verification and local credential
save. Reload resumes the retained request. An uncertain start, redemption or
cancellation is reconciled against the saved request/connection before another
intent is allowed; retries never silently replace the proof. Existing connected
metadata and version must match before a lost reply can be treated as success.
If a verified dedicated save committed before pending-request cleanup failed,
the backend locks and checks the exact owned attempt and one-step saved revision
before finishing cleanup. It preserves the credential and company; cleanup
neither reissues nor revokes access, even after the request deadline. Other
revisions, destinations, legacy or disconnected records cannot use this path.
The connected summary offers the existing generation workspace separately.

The first confirmation for an installation atomically creates a **new dedicated
company**, makes that customer its **Supermanager**, and records an immutable
installation-to-customer/company binding. Existing companies are never offered
or selected in this onboarding path. The company starts with the existing $0
credit and allowance policy; no credit, payment or balance transfer is created.
The customer manages funding, member limits and permitted keys through the
normal company controls. Closing, denying or merely inspecting does not create
a company or issue a credential.

A separate random 32-byte installation proof identifies the physical local
installation's connection to one normalized central HTTPS origin. It is shared
by local administrators only through the protected backend, and is encrypted in
the local database; the central server stores only its digest. It is never
browser state, a display name, a reported client URL or a replaceable API key.
Distinct installation databases receive distinct proofs and dedicated companies,
even for the same customer and identical display names. Retries, cancelled or
expired device requests, restarts, renewal and reconnect reuse the saved proof
and original company. Another central customer cannot take over that binding.
Preserve the database and encryption key when restoring an installation; a
cloned database retains its installation identity and must be treated as a
restore, not as a distinct new installation.

The local backend polls with a random 32-byte device secret that is never
returned to the browser. The approval window shows a short verification code
and safe request metadata. No inbound callback, public
self-hosted domain, CORS expansion or shared client secret is required, so
localhost/LAN clients work through outbound HTTPS. Requests expire after ten
minutes and enforce polling/rate limits. This is a bounded device-style protocol
informed by [RFC 8628](https://www.rfc-editor.org/rfc/rfc8628), not an OAuth
interoperability claim. Device secrets remain encrypted locally. Only their
hashes are stored on the host.

After browser approval and proof of the device secret, the host rechecks current
company ownership, account role and policy. It uses the existing API-key issuer
to create one shared, company-bound customer key. New `company-api-v1` approvals
issue the scopes `generation`, `organizations:manage` and `keys:manage`, with
no fixed expiry. Existing per-key request limits, active-key limits, company funds and
member allowance apply. A lost successful response returns the same credential,
not another key. Delivery ciphertext is inaccessible after request expiry and is
cleared during subsequent starts or explicit revocation/cancellation. The local
backend verifies `/v1/key` before atomically saving its encrypted credential.
The approved access version is retained on retries; replaying an existing
approval or issued key never silently expands its scopes or removes its expiry.
Upstream provider keys stay on the host. Protect database backups and keep the
existing encryption key stable.

#### Current company interface

The owning local Administrator's signed-in browser receives the saved company
key from `/auth/company-connection` in a `no-store, private` response. Other local
users cannot retrieve it. It is an ordinary bearer credential accessible to that
browser and its JavaScript, so use HTTPS outside loopback development. The fixed
central proxy requires that owner's local session, the exact saved bearer key
and `X-Company-Context`; writes also require the local Origin. Central APIs keep
their existing scopes, membership and role checks. The key remains encrypted at
rest locally. See [Frontend separation](frontend-separation.md#connect-the-existing-ui-to-central-server)
for the current interface, media transport and failure behavior.

#### Legacy generation workspace

Existing `generation-v1` approvals remain generation-only, valid for up to
30 days. This access version is separate from the installation's
`legacy`/`dedicated` provisioning state; an older dedicated generation grant
does not gain company-management scopes. The legacy generation proxy keeps its
saved bearer credential in the backend.

Every call through that generation proxy carries the opaque `context` from the connection the user
reviewed, including its revision, remote origin, company and account binding.
`X-Workspace-Context` is required; GET/HEAD result links can instead use the
`workspace_context` query value. Missing context returns 428; a changed context
returns 409 before any remote dispatch. A request admitted against a snapshot
keeps that original destination/credential even if the saved connection changes
later. Response links carry the admitted snapshot context. The token is a
precondition, not an authentication credential.

In that workspace, the customer's daily catalogue, estimate, asynchronous generation, latest 20
history items, balance and result downloads remain in the local interface.
The host remains authoritative for membership, model grants, prices/markup,
funding and allowances, billing, files and retention. There is no local task mirror or
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

Customer onboarding is hosted automatically by the canonical central installation,
as derived from the trusted configured console origin. Ordinary installations,
including staging, cannot enable hosting. There is no writable
`customer_workspaces` setting; legacy stored values have no authority and remain
preserved. Central uses its companies-only shared-resource policy even when old
stored resource flags differ. Role detection itself writes no configuration,
creates no company or funds, and revokes no grant. Browser sign-in, provider
rights/catalogue, funding, storage and payment readiness remain prerequisites.
Authorized grant reads, cancellation and revocation remain available after a role
change, with the existing ownership and cancellation-proof checks.

New companies retain the existing $0 funding and allowance defaults. Use the
existing company controls for credit, member limits, model grants, storage and
payments. The connection wizard performs no top-up transaction and introduces no
new identity or billing system. Registration/email delivery, provider rights and
host configuration remain prerequisites.

**Reconnect** confirms a replacement key for the same
installation company, preserving the current connection until verification
succeeds. The customer may explicitly revoke this installation's own prior key
as the replacement is issued. Keys from another installation cannot be rotated
through this request, even when the customer owns both companies. New company
access requires explicit `company-api-v1` consent and has no fixed expiry;
retained `generation-v1` approvals keep their original scopes and expiry.
Automatic renewal and refresh tokens are outside this flow. For an expiring
generation grant, reauthorize explicitly before expiry.

The account menu shows **Installation access** only when that account has
issued grant records. It lists that customer's own installation/company access,
including clearly marked legacy grants, and permits revocation even when new
onboarding is disabled. It is not a central administrator's list of all customer
companies. Company administration remains in the existing company controls.
Revoking a role/membership or key is enforced on subsequent requests.

**Cancel** serializes with issuance and revokes any key that
request issued. Cancellation keeps remote company data and funds. Cancellation remains able to revoke a key after the ten-minute request deadline;
expired consent/redemption remain forbidden. If cancellation is uncertain, retry
it: only positive host confirmation permits local proof deletion. Ambiguous 410,
malformed replies and expiry do not authorize discarding or overwriting that proof.

**Disconnect** removes the local saved key and increments a tombstone version,
preventing stale connection replies from restoring it. It preserves remote jobs,
charges and files and does not itself revoke the host key; use **Installation
access** on the host for revocation or loss of an installation. Keys are
scoped per installation and company. Other local Administrators cannot access a
connection they did not create. Revoking the local Administrator role removes
subsequent access to this local connector.

### Existing connections and rollback

Migration 083 preserves existing companies, members, keys, balances, grants,
history and files. Existing connections remain explicitly **legacy** and keep
their original company. No migration infers that an existing company belongs to
a new installation. Their existing requests continue to use the saved key and
company snapshot. **Replace legacy key** accepts only a replacement
generation-only shared key for that same active connection, origin, customer
and company; it cannot create a fresh connection or change its billing company.
The new-company wizard refuses an active legacy connection. To provision a
separate dedicated company, explicitly disconnect first; old activity and grants
remain in the original company. Disconnect does not revoke an old grant.

Upgrade both peers before starting a new dedicated connection. A pending legacy
request cannot be relabeled as dedicated. An already-issued legacy credential
remains recoverable at the central protocol endpoint, but the new local wizard
will preserve and report that pending legacy state rather than save it as a new
company binding; positively cancel the request before beginning a fresh one.
Cancellation preserves companies and history and explicitly revokes only the
key issued by that cancelled request.

Migration 083's Down preserves its additive tables, proof ciphertext and binding
metadata. Reapplying Up reuses them. Roll back application binaries without
dropping these structures, grants, credits or task snapshots. Old binaries do
not understand dedicated onboarding: avoid starting new onboarding while running
an older release, and keep a verified backup for an explicit disaster restore.

### Legacy generation proxy limits

The legacy generation proxy permits only catalogue, current credential/balance, generation
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
processing fee; the choice is fixed on each new payment. See
[organization funding](payments.md) for webhook setup, test/live isolation, refunds,
reconciliation and the separate trusted integration API.

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
