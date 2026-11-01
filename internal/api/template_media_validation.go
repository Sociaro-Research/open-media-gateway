package api

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"net/http"
	"strings"
)

// Check partial consumer bindings without altering the published recipe or
// accepting any replacement for a fixed slot. Never estimates or admits work.
func (s *Server) templateValidateInputs(w http.ResponseWriter, r *http.Request) {
	r, cancelInspection := referenceInspectionRequest(w, r)
	defer cancelInspection()
	if err := references.CheckRead(r.Context()); err != nil {
		s.referenceError(w, err)
		return
	}
	var in templateGenerationInput
	if !decodeSystemLimit(w, r, &in, 16384) {
		return
	}
	item, err := s.templates.Get(r.Context(), r.PathValue("template"))
	if err != nil || item.Published == nil {
		templateError(w, templates.ErrNotFound)
		return
	}
	if item.Revision != in.Revision {
		templateError(w, templates.ErrConflict)
		return
	}
	model, ok := templateModel(s.visibleCatalog(r), in.Provider, in.Model)
	if !ok {
		templateError(w, referencevalidation.Invalid("References", "Selected model is unavailable."))
		return
	}
	d := item.Draft
	if err = d.Compatible(model); err != nil {
		templateError(w, err)
		return
	}
	files, _, err := s.fixedTemplateMedia(r.Context(), item.ID, d)
	if err != nil {
		templateError(w, err)
		return
	}
	remaining := map[string]string{}
	for k, v := range in.Inputs {
		remaining[k] = v
	}
	for _, slot := range d.Slots {
		id, exists := remaining[slot.ID]
		if !exists {
			continue
		}
		delete(remaining, slot.ID)
		if slot.AssetID != "" {
			templateError(w, referencevalidation.Invalid(slot.Label, "Fixed references cannot be replaced."))
			return
		}
		if s.references == nil {
			templateError(w, templates.ErrNotFound)
			return
		}
		_, readContext, e := s.generationReference(r.Context(), id)
		if e != nil {
			s.referenceError(w, e)
			return
		}
		file, e := s.references.Inspect(readContext, id)
		if e != nil {
			s.referenceError(w, e)
			return
		}
		if !strings.EqualFold(file.Type, slot.Type) {
			templateError(w, referencevalidation.Invalid(slot.Label, "Select a "+slot.Type+" file for this input."))
			return
		}
		files = append(files, file)
	}
	if len(remaining) > 0 {
		templateError(w, referencevalidation.Invalid("References", "Undeclared reference inputs are not accepted."))
		return
	}
	options, err := d.OptionsFor(model)
	if err != nil {
		templateError(w, err)
		return
	}
	if err = s.referenceValidator(r)(r.Context(), &referencevalidation.Target{Provider: in.Provider, Model: in.Model, Options: options}, files, false); err != nil {
		templateError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"valid": true})
}

func (s *Server) validateDraftMedia(r *http.Request, id string, d templates.Definition) error {
	if len(d.Slots) > 64 {
		return referencevalidation.Invalid("References", "At most 64 slots can be checked together.")
	}
	files, _, err := s.fixedTemplateMedia(r.Context(), id, d)
	if err != nil {
		return err
	}
	options := d.Options
	if model, ok := templateModel(s.visibleCatalog(r), d.Provider, d.Model); ok {
		options = d.GenerationOptionsFor(model)
	}
	return s.referenceValidator(r)(r.Context(), &referencevalidation.Target{Provider: d.Provider, Model: d.Model, Options: options}, files, false)
}
func (s *Server) templateValidateMedia(w http.ResponseWriter, r *http.Request) {
	r, cancelInspection := referenceInspectionRequest(w, r)
	defer cancelInspection()
	if !s.templatePublisher(w, r) {
		return
	}
	id := r.PathValue("template")
	if _, err := s.templates.Get(r.Context(), id); err != nil {
		templateError(w, err)
		return
	}
	var in struct {
		Draft templates.Definition `json:"draft"`
	}
	if !decodeSystemLimit(w, r, &in, 65536) {
		return
	}
	if err := s.validateDraftMedia(r, id, in.Draft); err != nil {
		templateError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"valid": true})
}
