package templates

import (
	"errors"
	"strings"
	"testing"
)

func TestSlotLabelUnicodeCharacterLimit(t *testing.T) {
	for _, tc := range []struct {
		name, label string
		invalid     bool
	}{
		{"ASCII120", strings.Repeat("a", 120), false},
		{"ASCII121", strings.Repeat("a", 121), true},
		{"Cyrillic70", strings.Repeat("я", 70), false},
		{"Cyrillic120", strings.Repeat("я", 120), false},
		{"Cyrillic121", strings.Repeat("я", 121), true},
		{"mixed120", strings.Repeat("aя😀", 40), false},
		{"mixed121", strings.Repeat("aя😀", 40) + "z", true},
		{"supplementary120", strings.Repeat("😀", 120), false},
		{"supplementary121", strings.Repeat("😀", 121), true},
		// Count code points consistently with other template fields, not graphemes.
		{"combining120", strings.Repeat("e\u0301", 60), false},
		{"blank", " \t\n", true},
		{"UnicodeBlankNEL", "\u0085", true},
		{"UnicodeBlankNBSP", "\u00a0", true},
		{"BOMIsNotWhitespace", "\ufeff", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := danceModel()
			d := Definition{Title: "Labels", MediaType: "video", Prompt: "Image 1 and Image 2", Syntax: "numbered"}
			d.Options.ReferenceMode = "references"
			d.Slots, _ = d.InferSlots(model)
			d.Slots[1].Label = tc.label
			err := d.Validate(model)
			if !tc.invalid && err != nil {
				t.Fatalf("valid Unicode label rejected: %v", err)
			}
			if tc.invalid && (!errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "slots[1].label") || strings.Contains(err.Error(), "incorrectly bound")) {
				t.Fatalf("expected actionable label error at slots[1].label, got %v", err)
			}
			if d.Slots[1].Label != tc.label {
				t.Fatal("validation normalized or truncated the label")
			}
		})
	}
}

func TestSlotLabelDoesNotConcealBindingErrors(t *testing.T) {
	for _, change := range []func(*Slot){
		func(s *Slot) { s.ID = "image:1" },
		func(s *Slot) { s.Type = "video" },
		func(s *Slot) { s.Index = 1 },
		func(s *Slot) { s.Role = "first_frame" },
	} {
		model := danceModel()
		d := Definition{Title: "Labels", MediaType: "video", Prompt: "Image 1 and Image 2", Syntax: "numbered"}
		d.Options.ReferenceMode = "references"
		d.Slots, _ = d.InferSlots(model)
		d.Slots[0].Label = strings.Repeat("x", 121)
		change(&d.Slots[1])
		if err := d.Validate(model); err == nil || !strings.Contains(err.Error(), "duplicate, reordered or incorrectly bound slots") {
			t.Fatalf("binding diagnostic was replaced by a label diagnostic: %v", err)
		}
	}
}
