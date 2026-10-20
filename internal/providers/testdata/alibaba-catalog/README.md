# Alibaba catalogue report regression

Reviewed on 2026-10-12. Public table facts are normalized from the official sources
below. These fixtures retain relevant headings, row/column spans, exact IDs,
prices, selectors and lifecycle dates; they are not general document copies.
The releases fixture retains only media candidates needed for this review.

`api-models.json` contains selected public model descriptors from authenticated,
read-only DashScope discovery. No API keys, account details or generation content
are included. `report-state.json` retains the original 207 warning identities
and the previous models/rates necessary to reproduce reconciliation; unrelated
observation bodies and redundant metadata have been removed.

The test must resolve all 207 supplied warnings, preserve reviewed scope decisions
and retirement records, and remain quiet on an unchanged repeat. Newly surfaced
file models with unreviewed token billing remain warnings with source evidence.
The complete downloaded-page replay can also be run with
`GATEWAY_ALIBABA_REVIEW_DIR=/path/to/snapshots`; ordinary CI uses these fixtures.

| Fixture | Official source | SHA-256 |
| --- | --- | --- |
| `pricing.html` | [Source](https://www.alibabacloud.com/help/en/model-studio/model-pricing) | `4e0779e74c94bee3cf6935960895df8d860dcb35dbd2813a50fb8abe2e01d541` |
| `releases.html` | [Source](https://www.alibabacloud.com/help/en/model-studio/newly-released-models) | `b107c8829b1fd11b4bb46c581e9483c4ace9f9231af98b18ca6e6bf4a3231426` |
| `image-capabilities.html` | [Source](https://www.alibabacloud.com/help/en/model-studio/image-model) | `fdb5d249c7f28534eb7863763deecdb745643f9c8ca47ae19adf7931e2e17f7c` |
| `retirements.html` | [Source](https://www.alibabacloud.com/en/notice/model_studio_notice_of_retirement_for_selected_legacy_models_7d9) | `8b37aef4724207c2c428ea04980d62c3dfad751799b0de94ae9a7b49ed9a1491` |
