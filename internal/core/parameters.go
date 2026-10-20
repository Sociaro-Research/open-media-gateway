package core

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
)

// ParameterField describes additive controls without adding vendor names to the API.
type ParameterField struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Type      string   `json:"type"`
	Required  bool     `json:"required,omitempty"`
	Default   any      `json:"default,omitempty"`
	Choices   []string `json:"choices,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	MaxLength int      `json:"max_length,omitempty"`
}

var parameterKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func NormalizeParameters(fields []ParameterField, values map[string]any) (map[string]any, error) {
	result := map[string]any{}
	known := map[string]bool{}
	for _, f := range fields {
		if !parameterKey.MatchString(f.Key) || known[f.Key] {
			return nil, fmt.Errorf("%w: invalid parameter schema", ErrOptionsInvalid)
		}
		known[f.Key] = true
		v, ok := values[f.Key]
		if !ok && f.Default != nil {
			v = f.Default
			ok = true
		}
		if !ok {
			if f.Required {
				return nil, fmt.Errorf("%w: parameter %s is required", ErrOptionsInvalid, f.Key)
			}
			continue
		}
		valid := false
		switch f.Type {
		case "text", "select":
			s, ok := v.(string)
			valid = ok && (f.MaxLength <= 0 || len([]rune(s)) <= f.MaxLength) && (!f.Required || s != "") && (f.Type != "select" || slices.Contains(f.Choices, s))
		case "boolean":
			_, valid = v.(bool)
		case "number", "integer":
			n, ok := parameterNumber(v)
			v = n
			valid = ok && !math.IsNaN(n) && !math.IsInf(n, 0) && (f.Min == nil || n >= *f.Min) && (f.Max == nil || n <= *f.Max) && (f.Type != "integer" || math.Trunc(n) == n)
		}
		if !valid {
			return nil, fmt.Errorf("%w: invalid parameter %s", ErrOptionsInvalid, f.Key)
		}
		result[f.Key] = v
	}
	for k := range values {
		if !known[k] {
			return nil, fmt.Errorf("%w: unsupported parameter %s", ErrOptionsInvalid, k)
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func parameterNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, e := n.Float64()
		return f, e == nil
	}
	return 0, false
}
