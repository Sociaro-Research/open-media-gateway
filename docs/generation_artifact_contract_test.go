package docs_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestArtifactGuideDistinguishesPrimaryAndCompleteResults(t *testing.T) {
	guide, err := os.ReadFile("openai-api.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"### Archived results", "first archived result", "`artifacts`", "/v1/generations/{id}/artifacts/{artifact}/content", "does not establish a refund"} {
		if !strings.Contains(string(guide), text) {
			t.Errorf("generation guide must explain %q", text)
		}
	}
	schemas := object(t, object(t, generationSpecification(t)["components"])["schemas"])
	generation := object(t, object(t, schemas["Generation"])["properties"])
	description, _ := object(t, generation["content_url"])["description"].(string)
	if !strings.Contains(description, "first archived result") {
		t.Error("content_url must identify the primary result rather than the whole batch")
	}
	key := "private-primary-key"
	task := core.Task{ID: "owned", MediaType: "video", Provider: "fixture", Model: "video",
		StorageKey: &key, StoredArtifacts: []core.StoredArtifact{
			{ID: "1", ProviderID: "private-upstream-one", GroupID: "private-group", MediaType: "video", StorageKey: key, ContentType: "video/mp4", SizeBytes: 12, SHA256: "hash-one"},
			{ID: "2", ProviderID: "private-upstream-two", GroupID: "private-group", MediaType: "audio", Format: "wav", StorageKey: "private-second-key", ContentType: "audio/wav", SizeBytes: 24, SHA256: "hash-two"},
		}}
	for _, state := range []core.TaskStatus{core.TaskQueued, core.TaskRunning, core.TaskFailed, core.TaskSucceeded} {
		task.Status = state
		view := core.GenerationView(task)
		if len(view.Artifacts) != 0 && state != core.TaskSucceeded || state == core.TaskSucceeded && len(view.Artifacts) != 2 {
			t.Fatal("artifact array did not follow public completion", state, view.Artifacts)
		}
	}
	view := core.GenerationView(task)
	if view.ContentURL != "/v1/videos/owned/content" || view.Artifacts[1].ContentURL != "/v1/generations/owned/artifacts/2/content" || view.Artifacts[0].GroupID == "" || view.Artifacts[0].GroupID != view.Artifacts[1].GroupID {
		t.Fatal("primary route, individual route or local grouping changed", view)
	}
	artifactSchema := object(t, schemas["GenerationArtifact"])
	for _, artifact := range view.Artifacts {
		raw, err := json.Marshal(artifact)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for _, field := range artifactSchema["required"].([]any) {
			if _, present := fields[field.(string)]; !present {
				t.Errorf("OpenAPI requires artifact field %s omitted by the public view", field)
			}
		}
		if strings.Contains(string(raw), "private-") {
			t.Fatal("private provider or storage metadata reached the public result")
		}
	}
	deleted := time.Unix(456, 0)
	task.ResultDeletedAt = &deleted
	if len(core.GenerationView(task).Artifacts) != 0 {
		t.Fatal("deleted result retained public artifacts")
	}
	access := object(t, schemas["MediaAccess"])
	for _, field := range access["required"].([]any) {
		if field == "artifacts" || field == "last_frame_url" {
			t.Error("legacy/scalar media access must not require optional artifact or last-frame arrays")
		}
	}
}
