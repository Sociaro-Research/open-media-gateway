package api

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

type providerLogos interface {
	Logo(context.Context, string) ([]byte, error)
	SaveLogo(context.Context, string, []byte) (string, error)
}

func (s *Server) removeProviderLogo(w http.ResponseWriter, r *http.Request) {
	admin, err := s.systemAdmin(r.Context())
	if err != nil || !admin {
		writeError(w, 403, "forbidden", "administrator access is required to remove provider logos")
		return
	}
	store, ok := s.providerAdmin.(interface {
		RemoveLogo(context.Context, string) error
	})
	if !ok {
		writeError(w, 503, "unavailable", "provider logo storage is unavailable")
		return
	}
	if writeProviderError(w, store.RemoveLogo(r.Context(), r.PathValue("id"))) {
		return
	}
	s.writeViewerJSON(w, r, 200, map[string]string{"icon_url": ""})
}

func providerLogoForViewer(icon string, r *http.Request) string {
	if icon == "" || r.PathValue("org") == "" {
		return icon
	}
	return icon + "&organization=" + url.QueryEscape(r.PathValue("org"))
}

func (s *Server) uploadProviderLogo(w http.ResponseWriter, r *http.Request) {
	admin, err := s.systemAdmin(r.Context())
	if err != nil || !admin {
		writeError(w, 403, "forbidden", "administrator access is required to replace provider logos")
		return
	}
	store, ok := s.providerAdmin.(providerLogos)
	if !ok {
		writeError(w, 503, "unavailable", "provider logo storage is unavailable")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "image/png" {
		writeError(w, 400, "unsupported_logo_format", "send a PNG image with Content-Type image/png")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, providerconfig.MaxLogoBytes)
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "invalid_logo", "upload did not complete or exceeded 1 MiB; choose a PNG and retry")
		return
	}
	if err := providerconfig.ValidateLogo(data); err != nil {
		writeError(w, 400, "invalid_logo", err.Error())
		return
	}
	iconURL, err := store.SaveLogo(r.Context(), r.PathValue("id"), data)
	if writeProviderError(w, err) {
		return
	}
	s.writeViewerJSON(w, r, 200, map[string]string{"icon_url": iconURL})
}

func (s *Server) providerLogo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	owner := r.PathValue("owner")
	admin, adminErr := s.systemAdmin(ctx)
	allowed := adminErr == nil && admin && (owner == identity.ActorID(ctx) || owner == identity.SharedOwnerID)
	if !allowed && owner == identity.SharedOwnerID && identity.APIKeyID(ctx) == nil {
		org := r.URL.Query().Get("organization")
		if _, err := uuid.Parse(org); err == nil {
			if repo, ok := s.system.(system.SalesPermissionsRepository); ok {
				allowed = repo.CommercialPermission(ctx, identity.ActorID(ctx), org, "models") == nil || repo.CommercialPermission(ctx, identity.ActorID(ctx), org, "pricing") == nil
			}
		}
	}
	if !allowed && s.system != nil {
		settings, err := s.system.Settings(ctx)
		if err == nil {
			scopes, _ := ctx.Value(apiKeyScopesContextKey{}).([]string)
			key := identity.APIKeyID(ctx) != nil
			// Configuration readers see their own logos under the same personal
			// provider policy. Generation keys remain bound to their resource mode.
			if owner == identity.ActorID(ctx) && settings.PersonalProviders && (!key || slices.Contains(scopes, apikey.ProvidersManage)) {
				allowed = true
			} else if (!key || owner == identity.ProviderOwnerID(ctx)) && ((owner == identity.SharedOwnerID && settings.SharedResources) || (owner == identity.ActorID(ctx) && settings.PersonalProviders)) {
				catalogCtx := identity.WithResourceOwners(ctx, owner, identity.StorageOwnerID(ctx))
				if s.service != nil {
					catalog, catalogErr := s.applyModelAccess(catalogCtx, s.service.ListProviders(catalogCtx), false)
					if catalogErr != nil {
						http.NotFound(w, r)
						return
					}
					for _, provider := range catalog {
						if provider.ID == r.PathValue("id") {
							allowed = true
							break
						}
					}
				}
			}
		}
	}
	store, ok := s.providerAdmin.(providerLogos)
	if !allowed || !ok {
		http.NotFound(w, r)
		return
	}
	ctx = identity.WithResourceOwners(ctx, owner, identity.StorageOwnerID(ctx))
	data, err := store.Logo(ctx, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, _ = w.Write(data)
}
