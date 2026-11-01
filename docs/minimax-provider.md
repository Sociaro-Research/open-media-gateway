# MiniMax image and video provider

MiniMax's direct international API uses `https://api.minimax.io` and a
pay-as-you-go API key. M Plan keys, regional MiniMax products and self-hosted H3
are separate products. Configure `api_key` and `base_url` through the encrypted
provider connection; optional environment aliases are `MINIMAX_API_KEY`,
`MINIMAX_BASE_URL` and `MINIMAX_MODEL`. Credentials never belong in source files.
`file_operation_price_usd` is an optional, explicit operator-confirmed USD/request
rate for managed file operations. Blank remains unknown; no official file fee or
free tariff is inferred. It is separate from the published generation prices.

The module definition is `plugins/provider/minimax/definition.go`. It is explicitly composed in `plugins/provider/registry.go` alongside the other
built-in providers. The host injects `core.ProviderConnectionScope` after resolving authorized
credentials. Protected operations reject a missing concrete connection ID,
provider owner or accepted revision; no client-supplied scope is accepted. This document records supported adapter contracts and validation boundaries.
Live entitlement and account access have not been tested.

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

The public catalog contains 33 media descriptors: 15 upstream image/video model
IDs, 16 callable Video Agent workflows and both video-regeneration methods.
Context-IR is outside the [media-only scope](media-scope.md). The three native
file/binding helpers remain internal input preparation, not public model choices.
They require an authorized media consumer through `workflow_model`. Owned-task
and inspected-source regeneration remain supported; their internal bindings,
input inspection, task status and artifact parsers are preserved.


Historical matrix rows may describe internal helper contracts. They do not add
public models or authorize execution. Model/connection entitlement remains
separate from offline protocol and browser fixtures.

| Model or operation | Implemented workflows and constraints | Price / unit | Contract and tests | Remaining limit |
| --- | --- | --- | --- | --- |
| MiniMax-H3 | Text; first frame; last frame; first/last frames; image/video/audio references. 768P/2K, integer 4–15s. | 768P: output $0.08/s, input video $0.08/s, excess image $0.04/image. 2K: $0.13/s, $0.13/s, $0.04/image. First 5 images free. | [H3 create](https://platform.minimax.io/docs/api-reference/video-generation-v2-create); `requests.go`, `catalog.go`; generation, multimodal, validation and pricing tests. | Live entitlement unverified. |
| MiniMax-H3-Highspeed | Same five workflows; 480P/768P, integer 4–15s; no 2K. | 480P: output/input video $0.04/s. 768P: $0.06/s. Excess images $0.03/image; first 5 free. | [H3 create](https://platform.minimax.io/docs/api-reference/video-generation-v2-create); same tests for this model. | Live entitlement unverified. |
| MiniMax-H3-Lite | Same five workflows; 480P/768P, integer 4–15s; reference workflow requires a concrete ratio. | 480P: output/input video $0.025/s. 768P: $0.04/s. Excess images $0.02/image; first 5 free. | [H3 create](https://platform.minimax.io/docs/api-reference/video-generation-v2-create); same tests, including adaptive/2K rejection. | Live entitlement unverified. |
| MiniMax-H3-Max | Same five workflows; 480P/768P, integer 5–15s; prompt expansion disabled/balanced/quality. | 480P: output $0.05/s, input video $0.0553/s. 768P: output $0.08/s, input video $0.143/s. Excess images $0.074/image; first 2 free. | [H3 create](https://platform.minimax.io/docs/api-reference/video-generation-v2-create); same tests, including 4s rejection and expansion mapping. | Live entitlement unverified. |
| MiniMax-Hailuo-2.3 | Text or first frame. 768P 6/10s; 1080P 6s. Prompt optimizer and fast pretreatment. | $0.28 / 768P 6s video; $0.56 / 768P 10s; $0.49 / 1080P 6s. | [Text](https://platform.minimax.io/docs/api-reference/video-generation-t2v), [image](https://platform.minimax.io/docs/api-reference/video-generation-i2v); generation and legacy polling tests. | Duration-conditioned refresh and representative-request host forwarding pass all official tariff variants through the registered manager. |
| MiniMax-Hailuo-2.3-Fast | First frame only. 768P 6/10s; 1080P 6s. Optional prompt, optimizer and fast pretreatment. | $0.19 / 768P 6s; $0.32 / 768P 10s; $0.33 / 1080P 6s. | [Image](https://platform.minimax.io/docs/api-reference/video-generation-i2v); generation, validation and legacy polling tests. | Same manager-level tariff coverage. |
| MiniMax-Hailuo-02 | Text, first frame, last frame, first/last frames. 768P 6/10s; 1080P 6s. 512P 6/10s only in first-frame workflow. | 768P/1080P as Hailuo 2.3; 512P $0.10 / 6s or $0.15 / 10s. | [Text](https://platform.minimax.io/docs/api-reference/video-generation-t2v), [image](https://platform.minimax.io/docs/api-reference/video-generation-i2v), [first/last](https://platform.minimax.io/docs/api-reference/video-generation-fl2v); all modes and boundary tests. | No 512P text or first/last claim. Same manager-level tariff coverage. |
| T2V-01-Director | Text-to-video, 720P 6s, camera commands in prompt, prompt optimizer. | Unknown. | [Text](https://platform.minimax.io/docs/api-reference/video-generation-t2v); generation contract test. | No invented tariff. |
| T2V-01 | Text-to-video, 720P 6s, prompt optimizer. | Unknown. | [Text](https://platform.minimax.io/docs/api-reference/video-generation-t2v); generation contract test. | No invented tariff. |
| I2V-01-Director | First-frame video, 720P 6s, optional prompt and optimizer. | Unknown. | [Image](https://platform.minimax.io/docs/api-reference/video-generation-i2v); generation contract test. | No invented tariff. |
| I2V-01-live | First-frame video, 720P 6s, optional prompt and optimizer. | Unknown. | [Image](https://platform.minimax.io/docs/api-reference/video-generation-i2v); generation contract test. | No invented tariff. |
| I2V-01 | First-frame video, 720P 6s, optional prompt and optimizer. | Unknown. | [Image](https://platform.minimax.io/docs/api-reference/video-generation-i2v); generation contract test. | No invented tariff. |
| S2V-01 | One character portrait, optional prompt and optimizer. No invented duration/resolution controls. | Unknown. | [Subject reference](https://platform.minimax.io/docs/api-reference/video-generation-s2v); generation contract test. | No invented tariff. |
| image-01 | Text-to-image or one character portrait. Eight ratios or paired 512–2048px dimensions divisible by 8. Seed and prompt optimizer. Batch size n=1–9; Gateway uses URL results. The native helper also decodes the official Base64 response. | $0.0035 / image. | [Text](https://platform.minimax.io/docs/api-reference/image-generation-t2i), [portrait](https://platform.minimax.io/docs/api-reference/image-generation-i2i); generation, all-count/all-format batch and partial-safety tests. | Single and multiple image results pass actual PostgreSQL, worker/local archival, reopened reads and authenticated public GET/HEAD in deterministic tests. |
| image-01-live | Portrait-to-image only; one portrait, eight ratios, seed and optimizer. Custom dimensions are not advertised because width/height are documented only for image-01. | Unknown. | [Portrait enum](https://platform.minimax.io/docs/api-reference/image-generation-i2i); generation and text-only rejection tests. | No invented T2I capability or inherited image-01 tariff. |
| Context-IR (retired from public generation) | `MiniMax-H3-Context-IR` uses common generation/query and returns an archived text artifact. Same H3 inputs, 4–15s target, no resolution/audio-output control. | $0.90 / million input tokens; $3.60 / million output tokens. | [Context contract](https://platform.minimax.io/docs/api-reference/video-generation-v2-h3-context-ir); actual PostgreSQL admission, token metering, reopened reads, content GET/HEAD and redaction pass. | Preflight quantity remains unknown. Explicit cost caps reject before dispatch; uncapped work retains the tariff. Live access untested. |
| H3 regeneration by source task | `MiniMax-H3-Regeneration` accepts one local `source_task` resource binding. `MiniMax-Bind-H3-Source` creates it from an authorized archived H3 task in the same connection. Requires succeeded 768P source; targets 2K; optional AIGC watermark. | Output $0.05/s, original input video $0.05/s, original images beyond 5 cost $0.025/image again. | [Regeneration](https://platform.minimax.io/docs/api-reference/video-generation-v2-regeneration); actual PostgreSQL binding, public generation and metering sequence passes. | Whitelist access and live availability untested. Source must remain queryable within seven days. No private handle is a public parameter. |
| H3 regeneration by base video | `MiniMax-H3-Regeneration-Video` submits original final prompt/materials plus exactly one inspected `base_video`: 24fps, audio, dimensions divisible by 32, area 589,824–1,032,192 pixels, 107–362 frames in steps of 17. Original last frame requires first frame. | Same recharge tariff; inspected source duration estimates output. | [Regeneration](https://platform.minimax.io/docs/api-reference/video-generation-v2-regeneration); protocol, pricing, ownership/evidence and boundary fixtures pass. | Reviewed 40,000-character admission and inspected local-video generation/archive/metering pass. Both local-video and file-binding base inputs pass public PostgreSQL generation/archive/metering. Live whitelist access is untested. |
| MiniMax-Upload-File | `file_resource` creation from one inspected local reference; image/video/audio subtypes. Native multipart upload uses the trusted server source opener. | Unknown unless explicitly confirmed by the operator. | [Upload](https://platform.minimax.io/docs/api-reference/file-management-upload); real PostgreSQL/public API upload, binding and H3 consumption sequence passes. | The shared inspector implements a bounded HEVC still/grid HEIC/HEIF subset; unsupported constructs reject. Direct, rotated and grid HEIF plus canonical WAV pass real public upload/reference/archive/PostgreSQL reopen/content tests after deterministic shared storage handling. Input deletion is undocumented and rejects with 400 before job reservation or HTTP, preserving its usable binding. Signed input capabilities expire after seven days. |
| MiniMax-Bind-Output-File | Bind an owner-authorized archived legacy output to an opaque local video file resource, reusable as an H3/Context reference. | Operator-confirmed file-operation rate; blank unknown. | [Retrieve](https://platform.minimax.io/docs/api-reference/file-management-retrieve), [delete](https://platform.minimax.io/docs/api-reference/file-management-delete); real PostgreSQL output binding passes. | Public output deletion and H3/Context reuse/archive/metering pass with codec proof inspected from the owner-authorized archive and persisted in the binding. S2V output binding explicitly permits unknown duration and persists nil. Reusing that file where inspected duration is required rejects rather than inventing a free input. |
| MiniMax-Bind-H3-Source | Bind an owner-authorized archived, native-audio H3 768P task as a regeneration source, retaining owner/connection/revision/expiry. | Operator-confirmed file-operation rate; blank unknown. | [Source-task regeneration](https://platform.minimax.io/docs/api-reference/video-generation-v2-regeneration); real PostgreSQL/public source binding and regeneration sequence passes. | Queryable source age at most seven days; subsequent regeneration requires vendor whitelist access. |

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
The UI coordinator confirmed representative MiniMax `text_list` controls in
Chromium and WebKit using the actual descriptor fixture. All native template
serializers are separately tested; this is not a browser test of every
undocumented placeholder combination.
Image-only templates have no prompt and therefore cannot currently satisfy the
shared Templates author's intentional mandatory-prompt contract; they remain
native generation operations and are excluded from Templates authoring. The shared Templates
authoring contract also requires the `references` workflow; frame-only legacy
models cannot currently be authored as Templates, although their frame workflows
are implemented through ordinary generation. H3 References, S2V portraits, both
image models and the prompt-bearing image/text Video Agent workflows pass the
actual Templates slot/admission/normalization contract tests. Standalone
regeneration passes reference-role and reviewed 40,000-character policy admission,
including inspected local and managed base files.

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
| Context-IR | `POST /v1/generations`, model `MiniMax-H3-Context-IR` | Archived text content GET/HEAD; rate-only tariff retained, unknown totals omitted |
| Source-task regeneration | Bind archived H3 source through `POST /v1/provider-resources`, then `POST /v1/videos` with local `options.resource_references` alias `source_task` | Normal archived generation; private provider handle stays server-side |

These are shared Gateway routes. Seven representative scenarios exercise them
through the actual decoder, task service, worker and local storage with mocked
provider HTTP, queue and repository. That evidence does not establish live access,
PostgreSQL restart durability or accounting-enabled admission for unknown totals.
An unknown tariff is never a free quote: accounting-enabled admission may reject
an operation whose total cannot be safely reserved. Video Agent, legacy models
without a published tariff, image-01-live and Context-IR retain explicit pricing
limitations rather than invented estimates.

## Audio and conditional controls

| Operation | Native audio/output | Audio reference input | Configurable audio switch |
| --- | --- | --- | --- |
| Four H3 generation models | Fixed native audio (`audio_always_on`) | Separate WAV/MP3 reference inputs | None; Audio Off rejects before HTTP, and no audio flag is sent upstream |
| Context-IR | Text output | Supported multimodal context | None; both boolean values reject |
| Regeneration | Follows the documented regeneration endpoint; no new audio-generation control is inferred | Original audio inputs must be reproduced; base video must contain audio | None; no toggle is advertised or forwarded |
| Legacy video / Video Agent | No extra output-audio capability is inferred from undocumented template behavior | Only documented inputs | None |

Override Sound remains Gateway post-processing. Its ID, source snapshot and
settings are never sent as MiniMax audio parameters. Dedicated tests inspect the
wire payload and verify that reference audio remains `audio_url` input.

Adaptive ratio is admitted for H3 frame modes, and for References on H3,
Highspeed and Max. Text requires a concrete ratio; Lite References also requires
a concrete ratio. Resolution/duration Cartesian tests enforce each H3 range,
Hailuo 1080P at six seconds only, and Hailuo-02 512P only in first-frame mode.
Those conditional limits are also described in model API metadata.

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
supported-format limits. The standalone base-video path requires inspected size
below the Gateway 200 MiB ceiling; MiniMax publishes no separate base-video size
maximum, so this is not a vendor allowance. Ordinary reference videos and native
file uploads retain their documented 50 MB cap.

Image portraits accept one JPEG/PNG below 10 MB. Legacy video portraits and
frames accept JPEG/PNG/WebP below 20 MB, short edge above 300px and ratio 0.4–2.5.
Image width/height and aspect ratio are mutually exclusive in admission so that
the adapter never silently sends a custom dimension the provider would ignore.

## Supporting API completeness matrix

| API | Adapter contract | Tests and boundaries |
| --- | --- | --- |
| [H3 query](https://platform.minimax.io/docs/api-reference/video-generation-v2-query) | `GetTask`, `QueryNativeTask`; queued/running/succeeded/failed/cancelled; verifies model, task ID and task type. | Every H3 family/state, malformed identities, outputs and usage. Cancelled maps to Gateway failure because its task-status contract has no cancellation state. |
| [H3 list](https://platform.minimax.io/docs/api-reference/video-generation-v2-list) | `ListTasks` and `ListTasksFiltered`, pagination, repeated owned task IDs and optional status/model/task-type filters. | Requires signed owned handles; rejects unrequested/duplicate IDs and responses that ignore the requested filters. |
| [H3 cancel/delete](https://platform.minimax.io/docs/api-reference/video-generation-v2-delete) | `CancelOrDeleteTask`; queued cancellation or terminal-record deletion; no automatic cleanup or speculative retry. | Signed owner/account gate and response-action validation. Provider rejects running-task cancellation. |
| [Legacy query](https://platform.minimax.io/docs/api-reference/video-generation-query) | Preparing/Queueing → queued; Processing → running; Success → succeeded; Fail → failed. | All states and identity checks. |
| [Legacy result](https://platform.minimax.io/docs/api-reference/video-generation-download) | Owned successful task → retrieve file → temporary download URL for Gateway archival. | Numeric IDs retained as strings; authenticated file metadata, valid HTTPS output. |
| [File upload](https://platform.minimax.io/docs/api-reference/file-management-upload) | `UploadFile`, purpose video_generation_input, bounded multipart bytes, exact extension/type checks. | File lifecycle, multipart body, IDs above JavaScript's safe integer limit, foreign owner rejection. |
| [File list](https://platform.minimax.io/docs/api-reference/file-management-list) | `ListFiles` filters upstream inventory against owned handles. | Unowned files are excluded; no public account-wide enumeration. |
| [File retrieve](https://platform.minimax.io/docs/api-reference/file-management-retrieve) | `GetFile`, owner-bound handle, matching purpose/ID. | Foreign reads and malformed/raw handles rejected before HTTP. |
| [File content](https://platform.minimax.io/docs/api-reference/file-management-retrieve-content) | `OpenArtifact` streams protected bytes; `DownloadFile` is a bounded native convenience. Both enforce byte ceilings, status and media signatures. The source schema labels binary as application/json; actual media bytes are sniffed and JSON error bodies rejected. | Foreign owner, JSON-as-media and invalid limit rejection. |
| [File delete](https://platform.minimax.io/docs/api-reference/file-management-delete) | `GetTaskFile` derives an output-file handle only from an owned successful legacy task; `DeleteFile` accepts that documented video_generation purpose. | Input deletion is rejected: video_generation_input is absent from the official delete enum. It is not silently replaced by another purpose. Input files expire after 7 days. |
| [Models list](https://platform.minimax.io/docs/api-reference/models/openai/list-models) | `CheckCredentials`, success cached 5 minutes, marked non-generating request. | Concurrent check caching; malformed schema and auth/access failures remain unverified. |

The query/download/upload/retrieve/content and owned-file deletion operations above
are required parts of generation, archival and managed resources, and run through
the existing Gateway workflow paths. Credential verification is non-generating.
Native H3 task-list and record cancel/delete helpers are bounded internal
capabilities; the Gateway does not mirror account-wide provider history or expose
raw-ID administration. Public history, status and result deletion use the existing
owner-local Gateway contracts. Local cancellation does not promise an upstream
abort. File listing likewise uses locally owned resource bindings; it is not a
public proxy for the provider account's inventory. These are architectural
exclusions from public routing, not missing image/video generation modes.

Managed resource clients submit local reference IDs or generated task IDs, never
remote file/task IDs or arbitrary source URLs. The host stages and checks source
hash, MIME and byte length before a reader reaches the adapter. Public resource
views and generation options expose local binding IDs only. H3 References and
Context translate those bindings to private `mm_file://` values inside MiniMax.
First-frame, last-frame and first/last-frame cardinality is enforced across
local and managed image positions. All three managed-frame workflows pass
actual PostgreSQL admission, dispatch and archival tests.

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
usage is priced separately; no undocumented token estimator is invented. A
`quantity_unknown` quote retains the verified tariff while omitting expected USD
and estimated-unit fields. An explicit cost cap cannot be guaranteed and is
rejected before dispatch. Final provider token counters meter the accepted rate.

Inspected H3 video allowlists use MP4 codec sample-entry IDs (`avc1`, `avc3`,
`hvc1`, `hev1`) for the documented H.264/H.265 formats. H3 per-file byte ceilings
are inclusive: 30 MB image, 50 MB video and 15 MB audio. Legacy/Video Agent images
remain strictly below 20 MB, and image-01 references strictly below 10 MB;
metadata, local admission and native dispatch share those boundaries. The encoded
JSON body is limited to 64,000,000 bytes. Embedded reference-video audio must be
AAC/MP3 according to the vendor contract. Inspected local and managed reference
videos now require a known stream count and matching, positively proved codec
evidence (`allowed_audio_codecs`); verified silence is permitted. Unknown,
unsupported or inconsistent evidence rejects before submission. Proof is cloned
from accepted byte inspection and retained on resource reopen; native metadata
cannot replace it. The shared inspector uses bounded decoder/sample evidence,
not audio decoding: unsupported encrypted, external or fragmented constructions
remain unknown. These constraints apply to H3, Context-IR and regeneration
original references; the separate base-video role keeps its documented audio-track
requirement without an invented codec constraint. Externally hosted URLs that the
Gateway has not byte-inspected remain subject to provider validation.

## Evidence levels

“Verified documentation” below means reviewed official API contracts, not a paid
access test. “Common contract” means local Go admission/adapter fixtures; it does
not establish a production Gateway HTTP path or browser acceptance.

| Area | Verified official support | Local deterministic checks | Gateway end-to-end | Live account |
| --- | --- | --- | --- | --- |
| Four H3 generation models / five modes each | Current enums, native audio, limits, roles | All model/mode serializers; five states; audio/range matrix; Templates references | Representative H3 URL, uploaded-file References and all three managed-frame modes pass PostgreSQL/worker/archive. AAC/MP3/silent inspected references pass direct and file-bound flows; generated-output reuse passes with archived-byte codec proof retained by the shared binding path. Other exact model/modes have local fixtures | Not tested |
| Nine legacy video models | Endpoint enums; retained T2V resolution discrepancy | Every model/five states and generation modes | Protected legacy download, output binding/deletion and unknown-duration S2V binding pass actual PostgreSQL/worker tests; subsequent H3/Context reuse passes with trusted archived-byte codec proof | Not tested |
| image-01 / image-01-live | T2I/I2I enums, dimensions/counts/encodings | All native counts/encodings; admission and partial safety | Completed single/batch PostgreSQL/archive/reopen/content pass; interruption before the first or between batch artifacts passes recovery; live model exact PG path unexecuted | Not tested |
| 16 Video Agent workflows | Template IDs, input kinds and ordered arrays | Every serializer/five states; ordered media/text arrays | Representative text workflow passes PostgreSQL/archive/reopen with accounting disabled because price is unknown; representative text-list browser controls pass the UI coordinator's actual-descriptor fixture; all16native template contracts are separately tested | Not tested |
| Context-IR | Context/query and exact token units | Role, state and quantity-unknown fixtures | Accounting-enabled PostgreSQL cap rejection, uncapped admission, 270 micro-USD metering, archive/reopen/content/redaction pass | Not tested |
| Source-task regeneration | Whitelist-gated owned H3 source and 2K target | Protected binding, recharge, states | Real local source binding/public regeneration/250,000 micro-USD metering passes; manager refresh preserves cost/version and actual metering including refreshed component and primary scalar prices | Not tested |
| Standalone base-video regeneration | Separate conforming-source method, 40,000-character text | Protocol and strict inspected media/role/frame/timeline boundaries | Reviewed policy plus local and file-bound inspected sources pass public admission/worker/archive/metering; refreshed file-bound tariff selection also passes | Not tested |
| Managed files and sources | Upload/query/content and output-only deletion | Trusted stream, fee unknown/confirmed, ownership/revision/expiry gates | PNG/video upload/binding, managed frames, output deletion, rejected-input preservation, source-task regeneration and nil-duration S2V binding pass; direct/rotated/grid HEIF, canonical WAV and proved AAC/MP3/silent video pass actual upload/reference/archive/reopen/content. Generated-output H3/Context reuse passes with archived-byte inspection retained through resource admission and capture | Not tested |
| Supporting native task management | H3 owned list/cancel/delete contracts | Signed scope, requested-ID, status/model/type filtering and broadened-response tests | Account-wide/provider-console history and deletion mirrors are intentionally excluded; Gateway owner-local history and lifecycle semantics remain authoritative | Not tested |
| Published prices | Complete official table/exact units | 11 priced workflow IDs/51 input-selector variants, exact units and preservation | All 51 tariff checks and 50 executable representative quotes pass the registered manager; source-only refresh uses a pure tariff hook with zero task HTTP. The additional owner-bound source refresh test passes; refreshed file-binding selectors also pass; immediate-output recovery passes both tested interruption points, with the shared receipt prerequisite committed | Invoice/account access not tested |

## Integration and validation boundaries

The native adapter is implemented independently of ongoing shared work. Official
H3, image-01, all 11 Hailuo duration/resolution rows, Context-IR token rates and
regeneration recharge rates are parsed with exact units, including normalized
ingestion and replay of the complete retained official pricing page.
A generic `Rate.Duration` selector keeps 6s/10s tariffs distinct. Canonical
`options.output_count` and image API `n` use the shared `MaxOutputs=9` metadata;
no second provider-specific image-count control is introduced. MiniMax
`CatalogRateRequest` prepares executable representative workflows, including
Hailuo-02 512P first-frame input. The host forwards that hook and the pure source-regeneration tariff checker.
All 51 selector variants pass registered-manager tariff validation; 50 use
executable representative requests. Source regeneration validates its metric
tariff without querying any user task. Additional owner-bound source quotation
and metering fixtures retain strict price/version assertions.

The strict managed-file selector, deterministic HEIF/WAV storage and synchronized
primary/component snapshot tests pass. The shared private accepted-result receipt
(GW-256) now recovers synchronous image batches after interruption before the
first artifact and between artifacts. Real PostgreSQL/Redis restart tests verify
one native submission, both stable artifact IDs and distinct bytes, exact usage,
authenticated content retrieval and public redaction. The synchronous image API
has no task-poll endpoint; recovery uses the saved receipt. The shared bounded
receipt and cancellation implementation is committed in the shared prerequisite.
Generated-video resource admission inspects owner-authorized archived bytes,
checks exact size, MIME and SHA256, and persists a deep-cloned `Inspection`
through resource capture. Both H3 and Context-IR output reuse pass without native
metadata inference. Native duration remains distinct from the observed container
timeline; S2V retains a nil native duration. Uploaded local references retain
proved AAC/MP3 evidence and pass direct/file-bound workflows. Native account-wide
history and
provider-console deletion mirrors are architectural exclusions, not generation
workflows. Gateway owner-local history and lifecycle semantics remain unchanged.
The UI coordinator confirmed nine bounded acceptance groups in Chromium and
WebKit using the actual exported MiniMax catalogue: H3 direct/managed controls,
regeneration, image count 1–9 and all artifacts, Context-IR text, resources and
text lists, including themed responsive visual checks. Output/action responses
were mocked; this does not verify live provider entitlement or every Cartesian
model/mode combination. The complete affected local race/PostgreSQL/Redis suite
passes. Final composed CI and review still belong to their exact integrated
revision; earlier failed global runs are not a current pass.

Run deterministic adapter checks without credentials:

```sh
go test -race ./plugins/provider/minimax -count=1
```

Follow [the testing guide](testing.md) for disposable-service full CI and release
checks. Mock tests prove serialization and handling against documented fixtures;
they do not verify paid model access, external URLs or provider availability.
