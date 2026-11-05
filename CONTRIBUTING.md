# Contributing to Media Gateway

Thank you for helping improve Media Gateway. Small fixes, documentation, tests,
and complete media-provider adapters are all welcome.

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
Security vulnerabilities must be reported privately according to
[SECURITY.md](SECURITY.md), not in a public issue.

## Before you start

For a bug, search existing issues and include a minimal reproduction. For a
feature or provider, open an issue first when the change affects the public API,
database schema, security model, or introduces a major dependency. This prevents
contributors from investing in a direction the maintainers cannot merge.

## Local setup

For the console and free mock provider, follow the canonical
[source setup guide](docs/development.md#source-setup). It includes Docker Compose's
`ui` profile, first-owner setup, independent secrets and local browser origins.
For an API-only development environment, use [headless operation](docs/frontend-separation.md).

Native Go checks additionally require the Go version declared in `go.mod`,
Python 3.9+, PostgreSQL tools and Redis on PATH (or `POSTGRES_BIN` for PostgreSQL).
Browser suites require Node, Playwright and their Chromium/WebKit engines.
Docker is needed for container and installation checks. See
[the testing guide](docs/testing.md) for the exact scope of each check.

No paid provider key is needed: use the `mock` adapter for the complete local
flow. Never commit `.env`, credentials, generated media, provider responses, or
customer prompts.

Run the full Go gate with disposable services before opening a pull request,
then add the browser or other checks relevant to your change:

```bash
python3 scripts/with-test-services.py -- make verify-ci TEST_FLAGS=-count=1
```

See [the testing guide](docs/testing.md) for prerequisites, browser checks and
evidence handling. Keep raw reports, screenshots and investigation notes under
ignored `dist/` or `.local/`, or CI artifacts.

## Change guidelines

- Write all documentation, contributor instructions, and product interface text
  in English, including provider statuses and errors. `README.md` is the canonical
  entry point; do not maintain separate translated READMEs.
- Keep the public API and core types provider-neutral.
- Put vendor-specific request/response translation in `plugins/provider/<id>`.
- A media provider should expose every supported media model and capability,
  rather than implementing a video-only special case.
- For every UI feature or fix, read [the design system](docs/design-system.md)
  before implementation and compare the rendered result with its
  [spacing and visual review gate](docs/design-system.md#spacing-and-visual-review-gate)
  before completion. Use existing spacing tokens and shared patterns; inspect
  component-to-component spacing and alignment in before/after screenshots,
  adjacent affected screens, both themes, desktop/mobile and keyboard states.
  Tests alone do not establish visual acceptance. Record comparisons and limits
  in ignored local evidence.
- Add or update tests for behavior changes.
- Add a PostgreSQL migration for schema changes; never edit an applied migration.
- Update `docs/openapi.yaml`, `README.md`, and configuration docs when a public
  endpoint, field, or environment variable changes.
- Do not log credentials, authorization headers, provider response URLs, prompts,
  lyrics, or generated media.
- Prefer least-privilege, read-only credentials for billing integrations.

See [docs/provider-development.md](docs/provider-development.md) for the adapter
checklist.

## User-facing errors

Every error shown to a user or agent must provide the safest verified explanation
and a concrete next step. Apply this rule across API, UI, worker/Journal, CLI and
historical read paths. Review diagnostic changes against this checklist:

- [ ] Identify the verified cause and known field, type, range, choice or limit
  that helps correct the request. Do not guess causes or invent universal limits.
- [ ] Preserve reviewed public field names in `param` (for example, `speed`).
  For arbitrary/unrecognized names, including secret-like names, use a reviewed
  enclosing field such as `body` instead of reflecting the name; never reflect
  submitted values or private implementation details.
- [ ] Explain what the caller should correct or what an operator should check.
  Use task/correlation identifiers only when already available and authorized;
  preserve opaque authentication, permission and hidden-resource responses.
- [ ] If acceptance or completion is uncertain, say so. Direct the caller to the
  original task's status, Journal or provider history before a new submission.
  Describe only supported recovery using the original idempotency key, endpoint,
  body and account/workspace context. Avoid unconditional retry instructions or
  promises about recovery, charges or duplicate prevention.
- [ ] Preserve existing HTTP status, error code/type/envelope, permissions and
  lifecycle for diagnostic-only changes. Reconstruct safe errors from bounded,
  trusted evidence; never expose raw upstream bodies/messages, credentials,
  signed URLs, local paths or another user's data.
- [ ] Add synthetic negative and compatibility regressions at the affected public
  boundaries, including saved-error reads where relevant: useful safe fields and
  next steps survive, unsafe input stays private, and existing contracts hold.

## Pull requests

Keep pull requests focused. Explain the user-visible change, its risks, how it
was tested, and whether it changes configuration, migrations, API behavior, or
provider cost. Draft pull requests are welcome for early design feedback.

All commits and contributions are accepted under the repository's
[Apache License 2.0](LICENSE). No separate contributor license agreement is
currently required.

## Provider integrations

Read [Provider modules](docs/provider-modules.md) for the contract, composition,
configuration, model controls, tests and removal procedure. Normal integrations
change one module folder and the composition list; vendor behavior belongs in
the module, not in core, the manager or the UI.
