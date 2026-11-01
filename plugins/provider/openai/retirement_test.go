package openai

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"net/http"
	"strings"
	"testing"
)

func TestStaleDiscoveryCannotEnableReviewedRetirements(t *testing.T) {
	a := adapter(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/models") {
			t.Fatal("retired model reached generation", r.URL)
		}
		return response(200, "application/json", `{"data":[{"id":"gpt-image-1"},{"id":"gpt-audio-mini-2025-10-06"},{"id":"gpt-audio-mini"},{"id":"gpt-audio-mini-2025-12-15"}]}`), nil
	})
	models, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("stale discovery retained retired IDs: %+v", models)
	}
	for _, m := range models {
		if m.ID != "gpt-audio-mini" && m.ID != "gpt-audio-mini-2025-12-15" {
			t.Fatal(m.ID)
		}
	}
	for _, id := range []string{"gpt-image-1", "gpt-audio-mini-2025-10-06"} {
		if _, err = a.Generate(context.Background(), core.MediaRequest{Model: id, Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "retired") {
			t.Fatal(id, err)
		}
	}
}
