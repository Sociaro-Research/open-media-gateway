package companyinterface

import (
	"strconv"
	"strings"
	"time"
)

// These read contracts preserve existing central policy and public projection.
// In particular, central-info describes the central service, not the local
// installation's operator role or permission to enter company-interface mode.
func companyReadContract(method, path string) Contract {
	if method != "GET" && method != "HEAD" || strings.ContainsAny(path, "%\\?#\r\n\x00") || strings.Contains(path, "//") {
		return Contract{Class: Disallowed}
	}
	switch path {
	case "/v1/info":
		return customerContract("central-info", "", "", "central-info", "generation", true)
	case "/v1/storage/usage":
		c := customerContract("shared-storage-usage", "", "", "storage-usage", "generation", false)
		c.Auth = []AuthKind{CentralKey}
		return c
	}
	parts := strings.Split(path, "/")
	if len(parts) < 5 || parts[1] != "v1" || parts[2] != "organizations" || !validID(parts[3]) {
		return Contract{Class: Disallowed}
	}
	if len(parts) == 5 {
		switch parts[4] {
		case "storage":
			return customerContract("company-storage-read", "", "", "storage-settings", "organizations:manage", true)
		case "reports":
			c := customerContract("company-reports", "", "timezone from to user user_id api_key_id generation_id limit offset format view interval", "report-bytes", "generation", true)
			if method == "HEAD" {
				c.Scope = "organizations:manage"
			}
			c.Stream = true
			c.Timeout = time.Minute
			c.HeaderTimeout = time.Minute
			return c
		}
	}
	if parts[4] == "audit" {
		query := "scope actor_id action object_type object_id outcome correlation_id timezone from to"
		if len(parts) == 6 {
			switch parts[5] {
			case "events":
				return customerContract("company-audit-events", "", query+" before_id", "audit", "organizations:manage", true)
			case "revoked-keys":
				return customerContract("company-audit-revoked-keys", "", query+" offset", "audit", "organizations:manage", true)
			}
		}
		if len(parts) == 7 && parts[5] == "events" {
			id, e := strconv.ParseInt(parts[6], 10, 64)
			if e == nil && id > 0 && strconv.FormatInt(id, 10) == parts[6] {
				return customerContract("company-audit-event", "", "", "audit", "organizations:manage", true)
			}
		}
	}
	return Contract{Class: Disallowed}
}

// Header/preparation time is a route contract, never a client-supplied override.
// The shared public transport has no separate shorter response-header timeout.
func CustomerHeaderTimeout(method, path string) time.Duration {
	if budget := Classify(method, path).HeaderTimeout; budget > 0 {
		return budget
	}
	return MediaIdleTimeout
}
