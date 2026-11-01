package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/markup"
	registry "github.com/Sociaro-Research/open-media-gateway/plugins/provider"
	"github.com/google/uuid"
)

func (s *Server) markupSettings(w http.ResponseWriter, r *http.Request) {
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin && !s.requireCommercialPermission(w, r, "pricing") {
		return
	}
	repo, ok := s.system.(markup.Repository)
	if !ok {
		writeError(w, 503, "unavailable", "markup settings are unavailable")
		return
	}
	org := r.PathValue("org")
	if org != "" {
		if _, err := uuid.Parse(org); err != nil {
			writeError(w, 400, "invalid_request", "invalid organization ID")
			return
		}
	}
	if org != "" && credentialOrganization(r.Context()) != "" && credentialOrganization(r.Context()) != org {
		writeError(w, 403, "forbidden", "API key belongs to another organization")
		return
	}
	if readMethod(r.Method) {
		p, err := repo.Markups(r.Context(), identity.ActorID(r.Context()), org)
		if err != nil {
			systemError(w, err)
			return
		}
		writeJSON(w, 200, p)
		return
	}
	var input struct {
		Default    json.RawMessage `json:"default_percent"`
		Exceptions []markup.Rule   `json:"exceptions"`
		Version    int64           `json:"version"`
	}
	var next markup.Config
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid_request", "send one markup configuration with valid percentages")
		return
	}
	if len(input.Default) == 0 || input.Exceptions == nil || json.Unmarshal(input.Default, &next.Default) != nil {
		writeError(w, 400, "invalid_request", "default_percent and an exceptions array are required")
		return
	}
	next.Exceptions = input.Exceptions
	next.Version = input.Version
	if err := next.Validate(org == ""); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	for _, rule := range next.Exceptions {
		p, ok := registry.Find(rule.Provider)
		valid := ok && p.ID == rule.Provider
		if !valid && s.providerAdmin != nil {
			connection, e := s.providerAdmin.Get(identity.WithResourceOwners(r.Context(), identity.SharedOwnerID, identity.SharedOwnerID), rule.Provider)
			valid = e == nil && connection.ID == rule.Provider
		}

		if !valid {
			writeError(w, 400, "invalid_request", "unknown provider: "+rule.Provider)
			return
		}
	}
	p, err := repo.SaveMarkups(r.Context(), identity.ActorID(r.Context()), org, next)
	if err != nil {
		systemError(w, err)
		return
	}
	writeJSON(w, 200, p)
}

// Use shared connection catalogs, regardless of the administrator's memberships.
// Include model metadata so every administrative picker can show release dates and details.
func (s *Server) markupCatalog(w http.ResponseWriter, r *http.Request) {
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin && !s.requireCommercialPermission(w, r, "pricing") {
		return
	}
	type model struct {
		core.MediaModel
		Name string `json:"name"`
	}
	type provider struct {
		IconURL        string  `json:"icon_url,omitempty"`
		IconMonochrome bool    `json:"icon_monochrome,omitempty"`
		ID             string  `json:"id"`
		Name           string  `json:"name"`
		Models         []model `json:"models"`
	}
	ctx := identity.WithResourceOwners(r.Context(), identity.SharedOwnerID, identity.SharedOwnerID)
	catalog, err := s.applyModelAccess(ctx, s.service.ListProviders(ctx), true)
	if err != nil {
		systemError(w, err)
		return
	}
	result := []provider{}
	seen := map[string]bool{}
	for _, item := range catalog {
		if item.ID == "mock" {
			continue
		}
		p := provider{ID: item.ID, Name: item.Name, Models: []model{}, IconURL: providerLogoForViewer(item.IconURL, r), IconMonochrome: item.IconMonochrome}
		if d, ok := registry.Find(item.ID); ok {
			if p.Name == "" {
				p.Name = d.Name
			}
		}
		for _, m := range item.Models {
			p.Models = append(p.Models, model{MediaModel: m, Name: m.DisplayName})
		}
		result = append(result, p)
		seen[item.ID] = true
	}
	// Keep builtin choices for existing rules whose connection is unavailable.
	for _, d := range registry.All() {
		if d.ID == "mock" || seen[d.ID] {
			continue
		}
		p := provider{ID: d.ID, Name: d.Name, Models: []model{}}
		result = append(result, p)
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"data": result})
}
