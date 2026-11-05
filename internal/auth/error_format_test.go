package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnavailableAuthEndpointsUsePrivateJSONErrors(t *testing.T) {
	m := &Manager{methods: &methodsFixture{settings: LoginMethods{Email: true, Version: 1}}, email: &emailAuth{}}
	for _, handler := range []http.HandlerFunc{m.Setup, m.Email} {
		r := httptest.NewRequest("POST", "/auth/email/private-value", strings.NewReader(`{}`))
		r.SetPathValue("action", "private-value")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler(w, r)
		var body struct{ Error map[string]string }
		if w.Code != 404 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Error["code"] != "not_found" || body.Error["message"] == "" || len(body.Error) != 2 {
			t.Fatalf("unexpected auth error: %d %s", w.Code, w.Body)
		}
		if w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" || len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "private-value") {
			t.Fatal("auth privacy boundary changed")
		}
	}
}
