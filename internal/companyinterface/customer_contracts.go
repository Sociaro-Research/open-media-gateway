package companyinterface

import (
	"encoding/json"
	"net/url"
	"strings"
)

// Extended contracts are grouped by the existing central operation, never by a
// blanket authority prefix. Unsupported upload, payment and streaming contracts
// remain absent until their distinct budgets and response adapters are present.
func customerContract(id, body, query, response, scope string, management bool) Contract {
	return Contract{ID: id, Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, RequestBody: body, QueryFields: query, ResponseURLs: response, Scope: scope, BrowserManagement: management}
}
func extendedCustomerContract(method, path string) Contract {
	read := method == "GET" || method == "HEAD"
	if read && strings.HasPrefix(path, "/v1/models/") {
		model := strings.TrimPrefix(path, "/v1/models/")
		decoded, e := url.PathUnescape(model)
		if e == nil && decoded != "" && len(decoded) <= 1024 && !strings.ContainsAny(decoded, "\\?#%\r\n\x00") {
			valid := true
			for _, part := range strings.Split(decoded, "/") {
				if part == "" || part == "." || part == ".." {
					valid = false
				}
			}
			if valid {
				if strings.HasSuffix(model, "/preparations") && strings.Count(model, "/") == 1 {
					return customerContract("model-preparations", "", "", "preparations", "generation", false)
				}
				return customerContract("central-model", "", "", "catalog", "generation", false)
			}
		}
		return Contract{Class: Disallowed}
	}
	if strings.ContainsAny(path, "%\\?#\r\n") || strings.Contains(path, "//") || strings.Contains(path, "..") {
		return Contract{Class: Disallowed}
	}
	if read && path == "/v1/api-keys/usage" {
		return customerContract("company-key-usage", "", "organization_id month timezone", "usage", "keys:manage", true)
	}
	if method == "POST" && (path == "/v1/invitations/preview" || path == "/v1/invitations/accept") {
		scope := "generation"
		if path == "/v1/invitations/accept" {
			scope = "organizations:manage"
		}
		return customerContract("company-invitation-token", "invite-token", "", "invitation", scope, true)
	}
	if path == "/v1/projects" {
		if read {
			return customerContract("projects", "", "", "projects", "generation", false)
		}
		if method == "POST" {
			return customerContract("project-create", "project-create", "", "project", "generation", false)
		}
	}
	parts := strings.Split(path, "/")
	if len(parts) == 4 && parts[1] == "v1" && parts[2] == "projects" && validID(parts[3]) {
		if method == "PATCH" || method == "DELETE" {
			return customerContract("project-update", "project-update", "", "project", "generation", false)
		}
	}
	if len(parts) == 5 && parts[1] == "v1" && parts[2] == "generations" && validID(parts[3]) && parts[4] == "project" && method == "PATCH" {
		return customerContract("project-assignment", "project-assignment", "", "project", "generation", false)
	}
	if read && path == "/v1/generations" {
		return customerContract("generation-list", "", "limit offset project_id cursor through view ids include_total", "generations", "generation", false)
	}
	if read && len(parts) == 4 && parts[1] == "v1" && (parts[2] == "generations" || parts[2] == "videos") && validID(parts[3]) {
		return customerContract("generation-detail", "", "", "generation", "generation", false)
	}
	if len(parts) < 4 || parts[1] != "v1" || parts[2] != "organizations" || !validID(parts[3]) {
		return Contract{Class: Disallowed}
	}
	if len(parts) == 4 && method == "PUT" {
		return customerContract("company-settings", "company-settings", "", "none", "organizations:manage", true)
	}
	if len(parts) == 5 {
		switch parts[4] {
		case "support":
			if read {
				c := customerContract("company-support", "", "", "support", "", true)
				c.Auth = []AuthKind{CentralSession}
				return c
			}
		case "request-logging":
			if method == "PUT" {
				return customerContract("company-request-logging", "request-logging", "", "none", "organizations:manage", true)
			}
		case "credits", "balance-history":
			if read {
				scope := "organizations:manage"
				if parts[4] == "credits" && method == "GET" {
					scope = "generation"
				}
				return customerContract("company-"+parts[4], "", "offset", "company-history", scope, true)
			}
		case "invitations":
			if method == "POST" {
				return customerContract("company-invite-create", "invite-create", "", "invitation", "organizations:manage", true)
			}
		case "generations":
			if read {
				return customerContract("company-generation-list", "", "limit offset project_id cursor through view ids include_total", "generations", "generation", true)
			}
		}
	}
	if len(parts) == 6 && validID(parts[5]) {
		switch parts[4] {
		case "members":
			if method == "PUT" {
				return customerContract("company-member-update", "member-update", "", "none", "organizations:manage", true)
			}
			if method == "DELETE" {
				return customerContract("company-member-delete", "", "", "none", "organizations:manage", true)
			}
		case "invitations":
			if method == "DELETE" {
				return customerContract("company-invite-revoke", "", "", "none", "organizations:manage", true)
			}
		case "generations":
			if read {
				return customerContract("company-generation-detail", "", "", "generation", "generation", true)
			}
		}
	}
	return Contract{Class: Disallowed}
}
func extendedCustomerBody(kind string, raw []byte) error {
	if kind == "invite-create" && len(raw) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if strictCustomerJSON(raw, &fields) != nil || fields == nil {
		return ErrDenied
	}
	allowed, required := "", ""
	switch kind {
	case "generation-json":
		allowed = "model prompt options webhook_url max_expected_cost_usd project_id timeout_seconds input voice instructions response_format output_format quality background size seconds stream n image input_reference"
		required = "model"
	case "reference-validation":
		allowed = "validation reference_ids"
	case "template-input":
		allowed = "expected_resolution revision provider model inputs project_id max_expected_cost_usd"
		required = "revision provider model"
	case "company-settings":
		allowed = "name default_weekly_limit_usd"
		required = allowed
	case "request-logging":
		allowed = "minimize_request_logging"
		required = allowed
	case "member-update":
		allowed = "role weekly_limit_usd"
		required = "role"
	case "invite-create":
		allowed = "role"
	case "invite-token":
		allowed = "token"
		required = allowed
	case "project-create":
		allowed = "name"
		required = allowed
	case "project-update":
		allowed = "name version"
		required = "version"
	case "project-assignment":
		allowed = "project_id version"
		required = allowed
	default:
		return ErrDenied
	}
	for name := range fields {
		if !containsField(allowed, name) {
			return ErrDenied
		}
	}
	for _, name := range strings.Fields(required) {
		if _, ok := fields[name]; !ok {
			return ErrDenied
		}
	}
	if kind == "invite-token" {
		var token string
		if json.Unmarshal(fields["token"], &token) != nil || len(token) != 43 {
			return ErrDenied
		}
	}
	return nil
}
func containsField(fields, name string) bool {
	for _, field := range strings.Fields(fields) {
		if field == name {
			return true
		}
	}
	return false
}
