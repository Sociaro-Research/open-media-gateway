package api

import (
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"net/http"
)

func (s *Server) loginMethodSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin {
		writeError(w, 403, "forbidden", "administrator access is required")
		return
	}
	if s.loginProviders == nil {
		writeError(w, 503, "unavailable", "Login provider settings are unavailable.")
		return
	}
	if r.Method == http.MethodPut {
		var in auth.LoginProviderUpdate
		if !decodeSystem(w, r, &in) {
			return
		}
		next, err := s.loginProviders.Update(r.Context(), identity.ActorID(r.Context()), r.PathValue("provider"), in)
		if errors.Is(err, auth.ErrLoginProviderSettings) {
			writeError(w, 400, "invalid_request", "Enter a valid Google Client ID and Client Secret, enabled state and current version. Email has no OAuth settings.")
			return
		}
		if err != nil {
			systemError(w, err)
			return
		}
		writeJSON(w, 200, next)
		return
	}
	records, err := s.loginProviders.Settings(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	delivery := false
	if s.emailDelivery != nil {
		delivery, err = s.emailDelivery.Configured(r.Context())
		if err != nil {
			systemError(w, err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"data": records, "available_providers": []map[string]string{{"id": "google", "name": "Google"}, {"id": "email", "name": "Email and password"}}, "email_delivery_configured": delivery})
}
