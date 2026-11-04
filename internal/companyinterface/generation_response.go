package companyinterface

import (
	"encoding/json"
	"net/http"
	"strings"
)

const LocalCustomerPrefix = "/auth/company-interface/local/customer/"

// Only the known status field and Location of a single central generation are
// mapped. Prompts, parameters, snapshots and arbitrary nested JSON are untouched.
// Media/content URLs require their own typed transport and are not admitted here.
func generationStatusResponse(contract Contract, status int, method, path, company string, headers http.Header, raw []byte) ([]byte, string, error) {
	if status < 200 || status >= 300 || (contract.ResponseURLs != "generation" && contract.ResponseURLs != "template-generations" && contract.ResponseURLs != "resource-generation") {
		return raw, "", nil
	}
	if method == http.MethodHead {
		// HEAD has no representation to decode. Bind optional Location to the
		// already admitted requested object; never issue a hidden GET or invent
		// a JSON body, representation length, or body-derived validator.
		if len(raw) != 0 || (contract.ID != "generation-detail" && contract.ID != "company-generation-detail") {
			return nil, "", ErrUnavailable
		}
		parts := strings.Split(path, "/")
		id := parts[len(parts)-1]
		if !validID(id) {
			return nil, "", ErrUnavailable
		}
		expected := path
		if contract.ID == "company-generation-detail" {
			expected = "/v1/generations/" + id
		}
		values := headers.Values("Location")
		if len(values) > 1 || len(values) == 1 && values[0] != expected {
			return nil, "", ErrUnavailable
		}
		if len(values) == 1 {
			return raw, LocalCustomerPrefix + strings.TrimPrefix(expected, "/"), nil
		}
		return raw, "", nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, "", ErrUnavailable
	}
	var id, object, organization, statusURL string
	if json.Unmarshal(fields["id"], &id) != nil || !validID(id) || json.Unmarshal(fields["object"], &object) != nil || json.Unmarshal(fields["status_url"], &statusURL) != nil {
		return nil, "", ErrUnavailable
	}
	if value, ok := fields["organization_id"]; ok && (json.Unmarshal(value, &organization) != nil || organization != "" && organization != company) {
		return nil, "", ErrUnavailable
	}
	base := "/v1/generations/"
	if object == "video" {
		base = "/v1/videos/"
	} else if object != "generation" {
		return nil, "", ErrUnavailable
	}
	expected := base + id
	if statusURL != expected {
		return nil, "", ErrUnavailable
	}
	// GET detail is additionally bound to the exact requested object. Creation
	// receives its authoritative fresh ID from central, never from a caller URL.
	if contract.ID == "generation-detail" || contract.ID == "company-generation-detail" {
		parts := strings.Split(path, "/")
		if parts[len(parts)-1] != id {
			return nil, "", ErrUnavailable
		}
	}
	values := headers.Values("Location")
	if len(values) > 1 || len(values) == 1 && values[0] != expected || status == http.StatusAccepted && contract.ResponseURLs != "resource-generation" && len(values) != 1 {
		return nil, "", ErrUnavailable
	}
	mapped := LocalCustomerPrefix + strings.TrimPrefix(expected, "/")
	fields["status_url"], _ = json.Marshal(mapped)
	encoded, e := json.Marshal(fields)
	if e != nil {
		return nil, "", ErrUnavailable
	}
	location := ""
	if len(values) == 1 {
		location = mapped
	}
	return encoded, location, nil
}
