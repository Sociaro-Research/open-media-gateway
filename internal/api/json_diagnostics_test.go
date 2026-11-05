package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type diagnosticInput struct {
	Name    string `json:"name"`
	Options struct {
		Enabled bool `json:"enabled"`
		Count   int  `json:"count"`
	} `json:"options"`
	Items []struct {
		Name string `json:"name"`
	} `json:"items"`
	Labels []string `json:"labels"`
}

func TestJSONDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, body, param, message string }{
		{"unknown", `{"nam":"private-value"}`, "", "Unsupported JSON field"},
		{"wrong string", `{"name":123}`, "name", "a JSON string"},
		{"nested bool", `{"options":{"enabled":"private-value"}}`, "options.enabled", "a JSON boolean"},
		{"nested integer", `{"options":{"count":1.5}}`, "options.count", "a JSON integer"},
		{"array item", `{"items":[{"name":false}]}`, "items[].name", "a JSON string"},
		{"string array item", `{"labels":[false]}`, "labels[]", "a JSON string"},
		{"wrong array", `{"items":{}}`, "items", "a JSON array"},
		{"root array", `[]`, "", "a JSON object"},
		{"malformed", `{"name":!}`, "", "Malformed JSON at byte 9"},
		{"truncated", `{"name":`, "", "Unexpected end of JSON"},
		{"empty", ``, "", "Request body is empty"},
		{"limit", strings.Repeat(" ", 4097), "", "4096 bytes"},
		{"limit after object", `{}` + strings.Repeat(" ", 4095), "", "4096 bytes"},
		{"trailing object", `{} {}`, "", "after the first JSON value"},
		{"trailing primitive", `{} true`, "", "after the first JSON value"},
		{"trailing malformed", `{} private-value`, "", "after the first JSON value"},
		{"secret field", `{"sk_live_private-value":"private-value"}`, "", "Unsupported JSON field"},
		{"huge field", `{"` + strings.Repeat("private-value", 200) + `":"private-value"}`, "", "Unsupported JSON field"},
		{"secret syntax", `{"name":"private-value","options": private-value}`, "", "Malformed JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input diagnosticInput
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/v1/projects", strings.NewReader(tc.body))
			if decodeSystemLimit(w, r, &input, 4096) {
				t.Fatal("accepted invalid input")
			}
			var got struct {
				Error struct {
					Code, Type, Message string
					Param               *string
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if w.Code != 400 || got.Error.Code != "invalid_request" || got.Error.Type != "invalid_request_error" {
				t.Fatalf("changed envelope: %d %s", w.Code, w.Body)
			}
			param := ""
			if got.Error.Param != nil {
				param = *got.Error.Param
			}
			if param != tc.param || !strings.Contains(got.Error.Message, tc.message) {
				t.Errorf("got %s; want param=%q and message containing %q", w.Body, tc.param, tc.message)
			}
			for _, secret := range []string{"private-value", "sk_live", "diagnosticInput", "cannot unmarshal", "Go struct"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Errorf("leaked %q: %s", secret, w.Body)
				}
			}
			t.Logf("HTTP %d %s", w.Code, strings.TrimSpace(w.Body.String()))
		})
	}
}

func TestJSONDiagnosticsPreserveAcceptedBodies(t *testing.T) {
	// These accepted decoder semantics (including null and duplicate keys) are
	// deliberately unchanged; semantic validation belongs to the existing caller.
	for _, body := range []string{`{}`, `null`, `{"name":null}`, `{"name":"a","name":"b"}`, `{"NAME":"a"}`, `{"items":null}`, `{"options":{}}`, "{} \n\t"} {
		var input diagnosticInput
		if !decodeSystemLimit(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(body)), &input, 4096) {
			t.Fatalf("rejected previously accepted body %s", body)
		}
	}
}

func TestJSONDiagnosticsAtOrdinaryUserHandlers(t *testing.T) {
	s, _ := newPersonalTestServer()
	storage := &storageAdminStub{}
	s.storageAdmin = storage
	h := s.handler()
	for _, tc := range []struct{ method, path, body, param, hint string }{
		{"POST", "/v1/api-keys", `{"name":123}`, "name", "a JSON string"},
		{"POST", "/v1/api-keys", `{"scopes":[false]}`, "scopes[]", "a JSON string"},
		{"POST", "/v1/api-keys", strings.Repeat(" ", 4097), "", "4096 bytes"},
		{"PUT", "/v1/admin/storage", `{"driver":123}`, "driver", "a JSON string"},
		{"PUT", "/v1/admin/storage", `{"sk_live_private-value":"private-value"}`, "", "Unsupported JSON field"},
		{"PUT", "/v1/admin/storage", `{} true`, "", "after the first JSON value"},
		{"PUT", "/v1/admin/storage", strings.Repeat(" ", 16385), "", "16384 bytes"},
		// This adapter uses a custom unmarshaller without reliable field metadata.
		{"PUT", "/v1/admin/providers/mock", `{"version":"private-value"}`, "", "a JSON integer"},
	} {
		t.Run(tc.path+tc.hint, func(t *testing.T) {
			w := sendTest(h, tc.method, tc.path, tc.body, "alice", "")
			assertJSONDiagnostic(t, w, tc.param, tc.hint)
			if storage.owner != "" || storage.checks != 0 {
				t.Fatal("bad request reached storage")
			}
			unauthorized := sendTest(h, tc.method, tc.path, tc.body, "", "")
			if unauthorized.Code != http.StatusUnauthorized || strings.Contains(unauthorized.Body.String(), tc.hint) {
				t.Fatalf("JSON parsing bypassed authentication: %d %s", unauthorized.Code, unauthorized.Body)
			}
		})
	}
}

func TestGenerationJSONDiagnosticsPreserveEnvelopeAndSafeFields(t *testing.T) {
	h := New(openAIFixture(), healthStub{}, "secret")
	for _, tc := range []struct{ body, param, hint string }{
		{`{"model":`, "body", "Unexpected end of JSON"},
		{`[]`, "body", "a JSON object"},
		{`null`, "body", "send one JSON object"},
		{`{} true`, "body", "after the first JSON value"},
		{strings.Repeat(" ", 65537), "body", "65536 bytes"},
		{"{ \n  \"voice\": \"private-value\", \"model\": 123 }", "model", "a JSON string"},
		{`{"model":"mock/model","options":{"seed":"private-value"}}`, "options.seed", "a JSON integer"},
		{`{"model":"mock/model","options":{"sk_live_private-value":"private-value"}}`, "body", "Unsupported JSON field"},
		{`{"model":"mock/image","prompt":"test","sk_live_private-value":"private-value"}`, "body", "Unsupported JSON field"},
	} {
		w := openAIRequest(h, "/v1/generations", tc.body)
		assertJSONDiagnostic(t, w, tc.param, tc.hint)
		if (tc.param == "model" || tc.param == "options.seed") && strings.Contains(w.Body.String(), "byte") {
			t.Fatalf("reported a position in reconstructed JSON: %s", w.Body)
		}
	}
}

func assertJSONDiagnostic(t *testing.T, w *httptest.ResponseRecorder, param, hint string) {
	t.Helper()
	var got struct {
		Error struct {
			Type, Code, Message string
			Param               *string
		}
	}
	if json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatal(w.Body)
	}
	actual := ""
	if got.Error.Param != nil {
		actual = *got.Error.Param
	}
	if w.Code != 400 || got.Error.Type != "invalid_request_error" || got.Error.Code != "invalid_request" || actual != param || !strings.Contains(got.Error.Message, hint) {
		t.Fatalf("diagnostic: %d %s; want param=%q hint=%q", w.Code, w.Body, param, hint)
	}
	for _, private := range []string{"private-value", "sk_live", "Go struct", "cannot unmarshal", "core.MediaOptions"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("leaked %q: %s", private, w.Body)
		}
	}
}
