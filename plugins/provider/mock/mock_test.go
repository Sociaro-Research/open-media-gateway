package mock

import (
	"context"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestAdapterCompletesAfterDelay(t *testing.T) {
	base := time.Unix(100, 0)
	adapter := New(time.Second, "embedded")
	adapter.now = func() time.Time { return base }
	created, err := adapter.Generate(context.Background(), core.MediaRequest{TaskID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := adapter.GetTask(context.Background(), created.ID)
	if err != nil || result.Status != core.ProviderRunning {
		t.Fatalf("first poll: %#v, %v", result, err)
	}
	adapter.now = func() time.Time { return base.Add(2 * time.Second) }
	result, err = adapter.GetTask(context.Background(), created.ID)
	if err != nil || result.Status != core.ProviderSucceeded || result.ResultURL == "" {
		t.Fatalf("completed poll: %#v, %v", result, err)
	}
	if result.ResultURL[:15] != "data:video/mp4;" {
		t.Fatalf("mock did not return embedded video: %q", result.ResultURL[:32])
	}
}
