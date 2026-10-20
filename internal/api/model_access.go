package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/modelaccess"
	"github.com/google/uuid"
)

type modelCatalogKey struct{}

type modelSettingView struct {
	modelaccess.Model
	ProviderName           string           `json:"provider_name,omitempty"`
	ProviderIconURL        string           `json:"provider_icon_url,omitempty"`
	ProviderIconMonochrome bool             `json:"provider_icon_monochrome,omitempty"`
	Capabilities           *core.MediaModel `json:"capabilities,omitempty"`
}

// All consumption catalogs and admission resolve the same filtered snapshot.
func (s *Server) modelCatalogPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.modelAccess == nil || !(isModelCatalogPath(r.URL.Path) || r.Method == "POST" && isGenerationPath(r.URL.Path)) {
			next.ServeHTTP(w, r)
			return
		}
		result, err := s.applyModelAccess(r.Context(), s.service.ListProviders(r.Context()), false)
		if err != nil {
			writeError(w, 503, "model_settings_unavailable", "model availability could not be checked")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), modelCatalogKey{}, result)))
	})
}

func (s *Server) modelSettings(w http.ResponseWriter, r *http.Request) {
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin && !s.requireCommercialPermission(w, r, "models") {
		return
	}
	if s.modelAccess == nil {
		writeError(w, 503, "unavailable", "model settings are unavailable")
		return
	}
	org := r.PathValue("org")
	if org != "" {
		if _, err = uuid.Parse(org); err != nil {
			writeError(w, 400, "invalid_request", "invalid organization ID")
			return
		}
		if bound := credentialOrganization(r.Context()); bound != "" && bound != org {
			writeError(w, 403, "forbidden", "API key belongs to another organization")
			return
		}
	}
	if r.Method == "GET" {
		capabilities := map[string]core.MediaModel{}
		providerNames := map[string]string{}
		providerIcons := map[string]string{}
		providerMonochrome := map[string]bool{}
		// Administrative discovery includes hidden models and all shared connections.
		if s.providerAdmin != nil {
			ctx := identity.WithResourceOwners(r.Context(), identity.SharedOwnerID, identity.SharedOwnerID)
			providers, e := s.providerAdmin.List(ctx)
			if e != nil {
				writeError(w, 503, "unavailable", "provider models could not be read")
				return
			}
			refs := []modelaccess.Ref{}
			for _, p := range providers {
				providerNames[p.ID] = p.Name
				providerIcons[p.ID] = p.IconURL
				providerMonochrome[p.ID] = p.IconMonochrome
				for _, m := range p.Models {
					capabilities[modelaccess.Key(p.ID, m.ID)] = m
					refs = append(refs, modelaccess.Ref{Provider: p.ID, Model: m.ID, Name: m.DisplayName})
				}
			}
			if e = s.modelAccess.Ensure(ctx, refs); e != nil {
				systemError(w, e)
				return
			}
		}
		models, e := s.modelAccess.List(r.Context(), org)
		if e != nil {
			systemError(w, e)
			return
		}
		views := make([]modelSettingView, 0, len(models))
		for _, model := range models {
			view := modelSettingView{Model: model, ProviderName: providerNames[model.Provider], ProviderIconURL: providerIcons[model.Provider], ProviderIconMonochrome: providerMonochrome[model.Provider]}
			if capability, ok := capabilities[modelaccess.Key(model.Provider, model.Model)]; ok {
				capability.PublicID = model.PublicID
				view.Capabilities = &capability
			}
			views = append(views, view)
		}
		if org == "" {
			writeJSON(w, 200, map[string]any{"data": views})
			return
		}
		access, e := s.modelAccess.Organization(r.Context(), identity.ActorID(r.Context()), org)
		if e != nil {
			systemError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"data": views, "model_ids": access.ModelIDs, "version": access.Version})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if org != "" {
		var input modelaccess.Access
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || input.ModelIDs == nil || input.Version < 1 || len(input.ModelIDs) > 10000 {
			writeError(w, 400, "invalid_request", "send model_ids and the current version")
			return
		}
		seen := map[string]bool{}
		for _, id := range input.ModelIDs {
			if _, e := uuid.Parse(id); e != nil || seen[id] {
				writeError(w, 400, "invalid_request", "invalid or duplicate model ID")
				return
			}
			seen[id] = true
		}
		result, e := s.modelAccess.SaveOrganization(r.Context(), identity.ActorID(r.Context()), org, input)
		if e != nil {
			systemError(w, e)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	id := r.PathValue("model")
	if _, e := uuid.Parse(id); e != nil {
		writeError(w, 400, "invalid_request", "invalid model ID")
		return
	}
	var input modelaccess.Update
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid_request", "send public_id, enabled_by_default and version")
		return
	}
	if e := input.Validate(); e != nil {
		writeError(w, 400, "invalid_request", e.Error())
		return
	}
	result, e := s.modelAccess.Update(r.Context(), identity.ActorID(r.Context()), id, input)
	if e != nil {
		systemError(w, e)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) applyModelAccess(ctx context.Context, catalog []core.ProviderDescriptor, includeHidden bool) ([]core.ProviderDescriptor, error) {
	if s.modelAccess == nil {
		return catalog, nil
	}

	refs := []modelaccess.Ref{}
	for _, p := range catalog {
		for _, m := range p.Models {
			refs = append(refs, modelaccess.Ref{Provider: p.ID, Model: m.ID, Name: m.DisplayName})
		}
	}
	if err := s.modelAccess.Ensure(ctx, refs); err != nil {
		return nil, err
	}
	settings, err := s.modelAccess.List(ctx, identity.OrganizationFromContext(ctx).ID)
	if err != nil {
		return nil, err
	}
	byKey := map[string]modelaccess.Model{}
	for _, m := range settings {
		byKey[modelaccess.Key(m.Provider, m.Model)] = m
	}
	result := []core.ProviderDescriptor{}
	for _, p := range catalog {
		models := []core.MediaModel{}
		for _, m := range p.Models {
			policy, ok := byKey[modelaccess.Key(p.ID, m.ID)]
			if !ok {
				return nil, errors.New("model registration is incomplete")
			}
			if !includeHidden && !policy.EnabledByDefault && !policy.Granted {
				continue
			}
			m.PublicID = policy.PublicID
			models = append(models, m)
		}
		if len(models) == 0 {
			continue
		}
		p.Models = models
		if !slices.ContainsFunc(models, func(m core.MediaModel) bool { return m.ID == p.DefaultModel }) {
			p.DefaultModel = models[0].ID
		}
		result = append(result, p)
	}
	return result, nil
}
