# Model catalogue

The compiled catalogue in `internal/modelcatalog/catalog.json` contains metadata,
capabilities, published regional prices and source links for integrated media
models. Runtime discovery and access policy determine which models a connection
can use. The compiled catalogue is a fallback and reviewed baseline, not an
inventory of every language model offered by a vendor.

## Sources and date semantics

- OpenAI: individual model pages, the developer changelog, release announcements,
  deprecations, standard pricing and the image generation guide/calculator.
- Alibaba: model-pricing, newly-released-models and image-model documentation.
- BytePlus: model catalogue 1330310, pricing 1544106, release history 1159178
  and 2172655, and the public model overview.

Every entry records its sources and review date. Exact release dates, release
months, dated model versions, current alias targets and API object creation
timestamps remain separate. Sorting uses the latest known release/version date,
descending within the provider, with unknown dates last and stable name ties.
API object creation is displayed in the viewer's timezone, but never used as a
fabricated public launch date. Date-only announcements retain their published
calendar date. Some BytePlus releases have only month precision.

Model API responses supplement the reviewed data with OpenAI/BytePlus creation,
ownership and shutdown fields, and Alibaba capabilities and modalities. These
account-dependent responses use the adapters' existing ten-minute cache and are
not written into the shared public JSON. Unknown future IDs never inherit a
neighbouring model's reviewed metadata or tariff.

## Price presentation

The model list and generation estimate use the same provider quote implementation.
Catalogue examples use 100 ASCII prompt characters for images/video, 1,000 for
speech, the first supported size/quality, default duration, 16:9 where supported,
and the minimum required image references. No generation is submitted to calculate
these examples. Provider credentials are resolved once per catalogue.

The model list, tooltip and generation confirmation show gateway prices with
organization markup. The tooltip explains the example inputs and gateway controls.
Only system administrators receive provider unit tariffs, pricing calculation,
regional tariff facts and promotions. Raw provider accounting stays internal;
customer estimates and final charges retain their distinct meanings.

Pricing rules:

- GPT Image 2.5 uses 439 estimated output tokens at 1024 square/medium versus
  1,756 for GPT Image 2; both use $30 per million output image tokens. Their
  different prices are expected. Reference estimates now include the same
  explicit text input estimate as the generation quote.
- GPT Image 1 Mini's published size/quality price table differs from applying the
  generic legacy image token table. Planning uses Mini's own published output
  prices; metering still uses returned usage at $8 per million output tokens.
- BytePlus references now use the actual quote's dimensions and Seedance 1.5
  endpoint-frame adjustment. Seedance 2.5 and Dola Seedream 5.0 Pro have verified
  standard rates. Pro image references after the first incur $0.003 each.
- Seedream 5.0's non-Lite ID is explicitly linked to the Lite ID in the official
  model catalogue. Seedance 1.0 Lite video IDs have no confirmed current public
  tariff and are not assigned a guessed price.
- Reviewed Alibaba image and simple video tariffs include Beijing editing rates. Seven-decimal per-second rates are stored per 1,000 seconds to
  preserve precision with the money type.
- Video-reference tariffs/floors cannot use the no-video-input estimate.
  Both singular and array input forms now reject that incorrect substitution.
- BytePlus public estimates are restricted to the reviewed Singapore endpoint.
  Other endpoints, unknown IDs and unreviewed input configurations remain
  explicitly unpriced, even when informational vendor rate tables are available.
- Public temporary BytePlus and Alibaba promotions are described in the
  catalogue, but not silently applied to standard rates or frozen prior tasks.
  Eligibility, expiration, input-video floors, variable generated music duration
  and other unreviewed configurations need explicit pricing rules before they can
  produce an enforceable estimate.

Missing estimates never mean free. Provider capabilities, public limits and
available gateway controls are labelled separately; published rate limits are
not promises about an account's quota.

## Runtime synchronization

Shared providers now supplement the compiled baseline with a versioned daily
catalogue. Configure the common timezone/report email in system settings and each
provider's local check time/sources in its connection. The journal retains quiet
checks and unresolved warnings; safe metadata and compatible tariffs update
without rebuilding the gateway. See [catalogue synchronization](catalog-sync.md)
for source parsers, pricing boundaries, email behavior and production diagnosis.
The reviewed JSON remains the bootstrap and code-review baseline.

## Updating the compiled catalogue

`scripts/catalog/build_metadata.py <download-directory>` rebuilds the public JSON
from reviewed official documentation downloads. Required inputs:

- `openai-docs.json`: objects containing `id` and model-page Markdown `text`;
  `openai-changelog.md`.
- `alibaba-pricing.html`, `alibaba-images.html`, `alibaba-releases.html`.
- `byte-content.txt` (pricing Markdown), `byte-catalog.md`,
  `byte-releases-1159178.md`, `byte-releases-2172655.md`.

BytePlus's server-rendered page embeds its document Markdown in
`window._ROUTER_DATA.loaderData.*.curDoc.MDContent`. Review regenerated changes,
update the review date and verify exact IDs, table row spans, region headings,
alias mappings and release semantics. Never infer a newly discovered model's
tariff from a matching name prefix. Tariff code changes require separate
regression checks against provider pricing.

## Validation

The catalogue tests validate coverage, date precision, provenance and isolation
of per-account API metadata. Price regressions cover OpenAI's output differences,
Mini planning versus metering, regional tariffs, sub-microdollar unit precision,
and both video-reference input forms. The model-information browser check covers
grouping/order, hover, keyboard, touch, narrow and resized viewports, safe text
rendering, link protocols, closing and selection without accidental generation.
All browser/API responses in these checks are fixtures.

## Customer price visibility

Catalogue reference prices include the current organization markup. Customer HTTP responses and webhooks expose gateway amounts only. Provider tariff tables, pricing sources, promotions, raw usage payloads and markup rates are private to system administrators. Organization administrators and client managers receive the same customer price projection as members. Historical tasks retain their accepted markup. CSV exports contain customer charges only unless requested by a system administrator.
