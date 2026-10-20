package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
)

func (s *Server) registerProviderAssetRoutes(mux *http.ServeMux) {
	mux.Handle("GET /v1/references/providers/{provider}/groups", s.providerAssetRoute(s.assetGroups))
	mux.Handle("POST /v1/references/providers/{provider}/groups", s.providerAssetRoute(s.createAssetGroup))
	mux.Handle("GET /v1/references/providers/{provider}/inventory", s.providerAssetRoute(s.assetInventory))
	mux.Handle("POST /v1/references/providers/{provider}/import", s.providerAssetRoute(s.importAsset))
	mux.Handle("GET /v1/references/providers/{provider}/assets", s.providerAssetRoute(s.listProviderAssets))
	mux.Handle("POST /v1/references/providers/{provider}/assets", s.providerAssetRoute(s.registerProviderAsset))
	mux.Handle("GET /v1/references/providers/{provider}/assets/{id}", s.providerAssetRoute(s.getProviderAsset))
	mux.Handle("DELETE /v1/references/providers/{provider}/assets/{id}", s.providerAssetRoute(s.deleteProviderAsset))
}
func (s *Server) providerAssetRoute(handler http.HandlerFunc) http.Handler {
	return s.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(45 * time.Second))
		role := identity.OrganizationFromContext(r.Context()).Role
		if role != "" && (!org.OwnContent(role) || role == org.ContentOfficer && r.Method != "GET") {
			orgError(w, org.ErrForbidden)
			return
		}
		if s.providerAssets == nil {
			s.assetError(w, providerassets.ErrUnavailable)
			return
		}
		handler(w, r)
	}))
}
func (s *Server) assetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, providerassets.ErrUnavailable):
		writeError(w, 409, "asset_library_unavailable", err.Error())
	case errors.Is(err, providerassets.ErrNotFound):
		writeError(w, 404, "not_found", "provider asset not found")
	case errors.Is(err, providerassets.ErrConflict):
		writeError(w, 409, "asset_in_use", err.Error())
	case errors.Is(err, providerassets.ErrRejected):
		writeError(w, 400, "provider_asset_rejected", err.Error())
	case errors.Is(err, providerassets.ErrInvalid):
		writeError(w, 400, "invalid_request", err.Error())
	default:
		writeError(w, 502, "provider_asset_failed", "BytePlus asset operation failed. Check the asset AccessKey permissions, project and account rights; do not repeat an uncertain registration.")
	}
}

// A shared provider's external inventory is visible only to a matching signed-in
// administrator (or a system-authorized API key). Generation-only keys do not
// acquire inventory privileges merely because their recipient is an admin.
func (s *Server) assetInventoryAllowed(r *http.Request) bool {
	ctx := r.Context()
	if identity.ProviderOwnerID(ctx) == identity.ActorID(ctx) {
		return true
	}
	if identity.ActorID(ctx) == identity.SystemOwnerID && s.apiKey != "" {
		return true
	}
	if admin, err := s.systemAdmin(ctx); err == nil && admin {
		return true
	}
	if s.auth != nil && s.system != nil {
		u, err := s.auth.Authenticate(r)
		if err == nil && u.ID == identity.ActorID(ctx) {
			admin, err := s.system.IsAdmin(ctx, u.ID)
			return err == nil && admin
		}
	}
	return false
}
func (s *Server) assetGroups(w http.ResponseWriter, r *http.Request) {
	var data []providerassets.Group
	var err error
	if r.URL.Query().Get("inventory") == "1" {
		if !s.assetInventoryAllowed(r) {
			orgError(w, org.ErrForbidden)
			return
		}
		data, err = s.providerAssets.InventoryGroups(r.Context(), r.PathValue("provider"))
	} else {
		data, err = s.providerAssets.Groups(r.Context(), r.PathValue("provider"))
	}
	if err != nil {
		s.assetError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"data": data, "can_import": s.assetInventoryAllowed(r)})
}
func (s *Server) createAssetGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decodeSystem(w, r, &in) {
		return
	}
	g, err := s.providerAssets.CreateGroup(r.Context(), r.PathValue("provider"), in.Name)
	if err != nil {
		s.assetError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 201, g)
}
func (s *Server) assetInventory(w http.ResponseWriter, r *http.Request) {
	if !s.assetInventoryAllowed(r) {
		orgError(w, org.ErrForbidden)
		return
	}
	page, err := s.providerAssets.Inventory(r.Context(), r.PathValue("provider"), r.URL.Query().Get("group"), r.URL.Query().Get("next"))
	if err != nil {
		s.assetError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, page)
}
func (s *Server) importAsset(w http.ResponseWriter, r *http.Request) {
	if !s.assetInventoryAllowed(r) {
		orgError(w, org.ErrForbidden)
		return
	}
	var in struct {
		ID string `json:"asset_id"`
	}
	if !decodeSystem(w, r, &in) {
		return
	}
	a, err := s.providerAssets.Import(r.Context(), r.PathValue("provider"), in.ID)
	if err != nil {
		s.assetError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 201, a)
}
func (s *Server) listProviderAssets(w http.ResponseWriter, r *http.Request) {
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if r.URL.Query().Get("offset") == "" {
		offset = 0
		err = nil
	}
	if err != nil || offset < 0 || offset > 1000000 {
		writeError(w, 400, "invalid_request", "invalid asset offset")
		return
	}
	items, more, err := s.providerAssets.List(r.Context(), r.PathValue("provider"), offset)
	if err != nil {
		s.assetError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"data": items, "has_more": more, "next_offset": offset + len(items)})
}
func (s *Server) registerProviderAsset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source string `json:"reference_id"`
		Group  string `json:"group_id"`
	}
	if !decodeSystem(w, r, &in) {
		return
	}
	item, err := s.references.Get(r.Context(), in.Source)
	if err != nil {
		s.referenceError(w, err)
		return
	}
	source, err := s.references.DirectURL(r.Context(), item.ID, "GET", 24*time.Hour)
	if err != nil {
		s.referenceError(w, err)
		return
	}
	if source == "" {
		if s.assetPublicURL == "" {
			writeError(w, 409, "public_url_required", "Configure AUTH_PUBLIC_URL before registering local files with BytePlus.")
			return
		}
		source = s.assetPublicURL + s.referenceView(r, item).URL
	}
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		writeError(w, 400, "invalid_source", "BytePlus needs a publicly reachable HTTPS source file")
		return
	}
	kind := providerassets.Kind(item.ContentType)
	if kind == "" {
		writeError(w, 400, "invalid_source", "unsupported asset type")
		return
	}
	// Remote libraries have model-specific limits; retain the exact source bytes.
	if item.SizeBytes >= 30<<20 && kind == "Image" {
		writeError(w, 400, "invalid_source", "BytePlus image assets must be smaller than 30 MB")
		return
	}
	if kind == "Video" && item.ContentType != "video/mp4" && item.ContentType != "video/quicktime" || kind == "Audio" && (item.SizeBytes > 15<<20 || !strings.Contains(" audio/mpeg audio/wav audio/wave audio/x-wav ", " "+item.ContentType+" ")) {
		writeError(w, 400, "invalid_source", "BytePlus assets accept MP4/MOV videos and MP3/WAV audio up to 15 MB")
		return
	}

	record, err := s.providerAssets.Register(r.Context(), r.PathValue("provider"), item.ID, source, item.Name, kind, in.Group)
	if err != nil {
		s.assetError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 202, record)
}
func (s *Server) getProviderAsset(w http.ResponseWriter, r *http.Request) {
	a, err := s.providerAssets.Get(r.Context(), r.PathValue("provider"), r.PathValue("id"))
	if err != nil {
		s.assetError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, a)
}
func (s *Server) deleteProviderAsset(w http.ResponseWriter, r *http.Request) {
	var err error
	if r.URL.Query().Get("detach") == "1" {
		err = s.providerAssets.Detach(r.Context(), r.PathValue("provider"), r.PathValue("id"))
	} else {
		err = s.providerAssets.Delete(r.Context(), r.PathValue("provider"), r.PathValue("id"))
	}
	if err != nil {
		s.assetError(w, err)
		return
	}
	w.WriteHeader(204)
}
