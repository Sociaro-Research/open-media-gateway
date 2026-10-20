# Audit Log and historical identity

## Access and interface

**Administration → Audit Log** shows all management history to current
Administrators. **Organization → Audit Log** shows that organization's history
to Supermanagers and Managers. Other organization roles and Client managers
have no management audit access. A generation key never grants audit access;
management keys also need the relevant scope. Organization-bound keys cannot
read global audit or another organization's history.

The shared component has **Events** and **Revoked keys** tabs, date/actor/object
filters, bounded pagination and keyboard-accessible details. Events additionally
filter organization, scope, action, object type, result and correlation ID.
Dates cover complete days in the viewer's reporting timezone. The default is
the current month. Details show recorded identities and Field / Before / After
changes without depending on a live object's edit page. Manager responses hide
internal markups, request limits, model grants, Client manager assignments and
payment reconciliation details, including in list responses.

## Recorded operations

| Area | History |
| --- | --- |
| Installation | System settings, markups, request limits, branding, email delivery, login methods, payment providers and notifications. |
| People | Account creation, profile changes, global roles and completed password setup/reset as a fact. |
| Organizations | Creation, settings, logging policy, membership/limits, invitations, model grants, Client manager assignments, storage, credits and payment reconciliation. |
| Providers/models | Connection creation/configuration/availability/archive and public model names/default availability. Encrypted connection/storage configuration records a change indicator, not its contents. |
| Keys | Creation and explicit or cascaded revocation, identifying recipient and issuer separately. |

Successful mutations and their events commit in one database transaction. If
audit insertion fails, the mutation rolls back. Unchanged projected values do
not create events. Authenticated failed/denied management requests record only
the route template, HTTP status, actor/key and correlation ID after rollback;
they do not retain submitted values or raw error text. Invalid authentication,
ordinary generation traffic and completed sign-ins are not management events.
Server commands get transaction correlation IDs; one HTTP operation shares its
server-assigned correlation ID across affected objects.

Audit uses per-object safe-field projections and recursive redaction. No
credential values, ciphertext, hashes, prompts, generation content or arbitrary
provider responses are retained. Credential changes record added/replaced/cleared
indicators. Request-log minimization does not remove necessary management facts.
Automatic changes identify a service actor; reconciliation may also identify
the initiating user. A bearer key identifies its assigned account, not the
physical person holding the secret.

## Revoked keys and request snapshots

Revocation is irreversible. Its immutable snapshot contains safe key settings,
IDs and email labels for recipient/issuer/revoker, timestamps, reason,
correlation ID, known gross spend, pending requests and unknown-cost counts.
Current lifetime spending is separate: it may increase when previously accepted
requests finish or costs reconcile. No secret restoration or individual key
budget is introduced; existing organization/member accounting remains in effect.

New requests capture immutable user, organization, key and provider labels at
acceptance. Reports, request details and CSV use these labels after renaming or
revocation; aggregation and authorization still use stable IDs and current
permissions. Existing public model snapshots remain unchanged. JSON exposes
`identity_historical`; CSV exposes **Identity source**. Older requests use an
explicit current-label fallback, not an invented historical identity.

## Migration and limits

Migration **051** imports the former system/organization journals in chronological
order, preserving timestamps, safe details and source IDs, then removes their
writers, reader and tables. Imported revoked keys retain known facts; missing
revokers and spend-at-revocation remain unknown. No existing history is purged.
Restore a pre-migration backup to roll back; no destructive down migration exists.

Database triggers reject UPDATE, DELETE and TRUNCATE of event/revocation history.
Privileged database maintenance can bypass them: this is operational audit, not
a tamper-proof compliance archive. Full database time travel, deleted teams,
secret restoration, key-budget lifecycle and a global read-only role remain
outside this feature. Encrypted provider configuration has change facts rather
than a full readable revision diff.

## API

- `GET /v1/audit/events` and `/events/{event}`: Administrator with `system:manage`.
- `GET /v1/audit/revoked-keys`: same global policy.
- Equivalent `/v1/organizations/{org}/audit/...` reads: current Manager,
  Supermanager or Administrator with `organizations:manage` when using a key.
- Events use `before_id`; revoked keys use `offset`. Both return at most 50 rows.
- Event creation is internal; no edit/delete/restore endpoint exists.

See [OpenAPI](openapi.yaml) for filters and response schemas.
