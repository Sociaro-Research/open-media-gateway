# Agent instructions

Write documentation and contributor instructions in English. Keep `README.md`
as the canonical entry point. Follow the user's scope; editing the project does
not authorize a commit, push, release, deployment or change to external services.

## Project boundaries

- Keep the public API and core contracts provider-neutral. Provider-specific
  behavior belongs in `plugins/provider/<id>` and the explicit registry.
- Preserve applied PostgreSQL migrations. Add a new migration for schema changes.
- Keep credentials, generated media, account data and local environment files
  outside version control. Use `.env.example` for documented placeholders.
- Store temporary reports, review notes, plans and raw test evidence under ignored
  `.local/` or `dist/`. Preserve earlier evidence locally; keep public documentation
  focused on supported behavior and reproducible instructions.

## Tests

Read [the testing guide](docs/testing.md) before choosing checks, and
[performance and reliability testing](docs/performance-testing.md) before running
those campaigns. Reuse existing suites and distinguish executed, skipped and
unexecuted checks. Never use production services for tests.

Before publishing code, run `git diff --check`, `make release-check` and checks
relevant to the change. Run `make verify-ci` with disposable PostgreSQL and Redis
for full Go CI; it includes `make verify`. Documentation-only changes need the
whitespace and release checks. Store dated run evidence outside the public tree.

## UI changes

Follow [the design system](docs/design-system.md). Reuse shared fields, dropdowns,
calendars, model pickers, saves, switches and scrollbars. Fix shared components
rather than adding local copies. Explain justified exceptions and verify desktop,
mobile and keyboard behavior.

## Version control and releases

- Inspect staged, unstaged and untracked changes; preserve unrelated work.
- After a completed task, provide one short, copy-ready commit message covering
  completed work still uncommitted. Exclude committed or unfinished work.
- When a commit/push is requested, fetch `origin`, integrate upstream changes and
  push to `main` without force. Confirm the remote SHA and working-tree state.
- Publishing versioned images and an installation bundle is separate from
  deploying an application. Follow [the release guide](docs/ghcr-release.md).
- Deploy only when explicitly authorized. Follow [the deployment guide](docs/deployment.md)
  and preserve backups, configuration, named volumes and clean worker-drain gates.
  Never run `docker compose down -v` against an existing installation.
- Keep credentials out of command output. Use a descriptive User-Agent for
  scripted public HTTP checks.
