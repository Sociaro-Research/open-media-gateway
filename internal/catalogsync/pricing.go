package catalogsync

import (
	"fmt"
	"math"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

// Reprice reuses reviewed quantity estimation. It cannot introduce new usage
// metrics, currency conversion, minimums or billing units.
func Reprice(base core.TaskCost, rate Rate) (core.TaskCost, error) {
	p := base.Pricing
	if p.Currency != "USD" || rate.Region != p.Region || rate.Unit != p.Unit || rate.Per != p.Per || rate.Per <= 0 || rate.Price < 0 || p.FixedUSD < 0 || p.FixedUSD != 0 && rate.InputImages == nil {
		return base, fmt.Errorf("tariff does not match the reviewed billing contract")
	}
	if rate.InputImages != nil && (p.Unit != "image" || p.Per != 1 || rate.InputImages.Free < 0 || rate.InputImages.Price < 0 || len(p.Components) > 0) {
		return base, fmt.Errorf("reference-image tariff requires the reviewed image billing contract")
	}
	if math.IsNaN(base.EstimatedUnits) || math.IsInf(base.EstimatedUnits, 0) || base.EstimatedUnits < 0 {
		return base, core.ErrPricingUnavailable
	}
	if len(p.Components) > 0 {
		if len(rate.Components) != len(p.Components) {
			return base, fmt.Errorf("all existing usage metrics must be specified")
		}
		components := append([]core.PricingComponent(nil), p.Components...)
		total := money.USD(0)
		seen := map[string]bool{}
		for i, c := range components {
			found := false
			for _, r := range rate.Components {
				if r.Metric == c.Metric && !seen[r.Metric] && r.Per == c.Per && r.Price >= 0 {
					seen[r.Metric] = true
					found = true
					components[i].Price = r.Price
					amount, e := r.Price.Multiply(c.EstimatedUnits, r.Per)
					if e != nil {
						return base, e
					}
					if total > money.USD(math.MaxInt64)-amount {
						return base, core.ErrPricingUnavailable
					}
					total += amount
					break
				}
			}
			if !found {
				return base, fmt.Errorf("new or missing usage metric")
			}
		}
		base.Pricing.Components = components
		base.ExpectedUSD = total
	} else {
		if len(rate.Components) > 0 {
			return base, fmt.Errorf("usage metrics require a new estimator")
		}
		amount, err := rate.Price.Multiply(base.EstimatedUnits, rate.Per)
		if err != nil {
			return base, err
		}
		base.Pricing.Price = rate.Price
		base.ExpectedUSD = amount
	}
	if base.ExpectedUSD > money.USD(math.MaxInt64)-p.FixedUSD {
		return base, core.ErrPricingUnavailable
	}
	base.ExpectedUSD += p.FixedUSD
	unchanged := p.Price == base.Pricing.Price
	for i, c := range p.Components {
		if c.Price != base.Pricing.Components[i].Price {
			unchanged = false
		}
	}
	if unchanged && p.EstimateDescription != "" {
		return base, nil
	}
	// An adapter's prose may contain amounts at the old tariff. Rebuild the
	// arithmetic after repricing rather than displaying those amounts as current.
	parts := []string{}
	for _, c := range base.Pricing.Components {
		parts = append(parts, fmt.Sprintf("%g planning %s × $%s / %d", c.EstimatedUnits, c.Metric, c.Price.String(), c.Per))
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%g planning %s × $%s / %d", base.EstimatedUnits, p.Unit, base.Pricing.Price.String(), p.Per))
	}
	if p.FixedUSD != 0 {
		parts = append(parts, "$"+p.FixedUSD.String()+" fixed charge")
	}
	base.Pricing.EstimateDescription = strings.Join(parts, " + ") + " = $" + base.ExpectedUSD.String() + ". Uses reviewed adapter quantity estimates and the latest published tariff; planning quantities are not measured usage. Actual charges can differ."
	if p.EstimateDescription != "" {
		base.Pricing.EstimateDescription += " Adapter planning assumptions at its baseline tariff: " + p.EstimateDescription
	}
	return base, nil
}
