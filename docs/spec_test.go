package docs

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSpecificationReferencesResolve(t *testing.T) {
	data, err := JSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				var target any = spec
				for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
					obj, ok := target.(map[string]any)
					if !ok {
						t.Fatalf("broken reference %s", ref)
					}
					target = obj[part]
				}
				if target == nil {
					t.Fatalf("missing reference %s", ref)
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(spec)
	if spec["openapi"] != "3.1.0" {
		t.Fatal("unexpected OpenAPI version")
	}
}
