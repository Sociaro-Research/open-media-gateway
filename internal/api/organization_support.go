package api

import (
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/google/uuid"
	"net/http"
)

func (s *Server) organizationSupport(w http.ResponseWriter, r *http.Request) {
	user, ok := identity.UserFromContext(r.Context())
	if !ok || user.ID == identity.SystemOwnerID || identity.APIKeyID(r.Context()) != nil || r.Header.Get("Authorization") != "" {
		writeError(w, 403, "browser_session_required", "Sign in to view support contacts")
		return
	}
	id := r.PathValue("org")
	if _, err := uuid.Parse(id); err != nil {
		orgError(w, org.ErrInvalid)
		return
	}
	repo, ok := s.system.(org.SupportReader)
	if !ok {
		writeError(w, 503, "unavailable", "Support contacts are unavailable")
		return
	}
	result, err := repo.OrganizationSupport(r.Context(), user.ID, id)
	if err != nil {
		orgError(w, err)
		return
	}
	s.writeViewerJSON(w, r, http.StatusOK, result)
}
