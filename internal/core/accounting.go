package core

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"time"
)

var (
	ErrProviderChanged    = errors.New("provider credentials changed after the estimate; request a new generation")
	ErrPricingUnavailable = errors.New("a current price estimate is unavailable for this model and configuration")
	ErrBudgetExceeded     = errors.New("the estimated cost exceeds the organization balance or member weekly allowance")
	ErrEstimateChanged    = errors.New("the price estimate increased; request a new estimate before confirming")
	ErrKeyUnavailable     = errors.New("the API key is expired or revoked")
)

// PricingSnapshot is immutable once a task is accepted. The same tariff is used
// to price the provider's reported billable units, independently of later updates.
type PricingSnapshot struct {
	AccountSpecific     bool               `json:"account_specific,omitempty"` // Explicit account tariff; public catalogue rates must not replace it.
	EstimateDescription string             `json:"estimate_description,omitempty"`
	Components          []PricingComponent `json:"components,omitempty"`
	FixedUSD            money.USD          `json:"fixed_usd,omitempty"`
	Currency            string             `json:"currency"`
	Unit                string             `json:"unit"`
	Price               money.USD          `json:"price"`
	Per                 int64              `json:"per"`
	Source              string             `json:"source"`
	Version             string             `json:"version"`
	CheckedAt           time.Time          `json:"checked_at"`
	Region              string             `json:"region,omitempty"`
	// Nil preserves legacy metering. New duration-based contracts explicitly
	// distinguish charged input video from output-only continuation/editing.
	BillInputVideo      *bool  `json:"bill_input_video,omitempty"`
	DurationBillingRule string `json:"duration_billing_rule,omitempty"`
}

// PricingComponent prices one normalized usage metric. All components must be
// reported before a multi-component tariff can be metered.
type PricingComponent struct {
	EstimatedUnits float64   `json:"estimated_units,omitempty"`
	Metric         string    `json:"metric"`
	Price          money.USD `json:"price"`
	Per            int64     `json:"per"`
}

type TaskCost struct {
	QuantityUnknown   bool            `json:"quantity_unknown,omitempty"`
	BillingRequired   bool            `json:"billing_required,omitempty"`
	MarkupPercent     money.Percent   `json:"markup_percent,omitempty"`
	TariffUSD         *money.USD      `json:"tariff_usd,omitempty"`
	PackageUnits      *float64        `json:"package_units,omitempty"`
	BillingMatch      string          `json:"billing_match,omitempty"`
	BillingInstanceID string          `json:"billing_instance_id,omitempty"`
	SubmittedAt       *time.Time      `json:"submitted_at,omitempty"`
	UsageRecordedAt   *time.Time      `json:"usage_recorded_at,omitempty"`
	ExpectedUSD       money.USD       `json:"expected_usd"`
	MeteredUSD        *money.USD      `json:"metered_usd,omitempty"`
	ActualUSD         *money.USD      `json:"actual_usd,omitempty"`
	Status            string          `json:"status"`
	ActualSource      string          `json:"actual_source,omitempty"`
	InvoiceIDs        []string        `json:"invoice_ids,omitempty"`
	Pricing           PricingSnapshot `json:"pricing"`
	EstimatedUnits    float64         `json:"estimated_units"`
	BilledUnits       *float64        `json:"billed_units,omitempty"`
	QuotedAt          time.Time       `json:"quoted_at"`
	ReconciledAt      *time.Time      `json:"reconciled_at,omitempty"`
}

type CostEstimate struct {
	Cost           TaskCost   `json:"cost"`
	WeeklyLimitUSD *money.USD `json:"weekly_limit_usd"`
	RemainingUSD   *money.USD `json:"remaining_usd"`
	CanGenerate    bool       `json:"can_generate"`
}

// CostQuoter resolves owner-specific provider credentials and the applicable tariff.
type CostQuoter interface {
	Quote(context.Context, MediaModel, MediaRequest) (TaskCost, string, error)
}
type MarkupReader interface {
	ApplyMarkup(context.Context, *Task) error
}

type BudgetReader interface {
	Budget(context.Context, string, time.Time) (*money.USD, *money.USD, error)
}

func FreeCost(now time.Time) TaskCost {
	zero := money.USD(0)
	return TaskCost{ExpectedUSD: 0, ActualUSD: &zero, Status: "confirmed", ActualSource: "free_provider", QuotedAt: now,
		Pricing: PricingSnapshot{Currency: "USD", Unit: "request", Per: 1, Source: "Gateway free provider", Version: "1", CheckedAt: now}}
}

// MeterUsage uses the accepted tariff, never a refreshed catalogue. It is not an invoice.
func (c *TaskCost) MeterUsage(usage ProviderUsage) bool {
	if len(c.Pricing.Components) > 0 {
		amount := c.Pricing.FixedUSD
		for _, component := range c.Pricing.Components {
			found := false
			for _, metric := range usage.Metrics {
				if metric.Name != component.Metric {
					continue
				}
				part, err := component.Price.Multiply(metric.Quantity, component.Per)
				if err != nil {
					return false
				}
				amount += part
				found = true
				break
			}
			if !found {
				return false
			}
		}
		c.MeteredUSD = &amount
		if c.ActualUSD == nil {
			c.Status = "pending_billing"
		}
		return true
	}
	if c.Pricing.Unit == "request" {
		amount := c.Pricing.Price
		units := 1.0
		c.MeteredUSD = &amount
		c.BilledUnits = &units
		if c.ActualUSD == nil {
			c.Status = "pending_billing"
		}
		return true
	}
	names := map[string][]string{"token": {"completion_tokens", "total_tokens", "output_tokens"}, "image": {"image_count", "generated_images"}, "character": {"characters", "character_count"}, "second": {"output_video_duration", "video_duration", "duration", "audio_duration"}}
	quantity, found := 0.0, false
	for _, name := range names[c.Pricing.Unit] {
		for _, metric := range usage.Metrics {
			if metric.Name == name {
				quantity, found = metric.Quantity, true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return false
	}
	if c.Pricing.DurationBillingRule != "" {
		if c.Pricing.Unit != "second" || c.Pricing.DurationBillingRule != "output_plus_max_input_output" {
			return false
		}
		input, ok := 0.0, false
		for _, metric := range usage.Metrics {
			if metric.Name == "input_video_duration" {
				input, ok = metric.Quantity, true
			}
		}
		if !ok || input <= 0 {
			return false
		}
		quantity += max(input, quantity)
	} else if c.Pricing.Unit == "second" && (c.Pricing.BillInputVideo == nil || *c.Pricing.BillInputVideo) {
		inputFound := false
		for _, metric := range usage.Metrics {
			if metric.Name == "input_video_duration" {
				quantity += metric.Quantity
				inputFound = true
			}
		}
		if c.Pricing.BillInputVideo != nil && *c.Pricing.BillInputVideo && !inputFound {
			return false
		}
	}
	amount, err := c.Pricing.Price.Multiply(quantity, c.Pricing.Per)
	if err != nil {
		return false
	}
	amount += c.Pricing.FixedUSD
	c.MeteredUSD, c.BilledUnits = &amount, &quantity
	if c.ActualUSD == nil {
		c.Status = "pending_billing"
	}
	return true
}

// ConsumptionView retains the consumer's price without provider settlement details.
func (c *TaskCost) ConsumptionView() *TaskCost {
	if c == nil {
		return nil
	}
	copy := *c
	copy.ActualUSD = nil
	copy.PackageUnits = nil
	copy.InvoiceIDs = nil
	copy.BillingInstanceID = ""
	copy.BillingMatch = ""
	if copy.ActualSource != "free_provider" && copy.ActualSource != "provider_failed_no_charge" {
		copy.ActualSource = ""
	}
	return &copy
}

// CustomerExpected and the serialized customer prices include only the accepted
// markup. All original provider prices remain available for reconciliation.
func (c TaskCost) CustomerExpected() (money.USD, error) {
	if c.QuantityUnknown {
		return 0, ErrPricingUnavailable
	}
	return c.MarkupPercent.Apply(c.ExpectedUSD)
}
func (c TaskCost) MarshalJSON() ([]byte, error) {
	type plain TaskCost
	expected, err := c.MarkupPercent.Apply(c.ExpectedUSD)
	if err != nil {
		return nil, err
	}
	charged := func(base *money.USD) (*money.USD, error) {
		if base == nil {
			return nil, nil
		}
		v, e := c.MarkupPercent.Apply(*base)
		return &v, e
	}
	metered, err := charged(c.MeteredUSD)
	if err != nil {
		return nil, err
	}
	tariff, err := charged(c.TariffUSD)
	if err != nil {
		return nil, err
	}
	if c.ActualSource == "free_provider" || c.ActualSource == "provider_failed_no_charge" {
		zero := money.USD(0)
		metered, tariff = &zero, &zero
	}
	var providerExpected, customerExpected *money.USD
	var units *float64
	if !c.QuantityUnknown {
		providerExpected = &c.ExpectedUSD
		customerExpected = &expected
		units = &c.EstimatedUnits
	}
	return json.Marshal(struct {
		plain
		ExpectedUSD      *money.USD `json:"expected_usd,omitempty"`
		CustomerExpected *money.USD `json:"customer_expected_usd,omitempty"`
		EstimatedUnits   *float64   `json:"estimated_units,omitempty"`
		CustomerMetered  *money.USD `json:"customer_metered_usd,omitempty"`
		CustomerTariff   *money.USD `json:"customer_tariff_usd,omitempty"`
	}{plain(c), providerExpected, customerExpected, units, metered, tariff})
}
