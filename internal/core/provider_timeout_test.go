package core

import (
	"context"
	"errors"
	"testing"
)

func TestTimeoutIsDurableAdmissionInputAndIdempotency(t *testing.T) {
	repo := &memoryRepository{}
	s := NewTaskService(repo, &memoryQueue{}, "mock", "mock-video-v1")
	seconds := int64(600)
	in := CreateMediaInput{Prompt: "scene", Options: MediaOptions{TimeoutSeconds: &seconds}}
	first, err := s.CreateMediaRequest(context.Background(), in, "timeout-key")
	if err != nil {
		t.Fatal(err)
	}
	if first.Options.TimeoutSeconds == nil || *first.Options.TimeoutSeconds != 600 {
		t.Fatal(first)
	}
	other := int64(180)
	in.Options.TimeoutSeconds = &other
	if _, err = s.CreateMediaRequest(context.Background(), in, "timeout-key"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("timeout omitted from request identity", err)
	}
	for _, v := range []int64{0, -1, 9223372037} {
		in.Options.TimeoutSeconds = &v
		if _, err = s.CreateMediaRequest(context.Background(), in, ""); !errors.Is(err, ErrOptionsInvalid) {
			t.Fatal("invalid timeout accepted", v, err)
		}
	}
}
