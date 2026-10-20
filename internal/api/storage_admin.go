package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

type StorageAdministration interface {
	Get(context.Context) (storage.Settings, error)
	Update(context.Context, storage.Update) (storage.Settings, error)
	Check(context.Context, storage.Update) error
}

func (s *Server) getStorageSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.storageAdmin.Get(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "storage settings could not be read")
		return
	}
	s.writeViewerJSON(w, r, http.StatusOK, settings)
}
func (s *Server) updateStorageSettings(w http.ResponseWriter, r *http.Request) {
	s.storageSettingsRequest(w, r, false)
}
func (s *Server) checkStorageSettings(w http.ResponseWriter, r *http.Request) {
	s.storageSettingsRequest(w, r, true)
}
func (s *Server) storageSettingsRequest(w http.ResponseWriter, r *http.Request, checkOnly bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	defer r.Body.Close()
	var input storage.Update
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must contain one storage settings object")
		return
	}
	input.BrowserOrigin, _ = r.Context().Value(storageBrowserOriginKey{}).(string)
	if input.BrowserOrigin == "" {
		input.BrowserOrigin = r.Header.Get("Origin")
	}
	if r.PathValue("org") != "" && input.Driver != "gateway" && input.Driver != "s3" {
		writeError(w, 400, "invalid_request", "organization storage must be gateway or s3")
		return
	}
	if r.PathValue("org") == "" && input.Driver == "gateway" {
		writeError(w, 400, "invalid_request", "gateway storage selection belongs to an organization")
		return
	}
	if input.Driver == "local" && s.system != nil {
		admin, err := s.system.IsAdmin(r.Context(), identity.ActorID(r.Context()))
		if err != nil {
			systemError(w, err)
			return
		}
		if !admin && identity.ActorID(r.Context()) != identity.SystemOwnerID {
			writeError(w, 403, "local_storage_forbidden", "local storage is available only to administrators; use personal S3 or shared storage")
			return
		}
	}
	// Connection probes can take longer than the default server write deadline.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(45 * time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if checkOnly {
		if err := s.storageAdmin.Check(ctx, input); err != nil {
			if errors.Is(err, storage.ErrBrowserCORS) {
				writeError(w, http.StatusBadRequest, "storage_browser_setup_required", err.Error())
			} else if errors.Is(err, storage.ErrInvalidConfig) {
				writeError(w, http.StatusBadRequest, "storage_configuration_invalid", err.Error())
			} else {
				writeError(w, http.StatusBadRequest, "storage_check_failed", "Connection check failed. Verify endpoint, region, bucket, credentials and object read/write/delete permissions.")
			}
			return
		}
		s.writeViewerJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	settings, err := s.storageAdmin.Update(ctx, input)
	if errors.Is(err, storage.ErrInvalidConfig) {
		writeError(w, http.StatusBadRequest, "storage_configuration_invalid", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "storage settings could not be saved")
		return
	}
	s.writeViewerJSON(w, r, http.StatusOK, settings)
}

// Organization storage is configured by its administrators, independently of keys.
func (s *Server) organizationStorage(w http.ResponseWriter, r *http.Request) {
	repo := s.organizationStore()
	if repo == nil || s.storageAdmin == nil {
		writeError(w, 503, "unavailable", "organization storage is unavailable")
		return
	}
	id := r.PathValue("org")
	if bound := credentialOrganization(r.Context()); bound != "" && bound != id {
		orgError(w, org.ErrForbidden)
		return
	}
	role, err := repo.OrganizationAccess(r.Context(), identity.ActorID(r.Context()), id)
	if err != nil {
		orgError(w, err)
		return
	}
	if !org.Manage(role) {
		orgError(w, org.ErrForbidden)
		return
	}
	// System administration does not imply membership or storage management rights.
	canManage := role == org.Manager || role == org.Supermanager
	if role == "system_admin" {
		memberships, e := repo.Organizations(r.Context(), identity.ActorID(r.Context()), false)
		if e != nil {
			orgError(w, e)
			return
		}
		for _, membership := range memberships {
			if membership.ID == id && (membership.Role == org.Manager || membership.Role == org.Supermanager) {
				canManage = true
				break
			}
		}
	}
	if r.Method != "GET" && !canManage {
		orgError(w, org.ErrForbidden)
		return
	}
	ctx := identity.WithOrganization(r.Context(), id, true)
	ctx = identity.WithResourceOwners(ctx, identity.ProviderOwnerID(ctx), id)
	r = r.WithContext(ctx)
	if r.Method == "GET" {
		settings, err := s.storageAdmin.Get(ctx)
		if err != nil {
			writeError(w, 503, "unavailable", "organization storage could not be read")
			return
		}
		if settings.Driver == "local" {
			settings.Driver = "gateway"
		}
		var allowance any
		if s.system != nil {
			policy, e := s.system.Settings(ctx)
			if e != nil {
				systemError(w, e)
				return
			}
			allowance = policy.SharedStorageLimitBytes
		}
		var visibleSettings any = settings
		if !canManage {
			visibleSettings = map[string]string{"driver": settings.Driver}
		}
		s.writeViewerJSON(w, r, 200, map[string]any{"settings": visibleSettings, "gateway_allowance_bytes": allowance, "can_manage": canManage})
		return
	}
	s.storageSettingsRequest(w, r, r.Method == "POST")
}
