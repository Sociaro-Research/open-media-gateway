package providerresources

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

// PresetImporter is optional. Only host-verified official catalogue metadata may
// reach it; importing creates a local read-only binding, never a native resource.
type PresetImporter interface {
	ImportPreset(context.Context, Binding) (Binding, error)
}

func presetLabel(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

// ValidatePresetBinding deliberately excludes video-character presets until the
// host can inspect their actual source bytes. Native duration assertions are not
// source evidence. No caller-controlled labels or remote identifiers are allowed.
func ValidatePresetBinding(ctx context.Context, b Binding) error {
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if !ok || scope.ID == "" || scope.Revision == "" || scope.OwnerID != identity.ProviderOwnerID(ctx) ||
		b.OwnerID != identity.OwnerID(ctx) || b.ProviderOwnerID != scope.OwnerID || b.ActorID != identity.ActorID(ctx) ||
		b.ConnectionID != scope.ID || b.Revision != scope.Revision || !presetLabel(b.ProviderID, 512) ||
		!ValidRemoteID(b.RemoteID) || !presetLabel(b.Name, 256) || b.Status != Succeeded || !b.IsPreset ||
		b.Description != "" || b.Session != nil || len(b.Sources) != 0 || b.OriginTaskID != "" || b.BoundVoiceBindingID != "" || b.DeletedAt != nil {
		return ErrInvalid
	}
	for _, id := range []string{b.ID, b.OwnerID, b.ProviderOwnerID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return ErrInvalid
		}
	}
	if b.Kind == "voice" && b.Subtype == "preset" || b.Kind == "element" && b.Subtype == "multi_image" {
		return nil
	}
	return ErrInvalid
}

// ListPresets is called only after the HTTP boundary authorizes a model's preset
// capability. Scope is resolved and pinned before any native catalogue request,
// then checked again so a rotation cannot mint tickets for stale credentials.
func (l *Lifecycle) ListPresets(ctx context.Context, provider, kind string, page, size int) (core.ResourcePage, core.ProviderConnectionScope, error) {
	var empty core.ProviderConnectionScope
	if l == nil || l.current == nil || l.directory == nil || provider == "" || (kind != "element" && kind != "voice") || page < 1 || page > 1000 || size < 1 || size > 100 {
		return core.ResourcePage{}, empty, ErrInvalid
	}
	scope, err := l.current(ctx, provider)
	if err != nil {
		return core.ResourcePage{}, empty, err
	}
	if scope.ID == "" || scope.Revision == "" || scope.OwnerID != identity.ProviderOwnerID(ctx) {
		return core.ResourcePage{}, empty, ErrNotFound
	}
	pinned := core.WithProviderConnectionScope(core.WithProviderRevision(ctx, scope.Revision), scope)
	p, err := l.directory.Provider(pinned, provider)
	if err != nil {
		return core.ResourcePage{}, empty, err
	}
	catalog, ok := p.(core.ResourceProvider)
	if !ok {
		return core.ResourcePage{}, empty, core.ErrProviderUnavailable
	}
	result, err := catalog.ListResources(pinned, kind, true, page, size)
	if err != nil {
		return core.ResourcePage{}, empty, err
	}
	current, err := l.current(ctx, provider)
	if err != nil {
		return core.ResourcePage{}, empty, err
	}
	if current != scope {
		return core.ResourcePage{}, empty, ErrNotFound
	}
	if result.Page != page || result.PageSize != size || len(result.Resources) > 100 {
		return core.ResourcePage{}, empty, ErrInvalid
	}
	for _, resource := range result.Resources {
		if !resource.IsPreset || resource.IsDeleted || resource.Status != core.ProviderSucceeded || resource.Kind != kind ||
			!ValidRemoteID(resource.RemoteID) || !presetLabel(resource.Name, 256) || resource.Session != nil ||
			(resource.Kind == "voice" && resource.Subtype != "preset") ||
			(resource.Kind == "element" && resource.Subtype != "multi_image" && resource.Subtype != "video_character") ||
			(resource.BoundVoice != nil && (!resource.BoundVoice.IsPreset || resource.BoundVoice.Kind != "voice")) {
			return core.ResourcePage{}, empty, ErrInvalid
		}
	}
	return result, scope, nil
}
