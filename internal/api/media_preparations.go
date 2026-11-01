package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

// Workflow-scoped input metadata is not a second public model catalog.
type preparationSchema struct {
	Operation string                        `json:"operation"`
	Label     string                        `json:"label"`
	Outputs   []core.ResourceOutputSchema   `json:"outputs,omitempty"`
	Create    *core.ResourceOperationSchema `json:"create,omitempty"`
	Action    *core.SessionOperationSchema  `json:"action,omitempty"`
	Limits    *core.ReferenceMediaLimits    `json:"reference_media_limits,omitempty"`
}

func (s *Server) mediaWorkflowPreparations(r *http.Request, provider, workflow string) (core.MediaModel, []core.MediaModel, error) {
	var consumer core.MediaModel
	for _, p := range s.visibleCatalog(r) {
		if p.ID == provider {
			for _, m := range p.Models {
				if (m.ID == workflow || m.PublicID != "" && m.PublicID == workflow) && core.MediaGenerationModel(m) {
					consumer = m
					break
				}
			}
		}
	}
	if consumer.ID == "" {
		return consumer, nil, core.ErrModelUnavailable
	}
	service, ok := s.service.(interface {
		PreparationModelsForWorkflow(context.Context, string, string) ([]core.MediaModel, error)
	})
	if !ok {
		return consumer, nil, core.ErrProviderUnavailable
	}
	models, err := service.PreparationModelsForWorkflow(r.Context(), provider, consumer.ID)
	return consumer, models, err
}
func (s *Server) mediaPreparationSchemas(w http.ResponseWriter, r *http.Request) {
	consumer, models, err := s.mediaWorkflowPreparations(r, r.URL.Query().Get("provider"), r.PathValue("model"))
	if err != nil {
		invalidParameter(w, "model", "The selected media workflow is unavailable.")
		return
	}
	out := []preparationSchema{}
	for _, m := range models {
		outputs := m.ResourceOutputs
		if len(outputs) == 0 && m.ResourceOperation != nil {
			outputs = []core.ResourceOutputSchema{{Kind: m.ResourceOperation.Kind}}
		}
		out = append(out, preparationSchema{Operation: m.ID, Label: m.DisplayName, Outputs: outputs, Create: m.ResourceOperation, Action: m.SessionOperation, Limits: m.ReferenceMediaLimits})
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"workflow_model": consumer.ID, "data": out})
}

// Face detection is only an inspected input preparation for a media consumer.
// No arbitrary JSON/text operation, upstream handle or client URL is accepted.
func (s *Server) prepareFaceInput(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider       string     `json:"provider"`
		WorkflowModel  string     `json:"workflow_model"`
		ReferenceID    string     `json:"reference_id"`
		MaxExpectedUSD *money.USD `json:"max_expected_usd,omitempty"`
	}
	if !decodeSystem(w, r, &body) {
		return
	}
	consumer, models, err := s.mediaWorkflowPreparations(r, body.Provider, body.WorkflowModel)
	var preparation core.MediaModel
	if err == nil {
		for _, m := range models {
			if m.ResourceOperation == nil && m.SessionOperation == nil && len(m.ResourceOutputs) == 1 && m.ResourceOutputs[0].Kind == "face_session" {
				preparation = m
				break
			}
		}
	}
	if preparation.ID == "" || s.references == nil || s.mediaSigner == nil {
		invalidParameter(w, "workflow_model", "No face input preparation is available for this media workflow.")
		return
	}
	ref, readContext, err := s.generationReference(r.Context(), body.ReferenceID)
	if err != nil {
		s.referenceError(w, err)
		return
	}
	inspected, err := s.references.Inspect(readContext, ref.ID)
	if err != nil {
		s.referenceError(w, err)
		return
	}
	if err := referencevalidation.ValidateFiles(preparation, []referencevalidation.File{inspected}, true); err != nil {
		s.referenceError(w, err)
		return
	}
	if inspected.Type != "video" {
		s.resourceError(w, providerresources.ErrInvalid)
		return
	}
	url, err := references.CanonicalReferenceURL(s.assetPublicURL, s.mediaSigner.secret, identity.OwnerID(readContext), ref.ID)
	if err != nil {
		s.resourceError(w, err)
		return
	}
	info := inspected.Info
	evidence := core.InputMediaEvidence{ReferenceID: ref.ID, Type: "video", ContentType: info.ContentType, SizeBytes: info.Size, Width: info.Width, Height: info.Height, Duration: info.Duration, SHA256: inspected.SHA256, FrameCount: info.FrameCount, FPS: info.FPS, AudioStreams: info.AudioStreams, VideoStreams: info.VideoStreams, AudioCodecs: core.CloneAudioCodecs(info.AudioCodecs)}
	input := core.CreateMediaInput{Provider: body.Provider, Model: preparation.ID, MediaType: preparation.MediaType, MaxExpectedUSD: body.MaxExpectedUSD, Options: core.MediaOptions{VideoURLs: []string{url}}, ReferenceEvidence: map[string]core.InputMediaEvidence{url: evidence}}
	r = r.WithContext(core.WithMediaPreparationWorkflow(r.Context(), body.Provider, consumer.ID, preparation.ID, "", consumer.PublicID))
	if r.URL.Path == "/v1/media-inputs/faces/estimate" {
		s.estimateMediaPreparation(w, r, input)
		return
	}
	task, err := s.service.CreateMediaRequest(r.Context(), input, r.Header.Get("Idempotency-Key"))
	if err != nil {
		if errors.Is(err, core.ErrModelUnavailable) {
			invalidParameter(w, "model", "Input preparation is unavailable.")
			return
		}
		s.resourceAdmissionError(w, err)
		return
	}
	s.writeGenerationJSON(w, r, 202, task)
}

// Inputs and the media-consumer scope have already been resolved here. The
// ordinary HTTP generation estimator deliberately accepts public media models
// only, so do not send a private preparation back through that decoder.
func (s *Server) estimateMediaPreparation(w http.ResponseWriter, r *http.Request, input core.CreateMediaInput) {
	estimator, ok := s.service.(interface {
		Estimate(context.Context, core.CreateMediaInput) (core.CostEstimate, error)
	})
	if !ok {
		writeError(w, 503, "service_unavailable", "Cost estimation is unavailable.")
		return
	}
	quote, err := estimator.Estimate(r.Context(), input)
	if err != nil {
		s.resourceAdmissionError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, quote)
}
