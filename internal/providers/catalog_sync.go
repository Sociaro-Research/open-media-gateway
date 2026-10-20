package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/catalogsync"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/modelcatalog"
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
func (m *Manager) CheckCatalogRate(ctx context.Context, id string, model core.MediaModel, rate catalogsync.Rate) error {
	ctx = catalogContext(ctx)
	adapter, err := m.resolve(ctx, id, true)
	if err != nil {
		return err
	}
	r, _ := referenceRequest(model)
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
	}
	if model.Pricing.USDPerMillionAudioTokens > 0 && rate.Audio == nil {
		return fmt.Errorf("generated audio requires an explicit tariff selector")
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
	base, err := p.referenceQuoter(ctx, adapter)(model, r)
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
	if model.Metadata != nil {
		metadata.APICreated = model.Metadata.APICreated
		metadata.OwnedBy = model.Metadata.OwnedBy
	}
	model.Metadata = &metadata
	return model
}
func applyCatalogQuote(base core.TaskCost, err error, model core.MediaModel, request core.MediaRequest, state catalogsync.State) (core.TaskCost, error) {
	if err != nil || model.Manual {
		return base, err
	}
	// Input-reference and fixed surcharges cannot inherit a no-reference tariff.
	reviewedAudioRefs := false
	switch model.ID {
	case "dreamina-seedance-2-0-260128", "dreamina-seedance-2-0-fast-260128", "dreamina-seedance-2-0-mini-260615", "dreamina-seedance-2-5-260628":
		reviewedAudioRefs = true
	}
	if len(request.Options.Audios()) > 0 && !reviewedAudioRefs {
		return base, nil
	}
	entry, ok := state.Models[model.ID]
	if !ok {
		return base, nil
	}
	var selected *catalogsync.Rate
	for i := range entry.Rates {
		r := &entry.Rates[i]
		if r.Region != base.Pricing.Region || r.Unit != base.Pricing.Unit || r.Per != base.Pricing.Per {
			continue
		}
		if r.Resolution != "" && r.Resolution != request.Options.Resolution {
			continue
		}
		if r.Quality != "" && r.Quality != request.Options.Quality {
			continue
		}
		hasVideo := len(request.Options.Videos()) > 0
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
			n := len(r.Resolution) + len(r.Quality)
			if r.Audio != nil {
				n++
			}
			if r.VideoInput != nil {
				n++
			}
			return n
		}
		if selected == nil || specificity(r) > specificity(selected) {
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
