package httpclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A longer endpoint budget must not retain a hidden response-header timeout
// from the short authorization client. Exercise the actual public transport,
// using its existing explicit loopback seam only for this local fixture.
func TestPublicTransportPreservesPerRequestCustomerBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(50 * time.Millisecond); w.WriteHeader(204) }))
	defer server.Close()
	protocol := NewPublic(5*time.Millisecond, "127.0.0.1")
	transport := protocol.Transport.(*publicTransport)
	if transport.base.ResponseHeaderTimeout != 0 || transport.base.Proxy != nil {
		t.Fatal("unexpected hidden header deadline or proxy")
	}
	customer := *protocol
	customer.Timeout = 500 * time.Millisecond
	response, err := customer.Get(server.URL)
	if err != nil {
		t.Fatal("longer customer budget cut by short protocol transport", err)
	}
	response.Body.Close()
	if response.StatusCode != 204 || protocol.Timeout != 5*time.Millisecond {
		t.Fatal("request failed or shared protocol budget mutated")
	}
}
