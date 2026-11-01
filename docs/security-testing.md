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
