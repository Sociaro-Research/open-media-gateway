package postgres

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/auth"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestOrganizationSupportAndSharedContacts(t *testing.T) {
	ctx := context.Background()
	db := auditDatabase(t, true)
	people := NewAuthRepository(db)
	repo := NewSystemRepository(db)
	users := []identity.User{}
	for _, name := range []string{"Administrator", "Owner", "Manager", "Member", "Outsider"} {
		u, err := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", name, "")
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
	}
	admin, owner, manager, member, outsider := users[0].ID, users[1].ID, users[2].ID, users[3].ID, users[4].ID
	if _, err := db.Exec(`UPDATE users SET is_system_admin=true WHERE id=$1`, admin); err != nil {
		t.Fatal(err)
	}
	company, err := repo.CreateOrganization(ctx, admin, "Support", owner, uuid.NewString()+".example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SetUserRole(ctx, admin, manager, "client_manager"); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetOrganizationMember(ctx, admin, company.ID, member, org.MemberRole, nil, true, false); err != nil {
		t.Fatal(err)
	}
	contacts := []identity.Contact{{Label: "Telegram", URL: "https://t.me/support"}, {Label: "Email", URL: "mailto:support@example.test"}}
	for _, id := range []string{manager, member} {
		if _, err = people.UpdateProfile(ctx, id, auth.ProfileUpdate{Contacts: &contacts}); err != nil {
			t.Fatal(err)
		}
	}
	if got, e := repo.OrganizationSupport(ctx, member, company.ID); e != nil || got.ClientManager != nil {
		t.Fatal(got, e)
	}
	if err = repo.AssignClientManager(ctx, admin, company.ID, manager); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{member, owner, manager, admin} {
		got, e := repo.OrganizationSupport(ctx, actor, company.ID)
		if e != nil || got.ClientManager == nil || got.ClientManager.ID != manager || len(got.ClientManager.Contacts) != 2 {
			t.Fatal(actor, got, e)
		}
	}
	if _, err = repo.OrganizationSupport(ctx, outsider, company.ID); !errors.Is(err, org.ErrForbidden) {
		t.Fatal("outsider", err)
	}
	members, err := repo.OrganizationMembers(ctx, manager, company.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range members {
		if m.ID == member {
			found = true
			if len(m.Contacts) != 2 {
				t.Fatal(m)
			}
		}
	}
	if !found {
		t.Fatal("member missing")
	}
	if err = people.CreateSession(ctx, []byte("support-session"), member, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	signedIn, err := people.UserBySession(ctx, []byte("support-session"), time.Now())
	if err != nil || len(signedIn.Contacts) != 2 {
		t.Fatal(signedIn, err)
	}
	var subject string
	if err = db.QueryRow(`SELECT google_subject FROM users WHERE id=$1`, member).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	relogged, err := people.UpsertGoogleUser(ctx, subject, users[3].Email, "Updated Google name", "")
	if err != nil || len(relogged.Contacts) != 2 {
		t.Fatal(relogged, err)
	}
	again, err := people.UpsertGoogleUser(ctx, "unused", uuid.NewString()+"@example.test", "New", "")
	if err != nil || len(again.Contacts) != 0 {
		t.Fatal(again, err)
	}
	theme := "light"
	updated, err := people.UpdateProfile(ctx, member, auth.ProfileUpdate{Theme: &theme})
	if err != nil || len(updated.Contacts) != 2 {
		t.Fatal(updated, err)
	}
	empty := []identity.Contact{}
	if _, err = people.UpdateProfile(ctx, manager, auth.ProfileUpdate{Contacts: &empty}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.OrganizationSupport(ctx, member, company.ID)
	if err != nil || got.ClientManager == nil || len(got.ClientManager.Contacts) != 0 {
		t.Fatal(got, err)
	}
	if err = repo.AssignClientManager(ctx, admin, company.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, err = repo.OrganizationSupport(ctx, member, company.ID); err != nil || got.ClientManager != nil {
		t.Fatal(got, err)
	}
	if _, err = db.Exec(`UPDATE organization_members SET removed_at=NOW() WHERE organization_id=$1 AND user_id=$2`, company.ID, member); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.OrganizationSupport(ctx, member, company.ID); !errors.Is(err, org.ErrForbidden) {
		t.Fatal("removed member", err)
	}
}
