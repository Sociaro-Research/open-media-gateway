package auth

import (
	"context"
	"net/http"
)

// LoginMethods controls new authentications only; sessions and credentials remain intact.
type LoginMethods struct {
	Google  bool  `json:"google"`
	Email   bool  `json:"email"`
	Version int64 `json:"version"`
}
type LoginMethodsReader interface {
	LoginMethods(context.Context) (LoginMethods, error)
}

func (m *Manager) loginMethods(ctx context.Context) (LoginMethods, error) {
	if m.methods == nil {
		return LoginMethods{Email: true, Version: 1}, nil
	}
	return m.methods.LoginMethods(ctx)
}
func (m *Manager) allowLogin(w http.ResponseWriter, r *http.Request, method string) bool {
	if required, err := m.setupRequired(r.Context()); err != nil {
		m.emailFailure(w)
		return false
	} else if required {
		emailError(w, 409, "setup_required", "Create the installation owner account first.")
		return false
	}
	settings, err := m.loginMethods(r.Context())
	if err != nil {
		emailError(w, 503, "auth_unavailable", "Sign-in is temporarily unavailable. Try again later.")
		return false
	}
	enabled := settings.Email
	if method == "google" {
		enabled = settings.Google
	}
	if !enabled {
		emailError(w, 403, "login_method_disabled", "This sign-in method is currently disabled.")
		return false
	}
	return true
}
