package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/docs"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

func imageContractValue(value any, keys ...string) any {
	for _, key := range keys {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}

func imageContractRequires(value any, name string) bool {
	required, _ := imageContractValue(value, "required").([]any)
	for _, field := range required {
		if field == name {
			return true
		}
	}
	return false
}

func TestImageInputSchemaMatchesEditRepresentations(t *testing.T) {
	raw, err := docs.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	content := imageContractValue(spec, "paths", "/v1/images/edits", "post", "requestBody", "content")
	jsonSchema := imageContractValue(content, "application/json", "schema")
	all, _ := imageContractValue(jsonSchema, "allOf").([]any)
	hasBase, hasImage := false, false
	for _, branch := range all {
		hasBase = hasBase || imageContractValue(branch, "$ref") == "#/components/schemas/GenerationRequest"
		hasImage = hasImage || imageContractRequires(branch, "image")
	}
	if !hasBase || !hasImage {
		t.Error("JSON edits must retain GenerationRequest and require image")
	}
	multipart := imageContractValue(content, "multipart/form-data", "schema")
	anyOf, _ := imageContractValue(multipart, "anyOf").([]any)
	plain, brackets := false, false
	for _, branch := range anyOf {
		if imageContractRequires(branch, "image") && imageContractRequires(branch, "image[]") {
			t.Error("each file alternative must work without the other spelling")
		}
		plain = plain || imageContractRequires(branch, "image")
		brackets = brackets || imageContractRequires(branch, "image[]")
	}
	if !imageContractRequires(multipart, "model") || imageContractRequires(multipart, "image") || !plain || !brackets || imageContractValue(multipart, "oneOf") != nil {
		t.Error("multipart must require model and accept either or both file spellings")
	}
	image := imageContractValue(multipart, "properties", "image")
	variants, _ := imageContractValue(image, "oneOf").([]any)
	single, repeated := false, false
	for _, branch := range variants {
		single = single || imageContractValue(branch, "format") == "binary"
		repeated = repeated || imageContractValue(branch, "type") == "array"
	}
	if !single || !repeated {
		t.Error("plain image must describe single and repeated file parts")
	}
	// All six string positions reject empty values, including nested objects.
	stringsChecked := 0
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if v["type"] == "string" {
				stringsChecked++
				if v["minLength"] != float64(1) {
					t.Error("image-input strings must be nonempty", v)
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
	walk(imageContractValue(spec, "components", "schemas", "GenerationImageInput"))
	if stringsChecked != 6 {
		t.Fatal("review image input schema branches", stringsChecked)
	}
}

func TestImageReferenceParserShapesAndNamespaces(t *testing.T) {
	local, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	const owned = "00000000-0000-4000-8000-000000000011"
	const shared = "00000000-0000-4000-8000-000000000012"
	const foreign = "00000000-0000-4000-8000-000000000013"
	const video = "00000000-0000-4000-8000-000000000014"
	repo := &referenceMemory{items: map[string]references.Image{
		"alice/" + owned:                        {ID: owned, ContentType: "image/png"},
		templates.LibraryOwnerID + "/" + shared: {ID: shared, ContentType: "image/png"},
		"bob/" + foreign:                        {ID: foreign, ContentType: "image/png"},
		"alice/" + video:                        {ID: video, ContentType: "video/mp4"},
	}}
	library := references.New(repo, local)
	s := &Server{references: library, templateReferences: library, mediaSigner: newMediaURLSigner("fixture-secret", time.Hour)}
	r := httptest.NewRequest("POST", "https://fixture.invalid/v1/images/edits", nil)
	r = r.WithContext(identity.WithOwnerID(r.Context(), "alice"))
	for _, tc := range []struct {
		name, raw string
		count     int
	}{
		{"url", `"https://fixture.invalid/a.png"`, 1},
		{"url_object", `{"image_url":"https://fixture.invalid/a.png"}`, 1},
		{"mixed_order", `["https://fixture.invalid/a.png",{"image_url":"https://fixture.invalid/b.png"}]`, 2},
		{"opaque_asset", `"asset://registered"`, 1}, // Model validation, not this parser, gates asset capabilities.
		{"owned", `{"file_id":"` + owned + `"}`, 1},
		{"shared", `{"file_id":"` + shared + `"}`, 1},
		{"mixed_files", `[{"file_id":"` + owned + `"},{"file_id":"` + shared + `"}]`, 2},
		{"null", `null`, 0}, {"empty_string", `""`, 0}, {"empty_array", `[]`, 0},
		{"nested_array", `[["https://fixture.invalid/a.png"]]`, 0},
		{"empty_url", `{"image_url":""}`, 0}, {"null_url", `{"image_url":null}`, 0},
		{"numeric_url", `{"image_url":3}`, 0}, {"numeric_id", `{"file_id":3}`, 0},
		{"empty_id", `{"file_id":""}`, 0}, {"null_id", `{"file_id":null}`, 0},
		{"empty_object", `{}`, 0}, {"empty_item", `[""]`, 0},
		{"bare_id_is_opaque", `"` + owned + `"`, 1},
		{"both_keys", `{"image_url":"https://fixture.invalid/a.png","file_id":"` + owned + `"}`, 0},
		{"extra_key", `{"image_url":"https://fixture.invalid/a.png","other":true}`, 0},
		{"unknown_object", `{"url":"https://fixture.invalid/a.png"}`, 0},
		{"null_item", `[null]`, 0}, {"number", `3`, 0}, {"boolean", `true`, 0},
		{"invalid_uuid", `{"file_id":"missing"}`, 0}, {"missing_file", `{"file_id":"00000000-0000-4000-8000-000000000099"}`, 0}, {"foreign_file", `{"file_id":"` + foreign + `"}`, 0},
		{"not_image", `{"file_id":"` + video + `"}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			urls, err := s.inputReferenceURLs(r, json.RawMessage(tc.raw))
			if tc.count == 0 {
				if err == nil {
					t.Fatal("invalid representation accepted", urls)
				}
				return
			}
			if err != nil || len(urls) != tc.count {
				t.Fatal(urls, err)
			}
			if tc.name == "shared" && !strings.Contains(urls[0], "/references/"+templates.LibraryOwnerID+"/"+shared+"?") {
				t.Fatal("shared source lost its namespace", urls)
			}
			if tc.name == "owned" && !strings.Contains(urls[0], "/references/alice/"+owned+"?") {
				t.Fatal("private source lost its namespace", urls)
			}
			if tc.name == "mixed_order" && (urls[0] != "https://fixture.invalid/a.png" || urls[1] != "https://fixture.invalid/b.png") {
				t.Fatal("array order changed", urls)
			}
		})
	}
}

func TestImageEditsJSONRequirementIsRouteSpecific(t *testing.T) {
	for _, tc := range []struct {
		path, model, extra string
		want               int
	}{
		{"/v1/images/edits", "mock/image", "", 400},
		{"/v1/images/edits", "mock/image", `,"image":"https://fixture.invalid/a.png"`, 202},
		{"/v1/images/generations", "mock/image", "", 202},
		{"/v1/videos", "mock/video", "", 202},
	} {
		out := openAIRequest(New(openAIFixture(), healthStub{}, "secret"), tc.path, `{"model":"`+tc.model+`","prompt":"fixture"`+tc.extra+`}`)
		if out.Code != tc.want {
			t.Fatal(tc.path, out.Code, out.Body)
		}
	}
}
