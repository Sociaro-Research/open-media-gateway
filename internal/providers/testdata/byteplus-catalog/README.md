# BytePlus catalogue regression fixtures

These reduced Markdown tables and warning records are replay inputs for
`TestBytePlusFirstReportWarningsResolveAfterCodeUpgrade` in
`internal/providers/byteplus_catalog_test.go`. They exercise model discovery,
pricing, releases/retirements, resolution of 21 historical warnings and a quiet
unchanged repeat. Removing them would remove regression coverage.

The test uses an intercepted GET-only model-discovery response and local files;
it makes no authenticated or paid provider request. `report-warnings.json`
retains model IDs, warning identities, factual rates/evidence and timestamps from
the supplied report dated 2026-10-10. It is a report replay, not fresh account or
catalogue-access evidence.

## Reference sources

The adapter registers these official source routes in
`plugins/provider/bytedance/catalog_sources.go`:

| Fixture | Registered source |
| --- | --- |
| `models.md` | [Models](https://docs.byteplus.com/en/docs/modelark/1330310) |
| `pricing.md` | [Pricing](https://docs.byteplus.com/en/docs/modelark/1544106) |
| `releases.md` | [Releases](https://docs.byteplus.com/en/docs/modelark/1159178) |
| `archive.md` | [Release archive](https://docs.byteplus.com/en/docs/modelark/2172655) |

The registered routes and links inside fixtures provide source context; the
original full-download hashes and exact acquisition record are not recorded in
this directory. The hashes below identify the retained test inputs only. Tests
must remain deterministic rather than replacing these inputs with a live fetch.
The optional downloaded-page replay uses `GATEWAY_BYTEPLUS_REVIEW_DIR`.

These upstream-derived fixtures are not evidence of a separate Apache-2.0 grant
for BytePlus documentation or trademarks. Preserve source attribution and review
the [applicable upstream terms](https://docs.byteplus.com/en/docs/legal/docs-terms-of-service)
before redistributing captured content.

## Retained input SHA-256

| Fixture | SHA-256 |
| --- | --- |
| `models.md` | `9ef7fe0d9e01579d3b8f0aa289b9b08d0bbc10a840fc7587fbb8a7a12310ac23` |
| `pricing.md` | `70bd475d920fb1c14ca3c581ebeaa8fb968ab74a318a285b310d2fccda54572f` |
| `releases.md` | `9207eb7489c0adf121b905ed23027d87d573306ba2f591632f9942cca696d012` |
| `archive.md` | `85d4454f32749ada6a4e521f793858ab415cf38d6a200e402d3f077f72f35dbc` |
| `report-warnings.json` | `23ca4a6522b1759cbb5952491f26f2684e780b245b6a59c5604bc4b586f1548b` |
