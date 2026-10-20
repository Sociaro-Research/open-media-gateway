package postgres

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/google/uuid"
	"os"
	"sync"
	"testing"
	"time"
)

func TestOrganizationsPermissionsInvitesAndConcurrentBudgets(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires an isolated PostgreSQL database")
	}
	ctx := context.Background()
	db, e := Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = Migrate(db); e != nil {
		t.Fatal(e)
	}
	auth := NewAuthRepository(db)
	users := []identity.User{}
	for _, name := range []string{"System", "Alice", "Bob", "Outside"} {
		u, e := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		users = append(users, u)
	}
	system, alice, bob, outside := users[0], users[1], users[2], users[3]
	if _, e = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, system.ID); e != nil {
		t.Fatal(e)
	}
	repo := NewSystemRepository(db)
	o, e := repo.CreateOrganization(ctx, system.ID, "Team", alice.ID, uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateOrganization(ctx, system.ID, "Other", "  ", uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		for _, id := range []string{o.ID, other.ID} {
			for _, q := range []string{`DELETE FROM generation_profits WHERE organization_id=$1`, `DELETE FROM reference_images WHERE owner_id=$1`, `DELETE FROM stored_objects WHERE owner_id=$1`, `DELETE FROM tasks WHERE owner_id=$1`, `DELETE FROM user_api_keys WHERE organization_id=$1`, `DELETE FROM organization_invites WHERE organization_id=$1`, `DELETE FROM organization_credits WHERE organization_id=$1`, `DELETE FROM organization_members WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
				if _, e := db.Exec(q, id); e != nil {
					t.Error(e)
				}
			}
		}
		for _, u := range users {
			db.Exec(`DELETE FROM users WHERE id=$1`, u.ID)
		}
	})
	if members, err := repo.OrganizationMembers(ctx, system.ID, other.ID); err != nil || len(members) != 0 {
		t.Fatalf("organization without first admin: members=%v error=%v", members, err)
	}
	if _, err := repo.OrganizationAccess(ctx, outside.ID, other.ID); !errors.Is(err, org.ErrForbidden) {
		t.Fatalf("nonmember access to unassigned organization: %v", err)
	}
	if e = repo.SetOrganizationMember(ctx, system.ID, other.ID, outside.ID, "supermanager", nil, true, false); e != nil {
		t.Fatalf("assign first administrator later: %v", e)
	}
	if _, e = repo.CreateOrganization(ctx, alice.ID, "Forbidden", alice.ID, uuid.NewString()+".fixture.test"); e == nil {
		t.Fatal("org admin created organization")
	}
	if e = repo.SetOrganizationMember(ctx, alice.ID, o.ID, alice.ID, "member", nil, true, false); !errors.Is(e, org.ErrConflict) {
		t.Fatalf("last admin: %v", e)
	}
	invitation, e := repo.CreateOrganizationInvite(ctx, alice.ID, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.OrganizationInvitation(ctx, bob.ID, invitation.Token, false); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	success := make(chan string, 2)
	errs := make(chan error, 2)
	for _, user := range []string{bob.ID, outside.ID} {
		wg.Add(1)
		go func(user string) {
			defer wg.Done()
			_, e := repo.OrganizationInvitation(ctx, user, invitation.Token, true)
			if e == nil {
				success <- user
			} else {
				errs <- e
			}
		}(user)
	}
	wg.Wait()
	close(success)
	close(errs)
	var winner string
	count := 0
	for u := range success {
		winner = u
		count++
	}
	if count != 1 {
		t.Fatalf("accepted %d invitations", count)
	}
	for e := range errs {
		if !errors.Is(e, org.ErrInvitationUnavailable) {
			t.Fatal(e)
		}
	}
	// Ensure Bob is our test member regardless of which concurrent acceptance won.
	if winner != bob.ID {
		invite, e := repo.CreateOrganizationInvite(ctx, alice.ID, o.ID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = repo.OrganizationInvitation(ctx, bob.ID, invite.Token, true); e != nil {
			t.Fatal(e)
		}
	}
	zero := money.USD(0)
	weekly := money.USD(20 * money.Scale)
	if e = repo.SetOrganizationMember(ctx, alice.ID, o.ID, bob.ID, "supermanager", &weekly, true, false); e != nil {
		t.Fatal("org admin cannot appoint another admin", e)
	}
	if e = repo.SetOrganizationMember(ctx, bob.ID, o.ID, alice.ID, "member", &weekly, true, false); e != nil {
		t.Fatal(e)
	}
	if e = repo.SetOrganizationMember(ctx, bob.ID, o.ID, bob.ID, "member", &weekly, true, false); !errors.Is(e, org.ErrConflict) {
		t.Fatal("last admin demotion accepted", e)
	}
	if e = repo.OrganizationSettings(ctx, bob.ID, o.ID, "Team", money.USD(100*money.Scale), &zero, true); !errors.Is(e, org.ErrForbidden) {
		t.Fatal("org admin granted credit", e)
	}
	credit := org.Credit{Amount: money.USD(10 * money.Scale), Kind: "payment", Reason: "Test transfer", ExternalID: "bank-1"}
	if _, e = repo.AddOrganizationCredit(ctx, bob.ID, o.ID, credit); !errors.Is(e, org.ErrForbidden) {
		t.Fatal("org admin topped up", e)
	}
	if _, e = repo.AddOrganizationCredit(ctx, system.ID, o.ID, credit); e != nil {
		t.Fatal(e)
	}
	if _, e = repo.AddOrganizationCredit(ctx, system.ID, o.ID, credit); e != nil {
		t.Fatal(e)
	}
	credit.Amount++
	if _, e = repo.AddOrganizationCredit(ctx, system.ID, o.ID, credit); !errors.Is(e, org.ErrConflict) {
		t.Fatal("different repeated payment accepted", e)
	}
	keys := NewAPIKeyRepository(db)
	key, secret, e := apikey.Generate(apikey.CreateInput{OrganizationID: o.ID, Name: "Bob", ResourceMode: "shared", StorageMode: "shared"}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = keys.Create(ctx, bob.ID, key, apikey.Hash(secret)); e != nil {
		t.Fatal(e)
	}
	foreign, _, _ := apikey.Generate(apikey.CreateInput{OrganizationID: other.ID, Name: "Foreign"}, time.Now())
	if e = keys.Create(ctx, bob.ID, foreign, []byte(uuid.NewString())); e == nil {
		t.Fatal("foreign key accepted")
	}
	taskRepo := NewTaskRepository(db)
	bobCtx := identity.WithOrganization(identity.WithAPIKeyID(identity.WithUser(ctx, bob), key.ID), o.ID, true)
	bobCtx = identity.WithResourceOwners(bobCtx, identity.SharedOwnerID, identity.SharedOwnerID)
	newTask := func() core.Task {
		return core.Task{ID: uuid.NewString(), ActorID: bob.ID, OwnerID: o.ID, APIKeyID: &key.ID, ProviderOwnerID: identity.SharedOwnerID, StorageOwnerID: identity.SharedOwnerID, Status: core.TaskQueued, Provider: "bytedance", Model: "test", MediaType: "image", Prompt: "Private", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Cost: &core.TaskCost{ExpectedUSD: money.USD(6 * money.Scale)}}
	}
	created := make(chan core.Task, 2)
	fail := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, _, e := taskRepo.CreateOrGet(bobCtx, newTask(), uuid.NewString(), "request")
			if e == nil {
				created <- task
			} else {
				fail <- e
			}
		}()
	}
	wg.Wait()
	close(created)
	close(fail)
	var task core.Task
	count = 0
	for v := range created {
		task = v
		count++
	}
	if count != 2 {
		t.Fatalf("pending tasks blocked admissions: %d", count)
	}
	for e := range fail {
		if !errors.Is(e, core.ErrBudgetExceeded) {
			t.Fatal(e)
		}
	}
	list, e := repo.Organizations(ctx, bob.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(list) != 1 || list[0].Available != money.USD(10*money.Scale) || list[0].Balance != money.USD(10*money.Scale) {
		t.Fatalf("wallet %+v", list)
	}
	aliceCtx := identity.WithOrganization(identity.WithUser(ctx, alice), o.ID, false)
	if _, e = taskRepo.Get(aliceCtx, task.ID); e != nil {
		t.Fatal("member cannot read organization task", e)
	}
	if tasks, err := taskRepo.ListRecent(aliceCtx, 20); err != nil || len(tasks) != 2 {
		t.Fatalf("shared organization journal: %+v %v", tasks, err)
	}
	outsiderCtx := identity.WithOrganization(identity.WithUser(ctx, outside), other.ID, true)
	if _, e = taskRepo.Get(outsiderCtx, task.ID); !errors.Is(e, core.ErrTaskNotFound) {
		t.Fatal("cross org task access", e)
	}
	// Gross metered cost counts even if the provider charge is covered by a package.
	if e = taskRepo.updateCost(ctx, task.ID, func(task *core.Task) {
		v := money.USD(11 * money.Scale)
		zero := money.USD(0)
		task.Cost.MeteredUSD = &v
		task.Cost.ActualUSD = &zero
	}); e != nil {
		t.Fatal(e)
	}
	if e = taskRepo.MarkFailed(ctx, task.ID, "test artifact storage failed"); e != nil {
		t.Fatal(e)
	}
	list, e = repo.Organizations(ctx, bob.ID, false)
	if e != nil || list[0].Balance != -money.USD(money.Scale) {
		t.Fatal("overrun not retained", list, e)
	}
	if e = repo.OrganizationSettings(ctx, system.ID, o.ID, "Team", money.USD(5*money.Scale), &zero, true); e != nil {
		t.Fatal(e)
	}
	_, remaining, e := taskRepo.Budget(bobCtx, key.ID, time.Now())
	if e != nil || remaining == nil || *remaining != money.USD(4*money.Scale) {
		t.Fatal("credit limit", remaining, e)
	}
	weeklyTwelve := money.USD(12 * money.Scale)
	if e = repo.SetOrganizationMember(ctx, bob.ID, o.ID, bob.ID, "supermanager", &weeklyTwelve, true, false); e != nil {
		t.Fatal(e)
	}
	secondKey, secondSecret, e := apikey.Generate(apikey.CreateInput{OrganizationID: o.ID, Name: "Second Bob key", ResourceMode: "shared"}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = keys.Create(ctx, bob.ID, secondKey, apikey.Hash(secondSecret)); e != nil {
		t.Fatal(e)
	}
	if _, left, err := taskRepo.Budget(bobCtx, secondKey.ID, time.Now()); err != nil || left == nil || *left != money.USD(money.Scale) {
		t.Fatalf("weekly spending must span keys: %v %v", left, err)
	}
	// Member weekly caps apply across keys; a $0 cap blocks paid use despite credit.
	if e = repo.SetOrganizationMember(ctx, bob.ID, o.ID, bob.ID, "supermanager", &zero, true, false); e != nil {
		t.Fatal(e)
	}
	if _, _, e = taskRepo.CreateOrGet(bobCtx, newTask(), uuid.NewString(), "request"); !errors.Is(e, core.ErrBudgetExceeded) {
		t.Fatal("weekly cap ignored", e)
	}
	refRepo := NewReferenceRepository(db)
	ref := references.Image{ID: uuid.NewString(), Name: "Shared reference", ContentType: "image/png", SHA256: "shared-reference-hash", SizeBytes: 5, StorageKey: o.ID + "/references/test", CreatedAt: time.Now()}
	if e = refRepo.Insert(bobCtx, ref); e != nil {
		t.Fatal(e)
	}
	if _, e = refRepo.Get(aliceCtx, ref.ID); e != nil {
		t.Fatal("member cannot read organization reference", e)
	}
	if refs, err := refRepo.List(aliceCtx, 20, 0); err != nil || len(refs) != 1 || refs[0].ID != ref.ID {
		t.Fatalf("shared reference library: %+v %v", refs, err)
	}
	if duplicate, err := refRepo.FindByHash(aliceCtx, ref.SHA256); err != nil || duplicate.ID != ref.ID {
		t.Fatalf("cross-member duplicate detection: %+v %v", duplicate, err)
	}
	if _, err := refRepo.Get(outsiderCtx, ref.ID); err == nil {
		t.Fatal("cross-organization reference exposed")
	}

	// Preserve files and costs after membership removal; revoke old keys permanently.
	if e = repo.SetOrganizationMember(ctx, system.ID, o.ID, alice.ID, "supermanager", &weekly, true, false); e != nil {
		t.Fatal(e)
	}
	if e = repo.SetOrganizationMember(ctx, alice.ID, o.ID, bob.ID, "member", &weekly, true, true); e != nil {
		t.Fatal(e)
	}
	if _, _, e = keys.Authenticate(ctx, apikey.Hash(secret)); !errors.Is(e, apikey.ErrNotFound) {
		t.Fatal("removed member key works", e)
	}
	rejoin, e := repo.CreateOrganizationInvite(ctx, alice.ID, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.OrganizationInvitation(ctx, bob.ID, rejoin.Token, true); e != nil {
		t.Fatal(e)
	}
	if _, _, e = keys.Authenticate(ctx, apikey.Hash(secret)); !errors.Is(e, apikey.ErrNotFound) {
		t.Fatal("rejoining reactivated a revoked key", e)
	}
	members, e := repo.OrganizationMembers(ctx, alice.ID, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, member := range members {
		if member.ID == bob.ID && (member.Role != "member" || member.WeeklyLimit == nil || *member.WeeklyLimit != 0 || member.WeeklyCommitted != money.USD(11*money.Scale)) {
			t.Fatal("rejoining changed history or restored privileges", member)
		}
	}
	// All-time consumption survives a weekly reset; email filters retain organization scope.
	if _, e = db.Exec(`UPDATE tasks SET created_at=NOW()-INTERVAL '14 days' WHERE id=$1`, task.ID); e != nil {
		t.Fatal(e)
	}
	members, e = repo.OrganizationMembers(ctx, alice.ID, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, member := range members {
		if member.ID == bob.ID && (member.TotalConsumed != money.USD(11*money.Scale) || member.WeeklyCommitted != 0) {
			t.Fatalf("all-time vs weekly: %+v", member)
		}
	}
	reportRows := 0
	filter := core.ReportFilter{UserEmail: bob.Email, From: time.Now().AddDate(0, 0, -30), Until: time.Now().AddDate(0, 0, 1)}
	if e = taskRepo.WalkCostReport(identity.WithOrganization(aliceCtx, o.ID, true), filter, func(row core.CostReportRow) error { reportRows++; return nil }); e != nil || reportRows != 2 {
		t.Fatalf("email report: rows=%d error=%v", reportRows, e)
	}
	filter.UserEmail = alice.Email
	reportRows = 0
	if e = taskRepo.WalkCostReport(identity.WithOrganization(aliceCtx, o.ID, true), filter, func(row core.CostReportRow) error { reportRows++; return nil }); e != nil || reportRows != 0 {
		t.Fatalf("unrelated email report: rows=%d error=%v", reportRows, e)
	}
	adminCtx := identity.WithOrganization(identity.WithUser(ctx, alice), o.ID, true)
	if _, e = refRepo.Get(adminCtx, ref.ID); e != nil {
		t.Fatal("organization lost reference", e)
	}
	if _, e = taskRepo.Get(adminCtx, task.ID); e != nil {
		t.Fatal("organization lost task", e)
	}
}

func TestOrganizationNewMembersInheritDefaultWeeklyLimit(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires an isolated PostgreSQL database")
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
	auth, repo := NewAuthRepository(db), NewSystemRepository(db)
	positive, zero := money.USD(25*money.Scale), money.USD(0)
	for _, tc := range []struct {
		name  string
		limit *money.USD
	}{{"positive", &positive}, {"zero", &zero}, {"unlimited", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			users := make([]identity.User, 4)
			for i := range users {
				users[i], err = auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Default limit test", "")
				if err != nil {
					t.Fatal(err)
				}
			}
			admin, direct, invited, outsider := users[0], users[1], users[2], users[3]
			if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, admin.ID); err != nil {
				t.Fatal(err)
			}
			o, err := repo.CreateOrganization(ctx, admin.ID, "Default limit test", admin.ID, uuid.NewString()+".fixture.test")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				for _, q := range []string{`DELETE FROM organization_invites WHERE organization_id=$1`, `DELETE FROM organization_members WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
					if _, err := db.Exec(q, o.ID); err != nil {
						t.Error(err)
					}
				}
				for _, u := range users {
					if _, err := db.Exec(`DELETE FROM users WHERE id=$1`, u.ID); err != nil {
						t.Error(err)
					}
				}
			})
			if err = repo.OrganizationSettings(ctx, admin.ID, o.ID, o.Name, 0, tc.limit, false); err != nil {
				t.Fatal(err)
			}
			check := func(user string, want *money.USD) {
				t.Helper()
				members, err := repo.OrganizationMembers(ctx, admin.ID, o.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range members {
					if m.ID == user {
						if (m.WeeklyLimit == nil) != (want == nil) || want != nil && *m.WeeklyLimit != *want {
							t.Fatalf("member %s: limit %v, want %v", user, m.WeeklyLimit, want)
						}
						return
					}
				}
				t.Fatal("member missing")
			}
			// The assignment form supplies a role without an individual limit.
			if err = repo.SetOrganizationMember(ctx, admin.ID, o.ID, direct.Email, "supermanager", nil, false, false); err != nil {
				t.Fatal(err)
			}
			check(direct.ID, tc.limit)
			invitation, err := repo.CreateOrganizationInvite(ctx, admin.ID, o.ID)
			if err != nil {
				t.Fatal(err)
			}
			checkRecipient := func(email string) {
				t.Helper()
				items, err := repo.OrganizationInvites(ctx, admin.ID, o.ID)
				if err != nil || len(items) != 1 {
					t.Fatalf("invitation list: %+v, %v", items, err)
				}
				if items[0].ID != invitation.ID || items[0].AcceptedByEmail != email {
					t.Fatalf("invitation recipient: %+v, want email %q", items[0], email)
				}
				if email != "" && (items[0].AcceptedBy == nil || *items[0].AcceptedBy != invited.ID) {
					t.Fatalf("invitation recipient ID: %+v", items[0])
				}
			}
			checkRecipient("")
			if _, err = repo.OrganizationInvitation(ctx, invited.ID, invitation.Token, true); err != nil {
				t.Fatal(err)
			}
			checkRecipient(invited.Email)
			check(invited.ID, tc.limit)
			for _, accept := range []bool{false, true} {
				if _, err = repo.OrganizationInvitation(ctx, outsider.ID, invitation.Token, accept); !errors.Is(err, org.ErrInvitationUnavailable) {
					t.Fatalf("another user reused invitation: %v", err)
				}
			}
			if err = repo.SetOrganizationMember(ctx, admin.ID, o.ID, invited.ID, "supermanager", &positive, true, false); err != nil {
				t.Fatal(err)
			}
			var eventCount int
			if err = db.QueryRow(`SELECT count(*) FROM audit_events WHERE organization_id=$1`, o.ID).Scan(&eventCount); err != nil {
				t.Fatal(err)
			}
			for _, expired := range []bool{false, true} {
				if expired {
					if _, err = db.Exec(`UPDATE organization_invites SET expires_at=NOW()-INTERVAL '1 day', revoked_at=NOW() WHERE id=$1`, invitation.ID); err != nil {
						t.Fatal(err)
					}
					eventCount++ // The deliberate expiration/revocation itself is now audited.
				}
				for _, accept := range []bool{false, true} {
					info, err := repo.OrganizationInvitation(ctx, invited.ID, invitation.Token, accept)
					if err != nil || !info.AlreadyMember || info.OrganizationID != o.ID {
						t.Fatalf("reopen invitation: %+v, %v", info, err)
					}
					check(invited.ID, &positive)
					var role string
					if err = db.QueryRow(`SELECT role FROM organization_members WHERE organization_id=$1 AND user_id=$2`, o.ID, invited.ID).Scan(&role); err != nil || role != "supermanager" {
						t.Fatalf("role changed: %s, %v", role, err)
					}
				}
			}
			var after int
			if err = db.QueryRow(`SELECT count(*) FROM audit_events WHERE organization_id=$1`, o.ID).Scan(&after); err != nil || after != eventCount {
				t.Fatalf("reopening wrote events: %d -> %d, %v", eventCount, after, err)
			}
			if err = repo.SetOrganizationMember(ctx, admin.ID, o.ID, invited.ID, "member", nil, false, true); err != nil {
				t.Fatal(err)
			}
			checkRecipient(invited.Email)
			for _, accept := range []bool{false, true} {
				if _, err = repo.OrganizationInvitation(ctx, invited.ID, invitation.Token, accept); !errors.Is(err, org.ErrInvitationUnavailable) {
					t.Fatalf("removed member reused invitation: %v", err)
				}
			}

			// Explicit zero and null must remain distinct from an omitted limit.
			for _, explicit := range []*money.USD{&zero, nil, &positive} {
				if err = repo.SetOrganizationMember(ctx, admin.ID, o.ID, direct.ID, "member", explicit, true, false); err != nil {
					t.Fatal(err)
				}
				check(direct.ID, explicit)
				if err = repo.SetOrganizationMember(ctx, admin.ID, o.ID, direct.ID, "supermanager", nil, false, false); err != nil {
					t.Fatal(err)
				}
				check(direct.ID, explicit)
			}
			// Restoring a removed membership starts with the current default.
			if err = repo.SetOrganizationMember(ctx, admin.ID, o.ID, direct.ID, "member", nil, false, true); err != nil {
				t.Fatal(err)
			}
			if err = repo.SetOrganizationMember(ctx, admin.ID, o.ID, direct.ID, "member", nil, false, false); err != nil {
				t.Fatal(err)
			}
			check(direct.ID, tc.limit)
		})
	}
}
