package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func (s *Server) estimateGeneration(w http.ResponseWriter, r *http.Request, input core.CreateMediaInput) {
	if err := s.resolveOverrideSound(r, &input); err != nil {
		s.referenceError(w, err)
		return
	}
	if err := s.prepareGenerationReferences(r, &input); err != nil {
		s.referenceError(w, err)
		return
	}
	estimator, ok := s.service.(interface {
		Estimate(context.Context, core.CreateMediaInput) (core.CostEstimate, error)
	})
	if !ok {
		writeError(w, 503, "service_unavailable", "cost estimation is unavailable")
		return
	}
	quote, err := estimator.Estimate(r.Context(), input)
	switch {
	case errors.Is(err, core.ErrProjectInvalid):
		invalidParameter(w, "project_id", err.Error())
	case errors.Is(err, core.ErrCostLimitInvalid):
		invalidParameter(w, "max_expected_cost_usd", err.Error())
	case errors.Is(err, core.ErrPricingUnavailable):
		writeError(w, 422, "pricing_unavailable", err.Error())
	case errors.Is(err, core.ErrKeyUnavailable):
		writeError(w, 401, "unauthorized", err.Error())
	case errors.Is(err, core.ErrOptionsInvalid), errors.Is(err, core.ErrModelUnavailable), errors.Is(err, core.ErrPromptRequired), errors.Is(err, core.ErrPromptTooLong), errors.Is(err, core.ErrProviderUnavailable), errors.Is(err, core.ErrModelRequired), errors.Is(err, core.ErrWebhookInvalid):
		writeError(w, 400, "invalid_request", err.Error())
	case err != nil:
		writeError(w, 503, "service_unavailable", "could not calculate an estimate")
	default:
		s.writeViewerJSON(w, r, 200, quote)
	}
}

func (s *Server) billingStatus(w http.ResponseWriter, r *http.Request) {
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin {
		writeError(w, 403, "forbidden", "Provider billing is available to administrators only")
		return
	}
	if _, ok := personalOwner(w, r); !ok {
		return
	}
	reader, ok := s.service.(interface {
		BillingStatus(context.Context) ([]core.BillingStatus, error)
	})
	if !ok {
		s.writeViewerJSON(w, r, 200, map[string]any{"data": []core.BillingStatus{}})
		return
	}
	status, err := reader.BillingStatus(r.Context())
	if err != nil {
		writeError(w, 503, "service_unavailable", "billing status is unavailable")
		return
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"data": status})
}
