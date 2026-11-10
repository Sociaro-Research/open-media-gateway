# Synthetic PixVerse V6 contract fixtures

`contracts.json` is derived from the preserved GW-335 offline preparation corpus.
It contains 119 valid payloads, 113 negative inputs, 32 explicit per-model billing
cases, 32 HTTP transcript specifications, five media-evidence specimens and eight
connection-guard specimens. All media URLs use reserved `.invalid` placeholders.
They are synthetic inputs and responses, not captures of a paid provider call.

The derivative corrects the overlong R2V prompt specimen to retain its named
bindings at 2,049 code points, isolating the intended length violation. The
original preparation corpus is preserved unchanged; its SHA-256 and the
derivation adjustment are recorded in the JSON provenance.

The minimum-image discrepancy is deliberately unresolved: release notes say
2–7 while the detailed API only states a maximum of seven. One-image R2V is
blocked by a conservative Gateway policy, not labeled an official API minimum.
Test account attestations are simulated and prove no real activation.

The adapter transcript tests exercise HTTP request/response legs only.
The worker tests exercise actual worker recovery/storage behavior. A regular
archive error remains terminal under the existing policy; archive cancellation
can resume after restart. Neither outcome permits another native generation.

See [the provider notes](../../../../../docs/alibaba-pixverse.md) for authoritative
source links, exact prices, account guards and the task/output lifetime distinction.
