package api

import (
	"context"
	"crypto/hmac"
	"net/http"
	"net/url"
	"strings"

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
	if s.references == nil || s.mediaSigner == nil {
		return nil
	}
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
	for _, binding := range values {
		u, err := url.Parse(binding.raw)
		if err != nil {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		if len(parts) != 3 || parts[0] != "references" {
			continue
		}
		// Match the same signed, owner-scoped contract as the library resolver.
		if !hmac.Equal([]byte(u.Query().Get("signature")), []byte(s.referenceSignature(parts[1], parts[2]))) {
			continue
		}
		if parts[1] != identity.OwnerID(r.Context()) && parts[1] != templates.LibraryOwnerID {
			return storage.ErrObjectNotFound
		}
		file, err := s.references.Inspect(referenceReadContext(r.Context(), parts[1]), parts[2])
		if err != nil {
			return err
		}
		if file.Type != binding.kind {
			return referencevalidation.Invalid(file.Name, "Detected "+file.Type+" media cannot be used in a "+binding.kind+" input. Select the matching file type.")
		}
		files = append(files, file)
	}
	for _, raw := range input.Options.ReferenceVoices {
		u, e := url.Parse(raw)
		if e != nil {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		if len(parts) != 3 || parts[0] != "references" || !hmac.Equal([]byte(u.Query().Get("signature")), []byte(s.referenceSignature(parts[1], parts[2]))) {
			continue
		}
		if parts[1] != identity.OwnerID(r.Context()) && parts[1] != templates.LibraryOwnerID {
			return storage.ErrObjectNotFound
		}
		file, e := s.references.Inspect(referenceReadContext(r.Context(), parts[1]), parts[2])
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
