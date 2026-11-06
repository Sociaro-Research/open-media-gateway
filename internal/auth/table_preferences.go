package auth

import "slices"

// Stable UI identifiers; display labels and current permissions are independent.
// Requests keeps report_columns authoritative, including legacy unavailable IDs.
var TableColumnIDs = map[string][]string{
	"requests":                 nil,
	"organizations":            {"organization", "balance", "credit_limit", "shared_storage"},
	"client_companies":         {"organization", "balance", "spend", "last_activity", "credit_limit", "shared_storage"},
	"organization_members":     {"member", "role", "this_week", "total_consumption", "weekly_limit"},
	"organization_invitations": {"role", "expires", "status", "accepted_by"},
	"catalog_sources":          {"source", "url", "parser"},
	"catalog_runs":             {"checked", "provider", "result", "version", "changes", "open_warnings", "email"},
	"catalog_warnings":         {"model_source", "issue", "first_seen", "last_seen", "checks"},
	"audit_events":             {"time", "actor", "organization", "action", "object", "result"},
	"audit_revoked_keys":       {"revoked", "key", "organization", "owner", "revoked_by", "spend_at_revocation"},
	"payment_ledger":           {"date", "description", "payment", "fee", "balance_change", "status"},
	"personal_keys":            {"key", "spending", "permissions", "activity", "status"},
	"organization_keys":        {"key", "user", "issued_by", "spending", "permissions", "activity", "status"},
	"provider_models":          {"model", "public_api_name", "available_by_default"},
	"usage_providers":          {"provider", "spend", "successful", "failed", "tokens"},
	"company_invitations":      {"company", "first_supermanager", "invitation", "expires"},
	"receiving_wallets":        {"network", "receiving_address", "tokens"},
	"payment_requests":         {"date", "method", "amount", "status"},
	"provider_headers":         {"header", "value", "operation"},
	"provider_manual_models":   {"model", "api_profile", "price_per_request"},
	"vat_rules":                {"buyer_location", "buyer_vat_status", "vat_treatment", "rate"},
	"organization_roles":       {"function", "supermanager", "manager", "content_officer", "developer", "member"},
}

func validateTablePreferences(in ProfileUpdate) error {
	if in.TableColumns != nil {
		if len(*in.TableColumns) == 0 || len(*in.TableColumns) > len(TableColumnIDs) {
			return invalidProfile("table_columns", "provide 1–21 supported table IDs; use null for a table to restore its default columns")
		}
		for table, columns := range *in.TableColumns {
			allowed, ok := TableColumnIDs[table]
			if !ok {
				return invalidProfile("table_columns", "use a documented stable table ID")
			}
			if table == "requests" {
				return invalidProfile("table_columns", "use report_columns for Requests columns; table_wide.requests controls its width")
			}
			if columns == nil {
				continue
			}
			field := "table_columns." + table
			if len(columns) == 0 || len(columns) > len(allowed) {
				return invalidProfile(field, "select at least one supported data column, without duplicates; use null to restore defaults")
			}
			seen := map[string]bool{}
			for _, column := range columns {
				if !slices.Contains(allowed, column) || seen[column] {
					return invalidProfile(field, "select only this table's documented column IDs, without duplicates")
				}
				seen[column] = true
			}
		}
	}
	if in.TableWide != nil {
		if len(*in.TableWide) == 0 || len(*in.TableWide) > len(TableColumnIDs) {
			return invalidProfile("table_wide", "provide 1–22 supported table IDs with true for viewport width or false for normal width")
		}
		for table, wide := range *in.TableWide {
			if _, ok := TableColumnIDs[table]; !ok || wide == nil {
				return invalidProfile("table_wide", "use documented stable table IDs with boolean true or false values")
			}
		}
	}
	return nil
}
