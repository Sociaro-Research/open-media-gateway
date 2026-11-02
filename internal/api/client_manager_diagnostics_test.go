package api

import (
	"context"
	"encoding/json"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"testing"
)

type managerDiagnosticFixture struct {
	*clientManagerFixture
	failure error
	calls   int
	target  string
}

func (f *managerDiagnosticFixture) AssignClientManager(_ context.Context, _, _, target string) error {
	f.calls++
	f.target = target
	return f.failure
}
func (f *managerDiagnosticFixture) OrganizationAccess(_ context.Context, actor, _ string) (string, error) {
	if actor == "alice" {
		return "supermanager", nil
	}
	return "", org.ErrForbidden
}
func TestClientManagerAssignmentActionableErrors(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &managerDiagnosticFixture{clientManagerFixture: &clientManagerFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}}
	s.system = f
	path := "/v1/organizations/11111111-1111-4111-8111-111111111111/client-manager"
	for _, tc := range []struct {
		failure       error
		status        int
		message, code string
	}{{system.ErrClientManagerRole, 400, "The selected user does not have the Client manager role.", "invalid_client_manager_role"}, {system.ErrNotFound, 404, "User not found.", "not_found"}, {nil, 204, "", ""}} {
		f.failure = tc.failure
		w := sendTest(s.handler(), "PUT", path, `{"user":"manager@example.test"}`, "alice", "")
		if w.Code != tc.status {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if tc.message != "" {
			var body struct {
				Error struct{ Message, Code string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Message != tc.message || body.Error.Code != tc.code {
				t.Fatalf("diagnostic: %s %v", w.Body.String(), err)
			}
		}
	}
	for _, actor := range []string{"bob", ""} {
		before := f.calls
		w := sendTest(s.handler(), "PUT", path, `{"user":"manager@example.test"}`, actor, "")
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("authorization: %d", w.Code)
		}
		if f.calls != before {
			t.Fatal("unauthorized request resolved an identity")
		}
	}
	_, restrictedKey := issueTestKey(t, s.handler(), "alice", `{"name":"Restricted diagnostic fixture","scopes":["generation"]}`)
	before := f.calls
	w := sendTest(s.handler(), "PUT", path, `{"user":"manager@example.test"}`, "", restrictedKey)
	if w.Code != 403 || f.calls != before {
		t.Fatalf("restricted key resolved an identity: %d", w.Code)
	}

}
