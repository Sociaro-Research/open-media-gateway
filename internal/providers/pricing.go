package providers

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"time"
)

func (p *managedProvider) Quote(ctx context.Context, model core.MediaModel, request core.MediaRequest) (core.TaskCost, string, error) {
	if p.manager.isFree(p.id) {
		if _, err := p.manager.resolve(ctx, p.id, true); err != nil {
			return core.TaskCost{}, "", err
		}
		return core.FreeCost(time.Now().UTC()), p.id, nil
	}
	record, err := p.manager.loadRecord(ctx, p.id)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	credentials, err := p.manager.decryptCredentials(record.CredentialsCiphertext)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	credentials = p.manager.withDefaults(p.id, credentials)
	d, _ := p.manager.findSpec(p.id)
	billingConfigured, _ := d.GroupState(credentials, "billing")
	fingerprint, err := p.manager.pin(ctx, p.id, credentials)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	adapter, err := p.manager.resolve(ctx, p.id, true, fingerprint)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	ctx = p.connectionScope(ctx, adapter, fingerprint)
	state, err := p.catalogState(ctx)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	if quoter, ok := adapter.(core.CostQuoter); ok {
		cost, _, err := quoter.Quote(ctx, model, request)
		cost, err = applyCatalogQuote(cost, err, model, request, state)
		cost.BillingRequired = d.Bills != nil && billingConfigured
		return cost, fingerprint, err
	}
	if d.Quote == nil {
		return core.TaskCost{}, "", core.ErrPricingUnavailable
	}
	cost, err := d.Quote(credentials.BaseURL, model, request, time.Now().UTC())
	cost, err = applyCatalogQuote(cost, err, model, request, state)
	cost.BillingRequired = d.Bills != nil && billingConfigured
	return cost, fingerprint, err
}
