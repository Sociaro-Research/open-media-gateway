package api

import (
	"net/http"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
)

// Resolve live membership on every request, including requests made with old keys.
func (s *Server) organizationPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repository := s.organizationStore()
		id := r.PathValue("org")
		if id == "" {
			id = identity.OrganizationFromContext(r.Context()).ID
		}
		if id == "" || repository == nil {
			next.ServeHTTP(w, r)
			return
		}
		if bound := credentialOrganization(r.Context()); bound != "" && bound != id {
			orgError(w, org.ErrForbidden)
			return
		}
		role, err := repository.OrganizationAccess(r.Context(), identity.ActorID(r.Context()), id)
		if err != nil {
			orgError(w, err)
			return
		}
		r = r.WithContext(identity.WithOrganizationRole(r.Context(), id, role))
		path := r.URL.Path
		references := strings.Contains(path, "/references")
		content := strings.HasSuffix(path, "/content") || strings.HasSuffix(path, "/last-frame") || strings.HasSuffix(path, "/media-access")
		if references && !org.OwnContent(role) && r.Method != "POST" {
			orgError(w, org.ErrForbidden)
			return
		}
		if content && !org.OwnContent(role) {
			orgError(w, org.ErrForbidden)
			return
		}
		if r.Method == "DELETE" && (references || content) && !org.FullControl(role) && role != org.Developer && role != org.MemberRole {
			orgError(w, org.ErrForbidden)
			return
		}
		if role == org.ContentOfficer && (r.Method != "GET" && r.Method != "HEAD") {
			orgError(w, org.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
