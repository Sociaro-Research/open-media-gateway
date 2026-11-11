# Console table preferences

Every console table with more than three original columns has a column gear and
an adjacent viewport-width toggle. Existing action columns count toward eligibility;
the new control cell does not. Action cells remain available. Tables with three
or fewer columns, documentation/email tables, cards and galleries are excluded.
The first column names the row (member, organization, key…) and always stays
visible; the chooser shows it as a locked switch. Requests has no locked column.

Selections and width mode are stored in the signed-in user's profile, independently
for each logical table, and survive navigation, reload and subsequent sign-ins.
Equivalent personal/company/admin Requests views keep the existing shared
`report_columns` preference. Unavailable Requests selections survive role/scope
changes; at least one currently available data column remains visible.

Width mode bypasses site/content width caps using responsive viewport gutters.
Existing sidebars and adjacent charts keep their place; the table uses the
remaining viewport space beside them. Comparison popups widen their own panel.
This does not use the browser Fullscreen API or lock vertical document scrolling.

## Profile API

`PATCH /v1/me` requires the authenticated user's browser session. API keys cannot
write profiles. Connected-company requests use the authenticated BrowserActor,
not the selected generation key's recipient or the installation owner. Existing
company mutation admission and revocation checks still apply.

```json
{
  "table_columns": {"organizations": ["organization", "balance"]},
  "table_wide": {"organizations": true, "requests": false}
}
```

Each supplied table entry merges atomically into the current row. Other tables,
the other preference map, theme, contacts and other profile fields are preserved.
`table_columns.<table>: null` restores that table's default columns; an empty array
is invalid. `table_wide.<table>` accepts only boolean `true` or `false`. Omission
or a whole-field null preserves that map; a non-null empty object is invalid. At
least one non-null supported profile field is required. Unknown table/column IDs,
duplicates and excessive maps are rejected with bounded field diagnostics.

Requests columns remain exclusively in `report_columns`; `table_columns.requests`
is rejected. Legacy `user` maps to the combined `api_key` identity column in the UI.
`table_wide.requests` is supported. Column IDs are array values so financial/customer
JSON projection cannot erase a selection whose name matches a restricted data field.

The existing 8192-byte profile body limit is unchanged. Partial per-table PATCHes
are small; even all supported column/width entries and Requests columns fit within
that limit. Preferences are part of the existing atomic personal profile audit.
Applied migrations are not rewritten; migration097 adds the two JSONB maps and
retains them across a rollback/re-upgrade.

## Stable table identifiers

| Renderer | Logical table IDs |
| --- | --- |
| `reports.js` | `requests` |
| `organizations.js` | `organizations`, `client_companies`, `organization_members`, `organization_invitations` |
| `catalog-sync.js` | `catalog_sources`, `catalog_runs`, `catalog_warnings` |
| `audit-log.js` | `audit_events`, `audit_revoked_keys` |
| `payments.js` | `payment_ledger` |
| `keys.js` | `personal_keys`, `organization_keys` |
| `model-access.js` | `provider_models` |
| `report-summary.js` | `usage_providers` |
| `client-onboarding.js` | `company_invitations` |
| `stablecoin-payments.js` | `receiving_wallets`, `payment_requests` |
| `provider-connections.js` | `provider_headers`, `provider_manual_models` |
| `vat-settings.js` | `vat_rules` |
| `organization-roles.js` | `organization_roles` |

Editable and read-only member variants share `organization_members`. Column IDs
are documented in the [OpenAPI profile schemas](openapi.yaml), alongside their
bounds. IDs are independent of current display labels and permissions.

## Shared integration

Register each real renderer with `TableUI.attach(table, {id, columns, api})` using
explicit stable IDs, including `actions` for an existing action column. The helper
keeps row nodes and business controls in place. Its hidden chooser select lives
outside business forms and descendant-based payload snapshots. Hidden editable
fields have a recovery action; native invalid events reveal the affected column
before the browser tries to focus it. Temporary recovery does not change the saved
selection. Save/Cancel and pending business locks keep their original behavior.

Profile confirmations update the same user's cache even after a table unmounts.
Only fields/table entries changed by that PATCH are adopted from its full response;
unrelated stale response fields cannot revert a newer confirmation. Writes to the
same preference entry are FIFO, and failed later writes preserve earlier successful
confirmations. Identity, session and company-connection changes fence late responses.

Read-only background reconciliation preserves UI-owned header/feedback cells and
reapplies the table's current presentation after updating data. Unmount removes
owned controls and pickers; it does not remove externally supplied status nodes.

See the [UI testing guide](../scripts/ui/README.md) for synthetic control and real
renderer fixtures, and the [design system](design-system.md#large-table-controls)
for visual acceptance. These checks do not establish live provider/payment behavior.
