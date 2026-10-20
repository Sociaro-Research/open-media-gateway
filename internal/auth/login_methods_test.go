package auth

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type methodsFixture struct {
	settings LoginMethods
	err      error
}

func (f *methodsFixture) LoginMethods(context.Context) (LoginMethods, error) {
	return f.settings, f.err
}

type preservedSession struct{ Repository }

func (preservedSession) UserBySession(context.Context, []byte, time.Time) (identity.User, error) {
	return identity.User{ID: "existing-user"}, nil
}
func TestLoginMethodSwitchesGateNewAuthAndPreserveSessions(t *testing.T) {
	policy := &methodsFixture{settings: LoginMethods{Version: 1}}
	m := &Manager{methods: policy, repo: preservedSession{}, email: &emailAuth{}, signingKey: []byte(strings.Repeat("s", 32))}
	for _, action := range []string{"login", "register", "forgot", "verify", "reset", "registration", "complete"} {
		r := httptest.NewRequest("POST", "/auth/email/"+action, strings.NewReader(`{}`))
		r.SetPathValue("action", action)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		m.Email(w, r)
		if w.Code != 403 || len(w.Result().Cookies()) != 0 {
			t.Fatal(action, w.Code, w.Body)
		}
	}
	for _, handler := range []http.HandlerFunc{m.Login, m.Callback} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("GET", "/auth/google", nil))
		if w.Code != 403 || len(w.Result().Cookies()) != 0 {
			t.Fatal(w.Code, w.Body)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "existing-session"})
	if user, err := m.Authenticate(r); err != nil || user.ID != "existing-user" {
		t.Fatal("disabled methods invalidated session", user, err)
	}
	methods := func() map[string]bool {
		w := httptest.NewRecorder()
		m.Methods(w, r)
		var body map[string]bool
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	if got := methods(); got["google"] || got["email"] || got["email_confirmation"] {
		t.Fatal(got)
	}
	policy.settings.Google = true
	policy.settings.Email = true
	if got := methods(); !got["google"] || !got["email"] || got["email_delivery"] {
		t.Fatal("password login must remain available without delivery", got)
	}
	w := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/auth/email/register", strings.NewReader(`{"email":"test@example.test"}`))
	request.SetPathValue("action", "register")
	request.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	m.Email(w, request)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "email_delivery_unavailable") {
		t.Fatal("registration without delivery", w.Code, w.Body)
	}
	policy.err = errors.New("database unavailable")
	for _, handler := range []http.HandlerFunc{m.Login, m.Callback, m.Email, m.Methods} {
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 503 {
			t.Fatal("settings failure must fail closed", w.Code)
		}
	}
	if _, err := m.Authenticate(r); err != nil {
		t.Fatal("settings outage invalidated existing session", err)
	}
}
