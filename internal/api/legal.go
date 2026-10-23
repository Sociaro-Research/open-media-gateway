package api

import (
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/legal"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func (s *Server) legalDocument(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	kind := r.PathValue("kind")
	if !legal.ValidKind(kind) {
		http.NotFound(w, r)
		return
	}
	store, ok := s.system.(legal.Store)
	if !ok {
		writeError(w, 503, "unavailable", "legal documents are unavailable")
		return
	}
	d, err := store.LegalDocument(r.Context(), kind)
	if err != nil {
		systemError(w, err)
		return
	}
	// Public reads expose only display content, never settings or account data.
	writeJSON(w, 200, map[string]string{"text": d.Text})
}
func (s *Server) legalSettings(w http.ResponseWriter, r *http.Request) {
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin {
		systemError(w, system.ErrForbidden)
		return
	}
	kind := r.PathValue("kind")
	if !legal.ValidKind(kind) {
		http.NotFound(w, r)
		return
	}
	store, ok := s.system.(legal.Store)
	if !ok {
		writeError(w, 503, "unavailable", "legal documents are unavailable")
		return
	}
	var d legal.Document
	if r.Method == "PUT" {
		var input struct {
			Text    *string `json:"text"`
			Version int64   `json:"version"`
		}
		if !decodeSystemLimit(w, r, &input, legal.MaxRequestBytes) {
			return
		}
		if input.Text == nil {
			writeError(w, 400, "invalid_request", "text is required; use an empty string to restore the template")
			return
		}
		d = legal.Document{Text: *input.Text, Version: input.Version}
		if err = d.Validate(); err != nil {
			writeError(w, 400, "invalid_request", err.Error())
			return
		}
		d, err = store.SaveLegalDocument(r.Context(), identity.ActorID(r.Context()), kind, d)
	} else {
		d, err = store.LegalDocument(r.Context(), kind)
	}
	if err != nil {
		systemError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, d)
}
