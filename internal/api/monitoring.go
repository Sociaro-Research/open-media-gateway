package api

import (
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

// monitoringSession authorizes the UI proxy on every Grafana request. Only an
// active browser session and a current system Administrator role are accepted;
// API keys and caller-supplied Grafana identity headers cannot grant access.
func (s *Server) monitoringSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", "Cookie")
	if s.auth == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sign-in is required")
		return
	}
	user, err := s.auth.Authenticate(r)
	if err != nil || user.ID == "" || user.ID == identity.SystemOwnerID {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sign-in is required")
		return
	}
	admin, err := s.systemAdmin(identity.WithUser(r.Context(), user))
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "forbidden", "Monitoring requires Administrator access")
		return
	}
	// Stable internal ID only; do not forward session tokens, names or email.
	w.Header().Set("X-WEBAUTH-USER", "gateway-"+user.ID)
	w.WriteHeader(http.StatusNoContent)
}
