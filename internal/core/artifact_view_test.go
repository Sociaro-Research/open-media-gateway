package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestArtifactPublicViewKeepsSourceAndStoragePrivate(t *testing.T) {
	key := "private-owner/private-storage.png"
	task := Task{ID: "fixture", Status: TaskSucceeded, MediaType: "image", StorageKey: &key, StoredArtifacts: []StoredArtifact{{ID: "1", ProviderID: "upstream-private-id", MediaType: "image", StorageKey: key, ContentType: "image/png", SizeBytes: 12, SHA256: "hash"}, {ID: "2", MediaType: "image", StorageKey: "another-private-key", ContentType: "image/png"}}}
	raw, err := json.Marshal(GenerationView(task))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) || strings.Contains(string(raw), "another-private-key") || strings.Contains(string(raw), "upstream-private-id") || !strings.Contains(string(raw), "/v1/generations/fixture/artifacts/2/content") {
		t.Fatal(string(raw))
	}
	now := time.Now()
	task.ResultDeletedAt = &now
	raw, _ = json.Marshal(GenerationView(task))
	if strings.Contains(string(raw), "/artifacts/") {
		t.Fatal("deleted artifact exposed", string(raw))
	}
}
func TestInputDerivedVideoDurationPublicViewUsesMeasuredOutput(t *testing.T) {
	task := Task{ID: "fixture", MediaType: "video", Status: TaskSucceeded, Usage: &ProviderUsage{Metrics: []UsageMetric{{Name: "output_video_duration", Quantity: 8.5}}}}
	if view := GenerationView(task); view.Seconds != "8.5" {
		t.Fatal(view.Seconds)
	}
	task.StoredArtifacts = []StoredArtifact{{Duration: 7.25}}
	if view := GenerationView(task); view.Seconds != "7.25" {
		t.Fatal(view.Seconds)
	}
	task.StoredArtifacts = nil
	task.Usage = nil
	if view := GenerationView(task); view.Seconds != "" {
		t.Fatal("unknown duration became zero seconds", view.Seconds)
	}
}
