# MiniMax image and video provider

MiniMax's direct international API uses `https://api.minimax.io` and a
pay-as-you-go API key. M Plan keys, regional MiniMax products and self-hosted H3
are separate products. Configure `api_key` and `base_url` through the encrypted
provider connection; optional environment aliases are `MINIMAX_API_KEY`,
`MINIMAX_BASE_URL` and `MINIMAX_MODEL`. Credentials never belong in source files.

The module definition is `plugins/provider/minimax/definition.go`. It is explicitly composed in `plugins/provider/registry.go` alongside the other
built-in providers. The host injects `core.ProviderConnectionScope` after resolving authorized
credentials. Protected operations reject a missing concrete connection ID,
provider owner or accepted revision; no client-supplied scope is accepted. This document records implemented adapter contracts and
remaining shared integration work; it does not establish full product completion.

## Authoritative inventory

Reviewed on 31 October 2026 against the official
[documentation index](https://platform.minimax.io/docs/llms.txt),
[API overview](https://platform.minimax.io/docs/api-reference/api-overview),
[model lifecycle](https://platform.minimax.io/docs/faq/models), and endpoint
OpenAPI contracts. Endpoint enums are more complete than the overview tables:

- The H3 creation contract includes H3, Highspeed, Lite and Max. The shorter
  lifecycle table omits Highspeed and Lite; that omission is not retirement.
- The image-to-image enum includes `image-01-live`, while its adjacent model
  description says only `image-01`. The text-to-image enum includes only
  `image-01`; live is therefore exposed only with a portrait reference.
- Legacy endpoint enums still include T2V-01, I2V-01 and S2V-01 variants. No
  reviewed source declares those video models retired. Their availability is
  documented, not verified by a paid request or inferred from the shorter FAQ.
- No image/video retirement notice was found in those sources. Language,
  speech, transcription, music, voice cloning, web search and model self-hosting
  are outside this image/video integration. The FAQ's language/music retirement
  sections do not retire an unrelated image/video model.

Discovery is a reviewed built-in catalogue. The documented OpenAI-compatible
`GET /v1/models` endpoint provides authentication checking, not a complete media
capability catalogue. A successful check means API authentication only; it does
not verify image/video grants, funds, whitelist access or successful generation.
No paid generation is used for connection checks. All connection checks in this
task used deterministic mocks, never a live account.

## Model and workflow completeness matrix

The 33 catalogue entries comprise 15 upstream image/video models, 16 Video Agent
workflows and two explicit operation aliases (Context-IR and regeneration). They use `Generate`, `ValidateRequest` and their
catalogue entries. `TestEveryCatalogGenerationWorkflow` covers every declared
entry and reference mode. Native supporting methods have separate tests. An
adapter-level result does not by itself establish shared persistence or UI support.

| Model or operation | Implemented workflows and constraints | Price / unit | Contract and tests | Remaining limit |
| --- | --- | --- | --- | --- |
| MiniMax-H3 | Text; first frame; last frame; first/last frames; image/video/audio references. 768P/2K, integer 4–15s. | 768P: output $0.08/s, input video $0.08/s, excess image $0.04/image. 2K: $0.13/s, $0.13/s, $0.04/image. First 5 images free. | [H3 create](https://platform.minimax.io/docs/api-reference/video-generation-v2-create); `requests.go`, `catalog.go`; generation, multimodal, validation and pricing tests. | Live entitlement unverified. |
| MiniMax-H3-Highspeed | Same five workflows; 480P/768P, integer 4–15s; no 2K. | 480P: output/input video $0.04/s. 768P: $0.06/s. Excess images $0.03/image; first 5 free. | [H3 create](https://platform.minimax.io/docs/api-reference/video-generation-v2-create); same tests for this model. | Live entitlement unverified. |
| MiniMax-H3-Lite | Same five workflows; 480P/768P, integer 4–15s; reference workflow requires a concrete ratio. | 480P: output/input video $0.025/s. 768P: $0.04/s. Excess images $0.02/image; first 5 free. | [H3 create](https://platform.minimax.io/docs/api-reference/video-generation-v2-create); same tests, including adaptive/2K rejection. | Live entitlement unverified. |
| MiniMax-H3-Max | Same five workflows; 480P/768P, integer 5–15s; prompt expansion disabled/balanced/quality. | 480P: output $0.05/s, input video $0.0553/s. 768P: output $0.08/s, input video $0.143/s. Excess images $0.074/image; first 2 free. | [H3 create](https://platform.minimax.io/docs/api-reference/video-generation-v2-create); same tests, including 4s rejection and expansion mapping. | Live entitlement unverified. |
| MiniMax-Hailuo-2.3 | Text or first frame. 768P 6/10s; 1080P 6s. Prompt optimizer and fast pretreatment. | $0.28 / 768P 6s video; $0.56 / 768P 10s; $0.49 / 1080P 6s. | [Text](https://platform.minimax.io/docs/api-reference/video-generation-t2v), [image](https://platform.minimax.io/docs/api-reference/video-generation-i2v); generation and legacy polling tests. | Duration-conditioned refresh is implemented; representative-request host forwarding is pending. |
| MiniMax-Hailuo-2.3-Fast | First frame only. 768P 6/10s; 1080P 6s. Optional prompt, optimizer and fast pretreatment. | $0.19 / 768P 6s; $0.32 / 768P 10s; $0.33 / 1080P 6s. | [Image](https://platform.minimax.io/docs/api-reference/video-generation-i2v); generation, validation and legacy polling tests. | Same host-forwarding limit. |
| MiniMax-Hailuo-02 | Text, first frame, last frame, first/last frames. 768P 6/10s; 1080P 6s. 512P 6/10s only in first-frame workflow. | 768P/1080P as Hailuo 2.3; 512P $0.10 / 6s or $0.15 / 10s. | [Text](https://platform.minimax.io/docs/api-reference/video-generation-t2v), [image](https://platform.minimax.io/docs/api-reference/video-generation-i2v), [first/last](https://platform.minimax.io/docs/api-reference/video-generation-fl2v); all modes and boundary tests. | No 512P text or first/last claim. Same host-forwarding limit. |
| T2V-01-Director | Text-to-video, 720P 6s, camera commands in prompt, prompt optimizer. | Unknown. | [Text](https://platform.minimax.io/docs/api-reference/video-generation-t2v); generation contract test. | No invented tariff. |
| T2V-01 | Text-to-video, 720P 6s, prompt optimizer. | Unknown. | [Text](https://platform.minimax.io/docs/api-reference/video-generation-t2v); generation contract test. | No invented tariff. |
| I2V-01-Director | First-frame video, 720P 6s, optional prompt and optimizer. | Unknown. | [Image](https://platform.minimax.io/docs/api-reference/video-generation-i2v); generation contract test. | No invented tariff. |
| I2V-01-live | First-frame video, 720P 6s, optional prompt and optimizer. | Unknown. | [Image](https://platform.minimax.io/docs/api-reference/video-generation-i2v); generation contract test. | No invented tariff. |
| I2V-01 | First-frame video, 720P 6s, optional prompt and optimizer. | Unknown. | [Image](https://platform.minimax.io/docs/api-reference/video-generation-i2v); generation contract test. | No invented tariff. |
| S2V-01 | One character portrait, optional prompt and optimizer. No invented duration/resolution controls. | Unknown. | [Subject reference](https://platform.minimax.io/docs/api-reference/video-generation-s2v); generation contract test. | No invented tariff. |
| image-01 | Text-to-image or one character portrait. Eight ratios or paired 512–2048px dimensions divisible by 8. Seed and prompt optimizer. Batch size n=1–9; Gateway uses URL results. The native helper also decodes the official Base64 response. | $0.0035 / image. | [Text](https://platform.minimax.io/docs/api-reference/image-generation-t2i), [portrait](https://platform.minimax.io/docs/api-reference/image-generation-i2i); generation, all-count/all-format batch and partial-safety tests. | All results pass through real worker/local archival and authenticated public GET/HEAD in deterministic tests; PostgreSQL durability remains separate. |
| image-01-live | Portrait-to-image only; one portrait, eight ratios, seed and optimizer. Custom dimensions are not advertised because width/height are documented only for image-01. | Unknown. | [Portrait enum](https://platform.minimax.io/docs/api-reference/image-generation-i2i); generation and text-only rejection tests. | No invented T2I capability or inherited image-01 tariff. |
| H3 Context-IR | `MiniMax-H3-Context-IR` uses common `Generate`/`GetTask` and returns a shared text artifact; native `CreateContext`/`QueryNativeTask` are also available. Same H3 inputs, 4–15s target duration, no resolution. | $0.90 / million input tokens; $3.60 / million output tokens. | [Context](https://platform.minimax.io/docs/api-reference/video-generation-v2-h3-context-ir); context protocol and text-result tests. | Real worker/local text archival and public GET/HEAD pass; PostgreSQL text-task admission still needs an additive migration. Quote returns known token rates with an explicit unknown-total error; shared admission must preserve this rate-only snapshot without treating it as zero cost. |
| H3 regeneration by source task | `MiniMax-H3-Regeneration` maps common `Generate`/`GetTask` to `RegenerateTask`: signed owner-bound original H3 handle; queries source and requires succeeded 768P output; 2K target, optional AIGC watermark. | Output $0.05/s; original input video $0.05/s; original images above 5 cost $0.025/image again. | [Regeneration](https://platform.minimax.io/docs/api-reference/video-generation-v2-regeneration); both-protocol and cross-owner tests. | Vendor whitelist access required. Common quotation reuses the original task’s reported output/input seconds and image allowance; the common public HTTP/worker/archive test passes with a signed source handle; owner-resolved source selection remains pending. |
| H3 regeneration by base video | `RegenerateVideo`: exact original content plus owned 768P source output as one base_video. Signed original-content digest rejects changed prompt/materials. | Same regeneration tariff, separately from original task. | [Regeneration](https://platform.minimax.io/docs/api-reference/video-generation-v2-regeneration); both-protocol and changed-content tests. | Current wrapper requires a recent owned task and retained original request. The documented standalone conforming-video path still needs owner-inspected resource admission; shared public workflow pending. |

All prices above are from the official [pricing page](https://platform.minimax.io/docs/pricing/overview),
checked 31 October 2026. H3 audio input is free. USD monetary estimates are not
confirmed invoices, package conversions or evidence of a free trial.

### Video Agent templates

Each row is a separate `video-agent-<template_id>` catalogue entry and maps to
`POST /v1/video_template_generation`. Polling uses
`GET /v1/query/video_template_generation` and its returned `video_url`.
The [official template list](https://platform.minimax.io/docs/faq/video-agent-templates)
identifies these 16 entries; the
[creation contract](https://platform.minimax.io/docs/api-reference/video-agent-create)
defines ordered `media_inputs[].value` and `text_inputs[].value`.
`TestEveryCatalogGenerationWorkflow` covers all 16 serializers;
`TestLegacyAndAgentPolling` covers all documented polling states.

| Template ID | Template | Declared input |
| --- | --- | --- |
| 392747428568649728 | Diving | Image |
| 393769180141805569 | Run for Life | Image and text |
| 397087679467597833 | Transformers | Image |
| 393881433990066176 | Still rings routine | Image |
| 393498001241890824 | Weightlifting | Image |
| 393488336655310850 | Climbing | Image |
| 394514820878671878 | Anime Life Sim | Text |
| 393879757702918151 | McDonald's Delivery Pet | Image |
| 397017167949312007 | Pet Pilot | Image |
| 394176968202485769 | Miniature Set Ad | Image |
| 393876118804459526 | Male Model Try-On Ad | Image |
| 393866076583718914 | Female Model Try-On Ad | Image |
| 394875727173492739 | Art Fonts | Text |
| 394220989629177861 | Drinkfall | Image |
| 393853165953970178 | E-commerce Display Ad | Text, following the input column |
| 401431836934868999 | 3D character product presentation | Image and text |

These are 16 callable template workflows, not 16 separately named upstream model families. No template-specific tariff is published in the reviewed pricing page. Prices
remain unknown. The E-commerce Display Ad description mentions a photo while its
media-input column says none; the adapter follows the explicit column and retains
this discrepancy. Exact per-template multi-placeholder counts are not documented. The native
`CreateVideoAgent` and common generation preserve ordered image and text arrays.
A Gateway policy caps the aggregate at 64 images/texts, including the primary
prompt; this is not a documented per-template MiniMax allowance. Actual required
placeholder counts remain provider-validated. Text-enabled entries expose a
validated ordered `additional_texts` list through common generation (including
the Art Fonts word/element example); this is covered by a common-contract test.
The shared `text_list` contract validates item counts and per-item lengths;
exact provider-required counts remain unknown where the official index omits them.
The browser renderer for this new shared field type is pending; API fixtures alone
do not establish Playground or Templates authoring acceptance.
Image-only templates have no prompt and therefore cannot currently satisfy the
shared Templates author's mandatory-prompt contract; they remain native generation
operations until that shared limitation is resolved. The shared Templates
authoring contract also requires the `references` workflow; frame-only legacy
models cannot currently be authored as Templates, although their frame workflows
are implemented through ordinary generation. H3 References, S2V portraits, both
image models and the prompt-bearing image/text Video Agent workflows pass the
actual Templates slot/admission/normalization contract tests.

The legacy text-to-video source is internally inconsistent for T2V-01 and
T2V-01-Director: its duration table has a 1080P entry under “Other models”, while
its resolution table lists 720P for those models. The adapter conservatively
exposes 720P and retains this unresolved discrepancy; it does not infer 1080P
support from the conflicting row.

## Public generation paths

The configured connection namespace prefixes the catalogue ID, for example
`minimax/MiniMax-H3`. Additional connection IDs use their own namespace.

| Workflow | Gateway creation path | Result path |
| --- | --- | --- |
| H3, legacy video and Video Agent | `POST /v1/videos` or `POST /v1/generations` | `GET /v1/generations/{id}` and authorized content URL |
| image-01 / image-01-live | `POST /v1/images/generations`; referenced portrait inputs also use the shared image-edit contract | Generation status exposes each archived image artifact and content URL |
| Context-IR | `POST /v1/generations`, model `MiniMax-H3-Context-IR` | Text artifact content URL; accounting-enabled admission and PostgreSQL text support remain pending |
| Source-task regeneration | `POST /v1/videos`, model `MiniMax-H3-Regeneration`, `options.parameters.source_task` | Normal generation status/content; public source selection still needs an owner-resolved binding |

These are shared Gateway routes. Seven representative scenarios exercise them
through the actual decoder, task service, worker and local storage with mocked
provider HTTP, queue and repository. That evidence does not establish live access,
PostgreSQL restart durability or accounting-enabled admission for unknown totals.
An unknown tariff is never a free quote: accounting-enabled admission may reject
an operation whose total cannot be safely reserved. Video Agent, legacy models
without a published tariff, image-01-live and Context-IR retain explicit pricing
limitations rather than invented estimates.

## Parameters and references

H3 frame and reference workflows are mutually exclusive. Frame output uses
`adaptive`; text output requires a concrete ratio. Available concrete ratios are
16:9, 21:9, 4:3, 1:1, 3:4 and 9:16. Reference workflows accept adaptive except on
Lite. Prompt length is at most 7000 Unicode code points for H3, 2000 for legacy
video and 1500 for image generation. Legacy camera-command syntax remains
unchanged in the prompt. H3's official examples use natural-language phrases such
as “reference image 1”; no mandatory tag grammar is invented.

H3 accepts at most 9 reference images, 3 videos and 3 audio clips, with at most
12 files total. Images: JPEG/PNG/WebP/HEIC/HEIF, 30 MB, dimensions 256–5760px,
ratio 0.4–2.5. Videos: MP4/MOV with H.264/HEVC, 50 MB, 2–15s each and 15s total,
23.976–60fps, the same dimension/ratio range. Audio: WAV/MP3, 15 MB, 2–15s each
and 15s total. The native JSON request is bounded to 64 MB. Large references
should use URLs or uploaded files. Gateway storage/inspection may impose additional
supported-format limits; advertising a vendor format does not establish that
all local inspectors decode it.

Image portraits accept one JPEG/PNG below 10 MB. Legacy video portraits and
frames accept JPEG/PNG/WebP below 20 MB, short edge above 300px and ratio 0.4–2.5.
Image width/height and aspect ratio are mutually exclusive in admission so that
the adapter never silently sends a custom dimension the provider would ignore.

## Supporting API completeness matrix

| API | Adapter contract | Tests and boundaries |
| --- | --- | --- |
| [H3 query](https://platform.minimax.io/docs/api-reference/video-generation-v2-query) | `GetTask`, `QueryNativeTask`; queued/running/succeeded/failed/cancelled; verifies model, task ID and task type. | Every H3 family/state, malformed identities, outputs and usage. Cancelled maps to Gateway failure because its task-status contract has no cancellation state. |
| [H3 list](https://platform.minimax.io/docs/api-reference/video-generation-v2-list) | `ListTasks`, pagination and repeated task-ID filter. | Requires signed owned handles; rejects unrequested IDs rather than returning account-wide inventory. |
| [H3 cancel/delete](https://platform.minimax.io/docs/api-reference/video-generation-v2-delete) | `CancelOrDeleteTask`; queued cancellation or terminal-record deletion; no automatic cleanup. | Signed owner/account gate and response-action validation. Provider rejects running-task cancellation. |
| [Legacy query](https://platform.minimax.io/docs/api-reference/video-generation-query) | Preparing/Queueing → queued; Processing → running; Success → succeeded; Fail → failed. | All states and identity checks. |
| [Legacy result](https://platform.minimax.io/docs/api-reference/video-generation-download) | Owned successful task → retrieve file → temporary download URL for Gateway archival. | Numeric IDs retained as strings; authenticated file metadata, valid HTTPS output. |
| [File upload](https://platform.minimax.io/docs/api-reference/file-management-upload) | `UploadFile`, purpose video_generation_input, bounded multipart bytes, exact extension/type checks. | File lifecycle, multipart body, IDs above JavaScript's safe integer limit, foreign owner rejection. |
| [File list](https://platform.minimax.io/docs/api-reference/file-management-list) | `ListFiles` filters upstream inventory against owned handles. | Unowned files are excluded; no public account-wide enumeration. |
| [File retrieve](https://platform.minimax.io/docs/api-reference/file-management-retrieve) | `GetFile`, owner-bound handle, matching purpose/ID. | Foreign reads and malformed/raw handles rejected before HTTP. |
| [File content](https://platform.minimax.io/docs/api-reference/file-management-retrieve-content) | `OpenArtifact` streams protected bytes; `DownloadFile` is a bounded native convenience. Both enforce byte ceilings, status and media signatures. The source schema labels binary as application/json; actual media bytes are sniffed and JSON error bodies rejected. | Foreign owner, JSON-as-media and invalid limit rejection. |
| [File delete](https://platform.minimax.io/docs/api-reference/file-management-delete) | `GetTaskFile` derives an output-file handle only from an owned successful legacy task; `DeleteFile` accepts that documented video_generation purpose. | Input deletion is rejected: video_generation_input is absent from the official delete enum. It is not silently replaced by another purpose. Input files expire after 7 days. |
| [Models list](https://platform.minimax.io/docs/api-reference/models/openai/list-models) | `CheckCredentials`, success cached 5 minutes, marked non-generating request. | Concurrent check caching; malformed schema and auth/access failures remain unverified. |

Uploaded input handles are signed for the configured account, immutable concrete
connection identity, Gateway owner, operation type and media kind. Reusing an
audio upload as an image reference is rejected before submission. Their opaque `asset://` form is translated inside MiniMax to
`mm_file://<file_id>` only after signature/owner validation. Raw `mm_file://`
strings are rejected. File handles and task handles are not interchangeable.

Provider response text can contain private inputs. Errors expose stable safe
categories and codes, never raw response messages, keys or transport URLs.
Requests are never retried automatically; uncertain paid submissions are reported
as uncertain. Redirects do not replay credentials or submissions. Gateway webhooks remain
Gateway-owned; MiniMax callback URLs/challenge handling are not passed through
per-request parameters because task delivery uses the shared polling/webhook
contract.

H3 final usage preserves the raw usage object and normalizes output video seconds,
input video seconds, excess input images and token counters. Missing counters
remain unknown. Multi-component metering requires all priced counters. Quotes use
owner-inspected input-video duration when available; otherwise they reserve the
15-second documented input maximum, explicitly as an estimate. Context token
usage is priced separately; no undocumented token estimator is invented.

## Evidence levels

“Verified documentation” below means reviewed official API contracts, not a paid
access test. “Common contract” means local Go admission/adapter fixtures; it does
not establish a production Gateway HTTP path or browser acceptance.

| Area | Verified official support | Local deterministic checks | Gateway end-to-end | Live account |
| --- | --- | --- | --- | --- |
| Four H3 generation models / five modes each | Current creation enum, limits, roles and query contract | Serializers; shared normalization; Templates reference binding; states; errors; multi-input prices | Representative H3 URL flow passes through real HTTP/service/worker/local archive with mocked persistence; built-in composition verified separately | Not tested |
| Nine legacy video models | Endpoint enums; conflicting T2V resolution row explicitly retained | Serializers; optional image prompts; states; owned file result retrieval | Representative protected legacy file passes through real HTTP/service/worker/local archive; PostgreSQL durability separate | Not tested |
| image-01 and image-01-live | Separate T2I/I2I enums, dimensions, counts and encodings | Canonical output count; all native batch counts/formats; partial safety; artifacts and estimates | Single image and two-image batch pass real HTTP/service/worker/local archive plus content GET/HEAD; PostgreSQL durability separate | Not tested |
| 16 Video Agent workflows | Template IDs, required kinds and ordered arrays | Every template serializer; native ordered arrays; ordered additional text list through common generation | Representative text workflow passes real HTTP/service/worker/local archive; browser list renderer and undocumented placeholder counts remain unresolved | Not tested |
| Context-IR alias | Context creation/query and token-price units | Common provider path, shared text artifact and all task states | Real HTTP/service/worker/local text archive and content GET/HEAD pass with accounting disabled; rate-only accounting and PostgreSQL text admission pending | Not tested |
| Source-task regeneration alias | Whitelist-gated source-task creation and 2K output | Owned source query, common path, recharge quote/usage and states | Real HTTP/service/worker/local archive passes with a signed source handle; owner-resolved source selection pending; built-in composition verified separately | Not tested |
| Standalone conforming-base-video regeneration | Documented independently of a recent source task | Current helper covers recent owned source tasks only | Incomplete: standalone owner-inspected video admission absent | Not tested |
| Supporting tasks/files | Reviewed management endpoints and deletion-purpose discrepancy | Owner/connection/revision/media-kind gates, errors, lists, cancellation, upload, output-file deletion and protected streams | Private management routing/bindings pending | Not tested |
| Public prices | Complete official pricing page and exact units | Actual catalogue ingestion replay: 8 priced entries / 28 tariff variants; duration selection and preservation | Representative-request host forwarding still pending for 512P | Account-specific invoice rates not tested |

## Integration and validation boundaries

The native adapter is implemented independently of ongoing shared work. Official
H3, image-01 and all 11 Hailuo duration/resolution price rows are parsed with
exact units, including replay through the real catalogue ingestion pipeline.
A generic `Rate.Duration` selector keeps 6s/10s tariffs distinct. Canonical
`options.output_count` and image API `n` use the shared `MaxOutputs=9` metadata;
no second provider-specific image-count control is introduced. MiniMax
`CatalogRateRequest` prepares executable representative workflows, including
Hailuo-02 512P first-frame input; the host must forward that optional hook.

Full acceptance still requires catalogue-hook
integration, final PostgreSQL multi-artifact verification, Context-IR rate-only tariff admission and PostgreSQL text-result persistence,
owner-resolved public regeneration resources, appropriate shared API
and README documentation, final integrated regressions and final independent
review. The current base-video wrapper covers recent owned tasks only; standalone
owner-inspected conforming-video regeneration remains incomplete. No full
completion claim is made while those items remain unresolved.

Run deterministic adapter checks without credentials:

```sh
go test -race ./plugins/provider/minimax -count=1
```

Follow [the testing guide](testing.md) for disposable-service full CI and release
checks. Mock tests prove serialization and handling against documented fixtures;
they do not verify paid model access, external URLs or provider availability.
