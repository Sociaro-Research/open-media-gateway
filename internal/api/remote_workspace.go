package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
)

func remoteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, remote.ErrContextRequired):
		writeError(w, 428, "remote_context_required", err.Error())
	case errors.Is(err, remote.ErrNotFound):
		writeError(w, 404, "remote_not_connected", err.Error())
	case errors.Is(err, remote.ErrConflict):
		writeError(w, 409, "remote_connection_changed", err.Error())
	case errors.Is(err, remote.ErrUnavailable):
		writeError(w, 502, "remote_unavailable", err.Error())
	default:
		writeError(w, 400, "remote_invalid", "Check the HTTPS server origin, company ID, generation-only key and request parameters")
	}
}
func (s *Server) remoteWorkspace(w http.ResponseWriter, r *http.Request) {
	if s.remoteWorkspaceManager == nil {
		writeError(w, 503, "remote_unavailable", "Remote workspace connections are not configured on this installation")
		return
	}
	owner := identity.ActorID(r.Context())
	if readMethod(r.Method) {
		record, err := s.remoteWorkspaceManager.Repo.RemoteWorkspace(r.Context(), owner)
		if errors.Is(err, remote.ErrNotFound) {
			s.writeViewerJSON(w, r, 200, remote.Record{})
			return
		}
		if err != nil {
			remoteError(w, err)
			return
		}
		s.writeViewerJSON(w, r, 200, record)
		return
	}
	var in struct {
		Origin         string `json:"origin"`
		Token          string `json:"token"`
		OrganizationID string `json:"organization_id"`
		Version        int64  `json:"version"`
		Consent        bool   `json:"consent"`
	}
	if !decodeSystem(w, r, &in) {
		return
	}
	if in.Version < 0 {
		writeError(w, 400, "invalid_request", "version must be nonnegative")
		return
	}
	if r.Method == http.MethodDelete {
		record, err := s.remoteWorkspaceManager.Repo.DisconnectRemoteWorkspace(r.Context(), owner, in.Version)
		if err != nil {
			remoteError(w, err)
			return
		}
		s.writeViewerJSON(w, r, 200, record)
		return
	}
	if !in.Consent {
		writeError(w, 400, "consent_required", "Confirm sending this workspace's requests and files to the remote service")
		return
	}
	record, err := s.remoteWorkspaceManager.Connect(r.Context(), owner, in.Origin, in.Token, in.OrganizationID, in.Version)
	if err != nil {
		remoteError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, record)
}
func (s *Server) remoteWorkspaceProxy(w http.ResponseWriter, r *http.Request) {
	if s.remoteWorkspaceManager == nil {
		writeError(w, 503, "remote_unavailable", "Remote workspace connections are not configured")
		return
	}
	path := "/" + r.PathValue("path")
	if !remote.Allowed(r.Method, path) {
		writeError(w, 404, "remote_operation_unavailable", "This operation is outside the remote workspace pilot")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, remote.MaxJSON))
	if err != nil {
		writeError(w, 413, "request_too_large", "Remote requests are limited to 4 MiB")
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		remoteError(w, remote.ErrRoute)
		return
	}
	expected := r.Header.Get("X-Workspace-Context")
	if len(r.Header.Values("X-Workspace-Context")) > 1 {
		remoteError(w, remote.ErrContextRequired)
		return
	}
	if values, ok := query["workspace_context"]; ok {
		if expected != "" || len(values) != 1 || !readMethod(r.Method) {
			remoteError(w, remote.ErrContextRequired)
			return
		}
		expected = values[0]
		query.Del("workspace_context")
	}
	response, record, token, err := s.remoteWorkspaceManager.Proxy(r.Context(), identity.ActorID(r.Context()), r.Method, path, query.Encode(), body, r.Header.Get("Idempotency-Key"), expected)
	if err != nil {
		remoteError(w, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		if !strings.HasSuffix(path, "/content") && !strings.HasSuffix(path, "/last-frame") {
			writeError(w, 502, "remote_redirect", "Remote API redirects are not supported; configure the final server origin")
			return
		}
		location, e := response.Location()
		if e != nil {
			writeError(w, 502, "remote_download_unavailable", "Remote file link is unavailable")
			return
		}
		_ = response.Body.Close()
		response, e = s.remoteWorkspaceManager.Download(r.Context(), location.String(), r.Method)
		if e != nil {
			writeError(w, 502, "remote_download_unavailable", "Remote file could not be downloaded securely")
			return
		}
		defer response.Body.Close()
	}
	if response.ContentLength > 512<<20 {
		writeError(w, 502, "remote_file_too_large", "Remote downloads are limited to 512 MiB")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if value := response.Header.Get("Retry-After"); value != "" {
		w.Header().Set("Retry-After", value)
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.WriteHeader(response.StatusCode)
		return
	}
	if strings.Contains(response.Header.Get("Content-Type"), "application/json") {
		raw, err := io.ReadAll(io.LimitReader(response.Body, remote.MaxJSON+1))
		if err != nil || len(raw) > remote.MaxJSON {
			writeError(w, 502, "remote_invalid_response", "Remote JSON response is too large or incomplete")
			return
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			writeError(w, 502, "remote_invalid_response", "Remote response is not valid JSON")
			return
		}
		remote.Rewrite(value, record.Origin, token, record.SnapshotID())
		writeJSON(w, response.StatusCode, value)
		return
	}
	if response.StatusCode != 200 && response.StatusCode != 206 {
		writeError(w, response.StatusCode, "remote_failed", "Remote request failed")
		return
	}
	// Media is an authorized download, never an active same-origin document.
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Content-Disposition", `attachment; filename="remote-result"`)
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	if response.ContentLength >= 0 {
		w.Header().Set("Content-Length", fmt.Sprint(response.ContentLength))
	}
	w.WriteHeader(response.StatusCode)
	_, err = io.CopyN(w, response.Body, 512<<20)
	if err != nil && !errors.Is(err, io.EOF) {
		panic(http.ErrAbortHandler)
	}
	if err == nil {
		var extra [1]byte
		if n, readErr := response.Body.Read(extra[:]); n > 0 || readErr != io.EOF {
			// Abort instead of returning a silently truncated successful download.
			panic(http.ErrAbortHandler)
		}
	}
}
