package api

import (
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
	"net/http"
)

func (s *Server) requireCommercialPermission(w http.ResponseWriter, r *http.Request, permission string) bool {
	if _, e := uuid.Parse(r.PathValue("org")); e != nil {
		systemError(w, system.ErrForbidden)
		return false
	}
	repo, ok := s.system.(system.SalesPermissionsRepository)
	if !ok {
		systemError(w, system.ErrForbidden)
		return false
	}
	if err := repo.CommercialPermission(r.Context(), identity.ActorID(r.Context()), r.PathValue("org"), permission); err != nil {
		systemError(w, err)
		return false
	}
	return true
}
func (s *Server) salesPermissions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if identity.APIKeyID(r.Context()) != nil {
		systemError(w, system.ErrForbidden)
		return
	}
	repo, ok := s.system.(system.SalesPermissionsRepository)
	if !ok {
		writeError(w, 503, "unavailable", "sales permissions are unavailable")
		return
	}
	actor := identity.ActorID(r.Context())
	if r.Method == "PUT" {
		target, permission := r.PathValue("employee"), r.PathValue("permission")
		if _, err := uuid.Parse(target); err != nil || !system.SalesPermissionValid(permission) {
			writeError(w, 400, "invalid_request", "invalid employee or permission")
			return
		}
		var input struct {
			Enabled *bool `json:"enabled"`
			Version int64 `json:"version"`
		}
		if !decodeSystem(w, r, &input) {
			return
		}
		if input.Enabled == nil {
			writeError(w, 400, "invalid_request", "enabled is required")
			return
		}
		err := repo.SetSalesPermission(r.Context(), actor, target, permission, system.SalesPermissionChange{Enabled: *input.Enabled, Version: input.Version})
		if errors.Is(err, system.ErrSalesPermission) {
			writeError(w, 400, "invalid_request", err.Error())
			return
		}
		if err != nil {
			systemError(w, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	data, err := repo.SalesPermissions(r.Context(), actor)
	if err != nil {
		systemError(w, err)
		return
	}
	writeJSON(w, 200, data)
}
func (s *Server) organizationSalesPermissions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if identity.APIKeyID(r.Context()) != nil {
		systemError(w, system.ErrForbidden)
		return
	}
	repo, ok := s.system.(system.SalesPermissionsRepository)
	if !ok {
		systemError(w, system.ErrForbidden)
		return
	}
	result := map[string]any{"pricing": false, "models": false, "topup": false}
	for _, p := range []string{"pricing", "models", "topup"} {
		err := repo.CommercialPermission(r.Context(), identity.ActorID(r.Context()), r.PathValue("org"), p)
		if err != nil {
			if errors.Is(err, system.ErrForbidden) {
				continue
			}
			systemError(w, err)
			return
		}
		result[p] = true
	}
	writeJSON(w, 200, result)
}
