package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/docs"
)

func TestVideoMultipartSchemaModelsScalarAndFileAlternatives(t *testing.T) {
	raw, err := docs.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	schema := imageContractValue(spec, "paths", "/v1/videos", "post", "requestBody", "content", "multipart/form-data", "schema")
	if !imageContractRequires(schema, "model") || imageContractRequires(schema, "input_reference") || imageContractRequires(schema, "input_reference[]") {
		t.Error("model required; reference absence remains model-dependent")
	}
	plain := imageContractValue(schema, "properties", "input_reference")
	branches, _ := imageContractValue(plain, "anyOf").([]any)
	file, text := false, false
	for _, branch := range branches {
		if imageContractValue(branch, "minLength") != float64(1) {
			t.Error("every string alternative must reject empty values despite format annotations")
		}
		if imageContractValue(branch, "type") != "string" {
			t.Error("plain field must be a single string/file,never an array")
		}
		file = file || imageContractValue(branch, "format") == "binary"
		text = text || (imageContractValue(branch, "format") == nil && imageContractValue(branch, "minLength") == float64(1))
	}
	if !file || !text {
		t.Error("input_reference must describe binary file or scalar text")
	}
	alias := imageContractValue(schema, "properties", "input_reference[]")
	if imageContractValue(alias, "type") != "array" || imageContractValue(alias, "minItems") != float64(1) || imageContractValue(alias, "maxItems") != float64(1) || imageContractValue(alias, "items", "format") != "binary" || imageContractValue(alias, "items", "minLength") != float64(1) {
		t.Error("bracketed alias must describe exactly one file")
	}
	required, _ := imageContractValue(schema, "not", "required").([]any)
	if len(required) != 2 || !slices.Contains(required, any("input_reference")) || !slices.Contains(required, any("input_reference[]")) {
		t.Error("the two video fields must not coexist")
	}
}

func TestVideoMultipartReferenceTextStaysText(t *testing.T) {
	// Test only serialization; string parsing alone does not establish admission.
	for _, value := range []string{"https://fixture.invalid/a.png", `{"file_id":"00000000-0000-4000-8000-000000000011"}`, `["https://fixture.invalid/a.png"]`, "null"} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		if err := form.WriteField("input_reference", value); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/v1/videos", &body)
		r.Header.Set("Content-Type", form.FormDataContentType())
		out := httptest.NewRecorder()
		fields, files, ok := videoMultipartFields(out, r)
		if !ok || files {
			t.Fatal(ok, files, out.Code, out.Body)
		}
		var decoded string
		if err := json.Unmarshal(fields["input_reference"], &decoded); err != nil || decoded != value {
			t.Fatal("scalar was JSON-decoded as a reference object/array", string(fields["input_reference"]), err)
		}
	}
}
