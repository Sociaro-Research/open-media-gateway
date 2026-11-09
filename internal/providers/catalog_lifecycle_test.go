package providers

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPublishedLifecycleGuardsAdmissionButKeepsPollingAndPersonalScope(t *testing.T) {
	manager, _, personal := fixtureConnection(t)
	shared := identity.WithResourceOwners(personal, identity.SharedOwnerID, identity.SharedOwnerID)
	const id = "dreamina-seedance-2-5-260628"
	posts, polls := 0, 0
	manager.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			posts++
			t.Fatal("retired model submitted generation")
			return nil, errors.New("unexpected POST")
		}
		body := `{"data":[{"id":"` + id + `"}]}`
		if strings.Contains(r.URL.Path, "/contents/generations/tasks/") {
			polls++
			body = `{"id":"accepted","status":"running"}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	for _, ctx := range []context.Context{shared, personal} {
		if _, err := manager.Update(ctx, "bytedance", ProviderUpdate{APIKey: pointer("fixture"), Enabled: pointer(true)}); err != nil {
			t.Fatal(err)
		}
	}
	provider := manager.ManagedProviders()["bytedance"].(*managedProvider)
	models, err := provider.ListModels(shared)
	if err != nil || len(models) != 1 {
		t.Fatal(models, err)
	}
	manager.SetCatalog(&providerCatalogFixture{state: catalogsync.State{Models: map[string]catalogsync.Entry{id: {ID: id, Lifecycle: &catalogsync.Lifecycle{ShutdownDate: "2020-01-01"}}}}})
	modelsAfter, err := provider.ListModels(shared)
	if err != nil || len(modelsAfter) != 0 {
		t.Fatal("stale adapter cache bypassed published retirement", modelsAfter, err)
	}
	request, _ := referenceRequest(models[0])
	if err := provider.ValidateRequest(shared, request); !errors.Is(err, core.ErrModelUnavailable) {
		t.Fatal(err)
	}
	if _, _, err := provider.Quote(shared, models[0], request); !errors.Is(err, core.ErrModelUnavailable) {
		t.Fatal(err)
	}
	if _, err := provider.Generate(shared, request); !errors.Is(err, core.ErrModelUnavailable) {
		t.Fatal(err)
	}
	if _, err := provider.GetTask(shared, "accepted"); err != nil || polls != 1 || posts != 0 {
		t.Fatal("retirement blocked accepted polling", err, polls, posts)
	}
	personalModels, err := provider.ListModels(personal)
	if err != nil || len(personalModels) != 1 {
		t.Fatal("shared state leaked into personal connection", err)
	}
}

func TestPublishedAdmissionRechecksExactTimeWithoutAnotherSync(t *testing.T) {
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	state := catalogsync.State{Models: map[string]catalogsync.Entry{"m": {Lifecycle: &catalogsync.Lifecycle{ShutdownDate: "2026-11-11", ShutdownAt: at.Format(time.RFC3339)}}}}
	if catalogUnavailable(state, "m", at.Add(-time.Nanosecond)) || !catalogUnavailable(state, "m", at) {
		t.Fatal("admission depends on next daily sync")
	}
	state.Models["m"] = catalogsync.Entry{Inactive: true, Lifecycle: &catalogsync.Lifecycle{AvailableAt: at.Format(time.RFC3339)}}
	if !catalogUnavailable(state, "m", at.Add(-time.Nanosecond)) || catalogUnavailable(state, "m", at) {
		t.Fatal("stale pre-release flag overrode explicit availability")
	}
}

func TestInterpretedLifecycleProjectionWinsOverOlderBuiltInTimestamp(t *testing.T) {
	native := core.MediaModel{ID: "m", Metadata: &core.ModelMetadata{ShutdownDate: "2026-11-11", ShutdownAt: "2026-11-11T09:00:00Z"}}
	state := catalogsync.State{Models: map[string]catalogsync.Entry{"m": {ID: "m", Metadata: &core.ModelMetadata{ShutdownDate: "2026-11-10", ShutdownAt: "2026-11-10T09:00:00Z"}, Lifecycle: &catalogsync.Lifecycle{ShutdownDate: "2026-11-10", ShutdownAt: "2026-11-10T09:00:00Z"}}}}
	if got := applyCatalogModel(native, state); got.Metadata.ShutdownAt != "2026-11-10T09:00:00Z" {
		t.Fatal("accepted official boundary was hidden by stale built-in metadata", got.Metadata)
	}
}

type retirementAdmissionProbe struct {
	model       core.MediaModel
	submissions int
}

func (p *retirementAdmissionProbe) Name() string { return "fixture" }
func (p *retirementAdmissionProbe) ListModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{p.model}, nil
}
func (p *retirementAdmissionProbe) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	p.submissions++
	return core.ProviderTask{ID: "unexpected"}, nil
}
func (p *retirementAdmissionProbe) GetTask(context.Context, string) (core.ProviderResult, error) {
	return core.ProviderResult{Status: "running"}, nil
}
func TestEveryProviderChecksRetiredOutgoingIdentityBeforeManualRouting(t *testing.T) {
	for _, tc := range []struct{ provider, id, cutoff string }{{"openai", "gpt-image-1.5", "2026-12-01"}, {"alibaba", "qwen-image", "2026-10-10"}, {"bytedance", "seedance-1-0-lite-i2v-250428", "2026-05-13"}} {
		t.Run(tc.provider, func(t *testing.T) {
			at, _ := time.Parse("2006-01-02", tc.cutoff)
			price := .25
			model := core.MediaModel{ID: tc.id, MediaType: "image", Manual: true}
			probe := &retirementAdmissionProbe{model: model}
			adapter := &connectionAdapter{MediaProvider: probe, definition: providerkit.Definition{ID: tc.provider}, clock: func() time.Time { return at }, credentials: Credentials{Connection: &providerkit.Connection{ManualCatalog: true, Models: []providerkit.ManualModel{{Model: model, Profile: "active-profile", PriceUSD: &price}}}}}
			models, err := adapter.ListModels(context.Background())
			if err != nil || len(models) != 0 {
				t.Fatal(models, err)
			}
			request := core.MediaRequest{Model: tc.id}
			if err := adapter.ValidateRequest(context.Background(), request); !errors.Is(err, core.ErrModelUnavailable) {
				t.Fatal(err)
			}
			if _, _, err := adapter.Quote(context.Background(), model, request); !errors.Is(err, core.ErrModelUnavailable) {
				t.Fatal(err)
			}
			if _, err := adapter.Generate(context.Background(), request); !errors.Is(err, core.ErrModelUnavailable) || probe.submissions != 0 {
				t.Fatal(err, probe.submissions)
			}
		})
	}
}
