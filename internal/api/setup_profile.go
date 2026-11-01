package api

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"net/http"
)

// Profile selection changes only the confirmed local resource policy. Skip is
// deliberately a client-only action; Sociaro connection has its own lifecycle.
func (s *Server) setupProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Profile       string `json:"profile"`
		AllowPersonal bool   `json:"allow_personal"`
		Version       int64  `json:"version"`
	}
	if !decodeSystem(w, r, &in) {
		return
	}
	if centralInstallation(r) && (in.Profile != "operator" || in.AllowPersonal) {
		centralModeError(w)
		return
	}
	if in.Profile != "personal" && in.Profile != "operator" {
		writeError(w, 400, "invalid_profile", "choose personal or operator; connect Sociaro separately")
		return
	}
	current, err := s.system.Settings(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	current.Version = in.Version
	current.SetupPending = false
	current.SharedResources = in.Profile == "operator"
	current.PersonalProviders = in.Profile == "personal" || in.AllowPersonal
	saved, err := s.system.SaveSettings(r.Context(), identity.ActorID(r.Context()), current)
	if err != nil {
		systemError(w, err)
		return
	}
	s.writeInstallationSettings(w, r, saved)
}
