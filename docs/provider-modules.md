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
- Billing documentation, region labels and embedded PNG/SVG assets. SVG remains
  vector; OpenAI's supplied SVG is embedded unchanged.

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
  replacement of a saved secret, and original SVG at desktop/mobile sizes.
- Release preflight checks tracked files and whitespace. Production verification
  uses the explicitly free mock provider and downloads the generated result.

Real OpenAI paid generation still requires an operator-supplied API key and is
not claimed as tested by fixture calls. See [OpenAI setup](openai-provider.md) for
supported model families, retirements and pricing limitations.

### Provider logo assets

When adding a provider logo, supply it on a square canvas with minimal internal padding. Wide wordmarks are not supported. The console uses one 38 × 38 px icon slot with `object-fit: contain` and renders the provider name beside it. Set `IconMonochrome` only for black monochrome artwork that needs inversion on the dark background. Flag non-square artwork before integrating it; request a square source rather than adding provider-specific layouts.

Record the original asset URL, upstream version/date and applicable permission or
usage terms when adding artwork. The project license does not establish rights
to third-party branding. Provider names and the model picker's text fallback
remain available without an icon; check registry/API and browser tests before
removing or replacing an embedded asset. Keep third-party notices with distributed
assets and record provenance for upstream-derived catalogue test fixtures too.
