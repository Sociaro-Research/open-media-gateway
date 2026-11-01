package api

import (
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"net/http"
	"strings"
)

func remoteAuthorizationError(w http.ResponseWriter, err error) {
	if errors.Is(err, wa.ErrKeyLimit) || errors.Is(err, wa.ErrPending) || errors.Is(err, wa.ErrSlow) || errors.Is(err, wa.ErrUnavailable) || errors.Is(err, wa.ErrForbidden) || errors.Is(err, wa.ErrDisabled) {
		workspaceAuthorizationError(w, err)
		return
	}
	remoteError(w, err)
}
func (s *Server) remoteAuthorization(w http.ResponseWriter, r *http.Request) {
	if s.remoteWorkspaceManager == nil {
		remoteError(w, remote.ErrUnavailable)
		return
	}
	owner := identity.ActorID(r.Context())
	if readMethod(r.Method) {
		current, err := s.remoteWorkspaceManager.Authorization(r.Context(), owner)
		if errors.Is(err, remote.ErrNotFound) {
			s.writeViewerJSON(w, r, 200, map[string]bool{"pending": false})
			return
		}
		if err != nil {
			remoteAuthorizationError(w, err)
			return
		}
		s.writeViewerJSON(w, r, 200, current)
		return
	}
	var in struct {
		Origin       string `json:"origin"`
		Name         string `json:"client_name"`
		ClientOrigin string `json:"client_origin"`
		Version      int64  `json:"version"`
		ID           string `json:"id"`
		Consent      bool   `json:"consent"`
	}
	if !decodeSystem(w, r, &in) {
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.remoteWorkspaceManager.CancelAuthorization(r.Context(), owner, in.ID); err != nil {
			remoteAuthorizationError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"cancelled": true})
		return
	}
	if !in.Consent || in.Version < 0 {
		writeError(w, 400, "consent_required", "Confirm sending this installation's name and origin to the selected trusted server")
		return
	}
	view, err := s.remoteWorkspaceManager.BeginAuthorization(r.Context(), owner, in.Origin, strings.TrimSpace(in.Name), in.ClientOrigin, in.Version)
	if err != nil {
		remoteAuthorizationError(w, err)
		return
	}
	writeJSON(w, 200, view)
}
func (s *Server) pollRemoteAuthorization(w http.ResponseWriter, r *http.Request) {
	if s.remoteWorkspaceManager == nil {
		remoteError(w, remote.ErrUnavailable)
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if !decodeSystem(w, r, &in) {
		return
	}
	record, err := s.remoteWorkspaceManager.PollAuthorization(r.Context(), identity.ActorID(r.Context()), in.ID)
	if err != nil {
		remoteAuthorizationError(w, err)
		return
	}
	writeJSON(w, 200, record)
}
