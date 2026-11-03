package companyinterface

import (
	"strings"
	"time"
)

// These JSON contracts retain the existing central admission/library handlers.
// Multipart generation and upload plans are separate bridge contracts and are
// deliberately not admitted merely because their JSON aliases are available.
func workflowCustomerContract(method, path string) Contract {
	read := method == "GET" || method == "HEAD"
	if method == "POST" {
		switch path {
		case "/v1/generations", "/v1/generations/estimate", "/v1/videos", "/v1/images/generations", "/v1/images/edits", "/v1/audio/speech", "/v1/audio/generations":
			c := customerContract("generation-json", "generation-json", "", "generation", "generation", false)
			c.Auth = []AuthKind{CentralKey}
			c.MaxRequestBytes = 64 << 10
			c.Idempotency = path != "/v1/generations/estimate"
			if !c.Idempotency {
				c.ResponseURLs = "estimate"
			}
			return c
		case "/v1/references/validate":
			c := customerContract("reference-validation", "reference-validation", "", "none", "generation", false)
			c.Auth = []AuthKind{CentralKey}
			c.MaxRequestBytes = 64 << 10
			c.Timeout = 150 * time.Second
			return c
		}
	}
	if read && path == "/v1/references" {
		c := customerContract("references", "", "offset", "reference-list", "generation", false)
		c.Auth = []AuthKind{CentralKey}
		return c
	}
	parts := strings.Split(path, "/")
	if len(parts) == 4 && parts[1] == "v1" && parts[2] == "references" && validID(parts[3]) && method == "DELETE" {
		c := customerContract("reference-delete", "", "", "none", "generation", false)
		c.Auth = []AuthKind{CentralKey}
		return c
	}
	if len(parts) >= 5 && parts[1] == "v1" && parts[2] == "organizations" && validID(parts[3]) && parts[4] == "references" {
		if len(parts) == 5 && read {
			return customerContract("company-references", "", "offset", "reference-list", "generation", true)
		}
		if len(parts) == 6 && validID(parts[5]) && method == "DELETE" {
			return customerContract("company-reference-delete", "", "", "none", "organizations:manage", true)
		}
	}
	return Contract{Class: Disallowed}
}
