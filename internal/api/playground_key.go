package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/playgroundkey"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

// These account operations return an ordinary bearer secret. They never provide
// a session authentication alternative for generation or arbitrary key retrieval.
func (s *Server) playgroundKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if s.auth == nil || len(r.Header.Values("Authorization")) > 0 || identity.APIKeyID(r.Context()) != nil {
		writeError(w, 403, "browser_session_required", "Use your signed-in account to restore its Playground key")
		return
	}
	user, err := s.auth.Authenticate(r)
	if err != nil || user.ID != identity.ActorID(r.Context()) {
		writeError(w, 401, "unauthorized", "Sign in again")
		return
	}
	if s.playgroundKeys == nil {
		writeError(w, 503, "playground_key_unavailable", "Account key storage is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	defer r.Body.Close()
	var in struct {
		OrganizationID string `json:"organization_id,omitempty"`
		RequestID      string `json:"request_id,omitempty"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var decodeErr error
	if r.URL.Path == "/v1/playground-key/create" {
		decodeErr = dec.Decode(&in)
	} else {
		var workspace struct {
			OrganizationID string `json:"organization_id,omitempty"`
		}
		decodeErr = dec.Decode(&workspace)
		in.OrganizationID = workspace.OrganizationID
	}
	if decodeErr != nil || dec.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid_request", "Send one workspace object")
		return
	}
	if in.OrganizationID != "" {
		parsed, parseErr := uuid.Parse(in.OrganizationID)
		if parseErr != nil {
			writeError(w, 400, "invalid_request", "Invalid workspace")
			return
		}
		in.OrganizationID = parsed.String()
	}
	if allowed, e := s.keyOrganization(r.Context(), user.ID, in.OrganizationID); e != nil || !allowed {
		if e != nil {
			orgError(w, e)
		} else {
			writeError(w, 403, "forbidden", "This account cannot create or restore a key for that workspace")
		}
		return
	}
	if settings, ok := r.Context().Value(systemSettingsKey{}).(system.Settings); ok {
		if in.OrganizationID != "" && !settings.SharedResources {
			writeError(w, 403, "shared_resources_disabled", "Shared providers and storage are disabled")
			return
		}
		if in.OrganizationID == "" && !settings.PersonalProviders {
			admin, e := s.systemAdmin(r.Context())
			if e != nil || !admin {
				writeError(w, 403, "personal_providers_disabled", "Personal providers are disabled")
				return
			}
		}
	}
	var key apikey.Key
	var secret string
	if r.URL.Path == "/v1/playground-key/create" {
		if _, err = uuid.Parse(in.RequestID); err != nil {
			writeError(w, 400, "invalid_request", "A stable UUID request_id is required")
			return
		}
		if !s.checkLimits(w, r, in.OrganizationID, "key_create") {
			return
		}
		key, secret, err = s.playgroundKeys.Create(r.Context(), user.ID, in.OrganizationID, in.RequestID)
	} else {
		key, secret, err = s.playgroundKeys.Restore(r.Context(), user.ID, in.OrganizationID)
	}
	switch {
	case errors.Is(err, playgroundkey.ErrMissing):
		writeError(w, 404, "playground_key_missing", "Create a Playground key to save it to your account")
		return
	case errors.Is(err, playgroundkey.ErrInactive):
		writeError(w, 410, "playground_key_inactive", "Your saved key expired or was revoked. Create a new Playground key explicitly")
		return
	case errors.Is(err, playgroundkey.ErrConflict):
		writeError(w, 409, "playground_key_changed", "This creation request was superseded. Review the current saved key")
		return
	case errors.Is(err, apikey.ErrForbidden), errors.Is(err, apikey.ErrNotFound):
		writeError(w, 403, "forbidden", "This account cannot create or restore a key for that workspace")
		return
	case err != nil:
		if errors.Is(err, playgroundkey.ErrUnavailable) {
			writeError(w, 503, "playground_key_unavailable", "Saved key could not be decrypted or verified")
		} else {
			limitError(w, err)
		}
		return
	}
	writeJSON(w, 200, map[string]any{"key": key, "secret": secret})
}
