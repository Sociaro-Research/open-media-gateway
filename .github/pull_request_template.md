## What changed

<!-- Explain the user-visible behavior and why this change is needed. -->

## How it was tested

- [ ] `git diff --check` and `make release-check`
- [ ] Relevant regressions; for Go/integration changes, `python3 scripts/with-test-services.py -- make verify-ci TEST_FLAGS=-count=1`
- [ ] Skipped or unexecuted checks and their reasons are listed
- [ ] Local end-to-end flow with the `mock` provider, when relevant

## Impact checklist

- [ ] Tests cover the changed behavior
- [ ] Public API or OpenAPI changed
- [ ] Configuration or secrets changed
- [ ] Database migration added
- [ ] Provider behavior, pricing, or billable calls changed
- [ ] Documentation updated

## Security and privacy

<!-- Confirm that logs, fixtures, screenshots, and examples contain no real credentials, prompts, customer data, or provider result URLs. -->
