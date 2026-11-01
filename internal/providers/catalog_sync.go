package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
)

func (m *Manager) SetCatalog(repo catalogsync.Repository) { m.catalog = repo }
func catalogContext(ctx context.Context) context.Context {
	return identity.WithResourceOwners(identity.WithOwnerID(ctx, identity.SharedOwnerID), identity.SharedOwnerID, identity.SharedOwnerID)
}
func (m *Manager) EnsureCatalogSchedules(ctx context.Context) error {
	if m.catalog == nil {
		return nil
	}
	ctx = catalogContext(ctx)
	available, err := m.Providers(ctx)
	if err != nil {
		return err
	}
	for id := range available {
		d, ok := m.findSpec(id)
		if !ok || d.Free {
			continue
		}
		record, err := m.loadRecord(ctx, id)
		if err != nil {
			return err
		}
		if err = m.catalog.EnsureSchedule(ctx, catalogsync.Schedule{Provider: id, Enabled: record.Enabled && !record.Archived, Time: "03:00", Version: 1, Sources: append([]catalogsync.Source{}, d.CatalogSources...)}); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) CaptureCatalog(ctx context.Context, id string) (catalogsync.Capture, error) {
	ctx = catalogContext(ctx)
	adapter, err := m.resolve(ctx, id, true)
	if err != nil {
		return catalogsync.Capture{}, err
	}
	models, err := listAdapterModels(ctx, adapter)
	if err != nil {
		return catalogsync.Capture{}, err
	}
	d, _ := m.findSpec(id)
	capture := catalogsync.Capture{Models: models, Inactive: modelcatalog.Inactive(d.ID, time.Now()), IDs: []string{}}
	capture.DocumentReviews = d.CatalogReviews
	capture.VerificationSources = d.CatalogVerificationSources
	if reviewed, ok := adapter.(interface {
		CatalogExclusions() []catalogsync.Exclusion
	}); ok {
		capture.Excluded = reviewed.CatalogExclusions()
	}
	if reviewed, ok := adapter.(interface{ ReviewedCatalogModels() []core.MediaModel }); ok {
		capture.Reviewed = reviewed.ReviewedCatalogModels()
		for i := range capture.Reviewed {
			capture.Reviewed[i] = modelcatalog.Enrich(d.ID, capture.Reviewed[i])
		}
	}
	for i := range capture.Models {
		capture.Models[i] = modelcatalog.Enrich(d.ID, capture.Models[i])
	}
	capture.TariffAliases = map[string]string{}
	manual := map[string]bool{}
	for _, model := range capture.Models {
		if model.Manual {
			manual[model.ID] = true
		}
	}
	for _, model := range append(append([]core.MediaModel{}, capture.Models...), capture.Reviewed...) {
		if !model.Manual && !manual[model.ID] && d.CatalogAliases[model.ID] != "" {
			capture.TariffAliases[model.ID] = d.CatalogAliases[model.ID]
		}
	}
	if discovery, ok := adapter.(interface {
		DiscoverModelIDs(context.Context) ([]string, error)
	}); ok {
		capture.IDs, err = discovery.DiscoverModelIDs(ctx)
	}
	return capture, err
}

// InterpretCatalogSource is a pure plugin mapping over already fetched public
// source text. It does not resolve credentials, call providers or alter models.
func (m *Manager) InterpretCatalogSource(_ context.Context, id string, source catalogsync.Source, text string) ([]catalogsync.Entry, error) {
	definition, ok := m.findSpec(id)
	if !ok {
		return nil, nil
	}
	var entries []catalogsync.Entry
	var err error
	if definition.CatalogInterpret != nil {
		entries, err = definition.CatalogInterpret(source, text)
		if err != nil {
			return nil, err
		}
	}
	for model, hint := range modelcatalog.ParsedReferenceRecommendations(id, source.URL, text) {
		index := -1
		for i := range entries {
			if entries[i].ID == model {
				index = i
				break
			}
		}
		if index < 0 {
			entries = append(entries, catalogsync.Entry{ID: model})
			index = len(entries) - 1
		}
		if entries[index].OperationalFields == nil {
			entries[index].OperationalFields = map[string]json.RawMessage{}
		}
		entries[index].OperationalFields["reference_prompt_recommendation"], _ = json.Marshal(hint)
	}
	return entries, nil
}
func (m *Manager) CheckCatalogRate(ctx context.Context, id string, model core.MediaModel, rate catalogsync.Rate) error {
	if err := rate.ValidateConditions(); err != nil {
		return err
	}
	ctx = catalogContext(ctx)
	adapter, err := m.resolve(ctx, id, true)
	if err != nil {
		return err
	}
	r, _ := referenceRequest(model)
	typedRequest := false
	if hook, ok := adapter.(catalogRateRequestProvider); ok {
		exact, e := hook.CatalogRateRequest(model, rate)
		if e == nil {
			if exact.Model != model.ID {
				return fmt.Errorf("typed catalogue request targets a different model")
			}
			r = exact
			typedRequest = true
		} else if !errors.Is(e, errCatalogRateRequestUnavailable) {
			return fmt.Errorf("typed catalogue request rejected: %w", e)
		}
	}
	if !typedRequest {
		if rate.Duration < 0 {
			return fmt.Errorf("unsupported duration selector")
		}
		if rate.Duration > 0 {
			if model.MediaType != "video" || model.DurationFromInput || model.MaxDuration <= 0 || rate.Duration < model.MinDuration || rate.Duration > model.MaxDuration || model.DurationStep > 0 && (rate.Duration-model.MinDuration)%model.DurationStep != 0 {
				return fmt.Errorf("unsupported output duration")
			}
			r.Options.Duration = rate.Duration
		}

		if rate.Resolution != "" {
			r.Options.Resolution = rate.Resolution
		}
		if rate.Quality != "" {
			r.Options.Quality = rate.Quality
		}
		if rate.Audio != nil {
			if *rate.Audio && !model.SupportsAudio || !*rate.Audio && model.AudioAlwaysOn {
				return fmt.Errorf("unsupported generated audio selector")
			}
			r.Options.GenerateAudio = rate.Audio
		}
		if rate.VideoInput != nil && *rate.VideoInput {
			if !model.SupportsVideoInput {
				return fmt.Errorf("unsupported video-reference selector")
			}
			r.Options.VideoURL = "https://example.invalid/catalogue-reference.mp4"
			if model.ReferenceMediaLimits != nil && model.ReferenceMediaLimits.Video.MinDuration > 0 {
				r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.VideoURL: {Type: "video", Duration: model.ReferenceMediaLimits.Video.MinDuration}}
			}
		}
	}
	if model.Pricing.USDPerMillionAudioTokens > 0 && rate.Audio == nil {
		return fmt.Errorf("generated audio requires an explicit tariff selector")
	}
	if !rate.MatchesConditions(model, r) {
		return fmt.Errorf("typed quote request does not match tariff selectors")
	}
	definition, _ := m.findSpec(id)
	if rate.InputImages != nil {
		reviewed := definition.ID == "bytedance" && model.ID == "dola-seedream-5-0-pro-260628" && rate.InputImages.Free == 1
		reviewed = reviewed || definition.ID == "alibaba" && (model.ID == "qwen-image-3.0" || model.ID == "qwen-image-3.0-pro") && rate.InputImages.Free == 0
		if !reviewed || rate.InputImages.Price < 0 {
			return fmt.Errorf("reference-image charge requires a reviewed estimator")
		}
	}
	if rate.Resolution != "" && !contains(model.Resolutions, rate.Resolution) {
		return fmt.Errorf("unsupported resolution")
	}
	if rate.Quality != "" && !contains(model.Qualities, rate.Quality) {
		return fmt.Errorf("unsupported quality")
	}
	p := &managedProvider{id: id, manager: m}
	base, err := p.referenceQuoter(p.connectionScope(ctx, adapter, ""), adapter)(model, r)
	// A reviewed model may exist only in another documented region. Its
	// regional tariff must not depend on a quote being available in this account.
	if err != nil && definition.Quote != nil && definition.CatalogRegions[rate.Region] != "" {
		base, err = definition.Quote(definition.CatalogRegions[rate.Region], model, r, time.Now().UTC())
	}
	if err != nil {
		return fmt.Errorf("no reviewed estimator for this configuration")
	}
	if base.Pricing.Region != rate.Region {
		d, _ := m.findSpec(id)
		endpoint := d.CatalogRegions[rate.Region]
		if endpoint == "" || d.Quote == nil {
			return fmt.Errorf("no reviewed estimator for this region")
		}
		base, err = d.Quote(endpoint, model, r, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("no reviewed estimator for this regional configuration")
		}
	}
	_, err = catalogsync.Reprice(base, rate)
	return err
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (p *managedProvider) catalogState(ctx context.Context) (catalogsync.State, error) {
	if p.manager.catalog == nil || identity.ProviderOwnerID(ctx) != identity.SharedOwnerID {
		return catalogsync.State{}, nil
	}
	return p.manager.catalog.State(ctx, p.id)
}
func applyCatalogModel(model core.MediaModel, state catalogsync.State) core.MediaModel {
	if model.Manual {
		return model
	}
	entry, ok := state.Models[model.ID]
	if !ok || entry.Metadata == nil {
		return model
	}
	metadata := *entry.Metadata
	if metadata.Operational != nil {
		detached := map[string]core.OperationalFact{}
		for key, fact := range metadata.Operational {
			fact.Value = append([]byte(nil), fact.Value...)
			detached[key] = fact
		}
		metadata.Operational = detached
	}
	if model.Metadata != nil {
		metadata.APICreated = model.Metadata.APICreated
		metadata.OwnedBy = model.Metadata.OwnedBy
	}
	if fact, ok := metadata.Operational["reference_prompt_recommendation"]; ok {
		var hint core.ReferencePromptRecommendation
		if json.Unmarshal(fact.Value, &hint) == nil && model.ReferencePromptRecommendation.AcceptsObservation(hint) && fact.Source.URL == hint.SourceURL {
			model.ReferencePromptRecommendation = hint.Clone()
		}
	}
	model.Metadata = &metadata
	return model
}
func applyCatalogQuote(base core.TaskCost, err error, model core.MediaModel, request core.MediaRequest, state catalogsync.State) (core.TaskCost, error) {
	if err != nil || model.Manual || base.Pricing.AccountSpecific {
		return base, err
	}
	// Input-reference and fixed surcharges cannot inherit a no-reference tariff.
	reviewedAudioRefs := false
	switch model.ID {
	case "dreamina-seedance-2-0-260128", "dreamina-seedance-2-0-fast-260128", "dreamina-seedance-2-0-mini-260615", "dreamina-seedance-2-5-260628":
		reviewedAudioRefs = true
	}
	entry, ok := state.Models[model.ID]
	if len(request.Options.Audios()) > 0 && !reviewedAudioRefs {
		explicit := false
		for _, rate := range entry.Rates {
			if rate.AudioInput != nil && *rate.AudioInput && rate.MatchesConditions(model, request) {
				explicit = true
				break
			}
		}
		if !explicit {
			return base, nil
		}
	}
	if !ok {
		return base, nil
	}
	var selected *catalogsync.Rate
	for i := range entry.Rates {
		r := &entry.Rates[i]
		duration := request.Options.Duration
		if duration == 0 {
			duration = model.DefaultDuration
		}
		if r.Duration < 0 || r.Duration > 0 && r.Duration != duration {
			continue
		}

		if !r.MatchesConditions(model, request) {
			continue
		}
		if r.Region != base.Pricing.Region || r.Unit != base.Pricing.Unit || r.Per != base.Pricing.Per {
			continue
		}
		resolution := request.Options.Resolution
		if resolution == "" && len(model.Resolutions) > 0 {
			resolution = model.Resolutions[0]
		}
		if r.Resolution != "" && r.Resolution != resolution {
			continue
		}
		if r.Quality != "" && r.Quality != request.Options.Quality {
			continue
		}
		hasVideo := len(request.Options.Videos()) > 0
		if r.VideoInputIncludesResources {
			for _, resource := range request.Options.ResourceSnapshots {
				if resource.Kind == "element" && resource.Subtype == "video_character" {
					hasVideo = true
					break
				}
			}
		}
		if r.VideoInput == nil && hasVideo || r.VideoInput != nil && *r.VideoInput != hasVideo {
			continue
		}
		audio := model.AudioAlwaysOn || request.Options.GenerateAudio != nil && *request.Options.GenerateAudio
		if r.Audio != nil && *r.Audio != audio {
			continue
		}
		// Legacy scalar tariffs must never override an audio-dependent contract.
		if model.SupportsAudio && model.Pricing.USDPerMillionAudioTokens > 0 && r.Audio == nil {
			continue
		}
		if base.Pricing.FixedUSD != 0 && r.InputImages == nil {
			continue
		}
		specificity := func(r *catalogsync.Rate) int {
			n := len(r.Resolution) + len(r.Quality) + r.ConditionSpecificity()
			if r.Audio != nil {
				n++
			}
			if r.VideoInput != nil {
				n++
			}
			return n
		}
		if selected == nil || r.Duration > 0 && selected.Duration == 0 || r.Duration == selected.Duration && specificity(r) > specificity(selected) {
			selected = r
		}
	}
	if selected == nil {
		return base, nil
	}
	if selected.InputImages != nil {
		base.Pricing.FixedUSD, err = selected.InputImages.Price.Multiply(float64(max(0, len(request.Options.ImageURLs)-selected.InputImages.Free)), 1)
		if err != nil {
			return core.TaskCost{}, err
		}
	}
	base, err = catalogsync.Reprice(base, *selected)
	if err != nil {
		return core.TaskCost{}, err
	}
	base.Pricing.Version = fmt.Sprintf("catalog-%d/%s", state.Version, base.Pricing.Version)
	if selected.Source != "" {
		base.Pricing.Source = selected.Source
	}
	base.Pricing.CheckedAt = time.Now().UTC()
	return base, nil
}

// ResolveCatalogSource maps only explicitly reviewed public aliases; custom schedules remain intact.
func (m *Manager) ResolveCatalogSource(_ context.Context, id string, source catalogsync.Source) catalogsync.Source {
	if definition, ok := m.findSpec(id); ok && source.Parser == "document" {
		if url := definition.CatalogSourceAliases[source.URL]; url != "" {
			source.URL = url
		}
	}
	return source
}
