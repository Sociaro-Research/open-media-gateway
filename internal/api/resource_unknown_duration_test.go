package api

import (
	"math"
	"net/http/httptest"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestResourceGeneratedOutputUnknownDurationRequiresReviewedRule(t *testing.T) {
	f := newResourceAPIFixture(t)
	ctx := f.context("alice")
	task := generatedVideoArchivedTask(t, f, 0)
	id := task.ID
	f.tasks.tasks[id] = task
	intent := &core.ResourceIntent{Kind: "file_resource", Subtype: "video", Name: "Owned output", GeneratedTaskID: id}
	model := core.MediaModel{ResourceOperation: &core.ResourceOperationSchema{Kind: "file_resource", Subtypes: []string{"video"}, Variants: []core.ResourceVariantSchema{{Subtype: "video", GeneratedSources: []core.GeneratedResourceRule{{Model: task.Model, MinDuration: 0, MaxDuration: 10}}}}}}
	request := httptest.NewRequest("POST", "/v1/provider-resources", nil).WithContext(ctx)
	if _, err := f.s.resolveResourceCreation(request, intent, "kling", model); err == nil {
		t.Fatal("undeclared unknown duration admitted")
	}
	model.ResourceOperation.Variants[0].GeneratedSources[0].AllowUnknownDuration = true
	result, err := f.s.resolveResourceCreation(request, intent, "kling", model)
	if err != nil || result.GeneratedVideo == nil || result.GeneratedVideo.Duration != nil {
		t.Fatal("unknown duration lost or fabricated", result, err)
	}
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1)} {
		task.StoredArtifacts[0].Duration = bad
		f.tasks.tasks[id] = task
		if _, err = f.s.resolveResourceCreation(request, intent, "kling", model); err == nil {
			t.Fatal("invalid duration treated as unknown", bad)
		}
	}
	task.StoredArtifacts[0].Duration = 4
	f.tasks.tasks[id] = task
	result, err = f.s.resolveResourceCreation(request, intent, "kling", model)
	if err != nil || result.GeneratedVideo.Duration == nil || *result.GeneratedVideo.Duration != 4 {
		t.Fatal("known duration lost", result, err)
	}
	task.ProviderIdentity = "retired-revision"
	f.tasks.tasks[id] = task
	if _, err = f.s.resolveResourceCreation(request, intent, "kling", model); err == nil {
		t.Fatal("explicit unknown rule bypassed connection binding")
	}
}
