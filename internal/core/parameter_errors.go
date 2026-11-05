package core

import (
	"fmt"
	"math"
	"strings"
)

func parameterSchemaError() error {
	return optionError("parameters", "has an invalid model schema; ask an operator to check the parameter configuration")
}

func parameterError(key, hint string) error {
	// Only declared schema keys reach this helper. A diagnostic cannot render an
	// unbounded or unsafe schema key, and omitting a required key cannot fix it.
	if len(key) > 64 || !parameterKey.MatchString(key) {
		return parameterSchemaError()
	}
	return optionError("parameters."+key, hint)
}

// Diagnostic choices come from the model schema, never the submitted value.
// Bound their rendering and suppress URLs, controls and other free-form text.
func parameterChoices(choices []string) (string, bool) {
	if len(choices) == 0 || len(choices) > 32 {
		return "", false
	}
	for _, choice := range choices {
		if len(choice) == 0 || len(choice) > 64 {
			return "", false
		}
		for _, r := range choice {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(" _.-", r)) {
				return "", false
			}
		}
	}
	text := strings.Join(choices, ", ")
	return text, len(text) <= 512
}

// Called only after the unchanged validator rejects a value. Diagnosing an
// invalid schema here must not change which optional fields/defaults are used.
func invalidParameterValue(f ParameterField, value any) error {
	if f.Type == "select" && len(f.Choices) == 0 {
		return parameterSchemaError()
	}
	bad := func(hint string) error { return parameterError(f.Key, hint) }
	length := func(n int) string {
		return fmt.Sprintf("has %d characters; maximum %d. Shorten the value", n, f.MaxLength)
	}
	switch f.Type {
	case "text", "select":
		s, ok := value.(string)
		if !ok {
			return bad("must be a string; supply a JSON string")
		}
		if f.MaxLength > 0 && len([]rune(s)) > f.MaxLength {
			return bad(length(len([]rune(s))))
		}
		if f.Required && s == "" {
			return bad("is required; supply a nonempty string")
		}
		if f.Type == "select" {
			if choices, ok := parameterChoices(f.Choices); ok {
				return bad("must be one of: " + choices + ". Choose a supported value")
			}
			return bad("must be a documented choice; choose a value from the selected model's parameter schema")
		}
	case "text_list":
		if f.MaxItems <= 0 || f.MaxItems > 64 {
			return parameterSchemaError()
		}
		var items []any
		switch list := value.(type) {
		case []any:
			items = list
		case []string:
			for _, s := range list {
				items = append(items, s)
			}
		default:
			return bad("must be a list of strings; supply a JSON array of strings")
		}
		if len(items) > f.MaxItems {
			return bad(fmt.Sprintf("has %d items; supply at most %d items", len(items), f.MaxItems))
		}
		if f.Required && len(items) == 0 {
			return bad("is required; supply at least one string")
		}
		for i, item := range items {
			s, ok := item.(string)
			hint := ""
			if !ok {
				hint = "must be a string; replace this item with a JSON string"
			} else if f.MaxLength > 0 && len([]rune(s)) > f.MaxLength {
				hint = length(len([]rune(s)))
			}
			if hint != "" {
				if len(f.Key) > 64 || !parameterKey.MatchString(f.Key) {
					return bad(hint)
				}
				return optionError(fmt.Sprintf("parameters.%s.%d", f.Key, i), hint)
			}
		}
	case "boolean":
		return bad("must be a boolean; supply true or false without quotes")
	case "number", "integer":
		n, ok := parameterNumber(value)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return bad("must be a finite number; supply a JSON number without quotes")
		}
		if f.Min != nil && (math.IsNaN(*f.Min) || math.IsInf(*f.Min, 0)) || f.Max != nil && (math.IsNaN(*f.Max) || math.IsInf(*f.Max, 0)) || f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			return parameterSchemaError()
		}
		if f.Min != nil && n < *f.Min || f.Max != nil && n > *f.Max {
			kind := "a number"
			if f.Type == "integer" {
				kind = "an integer"
			}
			if f.Min != nil && f.Max != nil {
				return bad(fmt.Sprintf("must be %s between %g and %g inclusive; choose a value in this range", kind, *f.Min, *f.Max))
			}
			if f.Min != nil && n < *f.Min {
				return bad(fmt.Sprintf("must be %s at least %g; increase the value", kind, *f.Min))
			}
			return bad(fmt.Sprintf("must be %s at most %g; decrease the value", kind, *f.Max))
		}
		if f.Type == "integer" && math.Trunc(n) != n {
			return bad("must be an integer; supply a whole number without a fractional part")
		}
	}
	return parameterSchemaError()
}
