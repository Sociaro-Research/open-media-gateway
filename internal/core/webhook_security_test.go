package core

import (
	"context"
	"errors"
	"testing"
)

func TestPrivateWebhookRejectedBeforeTaskAcceptance(t *testing.T) {
	for _, url := range []string{"http://127.0.0.1/admin", "http://169.254.169.254/", "http://[::ffff:10.0.0.1]/", "http://localhost/hook"} {
		t.Run(url, func(t *testing.T) {
			repo, queue := &memoryRepository{}, &memoryQueue{}
			service := NewTaskService(repo, queue, "mock", "mock-video-v1")
			_, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{Prompt: "test", Provider: "", Model: "", MediaType: "video", WebhookURL: url}, "")
			if !errors.Is(err, ErrWebhookInvalid) || repo.createCount != 0 || queue.id != "" {
				t.Fatalf("private webhook admitted: err=%v tasks=%d queued=%s", err, repo.createCount, queue.id)
			}
		})
	}
}
