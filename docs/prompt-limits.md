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

Ordinary models use a 10,000-code-point Gateway ceiling, or a lower applicable
native bound. For example, OpenAI speech uses 4,096 and OpenAI image requests
retain 10,000. A larger provider-advertised limit alone does not extend admission.
Only an explicit reviewed native `prompt_policy` may set a different ceiling, up
to 40,000. MiniMax H3 base-video regeneration declares 40,000; reviewed Kling 4
Flash/Omni operations declare 26,000 with an additional mixed-character budget.
Use the selected operation's current metadata rather than inferring a limit from
its provider or family name.

When `prompt_policy` supplies `ascii_budget` and `non_ascii_budget`, both limits
apply together: `ASCII / ascii_budget + non-ASCII / non_ascii_budget <= 1`.
ASCII means code points below 128, including punctuation and whitespace; every
other code point belongs to the non-ASCII count. Kling's reviewed 26,000/11,000
budget therefore allows 26,000 ASCII or 11,000 non-ASCII code points, or a weighted
mixture within that inequality, also subject to `max_prompt_characters`. These
are Gateway enforcement rules, not a vendor tokenizer or a language classifier.

Administrator-defined manual models bypass native profile/policy validation and
retain the 10,000 Gateway ceiling. No generation, quote or validation rule is
changed by publishing these capabilities. Lyrics, negative prompts, instructions,
JSON timeline segments and other separate parameter fields keep their own
existing contracts.

## Counting and route semantics

Count Unicode code points, not UTF-8 bytes, UTF-16 code units or visible grapheme
clusters. For example, `😀` is one code point and `e` followed by a combining acute
accent is two. A JavaScript counter can use `Array.from(prompt).length`.

Create, estimate and validation share Gateway admission, which trims surrounding
whitespace before counting and passes that trimmed prompt to native validation.
Consequently a raw JSON Schema `maxLength` cannot exactly describe admission:
outer whitespace does not consume the character budget. JSON generation bodies
are limited to 64 KiB total, and multipart scalar fields to 64 KiB each, so a
Unicode prompt within its character budget may still exceed a transport limit.
Multipart generation forms also retain their 32 MiB total limit.

Template publication separately checks the raw stored prompt against the
selected model's effective character ceiling, requires nonblank text with no
surrounding whitespace, and applies its grammar and other template constraints.
Legacy/native-syntax reference-slot inference has a separate 40,000-byte guard;
a 40,000-code-point model capability does not promise that every such template can publish. An editor
may count raw input conservatively; this capability does not instruct it to
rewrite or truncate the user's prompt.

An empty field may display `Prompt (10,000 characters max)` and a nonempty field
`Prompt (5,550 / 10,000 characters)` using the selected model's advertised value.
Those numbers are illustrative. Retain server-side validation: character limits
alone do not establish model access, media compatibility, token allowance or
upstream acceptance.

The runtime projection copies descriptors before adding effective limits. The
derived field does not enter external catalogue contract reconciliation, create
catalogue revision/fingerprint changes, alter pricing or modify saved connection
configuration.
