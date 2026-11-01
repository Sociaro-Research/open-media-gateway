package api

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

func (s *Server) registerProviderResourceRoutes(mux *http.ServeMux) {
	mux.Handle("POST /v1/provider-resources/{id}/actions/{action}", s.providerResourceRoute(s.actOnResource))
	mux.Handle("POST /v1/provider-resources/{id}/actions/{action}/estimate", s.providerResourceRoute(s.actOnResource))
	mux.Handle("GET /v1/provider-resources", s.providerResourceRoute(s.listResources))
	mux.Handle("POST /v1/provider-resources/estimate", s.providerResourceRoute(s.createResource))
	mux.Handle("POST /v1/provider-resources", s.providerResourceRoute(s.createResource))
	mux.Handle("GET /v1/provider-resources/{id}", s.providerResourceRoute(s.getResource))
	mux.Handle("DELETE /v1/provider-resources/{id}", s.providerResourceRoute(s.deleteResource))
	mux.Handle("GET /v1/provider-resource-jobs/{id}", s.providerResourceRoute(s.getResourceJob))
}
func (s *Server) providerResourceRoute(next http.HandlerFunc) http.Handler {
	return s.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := identity.OrganizationFromContext(r.Context()).Role
		if role != "" && (!org.OwnContent(role) || role == org.ContentOfficer && r.Method != "GET") {
			orgError(w, org.ErrForbidden)
			return
		}
		if s.resourceRepository == nil || s.resourceScopes == nil {
			writeError(w, 503, "resource_service_unavailable", "Provider resources are unavailable.")
			return
		}
		next(w, r)
	}))
}
func (s *Server) resourceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, providerresources.ErrNotFound), errors.Is(err, storage.ErrObjectNotFound), errors.Is(err, core.ErrTaskNotFound):
		writeError(w, 404, "not_found", "Provider resource not found.")
	case errors.Is(err, providerresources.ErrConflict), errors.Is(err, providerresources.ErrInUse):
		writeError(w, 409, "resource_in_use", "The resource is in use or its lifecycle changed. Refresh before retrying.")
	case errors.Is(err, providerresources.ErrInvalid), errors.Is(err, core.ErrOptionsInvalid):
		writeError(w, 400, "invalid_resource", "Invalid provider resource request.")
	default:
		writeError(w, 503, "resource_service_unavailable", "The resource operation could not be confirmed. Read its job before retrying.")
	}
}

type resourceSessionView struct {
	ExpiresAt           time.Time             `json:"expires_at"`
	SourceVideoDuration float64               `json:"source_video_duration"`
	Faces               []resourceFaceView    `json:"faces,omitempty"`
	FrameCount          int                   `json:"frame_count,omitempty"`
	FPS                 float64               `json:"fps,omitempty"`
	SelectionRevision   int64                 `json:"selection_revision,omitempty"`
	Selections          []core.FrameSelection `json:"selections,omitempty"`
	HasSelection        bool                  `json:"has_selection"`
}
type resourceFaceView struct {
	Index             int    `json:"index"`
	StartMS           int64  `json:"start_ms"`
	EndMS             int64  `json:"end_ms"`
	PreviewArtifactID string `json:"preview_artifact_id,omitempty"`
}
type resourceBindingView struct {
	GenerationID string `json:"generation_id,omitempty"`
	providerresources.Binding
	Usable            bool                 `json:"usable"`
	UnavailableReason string               `json:"unavailable_reason,omitempty"`
	Session           *resourceSessionView `json:"session,omitempty"`
}

func (s *Server) resourceView(r *http.Request, b providerresources.Binding) resourceBindingView {
	view := resourceBindingView{Binding: b, GenerationID: b.OriginTaskID}
	scope, err := s.resourceScopes(r.Context(), b.ProviderID)
	view.Usable = err == nil && b.Status == providerresources.Succeeded && scope.ID == b.ConnectionID && scope.OwnerID == b.ProviderOwnerID && scope.Revision == b.Revision
	if !view.Usable {
		view.UnavailableReason = "resource_or_connection_unavailable"
	}
	if b.Session != nil {
		e := b.Session
		v := &resourceSessionView{ExpiresAt: e.ExpiresAt, SourceVideoDuration: e.SourceVideoDuration, FrameCount: e.FrameCount, FPS: e.FPS, SelectionRevision: e.SelectionRevision, Selections: e.Selections, HasSelection: e.HasSelection}
		for i, f := range e.Faces {
			v.Faces = append(v.Faces, resourceFaceView{Index: i + 1, StartMS: f.StartMS, EndMS: f.EndMS, PreviewArtifactID: f.PreviewArtifactID})
		}
		view.Session = v
		if !e.ExpiresAt.After(time.Now()) {
			view.Usable = false
			view.UnavailableReason = "session_expired"
		}
	}
	return view
}
func (s *Server) listResources(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		invalidParameter(w, "provider", "provider is required")
		return
	}
	limit, offset := 50, 0
	for key, target := range map[string]*int{"limit": &limit, "offset": &offset} {
		if raw := r.URL.Query().Get(key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 || key == "limit" && (n < 1 || n > 100) || key == "offset" && n > 1000000 {
				invalidParameter(w, key, "Invalid pagination value.")
				return
			}
			*target = n
		}
	}
	bindings, err := s.resourceRepository.ListBindings(r.Context(), provider, limit, offset)
	if err != nil {
		s.resourceError(w, err)
		return
	}
	data := []resourceBindingView{}
	kind := r.URL.Query().Get("kind")
	for _, binding := range bindings {
		if kind == "" || binding.Kind == kind {
			data = append(data, s.resourceView(r, binding))
		}
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"object": "list", "data": data, "limit": limit, "offset": offset})
}
func (s *Server) getResource(w http.ResponseWriter, r *http.Request) {
	binding, err := s.resourceRepository.GetBinding(r.Context(), r.PathValue("id"))
	if err != nil {
		s.resourceError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, s.resourceView(r, binding))
}
func (s *Server) deleteResource(w http.ResponseWriter, r *http.Request) {
	if s.resourceLifecycle == nil {
		writeError(w, 503, "resource_service_unavailable", "Provider resource lifecycle is unavailable.")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = "delete:" + r.PathValue("id")
	}
	job, err := s.resourceLifecycle.Delete(r.Context(), r.PathValue("id"), key)
	if err != nil {
		s.resourceError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 202, job)
}
func (s *Server) getResourceJob(w http.ResponseWriter, r *http.Request) {
	if s.resourceLifecycle == nil {
		writeError(w, 503, "resource_service_unavailable", "Provider resource lifecycle is unavailable.")
		return
	}
	job, err := s.resourceLifecycle.Poll(r.Context(), r.PathValue("id"))
	if err != nil {
		s.resourceError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, job)
}

type resourceCreateInput struct {
	Provider            string     `json:"provider"`
	Model               string     `json:"model,omitempty"`
	Kind                string     `json:"kind"`
	Subtype             string     `json:"subtype,omitempty"`
	Name                string     `json:"name"`
	Description         string     `json:"description,omitempty"`
	ReferenceIDs        []string   `json:"reference_ids,omitempty"`
	GeneratedTaskID     string     `json:"generated_task_id,omitempty"`
	BoundVoiceBindingID string     `json:"bound_voice_binding_id,omitempty"`
	Tags                []string   `json:"tags,omitempty"`
	MaxExpectedUSD      *money.USD `json:"max_expected_usd,omitempty"`
}

func (s *Server) createResource(w http.ResponseWriter, r *http.Request) {
	var body resourceCreateInput
	if !decodeSystem(w, r, &body) {
		return
	}
	var model core.MediaModel
	found := false
	for _, provider := range s.visibleCatalog(r) {
		if provider.ID != body.Provider {
			continue
		}
		for _, candidate := range provider.Models {
			if candidate.ResourceOperation != nil && candidate.ResourceOperation.Kind == body.Kind && (body.Model == "" && !candidate.Manual || body.Model == candidate.ID || body.Model == candidate.PublicID) {
				model = candidate
				found = true
				break
			}
		}
	}
	if !found {
		invalidParameter(w, "model", "No authorized resource creation operation matches this request.")
		return
	}
	if body.Subtype == "" && len(model.ResourceOperation.Subtypes) == 1 {
		body.Subtype = model.ResourceOperation.Subtypes[0]
	}
	intent := &core.ResourceIntent{Kind: body.Kind, Subtype: body.Subtype, Name: body.Name, Description: body.Description, ReferenceIDs: body.ReferenceIDs, GeneratedTaskID: body.GeneratedTaskID, BoundVoiceBindingID: body.BoundVoiceBindingID, Tags: body.Tags}
	options := core.MediaOptions{ResourceIntent: intent}
	if err := core.ValidateResourceCreationIntent(model, options, true); err != nil {
		s.resourceError(w, err)
		return
	}
	input := core.CreateMediaInput{Provider: body.Provider, Model: model.ID, PublicModel: model.PublicID, MediaType: model.MediaType, MaxExpectedUSD: body.MaxExpectedUSD, Options: options}
	creation, err := s.resolveResourceCreation(r, intent, body.Provider, model)
	input.ResourceCreation = creation
	input.AdmissionError = err
	if strings.HasSuffix(r.URL.Path, "/estimate") {
		if err != nil {
			s.resourceError(w, err)
			return
		}
		s.estimateGeneration(w, r, input)
		return
	}
	// Resource creation has its own inspected inputs, not free-form media URLs.
	// It still uses the existing priced, model-authorized, idempotent admission.
	task, err := s.service.CreateMediaRequest(r.Context(), input, r.Header.Get("Idempotency-Key"))
	if err != nil {
		if input.AdmissionError != nil && errors.Is(err, input.AdmissionError) {
			s.resourceError(w, err)
			return
		}
		s.resourceAdmissionError(w, err)
		return
	}
	s.writeGenerationJSON(w, r, 202, task)
}
func (s *Server) resolveResourceCreation(r *http.Request, intent *core.ResourceIntent, provider string, model core.MediaModel) (*core.ResourceCreateRequest, error) {
	request := &core.ResourceCreateRequest{Kind: intent.Kind, Subtype: intent.Subtype, Name: intent.Name, Description: intent.Description, Tags: append([]string(nil), intent.Tags...)}
	if s.references == nil || s.mediaSigner == nil {
		return nil, providerresources.ErrInvalid
	}
	for _, id := range intent.ReferenceIDs {
		ref, readContext, err := s.generationReference(r.Context(), id)
		if err != nil {
			return nil, err
		}
		inspected, err := s.references.Inspect(readContext, id)
		if err != nil {
			return nil, err
		}
		if err := validateResourceSourceFile(model, intent.Subtype, inspected); err != nil {
			return nil, err
		}
		sourceURL, err := references.CanonicalReferenceURL(s.assetPublicURL, s.mediaSigner.secret, identity.OwnerID(readContext), ref.ID)
		if err != nil {
			return nil, err
		}
		info := inspected.Info
		source := core.ResourceSourceInput{AudioCodecs: core.CloneAudioCodecs(info.AudioCodecs), ReferenceID: ref.ID, URL: sourceURL, MediaType: inspected.Type, ContentType: info.ContentType, SizeBytes: info.Size, Width: info.Width, Height: info.Height, SHA256: inspected.SHA256, FrameCount: info.FrameCount, FPS: info.FPS, AudioStreams: info.AudioStreams, VideoStreams: info.VideoStreams}
		if info.Duration > 0 {
			duration := info.Duration
			source.Duration = &duration
		}
		switch {
		case intent.Kind == "file_resource":
			request.FileSources = append(request.FileSources, source)
		case intent.Kind == "voice":
			request.VoiceSources = append(request.VoiceSources, source)
		case inspected.Type == "image":
			request.Images = append(request.Images, source)
		case inspected.Type == "video":
			request.Videos = append(request.Videos, source)
		default:
			return nil, providerresources.ErrInvalid
		}
	}
	if intent.BoundVoiceBindingID != "" {
		resolver := providerresources.New(s.resourceRepository, s.resourceScopes)
		snapshots, err := resolver.ResolveResourceReferences(r.Context(), provider, []core.ResourceReference{{BindingID: intent.BoundVoiceBindingID, ReferenceID: "bound_voice"}})
		if err != nil || len(snapshots) != 1 || snapshots[0].Kind != "voice" {
			return nil, providerresources.ErrNotFound
		}
		request.BoundVoice = &snapshots[0]
	}
	if intent.GeneratedTaskID != "" {
		task, err := s.service.GetTask(r.Context(), intent.GeneratedTaskID)
		if err != nil {
			return nil, err
		}
		scope, err := s.resourceScopes(r.Context(), provider)
		if err != nil || task.Provider != provider || task.ProviderIdentity != scope.Revision || task.ProviderOwnerID != scope.OwnerID || task.Status != core.TaskSucceeded || task.ResultDeletedAt != nil || task.MediaType != "video" || task.ProviderTaskID == nil || task.CreatedAt.Before(time.Now().Add(-30*24*time.Hour)) || len(task.StoredArtifacts) == 0 {
			return nil, providerresources.ErrNotFound
		}
		primary := task.StoredArtifacts[0]
		if primary.MediaType != "video" || primary.ProviderID == "" || primary.SHA256 == "" || primary.Duration < 0 || math.IsNaN(primary.Duration) || math.IsInf(primary.Duration, 0) {
			return nil, providerresources.ErrInvalid
		}
		var duration *float64
		if primary.Duration > 0 {
			d := primary.Duration
			duration = &d
		} else {
			permitted := false
			if model.ResourceOperation != nil {
				for _, variant := range model.ResourceOperation.Variants {
					if variant.Subtype == intent.Subtype {
						for _, rule := range variant.GeneratedSources {
							if rule.Model == task.Model && rule.AllowUnknownDuration {
								permitted = true
							}
						}
					}
				}
			}
			if !permitted {
				return nil, providerresources.ErrInvalid
			}
		}
		inspection, err := s.inspectGeneratedVideo(r.Context(), task, primary)
		if err != nil {
			return nil, err
		}
		// Keep the original optional native duration distinct from the
		// metadata observed in these exact archived bytes.
		request.GeneratedVideo = &core.GeneratedResourceSource{TaskID: task.ID, TaskHandle: *task.ProviderTaskID, OutputID: primary.ProviderID, SHA256: primary.SHA256, Operation: task.Model, Model: task.Model, NativeAudio: task.Options.GenerateAudio != nil && *task.Options.GenerateAudio, Duration: duration, Inspection: &inspection}
	}
	return request, nil
}

func (s *Server) resourceAdmissionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrEstimateChanged):
		writeError(w, 409, "estimate_changed", core.ErrEstimateChanged.Error())
	case errors.Is(err, core.ErrPricingUnavailable):
		writeError(w, 422, "pricing_unavailable", core.ErrPricingUnavailable.Error())
	case errors.Is(err, core.ErrBudgetExceeded):
		writeError(w, 402, "budget_exceeded", core.ErrBudgetExceeded.Error())
	case errors.Is(err, core.ErrIdempotencyConflict):
		writeError(w, 409, "idempotency_conflict", core.ErrIdempotencyConflict.Error())
	case errors.Is(err, core.ErrCostLimitInvalid), errors.Is(err, core.ErrOptionsInvalid), errors.Is(err, core.ErrModelUnavailable), errors.Is(err, core.ErrProviderUnavailable), errors.Is(err, core.ErrIdempotencyKey):
		writeError(w, 400, "invalid_request", err.Error())
	default:
		writeError(w, 503, "resource_service_unavailable", "The generation could not be admitted.")
	}
}
