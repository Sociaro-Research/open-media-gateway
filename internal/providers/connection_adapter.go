package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

type manualRequestKey struct{}
type manualRequest struct {
	model string
	body  map[string]any
}
type connectionTransport struct {
	base        http.RoundTripper
	credentials Credentials
}

func (t *connectionTransport) RoundTrip(original *http.Request) (*http.Response, error) {
	c := t.credentials.Connection
	request := original.Clone(original.Context())
	// An adapter may only send credentials to its configured API origins.
	allowed := false
	for _, base := range []string{t.credentials.BaseURL, t.credentials.MusicBaseURL} {
		u, err := url.Parse(base)
		if err == nil && u.Host != "" && u.Scheme == original.URL.Scheme && u.Host == original.URL.Host {
			allowed = true
		}
	}
	if !allowed {
		return nil, errors.New("provider request used an unconfigured API origin")
	}
	operation := "generate"
	if original.Method == http.MethodGet {
		operation = "poll"
		if strings.Contains(original.URL.Path, "/models") {
			operation = "models"
		}
	}
	if endpoint := c.Endpoints[operation]; endpoint != "" {
		endpoint = strings.ReplaceAll(endpoint, "{task_id}", url.PathEscape(path.Base(original.URL.Path)))
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, errors.New("invalid provider endpoint")
		}
		if !u.IsAbs() {
			u = original.URL.ResolveReference(u)
		}
		if u.RawQuery == "" {
			u.RawQuery = original.URL.RawQuery
		}
		request.URL = u
		request.Host = ""
	}
	for _, scope := range []string{"", operation} {
		for _, h := range c.Headers {
			if h.Operation == scope && h.Value != nil {
				request.Header.Set(h.Name, *h.Value)
			}
		}
	}
	if operation == "generate" && request.Body != nil && strings.Contains(request.Header.Get("Content-Type"), "application/json") {
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, errors.New("cannot read provider request")
		}
		_ = request.Body.Close()
		var body map[string]any
		if err = json.Unmarshal(raw, &body); err != nil {
			return nil, errors.New("cannot transform provider request")
		}
		mergeBody(body, c.ExtraBody)
		if manual, ok := original.Context().Value(manualRequestKey{}).(manualRequest); ok {
			mergeBody(body, manual.body)
			body["model"] = manual.model
		}
		raw, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
		request.Body = io.NopCloser(bytes.NewReader(raw))
		request.ContentLength = int64(len(raw))
		request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, errors.New("provider API request failed")
	}
	return response, nil
}

// Administrator defaults override generated optional parameters recursively. Input
// content and the upstream model are protected by configuration validation.
func mergeBody(dst, src map[string]any) {
	for key, value := range src {
		if child, ok := value.(map[string]any); ok {
			target, ok := dst[key].(map[string]any)
			if !ok {
				target = map[string]any{}
				dst[key] = target
			}
			mergeBody(target, child)
		} else {
			dst[key] = value
		}
	}
}

type connectionAdapter struct {
	core.MediaProvider
	credentials Credentials
	definition  providerkit.Definition
}

func (a *connectionAdapter) manual(id string) (providerkit.ManualModel, bool) {
	for _, m := range a.connection().Models {
		if m.Model.ID == id {
			return m, true
		}
	}
	return providerkit.ManualModel{}, false
}
func (a *connectionAdapter) connection() *providerkit.Connection {
	if a.credentials.Connection != nil {
		return a.credentials.Connection
	}
	return &providerkit.Connection{}
}

type redactedProviderError struct {
	message string
	cause   error
}

func (e redactedProviderError) Error() string { return e.message }

// Preserve domain classification without exposing an unredacted error through Unwrap.
func (e redactedProviderError) Is(target error) bool { return errors.Is(e.cause, target) }

func (a *connectionAdapter) redactText(message string) string {
	secrets := []string{a.credentials.APIKey, a.credentials.MusicAPIKey, a.credentials.BillingAccessKeySecret}
	for _, field := range a.definition.Fields {
		if field.Secret {
			secrets = append(secrets, a.credentials.Get(field.Key))
		}
	}
	for _, h := range a.connection().Headers {
		if h.Value != nil {
			secrets = append(secrets, *h.Value)
		}
	}
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return message
}

func (a *connectionAdapter) redact(err error) error {
	if err == nil {
		return nil
	}
	message := a.redactText(err.Error())
	if message == err.Error() {
		return err
	}
	return redactedProviderError{message: message, cause: err}
}
func (a *connectionAdapter) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	result := []core.MediaModel{}
	var err error
	if !a.connection().ManualCatalog {
		result, err = listAdapterModels(ctx, a.MediaProvider)
	}
	if err != nil && len(a.connection().Models) == 0 {
		return nil, a.redact(err)
	}
	index := map[string]int{}
	for i, m := range result {
		index[m.ID] = i
	}
	syntax := map[string]string{}
	if a.definition.Profiles != nil {
		for _, profile := range a.definition.Profiles() {
			syntax[profile.ID] = profile.ReferencePromptSyntax
		}
	}
	for _, manual := range a.connection().Models {
		model := manual.Model
		// Existing saved profiles predate this UI-only hint. Inherit it at read
		// time, preserving credentials, request contracts and pinned identities.
		if model.ReferencePromptSyntax == "" {
			model.ReferencePromptSyntax = syntax[manual.Profile]
		}
		if i, ok := index[model.ID]; ok {
			result[i] = model
		} else {
			result = append(result, model)
		}
	}
	return result, nil
}
func (a *connectionAdapter) DiscoverModelIDs(ctx context.Context) ([]string, error) {
	if a.connection().ManualCatalog {
		return []string{}, nil
	}
	if d, ok := a.MediaProvider.(interface {
		DiscoverModelIDs(context.Context) ([]string, error)
	}); ok {
		return d.DiscoverModelIDs(ctx)
	}
	return []string{}, nil
}
func (a *connectionAdapter) ReviewedCatalogModels() []core.MediaModel {
	if a.connection().ManualCatalog {
		return nil
	}
	if native, ok := a.MediaProvider.(interface{ ReviewedCatalogModels() []core.MediaModel }); ok {
		var models []core.MediaModel
		for _, model := range native.ReviewedCatalogModels() {
			if _, manual := a.manual(model.ID); !manual {
				models = append(models, model)
			}
		}
		return models
	}
	return nil
}
func (a *connectionAdapter) CatalogExclusions() []catalogsync.Exclusion {
	if a.connection().ManualCatalog {
		return nil
	}
	if native, ok := a.MediaProvider.(interface {
		CatalogExclusions() []catalogsync.Exclusion
	}); ok {
		var excluded []catalogsync.Exclusion
		for _, entry := range native.CatalogExclusions() {
			if _, manual := a.manual(entry.ID); !manual {
				excluded = append(excluded, entry)
			}
		}
		return excluded
	}
	return nil
}
func (a *connectionAdapter) CheckCredentials(ctx context.Context) error {
	if a.connection().ManualCatalog {
		return errors.New("catalog discovery is disabled; use an explicit model generation to verify access")
	}
	return a.redact(validateAdapter(ctx, a.MediaProvider))
}
func (a *connectionAdapter) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	if manual, ok := a.manual(r.Model); ok {
		return core.ValidateReferenceMode(manual.Model, r.Options)
	}
	if v, ok := a.MediaProvider.(core.RequestValidator); ok {
		return a.redact(v.ValidateRequest(ctx, r))
	}
	return nil
}
func (a *connectionAdapter) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if manual, ok := a.manual(r.Model); ok {
		// The profile selects a proven serializer/parser; only the outgoing model ID is replaced.
		body := map[string]any{}
		mergeBody(body, manual.ExtraBody)
		if len(r.Options.Parameters) > 0 {
			if a.definition.ID == "alibaba" {
				parameters := map[string]any{}
				if existing, ok := body["parameters"].(map[string]any); ok {
					mergeBody(parameters, existing)
				}
				mergeBody(parameters, r.Options.Parameters)
				body["parameters"] = parameters
			} else {
				for k, v := range r.Options.Parameters {
					body[k] = v
				}
			}
		}
		ctx = context.WithValue(core.WithManualCall(ctx), manualRequestKey{}, manualRequest{model: r.Model, body: body})
		r.Model = manual.Profile
	}
	_, manualCall := ctx.Value(manualRequestKey{}).(manualRequest)
	task, err := a.MediaProvider.Generate(ctx, r)
	if manualCall && task.Usage == nil && err == nil {
		task.Usage = &core.ProviderUsage{}
	}
	return task, a.redact(err)
}
func (a *connectionAdapter) GetTask(ctx context.Context, id string) (core.ProviderResult, error) {
	result, err := a.MediaProvider.GetTask(ctx, id)
	result.Error = a.redactText(result.Error)
	return result, a.redact(err)
}
func (a *connectionAdapter) PreferInlineImages() bool {
	p, ok := a.MediaProvider.(interface{ PreferInlineImages() bool })
	return ok && p.PreferInlineImages()
}
func (a *connectionAdapter) IsFree() bool        { return a.definition.Free }
func (a *connectionAdapter) FailureIsFree() bool { return a.definition.FailureNoCharge }
func (a *connectionAdapter) Quote(ctx context.Context, m core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	if manual, ok := a.manual(r.Model); ok {
		if manual.PriceUSD == nil {
			return core.TaskCost{}, "", core.ErrPricingUnavailable
		}
		amount := money.USD(*manual.PriceUSD*1e6 + 0.5)
		now := time.Now().UTC()
		return core.TaskCost{ExpectedUSD: amount, EstimatedUnits: 1, Status: "estimated", QuotedAt: now, Pricing: core.PricingSnapshot{Currency: "USD", Unit: "request", Price: amount, Per: 1, Source: "Administrator configured price per request", Version: "manual-1", CheckedAt: now}}, "", nil
	}
	if len(a.connection().ExtraBody) > 0 || a.connection().Endpoints["generate"] != "" {
		return core.TaskCost{}, "", core.ErrPricingUnavailable
	}
	if quoter, ok := a.MediaProvider.(core.CostQuoter); ok {
		return quoter.Quote(ctx, m, r)
	}
	if a.definition.Quote != nil {
		cost, err := a.definition.Quote(a.credentials.BaseURL, m, r, time.Now().UTC())
		return cost, "", err
	}
	return core.TaskCost{}, "", fmt.Errorf("%w", core.ErrPricingUnavailable)
}
