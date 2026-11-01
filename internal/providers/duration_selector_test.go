package providers

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/config"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	registry "github.com/Sociaro-Research/open-media-gateway/plugins/provider"
	"net/http"
	"testing"
	"time"
)

func TestDurationSpecificQuoteSelectionAndGenericFallback(t *testing.T) {
	m := core.MediaModel{ID: "duration-fixture", MediaType: "video", MinDuration: 6, MaxDuration: 10, DefaultDuration: 6, DurationStep: 4}
	base := core.TaskCost{ExpectedUSD: 100000, EstimatedUnits: 1, Pricing: core.PricingSnapshot{Currency: "USD", Region: "Global", Unit: "request", Per: 1, Price: 100000}}
	generic := catalogsync.Rate{Region: "Global", Unit: "request", Per: 1, Price: 90000}
	six := generic
	six.Duration = 6
	six.Price = 280000
	ten := generic
	ten.Duration = 10
	ten.Price = 560000
	for _, order := range [][]catalogsync.Rate{{generic, six, ten}, {ten, six, generic}} {
		state := catalogsync.State{Version: 1, Models: map[string]catalogsync.Entry{m.ID: {ID: m.ID, Rates: order}}}
		for _, tc := range []struct {
			duration int
			want     money.USD
		}{{0, 280000}, {6, 280000}, {10, 560000}, {8, 90000}} {
			got, e := applyCatalogQuote(base, nil, m, core.MediaRequest{Model: m.ID, Options: core.MediaOptions{Duration: tc.duration}}, state)
			if e != nil || got.ExpectedUSD != tc.want {
				t.Fatal(tc, got, e)
			}
		}
	}
	unrelated := catalogsync.State{Models: map[string]catalogsync.Entry{m.ID: {Rates: []catalogsync.Rate{generic}}}}
	for _, duration := range []int{0, 6, 10} {
		got, e := applyCatalogQuote(base, nil, m, core.MediaRequest{Options: core.MediaOptions{Duration: duration}}, unrelated)
		if e != nil || got.ExpectedUSD != generic.Price {
			t.Fatal("unconditioned pricing changed", got, e)
		}
	}
}

func TestCheckCatalogRateUsesAndValidatesDurationSelector(t *testing.T) {
	repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
	cipher, _ := providerconfig.NewCipher("duration-fixture-key")
	manager := NewManager(repo, cipher, config.Config{})
	model := core.MediaModel{ID: "duration-fixture", MediaType: "video", MinDuration: 6, MaxDuration: 10, DefaultDuration: 6, DurationStep: 4}
	seen := 0
	definition := providerkit.Definition{ID: "duration-fixture", Name: "Duration fixture", DefaultModel: model.ID, PublicCatalog: true, Factory: func(providerkit.Credentials, providerkit.Settings, *http.Client) (core.MediaProvider, error) {
		return extensionAdapter{}, nil
	}, Quote: func(_ string, _ core.MediaModel, r core.MediaRequest, _ time.Time) (core.TaskCost, error) {
		seen = r.Options.Duration
		return core.TaskCost{ExpectedUSD: 1, EstimatedUnits: 1, Pricing: core.PricingSnapshot{Currency: "USD", Region: "Global", Unit: "request", Per: 1, Price: 1}}, nil
	}}
	manager.registry = registry.MustRegistry(definition)
	ctx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), identity.SharedOwnerID), identity.SharedOwnerID, identity.SharedOwnerID)
	if e := manager.Bootstrap(ctx); e != nil {
		t.Fatal(e)
	}
	enabled := true
	if _, e := manager.Update(ctx, definition.ID, ProviderUpdate{Enabled: &enabled}); e != nil {
		t.Fatal(e)
	}
	for _, duration := range []int{0, 6, 10} {
		seen = 0
		rate := catalogsync.Rate{Duration: duration, Region: "Global", Unit: "request", Per: 1, Price: 2}
		if e := manager.CheckCatalogRate(ctx, definition.ID, model, rate); e != nil {
			t.Fatal(duration, e)
		}
		want := duration
		if want == 0 {
			want = 6
		}
		if seen != want {
			t.Fatal("duration not used by estimator", duration, seen)
		}
	}
	for _, duration := range []int{-1, 5, 8, 11} {
		seen = 0
		if e := manager.CheckCatalogRate(ctx, definition.ID, model, catalogsync.Rate{Duration: duration, Region: "Global", Unit: "request", Per: 1, Price: 2}); e == nil || seen != 0 {
			t.Fatal("unsupported duration reached estimator", duration, e, seen)
		}
	}
}
