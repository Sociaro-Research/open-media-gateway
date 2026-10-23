# UI smoke checks

Run the real persisted display-name navigation regression with fresh services:

```sh
GATEWAY_PROFILE_NAME_BROWSER_NODE=node python3 scripts/with-test-services.py -- \
  go test -race -count=1 -v -run '^TestProfileNameBrowserReconciliation$' ./internal/api
```

It holds actual PATCH responses after server processing, navigates through the
Profile controls, and measures the input, header, Save state and independent SQL
read. It covers untouched forms, newer drafts, deliberate reversion, repeated
submission, normalization, real validation failure, one injected repository
failure with an identical retry, reload and 1440/390/320px. Set
`UI_BROWSER_ENGINE=webkit` for WebKit and `UI_SCREENSHOT_DIR` to retain images.
The test refuses to run without the isolated database variable; the ordinary Go
suite skips this explicitly enabled browser check. The dedicated CI job enables
both engines with its own PostgreSQL service. No live identity/provider calls.

Run `UI_BROWSER_PATH=/path/to/chromium node scripts/ui/workspace-navigation.cjs`
for explicitly gated workspace reads: late failures/successes, A→B→A, leaving and
returning, current error feedback and keyboard retry. Each case uses a fresh
browser context and localhost API fixtures; it makes no real account/provider
requests and uses no timing sleeps. `UI_NAVIGATION_RESULTS` optionally names a
JSON results file. The existing `workspaces.cjs` suite separately checks workspace
creation, key-name readiness and required storage fields.

Run `node scripts/ui/installation-setup.cjs` (also with
`UI_BROWSER_ENGINE=webkit`) for the first-owner setup screen, desktop/mobile
layouts, keyboard controls, invalid-code retry and subsequent password sign-in
without delivery. It uses local fixtures and creates no live account.

For reliability/load commands and browser request budgets, see
[performance testing](../../docs/performance-testing.md).

Run `node scripts/ui/smoke.cjs` with Playwright available in Node's module path.
The script starts a temporary localhost server and uses mocked API responses;
it never submits a request to a real media provider.

Optional environment variables:

- `NODE_PATH`: directory containing the installed Playwright package.
- `UI_BROWSER_PATH`: Chrome/Chromium executable, if not using Playwright's bundled browser.
- `UI_SCREENSHOT_DIR`: screenshot directory (default: `/tmp/media-gateway-ui`).

Checks cover desktop and mobile layout, compact read-only confirmation, double
submission prevention, the two-second minimum, slow requests, retry idempotency,
running and completed timers, copying settings across media types, provider
navigation, S3/R2 storage setup, connection checks, save failures, credential clearing,
switching back to local, clipboard access, and reduced motion.

Run `node scripts/ui/organizations.cjs` for organization funding, role management,
weekly limits, invitations, organization-bound keys and responsive layout. Run
`shared.cjs` and `accounting-references.cjs` for shared connection and report regressions.

Run `node scripts/ui/storage-settings.cjs` (also with `UI_BROWSER_ENGINE=webkit`)
for provider-specific storage fields in personal, shared and organization scopes:
Backblaze key names and automatic region detection, R2's internal `auto` region,
Amazon and custom settings, required-field/provider-endpoint validation,
connection checks, save/reopen, automatic CORS repair for unchanged connections,
permission errors, propagation polling without repeated writes, preserved
secrets/addressing, optional prefixes,
managed storage, keyboard navigation and both themes at 1440/390/320 px. All API
and storage responses are isolated fixtures; no cloud credentials are needed.

Run `node scripts/ui/report-pagination.cjs` (also with `UI_BROWSER_ENGINE=webkit`)
for distinct report pages, draft user/ID/date/key filters, Apply resets, failed
reads/retry and full applied-filter CSV contents. It uses 123 synthetic rows
and rejects external requests and API writes. Evidence defaults to
`/tmp/gateway-report-pagination`; `UI_SCREENSHOT_DIR` changes the destination.
`UI_ROOT` can point to an archived UI directory to run the identical fixture
against a baseline runtime. CI retains both engines' matrices and screenshots.

Run `node scripts/ui/audit-log.cjs` for audit filters, cursor pagination,
keyboard/date controls, read-only event/revoked-key details, scoped organization
reads, safe text rendering and both themes at 1440/390/320 px. It uses isolated
browser fixtures; backend permissions and migration invariants have separate
PostgreSQL/API tests.

Run `node scripts/ui/monitoring.cjs` with the pinned `CADDY_BIN` for cookie-only
Administrator access, identity-header forgery/API-key denial, role/dependency
failure, private credential stripping, documentation-host isolation, login return
and the shared Monitoring action at desktop/mobile widths with keyboard controls.
By default Grafana is an isolated HTTP fixture. To verify actual Grafana file
provisioning, Viewer permissions and Prometheus queries, use a separate project:

```sh
docker compose -p gateway-monitoring-ci --profile monitoring -f compose.yaml -f monitoring/grafana/compose.test.yaml up -d --wait grafana
GRAFANA_TEST_UPSTREAM=127.0.0.1:13000 GRAFANA_TEST_UI_PORT=18088 node scripts/ui/monitoring.cjs
docker compose -p gateway-monitoring-ci --profile monitoring -f compose.yaml -f monitoring/grafana/compose.test.yaml down
```

This project runs no gateway/worker and uses its own Prometheus/Grafana volumes;
application panels may have no data and missing-target alerts are expected.
Never use the test override or test project name for a deployment. If Docker is
remote, tunnel only that isolated Grafana port and retain the same local UI port.

Security regressions: `logout-security.cjs` checks failed session revocation and
successful retry from Playground/Profile on desktop/mobile using the keyboard.
`origin-security.cjs` checks the external origin guard against direct peers and
forged forwarding headers; `log-security.cjs` checks removal of API keys, sessions,
signed links and OAuth query secrets from access logs. The latter two need
`CADDY_BIN` pointing to a verified Caddy 2.11.7 binary. They start isolated local
fixtures and do not change the server. `media-security.cjs` is driven by the
opt-in real Go API/Chromium check documented in
[security testing](../../docs/security-testing.md).

Run `node scripts/ui/openai.cjs` for OpenAI image quality/background controls,
model-specific speech fields, the credential form, the unchanged square SVG icon,
and desktop/mobile layout. It uses local fixtures and no paid provider calls.

Run `node scripts/ui/sonilo.cjs` for all ten Sonilo operation profiles: music
duration, fractional effects, media-driven/no-prompt forms, required references,
combined sound controls, tariff fields, standard floating labels, keyboard
activation/Escape and both themes at 1440/390/320px. It invokes `go run` to read
the actual module catalog (Go must be on PATH), then uses local browser API
fixtures. It makes no Sonilo request or paid submission. Screenshots are saved
under `/tmp/gateway-sonilo-ui`. Backend archival/download/webhook verification
is the separate isolated Go workflow test.

Run `node scripts/ui/email-delivery.cjs` for system-admin Resend configuration,
saved credential masks, key replacement and preservation in email and storage
settings, conditional login availability and responsive layout. Credential inputs
must use `FormUI.credentials` with `type="text"` and `autocomplete="off"`; only
the account sign-in password uses a password input. Saved masks are display-only
and must never be sent as replacement secrets.
Set `UI_BROWSER_ENGINE=webkit` to check the same behavior in WebKit.

Run `node scripts/ui/references.cjs` for local and direct S3 uploads, byte/credential
routing (including B2 payload-signed Authorization headers), hashing in a worker,
cancellation, failure without proxy fallback, library
operations and mobile layout. All storage requests are intercepted fixtures.

Run `node scripts/ui/reference-intent.cjs` (also with `UI_BROWSER_ENGINE=webkit`)
for voice-picker Escape/Close/Done cancellation followed by ordinary audio
selection or upload, gated interrupted uploads, repeated opens, successful voice
assignment, clear/reset and model return at 1440/390/320 px. It uses actual model,
library and file-chooser controls, synthetic file-drop events and isolated API
fixtures; it never submits generation. `UI_SOURCE_DIR` selects an archived UI
tree for the identical baseline regression. Results and screenshots use
`UI_SCREENSHOT_DIR` (default `/tmp/gateway-reference-intent`).

Set `UI_VOICE_UPLOAD=chooser` for the native Wan voice-upload follow-up. It repeats
the full cancellation/delayed-upload/reset matrix through real `filechooser`
events and adds click/Enter/Space, empty-selection retry, Change voice/clear,
model/mode scoping, busy/duplicate, MIME/50 MB validation and failed preparation
retry. Files are selected only through the chooser returned by the browser,
after asserting that its input is enabled. The default drop-based matrix remains
available. `UI_CASE_FILTER` selects a diagnostic case by name; CI runs all cases.

Run `node scripts/ui/request-limits.cjs` for system defaults and the shared
organization override editor: inheritance, save feedback, conflict preservation,
and 1440/390/320 px layout. `UI_BROWSER_ENGINE=webkit` selects WebKit.

For account email previews, first run
`GATEWAY_EMAIL_PREVIEW_DIR=/tmp/gateway-email-preview go test ./internal/auth -run TestAccountEmailPreviews -count=1`,
then `node scripts/ui/account-emails.cjs`. This renders the actual delivered
HTML using fixture links, without sending email. Both purposes are checked at
680/390/320 px, including stripped-style and blocked-image fallbacks.

Run `node scripts/ui/payments.cjs` for Stripe provider configuration, both processing
fee modes, credential masks, administrator visibility, payment history, idempotent
retries, Checkout returns and 1440/390/320 px layout. It uses only mocked APIs.

Run `node scripts/ui/provider-connections.cjs` for independent connection creation,
custom headers and endpoints, manual model profiles, masked-secret preservation,
archive, deferred successful/failed-save locking and retry, model/header dirty
feedback, provider artwork in the API profile picker, keyboard dialogs, and
1440/390/320 px layouts. All APIs are fixtures.

Run `node scripts/ui/model-picker.cjs` for model filters, keyboard selection,
nested calendars/dropdowns, organization grants, markup exceptions and scrollable
results at desktop, mobile and short viewport sizes. It checks original provider
artwork, missing-image fallbacks, distinct connections with identical names,
long-name wrapping and both themes. The long list must fill the available popup
height instead of collapsing below its filters. Set `UI_BROWSER_ENGINE=webkit`
to run in WebKit; screenshots are saved separately for each engine. WebKit's
existing ResizeObserver scheduling notice during viewport changes is
logged separately from application errors.

Run `node scripts/ui/model-information.cjs` for model information and generation
reuse, including failed Seedance 2.0 tasks with distinct public/native model IDs,
video references, renamed public aliases, unavailable-model draft preservation
and keyboard copying at 1440/390/320px. It uses local API fixtures and makes no
paid provider request. `UI_BROWSER_ENGINE=webkit` selects WebKit.

`node scripts/ui/branding.cjs` verifies administrator appearance drafts,
save/reload, hex validation, image upload, conflict/retry, reset, both themes,
public pages, keyboard controls, mobile layout and non-administrator navigation.
It uses a temporary localhost fixture and stores screenshots in
`/tmp/gateway-branding-ui`; it does not modify a deployed installation.

Run `node scripts/ui/organization-roles.cjs` for the shared permission matrix's hover,
touch, keyboard and 1440/390/320 px behavior, delegated recipient selection,
Manager content redaction, Content Officer read-only access and Member controls.

Run `node scripts/ui/catalog-sync.cjs` for common catalogue timezone/email settings,
offset-first grouped timezone labels, complete whole/fractional offsets,
preservation of existing IANA identifiers across unrelated saves,
provider schedules and sources, safe check/warning/version details, source
snapshots, readable email states (including unknown/missing values), unchanged
JSON details/export, keyboard/Escape and both themes at 1440/390/320px.
Set `UI_BROWSER_ENGINE=webkit` to run the same checks in WebKit.
It serves local UI files against isolated API fixtures, sends no email and makes
no provider request. Screenshots are written to `/tmp/gateway-catalog-ui`.

Run `node scripts/ui/sales.cjs` for the Administrator sales hierarchy, skipped
supervisor levels, pointer moves and role review, keyboard/touch alternatives,
adding existing users, stale saves, access loss and scoped leader reassignment.
It checks both themes at 1440/390/320px using local API fixtures. Set
`UI_BROWSER_ENGINE=webkit` to exercise the shared controls in WebKit; native
pointer drag/drop is checked in Chrome, while both engines check the equivalent
dialog workflow. Screenshots default to `/tmp/gateway-sales-ui` and can be
redirected with `UI_SCREENSHOT_DIR`.


The sales suite also exercises independent permission switches, grant metadata,
uncapped top-up permission, the shared top-right close icon, stale-save draft preservation, supervisor revocation without
self-grant rights, session-only requests, and company top-ups/markup controls.
Permission dialogs are captured in both themes at 1440/390/320px. API fixtures
verify UI contracts; PostgreSQL tests cover effective grant chains and mutations.

The sales suite additionally checks global bonus rates, the fourth delegated
permission, scoped bonus balances/history, USD payout retries with a stable
operation ID, overpayment into a negative balance, and self-payment UI exclusion.
Run in Chrome and WebKit with the existing environment variables. The report
summary suite includes the profit chart, negative profit bars, pending counts and
financial status in the generation table. These are local API fixtures; real
profit recognition, bill import, payout authorization and ledger immutability are
covered by isolated PostgreSQL/API tests.

Sales hierarchy fixtures also verify a loaded account picture, missing/broken
photo initials and non-draggable images. Existing theme, desktop/mobile and
keyboard/drag screenshots cover the shared avatar identity row.

The organizations suite checks required company websites in administrator and
sales creation, field entry out of order, duplicate name/site messages, draft
retention and operation-ID rotation after editing a failed create. Creation
screenshots cover both themes and 1440/390/320px. Real duplicate and concurrent
create/rename checks run in the PostgreSQL company identity tests.

Run `node scripts/ui/stablecoins.cjs` (also with `UI_BROWSER=webkit`) for the
installation seller/company and country/status VAT rules, manual verification
and revocation, Stripe-free funding, and the stablecoin provider's three-network address list, token multiselect, Safe link,
immediate availability switch, tax/billing fields, exact six-decimal amount and
fee, retry idempotency, payment card, copy controls, cancellation and history
access after disabling providers. It uses local fixtures in both themes and at
1440/390/320 px; it submits no real payment or tax calculation.


The stablecoin browser suite also verifies the Stripe invoice switch, independent
invoice-logo upload/removal with section Save, local-card billing without visible
network/token controls, native-card mode, and authenticated PDF downloads for
pending stablecoin/card payments. It retains desktop/mobile and theme checks;
PDF contents and numbering are verified separately by server/integration tests.
Seller settings also cover optional VAT/OSS state, blank-number saves, field
errors from client/server validation, exact heading/group/table spacing, hidden
Remove without a logo, keyboard upload/removal, square and wide logo containment,
96px horizontal previews and 12px action gaps at 1440/390/320px in both themes.
Screenshots are saved to `/tmp/gateway-company-settings-{chromium,webkit}/`.
The same suite measures the payment card heading's compact Save/Cancel buttons,
12px action gaps and 44px table/add controls; it checks keyboard draft cancellation
without writes and verifies that immediate availability changes do not dirty the
settings form. New-card screenshots use
`/tmp/gateway-payment-actions-{chromium,webkit}/`. The Stripe suite also measures
the matching heading actions and exercises native keyboard submission.

Run `node scripts/ui/stablecoin-network-removal.cjs` (also with
`UI_BROWSER_ENGINE=webkit`) for duplicate-network validation after row removal,
both removal directions, unrelated-row removal, failed save/retry and subsequent
editing/reopening at 1440/390/320 px in both themes. It uses shared keyboard
controls and synthetic localhost provider settings only. `UI_SOURCE_DIR` can
select an archived UI for the same baseline regression; `UI_SCREENSHOT_DIR`
selects the evidence directory and `UI_CASE_FILTER` selects a named case.

For server-produced visual fixtures, run:

```sh
GATEWAY_INVOICE_PREVIEW_DIR=/tmp/gateway-invoice-previews go test ./internal/payments -run TestInvoicePDFAndLogo -count=1
```

Render those PDFs with Poppler before assessing document layout. Browser fixtures
alone do not prove PDF contents, a real Stripe checkout or a real crypto transfer.

Run `node scripts/ui/support.cjs` for profile contact creation/removal/persistence,
failed saves, unsafe-link handling, company Support visibility and empty/retry
states, member contact links, stale responses, keyboard controls and both themes
at desktop and 390/320 px. Uses isolated HTTP fixtures; no external messages.


The payment suite also follows a PDF's account link after a simulated login/reload,
opens a renewed Stripe fixture without creating another payment, handles already
paid/expired requests, and verifies that an expired stablecoin card has no QR or
send/delete action. Backend expiry tests cover exact 720-hour deadlines across
DST, stopping inactive wallet RPCs, permanent amount reservations, rejecting late
credits, restarting a wallet at the current head and preserving paid history.

`provider-connections.cjs` also covers the default response timeout, keyboard edits,
validation, saved values, independent connections and narrow layouts; use
`UI_BROWSER=webkit` for its second browser engine.


Run `node scripts/ui/provider-assets.cjs` and repeat with
`UI_BROWSER_ENGINE=webkit` for BytePlus group creation, source upload, async
readiness, remote inventory import, automatic `asset://` selection, guarded
delete, workspace detach and original retention. Numbered-prompt checks cover
ordered hover/touch/keyboard previews, red missing indexes, browser-only submit
blocking and recovery after adding/removing images or editing text, literal HTML safety,
unchanged submission text, native Undo, Wan first-frame numbering, Agnes `<Picture N>` markers, long scrolling
prompts, both themes and 1440/390/320px. Fixtures are synthetic; no upstream
registration, paid generation, real account rights or deployment is exercised.

Browser evidence respects `UI_EVIDENCE_DIR` (or `UI_SCREENSHOT_DIR`) through the
shared `evidence.cjs` path helper. The local release launcher selects a dated,
per-suite/variant directory under ignored `dist/checks/`. Without an override,
legacy screenshot names are resolved inside the current user's temporary
directory; tests never need write access to another macOS user's `/tmp` files.

The persisted contact workflow runs via `TestProfileContactsBrowserWorkflow` in
`internal/api` with `GATEWAY_PROFILE_BROWSER_NODE=node`, Playwright and an isolated
`GATEWAY_TEST_DATABASE_URL`; use `scripts/with-test-services.py` locally. It serves
the real UI and auth/profile/company API against PostgreSQL, without mocked API
responses. It checks ordinary email → Save → PATCH → reload → a second authorized
company viewer, plus invalid/mixed rows, field errors, reversion, pending-write
locking and desktop/mobile layouts. The Go CI job installs the browser and enables
this workflow explicitly. `UI_BROWSER_PATH` can select local Chromium and
`UI_SCREENSHOT_DIR` retains screenshots outside the public source tree.

Run `node scripts/ui/provider-reference-state.cjs` for repeated/overlapping
BytePlus imports, key reset during import refresh, interrupted group creation
followed by key/connection changes, Refresh recovery and keyboard navigation away
from prompt previews. All APIs use delayed synthetic fixtures. Repeat with
`UI_BROWSER_ENGINE=webkit`, `UI_VIEWPORT_WIDTH=390` or `320`, and `UI_THEME=dark`.
`UI_SOURCE_DIR` selects a baseline UI tree for the identical regression;
`UI_SCREENSHOT_DIR` selects the local JSON results and screenshot directory.

Run `node scripts/ui/documentation-schemas.cjs` and repeat with
`UI_BROWSER_ENGINE=webkit` for composed documentation schemas. It uses Go's
`docs.JSON()` to serve the complete current OpenAPI contract from a temporary
localhost fixture, then separate synthetic schemas for required fields, reference
siblings, alternatives and bounded recursion. Go and Playwright are required.
Checks cover ReportGroup, ReportSummary, the three report endpoint responses,
keyboard navigation, both themes and 1440/390/320px. No live API is called.
`UI_EVIDENCE_DIR` selects the screenshots and JSON results directory;
`UI_DOCS_SCRIPT` can select an archived renderer for a baseline regression.

Run `node scripts/ui/documentation-theme.cjs`, also with
`UI_BROWSER_ENGINE=webkit`, for same-origin, sibling-domain and unrelated-domain
theme preferences. Intercepted HTTPS fixtures exercise explicit cookie scope,
public-suffix rejection, invalid values, blocked storage, direct visits, redirects,
section hashes, System changes and preference clearing. No external services are
called. `UI_EVIDENCE_DIR` selects results/screenshots at 1440/390/320px. The existing
`logout-security.cjs` suite checks successful versus failed sign-out through the
real console handler, and `separation.cjs` checks the preference header through
real Caddy while retaining public/private route isolation; both accept the same
browser-engine setting.

Run `node scripts/ui/report-axis.cjs` and repeat with
`UI_BROWSER_ENGINE=webkit` for Usage axis precision, zero and negative ranges,
micro amounts and compact large amounts. The synthetic local fixtures check
five distinct labels, numeric accuracy, unchanged bar geometry and tooltip
amounts, keyboard tooltips, both themes and 1440/390/320px layouts.
`UI_SOURCE_DIR` selects a baseline UI tree; `UI_EVIDENCE_DIR` selects the
screenshots and JSON results directory. No live API or paid provider is used.

Run `node scripts/ui/mock-audio.cjs` (also with `UI_BROWSER_ENGINE=webkit`) to
decode and play the fixed MP3 returned by Mock Speech and Mock Music. It checks
MIME type, size, duration, stereo PCM signal and playback to completion using only
a local HTTP fixture. This does not call a provider or test physical speakers.
JSON evidence is written through the shared evidence directory helper.

Run `node scripts/ui/legal-documents.cjs` and repeat with
`UI_BROWSER_ENGINE=webkit` for the legal-document editor and public display.
Local fixtures cover default templates, independent documents, literal HTML
payloads, save/cancel/Escape, pending writes, conflicts, reset, public read
failure/retry, both themes and 1440/390/320px. `UI_EVIDENCE_DIR` stores screenshots.
Backend permission and persistence tests run separately with disposable services.

For a real session/API/PostgreSQL round trip, enable
`GATEWAY_LEGAL_BROWSER_NODE=node` and run `TestLegalDocumentsBrowserWorkflow`
through `scripts/with-test-services.py`. It uses an isolated schema, local users
and the actual company-settings editor. It verifies public reads, anonymous and
member denial, persisted text, concurrent-edit conflicts, literal HTML display,
independent company saves, the UTF-8 byte limit and template reset.
Repeat with `UI_BROWSER_ENGINE=webkit`. No live provider or payment calls run.

Invitation feedback is covered by `organization-roles.cjs` and `organizations.cjs`
(`UI_BROWSER_ENGINE=webkit` selects WebKit). Fixtures check post-success warnings
with no effective login method, email, Google, both, failed invitation creation,
unavailable or malformed configuration, and first-owner invitations. Delivery
configuration alone does not determine sign-in availability.
