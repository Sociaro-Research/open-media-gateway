package openai

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
	"strings"
	"testing"
	"time"
)

func TestPromptCharacterLimitsReviewedModels(t *testing.T) {
	for _, id := range modelIDs {
		t.Run(id, func(t *testing.T) {
			m := describeModel(id)
			nativeLimit, effective := 4096, 4096
			if m.MediaType == "image" {
				nativeLimit, effective = 32000, 10000
			}
			if m.MaxPromptCharacters != effective {
				t.Fatal("wrong metadata", m.MaxPromptCharacters)
			}
			r := core.MediaRequest{Model: id, Prompt: strings.Repeat("😀", nativeLimit)}
			a := &Adapter{}
			if modelcatalog.IsInactive("openai", id, time.Now()) {
				if err := a.ValidateRequest(context.Background(), r); err == nil || !strings.Contains(err.Error(), "retired") {
					t.Fatal("retired-model rejection changed", err)
				}
				return
			}
			if err := a.ValidateRequest(context.Background(), r); err != nil {
				t.Fatal("native Unicode boundary changed", err)
			}
			r.Prompt += "界"
			if err := a.ValidateRequest(context.Background(), r); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal("excess native prompt accepted", err)
			}
		})
	}
	// Existing manual-mode bypass is not changed by metadata.
	if err := (&Adapter{}).ValidateRequest(core.WithManualCall(context.Background()), core.MediaRequest{Model: "operator-model", Prompt: strings.Repeat("😀", 10000)}); err != nil {
		t.Fatal(err)
	}
}
