# Organization funding

Organization administrators can buy USD balance using Stripe-hosted Checkout.
The Gateway never receives card numbers. Funds are paid to the merchant account
associated with the configured Stripe secret key. Bank payout timing and the
merchant's payout account remain configured in Stripe.

## Configure Stripe

In **Administration → Settings → Payments**, choose **Add provider → Stripe**.
Notification settings appear first; each provider's settings are shown in an
expanded card below them.

1. Enter a display name and a Stripe secret API key. Keep the provider disabled
   and save. The server verifies the merchant account and whether the key is live
   or test. Restricted keys need read access to Account, Balance, PaymentIntents,
   Charges/BalanceTransactions, Disputes and Checkout Sessions, plus Invoices read access and Customers and Checkout
   Sessions write access. A publishable key is not needed for hosted Checkout.
2. Copy the generated webhook endpoint into a Stripe webhook destination for
   **your account**. Use snapshot events with API version `2025-09-30.clover`.
3. Subscribe to:
   - `checkout.session.completed`
   - `checkout.session.async_payment_succeeded`
   - `checkout.session.async_payment_failed`
   - `checkout.session.expired`
   - `payment_intent.succeeded`
   - `charge.updated`
   - `charge.refunded`
   - `charge.dispute.created`, `charge.dispute.updated`, `charge.dispute.closed`
   - `charge.dispute.funds_withdrawn`, `charge.dispute.funds_reinstated`
   - `refund.created`, `refund.updated`, `refund.failed`
4. Paste the destination's `whsec_…` signing secret into the Gateway.
5. Choose **Processing fee paid by** and save. Then turn on **Enabled** in the
   card header; this switch saves immediately.
6. Verify the connection and make a Stripe test payment before enabling a live
   connection. The UI shows the last verified webhook time; connection checking
   alone does not verify webhook delivery.

The application needs `AUTH_PUBLIC_URL` set to its public HTTPS origin. Return
URLs and webhook URLs are derived from this trusted configuration, never the
request Host header. The existing provider credentials encryption key encrypts
payment secrets at rest. Keep that encryption key in deployment backups.

One connection per Stripe merchant account and mode is allowed. A saved
connection cannot switch accounts or test/live mode; add a separate connection.
Keys may be rotated for the same account and mode. During signing-secret rotation,
coordinate the Stripe destination change with the Gateway update and resend any
failed events. Disabling a Stripe connection stops new checkouts but preserves
credentials and reconciliation of existing payments, refunds and disputes.

## Fee modes

Both modes apply to the top-up principal before tax. The payer pays any applicable
VAT or sales tax in addition to that principal; tax never becomes spendable
credit. The examples below assume zero tax:

| Mode | Payment | Actual processing fee | Organization credit |
| --- | ---: | ---: | ---: |
| Service | $50.00 | $5.00 | $50.00 |
| Organization | $50.00 | $5.00 | $45.00 |

The fee is read from the original charge's Stripe balance transaction.
It is not calculated from a fixed percentage. Payments and organization balances
remain in USD, but Stripe may settle the merchant's funds in another currency.
For such payments, the USD fee is `charge USD cents × settlement fee / settlement
gross amount`, rounded to the nearest USD cent (half cents round up). Both
settlement values use the same minor units, so this also handles zero- and
three-decimal currencies. The ratio uses Stripe's actual settled gross amount,
including its rounding, rather than a current market rate. For example, a $5.00
charge settled as EUR 4.33 with a EUR 0.40 total fee has a USD 0.46 fee and credits
$4.54 in organization-paid mode. Stripe conversion fees included in the balance
transaction fee are included; payout settings are not changed by the Gateway.
Cross-currency settlement requires Stripe's exchange-rate evidence, and the
balance transaction must belong to the verified original charge. Missing fee data
is retried; inconsistent data is rejected. Stripe IC+ reporting is not supported.
The entry amount must be $1–$10,000 in
whole cents. Processing fees can arrive after payment confirmation; credit waits
for verified fee data. `charge.updated`, webhook retries, automatic reconciliation
and the history's Refresh action can finish this step.

The selected fee mode is stored on each payment when it is created. A settings
change affects future payments only. The checkout request includes the mode shown
in the form; a concurrent settings change returns a conflict before a new payment
is created. Balance history combines manual credits, payment credits and corrections in a
single paginated table. Each ledger change appears once; pending, failed and
zero-credit payment attempts remain visible without being counted as credits.
The table shows gross payment, actual fee, the fee payer's organization name (or
Sociaro), and the signed balance change. Technical IDs are omitted from the UI.
Dollar amounts use `$`; when Stripe settles in another currency, payment and fee
also show the actual settlement amounts in parentheses. These are Stripe merchant
settlement figures, not evidence of what the cardholder's bank debited. Existing
settled Stripe payments acquire this metadata on reconciliation after migration
037; this enrichment does not add another credit, financial revision or success
notification. Receipts remain available, while refresh/continue actions are shown
only for pending payments. Test payments are visible only to system administrators and
never produce spendable credits, regardless of mode.

## Payment lifecycle and accounting

1. The Gateway verifies current organization-administrator access, API-key scope
   and organization binding, then records an immutable payment request.
2. It creates hosted Checkout using a stable Stripe idempotency key derived from
   the payment ID. Retries use the same amount and reference. A browser timeout
   is not evidence that a payment failed; retry the same operation.
3. A signed webhook or a server-side refresh retrieves the current Checkout,
   PaymentIntent, charge, actual processing fee and dispute state from Stripe.
4. Merchant connection, test/live mode, organization metadata, amount, currency,
   successful capture and payment identity must match the stored request.
5. The current net credit is calculated and only its difference from the previous
   credit is appended to `organization_credits`, in the same transaction as the
   payment update. Existing generation accounting uses this ledger immediately.

A redirect is never proof of payment. Repeated and out-of-order webhooks are safe:
callbacks fetch current remote state under a database payment lock, and posting
uses a unique payment/revision pair. Failed validation or database work rolls back
without a partial credit. If a Stripe response is lost after Checkout creation,
its webhook can recover the session through the payment metadata. Automatic
reconciliation also retries the same Checkout creation within Stripe's
idempotency-retention safety window; it never recreates an uncertain order older
than 23 hours. Investigate such orders in Stripe rather than blindly recreating
an operation.

A gateway reconciliation loop uses database leases to coordinate replicas. It
checks pending payments about every minute and completed payments about every
six hours, in bounded batches. Errors are retried, and only payment IDs are logged.
Webhooks remain the primary low-latency settlement channel. Reconciliation keeps
running when a provider is disabled, so old charges remain accountable.

Refunds are initiated in the merchant Stripe Dashboard. Partial/full refunds and
funds held by disputes reduce the payment's net contribution; resolved, won
disputes restore eligible funds. The original processing fee is deducted once only
in organization-paid mode. The net contribution is clamped to zero so overlapping
refunds and disputes cannot debit a payment twice. This does not refund or pass
through additional dispute fees, bank fees or separately billed merchant fees.
A correction may make the organization's overall available balance negative if
it has already spent the credit; existing budget admission then blocks additional
shared-provider consumption. Previously accepted tasks retain existing behavior.

## Payment email notifications

In **Administration → Settings → Payment notifications**, configure two independent
lists: **Critical payment errors** and **Successful balance top-ups**. These lists
apply to all payment providers, including external integrations. Addresses must
belong to existing system administrators (up to 20 per list). An empty list disables
that notification. Settings changes are audited and use optimistic version checks.
Email uses the installation's existing delivery settings; no separate mail key is
required. Test-mode payments do not generate these notifications.

- A success is queued only when a live payment first credits the organization
  ledger. The credit and notification record commit in the same database
  transaction. Repeated webhooks and refreshes do not create more success emails.
- Verification mismatches and internal processing failures queue a critical alert
  immediately after detection. Provider connectivity failures have a two-minute
  grace period; missing settlement or fee data has a five-minute grace period.
  Recovery during the grace period cancels the incident. Each unresolved incident
  generates one alert per subscribed address. A new failure after recovery can
  generate a new alert. Routine bank declines, abandoned checkouts and rejected
  webhook signatures do not generate critical payment alerts.
- The notification worker checks its durable queue every five seconds and
  prioritizes critical messages. Delivery failures retry with backoff up to one
  hour; restarting the application does not discard queued messages. Disabling
  email leaves subscribed notifications queued. Unsubscribing or revoking a
  recipient's administrator role cancels queued deliveries at the next claim.
  A message already being sent cannot be recalled.
- Emails contain the provider, organization, internal payment ID, USD amounts,
  timestamp and a safe error category. They do not include credentials, card
  details, raw provider responses or raw database errors. Critical-alert amounts
  are the last committed Gateway state, not evidence of current provider funds.

The settings page shows pending/retrying counts and the last acceptance by the
email provider; acceptance is not proof of inbox delivery. The API is
`GET/PUT /v1/system/payment-notifications`; both require a system administrator
and `system:manage` scope for API keys.

A stable delivery key suppresses retries at Resend within its documented
[24-hour idempotency window](https://resend.com/docs/dashboard/emails/idempotency-keys).
If a send succeeds but the database acknowledgement is lost for longer than that
window, a duplicate email remains possible. Alerts also depend on a running
application, database and email provider: a complete outage needs independent
external monitoring. External payment modules must monitor failures before their
settlement request reaches the Gateway. No historical emails are generated merely
by adding a new subscriber.

## Independent integration API

The Stripe adapter is in `plugins/payment/stripe`; it has no database or balance
access. `internal/payments` defines the provider-neutral contracts and manager;
`internal/postgres/payments.go` owns durable records and atomic ledger posting.
The complete HTTP contract is in `docs/openapi.yaml`.

External modules can run outside the Gateway and submit confirmed payments over
HTTP. They must be explicitly trusted by a system administrator. An ordinary
organization administrator or consuming Gateway API key cannot issue credits.

Create an integration with a system administrator session or a system-management
API key:

```http
POST /v1/system/payment-integrations
Content-Type: application/json

{"name":"Bank transfer module","live":true}
```

The response returns `provider.id` and a `pay_…` token **once**. Store it in the
module's secret manager. Only its SHA-256 digest is stored in the Gateway. The
credential is scoped to this integration's settlements; it does not grant general
administrator access. It can fund any organization, so issue it only to a trusted
server module, never to a browser or customer. External integrations currently use
service-paid fees; changing Stripe fee settings has no effect on them.

Submit a provider-confirmed payment:

```http
POST /v1/payment-integrations/{provider_id}/settlements
Authorization: Bearer pay_REPLACE_WITH_INTEGRATION_TOKEN
Content-Type: application/json

{
  "organization_id": "22222222-2222-4222-8222-222222222222",
  "external_id": "bank-transfer-unique-reference",
  "amount_cents": 5000,
  "currency": "usd",
  "fee_cents": 500,
  "refunded_cents": 0,
  "held_cents": 0,
  "revision": 1
}
```

The integration must verify its payment source itself. `external_id` is unique
within the integration and cannot be rebound to another organization or amount.
Replays of the same revision and values return the same payment without another
credit. To report a refund, hold or release, increase `revision` and send the new
**cumulative totals**, not deltas. Stale revisions and conflicting values return
409. For example, revision 2 with `refunded_cents: 1000` changes this payment's
contribution from $50 to $40; retrying revision 2 does not subtract another $10.
The normal Gateway key and browser-session authentication paths are not accepted
at this endpoint. The endpoint requires HTTPS at the deployment edge.

Disable/revoke an integration through Administration or
`POST /v1/system/payment-providers/{id}/disable` with its current `version`.
Revocation is checked transactionally before each settlement. To rotate its token,
create a new integration and retire the old one after pending source transactions
have settled; references are scoped per integration, so do not replay old charges
under a new integration ID.

## Verification

- `make verify` and `make release-check` cover the repository baseline.
- With an isolated PostgreSQL in `GATEWAY_TEST_DATABASE_URL`, run:
  `go test -race ./internal/postgres -run 'TestPaymentLedger|TestExternalSettlement'`.
- `go test -race ./plugins/payment/stripe ./internal/api` covers Stripe response
  validation, HMAC checks and HTTP authorization.
- `node scripts/ui/payments.cjs` uses mocked APIs for fee modes, saved-secret masks,
  role visibility, repeat-submit idempotency and responsive layout.
- Before going live, perform a real Stripe **test-mode** Checkout, webhook delivery,
  fee reconciliation and refund in the configured merchant account. Automated
  fixtures do not establish connectivity to that account.

## Stripe references

- [Checkout fulfillment](https://docs.stripe.com/checkout/fulfillment)
- [Webhook signatures](https://docs.stripe.com/webhooks/signature)
- [Actual fees and delayed balance transactions](https://docs.stripe.com/expand/use-cases)
- [Balance transaction amounts and fees](https://docs.stripe.com/api/balance_transactions/object)

## Native Stripe invoices and automatic tax

When **Use Stripe Invoices** is enabled (the backward-compatible default), new Stripe top-ups use a required legal business name and billing address,
optional VAT/tax ID collection, automatic tax, and post-payment invoice creation.
The adapter pins API version `2025-09-30.clover` to support business name
collection. Existing webhook versions remain readable: webhook fields are used
only to find the payment, then authoritative objects are fetched with the pinned
version. Existing payments preserve their original tax-free Checkout request and
idempotency parameters, including retries after migration 038.

Before release, the configured restricted key needs **Customers read/write** and
**Invoices read** in addition to the original permissions. Do not reveal keys in
logs. The seller's business details, default invoice VAT IDs, actual tax
registrations, and default product tax category are maintained in Stripe; the
Gateway uses those existing settings. No registrations or seller tax IDs are
invented or changed by this integration. The merchant must classify prepaid
service credits correctly and configure registrations before collecting tax.
The product inherits Stripe's preset tax category; price tax behavior is
explicitly exclusive, independent of currency defaults. A zero tax calculation
can reflect reverse charge, exemption, or lack of registration; it does not
prove that registration settings are complete.

The customer pays tax in addition to principal. For a $50 top-up and a hypothetical
20% tax, Checkout charges $60; the ledger credits $50 in service-paid fee mode or
$50 minus the actual fee on $60 in organization-paid fee mode. Payment history
shows the gross charge, principal, tax, fee and net credit separately. Tax
reporting/remittance remains the merchant's responsibility or a separately
configured filing service. Stripe Tax and post-payment invoicing have their own
pricing; their service charges are not assumed to be card processing fees.

A provider-scoped Customer is stored per organization and reused across its
administrators. Stripe Checkout collects and stores the billing details on this
Customer; existing tax IDs may cause Stripe to omit the tax ID input. Changes to
an existing saved tax ID are managed in Stripe. Local payment billing snapshots
remain tied to the purchase rather than later Customer edits.

Paid sessions must have a completed tax calculation, unchanged principal, no
shipping/discounts, the expected organization Customer and matching metadata.
The session, PaymentIntent and charge must agree on gross amount and USD currency.
Legacy payments still require the original exact amount. Refunds and dispute
holds reduce principal proportionally to their gross amount, rounded up to the
nearest cent; full gross reversals remove all principal. The original fee remains
deducted in organization-paid mode. Merchant refunds and corresponding tax/invoice
corrections must follow Stripe's appropriate refund/credit-note workflow.

Funding commits before invoice retrieval. Invoice failures cannot undo credit.
The worker retries pending invoice documents every minute, including after a
restart; only Stripe-hosted validated URLs are exposed. Invoice documents must
match the payment, organization, Customer, currency and gross amount. Attaching a
PDF never creates another ledger entry, financial revision or success email.
Organization administrators can download the invoice from **Balance history** or
use **Refresh invoice** while it is being prepared. Older payments are not
retroactively invoiced or taxed.

Validation references:
- https://docs.stripe.com/tax/checkout/page
- https://docs.stripe.com/tax/checkout/tax-ids
- https://docs.stripe.com/receipts
- https://docs.stripe.com/changelog/clover/2025-09-30/checkout-name-collection

## Stablecoin balance top-ups

In **Settings → Payments**, choose **Add provider → Stablecoins**. Save the
receiving addresses and enable the card's switch. There is one stablecoin
configuration, with up to one receiving address per network and USDT/USDC selected
per network. Addresses may be Safe smart accounts; the settings link to
<https://app.safe.global/> and recommend activating the Safe on each accepted
network. No wallet private keys or transaction-signing permissions are requested.

The fixed catalog accepts Ethereum USDT/USDC, BNB Smart Chain USDT/USDC at their
Binance-Peg contracts, and Polygon USDT at its existing USDT0 contract plus native
Circle USDC. Users select only **USDT** or **USDC** and the network. Arbitrary
contracts, bridged USDC.e on Polygon and lookalike token symbols are not accepted.
The registry in `internal/payments/stablecoin.go` is authoritative; token contract
changes require a code review rather than an administrator-entered symbol.

### Seller company and VAT policy

Configure **Settings → Payments → Seller company and VAT** independently of
payment providers. Store the seller's legal name, registered address/country,
company registration number, VAT registration/number and OSS registration.
These are installation settings for the operator using the software; no company,
country, VAT number or rate is hardcoded or preset.

Add explicit VAT rules with a buyer country/region, buyer registration status,
treatment and rate. Scopes are a specific country, Seller's country, European
Union, Outside the EU or All countries. Treatments are Charge VAT, Reverse charge,
Exempt or Outside scope. Rates support two decimal places. Specific countries
win over domestic scope, regional scope and the all-country fallback; a specific
registration status wins over Any status within the same scope. Duplicate keys
are rejected. A missing rule blocks creation rather than assuming zero tax.
Reverse charge requires verified buyer registration; its geographic scope is
defined by the configured rule. A non-VAT-
registered seller cannot configure a positive collection rate. The configured
rules are the operator's tax policy, not a claim of automatic legal classification
of every jurisdiction or of the prepaid service itself.

The server calculates tax on principal locally with integer half-up cent
rounding, then adds the sub-cent payment fee. There is **no Stripe account,
Stripe Tax API, tax credential or third-party tax calculation dependency** in
stablecoin funding. The existing, separate card Checkout integration retains
its previous behavior; configuring this local policy does not edit the merchant's
Stripe account.

Organization managers choose Bank card or Stablecoins and request $1–$10,000 of
balance in whole cents. Company billing details are prefilled from the saved
organization profile. EU VAT numbers are checked against the official VIES SOAP
service; a returned business name must match the supplied legal name after
case/punctuation normalization. Unavailable/incomplete validation fails closed.
An invalid registration does not qualify for the verified-registration rule.

In an organization's **Billing → Buyer VAT verification**, an Administrator can
record documentary evidence for the exact legal name, country and full prefixed
VAT number, or revoke a previous verification. Ordinary organization managers
cannot self-verify. Manual confirmations expire after 90 days by default
(maximum 366 days through the API). This supports non-EU registrations and cases
where VIES is unavailable or cannot return usable company identity. A non-EU
registration without verification can match only an explicitly configured
Any-status rule; it never qualifies for registration-specific relief.

Every payment snapshots the policy version, seller identity, selected rule,
buyer country/status and verification evidence. Later settings/company changes
and idempotent retries do not recalculate an existing request.

The exact transfer amount is principal plus tax plus a **payment fee** of
1–9,999 USD millionths (less than one cent). The four digits after the two cent
digits distinguish requests; the complete token amount has six decimal places.
For example, a $50 principal, $10 tax and $0.001234 fee require exactly
**60.001234 USDC** and credit **$50**. The fee is persisted exactly as
`stablecoin.fee_micros` and the generated common `fee_micros` field; success
notifications preserve this precision. The existing integer `fee_cents` processor field remains
zero for these payments and must not be used to report the stablecoin fee.
Wallet/exchange withdrawal fees must be paid separately. One token unit is
priced at one USD for this funding method; there is no live exchange-rate feed.

After creation the form is replaced by a payment card with the network, token,
copyable exact amount/address, address-only QR, tax/fee breakdown and status.
Requests, including unpaid and cancelled attempts, appear in the Payments table.
The card can be reopened after leaving the page or disabling the method. Only
actual credits appear in Balance history. Stablecoin invoices use the local PDF issuer described below. Receipt generation remains a separate feature; no pending native Stripe invoice is shown for this method.

### Matching, finality and cancellation

The gateway checks wallets every minute via the fixed PublicNode HTTPS RPC
endpoints. Each RPC check verifies the chain ID. Scans use persisted block
cursors and at most 1,000 blocks per wallet/pass, catching up after downtime.
Public endpoint outages/rate limits delay verification without creating credit;
production availability depends on these external services.

Only canonical Transfer logs from the accepted contract, recipient and exact
amount can match. A transfer must be in a block after the request's creation
head. Confirmation requires both the RPC's finalized block and a conservative
additional depth (Ethereum 64, BNB Smart Chain 30, Polygon 128). Provisional
matches show Confirming payment and are re-read until confirmed; an orphaned
provisional transfer returns the request to Awaiting payment. Fixed finality
thresholds assume a truthful, synchronized RPC; independently validating a chain
or detecting a catastrophic reorganization behind the committed cursor is outside
this implementation.

`network + contract + recipient + exact amount` is permanently reserved across
all organizations, paid requests, cancellations and expired requests. There are 9,999 available
amounts per cent-denominated total and receiving tuple. Exhaustion returns
`payment_amount_exhausted` and asks for a different top-up amount. It never
recycles an old amount into another organization's request.

A payment can be Awaiting payment, Confirming payment, Paid, Cancelled or Expired.
**Delete payment** cancels a pending request while retaining its financial
history and amount reservation. Confirming/paid requests cannot be deleted.
Unpaid requests expire 30 days after creation. Disabling/changing wallet settings affects new
requests; previous receiving addresses are scanned only while they have an active,
unexpired request. Expired requests cannot be reactivated or match a late transfer.

Transfer identity is `(network, transaction hash, log index)`. Settlement links
that identity, changes payment status and adds exactly one principal credit in
one database transaction. Concurrent scanners, retries and multiple transfers in
a transaction cannot double-credit an organization. Cancelled, wrong-amount,
late and duplicate transfers remain in `stablecoin_transfers` without automatic
credit; an administrator must investigate them. There is no automatic refund or
manual-unmatched-transfer UI in this release.

### Stablecoin checks

- `TestStablecoinPaymentsExactOnceAndCancellation` exercises real isolated
  PostgreSQL reservations, concurrent retry creation, tax/fee accounting,
  cancellation, provisional reorganizations, RPC failure and one-time posting.
- `TestEVMReaderExactAmountsAndFinality` verifies RPC chain IDs, 18-decimal token
  conversion, bounded log filters, finality and rejection of another contract.
- `TestLocalVATRulesByCountryAndRegistration`, `TestVATCentRoundingAndConfiguration`
  and `TestVIESResponseValidation` cover country/status precedence, rate rounding,
  uncovered cases, identity checks and unavailable/incomplete registration checks.
- `TestVATSettingsAndVerificationAuthorization` covers isolated PostgreSQL
  configuration versions, administrator-only verification, revocation and audits.
- The stablecoin funding test uses no Stripe provider, adapter or credentials.
- `node scripts/ui/stablecoins.cjs` checks local component fixtures; set
  `UI_BROWSER=webkit` for WebKit. It covers settings, Safe guidance, exact fee,
  retry keys, keyboard selectors, hidden form, cancellation and narrow layouts.

These checks do not send funds or establish that an operator's configured VAT
policy is legally correct. Verify the seller's registrations/rules and a small
actual transfer before enabling production funding. Live VIES verification of a
customer's actual registration is not exercised by the fixture suites.

Registry/reference sources checked on 2026-10-17:

- [Circle USDC contracts](https://developers.circle.com/stablecoins/usdc-contract-addresses)
- [Tether supported protocols](https://tether.to/en/supported-protocols/)
- [Polygon's USDT0 upgrade](https://polygon.technology/blog/native-usdt0-comes-to-polygon-for-lower-fees-and-deeper-liquidity)
- [Official VIES VAT validation](https://europa.eu/youreurope/business/finance-and-tax/vat/check-vat-number-vies/index_en.htm)
- [EU place-of-taxation guidance](https://taxation-customs.ec.europa.eu/where-tax_en)
- [Ethereum JSON-RPC](https://ethereum.org/developers/docs/apis/json-rpc/)


## Provider-independent PDF invoices

The Stripe connection editor includes **Use Stripe Invoices**. Existing
connections retain `true` during migration 062; omitting the setting during an
update preserves its saved value. Each new payment records its `invoice_mode`.
A later switch changes future purchases only; retries and reconciliation keep
the original mode. Native Stripe invoices retain their existing Checkout flow.

When the switch is off, card payments collect company billing details in the
Gateway, quote VAT with the same local country/status rules used for stablecoins,
and send the exact gross USD amount to Stripe Checkout. Both
`automatic_tax[enabled]` and `invoice_creation[enabled]` are explicitly false.
No Stripe Customer, Tax or Invoice request is needed for this flow. The
[Checkout creation contract](https://docs.stripe.com/api/checkout/sessions/create)
provides those independent switches. Existing funding, processor-fee deduction,
refund and dispute rules remain unchanged.

**Seller company and VAT** includes a billing email and an independent
**Invoice logo** upload/preview/remove control, saved with the company section.
This logo never inherits the site's branding. PNG/JPEG files are limited to
512 KiB, 4096 pixels per side and 4 megapixels. The server checks decoded raster
content and re-encodes it as PNG, discarding metadata/trailing content. The
settings request is bounded at 1 MiB (including base64); other administration
endpoints keep their existing body limits. A new local payment snapshots the
seller, logo, buyer and VAT policy before checkout; subsequent edits cannot
change its invoice identity or tax basis.

An organization manager clicks **Invoice** beside a created payment or in its
stablecoin detail card/balance history. The authenticated
`GET /v1/organizations/{org}/payments/{id}/invoice` returns a private PDF attachment
(`Cache-Control: no-store`). The first download serializes under the payment
lock, allocates an installation-wide `INV-YYYY-NNNNNN` sequence number and stores
the issue date and PDF bytes. Concurrent requests and subsequent downloads return
the same document, including after settlement or changes to company settings.
Sequence gaps from failed issuance are allowed; numbers are never reused.
Cancelled stablecoin requests cannot issue/download a local invoice. Unpaid card
payments need an existing Checkout request before initial issuance. A completed
30-day request cannot issue a new invoice; an already issued PDF remains available
as a historical document.

The server renders A4 PDFs using the supplied invoice design: 18/20 mm margins,
32 pt title, aligned 28 mm labels, two party columns, right-aligned amounts and
payment details at the bottom. Embedded Liberation Sans fonts follow the sample’s Arial/Helvetica appearance and cover Latin, Greek and Cyrillic
without a runtime browser, system fonts or remote resources. Long party details
continue onto additional pages. The renderer uses
[fpdf's UTF-8 font and image APIs](https://pkg.go.dev/github.com/go-pdf/fpdf).

The service row is **Balance top-up for content generation**. Only stablecoin
invoices add **Payment processing fee**, the merchant's exact six-decimal technical
addition. Stripe's processor fee is not an invoice item or an extra customer
charge, regardless of who bears it in funding settings. VAT is shown separately
and the total matches the payment request. Stablecoin payment details specify one
token, network, address and exact amount, with no rounding and separate wallet
withdrawal costs. Unpaid card invoices contain a blue, underlined Stripe payment link
that opens the current Checkout within the payment request lifetime. Documents
first issued after settlement state **Paid**; previously
issued documents remain unchanged and are not payment receipts. Test invoices are
clearly marked. Legacy/native Stripe invoices do not get a second local invoice.


## Thirty-day payment request lifetime

Card and stablecoin requests store `expires_at = created_at + 720 hours`, exactly
30 days across timezone/DST changes. Every new local invoice prints that same
UTC deadline as **Due date**, including paid invoices; downloading it later does
not start another 30-day period. Stablecoin invoices also print a separate
**Exchange rate** row: `1 USD = 1 USDC` or `1 USD = 1 USDT`. This is the fixed
settlement rate used to create the payment request, not a live exchange quote.

The minute worker and authorized payment reads persist **Expired** for unpaid
pending/confirming requests at the deadline and clear stale checkout URLs.
Expired requests are excluded from card reconciliation and chain matching.
A wallet with no active request makes no RPC calls; the next request on that
wallet resumes at the current chain head, avoiding an inactive backlog. Old
exact amounts remain permanently reserved. An expired request never gains
spendable credit from a late observation; a transfer seen on a shared active
wallet remains unassigned. No automatic refund or reassignment is performed.
Paid/cancelled records retain their state, and paid card payments still reconcile
financial refunds/disputes. A user must create a new request after expiry.

A Stripe Checkout's technical expiry is separate from this business deadline.
[Stripe limits sessions to 30 minutes–24 hours](https://docs.stripe.com/api/checkout/sessions/create).
An expired short session leaves its request Awaiting payment while its 30-day
term remains. **Continue payment**/**Pay invoice** and the local PDF's durable
account link call the manager-authorized
`POST /v1/organizations/{org}/payments/{id}/checkout`. Native/local invoice mode
is preserved. The user signs in if needed; this is not a public bearer link.
A paid request is never charged again. Only a provider-confirmed expired session
can be replaced, preserving the request, invoice, VAT and principal. New Checkout
expiry is capped at the request deadline. When no valid session exists in the
last 31 minutes, creating another request is necessary because of Stripe's
minimum session window and a one-minute request margin.

Renewal reserves its attempt number and timestamp before the provider call.
Concurrent callers and lost responses reuse the same idempotency key/parameters.
If the response remains unknown beyond the ordinary retry window, the adapter
recovers the exact attempt from a bounded creation-window listing with trusted
metadata before permitting another attempt. Ambiguous/incomplete provider data
fails closed. Stale events from a superseded session cannot overwrite the new
one or add credit. After the 30-day deadline, resuming returns HTTP 410
`payment_expired`; expiration is never extended by opening or retrying a link.
