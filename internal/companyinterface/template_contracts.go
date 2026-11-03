package companyinterface

import (
	"strings"
	"time"
)

// Template authorship remains a direct central Administrator operation. These
// routes consume the central published recipe under the ordinary customer policy.
func templateCustomerContract(method, path string) Contract {
	read := method == "GET" || method == "HEAD"
	if read && path == "/v1/templates" {
		return customerContract("template-gallery", "", "", "template-gallery", "generation", false)
	}
	parts := strings.Split(path, "/")
	if len(parts) < 4 || len(parts) > 5 || parts[1] != "v1" || parts[2] != "templates" || !validID(parts[3]) {
		return Contract{Class: Disallowed}
	}
	if read && len(parts) == 4 {
		return customerContract("template-detail", "", "revision", "template-detail", "generation", false)
	}
	if read && len(parts) == 5 && parts[4] == "metadata" {
		return customerContract("template-metadata", "", "", "template-metadata", "generation", false)
	}
	if method == "POST" && len(parts) == 5 {
		switch parts[4] {
		case "editing", "validate-media", "estimate", "generations":
			c := customerContract("template-"+parts[4], "template-input", "", "template-"+parts[4], "generation", false)
			c.MaxRequestBytes = 16384 // Same limit as the existing central handlers.
			c.Idempotency = parts[4] == "generations"
			if parts[4] == "validate-media" {
				c.Timeout = 150 * time.Second
			}
			if parts[4] == "estimate" || parts[4] == "generations" {
				c.Auth = []AuthKind{CentralKey}
			}
			return c
		}
	}
	return Contract{Class: Disallowed}
}

// CustomerBodyLimit is selected from the exact route before either hop reads
// bytes. Unknown routes retain the small default and are subsequently denied.
func CustomerBodyLimit(method, path string) int64 {
	c := Classify(method, path)
	if c.MaxRequestBytes > 0 {
		return c.MaxRequestBytes
	}
	return MaxCustomerBody
}

// Only inspection gets the existing central reference-inspection budget.
// Authorization/control and ordinary JSON operations retain their short bound.
func CustomerTimeout(method, path string) time.Duration {
	if timeout := Classify(method, path).Timeout; timeout > 0 {
		return timeout
	}
	return ProtocolTimeout
}
