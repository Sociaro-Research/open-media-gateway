package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func (s *Server) registerTemplateRoutes(mux *http.ServeMux) {
	if s.templates == nil {
		return
	}
	if s.references != nil && s.storage != nil {
		s.templateReferences = s.references.WithStore(templateReferenceStore{s.storage})
	}
	mux.Handle("GET /v1/system/template-references", s.authenticate(http.HandlerFunc(s.sharedTemplateReferences)))
	mux.Handle("POST /v1/system/template-references", s.authenticate(http.HandlerFunc(s.sharedTemplateReferences)))
	mux.Handle("PATCH /v1/system/template-references/{id}", s.authenticate(http.HandlerFunc(s.sharedTemplateReferences)))
	mux.Handle("DELETE /v1/system/template-references/{id}", s.authenticate(http.HandlerFunc(s.sharedTemplateReferences)))
	mux.Handle("GET /v1/system/template-references/{id}/content", s.authenticate(http.HandlerFunc(s.sharedTemplateReferences)))
	mux.Handle("GET /v1/templates", s.authenticate(http.HandlerFunc(s.templateGallery)))
	mux.Handle("GET /v1/templates/{template}", s.authenticate(http.HandlerFunc(s.templateUse)))
	mux.Handle("GET /v1/templates/{template}/metadata", s.authenticate(http.HandlerFunc(s.templateMetadata)))
	mux.Handle("POST /v1/templates/{template}/editing", s.authenticate(http.HandlerFunc(s.templateEditing)))
	mux.Handle("POST /v1/templates/{template}/estimate", s.authenticate(http.HandlerFunc(s.templateGenerate)))
	mux.Handle("POST /v1/templates/{template}/validate-media", s.authenticate(http.HandlerFunc(s.templateValidateInputs)))
	mux.Handle("POST /v1/templates/{template}/generations", s.authenticate(http.HandlerFunc(s.templateGenerate)))
	mux.Handle("GET /v1/system/templates/library", s.authenticate(http.HandlerFunc(s.templateLibrary)))
	mux.Handle("GET /v1/system/templates", s.authenticate(http.HandlerFunc(s.templateAdmin)))
	mux.Handle("POST /v1/system/templates", s.authenticate(http.HandlerFunc(s.templateAdmin)))
	mux.Handle("GET /v1/system/templates/{template}", s.authenticate(http.HandlerFunc(s.templateAdmin)))
	mux.Handle("PUT /v1/system/templates/{template}", s.authenticate(http.HandlerFunc(s.templateAdmin)))
	mux.Handle("DELETE /v1/system/templates/{template}", s.authenticate(http.HandlerFunc(s.templateAdmin)))
	mux.Handle("POST /v1/system/templates/{template}/infer", s.authenticate(http.HandlerFunc(s.templateInfer)))
	mux.Handle("POST /v1/system/templates/{template}/assets", s.authenticate(http.HandlerFunc(s.templateUpload)))
	mux.Handle("GET /v1/system/templates/{template}/assets/{asset}/metadata", s.authenticate(http.HandlerFunc(s.templateAssetMetadata)))
	mux.Handle("POST /v1/system/templates/{template}/validate-media", s.authenticate(http.HandlerFunc(s.templateValidateMedia)))
	mux.Handle("GET /v1/system/templates/{template}/assets/{asset}", s.authenticate(http.HandlerFunc(s.templateAdminMedia)))
	mux.HandleFunc("GET /template-media/{asset}", s.templateMedia)
}
func templateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, templates.ErrNotFound), errors.Is(err, storage.ErrObjectNotFound):
		writeError(w, 404, "template_unavailable", "Template or fixed media is unavailable. Reload the gallery.")
	case errors.Is(err, templates.ErrConflict):
		writeError(w, 409, "template_conflict", err.Error())
	case errors.Is(err, templates.ErrInvalid), errors.Is(err, core.ErrOptionsInvalid), errors.Is(err, references.ErrInvalidImage), errors.Is(err, mediafile.ErrInvalid), errors.Is(err, referencevalidation.ErrInvalid):
		writeError(w, 400, "invalid_template", err.Error())
	default:
		writeError(w, 503, "template_unavailable", "Template operation failed. Retry without discarding your draft.")
	}
}
func (s *Server) templatePublisher(w http.ResponseWriter, r *http.Request) bool {
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return false
	}
	if !admin {
		writeError(w, 403, "forbidden", "Only an Administrator can author and publish instance templates.")
		return false
	}
	return true
}
func templateModel(catalog []core.ProviderDescriptor, provider, model string) (core.MediaModel, bool) {
	for _, p := range catalog {
		if p.ID == provider {
			for _, m := range p.Models {
				if m.ID == model {
					return m, true
				}
			}
		}
	}
	return core.MediaModel{}, false
}
func templateAuthoringCatalog(catalog []core.ProviderDescriptor) []core.ProviderDescriptor {
	result := []core.ProviderDescriptor{}
	for _, provider := range catalog {
		models := []core.MediaModel{}
		for _, model := range provider.Models {
			mode, err := templates.AuthoringMode(model, "references")
			if err != nil {
				continue
			}
			model.ReferenceModes = []core.ReferenceMode{mode}
			models = append(models, model)
		}
		if len(models) > 0 {
			provider.Models = models
			result = append(result, provider)
		}
	}
	return result
}
func templateWorkflowError(d templates.Definition) string {
	if err := d.WorkflowError(); err != nil {
		return err.Error()
	}
	return ""
}
func (s *Server) templateAdmin(w http.ResponseWriter, r *http.Request) {
	if !s.templatePublisher(w, r) {
		return
	}
	id := r.PathValue("template")
	if readMethod(r.Method) {
		if id == "" {
			items, e := s.templates.List(r.Context(), true)
			if e != nil {
				templateError(w, e)
				return
			}
			s.writeViewerJSON(w, r, 200, map[string]any{"data": items, "catalog": templateAuthoringCatalog(s.visibleCatalog(r))})
			return
		}
		item, e := s.templates.Get(r.Context(), id)
		if e != nil {
			templateError(w, e)
			return
		}
		s.writeViewerJSON(w, r, 200, item)
		return
	}
	var in struct {
		ID      string                `json:"id"`
		Version int64                 `json:"version"`
		Action  string                `json:"action"`
		Draft   *templates.Definition `json:"draft"`
		Content *templates.Definition `json:"content"`
	}
	if !decodeSystemLimit(w, r, &in, 65536) {
		return
	}
	var definition templates.Definition
	if in.Content != nil {
		definition = *in.Content
	}
	if in.Draft != nil {
		if in.Content != nil && !reflect.DeepEqual(*in.Content, *in.Draft) {
			templateError(w, templates.ErrInvalid)
			return
		}
		definition = *in.Draft
	}
	if r.Method == "POST" {
		if in.ID == "" {
			in.ID = uuid.NewString()
		}
		if _, e := uuid.Parse(in.ID); e != nil || in.Version != 0 || in.Action != "" {
			templateError(w, templates.ErrInvalid)
			return
		}
		id = in.ID
		// A create retry can recover a lost response using its client-generated ID.
		if existing, e := s.templates.Get(r.Context(), id); e == nil {
			s.writeViewerJSON(w, r, 200, existing)
			return
		} else if !errors.Is(e, templates.ErrNotFound) {
			templateError(w, e)
			return
		}
	} else if in.Version < 1 {
		templateError(w, templates.ErrConflict)
		return
	}
	if in.Action != "" && in.Action != "publish" && in.Action != "unpublish" {
		templateError(w, templates.ErrInvalid)
		return
	}
	if r.Method == "DELETE" {
		in.Action = "remove"
	}
	var current *templates.Record
	if r.Method != "POST" {
		old, e := s.templates.Get(r.Context(), id)
		if e != nil {
			templateError(w, e)
			return
		}
		if old.Version != in.Version {
			templateError(w, templates.ErrConflict)
			return
		}
		current = &old
		if in.Action == "unpublish" || in.Action == "remove" || (in.Action == "publish" && in.Draft == nil && in.Content == nil) {
			definition = old.Draft
		}
	}
	if in.Draft == nil && in.Content == nil && in.Action == "" {
		templateError(w, templates.ErrInvalid)
		return
	}
	liveSave := in.Action == "publish" || (in.Action == "" && current != nil && current.Published != nil)
	if in.Action != "unpublish" && in.Action != "remove" {
		model, ok := templateModel(s.visibleCatalog(r), definition.Provider, definition.Model)
		if !ok {
			templateError(w, fmt.Errorf("%w: default model is unavailable", templates.ErrInvalid))
			return
		}
		if _, err := templates.AuthoringMode(model, definition.Options.ReferenceMode); err != nil {
			templateError(w, err)
			return
		}
		if err := definition.ValidateGenerationOptions(model); err != nil {
			templateError(w, err)
			return
		}
		if definition.MediaType != model.MediaType {
			templateError(w, templates.ErrInvalid)
			return
		}
	}
	if definition.Options.OverrideSound != nil || definition.Options.OverrideSoundID != "" {
		templateError(w, templates.ErrInvalid)
		return
	}
	if definition.OverrideSoundID != "" {
		if definition.MediaType != "video" {
			templateError(w, templates.ErrInvalid)
			return
		}
		if _, err := s.templateAsset(r.Context(), definition.OverrideSoundID, id, "audio"); err != nil {
			templateError(w, err)
			return
		}
	}
	item := templates.Record{ID: id, Draft: definition}
	if in.Action == "" && definition.Provider != "" && definition.Model != "" {
		if e := s.validateDraftMedia(r, id, definition); e != nil {
			templateError(w, e)
			return
		}
	}
	if liveSave {
		model, ok := templateModel(s.visibleCatalog(r), definition.Provider, definition.Model)
		if !ok {
			templateError(w, fmt.Errorf("%w: default model is unavailable", templates.ErrInvalid))
			return
		}
		if e := definition.Validate(model); e != nil {
			templateError(w, e)
			return
		}
		if e := s.checkTemplateAssets(r.Context(), id, definition); e != nil {
			templateError(w, e)
			return
		}
		if e := s.checkTemplateModelMedia(r.Context(), id, definition, model); e != nil {
			templateError(w, e)
			return
		}
		// Freeze the same defaults admission will use; references are resolved later.
		normalized, e := s.validateTemplateRequest(r.Context(), definition, definition.Provider, model)
		if e != nil {
			templateError(w, e)
			return
		}
		normalized.ImageURLs = nil
		normalized.VideoURLs = nil
		normalized.VideoURL = ""
		normalized.AudioURLs = nil
		normalized.AudioURL = ""
		// Target adaptation must not erase the optional saved preference, including
		// absence. It may apply again when a different model is selected later.
		normalized.GenerateAudio = definition.Options.GenerateAudio
		normalized.Resolution = definition.Options.Resolution
		item.Draft.Options = normalized
	}
	saved, e := s.templates.Save(r.Context(), item, in.Version, in.Action)
	if e != nil {
		templateError(w, e)
		return
	}
	status := 200
	if r.Method == "POST" {
		status = 201
	}
	s.writeViewerJSON(w, r, status, saved)
}
func (s *Server) templateInfer(w http.ResponseWriter, r *http.Request) {
	if !s.templatePublisher(w, r) {
		return
	}
	var d templates.Definition
	if !decodeSystemLimit(w, r, &d, 65536) {
		return
	}
	model, ok := templateModel(s.visibleCatalog(r), d.Provider, d.Model)
	if !ok {
		templateError(w, templates.ErrInvalid)
		return
	}
	syntax := d.Syntax
	if syntax != templates.UIReferenceSyntax {
		syntax = templates.PromptSyntax(model, d.Options.ReferenceMode)
	}
	slots, e := d.InferSlots(model)
	if e != nil {
		templateError(w, e)
		return
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"syntax": syntax, "slots": slots})
}
func templateStorageContext(ctx context.Context) context.Context {
	ctx = identity.WithDataOwnerID(ctx, identity.SharedOwnerID)
	ctx = identity.WithOrganization(ctx, "", false)
	return identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
}
func (s *Server) templateUpload(w http.ResponseWriter, r *http.Request) {
	if !s.templatePublisher(w, r) {
		return
	}
	if !s.storageAdmission(w, r.WithContext(templateStorageContext(r.Context()))) {
		return
	}
	id := r.PathValue("template")
	if _, e := s.templates.Get(r.Context(), id); e != nil {
		templateError(w, e)
		return
	}
	var data []byte
	var filename, sourceID string
	var validation *referencevalidation.Target
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if s.references == nil {
			templateError(w, templates.ErrInvalid)
			return
		}
		release, e := s.references.AdmitMultipart()
		if e != nil {
			s.referenceError(w, e)
			return
		}
		defer release()
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(2 * time.Minute))
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(3 * time.Minute))
		r.Body = http.MaxBytesReader(w, r.Body, references.HardMaxBytes+(1<<20))
		defer r.Body.Close()
		if e = r.ParseMultipartForm(1 << 20); e != nil {
			templateError(w, templates.ErrInvalid)
			return
		}
		defer r.MultipartForm.RemoveAll()
		if raw := r.FormValue("validation"); raw != "" {
			if e = json.Unmarshal([]byte(raw), &validation); e != nil {
				templateError(w, referencevalidation.Invalid("Reference file", "Invalid model validation context."))
				return
			}
		}
		if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 {
			templateError(w, templates.ErrInvalid)
			return
		}
		file, header, e := r.FormFile("file")
		if e != nil {
			templateError(w, e)
			return
		}
		defer file.Close()
		filename = header.Filename
		limit, limitErr := s.referenceUploadLimit(r, validation)
		if limitErr != nil {
			templateError(w, limitErr)
			return
		}
		if header.Size <= 0 || header.Size > limit {
			templateError(w, referencevalidation.Invalid(filename, "The file exceeds the authorized upload size limit."))
			return
		}
		data, e = io.ReadAll(io.LimitReader(file, limit+1))
		if e != nil {
			templateError(w, e)
			return
		}
	} else {
		var in struct {
			ReferenceID        string                      `json:"reference_id"`
			LibraryReferenceID string                      `json:"library_reference_id"`
			Validation         *referencevalidation.Target `json:"validation,omitempty"`
		}
		if !decodeSystem(w, r, &in) {
			return
		}
		validation = in.Validation
		if in.ReferenceID != "" {
			templateError(w, referencevalidation.Invalid("Reference file", "Choose a file from Template references or upload a new file. Private library files are not promoted."))
			return
		}
		if s.references == nil {
			templateError(w, templates.ErrNotFound)
			return
		}
		library, ctx, referenceID := s.references, r.Context(), in.ReferenceID
		if in.LibraryReferenceID != "" {
			if in.ReferenceID != "" || s.templateReferences == nil {
				templateError(w, templates.ErrInvalid)
				return
			}
			library, ctx, referenceID = s.templateReferences, templateLibraryContext(ctx), in.LibraryReferenceID
			sourceID = referenceID
		}
		source, object, e := library.Open(ctx, referenceID)
		if e != nil {
			templateError(w, templates.ErrNotFound)
			return
		}
		defer object.Reader.Close()
		filename = source.Name
		limit, limitErr := s.referenceUploadLimit(r, validation)
		if limitErr != nil {
			templateError(w, limitErr)
			return
		}
		if source.SizeBytes <= 0 || source.SizeBytes > limit || object.SizeBytes != source.SizeBytes {
			templateError(w, referencevalidation.Invalid(filename, "The file exceeds the authorized upload size limit or its stored size changed."))
			return
		}
		data, e = io.ReadAll(io.LimitReader(object.Reader, limit+1))
		if e != nil {
			templateError(w, e)
			return
		}
		digest := sha256.Sum256(data)
		if int64(len(data)) != source.SizeBytes || source.SHA256 != "" && hex.EncodeToString(digest[:]) != source.SHA256 {
			templateError(w, fmt.Errorf("%w: source size or checksum changed; upload the reference again", templates.ErrInvalid))
			return
		}
	}
	file, e := references.InspectFile(filename, data, "")
	if e == nil {
		e = s.referenceValidator(r)(r.Context(), validation, []referencevalidation.File{file}, false)
	}
	if e != nil {
		templateError(w, e)
		return
	}
	kind := file.Info.ContentType
	asset := templates.Asset{ContentType: kind, Name: filename}
	var item references.Image
	if sourceID != "" {
		item, e = s.templateReferences.Get(templateLibraryContext(r.Context()), sourceID)
	} else {
		item, e = s.templateReferences.UploadWithValidation(templateLibraryContext(s.referenceUploadContext(r)), filename, bytes.NewReader(data), validation, s.referenceValidator(r))
	}
	if e != nil {
		templateError(w, e)
		return
	}
	asset.ID, asset.Name, asset.StorageKey, asset.SizeBytes, asset.SHA256, asset.LibraryReferenceID = item.ID, item.Name, item.StorageKey, item.SizeBytes, item.SHA256, item.ID

	asset.StorageOwnerID = item.StorageOwnerID
	if e = s.templates.PutAsset(r.Context(), asset); e != nil {
		templateError(w, e)
		return
	}
	s.writeViewerJSON(w, r, 201, asset)
}
func (s *Server) templateSignature(id string) string {
	if s.mediaSigner == nil {
		return ""
	}
	mac := hmac.New(sha256.New, s.mediaSigner.secret)
	fmt.Fprintf(mac, "template-media:%s\n", id)
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Server) templateAssetURL(id string) string {
	return "/template-media/" + id + "?signature=" + s.templateSignature(id)
}
func (s *Server) templateAsset(ctx context.Context, id, templateID, kind string) (templates.Asset, error) {
	a, e := s.templates.Asset(ctx, id)
	if e != nil {
		return a, e
	}
	if kind != "" && !strings.HasPrefix(a.ContentType, kind+"/") {
		return a, templates.ErrInvalid
	}
	if s.storage == nil {
		return a, templates.ErrNotFound
	}
	object, e := s.storage.Open(templateAssetStorageContext(ctx, a), a.StorageKey)
	if e != nil {
		return a, templates.ErrNotFound
	}
	object.Reader.Close()
	if object.SizeBytes != a.SizeBytes {
		return a, templates.ErrNotFound
	}
	return a, nil
}
func (s *Server) checkTemplateAssets(ctx context.Context, id string, d templates.Definition) error {
	if d.OverrideSoundID != "" {
		if d.MediaType != "video" {
			return templates.ErrInvalid
		}
		if _, err := s.templateAsset(ctx, d.OverrideSoundID, id, "audio"); err != nil {
			return err
		}
	}
	cover, e := s.templateAsset(ctx, d.CoverID, id, "")
	if e != nil {
		return e
	}
	if !strings.HasPrefix(cover.ContentType, "image/") && !strings.HasPrefix(cover.ContentType, "video/") {
		return fmt.Errorf("%w: cover must be a fixed image or video, or separate uploaded media", templates.ErrInvalid)
	}
	for _, slot := range d.Slots {
		if slot.AssetID != "" {
			if _, e = s.templateAsset(ctx, slot.AssetID, id, slot.Type); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Server) templateGallery(w http.ResponseWriter, r *http.Request) {
	items, e := s.templates.List(r.Context(), false)
	if e != nil {
		templateError(w, e)
		return
	}
	data := []any{}
	for _, item := range items {
		if item.Published == nil || item.Draft.WorkflowError() != nil {
			continue
		}
		a, e := s.templates.Asset(r.Context(), item.Draft.CoverID)
		if e != nil {
			continue
		}
		data = append(data, map[string]any{"id": item.ID, "revision": item.Revision, "title": item.Draft.Title, "description": item.Draft.Description, "cover_url": s.templateAssetURL(a.ID), "cover_type": a.ContentType})
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"data": data})
}

// templateMetadata exposes only the current published definition. Availability,
// model compatibility and media bytes are deliberately not represented as valid.
// The normal authentication and model-access middleware remain in force.
func (s *Server) templateMetadata(w http.ResponseWriter, r *http.Request) {
	item, err := s.templates.Get(r.Context(), r.PathValue("template"))
	if err != nil || item.Published == nil {
		templateError(w, templates.ErrNotFound)
		return
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"id": item.ID, "revision": item.Revision, "template": item.Draft, "validation_status": "pending"})
}

func (s *Server) templateUse(w http.ResponseWriter, r *http.Request) {
	item, e := s.templates.Get(r.Context(), r.PathValue("template"))
	if e != nil || item.Published == nil {
		templateError(w, templates.ErrNotFound)
		return
	}
	// Legacy callers may omit the revision. Progressive readers bind expensive
	// validation to the exact lightweight definition they are currently displaying.
	expected, guarded := int64(0), false
	if values, ok := r.URL.Query()["revision"]; ok {
		if len(values) != 1 {
			writeError(w, 400, "invalid_request", "revision must be one non-negative integer")
			return
		}
		var err error
		expected, err = strconv.ParseInt(values[0], 10, 64)
		if err != nil || expected < 0 {
			writeError(w, 400, "invalid_request", "revision must be one non-negative integer")
			return
		}
		guarded = true
		if item.Revision != expected {
			templateError(w, templates.ErrConflict)
			return
		}
	}
	fixedFiles, complete, fixedError := s.fixedTemplateMedia(r.Context(), item.ID, item.Draft)
	catalog := []core.ProviderDescriptor{}
	effectiveResolutions := map[string]map[string]string{}
	defaultAvailable := false
	for _, p := range s.visibleCatalog(r) {
		models := []core.MediaModel{}
		for _, m := range p.Models {
			if item.Draft.Compatible(m) == nil && fixedError == nil && templates.ValidateRequestFiles(m, item.Draft.GenerationOptionsFor(m), fixedFiles, complete) == nil {
				options, e := s.validateTemplateRequest(r.Context(), item.Draft, p.ID, m)
				if e != nil {
					continue
				}
				if effectiveResolutions[p.ID] == nil {
					effectiveResolutions[p.ID] = map[string]string{}
				}
				effectiveResolutions[p.ID][m.ID] = options.Resolution
				models = append(models, m)
				if p.ID == item.Draft.Provider && m.ID == item.Draft.Model {
					defaultAvailable = true
				}
			}
		}
		if len(models) > 0 {
			p.Models = models
			catalog = append(catalog, p)
		}
	}
	assetError := ""
	if e = s.checkTemplateAssets(r.Context(), item.ID, item.Draft); e != nil || fixedError != nil {
		assetError = "A fixed reference or cover is unavailable. Ask an Administrator to repair this template."
	}
	// Expose only the fixed copies in this exact published recipe. Source-library
	// records and draft-only replacements are never part of a consumer response.
	fixed := []map[string]string{}
	for _, slot := range item.Draft.Slots {
		if slot.AssetID == "" {
			continue
		}
		a, err := s.templateAsset(r.Context(), slot.AssetID, item.ID, slot.Type)
		if err != nil {
			continue
		}
		fixed = append(fixed, map[string]string{"slot_id": slot.ID, "asset_id": a.ID, "name": a.Name, "content_type": a.ContentType, "url": s.templateAssetURL(a.ID)})
	}
	if guarded {
		current, err := s.templates.Get(r.Context(), item.ID)
		if err != nil || current.Published == nil {
			templateError(w, templates.ErrNotFound)
			return
		}
		if current.Revision != expected {
			templateError(w, templates.ErrConflict)
			return
		}
	}
	workflowError := templateWorkflowError(item.Draft)
	validationStatus := "failed"
	if assetError == "" && workflowError == "" && len(catalog) > 0 {
		validationStatus = "ready"
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"id": item.ID, "revision": item.Revision, "template": item.Draft, "fixed_references": fixed, "catalog": catalog, "effective_resolutions": effectiveResolutions, "default_available": defaultAvailable, "asset_error": assetError, "workflow_error": workflowError, "validation_status": validationStatus})
}

// Use the same normalized request and native validator as admission, with one
// non-fetchable placeholder for each declared input. This checks settings/count
// interactions even when a user will supply the actual bytes later.
func (s *Server) validateTemplateRequest(ctx context.Context, d templates.Definition, provider string, model core.MediaModel) (core.MediaOptions, error) {
	options, err := d.OptionsFor(model)
	if err != nil {
		return options, err
	}
	for _, slot := range d.Slots {
		value := "https://template.invalid/" + slot.ID
		switch slot.Type {
		case "image":
			options.ImageURLs = append(options.ImageURLs, value)
		case "video":
			options.VideoURLs = append(options.VideoURLs, value)
		case "audio":
			options.AudioURLs = append(options.AudioURLs, value)
		}
	}
	options, err = core.NormalizeModelOptions(model, options)
	if err != nil {
		return options, err
	}
	err = s.service.ValidateMediaRequest(ctx, core.CreateMediaInput{Provider: provider, Model: model.ID, MediaType: d.MediaType, Prompt: d.Prompt, Options: options})
	return options, err
}

type templateGenerationInput struct {
	ExpectedResolution *string           `json:"expected_resolution,omitempty"`
	Revision           int64             `json:"revision"`
	Provider           string            `json:"provider"`
	Model              string            `json:"model"`
	Inputs             map[string]string `json:"inputs"`
	ProjectID          string            `json:"project_id,omitempty"`
	MaxExpectedUSD     *money.USD        `json:"max_expected_cost_usd,omitempty"`
}

func (s *Server) resolveTemplate(r *http.Request, item templates.Record, in templateGenerationInput) (core.CreateMediaInput, error) {
	var input core.CreateMediaInput
	if item.Published == nil {
		return input, templates.ErrNotFound
	}
	if item.Revision != in.Revision {
		return input, templates.ErrConflict
	}
	d := item.Draft
	model, ok := templateModel(s.visibleCatalog(r), in.Provider, in.Model)
	if !ok {
		return input, fmt.Errorf("%w: model is unavailable to this key", templates.ErrInvalid)
	}
	if e := d.Compatible(model); e != nil {
		return input, e
	}
	if e := s.checkTemplateAssets(r.Context(), item.ID, d); e != nil {
		return input, e
	}
	options, e := d.OptionsFor(model)
	if e != nil {
		return input, e
	}
	input = core.CreateMediaInput{Prompt: d.Prompt, Provider: in.Provider, Model: in.Model, MediaType: d.MediaType, PublicModel: model.PublicID, ProjectID: in.ProjectID, MaxExpectedUSD: in.MaxExpectedUSD, Options: options}
	input.Options.OverrideSoundID = d.OverrideSoundID
	input.ReferenceEvidence = map[string]core.InputMediaEvidence{}
	remaining := map[string]string{}
	for k, v := range in.Inputs {
		remaining[k] = v
	}
	resolved := []map[string]any{}
	files := []templates.InputMedia{}
	for _, slot := range d.Slots {
		value := ""
		if slot.AssetID != "" {
			if _, exists := remaining[slot.ID]; exists {
				return input, fmt.Errorf("%w: fixed references cannot be replaced", templates.ErrInvalid)
			}
			asset, e := s.templates.Asset(r.Context(), slot.AssetID)
			if e != nil {
				return input, e
			}
			info, e := s.inspectTemplateMedia(templateAssetStorageContext(r.Context(), asset), asset.StorageKey, asset.ContentType, asset.SizeBytes, asset.SHA256)
			if e != nil {
				return input, e
			}
			files = append(files, templates.InputMedia{Type: slot.Type, Info: info})
			value = s.assetPublicURL + s.templateAssetURL(slot.AssetID)
		} else {
			id, exists := remaining[slot.ID]
			if !exists || s.references == nil {
				return input, fmt.Errorf("%w: supply %s", templates.ErrInvalid, slot.Label)
			}
			delete(remaining, slot.ID)
			ref, refContext, e := s.generationReference(r.Context(), id)
			if e != nil || !strings.HasPrefix(ref.ContentType, slot.Type+"/") {
				return input, fmt.Errorf("%w: %s must be an available %s file owned by this key", templates.ErrInvalid, slot.Label, slot.Type)
			}
			info, e := s.inspectTemplateMedia(references.StorageContext(refContext, ref), ref.StorageKey, ref.ContentType, ref.SizeBytes, ref.SHA256)
			if e != nil {
				return input, e
			}
			files = append(files, templates.InputMedia{Type: slot.Type, Info: info})
			value = s.referenceAbsoluteURL(r.WithContext(refContext), ref)
		}
		if !strings.HasPrefix(value, "https://") && !strings.HasPrefix(value, "http://") {
			return input, fmt.Errorf("%w: public media origin is not configured", templates.ErrInvalid)
		}
		info := files[len(files)-1].Info
		evidence := core.InputMediaEvidence{Type: slot.Type, Duration: info.Duration, ContentType: info.ContentType, SizeBytes: info.Size, Width: info.Width, Height: info.Height, FPS: info.FPS, FrameCount: info.FrameCount, AudioCodecs: core.CloneAudioCodecs(info.AudioCodecs)}
		if info.AudioStreams != nil {
			count := *info.AudioStreams
			evidence.AudioStreams = &count
		}
		if info.VideoStreams != nil {
			count := *info.VideoStreams
			evidence.VideoStreams = &count
		}
		input.ReferenceEvidence[value] = evidence
		switch slot.Type {
		case "image":
			input.Options.ImageURLs = append(input.Options.ImageURLs, value)
		case "video":
			input.Options.VideoURLs = append(input.Options.VideoURLs, value)
		case "audio":
			input.Options.AudioURLs = append(input.Options.AudioURLs, value)
		}
		resolved = append(resolved, map[string]any{"id": slot.ID, "type": slot.Type, "index": slot.Index, "role": slot.Role, "url": value, "fixed": slot.AssetID != ""})
	}
	if len(remaining) > 0 {
		return input, fmt.Errorf("%w: undeclared or extra references", templates.ErrInvalid)
	}
	if e := templates.ValidateRequestFiles(model, input.Options, files, true); e != nil {
		return input, e
	}
	normalized, e := core.NormalizeModelOptions(model, input.Options)
	if e != nil {
		return input, e
	}
	input.Options = normalized
	if err := checkTemplateResolution(in, normalized.Resolution); err != nil {
		return input, err
	}
	snapshot := map[string]any{"id": item.ID, "revision": item.Revision, "prompt": input.Prompt, "references": resolved, "provider": input.Provider, "model": input.Model, "options": input.Options}
	input.TemplateSnapshot, _ = json.Marshal(snapshot)
	return input, nil
}

func checkTemplateResolution(in templateGenerationInput, resolution string) error {
	if in.ExpectedResolution != nil && *in.ExpectedResolution != resolution {
		return fmt.Errorf("%w: model output resolution changed; reload the template and review settings", templates.ErrConflict)
	}
	return nil
}
func (s *Server) templateGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") == "" {
		writeError(w, 401, "generation_key_required", "Use the same generation API key as Playground.")
		return
	}
	var in templateGenerationInput
	if !decodeSystemLimit(w, r, &in, 16384) {
		return
	}
	if strings.HasSuffix(r.URL.Path, "/generations") && (r.Header.Get("Idempotency-Key") == "" || in.MaxExpectedUSD == nil) {
		writeError(w, 400, "invalid_request", "Estimate and confirm the cost, then supply max_expected_cost_usd and Idempotency-Key.")
		return
	}
	e := s.templates.WithPublished(r.Context(), r.PathValue("template"), in.Revision, func(item templates.Record) error {
		input, e := s.resolveTemplate(r, item, in)
		if e != nil {
			return e
		}
		if strings.HasSuffix(r.URL.Path, "/estimate") {
			s.estimateGeneration(w, r, input)
		} else {
			s.submitGeneration(w, r, input)
		}
		return nil
	})
	if e != nil {
		templateError(w, e)
	}
}
func (s *Server) templateAdminMedia(w http.ResponseWriter, r *http.Request) {
	if !s.templatePublisher(w, r) {
		return
	}
	a, e := s.templates.Asset(r.Context(), r.PathValue("asset"))
	if e != nil {
		templateError(w, templates.ErrNotFound)
		return
	}
	if r.URL.Query().Get("preview") == "1" {
		s.serveTemplateAssetContent(w, r, a)
		return
	}
	s.serveTemplateAsset(w, r, a)
}
func (s *Server) templateMedia(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("asset")
	if s.mediaSigner == nil || !hmac.Equal([]byte(r.URL.Query().Get("signature")), []byte(s.templateSignature(id))) {
		http.NotFound(w, r)
		return
	}
	a, e := s.templates.Asset(r.Context(), id)
	if e != nil || !a.Published {
		http.NotFound(w, r)
		return
	}
	s.serveTemplateAsset(w, r, a)
}
func (s *Server) serveTemplateAsset(w http.ResponseWriter, r *http.Request, a templates.Asset) {
	if s.storage == nil {
		http.NotFound(w, r)
		return
	}
	ctx := templateAssetStorageContext(r.Context(), a)
	if direct, ok := s.storage.(storage.DirectReader); ok {
		location, e := direct.DirectURL(ctx, a.StorageKey, r.Method, a.ContentType, "inline", 15*time.Minute)
		if e != nil {
			templateError(w, e)
			return
		}
		if location != "" {
			if !isolatedMediaDestination(r, location) {
				writeError(w, 503, "unsafe_storage_origin", "media requires a separate storage origin")
				return
			}
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Cache-Control", "private, no-store")
			http.Redirect(w, r, location, 307)
			return
		}
	}
	s.serveTemplateAssetContent(w, r, a)
}

// Explicit authenticated editor preview, never an error-triggered S3 fallback.
// Generation/provider capabilities continue using direct signed storage URLs.
func (s *Server) serveTemplateAssetContent(w http.ResponseWriter, r *http.Request, a templates.Asset) {
	if s.storage == nil {
		http.NotFound(w, r)
		return
	}
	ctx := templateAssetStorageContext(r.Context(), a)
	object, e := s.storage.Open(ctx, a.StorageKey)
	if e != nil {
		templateError(w, e)
		return
	}
	defer object.Reader.Close()
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	http.ServeContent(w, r, a.ID, object.ModTime, object.Reader)
}

func (s *Server) inspectTemplateMedia(ctx context.Context, key, kind string, size int64, digest string) (mediafile.Info, error) {
	object, e := s.storage.Open(ctx, key)
	if e != nil {
		return mediafile.Info{}, templates.ErrNotFound
	}
	defer object.Reader.Close()
	if size <= 0 || size > references.HardMaxBytes || object.SizeBytes != size {
		return mediafile.Info{}, templates.ErrNotFound
	}
	data, e := io.ReadAll(io.LimitReader(object.Reader, size+1))
	if e != nil || int64(len(data)) != size {
		return mediafile.Info{}, templates.ErrNotFound
	}
	sum := sha256.Sum256(data)
	if digest != "" && hex.EncodeToString(sum[:]) != digest {
		return mediafile.Info{}, templates.ErrNotFound
	}
	detected, e := references.InspectMedia(data)
	if e != nil || mediafile.CanonicalType(detected) != mediafile.CanonicalType(kind) {
		return mediafile.Info{}, templates.ErrInvalid
	}
	file, err := references.InspectFile("Template reference", data, kind)
	return file.Info, err
}
func (s *Server) checkTemplateModelMedia(ctx context.Context, id string, d templates.Definition, model core.MediaModel) error {
	files, complete, e := s.fixedTemplateMedia(ctx, id, d)
	if e != nil {
		return e
	}
	return templates.ValidateRequestFiles(model, d.Options, files, complete)
}
func (s *Server) fixedTemplateMedia(ctx context.Context, id string, d templates.Definition) ([]templates.InputMedia, bool, error) {
	files := []templates.InputMedia{}
	complete := true
	for _, slot := range d.Slots {
		if slot.AssetID == "" {
			complete = false
			continue
		}
		a, e := s.templateAsset(ctx, slot.AssetID, id, slot.Type)
		if e != nil {
			return nil, false, e
		}
		info, e := s.inspectTemplateMedia(templateAssetStorageContext(ctx, a), a.StorageKey, a.ContentType, a.SizeBytes, a.SHA256)
		if e != nil {
			return nil, false, e
		}
		actualType := strings.SplitN(info.ContentType, "/", 2)[0]
		if slot.Type != actualType {
			return nil, false, referencevalidation.Invalid(slot.Label, "Detected "+actualType+" media does not match the declared "+slot.Type+" input.")
		}
		files = append(files, templates.InputMedia{Type: slot.Type, Name: slot.Label, Info: info})
	}
	return files, complete, nil
}

// Compatibility route now lists only the instance-wide creator collection.
func (s *Server) templateLibrary(w http.ResponseWriter, r *http.Request) {
	s.sharedTemplateReferences(w, r)
}

func templateAssetStorageContext(ctx context.Context, asset templates.Asset) context.Context {
	ctx = templateStorageContext(ctx)
	owner := asset.StorageOwnerID
	if owner == "" && asset.LibraryReferenceID == asset.ID {
		owner = templates.LibraryOwnerID
	}
	if owner != "" {
		ctx = identity.WithDataOwnerID(ctx, owner)
	}
	return ctx
}
