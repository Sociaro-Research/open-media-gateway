package providers

import (
	"context"
	"fmt"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/modelcatalog"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"strings"
	"time"
)

type referenceQuoter func(core.MediaModel, core.MediaRequest) (core.TaskCost, error)

// Resolve credentials once per catalogue, rather than once per model.
func (p *managedProvider) referenceQuoter(ctx context.Context, adapter core.MediaProvider) referenceQuoter {
	if quoter, ok := adapter.(core.CostQuoter); ok {
		return func(m core.MediaModel, r core.MediaRequest) (core.TaskCost, error) {
			c, _, err := quoter.Quote(ctx, m, r)
			return c, err
		}
	}
	unavailable := func(core.MediaModel, core.MediaRequest) (core.TaskCost, error) {
		return core.TaskCost{}, core.ErrPricingUnavailable
	}
	record, err := p.manager.loadRecord(ctx, p.id)
	if err != nil {
		return unavailable
	}
	credentials, err := p.manager.decryptCredentials(record.CredentialsCiphertext)
	if err != nil {
		return unavailable
	}
	credentials = p.manager.withDefaults(p.id, credentials)
	definition, _ := p.manager.findSpec(p.id)
	if definition.Quote == nil {
		return unavailable
	}
	return func(m core.MediaModel, r core.MediaRequest) (core.TaskCost, error) {
		return definition.Quote(credentials.BaseURL, m, r, time.Now().UTC())
	}
}

// The list uses the same local quote implementation as the estimate endpoint.
// It never sends a generation request. Unknown configurations stay unpriced.
func (p *managedProvider) modelReference(model core.MediaModel, quote referenceQuoter) core.MediaModel {
	if !model.Manual {
		d, _ := p.manager.findSpec(p.id)
		model = modelcatalog.Enrich(d.ID, model)
	}
	if p.manager.isFree(p.id) {
		return model
	}
	request, description := referenceRequest(model)
	cost, err := quote(model, request)
	model.Pricing.ReferenceQuote = nil
	model.Pricing.Known = err == nil
	model.Pricing.ReferencePriceUSD = 0
	if err != nil {
		model.Pricing.ReferenceDescription = "A verified estimate is unavailable for this model, region or input configuration. Published rates, when available, are listed below."
		return model
	}
	model.Pricing.ReferencePriceUSD = float64(cost.ExpectedUSD) / float64(money.Scale)
	model.Pricing.ReferenceQuote = &cost
	if model.Manual {
		model.Pricing.ReferenceDescription = "Administrator configured price per request; excludes organization markup."
		return model
	}
	model.Pricing.ReferenceDescription = description + " Provider standard list-rate estimate; excludes organization markup, taxes, account discounts and temporary promotions. Final cost depends on actual usage."
	if cost.Pricing.EstimateDescription != "" {
		model.Pricing.ReferenceDescription = description + " " + cost.Pricing.EstimateDescription + " Excludes organization markup and taxes."
	}
	return model
}

func referenceRequest(m core.MediaModel) (core.MediaRequest, string) {
	r := core.MediaRequest{Model: m.ID, Prompt: strings.Repeat("a", 100)}
	if m.NoPrompt {
		r.Prompt = ""
	}
	first := func(values []string) string {
		if len(values) > 0 {
			return values[0]
		}
		return ""
	}
	r.Options.Resolution = first(m.Resolutions)
	r.Options.Quality = first(m.Qualities)
	r.Options.Format = first(m.Formats)
	details := []string{"100 ASCII prompt characters"}
	if m.MediaType == "video" {
		r.Options.Ratio = first(m.Ratios)
		for _, ratio := range m.Ratios {
			if ratio == "16:9" {
				r.Options.Ratio = ratio
			}
		}
		r.Options.Duration = m.DefaultDuration
		if r.Options.Duration < m.MinDuration {
			r.Options.Duration = m.MinDuration
		}
		audio := m.AudioAlwaysOn
		r.Options.GenerateAudio = &audio
		details = append(details, fmt.Sprintf("%d seconds, %s, audio %t", r.Options.Duration, r.Options.Ratio, audio))
	}
	if m.MediaType == "speech" {
		r.Prompt = strings.Repeat("a", 1000)
		r.Options.Voice = first(m.Voices)
		details = []string{"1,000 ASCII input characters"}
	}
	if m.RequiresImageInput {
		count := max(1, m.MinImageInputs)
		for i := 0; i < count; i++ {
			r.Options.ImageURLs = append(r.Options.ImageURLs, "https://example.invalid/reference.png")
		}
		details = append(details, fmt.Sprintf("%d input image(s)", count))
	} else {
		details = append(details, "no input images")
	}
	if m.RequiresVideoInput {
		r.Options.VideoURL = "https://example.invalid/reference.mp4"
		details = append(details, "video reference required; duration unknown")
	}
	if m.RequiresAudioInput {
		r.Options.AudioURL = "https://example.invalid/reference.wav"
		details = append(details, "audio reference required; duration unknown")
	}
	if r.Options.Resolution != "" {
		details = append(details, r.Options.Resolution)
	}
	if r.Options.Quality != "" {
		details = append(details, r.Options.Quality+" quality")
	}
	return r, strings.Join(details, ", ") + "."
}
