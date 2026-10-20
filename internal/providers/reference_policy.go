package providers

import (
	"context"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"net/url"
)

// SetSystemSettings configures live reference transfer policy before the manager is used.
func (m *Manager) SetSystemSettings(settings system.SettingsReader) {
	m.systemSettings = settings
}

// referencePolicy is checked both at admission and at dispatch, including queued tasks.
func (m *Manager) referencePolicy(ctx context.Context, options core.MediaOptions) (bool, error) {
	allowed := true
	if m.systemSettings != nil {
		settings, err := m.systemSettings.Settings(ctx)
		if err != nil {
			return false, fmt.Errorf("read reference transfer policy: %w", err)
		}
		allowed = settings.AllowInlineReferences
	}
	if !allowed {
		refs := append([]string{}, options.ImageURLs...)
		refs = append(refs, options.VideoURLs...)
		refs = append(refs, options.AudioURLs...)
		if options.VideoURL != "" {
			refs = append(refs, options.VideoURL)
		}
		if options.AudioURL != "" {
			refs = append(refs, options.AudioURL)
		}
		for visual, voice := range options.ReferenceVoices {
			refs = append(refs, visual, voice)
		}
		for _, ref := range refs {
			if core.IsAssetReference(ref) {
				continue
			}
			u, err := url.Parse(ref)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
				return false, fmt.Errorf("%w: inline references are disabled; use HTTP(S) URLs or supported registered asset references", core.ErrOptionsInvalid)
			}
		}
	}
	return allowed, nil
}
