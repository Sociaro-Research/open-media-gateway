# Versioned provider catalogue synchronization

The gateway checks the configured public documentation and media-model discovery
APIs daily, publishes safe changes, and keeps a journal of every completed check,
including checks with no changes. This is a deterministic gateway feature; it does
not run a coding agent or execute instructions found in provider documents.

## Configuration

In **Administration → Settings → Model catalogue**, choose one timezone for the
installation (initially `UTC`) and an optional report email. The dropdown lists
UTC offsets in numeric order, including fractional offsets, with recognizable
cities in parentheses. Offsets show standard time; daylight saving still follows
the selected region. Equivalent cities share one choice, while regions with
different clock-change rules remain separate. Existing IANA settings retain their
original identifiers. Reports use the
existing **Email delivery** transport; configure it before expecting delivery.
In **Administration → Shared providers → provider → Catalogue synchronization**,
choose the daily local time, enable the schedule, and review the source list.
There is no per-provider timezone. Changing the common timezone recalculates all
next checks and invalidates work using the old schedule configuration.

Default source lists belong to each provider module in
`plugins/provider/<id>/catalog_sources.go`. They are copied when a shared
connection first receives a schedule. Existing administrator edits are preserved
on startup and upgrades. New sources introduced by a code upgrade must be added
to existing schedules deliberately. Newly initialized schedules use `03:00` and
inherit the connection's enabled/non-archived state; set different times for
providers to distribute their work. Free/mock providers have no schedule.
Personal and administrator-defined manual model catalogues are not overwritten.

The shared timezone catalogue is generated from Go's bundled IANA database by
`go run scripts/catalog/build_timezones.go`. Regenerate it when updating timezone
data, and advance its reference date/window when maintaining the catalogue. Groups
compare exact future offset transitions over ten years; aliases and equivalent
cities do not create duplicate choices. This is presentation metadata; scheduling
continues to use the saved IANA identifier and server timezone rules.

The scheduler lives in the gateway process. It checks due work every minute,
processes one provider at a time, and uses a PostgreSQL advisory lock across
replicas. An overdue schedule catches up once after restart; it does not replay
an email for every missed day. A repeated daylight-saving hour runs at its first
occurrence, once; a skipped local time moves to the first valid time after it.
**Check now** queues the same workflow, including for a disabled daily schedule.
Concurrent manual requests coalesce. Source fetches have time/body limits, use
public-network destination checks and send no provider API credentials.

## Evidence, publication and warnings

Each source observation records its URL, parser, content hash, normalized
candidates and failure status. Extracted documents are stored by SHA-256, so
unchanged text is not duplicated. For HTML sources this is the relevant extracted
text (or normalized pricing records), not a browser screenshot or arbitrary
JavaScript execution. Current state, immutable numbered published versions and
individual check reports are separate records. Warning repeats and freshness
checks do not create another catalogue version. History currently has no automatic
retention limit; include these tables in normal database backups and capacity
planning. Versions are evidence, not an automatic billing rollback facility.

Provider modules can record a human-reviewed document snapshot by exact URL and
SHA-256, with a review date and explanation. A successful `document` observation
matching that fingerprint resolves its unstructured-change warning and retains
the review in state, journal/export and email. This does not import prices or
enable models. A different URL/hash, failed fetch or structured parser cannot
reuse that acknowledgement. Removing a source still cannot resolve warnings.
Reviewed text-output services may be retained as explicit non-executable scope
records, rather than repeatedly requesting media adapters.

When an adapter temporarily falls back to offline descriptors, the journal keeps
last-confirmed `API ` facts that are absent from those descriptors. Explicit live
values, including empty values, replace them normally. API discovery warnings and
source failures remain actionable; this prevents alternating metadata removal
and restoration from publishing false catalogue changes.

Automatic publication is limited to reviewed adapter contracts:

- Published descriptions, release dates/months, version dates, snapshot labels
  and retirement dates can update metadata without changing request protocols.
  API object creation remains distinct from public release. Conflicting facts
  preserve the existing value and produce a warning; releases use the earliest
  confirmed public date across regional release rows.
- Newly discovered models become available only when the current adapter already
  supports their exact ID and request/response contract. Unknown IDs, capabilities,
  reference rules and availability removals require review. No prefix-based
  protocol or pricing inheritance is introduced.
- Prices update only when currency, billing unit, region, selectors and usage
  metrics match an existing quantity estimator. Regional prices remain separate.
  Promotions, unreviewed video/audio reference tariffs, fixed surcharges, unknown metrics and
  special planning tables cannot inherit a simple no-reference tariff. Missing
  previously confirmed rates warn and preserve the last price; a failed source
  never deletes confirmed data. Conflicting tariffs are not alternated daily.

The list's reference estimate and new generation quotes read the same published
state. Accepted tasks retain their original immutable pricing snapshot for
metering and reconciliation. Customer responses continue to hide provider tariff
information through the existing price projection.

Supported parsers are deliberately explicit:

| Parser | Current purpose |
| --- | --- |
| `byteplus-releases`, `byteplus-models` | Release months and published model/reference specifications |
| `byteplus-pricing` | Standard Singapore video tariffs with reference-video/generated-audio selectors, free-input image tariffs and reviewed Seedream Pro size/reference-image charges |
| `alibaba-releases`, `alibaba-pricing` | Exact media IDs, release dates, regional image/video/character prices and reviewed audio/reference selectors |
| `alibaba-image-models`, `alibaba-retirements` | Published image capabilities and explicit model retirement notices |
| `openai-model`, `openai-pricing` | Model descriptions, complete Standard multimodal token rates and explicit legacy TTS character prices |
| `manifest-v1` | Strict JSON with a `models` array of normalized entries |
| `document` | Baseline and change monitoring for documentation requiring review |

Manifest tariffs must include an explicit numeric `price`, including zero for a
free scalar tariff. Multi-metric tariffs must specify every component's price.
Missing/null prices are errors, not inferred free usage.

BytePlus tables are read by their column headers: image IPM limits are distinct
from video output specifications. Only reviewed exact-version aliases are merged;
unknown versions still require an adapter contract. Seedream Flash uses its reviewed
single-image API contract. Seedream 3.0, deactivated on 13 May 2026, is retained as
an `inactive` historical entry without an executable contract and is excluded from
the adapter's model picker. An API retaining its old ID does not reactivate it.

Alibaba keeps exact regional and third-party IDs. Reviewed models documented in
another region are distinct from models previously discovered by the configured
account and subsequently missing. Discovery failures preserve both retired
records and explicit scope exclusions. Interactive worlds, bidirectional/live
conversations, voice-profile management and JSON-only portrait detection are
recorded with `excluded_reason`, sources and no executable contract. They are
reviewed scope decisions, not recurring actionable warnings; unknown future IDs
still require review.

Alibaba tables preserve regional heading scope, merged cells, generated-audio
selectors and input-image charges. New billing columns/units remain evidence
requiring review rather than disappearing from the parser. For example,
Qwen Audio 3.1 TTS Next has published token prices, but its documented file API
reports duration rather than enough token-quantity information for a reviewed
reservation/metering path; it stays blocked pending that contract.

Reviewed Wan reference-video quotes reserve the documented maximum billable
input duration. Continuation and output-only workflows do not add input duration
again. The additive `bill_input_video` snapshot flag freezes this choice per task;
omitted flags preserve legacy metering. A charged input requires provider-reported
input usage. Estimates are reservations, not settled provider invoices. Adapter tests
and reviewed tariffs define the supported subsets and metering assumptions.

Seedream Pro single-image tariffs select `1K`, `1.5K` or `2K` and include the charge
for reference images after the first free image. Layer decomposition is unavailable;
its reviewed tariff remains evidence and a change requires review. Seedance 1.5
prices select generated audio explicitly. Fixed image charges are calculated before
acceptance and frozen in the task snapshot, alongside the output tariff.

Seedance 2.x **video-reference** quotes use distinct tariffs and the reviewed
minimum-token tables for each supported resolution, aspect ratio and output
duration. Since the gateway does not probe remote clip metadata during quoting,
the reservation assumes the maximum combined input duration accepted by BytePlus:
15 seconds for 2.0 and 30 seconds for 2.5. The estimate states this assumption;
metering uses the actual returned `completion_tokens` and accepted tariff. It
does not bill the reservation as actual usage. Adaptive output proportions and
unreviewed draft workflows remain unavailable for quoting. The minimum-token
tables are a reviewed adapter baseline; they are not automatically scraped from
the Lark application. Review the adapter fixtures and tariff sources when changing
these assumptions.

Agnes and Sonilo initially use their model/service APIs for discovery and
`document` monitoring for their documentation/pricing sources. This records
changes but does not infer their pricing or new generation behavior from prose.
A changed unstructured document remains an open warning until a reviewed parser
can interpret it. Extending the parser and selecting it on the existing source
is the explicit resolution path; hiding a source does not mark its warnings fixed.
OpenAI model pages also monitor unparsed changes beyond their description.
OpenAI exact snapshots share published parent tariffs only through the module's
reviewed `CatalogAliases` map. Each snapshot retains its wire ID and quantity
estimator; unknown snapshots remain blocked. Sora 2 aliases and snapshots are
inactive historical entries following the provider's 2026-09-24 shutdown.
Image Mini keeps its separate size/quality planning table when tariffs refresh;
actual usage is still priced from returned tokens. Image 1.5 and ChatGPT Image
include internal text/reasoning output in both the planning estimate and reported
usage metering; absent output details leave metered cost pending. The provider
module owns these planning and quantity-estimation rules.
A malformed structured page produces a failed-source warning rather than a
successful empty import. This is bounded to configured sources: a vendor moving
to a completely new website requires an administrator/code update.

Warnings have a stable identity, first/last observed timestamps, occurrence
count, source and evidence. Quiet repeats update that record rather than creating
thousands of indistinguishable issues. Every check retains its warning snapshot.
Candidates are re-evaluated even when the upstream content hash is unchanged,
so deploying support can resolve an old warning on the next check.

## Journal and reports

**Administration → Model catalogue** offers checks, open warnings and published
versions, filtered by provider, with JSON details/export and source snapshots.
An error, warning, incomplete or partial verification, applied change and no-change result
are distinguishable. A check can apply safe changes while also retaining
unsupported candidates.

Email is sent after a check with applied changes, a new/changed warning, a
resolved warning, or incomplete/partial operational verification. A fully verified
unchanged check stays in the journal without email. Every email includes:

1. Applied changes with before/after values and sources.
2. New warnings requiring attention, highlighted in HTML.
3. Previous warnings still open, with their age and check count.
4. Resolved warnings.
5. Operational completeness, current contracts, missing fields and source provenance.

Mail work is persisted with the check transaction. Transient failures retry with
the same idempotency key, every 15 minutes, up to ten scheduled attempts; final
failure remains visible in the journal. The transport may also perform bounded
retries. A crash after provider acceptance relies on the transport's idempotency
window; absolute exactly-once email delivery cannot be guaranteed. Checks made
without a recipient are recorded as delivery disabled and are not backfilled
when an email is later configured.

## Investigating production warnings

The read-only commands below run using the deployment's database configuration:

```sh
docker compose --profile ui exec -T gateway \
  /gateway admin catalog-warnings
# Optional: --provider <connection-id>
docker compose --profile ui exec -T gateway \
  /gateway admin catalog-document --hash <sha256>
```

An agent can use the report's exact IDs, source URLs, normalized candidates and
old/new document hashes to reproduce a warning with local fixtures. Review the
provider's protocol, estimator and reference validation; update the module/parser
and add the relevant regression. After the normal reviewed release, queue a check
or wait for the daily run, then verify publication and resolved warnings. No agent
or document can authorize code execution, generation or deployment through the
synchronizer itself.

Administrator APIs mirror the UI under `/v1/system/catalog`: `settings`,
`schedules`, `schedules/{id}/check`, `runs`, `providers/{id}`,
`providers/{id}/versions`, and `documents/{hash}`. They require the live system
Administrator role and, for API keys, `system:manage`; all use `Cache-Control:
no-store`. Settings and schedule writes require their current `version` and are
audited. A stale update or an in-flight check with obsolete settings returns a
conflict and cannot overwrite newer configuration. See [OpenAPI](openapi.yaml).

## Source parser acceptance

Ordinary CI uses deterministic fixtures. The optional live check makes public
GET requests to every registered source without account credentials or generation:

```sh
GATEWAY_CATALOG_PUBLIC_SOURCES=1 go test -race -count=1 -parallel=4 \
  -run TestOfficialCatalogueSources ./plugins/provider
```

Run this check when registering/changing sources or investigating upstream format
changes. It verifies current fetching/parsing, not authenticated model discovery,
provider billing acceptance or future website stability. Keep the tested source
revision, command and limitations with your local or CI evidence.

## Catalogue verification coverage

Every new catalogue check records `completeness` in the run journal/API and
email report, independently of publication changes and existing warnings.
`incomplete` means required native-contract verification is missing. `partial`
means required native-contract fields are verified but optional provider details
remain unverified. Neither result changes model eligibility or request validation.
Both queue the existing daily report when a recipient is configured, even when
nothing was published. Historical runs without this assessment are **not
assessed**, never implicitly complete.

The `operational-v1` requirements cover the connected adapter's supported active
models, excluding retired models and explicitly excluded operations. Each field
states whether it is required. Requirements follow the existing native contract:
output modality and prompt policy, exposed output selectors, selectable video
duration, audio formats/voices where applicable, and effective reference workflows
and counts. Effective workflows retain the existing core fallback when no separate
workflow list is declared. Native support is not inferred from optional metadata.
A zero count means unsupported by that declared gateway workflow; it does not
assert that the upstream provider cannot implement a different operation.

`ReferenceMediaLimits` was introduced for Templates and is optional augmentation.
Its absence is **not** proof of a pre-existing synchronization/parser defect or
missing provider capability. File formats, sizes, dimensions and clip durations
are reported as optional details when represented there. Missing optional values
remain visible, but cannot make required-contract verification fail. Changes to
optional file metadata also cannot invalidate otherwise matching native-contract
proof. Ordered references need no invented marker grammar. Tariffs stay in their
existing records and change reports; the protocol view omits pricing rather than
representing an omitted price as zero.

Present native values remain **unknown** until a matching structured interpretation
is available in this run. A URL, generic capability fact, document acknowledgment
or stable document hash alone does not verify executable fields. The generic
structured mechanism is `manifest-v1`. Provider definitions can additionally
interpret already-fetched `document` text into typed `operational_fields`.
Native-contract matching excludes optional file metadata, which is assessed
separately. Provider interpretation is pure: it receives no credentials and
cannot activate models, change native options or publish tariffs.
Provider prose parsers are not declared broken merely because they do not emit
this new coverage representation. Missing mappings are reported as unverified,
without pretending to have lost previously supported fields. This requirement set
is not a claim to enumerate every vendor fact or verify a paid operation.

Failed retrieval, stale/unchecked sources and preserved last-confirmed evidence
remain explicit, with original source identities and snapshot hashes. Required
field uncertainty or unresolved source/supported-model warnings prevents complete
coverage. Warning fingerprints and resolution rules, rates, catalogue versions,
activation and native validators are unchanged.

### Detailed video API verification sources

Alibaba defaults include the Wan3, Wan2.7 R2V and HappyHorse R2V API references.
BytePlus defaults include the public Singapore ModelArk generation-task Markdown
endpoint advertised by its API documentation. The older documentation alias can
return a JavaScript shell; it is not silently treated as a verified contract.
Exact supported model IDs/family mappings remain explicit in their provider
modules. New models, formats and workflows are never enabled from prose.

The provider-owned interpreters extract typed selector sets, ranges, workflow
counts, prompt namespaces and represented file constraints. Verification checks
that the gateway's supported values fit the published contract. Effective visual
counts include the existing combined-visual bound; independent maxima must not
produce a false disagreement. Source values that cannot be recognized stay absent,
and disagreements produce per-field adapter-review evidence rather than guessed
metadata. Untyped document changes still require review even when known fields
parse successfully; an unchanged fingerprint never acknowledges an old warning.

Wan3 also documents a combined input-video plus output-duration ceiling. This is
published as a typed source relation. Independent native maxima do not establish
a coupled constraint: coverage stays incomplete if their sum exceeds the source
limit. An omitted or failed current interpretation preserves the last-confirmed
relation and cannot verify it. Publishing evidence does not install an admission
rule or change native request validation.

Compatible typed facts are published under administrator model metadata
`operational`, with their source URL, normalized snapshot and last-confirmed check
time. Runtime descriptors merge these informational facts without replacing native
capabilities or prices. Failed, missing or incompatible observations preserve the
last-confirmed values and their original provenance. Current verification status
is in the check's coverage, not an implied freshness guarantee on historical facts.
Customer projections retain the existing restriction on imported source evidence.

Default registrations initialize new schedules. Existing administrator-edited
schedules are preserved: add the intended API sources deliberately when adopting
this change. Missing expected sources are reported as `not_checked`, distinct from
failed retrieval or a successfully fetched but uninterpretable shell. Registering
sources in code does not trigger a live check or alter existing configuration.

Operational facts from multiple sources are reconciled by exact model and field
before publication. Different provider values remain a conflict even when both
cover the gateway's narrower supported subset. The last-confirmed published fact
and provenance are preserved, with stable per-field review evidence. Identical
subsequent observations cannot create another publication version or new alert.
Agreement uses a deterministic source identity for provenance; it does not prefer
one conflicting value over another.

Per-source `operational_history` retains the exact last-confirmed field snapshot
when a later document omits a field. History is read-only and never counts as a
current observation. Missing, null, stale or failed source evidence cannot resolve
an outstanding conflict or establish current agreement. Fresh agreement from the
known sources can resolve the field conflict; any independent changed-document
warning still requires its own existing review. No native options or prices are
changed by this informational reconciliation.

For explicitly unordered alternatives (resolution, aspect ratio, format, voice,
workflow, MIME/codec and reference-modality choices), comparison ignores list
order and duplicate values. Conflict grouping, native equality and publication
change detection share this field-aware rule. Source values and snapshot hashes
remain exact; ordered positions, frame roles and unknown arrays are not sorted.
Changing an alternative's membership remains a factual difference, and an
order-only document edit still retains its independent fingerprint review.
