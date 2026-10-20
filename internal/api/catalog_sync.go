package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func (s *Server) catalogSyncAdmin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin {
		writeError(w, 403, "forbidden", "Administrator access is required")
		return
	}
	if s.catalogSync == nil {
		writeError(w, 503, "catalog_unavailable", "Catalogue synchronization is not configured")
		return
	}
	ctx := r.Context()
	actor := identity.ActorID(ctx)
	path := r.URL.Path
	var result any
	switch {
	case strings.HasPrefix(path, "/v1/system/catalog/documents/"):
		var text string
		text, err = s.catalogSync.Document(ctx, r.PathValue("hash"))
		result = map[string]string{"hash": r.PathValue("hash"), "text": text}
	case path == "/v1/system/catalog/settings":
		if r.Method == "GET" {
			result, err = s.catalogSync.Settings(ctx)
		} else {
			var in catalogsync.Settings
			if !decodeSystem(w, r, &in) {
				return
			}
			result, err = s.catalogSync.SaveSettings(ctx, actor, in)
		}
	case path == "/v1/system/catalog/schedules":
		var data []catalogsync.Schedule
		data, err = s.catalogSync.Schedules(ctx)
		result = map[string]any{"data": data}
	case strings.HasSuffix(path, "/check"):
		err = s.catalogSync.Request(ctx, actor, r.PathValue("id"))
		result = map[string]bool{"queued": err == nil}
	case strings.HasPrefix(path, "/v1/system/catalog/schedules/"):
		var in catalogsync.Schedule
		if !decodeSystem(w, r, &in) {
			return
		}
		in.Provider = r.PathValue("id")
		result, err = s.catalogSync.SaveSchedule(ctx, actor, in)
	case path == "/v1/system/catalog/runs":
		offset, ok := systemPage(w, r)
		if !ok {
			return
		}
		var data []catalogsync.Run
		data, err = s.catalogSync.Runs(ctx, r.URL.Query().Get("provider"), 51, offset)
		more := len(data) > 50
		if more {
			data = data[:50]
		}
		result = map[string]any{"data": data, "has_more": more}
	case strings.HasSuffix(path, "/versions"):
		offset, ok := systemPage(w, r)
		if !ok {
			return
		}
		var data []catalogsync.State
		data, err = s.catalogSync.Versions(ctx, r.PathValue("id"), 51, offset)
		more := len(data) > 50
		if more {
			data = data[:50]
		}
		result = map[string]any{"data": data, "has_more": more}
	default:
		result, err = s.catalogSync.State(ctx, r.PathValue("id"))
	}
	if errors.Is(err, catalogsync.ErrSettings) {
		writeError(w, 400, "invalid_request", "Enter a valid timezone, email, daily time and supported HTTPS sources")
		return
	}
	if err != nil {
		systemError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, result)
}
