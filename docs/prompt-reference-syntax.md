# Playground and Templates prompt references

The shared prompt editor recognizes the same reference forms for every model.
It highlights references and previews their selected media while preserving the
literal prompt, including brackets, capitalization and other text. Nothing
translates a reference into a provider-specific spelling before transmission.

We recommend **`@Image1`** as the concise common UI notation. Images also accept
`Picture`; videos accept only `Video`, and audio accepts only `Audio`. Words are
case-insensitive. Reference indexes are positive whole numbers, starting at 1.

| Form | Image examples | Video/audio examples |
| --- | --- | --- |
| Balanced angle brackets | `<Image1>`, `<Image 1>`, `<Picture_1>` | `<Video1>`, `<Audio_1>` |
| Balanced square brackets | `[Image1]`, `[Image 1]`, `[Picture_1]` | `[Video1]`, `[Audio_1]` |
| Balanced braces | `{Image1}`, `{Image 1}`, `{Picture_1}` | `{Video1}`, `{Audio_1}` |
| At sign, without a space | `@Image1`, `@Image_1`, `@Picture1` | `@Video1`, `@Audio_1` |

Inside brackets, the word and index may be contiguous or separated by one space
or one underscore. The at-sign form permits contiguous or underscore-separated
indexes, **never a space**. Bare `Image 1`, `(Image1)`, `character1`, malformed or
mismatched wrappers and other aliases are not UI reference forms. Native names
such as `character1` remain ordinary prompt text and are still transmitted.

The selected reference mode controls supported media, counts and frame roles.
Missing files, invalid indexes and unsupported media produce a field message.
Reordering/removing files updates previews and validation. In Wan
`first_frame_references`, image reference numbering follows the separate first
frame; a per-asset voice is not a separately numbered Audio input.

Hover or tap a reference to preview it. With the caret inside a reference, press
Alt+Down; Escape closes the preview. Video/audio playback is explicit, without
autoplay. Ordinary API generation keeps its existing native validation; this UI
recognizer does not introduce an API prompt grammar or alter outgoing text.

## Informational provider recommendations

A compact hint appears below the selected-model prompt only when reviewed
`reference_prompt_recommendation` metadata exists for that model and mode.
`example` distinguishes an endpoint example from an `instruction` described by
the provider. A provider example does not establish an exclusive grammar.
The hint never controls parsing, forces compliance or rewrites a prompt.
Unconfirmed exact-model API contracts produce no invented advice. API capability and account
access remain separate from documentation evidence.

Examples of reviewed API advice include Seedance 2.5 `@Image1`/`@Video1`,
HappyHorse 1.1 `[Image 1]`, Agnes `<Picture 1>`/`<Audio 1>` and Kling 4 `@id`
with adapter-assigned `image_1` IDs. Wan 2.6's native `character1` numbering follows
the combined URL order; those names are ordinary text in this UI.

Sources are the exact provider endpoint API references recorded in each hint:
[BytePlus video tasks](https://ai.byteplus.com/ark/region:ap-southeast-1/docs/create-video-generation-task-api.md),
[HappyHorse](https://www.alibabacloud.com/help/en/model-studio/happyhorse-reference-to-video-api-reference),
[Wan 2.6](https://www.alibabacloud.com/help/en/model-studio/legacy-wan-reference-to-video-api-reference),
[Wan 2.7](https://www.alibabacloud.com/help/en/model-studio/wan-video-to-video-api-reference),
[Wan 3](https://www.alibabacloud.com/help/en/model-studio/wan3-video-generation-api-reference),
[Agnes](https://wiki.agnes-ai.com/en/docs/agnes-video-25),
[Agnes Flash](https://wiki.agnes-ai.com/en/docs/agnes-video-25-flash) and
[Kling 4](https://kling.ai/document-api/api/video/4-0/image-to-video).
Prompt guides and general model/marketing pages are not evidence for these hints.

## Templates and compatibility

New authoring uses the same UI forms for highlighting, previews and slot
inference. Models with unmarked native prose can use explicit ordered reference
counts. Repeated mentions share a stable typed slot; template publication still
requires contiguous reviewed bindings and the complete native media contract.
The prompt is never rewritten. Previously saved/published recipes retain their
stored grammar and source text; editing/reviewing a new draft does not mutate an
existing published revision. Older catalogues can omit recommendation metadata. Reviewed hints are attached
by exact native model/profile identity. Catalogue interpretation can additionally
extract literal mention examples from fenced API request examples bound to one
exact `model` value on the reviewed endpoint. It does not reconstruct normative
rules from isolated words or from another model's example. These optional
observations retain normal snapshot/conflict history; omission or source failure
preserves last-confirmed advice without changing admission or mandatory
operational completeness.

See [Templates](templates.md), [reference storage](reference-library.md) and
[UI tests](../scripts/ui/README.md) for ownership, persistence and local fixtures.
