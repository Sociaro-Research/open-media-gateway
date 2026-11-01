package kling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func effectPriceCredentials(raw string) providerkit.Credentials {
	return providerkit.Credentials{Extra: map[string]string{"effect_prices": raw}}
}

func TestEffectPricesQuoteEveryExactDocumentedSceneWithoutNetwork(t *testing.T) {
	if len(effectScenes) != maxEffectPrices {
		t.Fatal("reviewed Effects scene count changed", len(effectScenes))
	}
	a := adapter(t, transport(func(*http.Request) (*http.Response, error) {
		t.Fatal("pricing must not contact a provider")
		return nil, nil
	}))
	prices := map[string]string{}
	for i, scene := range effectScenes {
		prices[scene] = fmt.Sprintf("0.%06d", i+1)
	}
	raw, err := json.Marshal(prices)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.setEffectPrices(effectPriceCredentials(string(raw))); err != nil {
		t.Fatal(err)
	}
	for i, scene := range effectScenes {
		r := solutionFixtureRequest(KlingEffects)
		r.Options.Parameters["effect_scene"] = scene
		if slices.Contains(dualImageEffects, scene) {
			r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/right.png")
		}
		before, _ := json.Marshal(r)
		q, fingerprint, err := a.quoteEffect(r)
		if err != nil || q.ExpectedUSD != money.USD(i+1) || q.EstimatedUnits != 1 || q.Status != "estimated" || q.QuantityUnknown || fingerprint != "" || q.Pricing.Price != q.ExpectedUSD || q.Pricing.Unit != "request" || q.Pricing.Per != 1 || !q.Pricing.AccountSpecific || q.Pricing.Source != "Operator-confirmed account tariff" {
			t.Fatalf("scene %q: incorrect explicit tariff: %#v fingerprint=%q err=%v", scene, q, fingerprint, err)
		}
		after, _ := json.Marshal(r)
		if !bytes.Equal(before, after) {
			t.Fatal("quote mutated accepted intent", scene)
		}
	}
	if solutionModel(KlingEffects).Pricing.Known {
		t.Fatal("account override invented a public scene tariff")
	}
}

func TestEffectPricesBlankAndUnconfiguredScenesRemainUnknown(t *testing.T) {
	a, _ := New("synthetic", "", nil)
	r := solutionFixtureRequest(KlingEffects)
	for _, raw := range []string{"", " \n\t", "{}"} {
		if err := a.setEffectPrices(effectPriceCredentials(raw)); err != nil {
			t.Fatal(err)
		}
		if q, _, err := a.quoteEffect(r); !errors.Is(err, core.ErrPricingUnavailable) || q.ExpectedUSD != 0 || q.Pricing.AccountSpecific {
			t.Fatal("unknown scene was priced", q, err)
		}
	}
	a.operationPrices["selection_init"] = 120000
	if err := a.setEffectPrices(effectPriceCredentials(`{"net_the_fish":"0.123456"}`)); err != nil {
		t.Fatal(err)
	}
	r.Options.Parameters["effect_scene"] = "birthday_board"
	if _, _, err := a.quoteEffect(r); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("one scene tariff leaked to another scene", err)
	}
	if err := a.setEffectPrices(effectPriceCredentials("")); err != nil {
		t.Fatal(err)
	}
	r.Options.Parameters["effect_scene"] = "net_the_fish"
	if _, _, err := a.quoteEffect(r); !errors.Is(err, core.ErrPricingUnavailable) || a.operationPrices["selection_init"] != 120000 {
		t.Fatal("clearing Effects retained a rate or altered other operation prices", err)
	}
}

func TestEffectPricesRejectMalformedOrUnreviewedConfigurationAtomically(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `"not an object"`, `1`, `{`, `{"net_the_fish":"0.1",}`, `{"net_the_fish":"0.1"} {}`, `{"net_the_fish":"0.1"} trailing`,
		`{"future_scene":"0.1"}`, `{"NET_THE_FISH":"0.1"}`, `{" net_the_fish":"0.1"}`, `{"":"0.1"}`,
		`{"net_the_fish":"0.1","net_the_fish":"0.2"}`, `{"net_the_fish":"0.1","\u006eet_the_fish":"0.2"}`,
		`{"net_the_fish":1}`, `{"net_the_fish":null}`, `{"net_the_fish":true}`, `{"net_the_fish":{}}`, `{"net_the_fish":[]}`,
		`{"net_the_fish":""}`, `{"net_the_fish":"0"}`, `{"net_the_fish":"0.000000"}`, `{"net_the_fish":"-0.1"}`, `{"net_the_fish":"+0.1"}`, `{"net_the_fish":" 0.1 "}`, `{"net_the_fish":"NaN"}`, `{"net_the_fish":"Inf"}`, `{"net_the_fish":"1/2"}`, `{"net_the_fish":"1e-1"}`, `{"net_the_fish":"0.0000001"}`, `{"net_the_fish":"9223372036854.775808"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			a, _ := New("synthetic", "", nil)
			if err := a.setEffectPrices(effectPriceCredentials(`{"birthday_board":"0.2"}`)); err != nil {
				t.Fatal(err)
			}
			err := a.setEffectPrices(effectPriceCredentials(raw))
			if err == nil || len(a.operationPrices) != 1 || a.operationPrices["effect:birthday_board"] != 200000 {
				t.Fatal("invalid update changed accepted configuration", err, a.operationPrices)
			}
			if strings.Contains(err.Error(), raw) {
				t.Fatal("configuration was copied into the error")
			}
		})
	}
}

func TestEffectPricesRequireTheTypedEffectsRequest(t *testing.T) {
	a, _ := New("synthetic", "", nil)
	if err := a.setEffectPrices(effectPriceCredentials(`{"net_the_fish":"0.1","pet_skateboard":"0.2"}`)); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*core.MediaRequest){
		func(r *core.MediaRequest) { r.Model = GoodsStudio },
		func(r *core.MediaRequest) { r.Prompt = "unsupported prompt" },
		func(r *core.MediaRequest) { r.Options.Parameters = nil },
		func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"scene": "net_the_fish"} },
		func(r *core.MediaRequest) { r.Options.Parameters["effect_scene"] = 1 },
		func(r *core.MediaRequest) { r.Options.Parameters["effect_scene"] = "future_scene" },
		func(r *core.MediaRequest) { r.Options.Parameters["effect_scene"] = "NET_THE_FISH" },
		func(r *core.MediaRequest) { r.Options.Parameters["raw_provider_payload"] = `{}` },
		func(r *core.MediaRequest) { r.Options.ImageURLs = nil },
		func(r *core.MediaRequest) { r.Options.ImageURLs[0] = "http://127.0.0.1/private" },
		func(r *core.MediaRequest) { r.Options.Parameters["effect_scene"] = "pet_skateboard" },
	} {
		r := solutionFixtureRequest(KlingEffects)
		change(&r)
		if _, _, err := a.quoteEffect(r); err == nil || errors.Is(err, core.ErrPricingUnavailable) {
			t.Fatal("malformed effect intent reached tariff lookup", r, err)
		}
	}
}

func TestEffectPricesFactoryAndAdapterQuoteUseOnlyExplicitSceneTariffs(t *testing.T) {
	definition := Definition()
	found := false
	for _, field := range definition.Fields {
		if field.Key == "effect_prices" {
			if field.Env != "KLING_EFFECT_PRICES" || field.Secret || field.Required {
				t.Fatal("Effects price configuration field changed", field)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("Effects prices are not wired into provider configuration")
	}
	client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		t.Fatal("Factory/Quote must not contact a provider")
		return nil, nil
	})}
	for _, raw := range []string{"", "{}", `{"net_the_fish":"1.250001"}`} {
		credentials := effectPriceCredentials(raw)
		credentials.APIKey = "synthetic"
		provider, err := definition.Factory(credentials, providerkit.Settings{}, client)
		if err != nil {
			t.Fatal("Factory rejected valid Effects configuration", err)
		}
		quoter, ok := provider.(core.CostQuoter)
		if !ok {
			t.Fatal("adapter has no quotation contract")
		}
		r := solutionFixtureRequest(KlingEffects)
		quote, _, err := quoter.Quote(context.Background(), solutionModel(KlingEffects), r)
		if raw == "" || raw == "{}" {
			if !errors.Is(err, core.ErrPricingUnavailable) {
				t.Fatal("default Effects tariff became available", quote, err)
			}
		} else if err != nil || quote.ExpectedUSD != 1250001 || !quote.Pricing.AccountSpecific || quote.Pricing.Unit != "request" {
			t.Fatal("Factory or public Quote dispatch lost explicit scene tariff", quote, err)
		}
		r.Options.Parameters["effect_scene"] = "birthday_board"
		if _, _, err := quoter.Quote(context.Background(), solutionModel(KlingEffects), r); !errors.Is(err, core.ErrPricingUnavailable) {
			t.Fatal("public Quote dispatch leaked another scene's tariff", err)
		}
	}
	for _, raw := range []string{`{"future_scene":"1"}`, `{"net_the_fish":"0.1","net_the_fish":"0.2"}`} {
		credentials := effectPriceCredentials(raw)
		credentials.APIKey = "synthetic"
		if _, err := definition.Factory(credentials, providerkit.Settings{}, client); err == nil {
			t.Fatal("Factory accepted invalid Effects configuration")
		}
	}
}
