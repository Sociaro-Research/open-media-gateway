# Direct Kling integration

This native protocol stage registers **37** generation, analysis, solution and
resource-creation descriptors. Public editing initialization/selection actions,
their UI and native account-query rows remain separate integration work.

Contracts were reviewed on **2026-10-31** against the official
[API index](https://kling.ai/document-api/llms.txt), including its endpoint-specific
Markdown exports. The adapter uses `https://api-singapore.klingai.com` and a
current API key in the Bearer authorization header. Authentication, account
funding and model entitlement are separate: a successful connection check does
not prove that a generation is funded or permitted.

Add a Kling connection, select its reviewed catalogue, save the operator's API
key and enable availability. Environment bootstrap uses `KLING_API_KEY`,
`KLING_BASE_URL` and `KLING_MODEL`. Loading models or estimates does not submit a
generation. The explicit connection check uses the documented free account query;
it discards balances, caches successful authentication for five minutes and
limits fresh checks to one per second per process.

## Operation families

Gateway IDs identify reviewed serializers and exact upstream versions. They are
not arbitrary upstream `model_name` overrides. Public model IDs include the
concrete connection prefix, for example `kling/kling-4.0-omni-video`.

| Family | Gateway operations | Official contract |
| --- | --- | --- |
| Native video | Text/image operations for4.0,3.0,3.0Turbo,2.6,2.5Turbo | [Video capability map](https://kling.ai/document-api/guides/capability-map/video.md); each specific endpoint supplies its limits |
| Omni video | `kling-4.0-omni-video`, `kling-3.0-omni-video`, `kling-o1-omni-video` | [4Omni](https://kling.ai/document-api/api/video/4-0/video-omni.md), [3Omni](https://kling.ai/document-api/api/video/3-0-omni/video-omni.md), [O1](https://kling.ai/document-api/api/video/o1/video-omni.md) |
| Motion control | `kling-3.0-motion-control`, `kling-2.6-motion-control` | [Motion control](https://kling.ai/document-api/api/video/motion-control.md) |
| Images | Standard3/2.1, Omni3/O1, multi-image, outpainting, AI Multi-Shot | [Image capability map](https://kling.ai/document-api/guides/capability-map/image.md) |
| Audio and Avatar | Text-to-audio, video-to-audio, TTS, Avatar image-to-video | [TTS](https://kling.ai/document-api/api/video/avatar/text-to-speech.md), [Avatar](https://kling.ai/document-api/api/video/avatar.md) |
| Solutions | Effects, Apparel, Goods Studio, Video Commerce, Virtual Try-On | [Apparel](https://kling.ai/document-api/api/ecommerce-replication/apparel.md), [Goods Studio](https://kling.ai/document-api/api/ecommerce-replication/goods-studio.md), [Video Commerce](https://kling.ai/document-api/api/ecommerce-replication/video-commerce.md), [Virtual Try-On](https://kling.ai/document-api/api/ecommerce-replication/virtual-try-on.md) |
| Analysis and editing | Image recognition, face detection, lip-sync, multi-element editing | [Recognition](https://kling.ai/document-api/api/video/image-recognition.md), [Face detection](https://kling.ai/document-api/api/video/lip-sync/face-detection.md), [Lip-sync](https://kling.ai/document-api/api/video/lip-sync.md), [Editing](https://kling.ai/document-api/api/video/multi-elements.md) |
| Managed resources | Element/voice creation and owner/connection-bound local bindings | [Elements](https://kling.ai/document-api/api/video/4-0/elements.md), [Voices](https://kling.ai/document-api/api/video/4-0/voice-customization.md) |

Use the authorized `/v1/models` response for exact operation IDs, controls,
reference modes, resource schemas and price availability. Capability maps do not
override specific endpoint contracts:4.0's direct API currently exposes720p and
1080p. No undocumented Flash route or4K parameter is synthesized.

## Kling4 keyframes and Omni references

The ordinary `kling-4.0-image-to-video` operation accepts ordered image keyframes.
The separate Omni operation accepts image and video references. Kling's ability
to use video references must not be inferred from the ordinary keyframe route.
Templates apply their selected slots, fixed media and settings to each operation's
actual capabilities and validation; provider availability alone is insufficient.

4Omni supports image roles `first_frame`, `middle_frame`, `last_frame` and
`refer_image`, and video roles `feature_video` and `base_video`. Use the catalogue's
`reference_role_schemas` and `options.reference_roles`, keyed by `image_1`,
`video_1`, etc. Omitting inferred duration/aspect controls preserves native
prompt/base-video inference instead of eagerly sending unrelated defaults.

```json
{
  "model": "kling/kling-4.0-omni-video",
  "prompt": "Follow @video_1 and preserve @image_1",
  "seconds": "10",
  "size": "720p",
  "input_reference": ["https://media.example/subject.png"],
  "options": {
    "reference_mode": "references",
    "video_urls": ["https://media.example/reference.mp4"],
    "reference_roles": {"image_1": "refer_image", "video_1": "feature_video"}
  }
}
```

The endpoint permits10images,7Elements (at most3video-character Elements),
5videos including video-character Elements,3voices and15combined inputs. At
most one video is a base video. Video duration is3–30seconds per input and in
aggregate, including Element videos. Reusable video Elements need trusted stored
type/duration evidence; an arbitrary remote ID or client duration cannot satisfy
quotas or pricing. Direct voice references select managed voice IDs; arbitrary
background audio is not a documented voice reference.

The reviewed4Omni prompt policy uses ASCII/non-ASCII budgets26,000/11,000 and
the weighted sum≤1. This is Gateway's explicit classification; the upstream
contract does not define punctuation/emoji/combining-character classifications.
Ordinary4.0 text/keyframe prompts retain Gateway's conservative10,000-character
ceiling.3Omni and O1 have different prompt, video, keyframe, Element, audio and
multi-shot combinations; they do not inherit4Omni's schema.

Managed uploads retain Gateway's shared limits as well as model limits. In
particular, the current200MiB shared video-upload ceiling is below4Omni's
300MB endpoint allowance. Uninspected external video duration remains unknown;
Gateway does not fetch arbitrary remote references as a side effect of quoting.
A required input-duration estimate cannot treat unknown metadata as zero.

## Managed resources and face detection

`POST /v1/provider-resources/estimate` and `POST /v1/provider-resources` accept
local reference UUIDs or an eligible owned generation for element/voice creation.
The host inspects source bytes and constructs private native inputs for the
selected connection. Public bodies do not accept native resource IDs, source
URLs, hashes or duration assertions. Accepted jobs retain their connection and
tariff revisions; reusing a binding requires its owner and connection revision.
Completed creation jobs return local `resources[{binding_id,reference_id}]`.

```json
{
  "provider": "kling",
  "model": "kling-create-voice",
  "kind": "voice",
  "subtype": "custom",
  "name": "Voice sample",
  "reference_ids": ["00000000-0000-4000-8000-000000000001"]
}
```

Read bindings with `GET /v1/provider-resources?provider=CONNECTION` and
`GET /v1/provider-resources/{id}`. This inventory contains the owner's local
bindings. Element creation distinguishes a frontal image plus 1–3 distinct
reference images from a 3–8 second 1080p character video. Voice creation requires
one eligible 5–30 second source. Use the catalogue's resource variants for exact
source types, dimensions and limits. Unverified creation tariffs remain
unavailable until a positive account-specific tariff is explicitly configured.

`kling-lip-sync-face-detection` accepts its own inspected video contract and
returns typed JSON plus archived face previews. When source hash and duration
are known, the host can retain an owner/connection-bound `face_session` for one
hour; upstream expiry is undocumented. Uninspected external video can return
artifacts without creating a reusable binding. Public results use local 1-based
face indices. Preview selectors identify images archived by that same generation;
native face/session IDs and provider preview URLs stay private.

Lip-sync selects the local binding through `options.resource_references` and one
`options.parameters.face_index`. Crop/insertion controls are milliseconds; crops
must fit inspected source duration and overlap the face interval by at least
2 seconds. Both volume controls permit zero. Generated-TTS audio selection still
requires its separate owner-bound provenance workflow; native IDs are not a
shortcut.

Multi-element editing has a native generation serializer for addition
(1–2 cropped JPEG/PNG images), swap (exactly one) and removal (no image). Prompt
aliases use `<<<video_1>>>`/`<<<image_1>>>`. It requires a trusted editing-session
binding. Public editing initialization and selection add/delete/clear/preview
actions are **not included in this stage**. Their serializer helpers are retained
for the later host workflow; no public selection action endpoint or UI is
promised here.

`DELETE /v1/provider-resources/{id}` uses the documented element/voice deletion
operation. Active admitted uses and dependent elements protect retained bindings.
An asynchronous handle without a documented matching poll endpoint remains
pending/uncertain; submission alone is not proof of deletion. Public local
face/editing-session revocation actions belong to the later workflow.

## Pricing and outputs

Published USD Balance prices are synchronized from the separate official
[video](https://kling.ai/document-api/pricing/base/video.md),
[image](https://kling.ai/document-api/pricing/base/image.md),
[effects](https://kling.ai/document-api/pricing/solutions/effects.md) and
[ecommerce](https://kling.ai/document-api/pricing/solutions/ecommerce.md) sources.
Units, USD/CNY cash, list prices, discounts and actual invoices remain separate.
Never apply one universal Unit-to-USD conversion.

4Omni without video input costs output seconds×USD0.157at720p or0.235at1080p.
With reference/base/video-Element input, billed seconds are
`output + max(total_input, output)`, atUSD0.094/0.141 respectively. Missing Element
duration cannot be guessed.3Omni's resource tariff rules remain separate.
Images charge per output image; fixed series count overrides `n`, while automatic
series count is unknown in advance. AI Multi-Shot instead charges per call.

A verified tariff with unknown quantity is retained as `quantity_unknown` and
omits the total; it is not free. Such a request cannot satisfy an explicit cost
cap. Unknown tariffs stay unavailable. Unpublished Element costs,
lip-sync partial-block rounding and Avatar/multi-edit mode-to-resolution mapping
are not invented. Optional positive operator-confirmed USD/request fields can
supply explicit account tariffs for these named operations; they are labeled
account-specific and never replaced by a public-source refresh. Effects uses the
optional `effect_prices` (`KLING_EFFECT_PRICES`) JSON object mapping exact
catalogue `effect_scene` keys to positive decimal USD/request strings. Unknown
scene keys, duplicates and malformed prices are rejected. Leaving a scene
unpriced keeps it unavailable; no template-dependent fee is guessed. Existing
`kling4_720p_price`/`kling4_1080p_price` USD/second overrides remain supported.

All normalized outputs are archived before success: multiple images/audio,
recognition JSON, face/segmentation previews and watermarked variants. Read
`artifacts[{id,media_type,content_url,content_type,size_bytes,sha256}]` from the
normal generation response. `/v1/generations/{id}/media-access` issues signed
per-artifact preview/download links. Owner checks, expiry and deletion apply to
every artifact. Scalar primary-result routes remain compatible.

Credentials, provider task/resource IDs, inspected source proof and connection
revisions remain private in generation/report views and download errors.

See [the OpenAI-style API](openai-api.md), [Templates API](template-api.md) and
[testing guide](testing.md). Provider fixtures use synthetic inputs and mocked
HTTP. Local tests do not establish live model entitlement or paid output quality.
