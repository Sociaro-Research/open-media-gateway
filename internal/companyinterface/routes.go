package companyinterface

import (
	"bytes"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Classification string

const (
	Disallowed      Classification = "disallowed"
	LocalControl    Classification = "local-control"
	CentralCustomer Classification = "central-customer"
)

type AuthKind string

const (
	CentralSession AuthKind = "central-session"
	CentralKey     AuthKind = "central-key"
)

// Contract is a transport ceiling, not a substitute for the central handler's
// object ownership, current membership, role, scope and redaction checks. This
// bounded subset includes typed company-management bodies. Every additional
// route remains denied until its request/response contract is implemented.
// ResponseURLs names a typed response adapter which later transport must supply;
// it does not authorize rewriting arbitrary JSON or following redirects.
type Contract struct {
	ID                string
	Class             Classification
	Auth              []AuthKind
	Scope             string
	ResponseURLs      string
	RequestBody       string
	QueryFields       string
	BrowserManagement bool
	Binary            bool
	Stream            bool
	MaxRequestBytes   int64
	Timeout           time.Duration
	HeaderTimeout     time.Duration
	Idempotency       bool
}

// Classify is deliberately exact. GET's Go ServeMux HEAD alias is spelled out.
// Unknown paths, encoded aliases, unsupported verbs and not-yet-implemented
// operations are disallowed. Never turn this into a /v1 or /system prefix rule.
func Classify(method, path string) Contract {
	c := classifyCustomerRoute(method, path)
	switch c.ID {
	case "central-profile", "central-profile-update", "company-list", "company-detail", "company-members", "company-invitations", "company-keys", "company-key-create", "company-key-revoke", "playground-key/create", "playground-key/restore":
		c.BrowserManagement = true
	}
	// Preserve the existing central non-GET organization scope selection.
	// Only these already admitted object contracts are affected; artifact HEAD
	// has a separate central exception and is not classified by this list.
	if method == "HEAD" {
		switch c.ID {
		case "company-list", "company-detail", "company-members", "company-invitations", "company-references", "company-generation-list", "company-generation-detail":
			c.Scope = "organizations:manage"
		}
	}
	return c
}
func classifyCustomerRoute(method, path string) Contract {
	if c := companyReadContract(method, path); c.Class == CentralCustomer {
		return c
	}
	if c := mediaCustomerContract(method, path); c.Class == CentralCustomer {
		return c
	}
	if c := resourceCustomerContract(method, path); c.Class == CentralCustomer {
		return c
	}
	if c := workflowCustomerContract(method, path); c.Class == CentralCustomer {
		return c
	}
	if c := templateCustomerContract(method, path); c.Class == CentralCustomer {
		return c
	}
	if c := extendedCustomerContract(method, path); c.Class == CentralCustomer {
		return c
	}
	if strings.ContainsAny(path, "%\\?#\r\n") || strings.Contains(path, "//") || strings.Contains(path, "..") {
		return Contract{Class: Disallowed}
	}
	if method == "PATCH" && path == "/v1/me" {
		return Contract{ID: "central-profile-update", Class: CentralCustomer, Auth: []AuthKind{CentralSession}, ResponseURLs: "profile", RequestBody: "profile"}
	}
	if method == "POST" && (path == "/v1/playground-key/create" || path == "/v1/playground-key/restore") {
		return Contract{ID: strings.TrimPrefix(path, "/v1/"), Class: CentralCustomer, Auth: []AuthKind{CentralSession}, ResponseURLs: "managed-key", RequestBody: "managed-key"}
	}
	if method == "POST" && path == "/v1/api-keys" {
		return Contract{ID: "company-key-create", Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "keys:manage", ResponseURLs: "key", RequestBody: "company-key"}
	}
	if method == "DELETE" && strings.HasPrefix(path, "/v1/api-keys/") && validID(strings.TrimPrefix(path, "/v1/api-keys/")) {
		return Contract{ID: "company-key-revoke", Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "keys:manage", ResponseURLs: "none"}
	}
	read := method == "GET" || method == "HEAD"
	if read {
		switch path {
		case "/auth/session", "/site.json":
			return Contract{ID: "local-bootstrap", Class: LocalControl}
		case "/v1/organizations":
			return Contract{ID: "company-list", Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "generation", ResponseURLs: "company-list"}
		case "/v1/api-keys":
			return Contract{ID: "company-keys", Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "keys:manage", ResponseURLs: "keys"}
		case "/v1/me":
			return Contract{ID: "central-profile", Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "generation", ResponseURLs: "profile"}
		case "/v1/key":
			return Contract{ID: "central-key", Class: CentralCustomer, Auth: []AuthKind{CentralKey}, ResponseURLs: "none"}
		case "/v1/providers", "/v1/models":
			return Contract{ID: "central-catalog", Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "generation", ResponseURLs: "catalog"}
		case "/v1/system/template-references":
			return Contract{ID: "shared-template-library", Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "generation", ResponseURLs: "reference-list", QueryFields: "offset"}
		}
		parts := strings.Split(path, "/")
		if len(parts) >= 4 && parts[0] == "" && parts[1] == "v1" && parts[2] == "organizations" && validID(parts[3]) {
			if len(parts) == 4 {
				return Contract{ID: "company-detail", Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "generation", ResponseURLs: "company"}
			}
			if len(parts) == 5 && (parts[4] == "members" || parts[4] == "invitations") {
				return Contract{ID: "company-" + parts[4], Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "generation", ResponseURLs: "none"}
			}
		}

		if len(parts) == 6 && parts[0] == "" && parts[1] == "v1" && parts[2] == "system" && parts[3] == "template-references" && validID(parts[4]) && parts[5] == "content" {
			return Contract{ID: "shared-template-content", Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "generation", ResponseURLs: "no-follow", Binary: true}
		}
	}
	return Contract{Class: Disallowed}
}

// RequestContract validates each implemented request shape.
// Callers must pass the original escaped path and query, before normalization;
// decoding and cleaning an attacker path before this check is not permitted.
// Body is bounded/read by the caller; reads require an empty body.
func RequestContract(method, escapedPath, rawQuery string, body []byte, p Principal) (Contract, error) {
	if strings.ContainsAny(rawQuery, "#\r\n\x00") {
		return Contract{}, ErrDenied
	}
	c := Classify(method, escapedPath)
	if c.Class != CentralCustomer || int64(len(body)) > CustomerBodyLimit(method, escapedPath) || (c.RequestBody == "" && len(body) != 0) {
		return Contract{}, ErrDenied
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return Contract{}, ErrDenied
	}
	for name, values := range query {
		if len(values) != 1 {
			return Contract{}, ErrDenied
		}
		switch c.ID {
		case "company-keys":
			if name != "organization_id" || values[0] != p.Grant.Company {
				return Contract{}, ErrDenied
			}
		case "company-list":
			if !containsField("all view from to timezone", name) || name == "all" && values[0] != "false" {
				return Contract{}, ErrDenied
			}
		default:
			if !containsField(c.QueryFields, name) || name == "organization_id" && values[0] != p.Grant.Company {
				return Contract{}, ErrDenied
			}
		}
	}
	if rawQuery != "" && len(query) == 0 {
		return Contract{}, ErrDenied
	}
	if strings.HasPrefix(escapedPath, "/v1/organizations/") {
		parts := strings.Split(escapedPath, "/")
		if len(parts) < 4 || parts[3] != p.Grant.Company {
			return Contract{}, ErrDenied
		}
	}
	if err = mediaQueryContract(c, escapedPath, query, p.Grant.Company); err != nil {
		return Contract{}, err
	}
	if c.RequestBody != "" {
		if err = validateCustomerBody(c, body, p.Grant.Company); err != nil {
			return Contract{}, err
		}
	}
	allowed := false
	for _, kind := range c.Auth {
		if kind == p.Kind {
			allowed = true
		}
	}
	if !allowed {
		return Contract{}, ErrDenied
	}
	if p.Kind == CentralKey && c.Scope != "" {
		found := false
		for _, scope := range p.Scopes {
			if scope == c.Scope {
				found = true
			}
		}
		if !found {
			return Contract{}, ErrDenied
		}
	}
	return c, nil
}

// ForwardHeaders creates a fresh allowlist. Caller Authorization, cookies,
// installation/session/identity headers, Host and all hop/forwarded headers are
// never copied. Trusted transport attaches its own credentials after this step.
// Connection-nominated headers are stripped too. Full streaming and local
// before/after-body epoch integration remain separate activation gates.
func ForwardHeaders(c Contract, incoming http.Header) (http.Header, error) {
	if c.Class != CentralCustomer {
		return nil, ErrDenied
	}
	out := http.Header{}
	connection := map[string]bool{}
	for k, values := range incoming {
		if strings.EqualFold(k, "Connection") {
			for _, value := range values {
				for _, name := range strings.Split(value, ",") {
					connection[http.CanonicalHeaderKey(strings.TrimSpace(name))] = true
				}
			}
		}
	}
	for k, values := range incoming {
		name := http.CanonicalHeaderKey(k)
		if name != "Accept" && !(c.Idempotency && name == "Idempotency-Key") && !(c.RequestBody != "" && name == "Content-Type") && !(c.Binary && (name == "Range" || name == "If-Range" || name == "If-Match" || name == "If-None-Match" || name == "If-Modified-Since" || name == "If-Unmodified-Since")) {
			continue
		}
		if connection[name] {
			continue
		}
		if len(values) != 1 || len(values[0]) > 1024 || strings.ContainsAny(values[0], "\r\n\x00") || out[name] != nil {
			return nil, ErrDenied
		}
		if name == "Content-Type" {
			mediaType, _, e := mime.ParseMediaType(values[0])
			if e != nil || mediaType != "application/json" {
				return nil, ErrDenied
			}
		}
		if name == "Range" && !singleRange(values[0]) {
			return nil, ErrDenied
		}
		out.Set(name, values[0])
	}
	return out, nil
}

func singleRange(value string) bool {
	if !strings.HasPrefix(value, "bytes=") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 || (parts[0] == "" && parts[1] == "") {
		return false
	}
	var numbers [2]int64
	for i, p := range parts {
		if p == "" {
			continue
		}
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				return false
			}
		}
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return false
		}
		numbers[i] = n
	}
	return (parts[0] != "" || numbers[1] > 0) && (parts[0] == "" || parts[1] == "" || numbers[0] <= numbers[1])
}

func strictCustomerJSON(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return ErrDenied
	}
	return nil
}
func validateCustomerBody(c Contract, raw []byte, company string) error {
	switch c.RequestBody {
	case "company-key":
		var in apikey.CreateInput
		if strictCustomerJSON(raw, &in) != nil || in.OrganizationID != company || !validID(company) || (in.UserID != "" && !validID(in.UserID)) || (in.ResourceMode != "" && in.ResourceMode != "shared") {
			return ErrDenied
		}
		for _, scope := range in.Scopes {
			if scope != apikey.Generation && scope != apikey.KeysManage && scope != apikey.OrganizationsManage {
				return ErrDenied
			}
		}
	case "managed-key":
		var in struct {
			OrganizationID string `json:"organization_id"`
			RequestID      string `json:"request_id,omitempty"`
		}
		if strictCustomerJSON(raw, &in) != nil || in.OrganizationID != company || !validID(company) {
			return ErrDenied
		}
		if c.ID == "playground-key/create" && !validID(in.RequestID) || c.ID == "playground-key/restore" && in.RequestID != "" {
			return ErrDenied
		}
	case "profile":
		var in map[string]json.RawMessage
		if strictCustomerJSON(raw, &in) != nil || len(in) == 0 {
			return ErrDenied
		}
		for name := range in {
			switch name {
			case "name", "theme", "color_preset", "contacts", "report_columns":
			default:
				return ErrDenied
			}
		}
	default:
		return extendedCustomerBody(c.RequestBody, raw)
	}
	return nil
}
