package companyinterface

import (
	"strings"
	"time"
)

// Only existing media-workflow resource operations are delegated. Provider-wide
// external inventory/import remains outside the company installation ceiling.
func resourceCustomerContract(method, path string) Contract {
	if strings.ContainsAny(path, "%\\?#\r\n") || strings.Contains(path, "//") || strings.Contains(path, "..") {
		return Contract{Class: Disallowed}
	}
	read := method == "GET" || method == "HEAD"
	makeContract := func(id, body, query, response string) Contract {
		c := customerContract(id, body, query, response, "generation", false)
		c.Auth = []AuthKind{CentralKey}
		c.Idempotency = method == "POST" && !strings.HasSuffix(path, "/estimate") && response == "resource-generation"
		return c
	}
	switch path {
	case "/v1/provider-resources":
		if read {
			return makeContract("resources", "", "provider kind limit offset", "resources")
		}
		if method == "POST" {
			return makeContract("resource-create", "resource-create", "", "resource-generation")
		}
	case "/v1/provider-resources/estimate":
		if method == "POST" {
			return makeContract("resource-estimate", "resource-create", "", "estimate")
		}
	case "/v1/media-inputs/faces", "/v1/media-inputs/faces/estimate":
		if method == "POST" {
			response := "resource-generation"
			if strings.HasSuffix(path, "/estimate") {
				response = "estimate"
			}
			return makeContract("face-preparation", "face-preparation", "", response)
		}
	case "/v1/provider-resource-presets":
		if read {
			return makeContract("resource-presets", "", "provider workflow_model kind page size", "resource-presets")
		}
	case "/v1/provider-resource-presets/import":
		if method == "POST" {
			c := makeContract("resource-preset-import", "resource-preset-import", "", "resource")
			c.MaxRequestBytes = 32 << 10
			return c
		}
	}
	parts := strings.Split(path, "/")
	if len(parts) == 4 && parts[1] == "v1" && validID(parts[3]) && read {
		if parts[2] == "provider-resources" {
			return makeContract("resource-detail", "", "", "resource")
		}
		if parts[2] == "provider-resource-jobs" {
			return makeContract("resource-job", "", "", "resource-job")
		}
	}
	if (len(parts) == 6 || len(parts) == 7 && parts[6] == "estimate") && parts[1] == "v1" && parts[2] == "provider-resources" && validID(parts[3]) && parts[4] == "actions" && resourcePathToken(parts[5]) && method == "POST" {
		response := "resource-generation"
		if len(parts) == 7 {
			response = "estimate"
		}
		return makeContract("resource-action", "resource-action", "", response)
	}
	if len(parts) >= 6 && parts[1] == "v1" && parts[2] == "references" && parts[3] == "providers" && resourcePathToken(parts[4]) {
		c := Contract{Class: Disallowed}
		if len(parts) == 6 {
			switch parts[5] {
			case "groups":
				if read {
					c = makeContract("asset-groups", "", "", "asset-groups")
				}
				if method == "POST" {
					c = makeContract("asset-group-create", "asset-group", "", "asset-group")
				}
			case "assets":
				if read {
					c = makeContract("assets", "", "offset", "assets")
				}
				if method == "POST" {
					c = makeContract("asset-register", "asset-register", "", "asset")
				}
			}
		}
		if len(parts) == 7 && parts[5] == "assets" && validID(parts[6]) {
			if read {
				c = makeContract("asset-detail", "", "", "asset")
			}
			if method == "DELETE" {
				c = makeContract("asset-delete", "", "detach", "none")
			}
		}
		if c.Class == CentralCustomer {
			c.Timeout = 45 * time.Second
		}
		return c
	}
	return Contract{Class: Disallowed}
}
func resourcePathToken(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
