package requestjson

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestUnknownFieldPositionsAreSafeAndUnambiguous(t *testing.T) {
	for _, tc := range []struct {
		body, want string
	}{
		{`{"nam":"secret"}`, "ending at byte 6"},
		{`{"nested":{"sk_live_secret":"secret"}}`, "ending at byte 27"},
		{`{"nam":"secret","nam":"secret"}`, "Unsupported JSON field."},
		{`{"name":"` + strings.Repeat("x", 8200) + `","nam":true}`, "Unsupported JSON field."},
	} {
		var target struct {
			Name   string   `json:"name"`
			Nested struct{} `json:"nested"`
		}
		p := Decode(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(tc.body)), &target, 16384, true)
		if p == nil || p.Param != "" || !strings.Contains(p.Message, tc.want) || strings.Contains(p.Message, "secret") || strings.Contains(p.Message, `"nam"`) {
			t.Fatalf("unsafe or incorrect unknown-field diagnostic: %+v", p)
		}
	}
}

type privateValue struct{}

func (*privateValue) UnmarshalJSON([]byte) error {
	return errors.New("secret-token /private/path response payload")
}

func TestCustomAndIOErrorsDoNotExposePrivateDetails(t *testing.T) {
	var target struct {
		Value privateValue `json:"value"`
	}
	p := Decode(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(`{"value":"secret-token"}`)), &target, 4096, true)
	if p == nil || p.Param != "" || !strings.Contains(p.Message, "documented field types and formats") || strings.Contains(p.Message, "secret") || strings.Contains(p.Message, "private") {
		t.Fatalf("custom error: %+v", p)
	}
	for _, err := range []error{errors.New("secret-token /private/path"), &json.UnmarshalTypeError{Field: "secret-token", Value: "secret-token", Type: reflect.TypeFor[privateValue]()}} {
		p := Describe(err, &target)
		if p.Param != "" || strings.Contains(p.Message, "secret") || strings.Contains(p.Message, "private") || strings.Contains(p.Message, "byte") {
			t.Fatalf("raw error leaked: %+v", p)
		}
	}
}

func TestPublicFieldPathsUseOnlySchemaNames(t *testing.T) {
	type item struct {
		Name string `json:"name"`
	}
	var target struct {
		Items        []item          `json:"items"`
		Matrix       [][]item        `json:"matrix"`
		Dynamic      map[string]item `json:"dynamic"`
		InternalName string
	}
	for _, tc := range []struct{ field, want string }{
		{"items.name", "items[].name"},
		{"items.0.name", "items[].name"},
		{"items.999999.name", "items[].name"},
		{"matrix.0.1.name", "matrix[][].name"},
		{"matrix.name", "matrix[][].name"},
		{"items.secret-token.name", ""},
		{"dynamic.secret-token.name", ""},
		{"InternalName", ""},
	} {
		if got := publicField(reflect.TypeOf(&target), tc.field, reflect.TypeFor[string]()); got != tc.want {
			t.Errorf("path %q: got %q want %q", tc.field, got, tc.want)
		}
	}
}

// Compare the externally meaningful acceptance and decoded result against the
// previous decoder. Seeds cover nulls, duplicates, custom numeric/string shapes,
// limit boundaries, nesting, extra values and hostile keys; fuzzing extends them.
func FuzzDecodePreservesSemantics(f *testing.F) {
	for _, body := range []string{`{}`, `null`, `[]`, `"secret"`, `{"name":null}`, `{"NAME":"x"}`, `{"name":"a","name":"b"}`, `{"name":42}`, `{"labels":["x",null]}`, `{"labels":{}}`, `{"blob":"YQ=="}`, `{"blob":[1,2,255]}`, `{"count":"3"}`, `{"count":3}`, `{"unknown":"secret"}`, `{} {}`, `{} null`, `{} secret`, `{"name":`, `{"name":!}`, `{}` + strings.Repeat(" ", 4094), `{}` + strings.Repeat(" ", 4095)} {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, body string) {
		type input struct {
			Name   string   `json:"name"`
			Labels []string `json:"labels"`
			Blob   []byte   `json:"blob"`
			Count  int      `json:"count,string"`
		}
		for _, strict := range []bool{true, false} {
			var before, after input
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			limited := http.MaxBytesReader(httptest.NewRecorder(), r.Body, 4096)
			d := json.NewDecoder(limited)
			if strict {
				d.DisallowUnknownFields()
			}
			err := d.Decode(&before)
			accepted := err == nil && d.Decode(&struct{}{}) == io.EOF
			_ = limited.Close()
			problem := Decode(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(body)), &after, 4096, strict)
			if accepted != (problem == nil) || accepted && !reflect.DeepEqual(before, after) {
				t.Fatalf("acceptance or value changed for strict=%t", strict)
			}
		}
	})
}
