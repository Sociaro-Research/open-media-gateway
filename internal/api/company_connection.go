package api

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	"io"
	"mime"
	"net/http"
	"strings"
)

func (s *Server) companyConnectionOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	w.Header().Set("Cache-Control", "no-store, private")
	if centralInstallation(r) || s.auth == nil || s.system == nil || s.remoteWorkspaceManager == nil {
		writeError(w, 404, "unavailable", "Company connection is unavailable")
		return "", false
	}
	user, err := s.auth.Authenticate(r)
	if err != nil {
		writeError(w, 401, "unauthorized", "Sign in to this installation")
		return "", false
	}
	admin, err := s.system.IsAdmin(identity.WithUser(r.Context(), user), user.ID)
	if err != nil {
		writeError(w, 503, "connection_unavailable", "Could not check the installation connection")
		return "", false
	}
	if !admin {
		writeError(w, 403, "forbidden", "This installation connection belongs to its local administrator")
		return "", false
	}
	own, _ := r.Context().Value(storageBrowserOriginKey{}).(string)
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || len(origins) == 1 && origins[0] != own {
		writeError(w, 403, "cross_origin_request", "Use this installation's interface")
		return "", false
	}
	if r.Method != "GET" && r.Method != "HEAD" && (len(origins) != 1 || origins[0] != own) {
		writeError(w, 403, "cross_origin_request", "Use this installation's interface")
		return "", false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		writeError(w, 403, "cross_origin_request", "Use this installation's interface")
		return "", false
	}
	return user.ID, true
}
func (s *Server) companyConnection(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.companyConnectionOwner(w, r)
	if !ok {
		return
	}
	if len(r.Header.Values("Authorization")) > 0 || len(r.Header.Values("X-API-Key")) > 0 {
		writeError(w, 403, "browser_session_required", "Use the signed-in installation account")
		return
	}
	record, secret, err := s.remoteWorkspaceManager.CompanyConnection(r.Context(), owner)
	if errors.Is(err, remote.ErrNotFound) {
		writeJSON(w, 200, map[string]bool{"connected": false})
		return
	}
	if err != nil {
		remoteError(w, err)
		return
	}
	if record.Origin != installation.FromContext(r.Context()).CentralOrigin {
		remoteError(w, remote.ErrConflict)
		return
	}
	writeJSON(w, 200, map[string]any{"connected": true, "connection": record, "key": secret})
}
func companyAPIPath(path string) bool {
	return strings.HasPrefix(path, "/v1/") && !strings.HasPrefix(path, "/v1/system/remote-workspace") || strings.HasPrefix(path, "/provider-assets/") || strings.HasPrefix(path, "/media/") || strings.HasPrefix(path, "/references/") || strings.HasPrefix(path, "/template-media/")
}
func (s *Server) companyKeyProxy(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.companyConnectionOwner(w, r)
	if !ok {
		return
	}
	record, secret, err := s.remoteWorkspaceManager.CompanyConnection(r.Context(), owner)
	if err != nil {
		remoteError(w, err)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/auth/company-api")
	if record.Origin != installation.FromContext(r.Context()).CentralOrigin || !companyAPIPath(path) || strings.ContainsRune(path, 92) || strings.IndexByte(path, 0) >= 0 {
		remoteError(w, remote.ErrRoute)
		return
	}
	switch r.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
	default:
		writeError(w, 405, "method_not_allowed", "Unsupported API method")
		return
	}
	if r.Header.Get("X-Company-Context") != record.SnapshotID() || len(r.Header.Values("X-Company-Context")) != 1 {
		remoteError(w, remote.ErrConflict)
		return
	}
	bearer := r.Header.Get("Authorization")
	if len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare([]byte(bearer), []byte("Bearer "+secret)) != 1 || len(r.Header.Values("X-API-Key")) > 0 {
		remoteError(w, remote.ErrCredential)
		return
	}
	response, err := s.remoteWorkspaceManager.CompanyRequest(r.Context(), record, secret, r, path)
	if err != nil {
		remoteError(w, remote.ErrUnavailable)
		return
	}
	defer response.Body.Close()
	latest, err := s.remoteWorkspaceManager.Repo.RemoteWorkspace(r.Context(), owner)
	if err != nil || !latest.Active || latest.SnapshotID() != record.SnapshotID() {
		remoteError(w, remote.ErrConflict)
		return
	}
	if confirmedCompanyKeyRevocation(response) {
		// The version compare-and-swap also closes the race after the snapshot
		// check: a late response must never disconnect a renewed connection.
		if _, err = s.remoteWorkspaceManager.Repo.DisconnectRemoteWorkspace(r.Context(), owner, record.Version); err != nil {
			remoteError(w, err)
			return
		}
		writeError(w, http.StatusGone, "company_connection_revoked", "Sociaro access was revoked. This installation is no longer connected.")
		return
	}
	// Do not follow upstream redirects or copy cookies. Browser transport also
	// uses redirect:error; external signed-storage URLs remain credential-free.
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Disposition", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Retry-After", "Location"} {
		if values := response.Header.Values(name); len(values) == 1 {
			w.Header().Set(name, values[0])
		}
	}
	w.WriteHeader(response.StatusCode)
	if r.Method != "HEAD" {
		if _, err = io.Copy(w, response.Body); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
}

// Only the central API's typed denial of the exact forwarded credential is
// terminal. Preserve all bytes for unrelated failures and never infer this
// state from an error message, local session failure or a temporary outage.
func confirmedCompanyKeyRevocation(response *http.Response) bool {
	if response.StatusCode != http.StatusUnauthorized {
		return false
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	response.Body = io.NopCloser(io.MultiReader(bytes.NewReader(raw), response.Body))
	if err != nil || len(raw) > 4096 {
		return false
	}
	var problem struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(raw, &problem) == nil && problem.Error.Code == "api_key_revoked"
}
