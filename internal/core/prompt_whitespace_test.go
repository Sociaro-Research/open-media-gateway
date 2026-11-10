package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReviewedPromptWhitespaceSurvivesAdmissionAndHashing(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		m := MediaModel{ID: "binding-fixture", MediaType: "video", MinDuration: 1, MaxDuration: 15, DefaultDuration: 5, PromptPolicy: &PromptPolicy{MaxCharacters: 2048, PreserveWhitespace: preserve}}
		repo := &memoryRepository{}
		service := NewTaskServiceWithProviders(repo, &memoryQueue{}, map[string]MediaProvider{"catalog": catalogProvider{models: []MediaModel{m}}}, map[string]string{"catalog": m.ID}, "catalog")
		prompt := " @person holds @guitar "
		task, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{Model: m.ID, Prompt: prompt}, "binding-request")
		if err != nil {
			t.Fatal(err)
		}
		want := strings.TrimSpace(prompt)
		if preserve {
			want = prompt
		}
		if task.Prompt != want || repo.task.Prompt != want {
			t.Fatalf("prompt changed under preserve=%v", preserve)
		}
		_, err = service.CreateMediaRequest(context.Background(), CreateMediaInput{Model: m.ID, Prompt: strings.TrimSpace(prompt)}, "binding-request")
		if preserve && err == nil {
			t.Fatal("different binding separator reused the same idempotency intent")
		}
		if !preserve && err != nil {
			t.Fatal("legacy whitespace-normalized replay changed", err)
		}
	}
}

func TestReviewedWhitespacePolicyKeepsLengthAndBlankGuards(t *testing.T) {
	m := MediaModel{ID: "binding-fixture", MediaType: "video", MinDuration: 1, MaxDuration: 15, DefaultDuration: 5, PromptPolicy: &PromptPolicy{MaxCharacters: 2048, PreserveWhitespace: true}}
	provider := catalogProvider{models: []MediaModel{m}}
	service := NewTaskServiceWithProviders(&memoryRepository{}, &memoryQueue{}, map[string]MediaProvider{"catalog": provider}, map[string]string{"catalog": m.ID}, "catalog")
	if _, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{Model: m.ID, Prompt: "   "}, ""); !errors.Is(err, ErrPromptRequired) {
		t.Fatal("blank prompt bypassed required guard", err)
	}
	if _, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{Model: m.ID, Prompt: strings.Repeat("x", 2048) + " "}, ""); !errors.Is(err, ErrPromptTooLong) {
		t.Fatal("retained separator bypassed character limit", err)
	}
	manual := m
	manual.ID, manual.Manual = "manual-fixture", true
	service = NewTaskServiceWithProviders(&memoryRepository{}, &memoryQueue{}, map[string]MediaProvider{"catalog": catalogProvider{models: []MediaModel{manual}}}, map[string]string{"catalog": manual.ID}, "catalog")
	task, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{Model: manual.ID, Prompt: "  legacy  "}, "")
	if err != nil || task.Prompt != "legacy" {
		t.Fatal("manual model normalization changed", err)
	}
}
