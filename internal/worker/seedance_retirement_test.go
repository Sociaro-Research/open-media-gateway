package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
)

type seedanceRetirementUsageRepo struct {
	*repoStub
	usage core.ProviderUsage
}

func (r *seedanceRetirementUsageRepo) SetProviderUsage(_ context.Context, _ string, usage core.ProviderUsage) error {
	r.usage = usage
	return nil
}

type seedanceRetirementUsageProvider struct {
	core.MediaProvider
	generations, polls, discoveries, histories int
}

func (p *seedanceRetirementUsageProvider) Name() string { return "bytedance" }
func (p *seedanceRetirementUsageProvider) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	p.generations++
	return core.ProviderTask{ID: "unexpected"}, nil
}
func (p *seedanceRetirementUsageProvider) ListModels(context.Context) ([]core.MediaModel, error) {
	p.discoveries++
	return nil, nil // Reviewed retirement removed the model from new-admission lists.
}
func (p *seedanceRetirementUsageProvider) HistoricalUsageModel(_ context.Context, id string) (core.MediaModel, bool, error) {
	p.histories++
	if id != "seedance-1-5-pro-251215" {
		return core.MediaModel{}, false, nil
	}
	return core.MediaModel{ID: id, Pricing: core.MediaModelPricing{Known: true, USDPerMillionTokens: 1.2}}, true, nil
}
func (p *seedanceRetirementUsageProvider) GetTask(context.Context, string) (core.ProviderResult, error) {
	p.polls++
	return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: "https://cdn.example/accepted.mp4",
		Usage: &core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "completion_tokens", Quantity: 38800}}, Raw: json.RawMessage(`{"completion_tokens":38800}`)}}, nil
}

func TestSeedanceAcceptedLegacyTaskRetainsUsageEstimateAfterRetirement(t *testing.T) {
	const id = "seedance-1-5-pro-251215"
	cutoff := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	if !modelcatalog.IsInactive("bytedance", id, cutoff) {
		t.Fatal("fixture must be a reviewed retired model")
	}
	nativeID := "accepted-before-cutoff"
	task := core.Task{ID: "legacy", Status: core.TaskRunning, Provider: "bytedance", Model: id,
		ProviderTaskID: &nativeID, ProviderIdentity: "original-revision", CreatedAt: cutoff.Add(-time.Minute),
		Options: core.MediaOptions{Resolution: "720p", Duration: 5}}
	repo := &seedanceRetirementUsageRepo{repoStub: &repoStub{task: task}}
	provider := &seedanceRetirementUsageProvider{}
	archive := &storageStub{}
	w := New(repo, nil, provider, archive, time.Millisecond, time.Second, 2, time.Second)
	if err := w.process(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if repo.task.Status != core.TaskSucceeded || repo.task.Model != id || repo.task.ProviderIdentity != task.ProviderIdentity || *repo.task.ProviderTaskID != nativeID {
		t.Fatal("accepted task identity/recovery changed", repo.task)
	}
	if provider.generations != 0 || provider.polls != 1 || provider.histories != 1 || provider.discoveries != 0 {
		t.Fatal("legacy usage re-discovered or re-submitted", provider)
	}
	if archive.archivedURL != "https://cdn.example/accepted.mp4" || repo.usage.EstimatedCost == nil {
		t.Fatal("accepted media or legacy cost estimate lost", archive, repo.usage)
	}
	if got := *repo.usage.EstimatedCost; got < 0.046559999 || got > 0.046560001 {
		t.Fatal("historical tariff estimate changed", got)
	}
	if repo.usage.Currency != "USD" || repo.usage.CostSource != "provider usage × public model tariff" || string(repo.usage.Raw) != `{"completion_tokens":38800}` {
		t.Fatal("historical usage provenance changed", repo.usage)
	}
}

func TestSeedanceAcceptedSnapshotNeverFallsBackToHistoricalPricing(t *testing.T) {
	repo := &seedanceRetirementUsageRepo{repoStub: &repoStub{}}
	provider := &seedanceRetirementUsageProvider{}
	w := &Worker{repo: repo}
	cost := 0.09
	for _, task := range []core.Task{
		{ID: "accepted-cost", Model: "seedance-1-5-pro-251215", Cost: &core.TaskCost{}},
		{ID: "provider-cost", Model: "seedance-1-5-pro-251215"},
	} {
		usage := &core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "completion_tokens", Quantity: 38800}}, EstimatedCost: &cost}
		if err := w.storeUsage(context.Background(), provider, task, usage); err != nil {
			t.Fatal(err)
		}
		if *repo.usage.EstimatedCost != cost {
			t.Fatal("accepted/provider cost was rewritten", repo.usage)
		}
	}
	if provider.histories != 0 || provider.discoveries != 0 {
		t.Fatal("existing cost was re-priced", provider)
	}
}
