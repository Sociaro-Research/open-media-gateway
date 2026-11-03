package api

import (
	"context"
	"net/http"
	"strings"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
)

const companyLocalCookie = "omg_company_browser_v1"

func (s *Server) registerCompanyInterfaceLocalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/company-interface/local/session", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceLocal(w, r, "session") })
	mux.HandleFunc("POST /auth/company-interface/local/begin", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceLocal(w, r, "begin") })
	mux.HandleFunc("POST /auth/company-interface/local/poll", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceLocal(w, r, "poll") })
	mux.HandleFunc("POST /auth/company-interface/local/refresh", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceLocal(w, r, "refresh") })
	mux.HandleFunc("POST /auth/company-interface/local/logout", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceLocal(w, r, "logout") })
	mux.HandleFunc("POST /auth/company-interface/local/cancel", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceLocal(w, r, "cancel") })

}
func (s *Server) companyInterfaceLocal(w http.ResponseWriter, r *http.Request, operation string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	own, _ := r.Context().Value(storageBrowserOriginKey{}).(string)
	meta := installation.FromContext(r.Context())
	if s.companyInterface == nil || s.companyCoordinator == nil || own == "" || own != s.companyCoordinator.LocalOrigin() || meta.Role == "central" || meta.CentralOrigin != s.companyCoordinator.CentralOrigin() {
		companyInterfaceError(w, ci.ErrProtocol)
		return
	}
	if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != own || len(r.Header.Values("Authorization")) > 0 || len(r.Header.Values("X-API-Key")) > 0 {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), ci.ProtocolTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	if !s.companyInterfaceRate(w, r, "local-"+operation) {
		return
	}
	var in struct {
		Mode         string `json:"mode,omitempty"`
		AttemptID    string `json:"attempt_id,omitempty"`
		Consent      bool   `json:"consent,omitempty"`
		ContextEpoch int64  `json:"context_epoch,omitempty"`
		TokenVersion int64  `json:"token_version,omitempty"`
	}
	if !decodeCompanyInterface(w, r, &in) {
		return
	}
	cookies := r.CookiesNamed(companyLocalCookie)
	if len(cookies) > 1 {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	local := ""
	if len(cookies) == 1 {
		local = cookies[0].Value
	}
	if operation == "session" {
		if in.Mode != "" || in.AttemptID != "" || in.Consent || in.ContextEpoch != 0 || in.TokenVersion != 0 {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		if local != "" && !ci.ValidSecret(local) {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		if local == "" {
			var e error
			local, e = ci.NewSecret()
			if e != nil {
				companyInterfaceError(w, ci.ErrUnavailable)
				return
			}
			// The opaque browser context is not an installation or central credential.
			// Issue it before Begin so a lost Begin reply keeps the same durable binding.
			http.SetCookie(w, &http.Cookie{Name: companyLocalCookie, Value: local, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(own, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: 365 * 24 * 60 * 60})
		}
		writeJSON(w, 200, map[string]bool{"ready": true})
		return
	}
	if !ci.ValidSecret(local) {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	switch operation {
	case "refresh", "logout":
		if in.Mode != "" || in.AttemptID != "" || in.Consent || in.ContextEpoch < 1 {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		if operation == "logout" {
			if in.TokenVersion != 0 {
				companyInterfaceError(w, ci.ErrDenied)
				return
			}
			if e := s.companyCoordinator.LogoutSession(ctx, local, in.ContextEpoch); e != nil {
				companyInterfaceError(w, e)
				return
			}
			// Retain the non-authorizing browser nonce for uncertain cleanup; a late
			// logout response must not erase a newer context sharing this browser.
			writeJSON(w, 200, map[string]string{"state": "signed_out"})
			return
		}
		if in.TokenVersion < 1 {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		result, e := s.companyCoordinator.RefreshSession(ctx, local, in.ContextEpoch, in.TokenVersion)
		if e != nil {
			companyInterfaceError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"state": "authorized", "context_epoch": result.Epoch, "connection_revision": result.Revision, "version": result.Tokens.Version})
	case "begin":
		if !in.Consent || in.AttemptID != "" || in.ContextEpoch != 0 || in.TokenVersion != 0 {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		owner := ""
		if in.Mode != "login" {
			if s.auth == nil {
				companyInterfaceError(w, ci.ErrDenied)
				return
			}
			user, e := s.auth.Authenticate(r)
			if e != nil {
				companyInterfaceError(w, ci.ErrDenied)
				return
			}
			owner = user.ID
			ctx = identity.WithUser(ctx, user)
		}
		challenge, e := s.companyCoordinator.Begin(ctx, owner, local, in.Mode)
		if e != nil {
			companyInterfaceError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"attempt_id": challenge.ID, "user_code": challenge.Code, "state": challenge.State, "expires_at": challenge.ExpiresAt, "central_origin": s.companyCoordinator.CentralOrigin()})
	case "poll", "cancel":
		if in.Mode != "" || in.Consent || in.ContextEpoch != 0 || in.TokenVersion != 0 || !ci.ValidID(in.AttemptID) {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		var e error
		if operation == "poll" {
			e = s.companyCoordinator.Poll(ctx, local, in.AttemptID)
		} else {
			e = s.companyCoordinator.Cancel(ctx, local, in.AttemptID)
		}
		if e != nil {
			companyInterfaceError(w, e)
			return
		}
		// This is protocol completion, never a claim that the full portal is enabled.
		writeJSON(w, 200, map[string]string{"state": map[string]string{"poll": "authorized", "cancel": "cancelled"}[operation]})
	}
}
