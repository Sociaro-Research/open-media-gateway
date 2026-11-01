package core

import (
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"strings"
	"testing"
)

func TestReferenceRolesAreTypedAndBoundToExistingInputs(t *testing.T) {
	m := MediaModel{ReferenceRoleSchemas: []ReferenceRoleSchema{{Kind: "video", Roles: []string{"reference", "source"}, Default: "reference"}}}
	for _, tc := range []struct {
		key, role string
		valid     bool
	}{{"video_1", "source", true}, {"video_2", "source", false}, {"image_1", "source", false}, {"video_01", "source", false}, {"video_1", "provider_arbitrary_field", false}} {
		o := MediaOptions{VideoURLs: []string{"https://example.test/v.mp4"}, ReferenceRoles: map[string]string{tc.key: tc.role}}
		if err := ValidateReferenceRoles(m, o); (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
	if got := ReferenceRole(m, MediaOptions{}, "video", 0); got != "reference" {
		t.Fatal(got)
	}
}

func TestReviewedMixedPromptBudgetAndLegacyCeiling(t *testing.T) {
	m := MediaModel{PromptPolicy: &PromptPolicy{MaxCharacters: 26000, ASCIIBudget: 26000, NonASCIIBudget: 11000}}
	exact := strings.Repeat("E", 13000) + strings.Repeat("界", 5500)
	if err := ValidatePromptPolicy(m, exact); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePromptPolicy(m, exact+"E"); !errors.Is(err, ErrPromptTooLong) {
		t.Fatal(err)
	}
	if err := ValidatePromptPolicy(MediaModel{}, strings.Repeat("a", 10001)); !errors.Is(err, ErrPromptTooLong) {
		t.Fatal("legacy default changed", err)
	}
	if err := ValidatePromptPolicy(m, strings.Repeat("界", 11001)); !errors.Is(err, ErrPromptTooLong) {
		t.Fatal(err)
	}
}

func TestNonlinearDurationTariffRequiresActualInputCounter(t *testing.T) {
	for _, input := range []float64{4, 10, 17} {
		cost := TaskCost{Pricing: PricingSnapshot{Currency: "USD", Unit: "second", Price: money.USD(141000), Per: 1, DurationBillingRule: "output_plus_max_input_output"}}
		data, _ := json.Marshal(cost)
		if err := json.Unmarshal(data, &cost); err != nil {
			t.Fatal(err)
		}
		usage := ProviderUsage{Metrics: []UsageMetric{{Name: "output_video_duration", Quantity: 10}, {Name: "input_video_duration", Quantity: input}}}
		if !cost.MeterUsage(usage) || *cost.MeteredUSD != money.USD((10+max(input, 10))*141000) {
			t.Fatal(input, cost)
		}
		cost.MeteredUSD = nil
		if cost.MeterUsage(ProviderUsage{Metrics: usage.Metrics[:1]}) {
			t.Fatal("missing input duration was treated as zero")
		}
	}
}
