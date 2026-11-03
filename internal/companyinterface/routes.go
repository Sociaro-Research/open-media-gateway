package companyinterface

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
// first subset has no bodies or recipient selectors and grants no mutations.
// ResponseURLs names a typed response adapter which later transport must supply;
// it does not authorize rewriting arbitrary JSON or following redirects.
type Contract struct {
	ID           string
	Class        Classification
	Auth         []AuthKind
	Scope        string
	ResponseURLs string
	Binary       bool
}

// Classify is deliberately exact. GET's Go ServeMux HEAD alias is spelled out.
// Unknown paths, encoded aliases, unsupported verbs and not-yet-implemented
// operations are disallowed. Never turn this into a /v1 or /system prefix rule.
func Classify(method, path string) Contract {
	if strings.ContainsAny(path, "%\\?#\r\n") || strings.Contains(path, "//") || strings.Contains(path, "..") {
		return Contract{Class: Disallowed}
	}
	read := method == "GET" || method == "HEAD"
	if read {
		switch path {
		case "/auth/session", "/site.json":
			return Contract{ID: "local-bootstrap", Class: LocalControl}
		case "/v1/me":
			return Contract{ID: "central-profile", Class: CentralCustomer, Auth: []AuthKind{CentralSession}, ResponseURLs: "profile"}
		case "/v1/key":
			return Contract{ID: "central-key", Class: CentralCustomer, Auth: []AuthKind{CentralKey}, ResponseURLs: "none"}
		case "/v1/providers", "/v1/models":
			return Contract{ID: "central-catalog", Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "generation", ResponseURLs: "catalog"}
		case "/v1/system/template-references":
			return Contract{ID: "shared-template-library", Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "generation", ResponseURLs: "reference-list"}
		}
		parts := strings.Split(path, "/")
		if len(parts) == 6 && parts[0] == "" && parts[1] == "v1" && parts[2] == "system" && parts[3] == "template-references" && validID(parts[4]) && parts[5] == "content" {
			return Contract{ID: "shared-template-content", Class: CentralCustomer, Auth: []AuthKind{CentralSession, CentralKey}, Scope: "generation", ResponseURLs: "no-follow", Binary: true}
		}
	}
	return Contract{Class: Disallowed}
}

// RequestContract validates the complete initial read-only request shape.
// Callers must pass the original escaped path and query, before normalization;
// decoding and cleaning an attacker path before this check is not permitted.
// Body is bounded/read by the caller; even one byte on these reads is rejected.
func RequestContract(method, escapedPath, rawQuery string, body []byte, p Principal) (Contract, error) {
	c := Classify(method, escapedPath)
	if c.Class != CentralCustomer || len(body) != 0 {
		return Contract{}, ErrDenied
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil || len(query) != 0 || rawQuery != "" {
		return Contract{}, ErrDenied
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
// Connection-nominated headers are stripped too. No transport exists yet:
// bounded streaming and before/after-body epoch checks remain integration gates.
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
		if name != "Accept" && !(c.Binary && (name == "Range" || name == "If-Range")) {
			continue
		}
		if connection[name] {
			continue
		}
		if len(values) != 1 || len(values[0]) > 1024 || strings.ContainsAny(values[0], "\r\n\x00") || out[name] != nil {
			return nil, ErrDenied
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
