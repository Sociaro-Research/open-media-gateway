package core

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceFailureRejectsNewAdmissionButPreservesAcceptedRetry(t *testing.T) {
	repo := &memoryRepository{}
	queue := &memoryQueue{}
	service := NewTaskService(repo, queue, "mock", "mock-video-v1")
	broken := errors.New("reference no longer available")
	input := CreateMediaInput{Prompt: "Synthetic", AdmissionError: broken}
	if _, err := service.CreateMediaRequest(context.Background(), input, "first"); !errors.Is(err, broken) || repo.createCount != 0 || queue.id != "" {
		t.Fatal("new invalid work admitted", err, repo.createCount)
	}
	input.AdmissionError = nil
	accepted, err := service.CreateMediaRequest(context.Background(), input, "first")
	if err != nil {
		t.Fatal(err)
	}
	input.AdmissionError = broken
	retry, err := service.CreateMediaRequest(context.Background(), input, "first")
	if err != nil || retry.ID != accepted.ID || repo.createCount != 1 {
		t.Fatal("accepted retry depends on mutable reference", err)
	}
	input.Prompt = "Changed"
	if _, err = service.CreateMediaRequest(context.Background(), input, "first"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("changed request bypassed conflict", err)
	}
}
