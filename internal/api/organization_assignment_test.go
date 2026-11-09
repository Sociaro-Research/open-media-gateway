package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type assignmentAPIFixture struct {
	*memberLimitFixture
	assigned bool
	target   string
	result   error
}

func (f *assignmentAPIFixture) AssignOrganizationMember(_ context.Context, _, _, target, role string, limit *money.USD, provided bool) error {
	f.assigned, f.target, f.role, f.limit, f.provided = true, target, role, limit, provided
	return f.result
}

func TestOrganizationAssignmentIntent(t *testing.T) {
	for _, target := range []string{"existing%40example.test", "bob"} {
		for _, tc := range []struct {
			name, body string
			admin      bool
			result     error
			status     int
			assign     bool
		}{
			{"add", `{"role":"developer","add_only":true}`, true, nil, 204, true},
			{"already-active", `{"role":"member","add_only":true}`, true, org.ErrAlreadyMember, 409, true},
			{"intentional-apply", `{"role":"admin"}`, true, nil, 204, false},
			{"explicit-update", `{"role":"admin","add_only":false}`, true, nil, 204, false},
			{"non-admin", `{"role":"member","add_only":true}`, false, nil, 403, false},
			{"wrong-type", `{"role":"member","add_only":"true"}`, true, nil, 400, false},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				s, _ := newPersonalTestServer()
				f := &assignmentAPIFixture{memberLimitFixture: &memberLimitFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": tc.admin}}}}, result: tc.result}
				s.system = f
				w := sendTest(s.handler(), "PUT", "/v1/organizations/"+fixtureOrg+"/members/"+target, tc.body, "alice", "")
				if w.Code != tc.status || f.assigned != tc.assign {
					t.Fatalf("status=%d assigned=%v: %s", w.Code, f.assigned, w.Body.String())
				}
				if tc.assign && (f.called || f.provided) {
					t.Fatal("assignment fell through to role update or supplied a limit")
				}
				if w.Code == 409 {
					var body struct {
						Error struct{ Code, Message string }
					}
					if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != "already_member" || body.Error.Message != org.ErrAlreadyMember.Error() {
						t.Fatal("duplicate member diagnostic", err, w.Body.String())
					}
				}
				if tc.name == "intentional-apply" || tc.name == "explicit-update" {
					if !f.called || f.role != "admin" {
						t.Fatal("member-row update lost its existing behavior")
					}
				}
			})
		}
	}
}

func TestOrganizationAssignmentFailsClosedWithoutCapability(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &memberLimitFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}}
	s.system = f
	w := sendTest(s.handler(), "PUT", "/v1/organizations/"+fixtureOrg+"/members/bob", `{"role":"member","add_only":true}`, "alice", "")
	if w.Code != http.StatusServiceUnavailable || f.called {
		t.Fatalf("unsupported assignment fell back to update: %d %s", w.Code, w.Body.String())
	}
}

func TestOrganizationAssignmentNullIntentRejected(t *testing.T) {
	for _, supported := range []bool{true, false} {
		t.Run(map[bool]string{true: "with-assigner", false: "without-assigner"}[supported], func(t *testing.T) {
			s, _ := newPersonalTestServer()
			base := &memberLimitFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}}
			assigner := &assignmentAPIFixture{memberLimitFixture: base, result: org.ErrAlreadyMember}
			if supported {
				s.system = assigner
			} else {
				s.system = base
			}
			w := sendTest(s.handler(), "PUT", "/v1/organizations/"+fixtureOrg+"/members/bob", `{"role":"member","add_only":null}`, "alice", "")
			if w.Code != http.StatusBadRequest || base.called || assigner.assigned {
				t.Fatalf("null intent reached a mutation: status=%d setter=%v assigner=%v body=%s", w.Code, base.called, assigner.assigned, w.Body.String())
			}
			var body struct {
				Error struct{ Code, Message string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != "invalid_request" || body.Error.Message != "add_only must be a boolean" {
				t.Fatalf("null intent diagnostic: %v %s", err, w.Body.String())
			}
		})
	}
}

func TestOrganizationAssignmentKeyPermissions(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &assignmentAPIFixture{memberLimitFixture: &memberLimitFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}}}
	f.settings.SharedResources, f.settings.PersonalProviders = true, false
	s.system = f
	h := s.handler()
	_, generation := issueTestKey(t, h, "alice", `{"name":"Generate","organization_id":"`+fixtureOrg+`"}`)
	_, manager := issueTestKey(t, h, "alice", `{"name":"Manage","organization_id":"`+fixtureOrg+`","scopes":["generation","organizations:manage"]}`)
	_, global := issueTestKey(t, h, "alice", `{"name":"Global","organization_id":"`+fixtureOrg+`","scopes":["generation","organizations:manage","system:manage"]}`)
	for _, tc := range []struct {
		key  string
		want int
	}{{generation, 403}, {manager, 403}, {global, 204}} {
		f.assigned = false
		w := sendTest(h, "PUT", "/v1/organizations/"+fixtureOrg+"/members/bob", `{"role":"member","add_only":true}`, "", tc.key)
		if w.Code != tc.want || f.assigned != (tc.want == 204) {
			t.Fatalf("assignment key scope: %d %s", w.Code, w.Body.String())
		}
	}
	f.admins["alice"], f.assigned = false, false
	w := sendTest(h, "PUT", "/v1/organizations/"+fixtureOrg+"/members/bob", `{"role":"member","add_only":true}`, "", global)
	if w.Code != 403 || f.assigned {
		t.Fatal("removed current system role retained assignment authority", w.Code)
	}
}
