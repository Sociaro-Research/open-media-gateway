package companyinterface

import (
	"context"
	"net/http"
)

// Test-only seams are absent from production binaries. Real transport tests
// separately verify NewClient rejects private destinations and redirects.
func NewFixtureClient(origin string, h *http.Client) *Client { return &Client{origin: origin, http: h} }
func FixtureCompleteProtocol(c *Coordinator) {
	c.protocol = func() Protocol {
		return Protocol{Purpose: Purpose, Version: Version, Capabilities: RequiredCapabilities()}
	}
}

// FixtureServerReadiness is absent from production binaries. The callback is
// read at the actual issuance boundary, so delayed-body races can be tested.
func FixtureServerReadiness(next http.Handler, complete func() bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture := serverReadinessFixture{current: func() Protocol {
			if complete != nil && complete() {
				return Protocol{Purpose: Purpose, Version: Version, Capabilities: RequiredCapabilities()}
			}
			return ImplementedProtocol()
		}}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serverReadinessKey{}, fixture)))
	})
}
