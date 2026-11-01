package api

import (
	"context"
	"maps"
	"net/http"
	"slices"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

func (s *Server) referenceValidator(r *http.Request) referencevalidation.Validator {
	catalog := s.visibleCatalog(r)
	return func(ctx context.Context, target *referencevalidation.Target, files []referencevalidation.File, complete bool) error {
		if target == nil {
			return nil
		}
		if len(target.ReferenceIDs)+len(files) > 64 {
			return referencevalidation.Invalid("References", "At most 64 reference files can be checked together.")
		}
		originalFiles := files
		baseFiles := []referencevalidation.File{}
		for _, id := range target.ReferenceIDs {
			if s.references == nil {
				return storage.ErrObjectNotFound
			}
			_, readContext, err := s.generationReference(ctx, id)
			if err != nil {
				return err
			}
			file, err := s.references.Inspect(readContext, id)
			if err != nil {
				return err
			}
			baseFiles = append(baseFiles, file)
		}
		if target.Role == "reference_voice" {
			files = baseFiles
		} else if target.Role != "" {
			return referencevalidation.Invalid("References", "Unknown reference role.")
		} else {
			files = append(files, baseFiles...)
		}
		model, ok := templateModel(catalog, target.Provider, target.Model)
		if !ok {
			return referencevalidation.Invalid("References", "The selected model is unavailable to this account or key. Choose an available model.")
		}
		options := target.Options
		// Client-supplied reference URLs and voices are never consulted or fetched.
		options.ImageURLs = nil
		options.VideoURLs = nil
		options.VideoURL = ""
		options.AudioURLs = nil
		options.AudioURL = ""
		options.ReferenceVoices = nil
		if err := referencevalidation.ValidateMode(model, options, files, complete); err != nil {
			return err
		}
		for _, file := range files {
			switch file.Type {
			case "image":
				options.ImageURLs = append(options.ImageURLs, "https://reference.invalid/input")
			case "video":
				options.VideoURLs = append(options.VideoURLs, "https://reference.invalid/input")
			case "audio":
				options.AudioURLs = append(options.AudioURLs, "https://reference.invalid/input")
			}
		}
		normalized, err := core.NormalizeReferenceOptions(model, options)
		if err != nil {
			return referencevalidation.Invalid("References", "Selected generation settings are invalid for this model. Review its duration, size and workflow.")
		}
		if err = referencevalidation.ValidateMode(model, normalized, files, complete); err != nil {
			return err
		}
		if target.Role == "reference_voice" {
			return referencevalidation.ValidateVoiceFiles(model, normalized, originalFiles)
		}
		return referencevalidation.ValidateRequestFiles(model, normalized, files, complete)
	}
}

func (s *Server) validateReferences(w http.ResponseWriter, r *http.Request) {
	r, cancelInspection := referenceInspectionRequest(w, r)
	defer cancelInspection()
	if err := references.CheckRead(r.Context()); err != nil {
		s.referenceError(w, err)
		return
	}
	var in struct {
		Validation   *referencevalidation.Target `json:"validation"`
		ReferenceIDs []string                    `json:"reference_ids"`
	}
	if !decodeSystemLimit(w, r, &in, 65536) {
		return
	}
	if len(in.ReferenceIDs) > 64 {
		s.referenceError(w, referencevalidation.Invalid("References", "At most 64 files can be checked together."))
		return
	}
	files := make([]referencevalidation.File, 0, len(in.ReferenceIDs))
	for _, id := range in.ReferenceIDs {
		_, readContext, err := s.generationReference(r.Context(), id)
		if err != nil {
			s.referenceError(w, err)
			return
		}
		file, err := s.references.Inspect(readContext, id)
		if err != nil {
			s.referenceError(w, err)
			return
		}
		files = append(files, file)
	}
	if err := s.referenceValidator(r)(r.Context(), in.Validation, files, false); err != nil {
		s.referenceError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"valid": true})
}

// Recheck owned stored inputs at estimate/admission too. External URLs and native
// provider asset IDs retain their existing validation; they are never fetched here.
func (s *Server) validateGenerationReferences(r *http.Request, input core.CreateMediaInput) error {
	return s.prepareGenerationReferences(r, &input)
}

func (s *Server) prepareGenerationReferences(r *http.Request, input *core.CreateMediaInput) error {
	// ReferenceEvidence is server-only (json:"-") and may contain inspected
	// fixed template assets. Preserve a copy, but always refresh local capability
	// evidence below. Metadata in public options never becomes trusted evidence.
	input.ReferenceEvidence = maps.Clone(input.ReferenceEvidence)
	input.Options.ReferenceMediaEvidence = nil
	if s.references == nil || s.mediaSigner == nil {
		return nil
	}
	// Copy URL containers before normalization so quotes and accepted tasks cannot
	// be changed through the caller's slices or reference-voice map.
	canonicalize := func(raw string) (string, error) {
		owner, id, ok := references.ParseSignedReference(raw, s.mediaSigner.secret)
		if !ok {
			return raw, nil
		}
		if owner != identity.OwnerID(r.Context()) && owner != templates.LibraryOwnerID {
			return "", storage.ErrObjectNotFound
		}
		target, err := references.CanonicalReferenceURL(s.assetPublicURL, s.mediaSigner.secret, owner, id)
		if err != nil {
			return "", referencevalidation.Invalid("References", "The public reference origin is unavailable. Contact the administrator.")
		}
		return target, nil
	}
	normalize := func(values []string) ([]string, error) {
		if values == nil {
			return nil, nil
		}
		out := make([]string, len(values))
		for i, raw := range values {
			target, err := canonicalize(raw)
			if err != nil {
				return nil, err
			}
			out[i] = target
		}
		return out, nil
	}
	options := input.Options
	var err error
	if options.ImageURLs, err = normalize(options.ImageURLs); err != nil {
		return err
	}
	if options.VideoURLs, err = normalize(options.VideoURLs); err != nil {
		return err
	}
	if options.AudioURLs, err = normalize(options.AudioURLs); err != nil {
		return err
	}
	if options.VideoURL, err = canonicalize(options.VideoURL); err != nil {
		return err
	}
	if options.AudioURL, err = canonicalize(options.AudioURL); err != nil {
		return err
	}
	if options.ReferenceVoices != nil {
		options.ReferenceVoices = make(map[string]string, len(input.Options.ReferenceVoices))
		for _, visual := range slices.Sorted(maps.Keys(input.Options.ReferenceVoices)) {
			voice := input.Options.ReferenceVoices[visual]
			visual, err = canonicalize(visual)
			if err != nil {
				return err
			}
			voice, err = canonicalize(voice)
			if err != nil {
				return err
			}
			if previous, exists := options.ReferenceVoices[visual]; exists && previous != voice {
				return referencevalidation.Invalid("References", "Conflicting voices target the same reference. Select one voice per visual.")
			}
			options.ReferenceVoices[visual] = voice
		}
	}
	input.Options = options
	type binding struct{ raw, kind string }
	values := []binding{}
	for _, group := range []struct {
		kind string
		urls []string
	}{{"image", input.Options.ImageURLs}, {"video", input.Options.Videos()}, {"audio", input.Options.Audios()}} {
		for _, raw := range group.urls {
			values = append(values, binding{raw, group.kind})
		}
	}
	files := []referencevalidation.File{}
	if input.ReferenceEvidence == nil {
		input.ReferenceEvidence = map[string]core.InputMediaEvidence{}
	}
	for raw := range input.ReferenceEvidence {
		if _, _, local := references.ParseSignedReference(raw, s.mediaSigner.secret); local {
			delete(input.ReferenceEvidence, raw)
		}
	}
	for _, binding := range values {
		owner, id, ok := references.ParseSignedReference(binding.raw, s.mediaSigner.secret)
		if !ok {
			continue
		}
		file, err := s.references.Inspect(referenceReadContext(r.Context(), owner), id)
		if err != nil {
			return err
		}
		if file.Type != binding.kind {
			return referencevalidation.Invalid(file.Name, "Detected "+file.Type+" media cannot be used in a "+binding.kind+" input. Select the matching file type.")
		}
		files = append(files, file)
		input.ReferenceEvidence[binding.raw] = core.InputMediaEvidence{Type: file.Type, Duration: file.Info.Duration}
	}
	for _, raw := range input.Options.ReferenceVoices {
		owner, id, ok := references.ParseSignedReference(raw, s.mediaSigner.secret)
		if !ok {
			continue
		}
		file, e := s.references.Inspect(referenceReadContext(r.Context(), owner), id)
		if e != nil {
			return e
		}
		if e = s.referenceValidator(r)(r.Context(), &referencevalidation.Target{Provider: input.Provider, Model: input.Model, Options: input.Options, Role: "reference_voice"}, []referencevalidation.File{file}, false); e != nil {
			return e
		}
	}
	if len(files) == 0 {
		return nil
	}
	return s.referenceValidator(r)(r.Context(), &referencevalidation.Target{Provider: input.Provider, Model: input.Model, Options: input.Options}, files, false)
}

func referenceReadContext(ctx context.Context, owner string) context.Context {
	if owner == templates.LibraryOwnerID {
		return templateLibraryContext(ctx)
	}
	return ctx
}
