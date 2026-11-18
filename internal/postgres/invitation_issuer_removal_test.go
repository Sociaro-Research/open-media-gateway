package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/google/uuid"
)

// An invitation is a bearer link: after its issuer is removed or demoted, the
// link must not grant a role the issuer could no longer assign.
func TestInvitationsOfRemovedOrDemotedManagersAreRevoked(t *testing.T) {
	ctx := context.Background()
	db := auditDatabase(t, true)
	people, repo := NewAuthRepository(db), NewSystemRepository(db)
	user := func(name string) identity.User {
		t.Helper()
		u, err := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "")
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	admin, owner, leaver, manager, guest := user("Administrator"), user("Owner"), user("Leaving supermanager"), user("Manager"), user("Guest")
	if _, err := db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	company, err := repo.CreateOrganization(ctx, admin.ID, "Invitation issuers", owner.ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	member := func(actor, target identity.User, role string, remove bool) {
		t.Helper()
		if err := repo.SetOrganizationMember(ctx, actor.ID, company.ID, target.ID, role, nil, false, remove); err != nil {
			t.Fatal(err)
		}
	}
	invite := func(issuer identity.User, role string) org.Invite {
		t.Helper()
		i, err := repo.CreateOrganizationInvite(ctx, issuer.ID, company.ID, role)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	usable := func(i org.Invite) bool {
		t.Helper()
		_, err := repo.OrganizationInvitation(ctx, guest.ID, i.Token, false)
		if err != nil && !errors.Is(err, org.ErrInvitationUnavailable) {
			t.Fatal(err)
		}
		return err == nil
	}
	member(admin, leaver, org.Supermanager, false)
	member(admin, manager, org.Manager, false)
	member(admin, admin, org.Supermanager, false)
	high, low := invite(leaver, org.Supermanager), invite(leaver, org.MemberRole)
	fromManager, fromOwner, fromAdmin := invite(manager, org.Developer), invite(owner, org.Supermanager), invite(admin, org.Supermanager)

	member(owner, leaver, org.Manager, false)
	if usable(high) {
		t.Fatal("a demoted Supermanager kept a working Supermanager invitation")
	}
	if !usable(low) {
		t.Fatal("demotion to Manager revoked a Member invitation a Manager may still issue")
	}
	member(owner, leaver, org.MemberRole, true)
	if usable(low) {
		t.Fatal("a removed manager kept a working invitation")
	}
	member(owner, manager, org.Developer, false)
	if usable(fromManager) {
		t.Fatal("a Manager demoted to Developer kept a working invitation")
	}
	// Administrators invite without membership, and nobody else's links are touched.
	member(owner, admin, org.MemberRole, true)
	if !usable(fromOwner) || !usable(fromAdmin) {
		t.Fatal("unrelated or administrator invitations were revoked")
	}
	var audited int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events WHERE action='invite_revoked' AND object_id IN ($1,$2,$3)`, high.ID, low.ID, fromManager.ID).Scan(&audited); err != nil || audited != 3 {
		t.Fatalf("revocations in the audit log: %d %v", audited, err)
	}
}
