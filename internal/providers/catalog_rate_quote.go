package providers

import (
	"fmt"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// The optional pure hook verifies a reviewed tariff whose executable workflow
// requires an owned resource. It receives no context, task ID or credentials and
// must never query a user's task or substitute for generation admission.
type catalogRateQuoteProvider interface {
	CatalogRateQuote(core.MediaModel, catalogsync.Rate) (core.TaskCost, bool, error)
}

func (a *connectionAdapter) CatalogRateQuote(model core.MediaModel, rate catalogsync.Rate) (core.TaskCost, bool, error) {
	hook, ok := a.MediaProvider.(catalogRateQuoteProvider)
	if !ok {
		return core.TaskCost{}, false, nil
	}
	cost, handled, err := hook.CatalogRateQuote(model, rate)
	return cost, handled, a.redact(err)
}
func validatePureCatalogRateQuote(base core.TaskCost, rate catalogsync.Rate) error {
	if !base.QuantityUnknown || base.ExpectedUSD != 0 || base.EstimatedUnits != 0 || base.ActualUSD != nil || base.MeteredUSD != nil || base.Pricing.AccountSpecific || !core.HasVerifiedQuantityUnknownTariff(base) {
		return fmt.Errorf("pure catalogue quote requires a verified public tariff with unknown quantity")
	}
	for _, component := range base.Pricing.Components {
		if component.EstimatedUnits != 0 {
			return fmt.Errorf("pure catalogue quote cannot fabricate usage quantities")
		}
	}
	// Reprice validates exactly the existing unit, region, component metrics,
	// divisors and duration formula. A newly fetched price can change; billing
	// contracts, quantities and account-specific tariffs cannot be invented here.
	repriced, err := catalogsync.Reprice(base, rate)
	if err != nil {
		return err
	}
	if !core.HasVerifiedQuantityUnknownTariff(repriced) {
		return core.ErrPricingUnavailable
	}
	return nil
}
