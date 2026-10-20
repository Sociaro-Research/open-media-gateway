package api

import (
	"errors"
	"net/http"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/google/uuid"
)

func (s *Server) clientOnboarding(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	manager, e := s.isClientManager(r.Context())
	if e != nil {
		orgError(w, e)
		return
	}
	if !manager || identity.APIKeyID(r.Context()) != nil {
		orgError(w, org.ErrForbidden)
		return
	}
	if s.onboarding == nil {
		writeError(w, 503, "unavailable", "Company onboarding is unavailable.")
		return
	}
	actor := identity.ActorID(r.Context())
	if r.Method == http.MethodGet {
		items, e := s.onboarding.Repo.ClientOnboardings(r.Context(), actor)
		if e != nil {
			onboardingError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"data": items})
		return
	}
	var c org.Client
	if r.PathValue("client") == "" {
		var in org.ClientInput
		if !decodeSystem(w, r, &in) {
			return
		}
		c, e = s.onboarding.Create(r.Context(), actor, in)
	} else {
		if r.PathValue("action") != "resend" && r.PathValue("action") != "revoke" {
			orgError(w, org.ErrInvalid)
			return
		}
		id := r.PathValue("client")
		if _, e = uuid.Parse(id); e != nil {
			orgError(w, org.ErrInvalid)
			return
		}
		var in struct {
			Operation string `json:"operation_id"`
			Version   int64  `json:"version"`
		}
		if !decodeSystem(w, r, &in) {
			return
		}
		c, e = s.onboarding.Change(r.Context(), actor, id, in.Operation, in.Version, r.PathValue("action") == "revoke")
	}
	if e != nil {
		onboardingError(w, e)
		return
	}
	writeJSON(w, 200, c)
}
func onboardingError(w http.ResponseWriter, e error) {
	var duplicate *org.Duplicate
	if errors.As(e, &duplicate) {
		writeJSON(w, 409, map[string]any{"error": map[string]any{"code": "duplicate_company", "message": duplicate.Error(), "duplicate": duplicate}})
		return
	}
	if errors.Is(e, org.ErrOnboardingLimit) {
		writeError(w, 429, "client_change_limit", e.Error())
		return
	}
	orgError(w, e)
}
