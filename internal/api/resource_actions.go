package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
)

type resourceActionInput struct {
	Model            string               `json:"model,omitempty"`
	ExpectedRevision *int64               `json:"expected_revision"`
	Frame            *core.FrameSelection `json:"frame,omitempty"`
	Recover          bool                 `json:"recover,omitempty"`
	MaxExpectedUSD   *money.USD           `json:"max_expected_usd,omitempty"`
}

func isResourceActionPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) >= 5 && len(parts) <= 6 && parts[0] == "v1" && parts[1] == "provider-resources" && parts[2] != "" && parts[3] == "actions" && parts[4] != "" && (len(parts) == 5 || parts[5] == "estimate")
}

// Actions use the same immutable, priced task admission as generation. The
// caller supplies local binding identity and a revision, never native session IDs.
func (s *Server) actOnResource(w http.ResponseWriter, r *http.Request) {
	var body resourceActionInput
	if !decodeSystem(w, r, &body) {
		return
	}
	if body.ExpectedRevision == nil {
		invalidParameter(w, "expected_revision", "The current session revision is required.")
		return
	}
	binding, err := s.resourceRepository.GetBinding(r.Context(), r.PathValue("id"))
	if err != nil {
		s.resourceError(w, err)
		return
	}
	var model core.MediaModel
	found := false
	for _, provider := range s.visibleCatalog(r) {
		if provider.ID != binding.ProviderID {
			continue
		}
		for _, candidate := range provider.Models {
			schema := candidate.SessionOperation
			if schema != nil && schema.Kind == binding.Kind && schema.Action == r.PathValue("action") && (body.Model == "" && !candidate.Manual || body.Model == candidate.ID || body.Model == candidate.PublicID) {
				model, found = candidate, true
				break
			}
		}
	}
	if !found {
		invalidParameter(w, "action", "No authorized session operation matches this request.")
		return
	}
	action := &core.SessionActionIntent{BindingID: binding.ID, Action: r.PathValue("action"), ExpectedRevision: *body.ExpectedRevision, Frame: body.Frame, Recover: body.Recover}
	options := core.MediaOptions{SessionAction: action, ResourceReferences: []core.ResourceReference{{BindingID: binding.ID, ReferenceID: binding.Kind + "_1"}}}
	if err := core.ValidateResourceReferenceIntent(model, options); err != nil {
		s.resourceError(w, err)
		return
	}
	if err := core.ValidateSessionAction(model, options, true); err != nil {
		s.resourceError(w, err)
		return
	}
	input := core.CreateMediaInput{Provider: binding.ProviderID, Model: model.ID, PublicModel: model.PublicID, MediaType: model.MediaType, MaxExpectedUSD: body.MaxExpectedUSD, Options: options}
	if strings.HasSuffix(r.URL.Path, "/estimate") {
		estimator, ok := s.service.(interface {
			Estimate(context.Context, core.CreateMediaInput) (core.CostEstimate, error)
		})
		if !ok {
			writeError(w, 503, "service_unavailable", "Cost estimation is unavailable.")
			return
		}
		quote, err := estimator.Estimate(r.Context(), input)
		if err != nil {
			s.sessionAdmissionError(w, err)
			return
		}
		s.writeViewerJSON(w, r, 200, quote)
		return
	}
	task, err := s.service.CreateMediaRequest(r.Context(), input, r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.sessionAdmissionError(w, err)
		return
	}
	s.writeGenerationJSON(w, r, 202, task)
}

func (s *Server) sessionAdmissionError(w http.ResponseWriter, err error) {
	if errors.Is(err, providerresources.ErrNotFound) || errors.Is(err, providerresources.ErrConflict) || errors.Is(err, providerresources.ErrInvalid) || errors.Is(err, core.ErrOptionsInvalid) {
		s.resourceError(w, err)
		return
	}
	s.resourceAdmissionError(w, err)
}
