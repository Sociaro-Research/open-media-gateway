# Template content and API compatibility

A template stores one current definition and an `is_published` visibility flag.
Save replaces that definition. Saving published content validates it fully and
atomically makes it current for subsequent template generations. Saving
unpublished content leaves it unpublished. Invalid or stale saves leave the
previous content and visibility untouched.

Publish and published Save require an explicitly selected, accessible image or
video from the shared Template library as `cover_id`. There is no automatic
fixed-reference fallback. Unpublished content may have no cover. Cover identity
is separate from generation input slots; changing a slot does not change its cover.

## Requests and compatibility

Administrator create/save requests accept `content` or its legacy alias `draft`.
If both are supplied they must decode to the same definition. Responses include
`content` and `is_published`; legacy `draft` contains the same current definition.
The legacy `published` field contains that same definition when visible and is
omitted when unpublished. It never contains an older, separately active snapshot.

Use `PUT /v1/system/templates/{id}` with `version` and the new `content` to save.
Use `version` and `action: publish` or `action: unpublish` to change visibility
without rewriting content. A legacy publish request may still include content;
it is validated and saved atomically. Unpublish ignores content and retains the
stored definition. Soft removal also preserves stored content and media.
Only system Administrators may write these endpoints; existing authentication,
generation admission, ownership and budget checks remain in force.

The numeric `version` and `revision` fields remain opaque compatibility tokens:

- `version` prevents one editor from overwriting another editor's save or toggle.
  Send the latest token for every write; conflicts return HTTP 409.
- `revision` binds generation, estimate, input/file validation and Advanced editing
  to the content the caller reviewed. Publish and published Save advance it.
  Requests with stale tokens return HTTP 409, requiring a reload and review of
  selected media/model settings. Saving unpublished content cannot enable use.

These tokens do not provide a template history or rollback API. Already admitted
jobs keep their own immutable resolved prompt, options and template snapshot.
Workers use those task values and do not reload a changed template. Task retention
and request-log minimization continue to follow their existing policies.

## Schema transition

Migration 081 introduces canonical `content` and `is_published` columns. For a
previously published record it chooses the previously visible published definition
as current, avoiding automatic activation of an unvalidated divergent draft. An
unpublished record keeps its saved draft as current. Existing explicit covers,
reference identities, sound settings and task snapshots are preserved.

The migration first retains the complete old draft, publication and tokens in
an inert `legacy_state` JSON column. It is recovery metadata only: runtime queries,
API responses, visibility toggles and active reference bindings never read it.
No template media is deleted by this transition. A divergent old editor's
`version` changes so that it must reload; the public `revision` remains unchanged
because the visible definition has not changed. Matching definitions retain their
edit token.

Migration Down restores the old column layout with both aliases set from the
current definition, rather than reactivating an obsolete publication. It retains
the inert backup. A separate `recovery_states` JSON array preserves distinct
current states before Down and distinct old-schema states before Up. This retains
new drafts written by an old application during a rollback window, even when the
original `legacy_state` already exists. Exact duplicate states are not appended;
unchanged repeated transitions do not grow the archive. Earlier recovery evidence
is never overwritten. Runtime/API code never reads either recovery column, and
re-upgrade still chooses the previously visible content rather than activating an
unvalidated old draft. These are inert recovery records, not a template history API.

Prepare the pinned Goose migration utility for the target architecture, verify
backups and the exact active version before using `down-to 80`, and stop all
application writers throughout the transition. Verify schema 80 before starting
old binaries; image-only rollback is incompatible. Use Goose for migration
bookkeeping and protect database connection diagnostics from credential logging.
The improved 081 is an unreleased migration intended for installations still on
schema 80; deployments already at 81 require a separately reviewed compatibility
plan rather than assuming an edited Up migration will run again. Its Down also
initializes missing recovery storage before preserving current data.

Database schema and application binaries must transition together; HTTP compatibility does
not make old and new binaries safe to run simultaneously against one schema.
Prepare backups and a coordinated application/schema rollback before deployment.
The migration itself is tested with disposable data; checking it into source does
not authorize running it against a live installation.
