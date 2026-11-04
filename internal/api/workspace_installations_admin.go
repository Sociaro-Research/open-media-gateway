package api

import (
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

// These company-wide operations replace the former personal administration
// endpoints. An installation's company key cannot use them.
func (s *Server) organizationInstallationAccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !centralInstallation(r) || !browserWorkspaceAccount(w, r) {
		if !centralInstallation(r) {
			workspaceAuthorizationError(w, wa.ErrForbidden)
		}
		return
	}
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin {
		workspaceAuthorizationError(w, wa.ErrForbidden)
		return
	}
	company := r.PathValue("company")
	if _, err := uuid.Parse(company); company != "" && err != nil {
		workspaceAuthorizationError(w, wa.ErrUnavailable)
		return
	}
	if !s.workspaceThrottle(w, r, "organization-installations", 60) {
		return
	}
	repo, ok := s.workspaceAuthorization.Repo.(wa.InstallationAdministration)
	if !ok {
		writeError(w, 503, "workspace_onboarding_unavailable", "Installation administration is unavailable")
		return
	}
	if company == "" {
		counts, err := repo.InstallationAccessSummary(r.Context(), identity.ActorID(r.Context()))
		if err != nil {
			workspaceAuthorizationError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"data": counts})
		return
	}
	if r.Method == http.MethodDelete {
		id := r.PathValue("id")
		if _, err := uuid.Parse(id); err != nil {
			workspaceAuthorizationError(w, wa.ErrUnavailable)
			return
		}
		if err := repo.RevokeOrganizationInstallation(r.Context(), identity.ActorID(r.Context()), company, id); err != nil {
			workspaceAuthorizationError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"revoked": true})
		return
	}
	grants, err := repo.OrganizationInstallationGrants(r.Context(), identity.ActorID(r.Context()), company)
	if err != nil {
		workspaceAuthorizationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": grants})
}
