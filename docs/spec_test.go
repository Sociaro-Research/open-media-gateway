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

func TestResponseObjectsPreserveDescriptions(t *testing.T) {
	data, err := JSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	check := func(name string, value any) {
		t.Run(name, func(t *testing.T) {
			response := value.(map[string]any)
			_, reference := response["$ref"]
			if !reference {
				description, ok := response["description"].(string)
				if !ok || strings.TrimSpace(description) == "" {
					t.Error("Response Object requires a nonempty string description")
				}
			}
			for field := range response {
				if strings.HasPrefix(field, "x-") {
					continue
				}
				if reference {
					if field != "$ref" && field != "summary" && field != "description" {
						t.Errorf("unexpected Reference Object field %q", field)
					}
					continue
				}
				if field != "description" && field != "headers" && field != "content" && field != "links" {
					t.Errorf("unexpected Response Object field %q; quote punctuation in flow descriptions", field)
				}
			}
		})
	}
	paths := spec["paths"].(map[string]any)
	for path, value := range paths {
		item := value.(map[string]any)
		for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
			operation, ok := item[method].(map[string]any)
			if !ok {
				continue
			}
			for status, response := range operation["responses"].(map[string]any) {
				check(method+" "+path+" "+status, response)
			}
		}
	}
	components := spec["components"].(map[string]any)
	for name, response := range components["responses"].(map[string]any) {
		check("components.responses."+name, response)
	}
	for _, example := range []struct{ path, status, description string }{
		{"/v1/playground-key/create", "503", "Encryption, authentication or storage unavailable; no automatic replacement."},
		{"/v1/references/validate", "200", "References are valid, body contains valid=true"},
		{"/v1/references/validate", "400", "Invalid reference with the affected file, actual value, allowed bound and corrective action"},
	} {
		operation := paths[example.path].(map[string]any)["post"].(map[string]any)
		response := operation["responses"].(map[string]any)[example.status].(map[string]any)
		if response["description"] != example.description {
			t.Errorf("%s %s description = %q, want %q", example.path, example.status, response["description"], example.description)
		}
	}
}

func TestModelIdentityDescriptionsSeparatePublicAndReferenceIDs(t *testing.T) {
	data, err := JSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]any)
	for _, path := range []string{"/v1/models", "/v1/models/{model}"} {
		operation := paths[path].(map[string]any)["get"].(map[string]any)
		description := operation["description"].(string)
		if !strings.Contains(description, "exact public IDs") || strings.Contains(description, "Raw unique model IDs") {
			t.Errorf("%s must describe exact public identities, including renamed IDs", path)
		}
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	generation := schemas["GenerationRequest"].(map[string]any)["properties"].(map[string]any)["model"].(map[string]any)["description"].(string)
	reference := schemas["ReferenceValidationTarget"].(map[string]any)["properties"].(map[string]any)["model"].(map[string]any)["description"].(string)
	if !strings.Contains(generation, "Exact public ID") || strings.Contains(generation, "legacy native") {
		t.Error("Generation model must remain an exact public ID")
	}
	if !strings.Contains(reference, "legacy native model ID on this provider connection") || !strings.Contains(reference, "Ambiguous matches are rejected") {
		t.Error("Reference validation alone retains connection-scoped legacy IDs without ambiguous aliases")
	}
}
