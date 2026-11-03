# Security testing

Read [SECURITY.md](../SECURITY.md) for reporting, support and deployment policy.
Use [the testing guide](testing.md) for isolated services and evidence handling.
Run security checks against a disposable environment or an explicitly authorized
target; do not use production credentials or customer data in fixtures.

## Automated regression areas

| Boundary | Existing coverage |
| --- | --- |
| Tenant, role and key isolation | API/PostgreSQL tests cover ownership, current membership, scope restrictions, expiry and revocation. |
| Browser authentication | Password/token lifecycle, OAuth state/nonce, cross-origin mutations, owner setup and failed logout revocation. |
| Outbound HTTP | Address validation, DNS pinning, redirects, private-network denial and explicit operator exceptions in `internal/httpclient`. |
| Stored credentials | Encryption, masking and re-entry when a provider destination changes. |
| Uploads and active media | Multipart admission, size/type/path checks, sandbox headers, separate direct-storage host and owner checks. |
| Money and retries | Quotas, reservations, accounting, signed payment events and idempotency on fresh databases. |
| Proxy and logs | Cookie-only session gates, forwarded-header forgery denial and access-log redaction through Caddy fixtures. |
| Delivery pipeline | Full-history secret scanning, dependency/image inventory scans and non-root container checks in CI. |

These suites cover encoded scenarios. They are not an independent penetration
test, a production assessment or a guarantee against future vulnerabilities.

The HTTP-method regressions discover all API route declarations and exercise
unsupported methods (including OPTIONS) through the actual router, checking 405
and Allow. Stateful fixtures cover read/write settings, organization management,
payment checkout and projects: GET/HEAD must not invoke domain mutations even
with a valid write-shaped body and a fully authorized key. Separate cases cover
scope/role revocation, foreign organization bindings, OAuth HEAD rejection,
headers-only errors, media HEAD aliases and real HTTP/PostgreSQL state. Read-side
bookkeeping such as catalog metadata synchronization, expired-payment handling
and access/rate accounting is separate from user-requested domain mutations.

## Run checks

Run the full Go/integration gate on disposable services:

```sh
python3 scripts/with-test-services.py --output dist/security/go \
  -- make verify-ci TEST_FLAGS=-count=1
```

Install checksum-pinned scanners and scan source plus history:

```sh
python3 scripts/security/install-tools.py --bin-dir /tmp/gateway-security-tools
/tmp/gateway-security-tools/gitleaks git --config .gitleaks.toml --redact=100 --log-opts=--all .
/tmp/gateway-security-tools/trivy fs --scanners vuln --severity HIGH,CRITICAL \
  --exit-code 1 --skip-dirs .git --skip-dirs .local --skip-dirs dist --skip-files .env .
```

Inspect findings before changing allowlists. Allow only reviewed literal
placeholders or independently reviewed exact non-secret source lines constrained
by detection rule and exact file path. Never suppress whole files, commits,
generic credential prefixes or variable source content. A clean current checkout
does not remove material from Git history. Keep scanner
evidence private and redact credentials from any shared report.

Verify exact-line exceptions against the pinned real scanner and synthetic
credential controls before accepting a scanner configuration change:

```sh
GITLEAKS_BIN=/tmp/gateway-security-tools/gitleaks \
  python3 -m unittest discover -s scripts/tests -p test_gitleaks.py -v
```

The suite checks original detections, exact benign exclusions, adjacent and
same-line credential assignments, changed SQL/field values and another provider
rule. It never uses real credentials. Without the scanner it reports skipped
tests; skips do not satisfy this gate.

For browser/proxy checks, install Playwright and use a verified Caddy binary as
specified in [the UI guide](../scripts/ui/README.md). Existing suites include
`logout-security.cjs`, `origin-security.cjs`, `log-security.cjs`, `separation.cjs`
and `workspaces.cjs`.

The real API/Chromium SVG sandbox check is opt-in. Run it through the disposable
service wrapper with Node/Playwright and `CADDY_BIN` available:

```sh
python3 scripts/with-test-services.py --output dist/security/media \
  -- env GATEWAY_SECURITY_BROWSER_NODE="$(command -v node)" \
  go test -race -count=1 -v -run '^TestStoredSVGIsSandboxed$' ./internal/api
```

Ordinary Go CI verifies media headers. The additional browser/proxy execution
occurs only when the opt-in variable is set. Scan the actual release images and
recheck transport/storage policies after dependency or configuration changes.
Live identity providers, mail/payment delivery, cloud permissions, host firewall,
public TLS and recovery remain separate controlled environment checks.

## Caddy JOSE security pin

The UI build pins go-jose/v3 to the genuine upstream Go pseudo-version
`v3.0.6-0.20261030183946-0e502b87c7aa` while the next tagged v3 release is pending.
[Upstream commit 0e502b8](https://github.com/go-jose/go-jose/commit/0e502b87c7aa651155b11ae8e195ec33ce10230a)
backports the RSA JWK denial-of-service fix for
[CVE-2026-116587](https://github.com/go-jose/go-jose/security/advisories/GHSA-mgcc-cww5-fp2q).
Caddy's standard PKI module reaches this dependency through Smallstep JOSE.
This keeps the v3 API and avoids a broader Smallstep/v4 authentication migration.
The upstream guard rejects RSA JWK moduli larger than 4096 bits and private key
material in public JWK/EPK message headers. Retain those security bounds.

The image's license collector includes the exact module's Apache-2.0 notice and
checksum in `/licenses/modules.json`. Verify that binary/module identity and run
the unchanged HIGH/CRITICAL image scan for every built architecture; a local
patch or a new-looking version string alone is not evidence of remediation.


## Company active sessions and reserved customer adapter

Exercise company-interface lifecycle and customer-handler tests with disposable
PostgreSQL. Migration 090 retains encrypted pending and settled operations across
rollback and re-upgrade. Refresh adoption and logout use exact local epoch,
revision, original grant/family and operation bindings; lost replies must survive
restart without rotating again or revoking a healthy concurrent session.
Terminal cleanup authenticates the installation and refresh proof, including an
exact committed old-version rotation within its fixed recovery window. It never
returns replacement credentials or revokes a successor family.

The reserved central customer adapter composes the original handlers and policy
chain under a one-company ceiling. Browser management uses the verified central
session actor; selected keys are independently checked, then removed before
management dispatch. Browser generation and SDK operations retain the original
key recipient. Test a global administrator with an ordinary company role, a weak
browser actor with a coworker's management key, malformed explicit credentials,
foreign company selectors, stale authority after body creation and a stalled
socket body. Narrow Playground create/restore retains existing issuance and
saved-key restore semantics. Existing direct-central behavior stays unchanged.

This bounded adapter buffers at most 8 MiB, admits only typed implemented
contracts and does not advertise full-company-surface/v1. Local portal/SDK
forwarding, complete route/response URL coverage, streaming media, uploads,
payment returns and all fourteen complete runtime surfaces remain separate
activation requirements. Passing these focused tests is not full mode acceptance.


The reserved local customer facade now has separate actual-handler fixtures for
original central SDK keys absent from local storage, malformed browser signals,
spoofed transport headers, capability gating, lost customer mutations, committed
refresh recovery and late local/global response fences. Company settings, members,
invitations, key usage, projects and slash-bearing models exercise the existing
central handlers and PostgreSQL repositories. Invitation tokens must resolve to
the bound company before preview or acceptance; first-owner provisioning is never
a delegated customer operation. Browser management uses its verified session
actor, while key-backed project/content operations preserve recipient attribution.

These tests call the reserved facade path. Ordinary external `/v1` routing and
complete portal activation remain gated. Complete customer contract and typed URL
coverage, original large-body/streaming budgets, uploads and payment return tests
are still required; the bounded adapter must not silently become their limits.

Valid customer traffic must not consume the authorization-attempt peer budget.
The shared installation IP is not a user or company request-limit identity.
Customer requests retain the existing central operation/key/company limits;
failed customer credential checks use a separate failure counter. Authorization
start/approval/redemption/local-control attempt limits remain unchanged. Exercise
more than 120 valid requests from one installation peer and retain the ordinary
protocol rate-limit regressions.


Template consumer contracts use the existing central published recipes; global
Administrator authoring remains direct-central-only. The actual-handler fixture
creates and publishes centrally, consumes through the reserved company facade,
then edits and unpublishes centrally. Assert original admitted recipe ID,
revision, prompt and fixed-reference snapshot retention, exact idempotent task
identity, current-revision conflicts and absence of local template/task copies.
The disposable fixture explicitly supplies a mock company budget and member
allowance; it does not prove generation from a newly connected unfunded company.

Preserve the central template JSON limit of 16 KiB on both hops and the 150-second
`validate-media` inspection deadline through the local request, fixed client and
central wrapper. Ordinary authorization/control and management requests retain
15 seconds. Test the actual public transport's response-header behavior as well
as propagated contexts; a longer context alone cannot override a shorter HTTP
client or transport timeout. Typed template/media URL rewriting, large response
streaming, direct-storage redirects and complete browser integration remain
separate pending contracts. Small shared-reference GET/HEAD fixture coverage is
not full media transport acceptance.


Generation JSON fixtures exercise the existing central admission handlers for
video, image and audio aliases, exact idempotent replay/conflict behavior, reference
validation/list/deletion and foreign-company isolation. Generation and reference
validation JSON retain 64 KiB; reference inspection retains 150 seconds on both
hops. Multipart generation and direct upload remain separate bridge requirements.
Only the typed single-generation `status_url` and matching `Location` map to the
reserved facade. Bind UUID, object kind and optional company; reject foreign,
encoded or mismatched locations without retry. Arbitrary prompt/parameter JSON
must remain unchanged. HEAD detail remains bodyless without a hidden GET or an
invented representation length; central entity validators do not describe rewritten
local JSON. Lists, artifact/media URLs and complete streaming remain pending.

Compare actual central and reserved-facade handlers with normally issued
`generation` and `generation,organizations:manage` keys. Preserve the central
GET credits versus balance-history distinction, invitation preview versus accept,
GET/HEAD profile versus browser-only PATCH, and organization HEAD scope rules.
Current key issuance requires generation scope; an organizations-only issuance
failure is not evidence of a supported key or a reason to widen issuance policy.
