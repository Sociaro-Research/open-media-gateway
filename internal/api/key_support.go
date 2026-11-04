package api

import (
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
)

// keySupport exposes the same published card as browser Support, scoped only
// to the authenticated key's company. It does not accept a company selector.
func (s *Server) keySupport(w http.ResponseWriter, r *http.Request) {
	user, ok := identity.UserFromContext(r.Context())
	company := identity.OrganizationFromContext(r.Context()).ID
	if !ok || user.ID == identity.SystemOwnerID || identity.APIKeyID(r.Context()) == nil || company == "" {
		writeError(w, http.StatusForbidden, "company_key_required", "Use a company API key to view support contacts")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Support is selected by the authenticated company key")
		return
	}
	repo, ok := s.system.(org.SupportReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Support contacts are unavailable")
		return
	}
	result, err := repo.OrganizationSupport(r.Context(), user.ID, company)
	if err != nil {
		orgError(w, err)
		return
	}
	s.writeViewerJSON(w, r, http.StatusOK, result)
}
