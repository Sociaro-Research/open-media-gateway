# Configurable provider connections

Administrators can create independent connections using **Providers → Add
provider** or **Administration → Shared providers → Add provider**. Select the
API adapter, name the connection, then configure credentials and addresses.
Multiple connections can use the same API type. Each has an immutable generated
ID; editing its name does not change model identities or historical tasks.
Existing built-in connections and public model IDs remain unchanged.

## Connection settings

- **API endpoint** is the adapter's base URL. Existing Alibaba music credentials
  and music endpoint remain independent fields.
- **Request timeout (seconds)** sets this connection's API-response budget. It
  inherits 30 seconds by default (or `PROVIDER_HTTP_TIMEOUT` if configured). A
  generation can override it with top-level `timeout_seconds`, for example 600.
  It does not change the asynchronous rendering lifetime.
- **Use manual catalog only** skips remote discovery. New connections start in
  this mode. Saving settings never requires a successful `/models` request and
  never performs paid generation.
- **Advanced API settings** can override model discovery, generation and task
  status URLs. Use a full HTTP(S) URL or an absolute path. A task status URL can
  contain `{task_id}`. A supplied query string overrides the original query;
  otherwise the original pagination query is retained. Empty values use adapter
  defaults. These overrides preserve the adapter's request/response format.
- **Additional headers** apply to all API requests or to one selected operation.
  Operation-specific values override connection-wide values, including an
  explicitly configured Authorization header. Transport framing and Content-Type
  remain adapter-controlled. Values are encrypted and returned only as masks.
  Leaving an existing masked value untouched preserves it; removing a row deletes
  it. Renaming a header requires supplying its value again.
- **Additional request body** accepts a JSON object. It recursively overrides
  generated optional fields. Per-model overrides take precedence; declared
  generation parameters then override those defaults. Model IDs and top-level
  prompt/input/message/content/image fields cannot be overridden this way.
- API redirects are rejected. Configure the final API URL. Credentials and custom
  headers are not attached to result downloads, which use the storage client.

Creation and advanced configuration require an Administrator. Existing personal
credential management still follows the personal-provider policy and key scopes.
The API supports GET `/v1/{admin|system}/providers/types`, POST
`/v1/{admin|system}/providers` (`api_type`, `name`), and PUT on the connection ID.
Send the current `version` when changing its name, archive flag or `connection`
object. The nested connection object replaces the saved object; omitted top-level
fields preserve existing values. Concurrent stale writes are rejected.

## Manual models

Choose **Add model**, enter the exact upstream ID and display name, and select
an **API request profile**. A profile reuses a supported model's wire format,
including its serializer, response parser and synchronous/asynchronous behavior.
The actual outgoing model field is replaced with the manually configured ID;
it need not appear in any public catalog or have a recognized name prefix.

Profile selection copies its capability defaults. **Model capabilities** allows
an administrator to describe supported resolutions, duration, references, voices,
formats and additional typed `parameters`. These fields drive the existing
Playground controls and server validation. The new model never inherits reviewed
metadata or a tariff from the profile. A different wire protocol still requires
an adapter change. The shipped OpenAI adapter covers its existing media APIs;
this change does not add general chat or every OpenAI endpoint.

**Model request body** contains administrator overrides. Typed user parameters
are placed under `parameters` for DashScope and at the top level for OpenAI and
ModelArk. An existing manual model can be edited or copied. Apply changes in the
dialog, then save the provider. The existing **Media models** table controls the
public API name and default access. Organization grants continue to apply.
Discovery refreshes preserve manual models; a manual entry with the same upstream
ID overrides the discovered descriptor within that connection only.

**Price per request** is explicit, in USD. A missing price remains unknown and
blocks budgeted generation. Zero is an explicit administrator choice, not a
fallback. A per-request price is a configured estimate, not a provider invoice;
organization markup is applied normally. Custom generation URLs or connection
body overrides also require a manual model with an explicit tariff, rather than
assuming that the ordinary public tariff applies to a modified request.

## Accepted tasks and history

The gateway and worker resolve connections from the owner-scoped database without
restart. An accepted task pins an encrypted configuration revision. Submission,
polling and historical billing retrieval use that revision after edits or archive,
including the original model profile, key, URLs and headers. Disabling or archiving
stops new work and retains history. Settings revisions have no automatic expiry
because accepted tasks and later billing reconciliation may still need them.

Accounting, model grants and reports distinguish connection IDs. Per-request
manual tariffs are metered separately and are not guessed from image/video usage
window invoices. Existing inference fingerprints are retained until a legacy
connection adopts advanced configuration. Migration 047 adds connection names,
archive state, optimistic versions and encrypted revisions; rollback requires a
backup. Automatic balancing and cross-connection failover are outside this change.

## Validation

The connection regression tests exercise multiple connections, owner isolation,
non-public IDs, independent model discovery, exact request rewriting, header
preservation/redaction, explicit pricing, archive and polling after settings
changes and manager restart. PostgreSQL tests exercise migrations, version
conflicts and isolated encrypted revisions. Browser fixtures cover creation,
manual model editing, secret preservation, model selection in dialogs, keyboard
operation and desktop/390/320 px layouts. They never call a paid provider.

Run `scripts/ui/provider-connections.cjs` with the environment described in
[UI checks](../scripts/ui/README.md), and `make verify-ci` with isolated PostgreSQL
and Redis. Live access to customer-only endpoints still requires their actual
credentials and an explicitly initiated generation.
