package sonilo

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"strings"
	"testing"
)

func TestPromptCharacterLimitsNativeOperations(t *testing.T) {
	for _, op := range operations {
		t.Run(op.id, func(t *testing.T) {
			m := modelFor(op.id)
			r := requestFor(op)
			a := &Adapter{}
			if m.NoPrompt {
				if m.EffectivePromptCharacterLimit() != 0 || m.MaxPromptCharacters != 0 {
					t.Fatal("non-applicable metadata")
				}
				r.Prompt = "unexpected"
				if err := a.ValidateRequest(context.Background(), r); !errors.Is(err, core.ErrOptionsInvalid) {
					t.Fatal("no-prompt contract changed", err)
				}
				return
			}
			if m.MaxPromptCharacters != 2000 {
				t.Fatal("wrong limit", m.MaxPromptCharacters)
			}
			r.Prompt = strings.Repeat("😀", 2000)
			if err := a.ValidateRequest(context.Background(), r); err != nil {
				t.Fatal("Unicode boundary rejected", err)
			}
			r.Prompt += "界"
			if err := a.ValidateRequest(context.Background(), r); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal("excess prompt accepted", err)
			}
		})
	}
}
