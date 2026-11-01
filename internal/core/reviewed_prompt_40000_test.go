package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExplicitReviewedFortyThousandPolicyDoesNotBroadenOtherModels(t *testing.T) {
	explicit := MediaModel{ID: "reviewed-regeneration", MediaType: "video", MaxPromptCharacters: 40000, PromptPolicy: &PromptPolicy{MaxCharacters: 40000}}
	ordinary := MediaModel{ID: "ordinary", MediaType: "video"}
	undeclared := MediaModel{ID: "undeclared", MediaType: "video", MaxPromptCharacters: 40000}
	manual := explicit
	manual.ID = "manual"
	manual.Manual = true
	models := []MediaModel{explicit, ordinary, undeclared, manual}
	provider := catalogProvider{models: models}
	repo := &memoryRepository{}
	queue := &memoryQueue{}
	svc := NewTaskServiceWithProviders(repo, queue, map[string]MediaProvider{"catalog": provider}, map[string]string{"catalog": explicit.ID}, "catalog")
	for _, prompt := range []string{strings.Repeat("x", 40000), strings.Repeat("界", 40000), strings.Repeat("😀", 39999) + "z"} {
		task, err := svc.CreateMediaRequest(context.Background(), CreateMediaInput{Model: explicit.ID, Prompt: prompt}, "")
		if err != nil || task.Prompt != prompt {
			t.Fatal("reviewed boundary rejected", err)
		}
	}
	if _, err := svc.CreateMediaRequest(context.Background(), CreateMediaInput{Model: explicit.ID, Prompt: strings.Repeat("x", 40001)}, ""); !errors.Is(err, ErrPromptTooLong) {
		t.Fatal("reviewed ceiling exceeded", err)
	}
	for _, model := range []MediaModel{ordinary, undeclared, manual} {
		if got := model.EffectivePromptCharacterLimit(); got != MaxPromptCharacters {
			t.Fatal("unrelated ceiling broadened", model.ID, got)
		}
		if _, err := svc.CreateMediaRequest(context.Background(), CreateMediaInput{Model: model.ID, Prompt: strings.Repeat("x", 10001)}, ""); !errors.Is(err, ErrPromptTooLong) {
			t.Fatal("ordinary/manual model broadened", model.ID, err)
		}
	}
	invalid := explicit
	invalid.PromptPolicy = &PromptPolicy{MaxCharacters: 40001}
	if err := ValidatePromptPolicy(invalid, "short"); !errors.Is(err, ErrOptionsInvalid) {
		t.Fatal("unreviewed ceiling accepted", err)
	}
}
