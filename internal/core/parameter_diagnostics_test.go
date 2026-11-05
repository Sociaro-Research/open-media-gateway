package core

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestParameterDiagnosticsConstraintsAndPrivacy(t *testing.T) {
	min, max := 0.0, 1.0
	for _, tc := range []struct {
		name       string
		field      ParameterField
		value      any
		path, hint string
	}{
		{"type", ParameterField{Key: "strength", Type: "number", Min: &min, Max: &max}, "private-request-value", "parameters.strength", "number"},
		{"range", ParameterField{Key: "strength", Type: "number", Min: &min, Max: &max}, 2.0, "parameters.strength", "between 0 and 1"},
		{"integer", ParameterField{Key: "steps", Type: "integer"}, 1.5, "parameters.steps", "integer"},
		{"nonfinite", ParameterField{Key: "strength", Type: "number"}, math.Inf(1), "parameters.strength", "finite number"},
		{"choices", ParameterField{Key: "style", Type: "select", Choices: []string{"ink", "oil"}}, "private-request-value", "parameters.style", "ink, oil"},
		{"length", ParameterField{Key: "caption", Type: "text", MaxLength: 2}, "秘密文", "parameters.caption", "3 characters"},
		{"count", ParameterField{Key: "tags", Type: "text_list", MaxItems: 2}, []string{"a", "b", "c"}, "parameters.tags", "at most 2 items"},
		{"item type", ParameterField{Key: "tags", Type: "text_list", MaxItems: 2}, []any{"ok", 7}, "parameters.tags.1", "string"},
		{"item length", ParameterField{Key: "tags", Type: "text_list", MaxItems: 2, MaxLength: 2}, []string{"ok", "秘密文"}, "parameters.tags.1", "3 characters"},
		{"boolean", ParameterField{Key: "smooth", Type: "boolean"}, "private-request-value", "parameters.smooth", "boolean"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeParameters([]ParameterField{tc.field}, map[string]any{tc.field.Key: tc.value})
			var detail *OptionValidationError
			if !errors.Is(err, ErrOptionsInvalid) || !errors.As(err, &detail) || detail.Field != tc.path || !strings.Contains(detail.Hint, tc.hint) {
				t.Fatalf("diagnostic: %v", err)
			}
			if strings.Contains(err.Error(), "private-request-value") || strings.Contains(err.Error(), "秘密文") {
				t.Fatal("reflected rejected content", err)
			}
		})
	}
	for _, key := range []string{"unknown", "private-request-value\nhttps://secret.example/", strings.Repeat("x", 10000)} {
		_, err := NormalizeParameters(nil, map[string]any{key: true})
		var detail *OptionValidationError
		if !errors.As(err, &detail) || !strings.Contains(detail.Hint, "omit") || len(err.Error()) > 1024 || strings.Contains(err.Error(), "private-request-value") {
			t.Fatalf("unknown field: %v", err)
		}
	}
	_, err := NormalizeParameters([]ParameterField{{Key: "style", Type: "text", Required: true}}, nil)
	var detail *OptionValidationError
	if !errors.As(err, &detail) || detail.Field != "parameters.style" || !strings.Contains(detail.Hint, "required") {
		t.Fatal(err)
	}
}

func TestPromptDiagnosticsMeasuredUnicodeAndBudget(t *testing.T) {
	model := MediaModel{MaxPromptCharacters: 10}
	for _, length := range []int{11, 50} {
		err := ValidatePromptPolicy(model, strings.Repeat("秘", length))
		if !errors.Is(err, ErrPromptTooLong) || !strings.Contains(err.Error(), "maximum 10") || !strings.Contains(err.Error(), "Shorten") || strings.Contains(err.Error(), "秘") {
			t.Fatal(err)
		}
	}
	if err := ValidatePromptPolicy(model, strings.Repeat("😀", 10)); err != nil {
		t.Fatal(err)
	}
	mixed := MediaModel{PromptPolicy: &PromptPolicy{MaxCharacters: 20, ASCIIBudget: 10, NonASCIIBudget: 5}}
	if err := ValidatePromptPolicy(mixed, "aaaaaa界界"); err != nil {
		t.Fatal(err)
	}
	err := ValidatePromptPolicy(mixed, "aaaaaaa界界")
	if !errors.Is(err, ErrPromptTooLong) || !strings.Contains(err.Error(), "7 ASCII") || !strings.Contains(err.Error(), "2 non-ASCII") || !strings.Contains(err.Error(), "ASCII/10 + non-ASCII/5") {
		t.Fatal(err)
	}
}

func TestParameterDiagnosticPreservesBoundariesDefaultsAndSchemas(t *testing.T) {
	min, max := 0.0, 1.0
	for _, v := range []any{0.0, 1.0, 0.5, int(1), int64(0), json.Number("0.5")} {
		out, err := NormalizeParameters([]ParameterField{{Key: "strength", Type: "number", Min: &min, Max: &max}}, map[string]any{"strength": v})
		if err != nil || out["strength"] == nil {
			t.Fatal(out, err)
		}
	}
	for _, v := range []any{-0.01, 1.01, float32(0.5), json.Number("bad"), nil, math.NaN()} {
		if _, err := NormalizeParameters([]ParameterField{{Key: "strength", Type: "number", Min: &min, Max: &max}}, map[string]any{"strength": v}); !errors.Is(err, ErrOptionsInvalid) {
			t.Fatal("changed rejection", v, err)
		}
	}
	// Absent optional invalid schemas and an unlimited text length historically
	// do not reject admission. Diagnosis must not introduce eager schema checks.
	if out, err := NormalizeParameters([]ParameterField{{Key: "absent", Type: "unknown"}, {Key: "text", Type: "text", MaxLength: -1, Default: "ok"}}, nil); err != nil || out["text"] != "ok" {
		t.Fatal(out, err)
	}
	for _, field := range []ParameterField{{Key: "tags", Type: "text_list", MaxItems: 65}, {Key: "bad\nkey", Type: "text"}, {Key: "field", Type: "unknown"}} {
		_, err := NormalizeParameters([]ParameterField{field}, map[string]any{field.Key: "private-input"})
		if !errors.Is(err, ErrOptionsInvalid) || !strings.Contains(err.Error(), "operator") || strings.Contains(err.Error(), "private-input") {
			t.Fatal(err)
		}
	}
	_, err := NormalizeParameters([]ParameterField{{Key: "style", Type: "select", Choices: []string{"https://secret.example/token"}}}, map[string]any{"style": "bad"})
	if strings.Contains(err.Error(), "secret.example") {
		t.Fatal(err)
	}
}
