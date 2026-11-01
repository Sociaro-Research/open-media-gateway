package api

import (
	"net/http"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func centralInstallation(r *http.Request) bool {
	return installation.FromContext(r.Context()).Role == "central"
}
func centralModeError(w http.ResponseWriter) {
	writeError(w, 403, "central_companies_only", "The configured central installation supports service for companies only")
}
func (s *Server) writeInstallationSettings(w http.ResponseWriter, r *http.Request, settings system.Settings) {
	value := struct {
		system.Settings
		installation.Metadata
	}{settings, installation.FromContext(r.Context())}
	s.writeViewerJSON(w, r, 200, value)
}

// Apply only to new remote-client operations. Existing records remain readable;
// explicit disconnect/cancel cleanup retains its authenticated handler and safeguards.
func centralRemoteClientRequest(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/v1/system/remote-workspace") {
		return false
	}
	return r.Method == http.MethodPost
}

func installationConsolePolicy(r *http.Request, settings system.Settings) map[string]any {
	policy := settings.Public()
	meta := installation.FromContext(r.Context())
	policy["installation_role"] = meta.Role
	policy["central_origin"] = meta.CentralOrigin
	if meta.Role == "central" {
		policy["personal_providers"] = false
	}
	return policy
}

// New workspace content requires a company. Finishing an upload admitted before
// a role change, historical reads/deletes and global template authoring retain
// their existing owner/role checks and immutable storage snapshots.
func centralNewCompanyContent(r *http.Request) bool {
	path := r.URL.Path
	if r.Method == http.MethodPost && (path == "/v1/provider-resources" || path == "/v1/provider-resources/estimate") {
		return true
	}
	if r.Method == http.MethodPost && strings.HasPrefix(path, "/v1/references") {
		if path == "/v1/references/validate" || strings.HasPrefix(path, "/v1/references/uploads/") && strings.HasSuffix(path, "/complete") {
			return false
		}
		return true
	}
	return r.Method == http.MethodPost && path == "/v1/projects" || r.Method == http.MethodPatch && (strings.HasPrefix(path, "/v1/projects/") || strings.HasPrefix(path, "/v1/generations/") && strings.HasSuffix(path, "/project"))
}
