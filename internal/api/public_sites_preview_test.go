package api

import (
	"net/http"
	"os"
	"testing"
)

// Opt-in local browser fixture; it does not connect to a database or provider.
func TestPublicSitesPreview(t *testing.T) {
	if os.Getenv("GATEWAY_SITES_PREVIEW") != "1" {
		t.Skip("manual preview")
	}
	s := &Server{service: serviceStub{}, health: healthStub{}, apiKey: "fixture-key", auth: browserAuthStub{}}
	h, err := WithPublicAPI(s.handler(), "http://localhost:18087", "http://localhost:18087", "")
	if err != nil {
		t.Fatal(err)
	}
	// Expose email choices for UI inspection without sending email.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/methods" {
			writeJSON(w, 200, map[string]bool{"email": true, "email_delivery": true, "google": true})
			return
		}
		h.ServeHTTP(w, r)
	})
	t.Fatal(http.ListenAndServe("127.0.0.1:18086", handler))
}
