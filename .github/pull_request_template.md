## What changed

<!-- Explain the user-visible behavior and why this change is needed. -->

## How it was tested

- [ ] `git diff --check` and `make release-check`
- [ ] Relevant regressions; for Go/integration changes, `python3 scripts/with-test-services.py -- make verify-ci TEST_FLAGS=-count=1`
- [ ] Skipped or unexecuted checks and their reasons are listed
- [ ] Local end-to-end flow with the `mock` provider, when relevant
- [ ] For every UI feature/fix: read [the design system](../docs/design-system.md) before implementation and compare rendered output with its [spacing and visual review gate](../docs/design-system.md#spacing-and-visual-review-gate); reuse existing spacing tokens/patterns and inspect component-to-component gaps and alignment
- [ ] UI before/after screenshots and adjacent affected screens inspected in both themes at desktop/mobile widths, with keyboard and applicable dirty/saved, disabled, pending/error and interrupted/repeated states; concrete design comparisons and limits recorded in ignored evidence (tests alone are insufficient)

## Impact checklist

- [ ] Tests cover the changed behavior
- [ ] Public API or OpenAPI changed
- [ ] Configuration or secrets changed
- [ ] Database migration added
- [ ] Provider behavior, pricing, or billable calls changed
- [ ] Documentation updated

## Security and privacy

<!-- Confirm that logs, fixtures, screenshots, and examples contain no real credentials, prompts, customer data, or provider result URLs. -->
