package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProjectNameAndSubmissionIdempotency(t *testing.T) {
	for _, name := range []string{"", " \t", strings.Repeat("界", 81), "line\nbreak", "x\x00"} {
		if _, err := ProjectName(name); !errors.Is(err, ErrProjectName) {
			t.Fatalf("accepted %q: %v", name, err)
		}
	}
	if name, err := ProjectName("  Проект 🎬  "); err != nil || name != "Проект 🎬" {
		t.Fatal(name, err)
	}
	repo := &memoryRepository{}
	s := NewTaskService(repo, &memoryQueue{}, "mock", "mock-video-v1")
	input := CreateMediaInput{Prompt: "scene", ProjectID: "11111111-1111-4111-8111-111111111111"}
	first, err := s.CreateMediaRequest(context.Background(), input, "same")
	if err != nil || first.ProjectID != input.ProjectID {
		t.Fatal(first, err)
	}
	retry, err := s.CreateMediaRequest(context.Background(), input, "same")
	if err != nil || retry.ID != first.ID {
		t.Fatal(retry, err)
	}
	input.ProjectID = "22222222-2222-4222-8222-222222222222"
	if _, err := s.CreateMediaRequest(context.Background(), input, "same"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("project switch reused submission", err)
	}
	input.ProjectID = "all"
	if _, err := s.CreateMediaRequest(context.Background(), input, ""); !errors.Is(err, ErrProjectInvalid) {
		t.Fatal("All treated as real project", err)
	}
}
