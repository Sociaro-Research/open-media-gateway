package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerationArtifactGroupsKeepNativeOutputIDsPrivate(t *testing.T) {
	task := Task{ID: "owned-result", Status: TaskSucceeded, MediaType: "audio", Provider: "fixture", Model: "tts", StoredArtifacts: []StoredArtifact{
		{ID: "1", ProviderID: "900719925474099312345", GroupID: "900719925474099312345", MediaType: "audio", Format: "mp3"},
		{ID: "2", ProviderID: "900719925474099312345:wav", GroupID: "900719925474099312345", MediaType: "audio", Format: "wav"},
		{ID: "3", ProviderID: "private-other", GroupID: "private-group-other", MediaType: "audio"},
		{ID: "4", ProviderID: "private-ungrouped", MediaType: "audio"},
	}}
	view := GenerationView(task)
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"900719925474099312345", "private-other", "private-group-other", "private-ungrouped"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("native output identity leaked through result grouping", secret)
		}
	}
	if len(view.Artifacts) != 4 || view.Artifacts[0].GroupID == "" || view.Artifacts[0].GroupID != view.Artifacts[1].GroupID || view.Artifacts[2].GroupID == view.Artifacts[0].GroupID || view.Artifacts[3].GroupID != "" {
		t.Fatal("local grouping lost its semantics", view.Artifacts)
	}
	again := GenerationView(task)
	if again.Artifacts[0].GroupID != view.Artifacts[0].GroupID || again.Artifacts[2].GroupID != view.Artifacts[2].GroupID {
		t.Fatal("local grouping identity is unstable")
	}
	if task.StoredArtifacts[0].GroupID != "900719925474099312345" || task.StoredArtifacts[0].ProviderID != "900719925474099312345" {
		t.Fatal("public view mutated private accepted evidence")
	}
}
