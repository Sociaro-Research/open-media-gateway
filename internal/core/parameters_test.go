package core

import (
	"reflect"
	"testing"
)

func TestParameterSchemaValidationAndDefaults(t *testing.T) {
	min, max := 0.0, 1.0
	fields := []ParameterField{{Key: "strength", Type: "number", Default: 0.5, Min: &min, Max: &max}, {Key: "style", Type: "select", Choices: []string{"ink", "oil"}, Required: true}, {Key: "smooth", Type: "boolean"}}
	for _, v := range []map[string]any{{"style": "watercolor"}, {"style": "ink", "strength": 2.0}, {"style": "ink", "strength": "0.5"}, {"style": "ink", "unknown": true}, {"smooth": true}, {"style": "ink", "smooth": "false"}} {
		if _, err := NormalizeParameters(fields, v); err == nil {
			t.Fatalf("accepted %+v", v)
		}
	}
	input := map[string]any{"style": "ink", "smooth": false}
	out, err := NormalizeParameters(fields, input)
	if err != nil || out["strength"] != 0.5 || !reflect.DeepEqual(input, map[string]any{"style": "ink", "smooth": false}) {
		t.Fatal(out, err)
	}
}
