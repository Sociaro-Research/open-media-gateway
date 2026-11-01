# Organizations, credit and membership

Organizations own generation records, reference files and result files. Each organization key has an immutable recipient (`user_id`) and issuer (`created_by`). Requests, reports and weekly allowances use the recipient; issuing a key does not transfer the issuer's permissions. Personal keys have no organization. Roles apply within each organization, independently of the user's personal workspace.

GET and HEAD select read operations and ignore write-shaped request bodies;
settings, credits, checkout and membership changes require their registered write
methods and existing scope/role checks. The generation-only HEAD scope exception
is limited to the registered organization generation/report `content` and
`last-frame` aliases. Other organization HEAD endpoints retain their management
scope requirements. Every HEAD response omits the body, including errors, while
preserving media headers and the original method for direct-storage signing.

## Organization roles

| Function | Supermanager | Manager | Content Officer | Developer | Member |
| --- | --- | --- | --- | --- | --- |
| Organization settings and member limits | All | All except privileged member limits | None | None | None |
| Invite and remove participants | Anyone, except the last Supermanager | Developer and Member | None | None | None |
| Assign roles | Any role | Developer and Member | None | None | None |
| Issue and revoke keys | For everyone | Self, Developer and Member | None | Self and Member | None |
| Generate with an issued key | Yes | Yes | No | Yes | Yes |
| Statistics and spending | Entire organization | Entire organization | Entire organization | Own only | Own only |
| Request logs | All, with content | All, without content | All, with content | Own, with content | Own, with content |
| Delete generation results and input files | Any | None | None | Own only | Own only |

The API identifiers are `supermanager`, `admin` (Manager), `content_officer`, `developer` and `member`. An Administrator appoints the first Supermanager; existing Supermanagers may appoint more. Organization locking protects the last Supermanager against concurrent removal or demotion. Managers cannot change roles, limits or keys of Supermanagers or Content Officers, and cannot assign or remove Managers. They may change Managers' weekly limits without changing their role.

Content includes prompts, instructions, input materials, provider payloads and generated results. Manager logs use an allowlist of operational/accounting metadata, including status, model, author, key, usage and cost. Errors and raw provider usage are excluded because they can contain request content. Content Officers have read-only content access and cannot delete files or generate through previously issued keys. Live role checks apply on every authenticated request.

The Members table's Role assignment information button uses the shared information popup. Hover opens it; click or touch pins it; Escape or the close button dismisses it. The matrix scrolls horizontally on narrow screens. The Requests tab pages through retained history and supports browser-session access, including for Content Officers who have no API key. Organization-scoped generation detail, media-access, content, last-frame and reference endpoints apply the same policy as key-authenticated operations.

Full access covers retained data only. Minimized request logging removes sensitive request content after completion; it cannot be recovered by a Supermanager or Content Officer. Result files follow their independent storage/deletion lifecycle.

When creating an organization key, select an active recipient in the User dropdown. API requests accept an optional `user_id`, defaulting to the issuer for backward compatibility. The organization and recipient cannot be changed later. A Member cannot issue or revoke even their own keys. Removing a recipient revokes all their organization keys, regardless of issuer; history and accepted tasks remain. The secret is shown once to the authorized issuer. Anyone holding a bearer secret acts as its recipient: attribution identifies the assigned key account, not the physical person using it.

Migration 050 preserves established access: existing Managers become Supermanagers, existing Members become Developers, and existing keys record their owner as issuer. New invitations default to Member; the issuer may choose another role they can assign. Existing generations and expenses are not reassigned. Legacy unfinished uploads without author attribution can be completed by full-content roles; other users must start a new upload. Rollback requires a pre-migration database backup.

## Company generation projects

Projects belong to the company, not their creator or API key. All company keys
see the same project list; the recorded creator remains provenance only.
Supermanagers, Developers and Members can create, rename, delete and assign
company projects independently of generation authorship. Content Officers read
projects without modifying them; Managers retain metadata-only access and cannot
change projects. Existing role-filtered generation content, expenses and file
deletion remain unchanged. Company project deletion clears tags for everyone,
never generation records or files. Profile projects remain isolated and personal.

Migration 077 adds indexes only: every existing project ID, name, version and
assignment remains intact and becomes company-visible. Legacy duplicate names
remain separate; selectors distinguish them with a short project-ID suffix.
New same-name creates converge under a company/name transaction lock; a retry
chooses the oldest existing duplicate by creation time then ID. Stop old writers
before rollout: old binaries do not participate in this lock. Application
rollback retains schema and data; old UI shows the former creator-scoped view.
Do not down-migrate, merge duplicates or restore the database automatically.

## Funding and limits

Supermanagers and Managers can inspect their organization's **Audit Log**,
including safe change details and immutable revoked-key history. Internal
Administrator-only settings are hidden. Historical request labels survive
renaming and revocation; older records explicitly indicate missing snapshots.
See [Audit Log](audit-log.md) for coverage and access rules.

Amounts use USD with integer microdollar accounting. Only system administrators may add credits or change the organization's Credit Limit. A new organization has zero balance, zero allowed debt and a $0 default weekly limit for new members. A null weekly limit means unlimited. Weekly spending resets on Monday at 00:00 UTC; the organization balance never resets.

Each credit has its own immutable operation ID, amount, actor, type (`payment`, `promo`, `adjustment`), reason and timestamp. Negative amounts are allowed only for adjustments. Repeating the same `external_id` and payload returns the original credit; a changed payload returns 409. Correct mistakes with a new adjustment, not by editing history. API access requires both `organizations:manage` and `system:manage` for funding and credit-limit changes, and a system administrator account. Browser sessions use the same endpoints and role checks.

For shared-provider generation, available funds equal credits plus Credit Limit minus known completed generation costs. New requests are blocked only when existing available funds or weekly allowance are zero or negative. Estimates and pending tasks do not consume budget. Simultaneous requests may exceed limits; accepted tasks finish and save results. A member's weekly allowance spans all their keys. API keys have no separate financial limits. Provider packages do not restore allowance. Personally funded provider calls do not debit the organization balance.

## Invitations and access

Create a seven-day, single-use invitation in the organization’s Members tab. Choose **Invitation role** before selecting **Create invitation**. Administrators and Supermanagers may choose any organization role; Managers may choose Developer or Member. The role is fixed when the invitation is created and appears in its history and recipient preview. Omitted roles, including existing ordinary invitations, default to Member. The secret is shown once and stored only as a hash. The browser link carries it in the URL fragment, keeping it out of HTTP request paths. After Google sign-in the recipient previews the organization and explicitly accepts or declines. Preview and decline do not consume the link. Acceptance is atomic, assigns the invitation's saved role, and takes the organization's default weekly limit. The accept request cannot override that role. First-owner onboarding invitations remain recipient-bound and assign Supermanager; they are a separate flow. Role grants are recorded in the organization audit log. Opening or accepting an invitation as an existing active member returns `already_member: true` without consuming the invitation or changing membership, role, or limits. The console opens the organization directly, even when that invitation has since expired, been revoked, or been used. Used invitations cannot restore removed members or admit another user. Rejoining does not clear prior weekly spending or reactivate revoked API keys.

Supermanagers and Content Officers can inspect retained organization content. Managers can inspect metadata and organization statistics. Developers and Members can read and delete only their own organization inputs and result files, and see only their own financial records. Their organization-wide balances, credit histories and provider settlement fields are hidden. Organization webhooks also carry consumption costs without provider settlement details. Removing a member revokes their organization keys immediately for new requests. Files, historical expenses and accepted tasks stay with the organization. API keys inherit only the actor's actual membership role in each organization unless the credential also has `system:manage` and its owner is currently a system administrator. A personal key with `organizations:manage` alone cannot use its owner's system role to read or modify unrelated organizations, promote its membership role, or make system-only changes. Organization-bound keys remain restricted to their organization unless both system permission and the current system role allow global access. Browser sessions retain account-role authorization.

Shared-storage usage and the global storage allowance now apply per organization. New paths are `<prefix>/<organization-id>/references/...` and `<prefix>/<organization-id>/results/...`. Legacy personal S3 connections may still hold historical organization-owned files; the original connection remains attached to those objects for reading and deletion by authorized organization members. Storage configuration secrets are never disclosed through organization APIs.

## Migration

Migration 018 creates one isolated workspace for each existing account, with the same UUID as its former data owner. Existing API keys are assigned to it, and the original owner is recorded as author. Existing object paths and signed URLs keep working; files are not copied or merged. The account becomes that workspace's organization administrator.

Organization accounting starts at the organization's creation timestamp. Earlier generation records retain their costs in reports, but do not become a new unpaid debt. Existing monthly user allowances are not payments and are not converted into credits. Each migrated organization starts at $0 and requires a credit or system-admin credit limit for new paid shared generation. Migration 019 removes obsolete monthly key and user limits. Rolling back this ownership migration requires restoring a pre-migration database backup; an automatic down migration is intentionally refused. The old per-user and per-key budget endpoints are removed; use organization credits and weekly member limits instead.

## API examples

Create an organization using a system administrator's management key:

```sh
curl "$GATEWAY_URL/v1/organizations" -H "Authorization: Bearer $ADMIN_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Example team","admin_user":"owner@example.com"}'
```

Credit a confirmed payment (reuse the same operation ID on retries):

```sh
curl "$GATEWAY_URL/v1/organizations/$ORG_ID/credits" -H "Authorization: Bearer $ADMIN_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"amount_usd":100,"kind":"payment","reason":"Bank transfer","external_id":"transfer-123"}'
```

Create a generation key for a Member as a Developer, Manager or Supermanager:

```sh
curl "$GATEWAY_URL/v1/api-keys" -H "Authorization: Bearer $MANAGEMENT_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Application","organization_id":"YOUR_ORGANIZATION_UUID","user_id":"RECIPIENT_USER_UUID","resource_mode":"shared","scopes":["generation"]}'
```

Invitation acceptance also requires `organizations:manage` for API keys. Preview requires only authentication. All membership mutations require `organizations:manage` when using API keys, plus an appropriate organization role. Invites are previewed with `POST /v1/invitations/preview` and accepted with `POST /v1/invitations/accept`, both carrying `{"token":"..."}`. Organization reports use the existing `from`, `to`, `api_key_id`, `limit`, and `offset` filters on `GET /v1/organizations/{org}/reports`.

## Client managers

A client manager is a separate sales account role. System administrators assign or remove it in **Administration → Users**, then select the manager in an organization's **Settings → Client manager** form. A user must sign in once before a role can be assigned. Each organization has at most one client manager; a manager can be assigned to multiple organizations.

Managers can read assigned organizations' balances, credit histories, storage usage, member spending and generation reports, including tokens, estimated cost, usage-based cost, confirmed cost and provider settlement. They cannot generate, issue or manage API keys, access reference/result files, change membership or settings without the explicit commercial permissions below, invite additional members, or read unassigned organizations. The dedicated onboarding flow below allows creating a company and inviting its first owner. Assignment grants no organization membership. Existing memberships do not bypass these restrictions. Public documentation and sign-out remain available.

Changing a user to a client manager revokes existing API keys permanently. Remove any system or organization administrator roles first to avoid conflicting permissions or stranding an organization. Removing the client manager role clears all manager assignments and restores ordinary user access under any existing memberships; revoked keys remain revoked. Assignment changes and role changes are audited and checked on subsequent API requests.

System administrators can use the same APIs as the console:

- `PUT /v1/system/users/{id}/role` with `{"is_client_manager":true}` assigns the role; `false` removes it. Send exactly one role field per request.
- `PUT /v1/organizations/{org}/client-manager` with `{"user":"manager@example.com"}` assigns or replaces the manager. The user must already have the role. `{"user":""}` clears the assignment. An administrator API key needs `organizations:manage` and `system:manage`.
- A manager's Google session can read `/v1/organizations`, `/v1/organizations/{org}`, and that organization's `/members`, `/credits`, and `/reports`. Every request checks the current assignment. Managers cannot create API keys in this iteration.

Administration reports include an organization selector for system administrators. Organization administrators and client managers open reports within an accessible organization’s Accounting tab. Selecting an organization reloads its report immediately, preserves the date range, and resets pagination and key filters. Embedded organization reports stay scoped to that organization. Ordinary members continue to see only their own expenses.

The Users role dropdown applies changes through `PUT /v1/system/users/{id}/role` with `{"role":"user|system_admin|client_manager"}` (one value). Transitions are atomic; the last system administrator remains protected. Organization members expose `total_consumed_usd` for all-time known completed gross spending within that organization. No pending amounts are included.

Report filters accept `user` (ID or exact email) and `timezone` (IANA name; defaults to UTC). `from` and `to` cover complete calendar days in that zone, including daylight-saving changes. `format=csv` exports all matching rows, ignores UI pagination and uses UTC timestamps. The same authorization applies to JSON and CSV; members' financial detail remains restricted. Text cells are protected from spreadsheet formula interpretation.

## Organization file storage

Organization administrators choose **Gateway storage** or **Own S3 Compatible Storage** in the organization's **Management** tab. Gateway storage is the default, with the installation's free per-organization allowance. Own S3 storage stores new references and generation results in the organization's own bucket. File access follows the organization role and author rules in either mode.

Use `GET /v1/organizations/{id}/storage` to read settings and the Gateway allowance, `PUT` on the same endpoint to save `driver: gateway` or `driver: s3`, and `POST /v1/organizations/{id}/storage/check` to test a connection without saving. Browser sessions require an organization or system administrator; API keys additionally require `organizations:manage`. S3 credentials are encrypted and secrets are never returned.

The selection applies to keys using shared providers, including existing keys. Keys using personal providers instead use the authenticated user’s personal Storage settings. The personal Providers and Storage sections follow the same system policy. The legacy key `storage_mode` field does not override it. Switching storage does not migrate or delete existing files. Their original connections are retained for reading and deletion, so keep the old bucket and credentials valid. Already running generations can still save their results; a selection change never applies a new quota check during archival.


## Organization and personal workspaces

The header selector opens an organization or **Personal settings**, replacing the standalone Configuration navigation. Organizations contain Members (including invitations), API keys, Requests, Billing, Usage, Management and Settings according to the viewer's role. Billing combines balance history and generation reports. **Management** contains organization-admin controls: name, default weekly member limit, billing details (read-only) and file storage. **Settings** is visible only to system administrators and contains the credit limit, client manager, additional models, markups and request limits. System administrators can also access Management. Administration retains the cross-organization report. Personal settings contains Providers, Storage, API keys and Reports and follows the personal provider configuration policy; system administrators retain personal configuration access.

Create an organization key with `organization_id`; it uses shared providers and organization storage. Create a personal key without `organization_id`; it uses personal providers and personal storage, without membership or organization funds. An explicit `resource_mode` must agree with that choice. Personal references, generation results and reports use a separate data namespace and cannot be accessed through an organization key, including legacy organizations whose IDs match user IDs. Personal financial history is available at `GET /v1/admin/reports` (or CSV with `format=csv`).

Migration 23 detaches existing personal-provider keys from organizations. Historical organization files and task records remain in the original organization; the migration does not move files or rewrite financial history. New personal generations and references are private to their owner.

The console remembers a separate generation key for each selected workspace in the current browser tab for up to 24 hours, bound to the sign-in session. Switching workspaces clears the previous journal and reference selection. Pasting a key for a different available workspace requires confirming the workspace switch before the console uses it. Signing out clears all remembered keys.

## Client manager company list

Client managers open **Companies** from an assigned organization's heading.
The existing list adds customer spend for selected dates and latest accepted
generation activity, including free and pending requests. Shared date calendars
use inclusive dates in the browser's timezone. Sorting covers company name,
spend and balance. **No use in the last 30 days** uses the server reference time
and includes companies that have never used the service; activity is independent
of the spend period. Unknown charges remain explicit. Failed refreshes retain
previous values and their update time; access denial clears them. Returning
from a company retains the list filters.

`GET /v1/organizations?view=summary&from=YYYY-MM-DD&to=YYYY-MM-DD&timezone=UTC`
adds customer-only summaries in one authorized database query. Ordinary list
responses are unchanged. Developer/Member projections omit summaries, and
Client managers receive only currently assigned organizations; sales leaders also
receive organizations assigned to their reporting branch.

## Client manager company onboarding

**Companies → Create company** asks for company name, required company website, country,
contact name and first Supermanager email. The comment is optional. Creation
saves an organization without members, its provisional manager, one owner
invitation and a durable email delivery. Pending cards appear under **New companies**;
financial access and the actual client-manager assignment start only when the
invited owner accepts. The manager never becomes a customer member or key issuer.

The recipient signs in or registers through the existing account flow, using the
invited verified email, then explicitly accepts. Acceptance atomically creates
the first Supermanager membership, records activation once and attaches the
still-authorized provisional manager. The first owner has no individual weekly
allowance cap, so funding the company is sufficient to pass the budget check for
paid requests. Organization funds and credit remain the spending boundary; no
credits are granted by acceptance. The owner can use existing member and key
management. Ordinary 7-day Member invitations still inherit the organization's
default weekly allowance, including a zero default.

Owner links expire after 14 days, are single-use and are stored only as hashes.
Domain-separated HMAC with the session signing secret reconstructs links for
mail retries; plaintext links never enter the database or audit. Rotating the
signing secret can prevent delivery of already queued mail; manual resend
creates a new delivery/hash using the current secret. Sent links remain valid
against their stored hash. Removing a sales role clears provisional assignments;
reenabling it cannot restore them through an old invitation. The client may
still accept and become owner without that manager.

Status distinguishes sending, sent, send_failed, accepted, expired and revoked.
Sent means the email provider accepted the message, not proof of inbox delivery.
Resend renews the invitation; the old unrevoked link retains its original expiry
until the new email is sent successfully. Revoke invalidates all links without
deleting the company. Mail retries survive gateway restarts and use one stable
provider idempotency key and payload per delivery; automatic retry stops before
[Resend's 24-hour idempotency window](https://resend.com/docs/dashboard/emails/idempotency-keys)
expires. The UI then offers manual retry.
No real emails are sent by the regression fixtures.

Dedicated browser-session endpoints are `/v1/client-onboardings` and
`/v1/client-onboardings/{org}/{resend|revoke}`. Live role/scope checks, operation
UUIDs and versions prevent stale authority and duplicate requests. Manual
changes are limited to 20 distinct companies per manager over the rolling last
hour; mail delivery is excluded. Duplicate normalized name or website
blocks creation. Only the matched criterion and responsible manager are returned
for another manager's company; contact details and balances remain private.
Legal details, drafts without invitation, manager key issuance,
funding and CRM integration are separate work.

## Sales hierarchy

**Administration → Sales management** shows all sales employees beneath an
Administrator root: Regional client manager → Lead client manager → Client
manager. Every employee has one sales role and at most one immediate supervisor.
A supervisor must have a strictly higher role. Missing ranks are valid and shown
as vacant: a Client manager may report directly to a Regional client manager or
Administrator, and a Lead may report directly to Administrator. Vacant nodes
are visual placeholders, not extra employees or permissions.

Administrators drag an employee onto a supervisor to move that employee and
their branch. Dropping into a role slot opens a review dialog before changing
the role. Clicking an employee's avatar/name provides the same controls on touch screens
and with the keyboard. Existing clients remain assigned to that employee. Direct
reports incompatible with a lower role move to the nearest strictly higher-rank
supervisor in the selected destination chain, or Administrator. The dialog lists
the affected employees grouped by their resulting supervisor before saving.
Dropping into a role slot within the moving employee's own branch uses their
nearest eligible external supervisor. This supports promotion/demotion round trips
without proposing self-reporting. Cycles, self-reporting and
equal/lower-rank supervisors are rejected. Saves use a global version; stale
changes require automatic synchronization and a new review. Failed saves preserve the dialog draft.

The tree has separate drop targets for **Lead this team**, **New team** (lead)
and **New region** (regional). Filling a vacant level adopts lower-rank employees who
currently report directly to that vacancy's supervisor; an independent
placement leaves those employees in place. Starting an empty team/region moves
only the selected employee: their former direct reports stay with their old
supervisor (or Administrator), retaining their own nested teams. This applies
even when the employee's role and supervisor remain unchanged. Ordinary moves
retain compatible direct reports. This also works when an existing
lead already has the same role and supervisor. The review dialog lists the team
and uses a bottom-right **Apply**, a top-right close icon and unchanged/invalid
draft disabling. Its **Team** selector exposes the same choice for keyboard and
touch users. The API's optional `fill_vacancy` and `new_team` flags default to false.
`fill_vacancy` adopts unled peers; `new_team` releases the moving employee's
former direct reports to their previous supervisor. Both flags require a lead
or regional destination and cannot be combined. Report repairs, team changes
and the employee's placement share the same transaction, settings lock and
structure-version check. Client assignments are unchanged.

**Add employee** searches existing signed-in users and sets their role and
supervisor together. Organization owners/managers must first leave their
conflicting customer roles. Admission revokes existing personal API keys,
preserving the existing sales-account restriction. Existing Client managers
migrate without losing their clients and initially report to Administrator.
The Users role selector also supports all three sales roles. Removing a sales
role lifts its direct reports to the previous supervisor and clears that
employee's current and provisional client assignments.

All ranks retain the existing onboarding and read-only company statistics
capabilities. Leads see their own clients and every descendant's clients;
Regional client managers see their entire branch. The existing company screen
offers leaders a shared Client manager picker to transfer a client within that
branch, including to themselves. Leaders cannot claim unassigned clients,
transfer to another branch, issue keys or acquire customer management rights.
When a rank is vacant, the next actual supervisor receives this same branch
access. Statistics and authorization are checked against the current hierarchy
on every request. Administrator-only tree changes are audited and require a
browser session. The tree is company-wide, without departments or territories.


## Delegated commercial permissions

Sales roles remain unchanged. **Permissions** on an employee's Sales management
card and **Sales permissions** on the Companies list open the same scoped editor.
Ordinary managers can inspect their own permissions; leaders also see their
current descendants. Each grant shows its issuer and date.

Commercial permissions are off by default:

| Permission | Client action |
| --- | --- |
| `pricing` | Edit the existing company markup and provider/model exceptions. No separate discount calculation. |
| `models` | Edit the company's additional model grants. Globally enabled models remain available; global settings and provider credentials are inaccessible. |
| `topup` | Add a positive payment credit, with a mandatory reason and no amount ceiling. No promo credits, negative adjustments or credit-limit editing. |

Permissions apply only to current own clients and clients assigned to current
descendants. The company screen displays **Commercial settings** for pricing or
models and **Top up balance** in Billing when permitted. The server rechecks
permission and assignment inside each write transaction. Top-up `external_id`
retains existing idempotency; the UI reuses the operation ID after a failed or
lost response. No per-operation or cumulative volume limit is applied. Crediting a balance records confirmed money received; it does not collect
a payment from a bank or payment provider.

Administrators grant to any sales employee. Leads and regional managers grant
only permissions they themselves currently hold, only to descendants, and never
to themselves. Any ancestor can
revoke a descendant's permission, even without that permission themselves.
Missing ranks follow the actual supervisor chain.

Each grant links to its issuer's exact grant. Revoking **or replacing** a grant
permanently revokes permissions delegated from it. Regranting later cannot revive
old delegations. Moving outside an issuer's branch invalidates the affected
non-administrator grants and their dependents. Administrator-issued grants
survive branch moves. Removing a sales role or the issuing administrator's
administrator status invalidates dependent grants. No new access is inherited
automatically from a new supervisor.

Grant writes and hierarchy changes share optimistic versioning and the settings
transaction lock. Commercial writes acquire the same lock before authorization;
a concurrent revoke or client reassignment cannot slip between the authorization
check and mutation. Grants, revocations, markup/model changes and payment credits
are audited; payment credit audit entries include the reason. API keys cannot
use or delegate sales permissions. Existing administrator key capabilities are
unchanged.

New session endpoints: `GET /v1/sales/permissions`,
`PUT /v1/sales/permissions/{employee}/{permission}` and
`GET /v1/organizations/{org}/sales-permissions`. The existing organization markup,
model-access and credit endpoints perform commercial actions. The scoped markup
picker uses `GET /v1/organizations/{org}/markups/catalog`.

### Generation profit and sales bonuses

Migration 057 starts accounting prospectively (`sales_bonus_settings.starts_at`).
Requests created before activation are marked `not_tracked`; there is no backfill.
Only company requests using platform-owned providers participate. Personal and
company-owned provider requests are `not_applicable`, since their spending is not
platform revenue. All amounts are USD with six decimal places.

A terminal request recognizes profit as the actual customer charge (the existing
accepted markup and tariff calculation) minus provider cost. Deposits are never
profit. Billing availability is captured in the accepted quote: when both the
provider billing adapter and complete billing credentials exist, recognition waits
for a confirmed cost with invoice references. Removing credentials later cannot
turn a pending bill into an immediate accrual. Without billing credentials,
reported generation cost is used; an estimate alone cannot establish profit.
Explicitly free or provider-confirmed uncharged failures recognize zero.

Reports expose `generating`, `awaiting_billing`, `awaiting_cost`, `recognized`,
`not_tracked` or `not_applicable` independently of generation success/failure.
Only administrators and sales viewers within the existing client scope receive
profit amounts/statuses in report JSON and CSV. Usage totals and the profit chart
sum recognized amounts, including losses; pending counts remain visible. Periods
use the same request-creation dates and filters as existing Usage reporting.
Ordinary customers and webhooks do not receive platform profit or bonus data.
Billing sync continues to revisit older months with pending profit verification.

At first recognition, the current assigned manager, their immediate actual
supervisor and global percentages are snapshotted. Missing hierarchy levels are
already represented by direct supervisor links. Without an assigned manager or
actual supervisor, that person's share is not credited to an arbitrary admin.
Defaults are 5% and 1%; administrators edit them on the Sales page, with two decimal
places, optimistic version checks and a combined maximum of 100%. Negative profit
earns no bonus. Each positive share rounds down to USD millionths.

First recognition and subsequent reconciliations commit profit and differential
bonus entries atomically. Repeat billing snapshots do not duplicate credits.
Later corrections retain the original recipients/rates; reduced or withdrawn
cost verification reverses the previous bonus, and verified replacement data
restores only the corrected amount. Corrections may reduce an already-paid
employee's balance below zero. Assignment, hierarchy and rate changes serialize
with recognition through the existing settings lock.

The Bonuses dialog shows accrued, paid and remaining amounts plus paginated
history. Each sales employee sees themselves and current descendants. The new
`bonuses` delegated permission allows an ancestor to record payments to their
subordinates; existing grant, delegation and revocation rules apply. An admin can
record payments to sales employees and former employees with bonus history.
Self-payments are forbidden. Revoked rights are rechecked inside the transaction.

A payout records a positive USD amount, actual date, required payment method and
optional note. It records an external payment; it does not transfer funds. A
client-generated operation UUID prevents duplicate retry deductions. Changed
content under the same UUID conflicts. Payments may exceed the available balance.
Accruals, corrections and payouts are immutable; audit records identify payment
authors and rate changes. Financial history prevents deleting referenced tasks or
people. Per-person percentage overrides and payout reversals are not implemented.

Migration 058 removes top-up amount ceilings from authorization and delegation.
Existing grants and their lineage remain valid. Legacy limit values remain in the
database only as historical metadata and do not constrain payments or delegation.
Current permission APIs contain only boolean capabilities and a version.

### Company duplicate protection

Both sales onboarding and Administrator organization creation require a company
website. Names collapse whitespace and compare without case. Websites compare
by lowercase hostname without `www.`, HTTP/HTTPS, port, path, query or trailing
dot; internationalized hostnames use IDNA. A different subdomain is a different
website. No DNS lookup or website fetch is performed. Either a matching name or
matching website blocks creation with HTTP 409 and a clear criterion. Email is a
contact address and may be shared by distinct companies.

Migration 059 copies existing onboarding domains to the organization identity
and adds lookup indexes. Existing companies without a known website remain
usable; historical duplicates are not merged or deleted. All application create
paths and organization rename writes serialize through the existing settings
transaction lock. The duplicate check and insert/rename share the transaction,
so concurrent sales/admin submissions cannot create two companies with the same
identity. Direct SQL imports must follow the same lock and checks. Failed creates
leave no organization, invitation or delivery. Existing normalized operation-ID
retries return the original company; changing input requires a fresh operation ID.
Renaming also checks uniqueness, so editing a name cannot bypass the rule.
