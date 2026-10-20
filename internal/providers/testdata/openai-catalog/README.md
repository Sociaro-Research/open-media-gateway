# OpenAI catalogue report fixtures

Reviewed on 2026-10-12. These fixtures contain public model identifiers, factual
prices and normalized warning evidence. They contain no credentials or customer
generation content and do not make authenticated or paid requests.

- `report-state.json`: the 13 initial model records, confirmed tariffs and all
  20 warning identities from the user-supplied 2026-10-10T03:04:06Z report.
  Markdown-wrapped URLs in the pasted report remain historical evidence.
- `api-models.json`: the IDs in that report, represented as a minimal discovery
  response. This is a report replay, not a fresh account-access assertion.
- `pricing.html`: the two relevant Standard `GroupedPricingTable` component
  props extracted from the public [pricing page](https://developers.openai.com/api/docs/pricing).
  It preserves Astro serialization, tooltip-decorated column headings, token
  rates and explicit character-unit cells. Navigation and unrelated model prices
  were removed; the wrapper retains the explicit unit declaration.

SHA-256:

| Artifact | SHA-256 |
| --- | --- |
| `api-models.json` | `4ad71072bb90034923f14f725a0ea3dc0c358776ee90ac445ccdc6ad30e18a11` |
| `report-state.json` | `b19764ccd071c53b9d922530731543dff1f383f1c90ebbffe1b394bcf1a663d5` |
| `pricing.html` | `bbef3881cc10772449f2007aa63ce437df61262a54739caef9332e14049212a0` |
| Full downloaded pricing HTML, outside the repository | `e3a0e350f42d5e63193114fd3d3d1f93a3f6b482ed0bc64ba8952f56d1529e3a` |

`TestOpenAIReportWarningsResolveAfterCodeUpgrade` exercises the configured
provider connection wrapper, reconciliation, a quiet repeated check and changed
tariffs for every supported alias/snapshot. Set `GATEWAY_OPENAI_REVIEW_DIR` to a
directory containing the complete downloaded `pricing.html` to run the same
acceptance against the actual public page without fetching during tests.
