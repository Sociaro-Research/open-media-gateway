package api

import (
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"net/http"
	"net/url"
)

func remoteAuthorizationError(w http.ResponseWriter, err error) {
	if errors.Is(err, wa.ErrLegacy) || errors.Is(err, wa.ErrKeyLimit) || errors.Is(err, wa.ErrPending) || errors.Is(err, wa.ErrSlow) || errors.Is(err, wa.ErrUnavailable) || errors.Is(err, wa.ErrForbidden) || errors.Is(err, wa.ErrDisabled) {
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
		writeError(w, 400, "consent_required", "Confirm sharing this installation’s name and address with Sociaro")
		return
	}
	central := installation.FromContext(r.Context()).CentralOrigin
	if centralInstallation(r) {
		centralModeError(w)
		return
	}
	if in.Origin != "" {
		requested, e := installation.NormalizeOrigin(in.Origin)
		if e != nil || requested != central {
			writeError(w, 400, "invalid_destination", "Connect to the configured Sociaro service")
			return
		}
	}
	// Only trusted public configuration supplies the label/address sent to Sociaro.
	own, _ := r.Context().Value(storageBrowserOriginKey{}).(string)
	label := "Local installation"
	if own == "" {
		own = "http://localhost"
	} else if parsed, e := url.Parse(own); e == nil {
		label = parsed.Host
	}
	if len(label) > 80 {
		label = label[:80]
	}
	if current, e := s.remoteWorkspaceManager.Repo.RemoteWorkspace(r.Context(), owner); e != nil && !errors.Is(e, remote.ErrNotFound) {
		remoteAuthorizationError(w, e)
		return
	} else if e == nil && current.Active {
		previous, e := installation.NormalizeOrigin(current.Origin)
		if e != nil || previous != central {
			writeError(w, 409, "remote_destination_changed", "This saved connection is preserved. Manage its existing access before connecting to a different service")
			return
		}
	}
	// Retry the retained request with its original display metadata and proof.
	if pending, e := s.remoteWorkspaceManager.Authorization(r.Context(), owner); e == nil {
		prior, e := installation.NormalizeOrigin(pending.Origin)
		if e != nil || prior != central {
			writeError(w, 409, "remote_destination_changed", "Cancel the saved request before connecting to Sociaro")
			return
		}
		label, own = pending.Name, pending.ClientOrigin
	} else if !errors.Is(e, remote.ErrNotFound) {
		remoteAuthorizationError(w, e)
		return
	}
	view, err := s.remoteWorkspaceManager.BeginAuthorization(r.Context(), owner, central, label, own, in.Version)
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
