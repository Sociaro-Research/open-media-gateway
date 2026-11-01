package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPromptCharacterLimitsEffective(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model MediaModel
		want  int
	}{
		{"unknown native bound", MediaModel{}, 10000},
		{"lower native bound", MediaModel{MaxPromptCharacters: 3072}, 3072},
		{"higher native bound", MediaModel{MaxPromptCharacters: 32000}, 10000},
		{"manual bypass", MediaModel{Manual: true, MaxPromptCharacters: 2000}, 10000},
		{"no prompt", MediaModel{NoPrompt: true, MaxPromptCharacters: 2000}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.model.EffectivePromptCharacterLimit(); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}

type promptLimitFreeProvider struct{ catalogProvider }

func (promptLimitFreeProvider) IsFree() bool { return true }

func TestPromptCharacterLimitsGatewayUnicodeAndRoutes(t *testing.T) {
	model := MediaModel{ID: "fixture", MediaType: "image"}
	svc := NewTaskServiceWithProviders(&memoryRepository{}, &memoryQueue{}, map[string]MediaProvider{"fixture": promptLimitFreeProvider{catalogProvider{models: []MediaModel{model}}}}, map[string]string{"fixture": model.ID}, "fixture")
	svc.EnableAccounting()
	for _, prompt := range []string{strings.Repeat("界", 10000), strings.Repeat("😀", 10000), "  " + strings.Repeat("😀", 10000) + "  ", strings.Repeat("e\u0301", 5000), strings.Repeat("😀", 10001)} {
		input := CreateMediaInput{Provider: "fixture", Model: model.ID, MediaType: "image", Prompt: prompt}
		over := len([]rune(strings.TrimSpace(prompt))) > 10000
		for _, action := range []string{"validate", "estimate", "create"} {
			var err error
			switch action {
			case "validate":
				err = svc.ValidateMediaRequest(context.Background(), input)
			case "estimate":
				_, err = svc.Estimate(context.Background(), input)
			case "create":
				_, err = svc.CreateMediaRequest(context.Background(), input, "")
			}
			if over {
				if !errors.Is(err, ErrPromptTooLong) {
					t.Fatalf("%s accepted excess Unicode prompt: %v", action, err)
				}
			} else if err != nil {
				t.Fatalf("%s counted bytes or UTF-16 units: %v", action, err)
			}
		}
	}
}
