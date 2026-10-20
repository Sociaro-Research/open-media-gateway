package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/Sociaro-Research/media-gateway-dev/internal/branding"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
)

func (s *Server) brandingReader() branding.Reader {
	reader, _ := s.system.(branding.Reader)
	return reader
}
func readBranding(ctx context.Context, reader branding.Reader) branding.Settings {
	if reader != nil {
		if settings, err := reader.Branding(ctx); err == nil {
			return settings.Normalize()
		}
	}
	return branding.Defaults().Normalize()
}
func (s *Server) brandingSettings(w http.ResponseWriter, r *http.Request) {
	store, ok := s.system.(branding.Store)
	if !ok {
		writeError(w, 503, "unavailable", "appearance settings are unavailable")
		return
	}
	if r.Method == "GET" {
		settings, err := store.Branding(r.Context())
		if err != nil {
			systemError(w, err)
			return
		}
		s.writeViewerJSON(w, r, 200, map[string]any{"settings": settings.Normalize(), "defaults": branding.Defaults().Normalize(), "colors": branding.Colors})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, branding.MaxRequestBytes)
	defer r.Body.Close()
	next := branding.Defaults()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&next); err != nil || d.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid_request", "send one appearance object up to 6 MiB")
		return
	}
	if err := next.Validate(); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	saved, err := store.SaveBranding(r.Context(), identity.ActorID(r.Context()), next)
	if err != nil {
		systemError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, saved.Normalize())
}
func brandingAsset(w http.ResponseWriter, r *http.Request, reader branding.Reader) {
	settings := readBranding(r.Context(), reader)
	a, ok := settings.Assets[r.PathValue("asset")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", a.MIME)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, _ = w.Write(a.Data)
}
