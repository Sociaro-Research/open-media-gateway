package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/google/uuid"
)

// All requests pass through real authentication/policy middleware and PostgreSQL.
// No network server, external invitation or email delivery is involved.
type invitationKeyHTTPFixture struct {
	t       *testing.T
	ctx     context.Context
	db      *sql.DB
	people  *AuthRepository
	sys     *SystemRepository
	keys    *APIKeyRepository
	handler http.Handler
	admin   identity.User
	owner   identity.User
	a, b    org.Organization
}

func newInvitationKeyHTTPFixture(t *testing.T) *invitationKeyHTTPFixture {
	t.Helper()
	f := &invitationKeyHTTPFixture{t: t, ctx: context.Background(), db: auditDatabase(t, true)}
	f.people, f.sys, f.keys = NewAuthRepository(f.db), NewSystemRepository(f.db), NewAPIKeyRepository(f.db)
	f.admin, f.owner = f.user("Administrator"), f.user("Company owner")
	f.exec(`UPDATE users SET is_system_admin=true WHERE id=$1`, f.admin.ID)
	for i, name := range []string{"Invitation boundary A", "Invitation boundary B"} {
		company, err := f.sys.CreateOrganization(f.ctx, f.admin.ID, name, f.owner.ID, uuid.NewString()+".fixture.test")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.a = company
		} else {
			f.b = company
		}
	}
	f.exec(`UPDATE organizations SET default_weekly_limit_micros=17000000 WHERE id=$1`, f.b.ID)
	login, err := auth.New(f.ctx, f.people, auth.Config{SessionSecret: strings.Repeat("i", 32), PublicURL: "https://invitation.fixture.test"})
	if err != nil {
		t.Fatal(err)
	}
	f.handler = api.NewGateway(api.Dependencies{Auth: login, System: f.sys, Keys: f.keys, APIKey: "synthetic-service"})
	return f
}

func (f *invitationKeyHTTPFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *invitationKeyHTTPFixture) user(name string) identity.User {
	f.t.Helper()
	u, err := f.people.UpsertGoogleUser(f.ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "")
	if err != nil {
		f.t.Fatal(err)
	}
	return u
}

func (f *invitationKeyHTTPFixture) member(company string, user identity.User, role string) {
	f.t.Helper()
	if err := f.sys.SetOrganizationMember(f.ctx, f.admin.ID, company, user.ID, role, nil, false, false); err != nil {
		f.t.Fatal(err)
	}
}

func (f *invitationKeyHTTPFixture) key(user identity.User, company string, scopes ...string) string {
	f.t.Helper()
	k, secret, err := apikey.Generate(apikey.CreateInput{UserID: user.ID, OrganizationID: company, Name: "Synthetic invitation key", ResourceMode: "shared", Scopes: scopes}, time.Now())
	if err != nil {
		f.t.Fatal(err)
	}
	if err = f.keys.Create(identity.WithUser(f.ctx, user), user.ID, k, apikey.Hash(secret)); err != nil {
		f.t.Fatal(err)
	}
	return secret
}

func (f *invitationKeyHTTPFixture) session(user identity.User) string {
	f.t.Helper()
	secret := uuid.NewString() + uuid.NewString()
	hash := sha256.Sum256([]byte(secret))
	if err := f.people.CreateSession(f.ctx, hash[:], user.ID, time.Now().Add(time.Hour)); err != nil {
		f.t.Fatal(err)
	}
	return secret
}

func (f *invitationKeyHTTPFixture) invite(company, role string) org.Invite {
	f.t.Helper()
	i, err := f.sys.CreateOrganizationInvite(f.ctx, f.admin.ID, company, role)
	if err != nil {
		f.t.Fatal(err)
	}
	return i
}

func (f *invitationKeyHTTPFixture) send(method, path, token, key, session string) *httptest.ResponseRecorder {
	f.t.Helper()
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		f.t.Fatal(err)
	}
	r := httptest.NewRequest(method, "https://invitation.fixture.test"+path, strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://invitation.fixture.test")
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if session != "" {
		r.AddCookie(&http.Cookie{Name: "media_gateway_session", Value: session})
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

type invitationMembershipState struct {
	Rows, Active, RoleGrants, AcceptAudits int
	Role, RemovedAt, AcceptedBy            string
	WeeklyLimit                            int64
}

func (f *invitationKeyHTTPFixture) state(user identity.User, invite org.Invite) invitationMembershipState {
	f.t.Helper()
	var s invitationMembershipState
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*),count(*) FILTER(WHERE removed_at IS NULL),COALESCE(max(role),''),COALESCE(max(weekly_limit_micros),0),COALESCE(max(removed_at)::text,'') FROM organization_members WHERE organization_id=$1 AND user_id=$2`, invite.OrganizationID, user.ID).Scan(&s.Rows, &s.Active, &s.Role, &s.WeeklyLimit, &s.RemovedAt); err != nil {
		f.t.Fatal(err)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT COALESCE(accepted_by::text,'') FROM organization_invites WHERE id=$1`, invite.ID).Scan(&s.AcceptedBy); err != nil {
		f.t.Fatal(err)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM audit_events WHERE outcome='success' AND organization_id=$1 AND object_type='membership' AND object_id=$2 AND details->'after' ? 'role'`, invite.OrganizationID, invite.OrganizationID+"/"+user.ID).Scan(&s.RoleGrants); err != nil {
		f.t.Fatal(err)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM audit_events WHERE outcome='success' AND object_id=$1 AND action='invite_accepted'`, invite.ID).Scan(&s.AcceptAudits); err != nil {
		f.t.Fatal(err)
	}
	return s
}

func TestOrdinaryInvitationsRespectDirectKeyCompanyBoundary(t *testing.T) {
	for _, membership := range []string{"absent", "removed", "active"} {
		t.Run(membership, func(t *testing.T) {
			f := newInvitationKeyHTTPFixture(t)
			u := f.user("Ordinary key owner")
			f.member(f.a.ID, u, org.Supermanager)
			if membership != "absent" {
				f.member(f.b.ID, u, org.Developer)
				f.exec(`UPDATE organization_members SET weekly_limit_micros=42000000 WHERE organization_id=$1 AND user_id=$2`, f.b.ID, u.ID)
				if membership == "removed" {
					f.exec(`UPDATE organization_members SET removed_at=NOW() WHERE organization_id=$1 AND user_id=$2`, f.b.ID, u.ID)
				}
			}
			key := f.key(u, f.a.ID, apikey.Generation, apikey.OrganizationsManage)
			if w := f.send("GET", "/v1/organizations/"+f.a.ID, "", key, ""); w.Code != 200 {
				t.Fatalf("fixture key cannot access its own company: %d", w.Code)
			}
			invite := f.invite(f.b.ID, org.MemberRole)
			before := f.state(u, invite)
			for _, action := range []string{"preview", "accept"} {
				w := f.send("POST", "/v1/invitations/"+action, invite.Token, key, "")
				after := f.state(u, invite)
				leaked := strings.Contains(w.Body.String(), f.b.ID) || strings.Contains(w.Body.String(), f.b.Name)
				t.Logf("membership=%s action=%s status=%d company_metadata=%t before=%+v after=%+v", membership, action, w.Code, leaked, before, after)
				if w.Code != 403 || leaked {
					t.Errorf("A-bound key reached foreign invitation: status=%d metadata=%t", w.Code, leaked)
				}
				if after != before {
					t.Error("denied invitation changed membership, token consumption or successful role-grant audit")
				}
			}
			if w := f.send("GET", "/v1/organizations/"+f.b.ID, "", key, ""); w.Code != 403 {
				t.Errorf("ordinary foreign company route boundary changed: %d", w.Code)
			} else {
				t.Log("ordinary B-bound route remains HTTP403")
			}
		})
	}
}

func TestOrdinaryInvitationSessionAndUnboundKeyAcceptance(t *testing.T) {
	for _, credential := range []string{"browser", "unbound-key"} {
		t.Run(credential, func(t *testing.T) {
			f := newInvitationKeyHTTPFixture(t)
			u := f.user("Legitimate invitee")
			var key, session string
			if credential == "browser" {
				session = f.session(u)
			} else {
				key = f.key(u, "", apikey.Generation, apikey.OrganizationsManage)
			}
			invite := f.invite(f.b.ID, org.Developer)
			before := f.state(u, invite)
			if w := f.send("POST", "/v1/invitations/preview", invite.Token, key, session); w.Code != 200 || f.state(u, invite) != before {
				t.Fatalf("legitimate preview failed or changed state: %d", w.Code)
			}
			w := f.send("POST", "/v1/invitations/accept", invite.Token, key, session)
			var result org.Invite
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Role != org.Developer || result.AlreadyMember {
				t.Fatalf("legitimate acceptance failed: %d", w.Code)
			}
			after := f.state(u, invite)
			if after.Active != 1 || after.Role != org.Developer || after.WeeklyLimit != 17000000 || after.AcceptedBy != u.ID || after.RoleGrants != before.RoleGrants+1 || after.AcceptAudits != before.AcceptAudits+1 {
				t.Fatalf("stored role/default limit/single grant changed: %+v", after)
			}
			if w = f.send("POST", "/v1/invitations/accept", invite.Token, key, session); w.Code != 200 || f.state(u, invite) != after {
				t.Fatal("existing-member acceptance consumed or granted twice", w.Code)
			}
			other := f.user("Another invitee")
			otherKey := f.key(other, "", apikey.Generation, apikey.OrganizationsManage)
			unchanged := f.state(other, invite)
			for _, action := range []string{"preview", "accept"} {
				if w = f.send("POST", "/v1/invitations/"+action, invite.Token, otherKey, ""); w.Code != 409 || f.state(other, invite) != unchanged {
					t.Fatalf("single-use invitation admitted another account: %d", w.Code)
				}
			}
		})
	}
}

func TestOrdinaryInvitationSameCompanyMemberAndMissingScope(t *testing.T) {
	f := newInvitationKeyHTTPFixture(t)
	u := f.user("Same-company member")
	f.member(f.a.ID, u, org.Supermanager)
	f.exec(`UPDATE organization_members SET weekly_limit_micros=42000000 WHERE organization_id=$1 AND user_id=$2`, f.a.ID, u.ID)
	key := f.key(u, f.a.ID, apikey.Generation, apikey.OrganizationsManage)
	invite := f.invite(f.a.ID, org.MemberRole)
	before := f.state(u, invite)
	for _, action := range []string{"preview", "accept"} {
		w := f.send("POST", "/v1/invitations/"+action, invite.Token, key, "")
		var result org.Invite
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || !result.AlreadyMember || f.state(u, invite) != before {
			t.Fatalf("same-company member semantics changed: action=%s status=%d", action, w.Code)
		}
	}
	limited := f.key(u, f.a.ID, apikey.Generation)
	if w := f.send("POST", "/v1/invitations/preview", invite.Token, limited, ""); w.Code != 200 {
		t.Fatal("preview gained an organizations:manage requirement", w.Code)
	}
	// A browser session must not rescue an explicitly insufficient key.
	if w := f.send("POST", "/v1/invitations/accept", invite.Token, limited, f.session(u)); w.Code != 403 || f.state(u, invite) != before {
		t.Fatal("missing scope fell back to the browser or changed state", w.Code)
	}
	for _, action := range []string{"preview", "accept"} {
		if w := f.send("POST", "/v1/invitations/"+action, strings.Repeat("x", 43), key, ""); w.Code != 404 || f.state(u, invite) != before {
			t.Fatal("unknown invitation token behavior changed", action, w.Code)
		}
	}
}

func (f *invitationKeyHTTPFixture) denied(user identity.User, invite org.Invite, key string) {
	f.t.Helper()
	before := f.state(user, invite)
	for _, action := range []string{"preview", "accept"} {
		w := f.send("POST", "/v1/invitations/"+action, invite.Token, key, "")
		if w.Code != 403 || strings.Contains(w.Body.String(), invite.OrganizationID) || f.state(user, invite) != before {
			f.t.Fatalf("foreign invitation ceiling not enforced: action=%s status=%d", action, w.Code)
		}
	}
}

func TestOrdinaryInvitationGlobalExceptionRequiresScopeAndLiveRole(t *testing.T) {
	f := newInvitationKeyHTTPFixture(t)
	u := f.user("Initially global administrator")
	f.member(f.a.ID, u, org.Supermanager)
	f.exec(`UPDATE users SET is_system_admin=true WHERE id=$1`, u.ID)
	withoutSystemScope := f.key(u, f.a.ID, apikey.Generation, apikey.OrganizationsManage)
	f.denied(u, f.invite(f.b.ID, org.MemberRole), withoutSystemScope)
	global := f.key(u, f.a.ID, apikey.Generation, apikey.OrganizationsManage, apikey.SystemManage)
	invite := f.invite(f.b.ID, org.Developer)
	for _, action := range []string{"preview", "accept"} {
		if w := f.send("POST", "/v1/invitations/"+action, invite.Token, global, ""); w.Code != 200 {
			t.Fatal("valid global scope and live administrator lost access", action, w.Code)
		}
	}
	if state := f.state(u, invite); state.Active != 1 || state.Role != org.Developer || state.AcceptedBy != u.ID {
		t.Fatal("valid global invitation did not grant its stored role", state)
	}
	// The same key retains its scope and A membership; only the current system role is removed.
	f.exec(`UPDATE users SET is_system_admin=false WHERE id=$1`, u.ID)
	f.denied(u, f.invite(f.b.ID, org.MemberRole), global)
	if w := f.send("GET", "/v1/organizations/"+f.a.ID, "", global, ""); w.Code != 200 {
		t.Fatal("role removal incorrectly revoked the still-valid A key", w.Code)
	}
}

func (f *invitationKeyHTTPFixture) firstOwner(recipient identity.User) org.Invite {
	f.t.Helper()
	manager := f.user("Synthetic onboarding manager")
	if err := f.sys.SetClientManager(f.ctx, f.admin.ID, manager.ID, true); err != nil {
		f.t.Fatal(err)
	}
	token := strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")[:43]
	hash := sha256.Sum256([]byte(token))
	input := org.ClientInput{OperationID: uuid.NewString(), Name: "Synthetic first-owner company", Country: "US", Domain: uuid.NewString() + ".fixture.test", ContactName: recipient.Name, Email: recipient.Email}
	delivery := org.OwnerDelivery{ID: uuid.NewString(), Hash: hash[:], ExpiresAt: time.Now().Add(14 * 24 * time.Hour), Origin: "https://invitation.fixture.test"}
	company, err := f.sys.CreateClient(f.ctx, manager.ID, input, delivery)
	if err != nil {
		f.t.Fatal(err)
	}
	i := org.Invite{OrganizationID: company.OrganizationID, Token: token, FirstOwner: true}
	if err := f.db.QueryRowContext(f.ctx, `SELECT id FROM organization_invites WHERE organization_id=$1`, company.OrganizationID).Scan(&i.ID); err != nil {
		f.t.Fatal(err)
	}
	return i
}

func TestFirstOwnerInvitationStillRejectsAllAPIKeys(t *testing.T) {
	for _, credential := range []string{"unbound", "global-admin"} {
		t.Run(credential, func(t *testing.T) {
			f := newInvitationKeyHTTPFixture(t)
			u := f.user("First-owner recipient")
			var key string
			if credential == "unbound" {
				key = f.key(u, "", apikey.Generation, apikey.OrganizationsManage)
			} else {
				f.member(f.a.ID, u, org.Supermanager)
				f.exec(`UPDATE users SET is_system_admin=true WHERE id=$1`, u.ID)
				key = f.key(u, f.a.ID, apikey.Generation, apikey.OrganizationsManage, apikey.SystemManage)
			}
			invite := f.firstOwner(u)
			before := f.state(u, invite)
			if w := f.send("POST", "/v1/invitations/preview", invite.Token, "", f.session(u)); w.Code != 200 || !strings.Contains(w.Body.String(), `"first_owner":true`) {
				t.Fatal("first-owner control invitation is not valid for its browser recipient", w.Code)
			}
			f.denied(u, invite, key)
			if f.state(u, invite) != before {
				t.Fatal("first-owner API-key denial mutated state")
			}
		})
	}
}

func TestOrdinaryInvitationDelegatedBoundaryStillNeedsMutationAuthority(t *testing.T) {
	f := newInvitationKeyHTTPFixture(t)
	lifecycle := ciRepository(t, f.db)
	u, _ := ciUser(t, f.db)
	start := ciStart(t)
	tokens := ciIssue(t, lifecycle, u, start)
	credentials := ciCredentials(start, tokens)
	principal, err := ciGate(f.db).AuthenticateBrowser(ciHost(), credentials, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	bound := identity.WithCompanyBoundary(identity.WithUser(ciHost(), u), tokens.Company)
	invite := f.invite(tokens.Company, org.MemberRole)
	before := f.state(u, invite)
	if _, err := f.sys.OrganizationInvitation(bound, u.ID, invite.Token, true); !errors.Is(err, ci.ErrDenied) {
		t.Fatal("bare delegated boundary gained mutation authority", err)
	}
	valid := ci.WithMutationAuthority(bound, principal, principal, credentials, apikey.OrganizationsManage, "invitation-control")
	for _, accept := range []bool{false, true} {
		result, err := f.sys.OrganizationInvitation(valid, u.ID, invite.Token, accept)
		if err != nil || !result.AlreadyMember || f.state(u, invite) != before {
			t.Fatal("valid delegated same-company member control changed", err)
		}
	}
	foreign := f.invite(f.b.ID, org.MemberRole)
	unchanged := f.state(u, foreign)
	for _, accept := range []bool{false, true} {
		if _, err := f.sys.OrganizationInvitation(valid, u.ID, foreign.Token, accept); !errors.Is(err, org.ErrForbidden) || f.state(u, foreign) != unchanged {
			t.Fatal("delegated foreign invitation boundary changed", err)
		}
	}
}
