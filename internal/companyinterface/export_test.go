package companyinterface

import "net/http"

// Test-only seams are absent from production binaries. Real transport tests
// separately verify NewClient rejects private destinations and redirects.
func NewFixtureClient(origin string, h *http.Client) *Client { return &Client{origin: origin, http: h} }
func FixtureCompleteProtocol(c *Coordinator) {
	c.protocol = func() Protocol {
		return Protocol{Purpose: Purpose, Version: Version, Capabilities: RequiredCapabilities()}
	}
}
