package companyinterface

import "strings"

// Payment mutations, callbacks, invoice issuance and integration settlement are
// separate unaccepted contracts. Only the actual read-only status projection is
// admitted here; method/path neighbors are not inherited by prefix.
func paymentReadContract(method, path string) Contract {
	if method != "GET" && method != "HEAD" || strings.ContainsAny(path, "%\\?#\r\n\x00") {
		return Contract{Class: Disallowed}
	}
	p := strings.Split(path, "/")
	if len(p) < 5 || p[0] != "" || p[1] != "v1" || p[2] != "organizations" || !validID(p[3]) {
		return Contract{Class: Disallowed}
	}
	if len(p) == 5 {
		switch p[4] {
		case "payment-methods":
			return customerContract("company-payment-methods", "", "", "payment-methods", "organizations:manage", true)
		case "payments":
			return customerContract("company-payment-list", "", "offset", "payment-list", "organizations:manage", true)
		}
	}
	if p[4] == "payments" && len(p) >= 6 && validID(p[5]) {
		if len(p) == 6 {
			return customerContract("company-payment-status", "", "", "payment-status", "organizations:manage", true)
		}
		if len(p) == 7 && p[6] == "qr" {
			c := customerContract("company-payment-qr", "", "", "payment-qr", "organizations:manage", true)
			c.Binary = true
			return c
		}
	}
	return Contract{Class: Disallowed}
}
