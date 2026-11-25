package api

import (
	"strings"
	"testing"
)

func TestAPIKeyCreationNamesTheRejectedField(t *testing.T) {
	s, _ := newPersonalTestServer()
	h := s.handler()
	for _, tc := range []struct{ body, param string }{
		{`{"name":" "}`, "name"},
		{`{"name":"Old","expires_at":"2020-01-01T00:00:00Z"}`, "expires_at"},
		{`{"name":"Scope","scopes":["generation","nope"]}`, "scopes"},
		{`{"name":"Scope","scopes":["keys:manage"]}`, "scopes"},
		{`{"name":"Mode","resource_mode":"team"}`, "resource_mode"},
	} {
		w := sendTest(h, "POST", "/v1/api-keys", tc.body, "alice", "")
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"param":"`+tc.param+`"`) {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
	}
}
