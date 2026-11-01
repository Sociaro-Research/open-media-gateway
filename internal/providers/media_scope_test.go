package providers

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"testing"
)

type nativePreparationProbe struct {
	core.MediaProvider
	calls []string
}

func (p *nativePreparationProbe) ListModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{{ID: "media", MediaType: "video"}}, nil
}
func (p *nativePreparationProbe) PreparationModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{{ID: "prepare", MediaType: "resource", ResourceOperation: &core.ResourceOperationSchema{Kind: "file_resource", Subtypes: []string{"video"}}}}, nil
}
func (p *nativePreparationProbe) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	if r.Model != "prepare" || core.IsManualCall(ctx) {
		panic("canonical helper was substituted")
	}
	p.calls = append(p.calls, "validate")
	return nil
}
func (p *nativePreparationProbe) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if r.Model != "prepare" || core.IsManualCall(ctx) {
		panic("canonical helper was substituted")
	}
	p.calls = append(p.calls, "generate")
	return core.ProviderTask{ID: "native"}, nil
}
func (p *nativePreparationProbe) Quote(ctx context.Context, m core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	if r.Model != "prepare" || core.IsManualCall(ctx) {
		panic("canonical quote was substituted")
	}
	p.calls = append(p.calls, "quote")
	return core.TaskCost{ExpectedUSD: money.USD(7)}, "native", nil
}
func TestCanonicalPreparationCannotBeInterceptedByPersistedManualAlias(t *testing.T) {
	native := &nativePreparationProbe{}
	price := 999.0
	a := connectionAdapter{MediaProvider: native, definition: providerkit.Definition{Profiles: func() []core.MediaModel { return []core.MediaModel{{ID: "media", MediaType: "video"}} }}, credentials: Credentials{Connection: &providerkit.Connection{Models: []providerkit.ManualModel{{Profile: "media", Model: core.MediaModel{ID: "prepare", MediaType: "video"}, PriceUSD: &price, ExtraBody: map[string]any{"untrusted": "override"}}}}}}
	before := connectionIdentity("fixture", a.definition, a.credentials)
	models, err := a.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "media" {
		t.Fatal("manual helper leaked", models, err)
	}
	request := core.MediaRequest{Model: "prepare"}
	if err = a.ValidateRequest(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	quote, _, err := a.Quote(context.Background(), core.MediaModel{ID: "prepare"}, request)
	if err != nil || quote.ExpectedUSD != 7 {
		t.Fatal("manual fee replaced canonical helper", quote, err)
	}
	result, err := a.Generate(context.Background(), request)
	if err != nil || result.ID != "native" {
		t.Fatal("native dispatch changed", result, err)
	}
	if len(native.calls) != 3 || connectionIdentity("fixture", a.definition, a.credentials) != before {
		t.Fatal("native calls/configuration changed")
	}
}
