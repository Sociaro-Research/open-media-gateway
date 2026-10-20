package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type logoutRepository struct {
	Repository
	err error
}

func (r *logoutRepository) DeleteSession(context.Context, []byte) error { return r.err }

func TestLogoutDoesNotClaimRevocationDuringDatabaseOutage(t *testing.T) {
	repo := &logoutRepository{err: errors.New("database unavailable")}
	m := &Manager{repo: repo, cookieSecure: true}
	request := httptest.NewRequest("POST", "/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "existing-session"})
	w := httptest.NewRecorder()
	m.Logout(w, request)
	if w.Code != 503 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("logout hid failed revocation: status=%d cookies=%v", w.Code, w.Result().Cookies())
	}
	repo.err = nil
	w = httptest.NewRecorder()
	m.Logout(w, request)
	if w.Code != 204 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("successful retry did not clear session")
	}
}
