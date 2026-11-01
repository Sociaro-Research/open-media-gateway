package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

// Resolve an editable snapshot without admitting work or copying media. Empty
// array entries retain unfilled slot positions until the user edits Playground.
func (s *Server) templateEditing(w http.ResponseWriter, r *http.Request) {
	if err := references.CheckRead(r.Context()); err != nil {
		s.referenceError(w, err)
		return
	}
	var in templateGenerationInput
	if !decodeSystemLimit(w, r, &in, 16384) {
		return
	}
	var snapshot map[string]any
	err := s.templates.WithPublished(r.Context(), r.PathValue("template"), in.Revision, func(item templates.Record) error {
		d := *item.Published
		model, ok := templateModel(s.visibleCatalog(r), in.Provider, in.Model)
		if !ok {
			return fmt.Errorf("%w: selected model is unavailable", templates.ErrInvalid)
		}
		if err := d.Compatible(model); err != nil {
			return err
		}
		if err := s.checkTemplateAssets(r.Context(), item.ID, d); err != nil {
			return err
		}
		options, err := d.OptionsFor(model)
		if err != nil {
			return err
		}
		remaining := map[string]string{}
		for k, v := range in.Inputs {
			remaining[k] = v
		}
		options.ImageURLs = nil
		options.VideoURLs = nil
		options.AudioURLs = nil
		for _, slot := range d.Slots {
			id, supplied := remaining[slot.ID]
			delete(remaining, slot.ID)
			if slot.AssetID != "" {
				if supplied {
					return fmt.Errorf("%w: fixed references cannot be replaced", templates.ErrInvalid)
				}
				id = slot.AssetID
			}
			value := ""
			if id != "" {
				// Shared assets have the same stable library identity. Authorization falls
				// back only to that namespace, never to another user's private library.
				var ref references.Image
				ctx := r.Context()
				var e error
				if slot.AssetID != "" {
					if s.templateReferences == nil {
						return templates.ErrNotFound
					}
					ctx = templateLibraryContext(ctx)
					ref, e = s.templateReferences.Get(ctx, id)
				} else {
					ref, ctx, e = s.generationReference(ctx, id)
				}
				if e != nil {
					return e
				}
				if !strings.HasPrefix(ref.ContentType, slot.Type+"/") {
					return fmt.Errorf("%w: wrong media type for %s", templates.ErrInvalid, slot.Label)
				}
				value = s.referenceAbsoluteURL(r.WithContext(ctx), ref)
			}
			switch slot.Type {
			case "image":
				options.ImageURLs = append(options.ImageURLs, value)
			case "video":
				options.VideoURLs = append(options.VideoURLs, value)
			case "audio":
				options.AudioURLs = append(options.AudioURLs, value)
			}
		}
		if len(remaining) > 0 {
			return fmt.Errorf("%w: undeclared reference inputs", templates.ErrInvalid)
		}
		snapshot = map[string]any{"prompt": d.Prompt, "provider": in.Provider, "model": in.Model, "media_type": d.MediaType, "options": options}
		return nil
	})
	if err != nil {
		templateError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, snapshot)
}
