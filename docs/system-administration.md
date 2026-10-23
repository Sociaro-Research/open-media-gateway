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

Password sign-in works without email delivery. Public registration and sending
password-reset links require configured delivery; existing confirmation/reset
links can still be completed while delivery is temporarily unavailable. Login
method switches continue to control new sign-ins.

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

## Settings

The page is titled **Settings**, with the general options under **System**.

Provider availability is controlled in **Shared providers** (shared connections) or **Providers** (personal connections), using **Available for generation**. There is no installation-wide provider allowlist. Model defaults and organization-specific model grants still apply.

All administrators can configure [shared providers and storage](shared-resources.md), enable shared access and manage user budgets. Shared resources belong to the installation, not an administrator’s personal account.

- **Show Playground:** hides the Playground screen for everyone; API generation remains available.
- **Allow personal provider configuration:** controls non-administrators' ability to view and manage personal connections through the console and API. Saved connections are preserved, but personal keys cannot start new generations while this is disabled. Administrators retain access to their own connections.
- **Allow reference files in generation requests:** enabled by default. Allows Base64 image transfer for BytePlus using signed local-storage library references, within the payload limit. External URLs, oversized payloads, video/audio references, and providers without inline transfer continue to use links. S3 references always use direct signed storage URLs. Disabling sends only HTTP(S) links or model-supported registered `asset://` references and rejects direct data URLs, including at dispatch for queued tasks. The worker reads the current setting before generation, without a restart; requests already sent are unaffected. This applies to shared and personal connections. The API field is `allow_inline_references`; omitting it on settings updates preserves its current value.
- **Email delivery:** select Resend and configure its sender and API key. This controls sending account emails and system notifications, independently of sign-in methods. Keys are encrypted and never returned to the console or activity log.
- **Login methods:** add and configure Google or email in expanded cards. Google Client ID and Client Secret are editable; the callback URL is read-only. Switches preserve credentials, users and sessions. Password sign-in works without delivery; registration and recovery require it, with a warning in the email card. See [Email authentication](email-authentication.md).

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
