package api

import (
	"context"
	"encoding/json"
	"testing"

	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

type invitationRoleFixture struct {
	*organizationFixture
	roles   []string
	accepts int
}

func (f *invitationRoleFixture) CreateOrganizationInvite(_ context.Context, _, _ string, roles ...string) (org.Invite, error) {
	if len(roles) != 1 {
		return org.Invite{}, org.ErrInvalid
	}
	f.roles = append(f.roles, roles[0])
	return org.Invite{Role: roles[0], Token: "fixture"}, nil
}
func (f *invitationRoleFixture) OrganizationInvitation(_ context.Context, _, _ string, accept bool) (org.Invite, error) {
	if accept {
		f.accepts++
	}
	return org.Invite{Role: org.Developer}, nil
}
func TestInvitationRoleRequest(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &invitationRoleFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}}
	s.system = f
	for _, tc := range []struct{ body, want string }{{"", "member"}, {`{}`, "member"}, {`{"role":"supermanager"}`, "supermanager"}, {`{"role":"admin"}`, "admin"}, {`{"role":"content_officer"}`, "content_officer"}, {`{"role":"developer"}`, "developer"}} {
		w := sendTest(s.handler(), "POST", "/v1/organizations/"+fixtureOrg+"/invitations", tc.body, "alice", "")
		var i org.Invite
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &i) != nil || i.Role != tc.want || f.roles[len(f.roles)-1] != tc.want {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body)
		}
	}
	count := len(f.roles)
	for _, body := range []string{`{"role":""}`, `{"role":"system_admin"}`, `{"role":"client_manager"}`, `{"role":1}`, `{"role":"developer","expires_at":"tomorrow"}`} {
		w := sendTest(s.handler(), "POST", "/v1/organizations/"+fixtureOrg+"/invitations", body, "alice", "")
		if w.Code != 400 || len(f.roles) != count {
			t.Fatalf("invalid creation accepted: %d %s", w.Code, w.Body)
		}
	}
	w := sendTest(s.handler(), "POST", "/v1/invitations/accept", `{"token":"fixture","role":"supermanager"}`, "alice", "")
	if w.Code != 400 || f.accepts != 0 {
		t.Fatal("acceptance overrode stored role", w.Code, w.Body)
	}
}
