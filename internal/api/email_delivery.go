package api

import (
	"errors"
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func (s *Server) emailDeliverySettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.emailDelivery == nil {
		writeError(w, 503, "email_unavailable", "Email delivery is not configured on this server.")
		return
	}
	var settings auth.DeliverySettings
	var err error
	if r.Method == http.MethodGet {
		settings, err = s.emailDelivery.Settings(r.Context())
	} else {
		var in struct {
			Provider *string `json:"provider"`
			From     *string `json:"from"`
			APIKey   string  `json:"api_key,omitempty"`
			Version  int64   `json:"version"`
		}
		if !decodeSystem(w, r, &in) {
			return
		}
		if in.Provider == nil || in.From == nil {
			writeError(w, 400, "invalid_request", "provider, from and version are required.")
			return
		}
		settings, err = s.emailDelivery.Update(r.Context(), identity.ActorID(r.Context()), auth.DeliveryUpdate{Provider: *in.Provider, From: *in.From, APIKey: in.APIKey, Version: in.Version})
	}
	if errors.Is(err, auth.ErrDeliverySettings) {
		writeError(w, 400, "invalid_request", "Select Resend and enter an API key and a valid sender address, or select Not configured to remove the connection.")
		return
	}
	if err != nil {
		systemError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, settings)
}
