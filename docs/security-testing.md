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
placeholders; do not suppress whole files or generic credential prefixes. A
clean current checkout does not remove material from Git history. Keep scanner
evidence private and redact credentials from any shared report.

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
