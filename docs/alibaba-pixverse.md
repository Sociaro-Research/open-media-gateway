# Alibaba PixVerse V6

The native adapter recognizes these exact Singapore model IDs independently:

| Model | Native input | Geometry | Prompt ceiling |
| --- | --- | --- | --- |
| `pixverse/pixverse-v6-t2v` | Text | `size` | 5,000 code points |
| `pixverse/pixverse-v6-it2v` | One `image_url`; optional prompt | `resolution` | 5,000 |
| `pixverse/pixverse-v6-kf2v` | One `first_frame` and one `last_frame` | `resolution` | 5,000 |
| `pixverse/pixverse-v6-r2v` | Ordered `image_url` references | `size` | 2,048 |

All four use integer durations from 1 through 15 seconds, optional seed
0–2,147,483,647, an audio toggle defaulting to false and an optional watermark.
Only T2V/I2V expose `options.parameters.shot_type` (`single` or `multi`).
The adapter rejects unsupported options and references before generation.

## Account and endpoint guard

PixVerse requires an existing, separately verified Singapore account activation,
a matching API key, and the exact workspace endpoint shape:

`https://{WorkspaceId}.ap-southeast-1.maas.aliyuncs.com/api/v1`

The account-review runtime setting defaults to empty. Only after an operator
has separately established the applicable authorization, account activation,
region and workspace may they attest that reviewed connection using
`DASHSCOPE_PIXVERSE_REVIEWED_CONNECTION` (runtime key
`pixverse_reviewed_connection`). Its value must match the existing Alibaba
`Definition.Identity` of that exact normalized endpoint and API key. A boolean
`true`, another workspace, a rotated key or a second account does not match.

This value is an operator attestation bound to a connection, not an automatic
activation check. It must come from a separately authorized account-review
workflow. The adapter never accepts a real attestation automatically from public
listings or offline tests. No live attestation or catalogue fingerprint is
adopted by this code change.

The adapter does not activate services, accept terms, generate credentials,
replace endpoints or migrate live settings. Unverified connections keep PixVerse
hidden from runtime model choices and reject native submission/quotes.
Already accepted tasks retain their native IDs and remain recoverable.

## References and prompt preservation

Image inputs support public HTTP(S) URLs for JPG, PNG and WEBP. Owned uploads are
checked using trusted byte-inspection evidence. Each dimension is limited to
10,000 pixels. Gateway conservatively caps these uploads at 20,000,000 bytes;
the API documentation states 20 MB without establishing a binary-byte convention.
External URLs remain provider-fetched; the adapter never claims to have inspected
their bytes or fetches arbitrary client URLs during quotation.

Use the connection-prefixed model ID from `GET /v1/models` in `POST /v1/videos`
or `POST /v1/generations/estimate`. Supply `seconds` as a string and `size` as
a catalogue tier, such as `720p`. Image references use `input_reference` (a URL,
file reference or ordered array); `image_urls` and `resolution` are internal
option names, not public request fields.

KF2V defaults to ordered first/last frames. Explicit `options.reference_roles` may assign
`image_1` and `image_2` to `first_frame`/`last_frame`; exactly one of each is
required, and the adapter preserves the supplied image order.

R2V optionally accepts `options.parameters.reference_names`, an ordered list matching
`input_reference`; an empty item leaves that image unnamed. Nonempty names are unique,
at most 30 code points, and must match prompt references with a following space.
The reviewed prompt policy preserves the original text, including trailing
reference separators. Unnamed image references are also supported.
Template publication retains its existing slot grammar and surrounding-whitespace
restriction described in [prompt limits](prompt-limits.md#counting-and-route-semantics).
The named-binding path is verified through the public API and Playground;
automatic Template name-to-slot inference remains outside this contract.

For example, an already reviewed builtin Alibaba connection accepts this JSON
intent (submitting it can incur provider charges):

```json
{
  "model": "alibaba/pixverse/pixverse-v6-r2v",
  "prompt": "@person holds @guitar ",
  "input_reference": ["https://example.com/person.png", "https://example.com/guitar.png"],
  "seconds": "5",
  "size": "720p",
  "options": {
    "ratio": "16:9",
    "parameters": {"reference_names": ["person", "guitar"]}
  }
}
```

**Unresolved R2V minimum:** the release list says 2–7 images, while the detailed
API specifies only a maximum of seven. Gateway conservatively requires two;
this is a Gateway guard pending clarification, not a proven native API minimum.

## Pricing and asynchronous recovery

The standard [Singapore pricing table](https://www.alibabacloud.com/help/en/model-studio/model-pricing)
contains separate rows for each exact ID. Prices rechecked 10 November 2026:

| Output tier | Silent USD/second | Audio USD/second |
| --- | --- | --- |
| 360P | 0.023 | 0.032 |
| 540P | 0.032 | 0.040 |
| 720P | 0.040 | 0.054 |
| 1080P | 0.080 | 0.103 |

T2V/R2V dimensions use their exact published size tables, including non-Wan sizes.
I2V/KF2V can return dimensions outside those tables; the reported `SR`, audio and
successful output duration establish metering dimensions. Missing or inconsistent
usage preserves the finished video and raw usage while withholding unproved
metering. Accepted tariffs and provider invoice reconciliation remain separate.

All four POST to `/services/aigc/video-generation/video-synthesis` with
`X-DashScope-Async: enable`, then poll the original ID at `/tasks/{task_id}`.
The worker supplies accepted request context for native response validation while
persisting the original provider ID unchanged for recovery and invoice matching.
Task IDs are queryable for 24 hours; an `UNKNOWN` lookup does not establish zero
charge and does not authorize another generation. The documentation says output
links currently do not expire; Gateway still archives completed media promptly
through its normal storage path. No output-link expiry is inferred from task TTL.

## Contract sources and offline checks

- [T2V API](https://www.alibabacloud.com/help/en/model-studio/pixverse-text-to-video-api-reference)
- [I2V API](https://www.alibabacloud.com/help/en/model-studio/pixverse-image-to-video-api-reference)
- [KF2V API](https://www.alibabacloud.com/help/en/model-studio/pixverse-keyframe-to-video-api-reference)
- [R2V API](https://www.alibabacloud.com/help/en/model-studio/pixverse-reference-to-video-api-reference)
- [Official model releases](https://www.alibabacloud.com/help/en/model-studio/newly-released-models)

The synthetic corpus in `plugins/provider/alibaba/testdata/pixverse-v6/` exercises
payloads, prices, media limits and fake HTTP. Worker tests separately cover native
ID persistence, polling errors, restart, archive cancellation and ordinary terminal
archive failure. These fixtures never call a real provider. Running them does not
prove account activation, actual live generation, source adoption, release or
deployment. Follow [the testing guide](testing.md) for required repository checks.
