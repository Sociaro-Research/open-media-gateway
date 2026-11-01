package kling

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

// These are explicitly confirmed account-specific tariffs, not inferred public
// prices. Unknown upstream subcall tariffs remain unavailable when left blank.
func (a *Adapter) setOperationPrices(c providerkit.Credentials) error {
	if a.operationPrices == nil {
		a.operationPrices = map[string]money.USD{}
	}
	for _, key := range []string{"selection_init", "selection_add", "selection_delete", "selection_clear", "selection_preview", "element_multi_image", "element_video_character", "avatar_std", "avatar_pro", "lip_sync", "multi_edit_std", "multi_edit_pro"} {
		raw := strings.TrimSpace(c.Get(key + "_price"))
		if raw == "" {
			continue
		}
		price, err := money.Parse(raw)
		if err != nil || price <= 0 {
			return fmt.Errorf("Kling %s_price must be a confirmed positive USD/request tariff", key)
		}
		a.operationPrices[key] = price
	}
	return nil
}
func (a *Adapter) quoteFixedSessionOperation(id string) (core.TaskCost, string, error) {
	key := strings.TrimPrefix(id, "kling-")
	key = strings.ReplaceAll(key, "-", "_")
	return a.quoteFixedOperation(key)
}
func (a *Adapter) quoteFixedOperation(key string) (core.TaskCost, string, error) {
	price, ok := a.operationPrices[key]
	if !ok || price <= 0 {
		return core.TaskCost{}, "", core.ErrPricingUnavailable
	}
	now := time.Now().UTC()
	return core.TaskCost{ExpectedUSD: price, EstimatedUnits: 1, Status: "estimated", QuotedAt: now, Pricing: core.PricingSnapshot{Currency: "USD", Unit: "request", Price: price, Per: 1, Source: "Operator-confirmed account tariff", AccountSpecific: true, CheckedAt: now, EstimateDescription: "Explicit operator-confirmed fixed USD tariff for one operation. The public API documentation does not publish a numeric rate for this subcall; actual provider deductions remain unverified."}}, "", nil
}
