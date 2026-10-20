package catalogsync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

type inactiveRunnerRepository struct {
	Repository
	state State
	run   Run
}

func (r *inactiveRunnerRepository) State(context.Context, string) (State, error) {
	return r.state, nil
}

func (r *inactiveRunnerRepository) Commit(_ context.Context, _ Schedule, state State, run Run, _ time.Time) error {
	r.state, r.run = state, run
	return nil
}

type unavailableCatalogProvider struct{}

func (unavailableCatalogProvider) CaptureCatalog(context.Context, string) (Capture, error) {
	return Capture{}, errors.New("unavailable")
}

func (unavailableCatalogProvider) CheckCatalogRate(context.Context, string, core.MediaModel, Rate) error {
	return errors.New("unavailable")
}

func TestRunnerDiscoveryFailurePreservesInactiveModel(t *testing.T) {
	const id = "seedream-3-0-t2i-250415"
	repo := &inactiveRunnerRepository{state: State{
		Version: 1,
		Models: map[string]Entry{id: {
			ID: id, Inactive: true,
			Metadata: &core.ModelMetadata{ShutdownDate: "2026-05-13"},
		}},
	}}
	runner := Runner{Repo: repo, Providers: unavailableCatalogProvider{}}
	if err := runner.check(context.Background(), Schedule{Provider: "bytedance", Time: "03:00"}, Settings{Timezone: "UTC"}, time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	entry := repo.state.Models[id]
	if !entry.Inactive || entry.Contract != nil || entry.Metadata.ShutdownDate != "2026-05-13" {
		t.Fatalf("discovery failure reactivated historical model: %+v", entry)
	}
	if len(repo.run.Changes) != 0 || len(repo.run.OpenWarnings) != 1 || repo.run.OpenWarnings[0].Source != "_api" {
		t.Fatalf("discovery failure must only warn, not change the catalogue: %+v", repo.run)
	}
}

func TestRunnerDiscoveryFailurePreservesScopeDecision(t *testing.T) {
	const id = "voice-enrollment"
	repo := &inactiveRunnerRepository{state: State{Version: 1, Models: map[string]Entry{id: {
		ID: id, ExcludedReason: "Voice profile creation is outside file generation",
		Metadata: &core.ModelMetadata{Sources: []core.ModelSource{{URL: "https://example.test/voice-profiles"}}},
	}}}}
	runner := Runner{Repo: repo, Providers: unavailableCatalogProvider{}}
	if err := runner.check(context.Background(), Schedule{Provider: "alibaba", Time: "03:00"}, Settings{Timezone: "UTC"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	entry := repo.state.Models[id]
	if entry.Contract != nil || entry.ExcludedReason == "" || len(entry.Metadata.Sources) != 1 {
		t.Fatal("discovery outage discarded the reviewed scope decision", entry)
	}
	if len(repo.run.Changes) != 0 || len(repo.run.OpenWarnings) != 1 || repo.run.OpenWarnings[0].Source != "_api" {
		t.Fatal("outage should only report the failed discovery", repo.run)
	}
}
