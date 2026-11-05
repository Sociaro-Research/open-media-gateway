package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	user, ok := identity.UserFromContext(r.Context())
	if !ok || user.ID == identity.SystemOwnerID || r.Header.Get("Authorization") != "" || identity.APIKeyID(r.Context()) != nil {
		writeError(w, 403, "browser_session_required", "Sign in to update your profile")
		return
	}
	var in auth.ProfileUpdate
	if !decodeSystem(w, r, &in) {
		return
	}
	if in.ColorPreset != nil && !readBranding(r.Context(), s.brandingReader()).HasPreset(*in.ColorPreset) {
		field, message := auth.ErrProfileColorPreset.Diagnostic()
		writeAPIError(w, 400, "invalid_profile", message, &field)
		return
	}
	updater, ok := s.auth.(interface {
		UpdateProfile(context.Context, string, auth.ProfileUpdate) (identity.User, error)
	})
	if !ok {
		writeError(w, 503, "unavailable", "Profile settings are unavailable")
		return
	}
	updated, err := updater.UpdateProfile(r.Context(), user.ID, in)
	if errors.Is(err, auth.ErrInvalidProfile) {
		var validation *auth.ProfileValidationError
		if errors.As(err, &validation) {
			field, message := validation.Diagnostic()
			writeAPIError(w, 400, "invalid_profile", message, &field)
		} else {
			writeError(w, 400, "invalid_profile", "Check the documented profile fields and correct the request before saving again")
		}
		return
	}
	if err != nil {
		writeError(w, 500, "profile_update_failed", "Could not save profile")
		return
	}
	updated.SessionID = user.SessionID
	s.writeViewerJSON(w, r, http.StatusOK, updated)
}
