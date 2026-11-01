package core

import (
	"bytes"
	"encoding/json"
	"strings"
)

// CustomerJSON is the public financial projection, shared by HTTP and webhooks.
// It never mutates its input or persisted provider accounting.
func CustomerJSON(value any) ([]byte, error) { return customerJSON(value, false) }

// SalesJSON adds only scoped commercial metrics to the ordinary customer projection.
func SalesJSON(value any) ([]byte, error) { return customerJSON(value, true) }
func customerJSON(value any, profits bool) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var view any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err = decoder.Decode(&view); err != nil {
		return nil, err
	}
	customerPrices(view, profits)
	return json.Marshal(view)
}

func customerPrices(value any, profits ...bool) {
	keepProfit := len(profits) > 0 && profits[0]
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			customerPrices(item, keepProfit)
		}
	case map[string]any:
		if metadata, ok := v["metadata"].(map[string]any); ok {
			delete(metadata, "operational") // Imported model evidence retains administrator-only provenance.
		}
		if !keepProfit {
			for _, key := range []string{"profit_usd", "profit_status", "profit_recognized_at", "recognized_profit_requests", "pending_profit_requests"} {
				delete(v, key)
			}
		}
		// Pending deliveries created before customer fields existed still use
		// their recorded markup (zero for historical tasks).
		if _, ok := v["expected_usd"]; ok && v["pricing"] != nil && v["customer_expected_usd"] == nil {
			data, _ := json.Marshal(v)
			var c TaskCost
			var updated map[string]any
			if json.Unmarshal(data, &c) == nil {
				if b, e := json.Marshal(c); e == nil {
					d := json.NewDecoder(bytes.NewReader(b))
					d.UseNumber()
					_ = d.Decode(&updated)
				}
			}
			if updated == nil {
				// Invalid historical billing must not fall back to raw prices.
				for key := range v {
					delete(v, key)
				}
				return
			}
			for key, val := range updated {
				v[key] = val
			}
		}
		// TaskCost's customer amounts are computed from its immutable accepted rate.
		if expected, ok := v["customer_expected_usd"]; ok {
			safe := map[string]any{"expected_usd": expected}
			for _, key := range []string{"quoted_at"} {
				if x, ok := v[key]; ok {
					safe[key] = x
				}
			}
			if x, ok := v["customer_metered_usd"]; ok {
				safe["metered_usd"] = x
			}
			if x, ok := v["customer_tariff_usd"]; ok {
				safe["tariff_usd"] = x
			}
			for key := range v {
				delete(v, key)
			}
			for key, x := range safe {
				v[key] = x
			}
		}
		if _, ok := v["reference_price_usd"]; ok {
			for key := range v {
				switch key {
				case "known", "reference_price_usd", "reference_description", "reference_quote":
				default:
					delete(v, key)
				}
			}
			if description, ok := v["reference_description"].(string); ok {
				description = strings.Split(description, " Provider standard list-rate estimate;")[0]
				if strings.Contains(description, "Published rates") {
					description = "A verified estimate is unavailable for this configuration."
				}
				v["reference_description"] = description + " Gateway price. Final cost depends on actual usage."
			}
		}
		// MonthlyUsage: keep the already marked-up consumption, never raw aggregates.
		if _, ok := v["consumed_usd"]; ok {
			if _, monthly := v["month"]; monthly {
				for _, key := range []string{"actual_usd", "expected_usd", "estimated_cost_usd", "package_covered_usd", "unconfirmed_tasks"} {
					delete(v, key)
				}
			}
		}
		if _, ok := v["sources"]; ok {
			delete(v, "sources")
		}
		// Unstructured provider payloads can contain arbitrary billing data.
		for _, key := range []string{"raw", "estimated_cost", "cost_source", "markup_percent", "markup_basis_points", "effective_markup_percent"} {
			delete(v, key)
		}
		if facts, ok := v["facts"].([]any); ok {
			safe := []any{}
			for _, item := range facts {
				f, ok := item.(map[string]any)
				if !ok {
					continue
				}
				label, _ := f["label"].(string)
				// Only explicitly nonfinancial facts are public. New imported facts stay private.
				switch label {
				case "Documented equivalent ID", "Provider: Context window", "Provider: Editing", "Provider: Input modalities", "Provider: Knowledge cutoff", "Provider: Max output", "Provider: Max outputs", "Provider: Max resolution", "Provider: Output modalities", "Provider: Supported endpoints", "Provider: Supported features", "Provider: Text-to-image", "Provider: output specifications / limits", "Provider: reference workflows":
					safe = append(safe, item)
				}
			}
			v["facts"] = safe
			delete(v, "sources") // Imported sources include provider tariff and promotion pages.
		}
		for _, item := range v {
			customerPrices(item, keepProfit)
		}
	}
}
