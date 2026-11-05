package api

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/providers"
)

func (s *Server) listAdminProviders(w http.ResponseWriter, r *http.Request) {
	items, err := s.providerAdmin.List(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "provider settings could not be read")
		return
	}
	items, err = s.providerModelSettings(r, items)
	if err != nil {
		writeError(w, 503, "model_settings_unavailable", "model settings could not be read")
		return
	}
	s.writeViewerJSON(w, r, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) getAdminProvider(w http.ResponseWriter, r *http.Request) {
	item, err := s.providerAdmin.Get(r.Context(), r.PathValue("id"))
	if writeProviderError(w, err) {
		return
	}
	items, e := s.providerModelSettings(r, []providers.AdminProvider{item})
	if e != nil {
		writeError(w, 503, "model_settings_unavailable", "model settings could not be read")
		return
	}
	s.writeViewerJSON(w, r, http.StatusOK, items[0])
}

func (s *Server) checkAdminProvider(w http.ResponseWriter, r *http.Request) {
	item, err := s.providerAdmin.Check(r.Context(), r.PathValue("id"))
	if writeProviderError(w, err) {
		return
	}
	items, e := s.providerModelSettings(r, []providers.AdminProvider{item})
	if e != nil {
		writeError(w, 503, "model_settings_unavailable", "model settings could not be read")
		return
	}
	s.writeViewerJSON(w, r, http.StatusOK, items[0])
}

func (s *Server) updateAdminProvider(w http.ResponseWriter, r *http.Request) {
	var update providers.ProviderUpdate
	if !decodeSystemLimit(w, r, &update, 512<<10) {
		return
	}
	if update.Connection != nil || update.Name != nil || update.Archived != nil {
		admin, err := s.systemAdmin(r.Context())
		if err != nil || !admin {
			writeError(w, 403, "forbidden", "administrator access is required")
			return
		}
		if update.Version == nil {
			writeError(w, 400, "invalid_request", "the current provider version is required")
			return
		}
	}
	item, err := s.providerAdmin.Update(r.Context(), r.PathValue("id"), update)
	if writeProviderError(w, err) {
		return
	}
	items, e := s.providerModelSettings(r, []providers.AdminProvider{item})
	if e != nil {
		writeError(w, 503, "model_settings_unavailable", "model settings could not be read")
		return
	}
	s.writeViewerJSON(w, r, http.StatusOK, items[0])
}

func (s *Server) providerTypes(w http.ResponseWriter, r *http.Request) {
	admin, err := s.systemAdmin(r.Context())
	if err != nil || !admin {
		writeError(w, 403, "forbidden", "administrator access is required")
		return
	}
	manager, ok := s.providerAdmin.(interface {
		Types() []providers.ProviderType
	})
	if !ok {
		writeError(w, 503, "unavailable", "provider creation is unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"data": manager.Types()})
}

func (s *Server) createAdminProvider(w http.ResponseWriter, r *http.Request) {
	admin, err := s.systemAdmin(r.Context())
	if err != nil || !admin {
		writeError(w, 403, "forbidden", "administrator access is required")
		return
	}
	manager, ok := s.providerAdmin.(interface {
		Create(context.Context, providers.CreateConnection) (providers.AdminProvider, error)
	})
	if !ok {
		writeError(w, 503, "unavailable", "provider creation is unavailable")
		return
	}
	var input providers.CreateConnection
	if !decodeSystemLimit(w, r, &input, 4096) {
		return
	}
	item, err := manager.Create(r.Context(), input)
	if writeProviderError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func writeProviderError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, providers.ErrUnsupportedProvider), errors.Is(err, providerconfig.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "provider not found")
	case errors.Is(err, providers.ErrProviderUnconfigured):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		// Credential checks and invalid endpoints are actionable configuration
		// failures, not outages of the Gateway itself.
		writeError(w, http.StatusBadRequest, "provider_configuration_invalid", err.Error())
	}
	return true
}

func (s *Server) providerModelSettings(r *http.Request, items []providers.AdminProvider) ([]providers.AdminProvider, error) {
	if s.modelAccess == nil {
		return items, nil
	}
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		return nil, err
	}
	catalog := []core.ProviderDescriptor{}
	for _, p := range items {
		catalog = append(catalog, core.ProviderDescriptor{ID: p.ID, Models: p.Models})
	}
	catalog, err = s.applyModelAccess(r.Context(), catalog, admin)
	if err != nil {
		return nil, err
	}
	result := slices.Clone(items)
	for i := range result {
		result[i].Models = []core.MediaModel{}
		result[i].MediaCounts = map[string]int{}
		for _, p := range catalog {
			if p.ID == result[i].ID {
				result[i].Models = p.Models
			}
		}
		result[i].ModelCount = len(result[i].Models)
		for _, m := range result[i].Models {
			result[i].MediaCounts[m.MediaType]++
		}
	}
	return result, nil
}
