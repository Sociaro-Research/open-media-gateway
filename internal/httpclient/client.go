// Package httpclient provides reusable outbound connections for concurrent
// provider operations and worker-to-storage transfers.
package httpclient

import (
	"net/http"
	"time"
)

func New(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// The standard two idle connections per host cause connection churn between
	// polling waves. Keep a bounded idle pool without limiting active submissions.
	transport.MaxIdleConns = 512
	transport.MaxIdleConnsPerHost = 128
	return &http.Client{Transport: transport, Timeout: timeout}
}
