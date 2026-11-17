package bytedance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

const base25ID = "dreamina-seedance-2-5-260628"

func TestSeedance25Base4KOnlyOnBaseModel(t *testing.T) {
	if _, err := core.NormalizeModelOptions(describeModel(base25ID), core.MediaOptions{Resolution: "4k", Ratio: "16:9", Duration: 30}); err != nil {
		t.Fatal("base 2.5 4K rejected", err)
	}
	for _, id := range []string{flashID, "dreamina-seedance-2-0-fast-260128", "dreamina-seedance-2-0-mini-260615"} {
		if _, err := core.NormalizeModelOptions(describeModel(id), core.MediaOptions{Resolution: "4k"}); err == nil {
			t.Fatal("4K admitted for", id)
		}
	}

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != base25ID || body["resolution"] != "4k" || body["ratio"] != "16:9" {
			t.Fatal(body)
		}
		return jsonResponse(http.StatusOK, `{"id":"base-4k-task"}`), nil
	})}
	a, err := New("synthetic", "https://api.example.test", client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Generate(context.Background(), core.MediaRequest{Model: base25ID, Prompt: "city at night", Options: core.MediaOptions{Resolution: "4k", Ratio: "16:9", Duration: 5}}); err != nil {
		t.Fatal(err)
	}
}

func TestSeedance25Base4KPrice(t *testing.T) {
	m := describeModel(base25ID)
	quote := func(o core.MediaOptions) (core.TaskCost, error) {
		return quotePublic("https://ark.ap-southeast.bytepluses.com", m, core.MediaRequest{Model: base25ID, Options: o}, time.Now())
	}
	// Published example: 5 s 16:9 4K without input video is about USD 4.568.
	cost, err := quote(core.MediaOptions{Resolution: "4k", Ratio: "16:9", Duration: 5})
	if err != nil || cost.Pricing.Price.String() != "4.700000" || cost.ExpectedUSD.String() != "4.568400" || cost.Pricing.Version != "2026-11-16.1" || !cost.Pricing.CheckedAt.Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)) {
		t.Fatal(cost, err)
	}
	if !cost.MeterUsage(core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "completion_tokens", Quantity: 500_000}}}) || cost.BilledUnits == nil || *cost.BilledUnits != 500_000 {
		t.Fatal("actual completion_tokens not metered", cost)
	}
	for _, ratio := range []string{"9:16", "4:3", "3:4", "1:1", "21:9"} {
		if c, err := quote(core.MediaOptions{Resolution: "4k", Ratio: ratio, Duration: 30}); err != nil || c.Pricing.Price.String() != "4.700000" {
			t.Fatal(ratio, c, err)
		}
	}
	// Published 5 s 4K floor with input video: USD 5.07384 = 1,749,600 tokens at 2.9.
	if minimumVideoTokens(5, 3840*2160*24/1024.0) != 1_749_600 {
		t.Fatal("4K floor example changed")
	}
	if _, err := quote(core.MediaOptions{Resolution: "4k", Ratio: "adaptive", Duration: 5}); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("adaptive 4K priced without dimensions", err)
	}
	// Lower resolutions keep their earlier review and price.
	if c, err := quote(core.MediaOptions{Resolution: "1080p", Ratio: "16:9", Duration: 5}); err != nil || c.Pricing.Price.String() != "11.700000" || c.Pricing.Version != tariffVersion {
		t.Fatal(c, err)
	}
}
