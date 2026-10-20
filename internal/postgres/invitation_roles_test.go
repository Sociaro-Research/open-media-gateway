package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/google/uuid"
)

func TestInvitationRoleGrants(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	auth := NewAuthRepository(db)
	repo := NewSystemRepository(db)
	var users []identity.User
	user := func(name string) identity.User {
		t.Helper()
		u, e := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		users = append(users, u)
		return u
	}
	system, owner := user("System"), user("Supermanager")
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, system.ID); err != nil {
		t.Fatal(err)
	}
	company, err := repo.CreateOrganization(ctx, system.ID, "Invitation roles", owner.ID, uuid.NewString()+".example.test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM organization_invites WHERE organization_id=$1`, `DELETE FROM organization_members WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, company.ID); e != nil {
				t.Error(e)
			}
		}
		for _, u := range users {
			if _, e := db.Exec(`DELETE FROM users WHERE id=$1`, u.ID); e != nil {
				t.Error(e)
			}
		}
	}()
	if _, err = db.Exec(`UPDATE organizations SET default_weekly_limit_micros=17000000 WHERE id=$1`, company.ID); err != nil {
		t.Fatal(err)
	}
	roles := []string{org.Supermanager, org.Manager, org.ContentOfficer, org.Developer, org.MemberRole}
	for _, issuer := range []struct {
		user    identity.User
		allowed []string
	}{{system, roles}, {owner, roles}} {
		for _, role := range roles {
			if _, e := repo.CreateOrganizationInvite(ctx, issuer.user.ID, company.ID, role); e != nil {
				t.Fatal(e)
			}
		}
	}
	for _, actorRole := range []string{org.Manager, org.ContentOfficer, org.Developer, org.MemberRole} {
		issuer := user(actorRole)
		if e := repo.SetOrganizationMember(ctx, system.ID, company.ID, issuer.ID, actorRole, nil, false, false); e != nil {
			t.Fatal(e)
		}
		for _, role := range roles {
			_, e := repo.CreateOrganizationInvite(ctx, issuer.ID, company.ID, role)
			allowed := actorRole == org.Manager && (role == org.Developer || role == org.MemberRole)
			if allowed && e != nil || !allowed && !errors.Is(e, org.ErrForbidden) {
				t.Fatalf("%s -> %s: %v", actorRole, role, e)
			}
		}
	}
	for _, bad := range []string{"", "system_admin", "client_manager", "unknown"} {
		if _, e := repo.CreateOrganizationInvite(ctx, system.ID, company.ID, bad); !errors.Is(e, org.ErrInvalid) {
			t.Fatalf("invalid role %q: %v", bad, e)
		}
	}
	legacy, e := repo.CreateOrganizationInvite(ctx, owner.ID, company.ID)
	if e != nil || legacy.Role != org.MemberRole {
		t.Fatal("omitted role changed default", e)
	}
	// Each supported role survives creation, listing and preview and is granted once.
	for _, role := range roles {
		invitation, e := repo.CreateOrganizationInvite(ctx, owner.ID, company.ID, role)
		if e != nil {
			t.Fatal(e)
		}
		recipient := user("Recipient")
		preview, e := repo.OrganizationInvitation(ctx, recipient.ID, invitation.Token, false)
		if e != nil || preview.Role != role || preview.FirstOwner {
			t.Fatal("wrong preview", preview, e)
		}
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM organization_members WHERE user_id=$1`, recipient.ID).Scan(&count)
		if count != 0 {
			t.Fatal("preview granted membership")
		}
		listed, e := repo.OrganizationInvites(ctx, owner.ID, company.ID)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, i := range listed {
			if i.ID == invitation.ID {
				found = i.Role == role
			}
		}
		if !found {
			t.Fatal("list lost role")
		}
		if _, e = repo.OrganizationInvitation(ctx, recipient.ID, invitation.Token, true); e != nil {
			t.Fatal(e)
		}
		var assigned string
		var limit int64
		if e = db.QueryRow(`SELECT role,weekly_limit_micros FROM organization_members WHERE user_id=$1 AND organization_id=$2`, recipient.ID, company.ID).Scan(&assigned, &limit); e != nil || assigned != role || limit != 17000000 {
			t.Fatal("wrong role/limit", assigned, limit, e)
		}
		var audited string
		if e = db.QueryRow(`SELECT details->'after'->>'role' FROM audit_events WHERE object_id=$1 AND action='invite_created' ORDER BY id DESC LIMIT 1`, invitation.ID).Scan(&audited); e != nil || audited != role {
			t.Fatal("role absent from audit", audited, e)
		}
	}
	existing := user("Existing developer")
	if e = repo.SetOrganizationMember(ctx, system.ID, company.ID, existing.ID, org.Developer, nil, false, false); e != nil {
		t.Fatal(e)
	}
	high, e := repo.CreateOrganizationInvite(ctx, owner.ID, company.ID, org.Supermanager)
	if e != nil {
		t.Fatal(e)
	}
	preview, e := repo.OrganizationInvitation(ctx, existing.ID, high.Token, true)
	if e != nil || !preview.AlreadyMember {
		t.Fatal(e)
	}
	var assigned string
	db.QueryRow(`SELECT role FROM organization_members WHERE user_id=$1 AND organization_id=$2`, existing.ID, company.ID).Scan(&assigned)
	if assigned != org.Developer {
		t.Fatal("link changed existing member role")
	}
	// Removed accounts receive the selected role and current default when rejoining.
	if e = repo.SetOrganizationMember(ctx, system.ID, company.ID, existing.ID, org.Developer, nil, false, true); e != nil {
		t.Fatal(e)
	}
	restored, e := repo.CreateOrganizationInvite(ctx, owner.ID, company.ID, org.ContentOfficer)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.OrganizationInvitation(ctx, existing.ID, restored.Token, true); e != nil {
		t.Fatal(e)
	}
	db.QueryRow(`SELECT role FROM organization_members WHERE user_id=$1 AND organization_id=$2`, existing.ID, company.ID).Scan(&assigned)
	if assigned != org.ContentOfficer {
		t.Fatal("removed member ignored invitation role")
	}
	// Concurrent recipients cannot redeem one elevated invitation twice.
	one, two := user("Concurrent one"), user("Concurrent two")
	single, e := repo.CreateOrganizationInvite(ctx, owner.ID, company.ID, org.Manager)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, u := range []identity.User{one, two} {
		wg.Add(1)
		go func(u identity.User) {
			defer wg.Done()
			_, e := repo.OrganizationInvitation(ctx, u.ID, single.Token, true)
			results <- e
		}(u)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, org.ErrInvitationUnavailable) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("redemptions=%d", success)
	}
	for _, state := range []string{"revoked", "expired"} {
		i, e := repo.CreateOrganizationInvite(ctx, owner.ID, company.ID, org.Supermanager)
		if e != nil {
			t.Fatal(e)
		}
		if state == "revoked" {
			e = repo.RevokeOrganizationInvite(ctx, owner.ID, company.ID, i.ID)
		} else {
			_, e = db.Exec(`UPDATE organization_invites SET expires_at=NOW()-interval '1 second' WHERE id=$1`, i.ID)
		}
		if e != nil {
			t.Fatal(e)
		}
		if _, e = repo.OrganizationInvitation(ctx, user("Unavailable").ID, i.Token, true); !errors.Is(e, org.ErrInvitationUnavailable) {
			t.Fatal("unavailable grant accepted", e)
		}
	}
}
