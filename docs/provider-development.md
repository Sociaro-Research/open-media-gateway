# Adding or extending a provider

Media Gateway treats every provider as a catalog of media capabilities, not as a
single video endpoint. An adapter should make all supported image, video, speech,
audio, and music models discoverable through the common API.

## Adapter contract

Implement `core.MediaProvider`:

- `Name()` returns a stable lowercase provider ID;
- `Generate()` translates a validated provider-neutral request and returns a
  provider task ID, immediate result URL, and usage when available;
- `GetTask()` maps vendor async states to `queued`, `running`, `succeeded`, or
  `failed`, and returns final usage/result metadata.

Implement `core.ModelCatalog` so the UI and validation layer know the available
models and their capabilities. Implement a credential check when the provider
has a cheaper or safer endpoint than creating a generation.

## Capability rules

Each catalog entry declares one `media_type` and the exact supported inputs and
options: resolutions, ratios, durations, image/audio/video references, voice,
lyrics, formats, and relevant controls. Do not advertise an option the adapter
silently ignores. Do not hard-code a video-only route for a provider that also
offers other media types.

Prefer runtime model discovery when the vendor exposes a reliable catalog. Keep
a conservative built-in fallback when availability is required, and document
which source wins and how long it is cached.

## Credentials and regions

- Keep vendor credentials out of core types, logs, errors, fixtures, and docs.
- Store supported credentials in the encrypted provider record.
- Validate base URL scheme and expected provider host or workspace pattern.
- Treat regional endpoints, keys, catalogs, and model availability as one unit.
- Use separate least-privilege credentials for billing or account metadata.

## Usage and cost

Preserve the raw provider usage payload and normalize billable counters that are
actually returned. A monetary value derived from public list prices must remain
marked as an estimate. Exact invoiced cost requires later reconciliation with a
billing-detail API because discounts, quotas, resource packs, and rounding may
apply.

Document which models return usage, the unit, when it becomes final, pricing
source/date, bill detail API, and the minimal billing permission.

## Result handling

Provider URLs are temporary and must not be returned as the durable public
result. The worker archives supported image, video, or audio content first.
Validate status, content type, and size. Production deployments should also
constrain outbound destinations and redirects.

## Integration checklist

1. Add the adapter under `plugins/provider/<id>` with unit tests for every media
   path, state mapping, malformed response, provider error, and usage payload.
2. Expose its `Definition()` and register it in `plugins/provider/registry.go`;
   declare credential fields in the module schema.
3. Add environment bootstrap fields only when local setup benefits from them.
4. Expose every supported model/capability in the catalog.
5. Use the shared metadata-driven UI controls without exposing stored secrets.
6. Update `README.md`, configuration docs and OpenAPI where public behavior changes;
   add environment aliases only when required.
7. Test through the canonical endpoint for the media type (see [OpenAI-style API](openai-api.md)), the generic `POST /v1/generations` endpoint, polling, archival, signed media access, and
   webhook delivery using a non-production provider account.
8. Run `make verify` and document any paid calls used in manual testing.

## Shared extension points

Adding a directory does not register an adapter. Use the explicit module registry
and the [provider module guide](provider-modules.md); normal integrations do not
add provider-specific branches to the core, manager or browser.

Adapters can implement `core.CostQuoter` to own their tariffs; the managed
wrapper supplies the credential fingerprint. Multi-component pricing snapshots
use normalized metric names and require complete usage before metering. An
optional `PreferInlineImages() bool` method opts into the existing reference
resolver, without another provider-ID branch for each new adapter. Model
`qualities` and `backgrounds` feed common API validation and UI controls.

## Catalogue synchronization registration

Every paid provider module must register its reviewed official model, pricing,
release and protocol documentation in `Definition.CatalogSources`. Choose an
explicit parser for known structured formats; use `document` when changes need
human/code review. Implement `DiscoverModelIDs` separately from executable model
filtering, so unknown media IDs can produce warnings without inheriting another
model's serializer or price. Keep this read-only and authenticated only for the
provider's own model/service API. Public documentation fetches use the independent
public-network client and no provider credentials.

Add parser fixtures for dates, price units, regional scopes and unsupported
reference/billing changes. The paid-module regression requires valid registered
sources. `Definition.CatalogRegions` may declare reviewed endpoint contexts for
pure local price validation; it must never turn a source URL into a generation
endpoint. Existing administrators' source lists are preserved on code upgrades;
document any source additions they need to apply. See [catalogue synchronization](catalog-sync.md).
