# Testing

Use the automated suites to verify a specific checkout. A command's presence or
an earlier run is not evidence that the current release passed. CI results belong
to their exact commit; provider credentials and deployed state are separate.

## Required development checks

Run full Go CI with fresh local PostgreSQL and Redis:

```sh
python3 scripts/with-test-services.py --output dist/checks/go \
  -- make verify-ci TEST_FLAGS=-count=1
```

Install the Go version from `go.mod`, PostgreSQL tools and Redis first. Set
`POSTGRES_BIN=/path/to/postgresql/bin` if PostgreSQL is not on PATH. The wrapper
creates disposable services on loopback, replaces inherited test URLs, records
versions/source identity and removes service data afterward. It never connects
to existing application services. `make verify-ci` includes module/format checks,
vet, vulnerability checks, race/integration tests and binary builds.

Plain `make verify` skips database/Redis integration tests without their test
URLs. Do not report those skipped checks as passing.

Run release-tool checks and build the independent UI:

```sh
python3 -m unittest discover -s scripts/tests -p 'test_release*.py' -v
python3 -m unittest discover -s scripts/tests -p 'test_stop_worker_release.py' -v
make build-ui
git diff --check
make release-check
```

The release preflight checks publishable file contents and paths. It does not
replace a complete secret-history scan or prove that a deployment is healthy.

## Choose checks for the change

| Change | Additional checks |
| --- | --- |
| API, authentication, database or worker | The affected Go packages with race detection and disposable services; update OpenAPI for contract changes. |
| UI or shared styles | Existing browser fixtures from [the UI guide](../scripts/ui/README.md); check Chromium/WebKit, keyboard, both themes and 1440/390/320 px layouts where applicable. |
| Installation or images | `scripts/verify-release.py` with four explicit test images; local/HTTPS owner setup, private services, mock download and restart persistence. |
| Outbound transport, sessions, uploads or permissions | [Security regressions](security-testing.md) plus affected API/PostgreSQL/browser suites. |
| Recovery, queueing, storage pressure or throughput | [Reliability and performance runbook](performance-testing.md); use its opt-in flags and isolated environments. |
| Provider protocol or pricing | Adapter HTTP fixtures and catalogue regression data; paid/live calls require explicit credentials and budget. |
| Documentation | Check relative links, whitespace, installation examples and release preflight. |

The [local launcher](../tools/release/README.md) runs native checks or full local
CI without committing or pushing. Full local CI includes security scans, container
installation and browser variants; native-only mode reports these container and
scanner checks as unexecuted. Hosted workflows are manual/reusable; pushes, PRs
and version tags no longer trigger them automatically.

The optional CI workflows provision isolated databases, build/scan images and run their
browser suites. The versioned release workflow also checks source-free installs
on native AMD64 and ARM64 runners. A local subset does not establish full hosted
CI or cross-architecture results.

## Evidence and verification boundaries

Keep run logs, screenshots, manifests, scanner output and investigation notes in
ignored `dist/` or `.local/`, or attach them as CI artifacts. Record the commit,
working-tree/source identity, command, service/tool versions, result and limits.
Retain failed runs separately from successful reruns. Do not commit raw evidence,
customer data, temporary result URLs or credentials as project documentation.

Mock generation is free and deterministic. It does not verify live paid
providers, email delivery, payment settlement, real cloud storage permissions or
browser/device combinations outside the exercised fixtures. Private-CA HTTPS
checks verify routing and certificate handling; public ACME/DNS need a separate
controlled environment. Capacity, soak and recovery results are machine- and
workload-specific. Rerun the relevant scenario after a material change rather
than treating historical numbers as a universal guarantee.
