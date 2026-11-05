package core

import (
	"errors"
	"strings"
	"testing"
)

func TestParameterUnknownNamesStayPrivate(t *testing.T) {
	for _, key := range []string{"sk_live_private_value", "unrecognized_name", "strength"} {
		_, err := NormalizeParameters(nil, map[string]any{key: true})
		var detail *OptionValidationError
		if !errors.Is(err, ErrOptionsInvalid) || !errors.As(err, &detail) || detail.Field != "parameters" || !strings.Contains(detail.Hint, "omit undocumented fields") || strings.Contains(err.Error(), key) {
			t.Fatalf("unknown name escaped or lost safe correction: %v", err)
		}
	}
	// A declared public field still identifies the exact place to correct.
	_, err := NormalizeParameters([]ParameterField{{Key: "strength", Type: "number"}}, map[string]any{"strength": "private-input"})
	var detail *OptionValidationError
	if !errors.As(err, &detail) || detail.Field != "parameters.strength" || !strings.Contains(detail.Hint, "JSON number") {
		t.Fatal("declared field lost its useful diagnostic", err)
	}
}

func TestParameterUncorrectableSchemasIdentifyOperatorAction(t *testing.T) {
	longKey := strings.Repeat("x", 65)
	for _, tc := range []struct {
		name   string
		field  ParameterField
		values map[string]any
	}{
		{"required long key", ParameterField{Key: longKey, Type: "text", Required: true}, nil},
		{"invalid long key value", ParameterField{Key: longKey, Type: "number"}, map[string]any{longKey: "private-input"}},
		{"empty choices", ParameterField{Key: "style", Type: "select"}, map[string]any{"style": "private-input"}},
		{"missing required empty choices", ParameterField{Key: "style", Type: "select", Required: true}, nil},
		{"empty required value without choices", ParameterField{Key: "style", Type: "select", Required: true}, map[string]any{"style": ""}},
		{"wrong type without choices", ParameterField{Key: "style", Type: "select"}, map[string]any{"style": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeParameters([]ParameterField{tc.field}, tc.values)
			var detail *OptionValidationError
			if !errors.Is(err, ErrOptionsInvalid) || !errors.As(err, &detail) || detail.Field != "parameters" || !strings.Contains(detail.Hint, "operator") || strings.Contains(err.Error(), "private-input") || strings.Contains(err.Error(), longKey) {
				t.Fatalf("uncorrectable schema needs safe operator guidance: %v", err)
			}
		})
	}
	// Diagnostic bounds do not become new acceptance limits.
	if _, err := NormalizeParameters([]ParameterField{{Key: longKey, Type: "text", Required: true}}, map[string]any{longKey: "accepted"}); err != nil {
		t.Fatal("diagnostic bound changed acceptance", err)
	}
	if _, err := NormalizeParameters([]ParameterField{{Key: "style", Type: "select"}}, nil); err != nil {
		t.Fatal("absent optional schema became an admission failure", err)
	}
	_, err := NormalizeParameters([]ParameterField{{Key: "style", Type: "select", Required: true, Choices: []string{"ink"}}}, nil)
	var detail *OptionValidationError
	if !errors.As(err, &detail) || detail.Field != "parameters.style" || !strings.Contains(detail.Hint, "required") {
		t.Fatal("valid required select lost its caller correction", err)
	}
}
