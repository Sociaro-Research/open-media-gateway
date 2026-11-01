# Prompt character-limit capability

Runtime model catalogues expose `max_prompt_characters` as an optional positive
integer on the model capability object:

| Endpoint | Field path |
| --- | --- |
| `GET /v1/providers` | `data[].models[].max_prompt_characters` |
| `GET /v1/models` | `data[].capabilities.max_prompt_characters` |
| `GET /v1/models/{id}` | `capabilities.max_prompt_characters` |
| Administrator template editor | `catalog[].models[].max_prompt_characters` |
| Published template details/runner | `catalog[].models[].max_prompt_characters` |

The field describes the **known enforced input ceiling of this Gateway and its
adapter**, not a claimed maximum of an external provider. Model identity, aliases
or provider display names are not a reason for a client to infer a different cap.
Missing metadata on an older server means unknown. A `no_prompt` model omits the
field because text entry does not apply; `prompt_optional` still has a ceiling.

The common gateway ceiling is 10,000 Unicode code points. Existing native checks
can make it lower: Kling 3 operations enforce 3,072, Sonilo prompt-accepting
operations enforce 2,000, and OpenAI speech operations enforce 4,096. OpenAI image
operations accept up to 32,000 in their native adapter validator, but Gateway
admission limits their effective input to 10,000. Kling 4 operations retain the
existing 10,000 Gateway ceiling. These values report existing code enforcement;
they do not introduce or claim new vendor documentation limits. Other models keep
the known Gateway ceiling even when a vendor-specific bound is unknown.

Administrator-defined manual models bypass native profile validation. Their
runtime metadata therefore reports the Gateway ceiling, rather than a lower
limit copied from the request profile. No generation, quote or validation rule is
changed by this capability. Lyrics, negative prompts, instructions, JSON timeline
segments and other separate parameter fields keep their own existing contracts.

## Counting and route semantics

Count Unicode code points, not UTF-8 bytes, UTF-16 code units or visible grapheme
clusters. For example, `😀` is one code point and `e` followed by a combining acute
accent is two. A JavaScript counter can use `Array.from(prompt).length`.

Create, estimate and validation share Gateway admission, which trims surrounding
whitespace before counting and passes that trimmed prompt to native validation.
Template publication additionally validates the raw stored prompt against the
same common 10,000 ceiling before validating the effective native request. An
editor may count its raw input conservatively; this capability does not instruct
it to rewrite or truncate the user's prompt. Existing request-body byte limits
remain separate transport limits.

An empty field may display `Prompt (10,000 characters max)` and a nonempty field
`Prompt (5,550 / 10,000 characters)` using the selected model's advertised value.
Those numbers are illustrative. Retain server-side validation: character limits
alone do not establish model access, media compatibility, token allowance or
upstream acceptance.

The runtime projection copies descriptors before adding effective limits. The
derived field does not enter external catalogue contract reconciliation, create
catalogue revision/fingerprint changes, alter pricing or modify saved connection
configuration.
