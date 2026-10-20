# Cost accounting

The Gateway estimates generation costs before accepting a task. The console calls
`POST /v1/generations/estimate` after a 400 ms pause in editing and again before confirmation.
The response includes the USD estimate, its tariff version, and the calling key's
remaining budget. Estimation is free and does not reserve funds.

Pass `cost.customer_expected_usd` as `max_expected_cost_usd` to `POST /v1/generations`. Admission
recalculates the quote and checks whether completed spending has exhausted the budget. A higher estimate
returns `409 estimate_changed`; insufficient funds return `402 budget_exceeded`.
An idempotent retry returns its original task without another task.

## Expected, metered, and actual

| Task field | Meaning |
| --- | --- |
| `cost.expected_usd` | Immutable estimate accepted before submission. |
| `cost.metered_usd` | Provider-reported units multiplied by the accepted tariff; provisional. |
| `cost.tariff_usd` | Completed usage priced with the matched provider bill’s unit price, before package coverage. |
| `cost.package_units` | Units covered by a provider package. This does not imply a daily or free allowance. |
| `cost.billing_match` | How the bill was linked to the task. |
| `cost.actual_usd` | Confirmed paid provider bill, or a confirmed free mock/failed generation. Absent until known. |
| `cost.pricing` | Accepted tariff, units, source, region, version, and review date. |
| `cost.invoice_ids` | Provider bill references supporting the confirmed charge. |

Amounts use integer microdollars internally and JSON numbers with six decimal
places. Per-task estimates round upward to the nearest microdollar. Account-level
free quotas, promotions, discounts, taxes, and resource packs are not inferred from
an account balance. Their effect is visible when a bill can be reconciled.

Tariffs are reviewed and versioned with releases, independently of the provider
model catalogue (cached for ten minutes). There is no automatic scraping of public
pricing pages. New models, unsupported regions, and reference-video configurations
without verified pricing return `422 pricing_unavailable`. Supported quote paths
currently include the existing priced Seedance video models without video input,
Seedream 4.0/4.5/5.0 Lite, Qwen Image 2/3 and selected editing models, Wan 3.0 and
Wan 2.6/2.7 text-to-video, and Qwen3 speech, subject to the exact model/region map in
`internal/providers/pricing.go`. Input-image fees are included where applicable.

## Organization balance and weekly member limits

Shared-provider generation consumes the organization's persistent USD balance. System administrators add credits and set maximum debt; organization administrators set member weekly limits. Weekly limits reset Monday at 00:00 UTC and span every key issued to the member. There are no monthly or per-key spending limits.

Admission checks only known completed gross costs against the organization balance plus Credit Limit and member weekly allowance. Estimates, running tasks and unknown costs do not reserve funds. The estimate may exceed the remaining amount. Parallel requests can exceed limits; accepted work always finishes. Provider packages do not restore allowance. Personally funded generation does not debit shared organization funds.

`GET /v1/api-keys/usage?month=YYYY-MM` provides reporting statistics only; selecting a month never resets or changes organization balance. `consumed_usd` is known completed generation cost; pending work consumes no allowance. `actual_usd` is the provider cash charge, visible only to authorized administrators.

Example: $0.51 remaining permits a $0.50 estimate. A final $0.55 charge is recorded,
leaving −$0.04. Further paid requests are blocked. Final charges are never truncated
to the estimate or limit. An uncertain provider timeout or a local storage failure
does not prove a refund, so its provisional charge remains until reconciliation.

## Account reports

**Configuration → Reports** shows date, API key, model, status, generation cost,
provider cash charge and settlement information. Filter by inclusive UTC dates
and API key, including revoked keys. `GET /v1/organizations/{org}/reports` exposes the same
paginated data and requires a Google session or `keys:manage`. Missing billing
data is shown as unknown, never as a zero charge. Package coverage is reported
only when confirmed; a bill does not necessarily identify a daily allowance.

The Generation journal shows only estimated cost, actual generation cost and
elapsed time. Here "Actual cost" means provider-reported usage priced at its
tariff (`tariff_usd`, falling back to `metered_usd`), before package coverage.

## Provider billing integration

Configure Billing Access Key ID and Billing Access Key Secret in **Providers**,
with read access to billing details. Generation API keys alone cannot read bills.
The worker imports current and previous month snapshots every five minutes using
Alibaba `DescribeInstanceBill` and BytePlus `ListBillDetail`. All pages must succeed.
Billing status is available in **API keys** and `GET /v1/api-keys/billing`.

Provider records are stored separately for audit. BytePlus responses are decoded
from `Result`. The worker discovers products from the monthly summary, then fetches
all detail pages separately for each product. This avoids omitted usage rows in the
unfiltered detail response. A truncated or changing snapshot is rejected.

Seedream and supported Seedance usage bills have model-service instance IDs, not generation request IDs.
The Gateway matches a bill only when its model, operation (when supplied), time
window (UTC+8 converted to UTC), and billable count identify one completed task.
Seedance 1.5 audio rows additionally require the matching audio mode. Their
`K tokens` counts and package deductions are normalized to individual tokens.
Unreviewed video bill formats remain pending.
Other credentials and uncertain requests under the owner participate in ambiguity
checks. Multiple possible tasks or bills remain unconfirmed; totals are not divided
between API keys. This correlation is exposed as `billing_match=model_usage_window`
and must not be treated as a provider-supplied request-level attribution. Usage
outside Gateway in the same provider account can prevent reliable attribution;
use a dedicated provider account for Gateway accounting.

For a matched bill, `tariff_usd` is `Price × Count`, `package_units` is
`DeductionCount`, and `actual_usd` is the settled USD payment. For example, one image
at $0.04 covered by a package has $0.04 consumption and $0 cash payment. The journal
shows one price, switching from Estimated cost to Cost once terminal usage is known; cash settlement belongs in Reports.
The original estimate is retained. Package coverage is not guessed from balance or
called a daily allowance. Revisions replace charges; an invalidated match restores
the provisional reservation. Gross consumption drives organization balance and member weekly usage; net provider charges do not.

Other provider bills still require an exact instance/task identifier match. Unpaid,
non-USD, or ambiguous bills remain pending. Alibaba JSON is decoded before the
generated SDK's lossy float32 money conversion. Historical tasks are not assigned
invented preflight quotes.

Sources: [Alibaba pricing](https://www.alibabacloud.com/help/en/model-studio/model-pricing),
[Alibaba instance bills](https://www.alibabacloud.com/help/en/user-center/developer-reference/api-bssopenapi-2017-12-14-describeinstancebill),
[BytePlus pricing](https://docs.byteplus.com/docs/ModelArk/1099320), and
[BytePlus bill details](https://docs.byteplus.com/en/docs/byteplus-platform/ListBillDetail).

## Seedance 1.5 precision audit (2026-09-09)

For a 4-second, 480p, square video with audio, the previous estimate used
`640 × 640 × 24 × 4 / 1024 = 38,400` tokens, or $0.09216 at $2.40/M tokens.
Inspection of the completed MP4 found 640×640 pixels, 24 fps, **97 frames** and
4.041667 seconds. The provider returned `completion_tokens=38800` and
`total_tokens=38800`. `640 × 640 × 97 / 1024 = 38,800`, priced at **$0.09312**.
The $0.00096 difference is exactly one frame, not money rounding.

The corresponding provider usage bill reports `Count=38.8`, `Unit=K tokens`,
`Price=0.002400`, `PriceUnit=K tokens` and `DeductionCount=38.8`. Its gross value
is also **$0.09312**, with $0 paid because the usage was covered by a package.
The token-derived amount and bill-derived amount agree exactly in this case.

New Seedance 1.5 estimates include one endpoint frame. This is an observed
model-specific correction, not a guarantee of future output dimensions or frame
counts. BytePlus explicitly describes its duration/pixel formula as an estimate
and identifies `usage.completion_tokens` as the actual consumption. Other models
retain their existing estimate until their output behavior is verified. Accepted
historical estimates remain unchanged so comparisons remain honest.

After completion, the Gateway uses `completion_tokens` and the accepted tariff,
without estimating tokens from the MP4 or adding `total_tokens` a second time.
Draft conversion factors apply only to estimated tokens. The returned billable
counter already reflects draft conversion, so its tariff must not be discounted
again. This fixes double discounting for newly accepted draft tasks; historical
tariff snapshots remain immutable.

USD uses six decimal places (0.0001 cent). Upward rounding adds less than one
microdollar per multiplication, below 0.001 cent; the example above requires no
rounding. This numerical precision does not guarantee an account-specific invoice:
unknown price changes, discounts or ambiguous aggregate bills still require
reconciliation. A known usage cost and an unconfirmed cash payment are separate
states; `pending_billing` does not mean the provider's token count is missing.

## Console cost labels

The journal shows a single price: **Estimated cost** while queued/running, and
**Cost** after completion when `cost.metered_usd` is available. If usage is missing,
the estimate remains explicitly labeled. Confirmed free mock or rejected requests
show Cost $0 after termination. Later invoices do not replace the journal's
usage-derived price, and the original estimate is retained in the API and Reports.
The progress bar fills on a newly observed completion, remains for one second,
and disappears. Historical completed tasks do not replay the animation.

Organization-admin and system-admin Reports show three separate columns:

| Label | API field | Meaning |
| --- | --- | --- |
| Estimated cost | `cost.expected_usd` | Accepted pre-generation estimate. |
| Cost | `cost.metered_usd` | Actual reported consumption multiplied by the accepted tariff. |
| Confirmed cost | `cost.tariff_usd` | Gross generation price calculated from the matched provider bill. |

Missing values display **Not yet known**; one stage never substitutes for another.
Known free requests display zero. Cash payments (`cost.actual_usd`) and package
coverage remain in Settlement, separate from Confirmed cost. Organization admins
see their organization's report. System admins see all organizations and provider
modes, with an Organization column. Ordinary members retain their own expense view
without provider settlement details.

## OpenAI multi-component pricing

Accepted pricing snapshots can now contain a `components` array, mapping a
normalized metric to a USD tariff and denominator. Every required metric must
be present before calculating a metered total. Single-unit legacy snapshots
continue through the existing path.

OpenAI image input text, input images and output images are priced separately.
Audio Chat distinguishes text input, text output and audio output. These are
standard list-price estimates, not confirmed invoice charges; binary TTS has no
provider usage response and remains estimated. See the explicit estimation
heuristics and limitations in [OpenAI provider](openai-provider.md#cost-semantics).


## Shared-provider markup

System administrators set `markup_percent` in system settings (default 0), and
can override it in organization settings. An omitted organization update preserves
its rate; JSON null restores inheritance; zero explicitly disables markup.
Organization administrators, members and client managers cannot modify these
fields. Rates allow 0–1000%, in increments of 0.01%.

Only tasks using shared provider credentials receive markup. Each new task stores
its effective rate transactionally at acceptance; historical tasks and idempotent
replays retain their original rate. Changing settings never reprices existing
tasks. Estimates expose `customer_expected_usd`; clients should use that value
for `max_expected_cost_usd`. An increase before acceptance returns 409.

Original `expected_usd`, `metered_usd`, `tariff_usd`, and `actual_usd` remain provider
accounting values. `customer_*` fields apply the stored markup. Completed
`consumed_usd` charges include it and drive organization balances, member weekly
allowances and key spending. Existing admission behavior is unchanged: in-flight
jobs do not reserve funds; new work is blocked when the balance/allowance is
already exhausted. Provider package coverage does not make an organization job
free: the existing gross-cost accounting basis is retained. Explicit free-provider
and failed-with-no-charge tasks consume zero. Unknown provider cost stays unknown.
Each customer charge is rounded up once to a millionth of a dollar. Reports and
CSV include the organization charge separately from provider settlement.

## Financial visibility

Only system administrators can read provider settlement and markup details. All other actors receive gateway prices through the shared customer projection, including estimates, tasks, model catalogues, key usage and report exports. Webhook delivery always uses the customer projection, including queued deliveries from earlier releases. Stored raw accounting remains unchanged.
