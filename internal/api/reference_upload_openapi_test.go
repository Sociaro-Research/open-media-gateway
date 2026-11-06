package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/docs"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

func uploadSpecObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected OpenAPI object, got %T", value)
	}
	return object
}

func referenceUploadSpecification(t *testing.T) map[string]any {
	t.Helper()
	raw, err := docs.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestReferenceUploadOpenAPISizeCeilingIsConditional(t *testing.T) {
	spec := referenceUploadSpecification(t)
	paths := uploadSpecObject(t, spec["paths"])
	prepare := uploadSpecObject(t, uploadSpecObject(t, paths["/v1/references/uploads"])["post"])
	body := uploadSpecObject(t, prepare["requestBody"])
	content := uploadSpecObject(t, body["content"])
	request := uploadSpecObject(t, uploadSpecObject(t, content["application/json"])["schema"])
	requestSize := uploadSpecObject(t, uploadSpecObject(t, request["properties"])["size_bytes"])
	schemas := uploadSpecObject(t, uploadSpecObject(t, spec["components"])["schemas"])
	responseSize := uploadSpecObject(t, uploadSpecObject(t, uploadSpecObject(t, schemas["ReferenceImage"])["properties"])["size_bytes"])
	for name, size := range map[string]map[string]any{"prepare request": requestSize, "saved reference": responseSize} {
		if size["type"] != "integer" || size["minimum"] != float64(1) || size["maximum"] != float64(referencevalidation.HardVideoMaxBytes) {
			t.Errorf("%s size schema excludes supported extended video: %v", name, size)
		}
	}
	description, _ := prepare["description"].(string)
	for _, condition := range []string{"200 MiB", "300000000", "validation", "visible", "reviewed native", "manual", "reference_voice", "stricter"} {
		if !strings.Contains(strings.ToLower(description), strings.ToLower(condition)) {
			t.Errorf("prepare description must explain conditional video allowance: missing %q", condition)
		}
	}
	if referencevalidation.DefaultVideoMaxBytes != 200<<20 {
		t.Fatal("documented default video ceiling needs review")
	}
}

func TestReferenceUploadOpenAPIDocumentsUnavailableSelectedReference(t *testing.T) {
	// The real handler checks selected references before opening an upload. No
	// provider, remote storage request or uploaded file is needed for this path.
	for _, owner := range []string{"missing", "bob"} {
		t.Run(owner, func(t *testing.T) {
			s, _, service, local := templateFixture(t)
			id := "00000000-0000-4000-8000-000000000001"
			repo := &audioUploadRepository{referenceMemory: &referenceMemory{items: map[string]references.Image{}}, sessions: map[string]references.UploadSession{}}
			if owner != "missing" {
				repo.items[owner+"/"+id] = references.Image{ID: id}
			}
			s.references = references.New(repo, &audioUploadStore{Local: local})
			handler := s.handler()
			_, key := issueTestKey(t, handler, "alice", `{"name":"missing-reference-contract"}`)
			raw, err := json.Marshal(map[string]any{
				"name": "synthetic.png", "content_type": "image/png", "size_bytes": 1, "sha256": strings.Repeat("a", 64),
				"validation": &referencevalidation.Target{Provider: "mock", Model: service.catalog[0].Models[0].ID, ReferenceIDs: []string{id}},
			})
			if err != nil {
				t.Fatal(err)
			}
			out := sendTest(handler, "POST", "/v1/references/uploads", string(raw), "", key)
			if out.Code != 404 {
				t.Fatalf("unavailable selection: %d %s", out.Code, out.Body.String())
			}
			var envelope map[string]any
			if err = json.Unmarshal(out.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			failure := uploadSpecObject(t, envelope["error"])
			if failure["code"] != "not_found" {
				t.Fatal("unavailable reference changed its public error", failure)
			}
			for _, field := range []string{"message", "type", "param"} {
				if _, present := failure[field]; !present {
					t.Errorf("standard error envelope missing %s", field)
				}
			}
			if len(repo.sessions) != 0 || service.calls != 0 {
				t.Fatal("unavailable selection prepared an upload or generated media")
			}
		})
	}
	spec := referenceUploadSpecification(t)
	prepare := uploadSpecObject(t, uploadSpecObject(t, uploadSpecObject(t, spec["paths"])["/v1/references/uploads"])["post"])
	responses := uploadSpecObject(t, prepare["responses"])
	response := uploadSpecObject(t, responses["404"])
	if response["$ref"] != "#/components/responses/NotFound" {
		t.Fatal("prepare 404 must retain the standard error response", response)
	}
	description, _ := response["description"].(string)
	if !strings.Contains(description, "validation.reference_ids") || !strings.Contains(description, "not_found") {
		t.Fatal("prepare 404 must explain unavailable selected references and not_found", response)
	}
	shared := uploadSpecObject(t, uploadSpecObject(t, uploadSpecObject(t, spec["components"])["responses"])["NotFound"])
	content := uploadSpecObject(t, shared["content"])
	schema := uploadSpecObject(t, uploadSpecObject(t, content["application/json"])["schema"])
	if schema["$ref"] != "#/components/schemas/ErrorResponse" {
		t.Fatal("404 no longer documents the standard JSON error envelope", schema)
	}
}
