package api

import (
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/legal"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func (s *Server) legalFooterSettings(w http.ResponseWriter, r *http.Request) {
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin {
		systemError(w, system.ErrForbidden)
		return
	}
	store, ok := s.system.(legal.FooterStore)
	if !ok {
		writeError(w, 503, "unavailable", "Footer settings are unavailable on this server; ask an Administrator to check the server version and configuration")
		return
	}
	var footer legal.Footer
	if r.Method == http.MethodPut {
		var input struct {
			CopyrightHolder *string `json:"copyright_holder"`
			Version         int64   `json:"version"`
		}
		if !decodeSystemLimit(w, r, &input, legal.MaxFooterRequestBytes) {
			return
		}
		if input.CopyrightHolder == nil {
			invalidParameter(w, "copyright_holder", "copyright_holder is required; use an empty string for the placeholder")
			return
		}
		footer = legal.Footer{CopyrightHolder: *input.CopyrightHolder, Version: input.Version}
		if err = footer.Validate(); err != nil {
			field := "copyright_holder"
			if footer.Version < 1 {
				field = "version"
			}
			invalidParameter(w, field, err.Error())
			return
		}
		footer, err = store.SaveLegalFooter(r.Context(), identity.ActorID(r.Context()), footer)
	} else {
		footer, err = store.LegalFooter(r.Context())
	}
	if err != nil {
		systemError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, footer)
}
