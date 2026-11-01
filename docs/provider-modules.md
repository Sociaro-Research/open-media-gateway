# Provider modules

## Integration workflow

Adding a provider requires **one module directory and one composition-file edit**:

1. Add `plugins/provider/<id>/` containing the adapter, `Definition()`, model
   catalog, vendor rules, tests and optional assets/billing integration.
2. Import that package and add its `Definition()` to the list in
   `plugins/provider/registry.go`.
3. Run checks, rebuild and restart the gateway and worker. Configure the new
   connection through Providers, or Shared providers for organization use, and
   turn on Available for generation. Model access settings also apply.

There is no additional list in configuration, policy, the HTTP API, UI,
application startup, pricing or billing. Copying a folder alone does not change a
Go binary: explicit composition is intentional, reviewable, and avoids implicit
`init()` registration and runtime plugin loading. A generator could produce the
same composition file later; it would still require rebuilding.

## Product boundary

Every module must follow the [media generation scope](media-scope.md). Public
catalogs, discovery and direct admission expose media workflows only. Native
preparation descriptors remain separate from the public model catalog and cannot
be enabled by a manual model or a stale saved access setting. Preserve internal
preparation and already-accepted task polling when retiring public utility models.

## Module contract

`internal/providerkit.Definition` describes:

- Identity, display name, aliases, default model and optional default-provider flag.
- Connection fields: key, label, secret flag, required flag, default and legacy
  environment-variable alias. The UI renders this schema and sends the same field
  keys in its update request. Arbitrary new string fields require no host edit.
- Runtime settings and environment aliases, including provider-specific timeouts.
- Adapter factory, receiving settings, decrypted credentials and the host HTTP client.
- Optional price quotation, complete bill snapshots and bill
  normalization. A module may implement `core.CostQuoter` on its adapter instead.
- Free-request and confirmed-failure billing semantics. A failed request is only
  marked free when the module explicitly declares that provider contract.
- `PublicCatalog`: discovery is unauthenticated and cannot verify credentials.
  The shared connection status remains unverified even after discovery succeeds.
- Optional `CatalogAliases`: exact reviewed snapshot-to-parent tariff mappings.
  This permits source-price updates for known snapshots after checking their own
  estimators. It never enables future model IDs or overrides manual contracts.
- Billing documentation and region labels. Provider artwork is uploaded per
  connection; see [Provider logos](#provider-logos).

The host owns encryption, owner isolation, configuration persistence, connection
validation, adapter caching, reference resolution, task processing and generic
accounting. The module owns vendor HTTP payloads, credentials, model names,
reference modes, prices, error meanings, invoice endpoints and normalization.
Optional interfaces such as `PreferInlineImages()` describe behavior without
checking the provider name in the host.

All optional billing fields use the `billing` group. Once any is supplied, every
field in that group is required. Secrets are never included in `values`; only
masked hints are returned in `hints`. Existing fixed credential properties remain
as wire/source compatibility fields; new modules use `Credentials.Get/Set` and
`ProviderUpdate.Extra`, without adding struct members.

## Configurable connections

A module describes an API adapter. Administrators can create multiple independent
connections using that adapter without registering another module or restarting
processes. Modules expose `Profiles` for manually configured upstream models. See
[configurable connections](provider-connections.md) for routing, secret headers,
manual catalogs and accepted-task revisions.

## Model controls

Standard controls remain in `MediaOptions`. Modules describe reference modes with
counts, frame roles, voice-reference eligibility and duration limits; the core and
browser consume the same metadata without recognizing model names.

Additional controls can be declared in `MediaModel.Parameters` and supplied under
`options.parameters`. The built-in renderer supports text, select, boolean,
number and integer inputs. The core validates allowed keys, types, required
values, choices, numeric ranges and text lengths, applying declared defaults.
The adapter explicitly translates validated controls into its native payload;
unknown parameters are rejected rather than forwarded blindly. Controls are
included in estimates, generation requests, task details and task reuse.

`PromptOptional` permits media-driven operations without text direction;
`NoPrompt` hides the prompt and rejects supplied text. Music and audio models can
advertise whole-second duration bounds/defaults/steps. `DurationFromInput`
rejects overrides, and fractional durations use a declared number parameter.
Video models with no aspect-ratio catalog accept an omitted ratio. Declared
formats are validated for all media types. These capabilities are shared by
admission, the OpenAI-style API and the console.

The [Sonilo integration](sonilo-provider.md) added one module and one registry
entry, plus these generic prompt/duration/format capabilities and the common
parameter renderer's floating-label fix. It required core, API and UI changes;
it was not folder-only. Vendor operations, multipart payloads, service discovery,
polling, timelines, output selection and tariffs stay inside its module. Its vendor-specific behavior remains in the provider module.

A new media type, streaming protocol, authentication flow (such as OAuth), or an
interactive control beyond these types can still require extending the shared
contract. That is a platform capability change, not normal provider registration.

## Environment bootstrap

The primary connection path is the encrypted console configuration. For native
processes, module-declared environment aliases remain supported. Compose loads
the optional ignored `.env` into both application services, so new aliases no
longer require modifying `compose.yaml`. Core deployment variables retain their
explicit Compose values.

Alternatively use the generic `PROVIDER_SETTINGS` JSON object, keyed by provider
ID and setting key. It overrides module defaults and environment aliases:

```json
{"example":{"api_key":"YOUR_KEY","base_url":"https://api.example.test/v1","model":"example-image"}}
```

Runtime setting keys are also declared by the module. Existing encrypted database
rows remain authoritative; environment bootstrap only creates missing rows and
never overwrites saved keys or re-enables a saved disabled connection. Gateway
and worker must receive the same runtime settings and encryption key.

## Shared responsibilities

| Area | Contract |
| --- | --- |
| Registration | One explicit composition list |
| Configuration/startup | Module settings and registry-derived defaults |
| Credentials UI | Schema-driven fields, public values and secret hints |
| Availability | Connection availability plus model access policy |
| Quotes | Module-owned `CostQuoter`, preserving the credential fingerprint |
| Vendor bill APIs | Billing retrieval and normalization owned by the module |
| References | Module-declared modes, counts and roles |
| Extra controls | Validated additive parameter schema |

Extend shared contracts only when a new capability cannot be expressed by the
existing module interfaces. A normal provider integration should not add vendor
branches to core scheduling, credential management or browser components.

## Compatibility and removal

Existing provider IDs, encrypted credential JSON, endpoints and accepted-task
fingerprints are preserved. Existing modules explicitly opt into the legacy
fingerprint algorithm. New modules derive their fingerprint from all declared
inference fields, excluding billing credentials. Changes to an inference identity
are rejected for already accepted work.

Migration 025 changes the historical `free_mock` accounting marker to
`free_provider` and fills explicit zero costs for legacy free tasks. It preserves
paid task costs. Historical migration SQL and documentation examples may still
name providers; they are not runtime registration dependencies.

Removing an entry and its import hides the module everywhere and prevents new
connections. Saved credential rows and task history are retained, allowing
reinstallation. Finish active jobs before removing an adapter: a removed adapter
cannot continue their provider polling. Remove/replace an explicitly configured
default provider too. Disabling in the console is the way to stop new work while
keeping the adapter available for accepted jobs. An unused vendor SDK can later
be removed with `go mod tidy`.

## Validation

- Full formatting, vet, race tests and application builds through `make verify`.
- Registry test with an unknown module/credential: encrypted save, schema and
  secret-hint output, quote, identity, generation, caching, removal and reinstall.
- Existing owner-isolation and OpenAI HTTP/price tests retained; vendor billing
  tests moved with their implementation.
- Registry rejects duplicate IDs, ambiguous aliases and reserved credential keys.
- Parameter validation rejects unknown fields, wrong types and out-of-range values.
- Isolated PostgreSQL tests cover accounting plus migration of legacy free and
  paid records; no live provider credentials are used by tests.
- Browser fixture verifies image/TTS controls, additional schema controls,
  replacement of a saved secret, and uploaded PNG at desktop/mobile sizes.
- Release preflight checks tracked files and whitespace. Production verification
  uses the explicitly free mock provider and downloads the generated result.

Real OpenAI paid generation still requires an operator-supplied API key and is
not claimed as tested by fixture calls. See [OpenAI setup](openai-provider.md) for
supported model families, retirements and pricing limitations.

### Provider logos

Administrators upload or replace each connection's logo in its provider settings
using the shared click/keyboard and drag-and-drop upload area. A round × beside
installed artwork removes it immediately and persistently without changing
provider credentials or settings; uploading again remains available.
Only PNG images with at least one partially or fully transparent pixel are accepted,
including palette transparency and 16-bit PNG. The upload limit is 1 MiB, with
1–2048 pixels per side (at most 4,194,304 decoded pixels). SVG, other formats,
malformed files and fully opaque PNG are rejected; no source-color check is applied.

The original image is displayed unchanged in dark theme. Light theme sets RGB to
black while preserving every alpha value. The same shared renderer covers the
provider list, settings preview and all model-picker groups: Playground, Templates,
organization model grants, markup rules and connection API request profiles.
Live display-mode changes update existing images. Rectangular logos preserve
their aspect ratio inside the shared icon slot.

There are no bundled provider logos or legacy asset fallbacks. Missing/failed logos
show an initial; settings explicitly show **No logo** until an upload succeeds.
Old artwork remains recoverable in Git history. Uploads are associated with both
the resource owner and concrete provider connection, stored atomically in PostgreSQL
separately from credentials, and use content-versioned authenticated URLs. Failed
replacement keeps the previous logo. Serving uses private no-store caching and
nosniff/sandbox headers. Ordinary users cannot replace logos; display follows the
existing provider/model and commercial-access permissions.

The shared renderer retains decoded logos in memory within the current tab.
Reopening or filtering a model picker reuses the same resource synchronously;
concurrent cold renders share one authenticated fetch and decode. Cache identity
includes the complete versioned URL (owner, connection and access query) and the
current sign-in/key/workspace epoch. Confirmed replacement or deletion invalidates
all URL variants for that connection, including pending reads. Known authorization
failures, sign-out and context changes clear prior resources; failures retry on the
next render. No logo bytes are written to browser persistent storage.

The least-recently-used cache retains at most 32 decoded resources, 8 MiB of PNG
data and 16 million decoded pixels. At most four cold fetch/decode operations
run concurrently; other pending renders queue without fetching bytes. Entries revalidate after five minutes on their
next use. A warm memory hit reuses its prior authorization; it cannot detect an
unknown server-side revocation until another request reports it or revalidation
occurs. HTTP responses retain their authenticated private no-store policy.

Uploaded artwork remains subject to its owner's branding permissions; the project
license does not establish rights to third-party marks. Keep provenance and notices
with any independently distributed artwork or upstream-derived catalogue fixtures.
