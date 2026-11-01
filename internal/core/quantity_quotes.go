package core

import "encoding/json"

// A published rate remains useful even when the provider determines quantity.
// Unknown quantity never serializes as a zero reference price or a free quote.
func (p MediaModelPricing) MarshalJSON() ([]byte, error) {
	type plain MediaModelPricing
	raw, err := json.Marshal(plain(p))
	if err != nil {
		return nil, err
	}
	if p.ReferenceQuote == nil || !p.ReferenceQuote.QuantityUnknown {
		return raw, nil
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	delete(fields, "reference_price_usd")
	fields["quantity_unknown"] = json.RawMessage("true")
	return json.Marshal(fields)
}

// HasVerifiedQuantityUnknownTariff distinguishes missing quantity from missing
// price. Only a complete positive USD tariff may admit work without a total.
func HasVerifiedQuantityUnknownTariff(cost TaskCost) bool {
	p := cost.Pricing
	if !cost.QuantityUnknown {
		return true
	}
	if p.Currency != "USD" || p.Unit == "" || p.Per <= 0 || p.Source == "" {
		return false
	}
	if len(p.Components) == 0 {
		return p.Price > 0
	}
	positive := false
	for _, component := range p.Components {
		if component.Metric == "" || component.Per <= 0 || component.Price < 0 {
			return false
		}
		if component.Price > 0 {
			positive = true
		}
	}
	return positive
}
