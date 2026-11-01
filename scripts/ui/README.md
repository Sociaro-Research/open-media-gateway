# UI smoke checks

## Required design and spacing review

For every UI feature or fix, read [the design system](../../docs/design-system.md)
before implementation and compare rendered output against its
[spacing and visual review gate](../../docs/design-system.md#spacing-and-visual-review-gate)
before completion. Reuse its existing tokens and shared spacing patterns.
Inspect component-to-component gaps, label/switch separation, heading rhythm,
alignment, panel gutters and wrapping in before/after screenshots and adjacent
affected screens. Exercise both themes at 1440/390/320px, keyboard behavior and
applicable dirty/saved, disabled, pending, error/retry and interrupted/repeated states.
Passing assertions or capturing screenshots without inspecting them is insufficient.
Keep concrete design comparisons, screenshots and executed/unexecuted checks in
ignored `.local/` or `dist/`; record limits explicitly.

## Available fixtures

Run `node scripts/ui/duration-labels.cjs` (also with `UI_BROWSER_ENGINE=webkit`)
for actual Templates/Playground model changes, bounded/stepped/fixed/one-sided
Duration labels, unknown/input-derived/unsupported capabilities, native validation
and readable labels in both themes at 1440/390/320px. It blocks external requests
and submits no generations. `UI_EVIDENCE_DIR` selects its internal screenshots.

`template-file-selection.cjs` checks retained covers, image/video library reuse,
drafts without covers, publication gating, fixed binding removal without deletion,
save/reload, keyboard focus, author visibility and measured Publish/settings gaps.
It uses synthetic media in both themes at 1440/390/320px; generate the disposable
video fixture described below first. `UI_EVIDENCE_DIR` selects its evidence.

`switches.cjs` measures shared switch/label spacing and alignment.
`override-sound.cjs` covers selected-only internal Remove, remove-before-select,
empty-upload failure/retry, native preview, persistence and interrupted uploads.
`shared-reference-library.cjs` checks Play overlays, selection/playback isolation,
configured accent/focus tokens, delete cancellation and repeated close/reopen.
Run these in Chromium and WebKit, then inspect their screenshots against the
design review gate above. Templates' `templates.cjs` additionally exercises fixed
and variable binding removal, cover retention, publication and scoped libraries.

Run `node scripts/ui/reference-validation.cjs` in Chromium and with
`UI_BROWSER_ENGINE=webkit` for early reference errors, model changes, library
binding, rejected replacements, cancellation and stale uploads, plus template
Save/Publish gating, voice-role selection, concurrent manual edits, variable-slot replacement and Unpublish. It uses synthetic transfers and dummy localhost APIs, checks
keyboard navigation and both themes at 1440/390/320px, and makes no live calls.
It also gates simultaneous variable-slot bindings against aggregate limits and
ignores delayed model-check errors after valid replacements. Creator upload
fixtures distinguish model-context uploads from generic shared source uploads.
`UI_EVIDENCE_DIR` selects evidence output. API/PostgreSQL regressions separately
exercise actual byte inspection and rejected-file persistence/cleanup.

Run `node scripts/ui/managed-generation-key.cjs` with Chromium and
`UI_BROWSER_ENGINE=webkit` for explicit account-key creation, restoration in a
second browser, local Forget, manual entry, revocation, session/workspace races
and validation failure. Local synthetic fixtures assert bearer-only generation
and capture both themes at 1440/390/320px. `UI_EVIDENCE_DIR` selects output.
Generation-focused fixtures use `fixture-key.cjs` for manual synthetic entry;
the dedicated managed-key fixture covers account creation and restoration.

Run `node scripts/ui/generation-key-panel.cjs` (also with
`UI_BROWSER_ENGINE=webkit`) for the shared Playground/Templates key panel:
session restoration, compact key actions, keyboard focus after Forget, invalid
keys, long names, limit-error recovery, Manage keys navigation and API-key mode. It captures both themes at
1440/390/320px with synthetic keys and blocks external requests and API writes.
WebKit uses Option+Tab for full control navigation. `UI_EVIDENCE_DIR` selects the
output directory; `UI_SOURCE_DIR` with `UI_BASELINE=1` captures an older layout.

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
layouts, keyboard controls, invalid-code retry, the owner session without delivery,
and hidden ordinary email login without delivery, with independent Google availability. It uses local fixtures and creates no live account.

Run `node scripts/ui/setup-settings.cjs` (also with
`UI_BROWSER_ENGINE=webkit`) for setup Apply/Advanced Save reconciliation,
unsaved drafts, real version-conflict controls, cancellation, retries and
interrupted navigation. It uses synthetic loopback API fixtures and covers both
themes at 1440/390/320 px.

For reliability/load commands and browser request budgets, see
[performance testing](../../docs/performance-testing.md).

Run `node scripts/ui/playground-repeat.cjs` (also with
`UI_BROWSER_ENGINE=webkit`) for retained generation drafts, identical repeat
submissions, history Try again across media types, fresh confirmation estimates,
cancellation, late responses, validation/permission/budget failures, idempotent
error retry and duplicate-click safety. It also checks keyboard focus and both
themes at 1440/390/320 px. All API responses are localhost fixtures; external
requests are blocked. `UI_SOURCE_DIR` selects an archived UI for baseline checks.
Evidence uses `UI_SCREENSHOT_DIR` (default system temporary directory).

Run `node scripts/ui/response-context.cjs` (also with
`UI_BROWSER_ENGINE=webkit`) for key changes after response headers arrive but
before the body finishes. A localhost HTTP server gates actual JSON and media
bodies, checking late success/failure, A→B→A, model catalog preservation,
body-read cancellation and Forget key.
`UI_SOURCE_DIR` selects a baseline UI; `UI_EVIDENCE_DIR` stores results. No live
account or provider calls run.

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
for voice-picker Escape/Close cancellation followed by ordinary audio
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

Run `node scripts/ui/color-presets.cjs` (also with `UI_BROWSER_ENGINE=webkit`)
for the Original-only fresh catalog, stale personal selection fallback without
rewriting the preference, legacy palette fallback, saved Clear Lime preservation,
and custom theme creation/editing/deletion, independent saves and keyboard controls
at 1440/390/320px. The alternate palette is a test fixture for existing catalogs;
it is not bundled in new installations. All writes stay in localhost fixtures.


Run `node scripts/ui/auth-session.cjs` and repeat with
`UI_BROWSER_ENGINE=webkit` for console session restoration. Gated localhost
fixtures verify safe HTML before the application script runs, delayed valid
sessions and reloads without a visible login frame, absent/expired sessions,
configuration and transport failures, forbidden/malformed responses, a real
15-second timeout, keyboard retry, late-response rejection, repeated navigation,
email sign-in, failed/successful logout, sanitized return paths, workspace read
failures and API-key mode. Dark, Light and System modes run at 1440/390/320px,
including a live System theme change while checking. `UI_SOURCE_DIR` selects an
archived UI for baseline reproduction; `UI_EVIDENCE_DIR` selects screenshots and
JSON results. No live account, identity provider or paid service is called.

Run `node scripts/ui/playground-projects.cjs` and repeat with
`UI_BROWSER_ENGINE=webkit` for Projects plus the repeat-generation integration.
Local fixtures cover project creation/retry/cancel, empty/All/project history,
older ungrouped generations, reload, stale reads, key changes during writes,
submission-time assignment, history retry project preservation and unchanged
form drafts. Shared dropdown keyboard/Escape behavior and both themes run at
1440/390/320px. Evidence uses `UI_EVIDENCE_DIR`; no provider is called.
Run `playground-repeat.cjs` against the same checkout for its complete retry matrix.

The Projects matrix also covers the separate +/management actions, checked Project
submenus (hover/touch/keyboard), disabled workspace/content-role permissions, long names,
viewport bounds, assignment failure and key changes during pending writes. It
also covers versioned rename/delete/reassignment, duplicate
names, stale drafts, ungrouping old history, confirmed populated deletion and
project-management dialogs in both themes at desktop/mobile widths. Database
fixtures preserve queued/running/completed/failed rows, private request metadata,
result indexes and actual temporary media files while racing delete with submit
and assignment. Project deletion never invokes media deletion.

Run `node scripts/ui/playground-project-polling.cjs` in Chromium and WebKit for
natural 2000ms polling regressions. Do not replace application timers or call
`loadTasks` directly in the measured cases. The fixture verifies unchanged,
unrelated and target-card refreshes, focused anchor/item restoration, 409
review/retry, independent background errors, and closing on task removal,
permission loss or key change.

`node scripts/ui/playground-controls.cjs` checks compact bottom placement and
keyboard operation of Forget key, preserved 24-hour tab/session restoration,
reference selection without Done, X/Escape and focus return. Local fixtures run
both themes at 1440/390/320px; repeat with `UI_BROWSER_ENGINE=webkit`.
Reference-intent, references and provider-assets suites use the retained Close
library action; no fixture depends on a removed Done action.


Run `node scripts/ui/reference-prompts-test.cjs` for the shared recognition fixture
matrix (also consumed by Go template tests), provider-independent parsing,
selected-mode types, missing files and immutable options. Run
`prompt-reference-contracts.cjs` in Chromium/WebKit for exact transmitted prompts,
hover/keyboard previews, informational model hints and model changes. Run
`templates.cjs` for the same editor in authoring, explicit ordered counts and
unchanged consumer requests. Existing saved recipe grammars remain compatible.

Run `node scripts/ui/stored-media.cjs` and repeat with
`UI_BROWSER_ENGINE=webkit` for Journal Open, Download and Last frame renewal.
The localhost fixture advances its signing clock across two-hour idle periods,
checks repeated/duplicate clicks, retained image/audio/video players, missing
media and authorization failures, late responses after key changes/Forget/card
removal, keyboard activation, both themes and 1440/390/320px layouts.
`UI_SOURCE_DIR` selects a baseline; `UI_EVIDENCE_DIR` stores results and screenshots.
All authorization and media requests stay local; no provider is called.

Run `node scripts/ui/setup-remote.cjs` and repeat with
`UI_BROWSER_ENGINE=webkit` for the three setup scenarios, no-write Skip and
reload, failed/reversible preset changes, honest mixed settings, explicit remote
consent, catalogue/estimate/job/history/download, interrupted submission recovery
with the same idempotency key, and disconnect. It checks both themes at
1440/390/320 px using localhost fixtures and blocks all external requests.
`UI_SCREENSHOT_DIR` selects retained images. It does not exercise real account
registration, company creation, credential grants, payments or provider calls.

Run `node scripts/ui/setup-selector.cjs` in Chromium and WebKit for saved setup
selection across reopen/reload, shared plus personal policy, custom legacy
combinations, unchanged Apply, native radio keyboard navigation, no-write
Skip/Close/Escape, failed and interrupted saves, pending locks and version
conflicts. Synthetic localhost fixtures capture both themes at 1440/390/320px
and measure footer text centering, equal button sizes, padding and legend gaps.
`UI_EVIDENCE_DIR` selects evidence; `UI_SOURCE_DIR` with `UI_BASELINE=1` records
the same layout against an archived UI. Backend persistence remains covered by
the setup-profile API and PostgreSQL setup tests.

Run `node scripts/ui/system-scenarios.cjs` in Chromium and WebKit for the
Settings scenario/Advanced settings boundary: no duplicate resource switches,
retained customer-onboarding gate, version propagation, preserved Advanced
drafts across scenario changes, and unrelated saves that omit resource policy.
All four legacy policy combinations survive those saves. Reopen/reload and
1440/390/320px screenshots use synthetic localhost data. `UI_EVIDENCE_DIR`
selects output; `UI_SOURCE_DIR` with `UI_BASELINE=1` captures an archived layout.

Run `node scripts/ui/remote-navigation.cjs` in Chromium and WebKit for the real
console header and scenario-only outbound connection entry. It preserves the
local company selector, checks keyboard navigation/focus return, no-write
cancellation and closing, connected reopen/reload, ordinary-role visibility and
the independent onboarding gate. Synthetic localhost APIs block all writes and
external requests. Both themes at 1440/390/320px use `UI_EVIDENCE_DIR`; archived
`app.js`/`setup-selector.js` via `UI_SOURCE_DIR` and `UI_BASELINE=1` capture before.

Run `node scripts/ui/workspace-authorization.cjs` and repeat with
`UI_BROWSER_ENGINE=webkit` for explicit connection consent, trusted existing
login return, company creation/selection consent, renewal, cancellation,
revocation, uncertain-response recovery, keyboard and both themes at
1440/390/320 px. It uses only localhost fixtures. The real signup/login,
company/grant SQL transactions, delivery failures and isolation run in
`TestWorkspaceSignupCompanyAndInstallationEndToEnd` and the PostgreSQL
`TestWorkspaceAuthorization*` suites on disposable services; no real account,
email, payment or provider service is used.

Run `node scripts/ui/remote-context.cjs` with Chromium and
`UI_BROWSER_ENGINE=webkit` for reviewed destination rejection, retained uncertain
submissions across same-company key/login renewal, cross-context isolation and
explicit legacy history reconciliation. It uses localhost fixtures only, with
both themes and 1440/390/320 px screenshots. Backend regressions additionally
prove one actual TaskRepository admission after key rotation and actual issued-key
revocation before an expired local cancellation proof is cleared.

Run `node scripts/ui/templates.cjs` (also with `UI_BROWSER_ENGINE=webkit`) for
instance Templates: hidden immutable prompts, missing typed slots,
fully fixed templates, exact slot-ID payloads, shared key/reset and late responses,
existing estimate/confirmation/journal transfer, duplicate-click protection,
unavailable models, administrator draft/publication/conflict states, keyboard
actions and both themes at 1440/390/320 px. The localhost fixture blocks all
external requests. Evidence goes to `.local/gw172/ui-<engine>` by default;
`UI_EVIDENCE_DIR` overrides it. Run the affected Playground, response-context,
reference-parser and storage fixtures alongside it.

Run `node scripts/ui/project-journal-state.cjs` in Chromium and WebKit for
metadata-only Journal updates with 30 synthetic company generations and actively
playing native video/audio. Requires `ffmpeg` on PATH to generate disposable media
under `UI_EVIDENCE_DIR`. Assignment, reassignment, removal and rename retain
cards, scroll anchors, focus and playback; natural polling, delayed/repeated
writes, 503 recovery, stale versions/history, filters and permission/key loss
exercise the reconciliation boundaries. Explicit filter changes and cards that
leave the selected project still clear their corresponding context/media. The
fixture uses unchanged application timers and blocks external requests.
`UI_SOURCE_DIR` optionally selects a baseline UI source directory.
Run `node scripts/ui/templates.cjs` with Chromium and `UI_BROWSER_ENGINE=webkit`
for Templates authoring file drops, keyboard file selection, typed validation,
duplicate prevention, pending/cancel/retry and failed-replacement preservation,
fixed binding/filename/preview feedback, prompt markers/IME/caret/scroll geometry,
and heading/action typography at 2048/1440/390/320px in both themes. These fixtures
use synthetic local files, reject external requests, and never contact providers.
`UI_EVIDENCE_DIR` selects logs/screenshots; `UI_SOURCE_DIR` with `UI_BASELINE=1`
captures the approved pre-repair UI for comparison. The existing
`prompt-reference-contracts.cjs` suite checks the shared editor in Playground.

`template-upload-errors.cjs` verifies that the shared API parser and upload card
retain complete long/multiple error reasons, visible line breaks and retry behavior
for template-library and direct-slot uploads in both themes at1440/390/320px.
It uses synthetic local responses and supports `UI_BROWSER_ENGINE` and
`UI_EVIDENCE_DIR`; no provider calls or real user files are used.

Generate disposable clips with `python3 scripts/ui/template-video-fixtures.py
.local/video-workflow/clips`, then run `node scripts/ui/template-video-workflow.cjs`
in Chromium and WebKit. The real console fixture verifies three distinct file
attempts, cancellation, preserved failed replacements, playable previews, exact
draft/publication identities and independent covers. `GATEWAY_TEMPLATE_CLIP_DIR`
selects these files for `TestTemplateThreeVideoIdentityAndPublication` as well;
that API test compares hashes and consumer bytes using actual local storage.
No provider call or user media is involved.

Run `node scripts/ui/template-gallery.cjs` in Chromium and WebKit for the shared
gallery/selection screen, inline authoring permission changes, own-library missing
inputs, exact estimate payloads, editor return, polling, hover/keyboard previews,
blocked-playback retries and reduced motion. Generate the synthetic video fixtures
above first and set `GATEWAY_TEMPLATE_CLIP_DIR` to that directory. Screenshots cover
both themes at 1920/1440/1150/900/390/320px, natural portrait covers and three/two/one
columns. `UI_SOURCE_DIR` and `UI_BASELINE=1` capture the previous gallery layout.
All requests remain on loopback; the fixture submits no generation.

Run `node scripts/ui/kling-connection.cjs` (also with
`UI_BROWSER_ENGINE=webkit`) for explicit connection authentication, cached status
after navigation, permission failures and optional empty price overrides. It also
checks combined connection/verification labels and ignores late responses after
configuration reload or personal/shared scope changes. It
checks keyboard activation, both themes and 1440/390/320 px, blocks external
requests and uses synthetic localhost APIs only. `UI_EVIDENCE_DIR` selects output.

Run `node scripts/ui/playground-project-filter.cjs` in Chromium and WebKit for
All / No project / concrete project membership, old unassigned history beyond the
loaded All page, empty results, natural polling, delayed reads, reload/Back, key
resets and unchanged generation drafts/submission assignment. It measures actual
selector width and full accessible labels for short, 20-character and long
Latin/Cyrillic names in both themes at 1440/390/320px, checks keyboard/Escape and
rejects filter-triggered writes and external calls. Evidence uses
`UI_EVIDENCE_DIR`; `UI_SOURCE_DIR` can select an archived UI. PostgreSQL regressions
separately verify full-set filtering before pagination and workspace/role isolation.

Run the real provider-logo regression with disposable services:

```sh
GATEWAY_LOGO_BROWSER_NODE=node CADDY_BIN=/path/to/pinned/caddy \
  python3 scripts/with-test-services.py -- \
  go test -race -count=1 -timeout=10m -v -run '^TestProviderLogoBrowserPersistence$' ./internal/api
```

Repeat with `UI_BROWSER_ENGINE=webkit`. Node needs Playwright and pngjs on its
module path. `UI_EVIDENCE_DIR` selects screenshots/results. The fixture uses real
synthetic administrator sessions, uploads and PostgreSQL, then kills/restarts its
owned API subprocess against the same database and verifies persisted associations
and unchanged credentials/settings. Both engines check decoded PNG transparency,
validation failures, retries/interruptions, live theme changes, exact rendered RGB
and alpha, keyboard access and 1440/390/320px layouts. With `CADDY_BIN`, it runs the
production proxy configuration and verifies bearer-only image reads. All traffic
is loopback; no provider, payment or identity service is contacted.

The provider-logo fixture also exercises the shared ReferenceUpload picker and
dropzone, multiple-file rejection, round remove action, failure/retry/navigation
locks, removal persistence across the owned API process restart, and re-upload.
It also switches real synthetic owners' keys in API-key mode, using a logo URL
without a generation hint, and verifies that memory cannot expose the first
owner's private logo to the second key.

Run `node scripts/ui/provider-logo-cache.cjs` and repeat with
`UI_BROWSER_ENGINE=webkit` for the production shared picker's cache lifecycle.
The loopback fixture counts actual image requests, blob URLs and decode calls:
two initial providers remain at two requests/decodes across repeated opens,
search rebuilds and content-type filters, with no warm placeholder frames.
It covers concurrent slow responses, request/decode errors and retry, hidden
marks, content A→B→A and delete/re-upload invalidation, delayed account A→B→A
responses, resource eviction, both themes and responsive popup screenshots.
All images are generated fixtures; captures exclude credential fields.
Screenshots are cropped to the logo control and synthetic pixel proof; provider
credential fields are never included. The optional validated-file hook leaves
the existing reference/template upload defaults unchanged.

Run `node scripts/ui/override-sound.cjs` in Chromium and WebKit for Playground
request binding, drag/drop, replacement failures, cancellation, template
save/reload/removal, keyboard actions, both themes and 1440/390/320px layouts.
These use local synthetic uploads; actual media remux and permissions are tested
in the Go fixture suites. `UI_EVIDENCE_DIR` selects screenshot/result output.

The reference and template fixtures also cover the shared `ReferenceLibrary`
component. `references.cjs` checks the Playground multiple-selection adapter;
`templates.cjs` checks administrator-library upload/Play preview/protected deletion,
organization-only slot selection/replacement/deselection and confirmed deletion
versus cancellation/rejection. Run both engines with the existing local fixture
settings. `reference-validation.cjs` verifies slot compatibility and late upload
cancellation; `template-gallery.cjs` retains real hover, keyboard and touch media
coverage. These checks use synthetic files and do not mutate live libraries.

`shared-reference-library.cjs` exercises the common image/audio/video card and
native playback lifecycle directly, including preview retry, keyboard selection,
Delete/Keep, Escape/reopen and both themes at 1440/390/320 px. Generate clips with
`template-video-fixtures.py` first and set `GATEWAY_TEMPLATE_CLIP_DIR` as above.

Run `node scripts/ui/template-editing.cjs` in Chromium and WebKit after generating
the synthetic template video fixtures. It checks compact fixed-media previews,
real video/audio playback, both themes at 1440/390/320px, partial and duplicate
reference positions, reorder, settings preservation, cancelled/repeated handoff,
return to the runner and key-reset cancellation without generation or template
mutation. Backend `TestTemplateEditingPreservesPartialBindingsAndIsolation` checks
published revisions, shared/private access, fixed-slot protection and unchanged
submission state. Evidence stays under `.local/editing/`.

### Shared Template and Playground UI acceptance

Run each fixture with Node/Playwright, then repeat with `UI_BROWSER_ENGINE=webkit`. Set `UI_BROWSER_PATH` when using an installed browser outside Playwright's default cache, and `UI_EVIDENCE_DIR` to a new ignored run directory. All APIs and media are synthetic and local; these checks never authorize live writes or paid providers.

- `duration-labels.cjs`: selected-model bounds/steps in Duration labels and native validation in both editors, themes and relevant responsive breakpoints.
- `prompt-labels.cjs`: `max_prompt_characters`, formatted dynamic Unicode code-point labels, typing/paste/clear/model switches, trimmed validation, unknown capabilities and both editors.
- `template-file-selection.cjs`: canonical-only administrator content/visibility responses, explicit image/video shared covers, internal Remove, no copies/deletes, persistence and publication gating.
- `disclosures.cjs`: native keyboard disclosure semantics, repeated collapse/expand retaining values, trailing shared chevrons, matching heading typography, expanded spacing and separate Override Sound panels.
- `button-alignment.cjs`: primary/secondary glyph geometry in normal, compact, icon, disabled, pending and dialog contexts.
- `interaction-borders.cjs`: one actual border perimeter, custom accent, selected/keyboard/invalid/disabled states, stable outer/content geometry and forced colors.


Inspect the resulting pixels and compare with the design-system spacing gate. Passing geometry assertions does not replace visual acceptance. Keep previous failures alongside successful reruns; report the exact executed browser/state matrix and any unexecuted limits.
