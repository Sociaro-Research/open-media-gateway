package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type jsonSetupFixture struct{ SetupRepository }

func (jsonSetupFixture) InstallationSetup(context.Context) (SetupState, error) {
	return SetupState{Required: true}, nil
}

func TestAuthJSONDiagnosticsPreservePrivateEnvelope(t *testing.T) {
	for _, setup := range []bool{false, true} {
		m := &Manager{methods: &methodsFixture{settings: LoginMethods{Email: true, Version: 1}}, email: &emailAuth{}}
		handler := http.HandlerFunc(m.Email)
		if setup {
			m.setup = jsonSetupFixture{}
			handler = m.Setup
		}
		for _, tc := range []struct{ body, hint string }{
			{`{"email":123}`, "a JSON string"},
			{`{"password":{"sk_live_private-value":"private-value"}}`, "a JSON string"},
			{`{"sk_live_private-value":"private-value"}`, "Unsupported JSON field"},
			{`{"email":`, "Unexpected end of JSON"},
			{`{"email":!}`, "Malformed JSON at byte"},
			{strings.Repeat(" ", 4097), "4096 bytes"},
			{`{} {}`, "after the first JSON value"},
		} {
			r := httptest.NewRequest("POST", "/auth/email/login", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.SetPathValue("action", "login")
			w := httptest.NewRecorder()
			handler(w, r)
			var got struct{ Error map[string]string }
			if json.Unmarshal(w.Body.Bytes(), &got) != nil || w.Code != 400 || len(got.Error) != 2 || got.Error["code"] != "invalid_request" || !strings.Contains(got.Error["message"], tc.hint) {
				t.Fatalf("setup=%t: %d %s", setup, w.Code, w.Body)
			}
			for _, value := range []string{"private-value", "sk_live", "Unmarshal", "Go struct"} {
				if strings.Contains(w.Body.String(), value) {
					t.Fatalf("private input leaked: %s", w.Body)
				}
			}
			if w.Header().Get("Cache-Control") != "no-store" || len(w.Result().Cookies()) != 0 {
				t.Fatal("auth response privacy changed")
			}
		}
	}
}
