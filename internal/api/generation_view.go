package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
)

func (s *Server) generationView(r *http.Request, task core.Task) any {
	view := core.GenerationView(task)
	// Keep the native ID intact for provider_model during JSON serialization.
	publicModel := view.Model
	if task.PublicModel != "" {
		publicModel = task.PublicModel
	}
	role := identity.OrganizationFromContext(r.Context()).Role
	if role != "" && !org.OwnContent(role) {
		// An allowlist prevents provider errors, extensions and signed URLs leaking content.
		return map[string]any{"id": view.ID, "object": view.Object, "status": view.Status, "model": publicModel, "media_type": view.MediaType, "created_at": view.CreatedAt, "updated_at": view.UpdatedAt, "user_id": task.ActorID, "identity_snapshot": task.IdentitySnapshot, "api_key_id": task.APIKeyID, "usage": safeUsage(task.Usage), "cost": task.Cost, "status_url": view.StatusURL, "content_access": false, "can_delete": false}
	}
	// Work on the serialized representation to keep provider extensions intact.
	fields := generationWithPermissions(view, role)
	if strings.HasPrefix(r.URL.Path, "/v1/organizations/") {
		if data, ok := fields.(map[string]json.RawMessage); ok && len(task.IdentitySnapshot) > 0 {
			data["identity_snapshot"] = task.IdentitySnapshot
		}
	}
	return fields
}
func (s *Server) writeGenerationJSON(w http.ResponseWriter, r *http.Request, status int, task core.Task) {
	s.writeViewerJSON(w, r, status, s.generationView(r, task))
}

func safeUsage(usage *core.ProviderUsage) *core.ProviderUsage {
	if usage == nil {
		return nil
	}
	copy := *usage
	copy.Raw = nil
	return &copy
}
func generationWithPermissions(view core.Generation, role string) any {
	b, err := json.Marshal(view)
	if err != nil {
		return view
	}
	fields := map[string]json.RawMessage{}
	if json.Unmarshal(b, &fields) != nil {
		return view
	}
	fields["content_access"] = json.RawMessage(`true`)
	canDelete := role == "" || org.FullControl(role) || role == org.Developer || role == org.MemberRole
	if canDelete {
		fields["can_delete"] = json.RawMessage(`true`)
	} else {
		fields["can_delete"] = json.RawMessage(`false`)
	}
	return fields
}
