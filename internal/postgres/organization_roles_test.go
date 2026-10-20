package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/references"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/google/uuid"
)

func TestOrganizationRoleBoundariesAndDelegatedKeys(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
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
	users := map[string]identity.User{}
	auth := NewAuthRepository(db)
	sys := NewSystemRepository(db)
	for _, role := range []string{"system_admin", org.Supermanager, org.Manager, org.ContentOfficer, org.Developer, org.MemberRole} {
		u, e := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", role, "")
		if e != nil {
			t.Fatal(e)
		}
		users[role] = u
	}
	admin := users["system_admin"].ID
	db.Exec(`UPDATE users SET is_system_admin=true WHERE id=$1`, admin)
	o, err := sys.CreateOrganization(ctx, admin, "Role boundaries", users[org.Supermanager].ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM generation_profits WHERE organization_id=$1`, `DELETE FROM reference_images WHERE owner_id=$1`, `DELETE FROM tasks WHERE owner_id=$1`, `DELETE FROM user_api_keys WHERE organization_id=$1`, `DELETE FROM organization_members WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, o.ID); e != nil {
				t.Error(e)
			}
		}
		for _, u := range users {
			db.Exec(`DELETE FROM users WHERE id=$1`, u.ID)
		}
	}()
	for role, u := range users {
		if role == "system_admin" || role == org.Supermanager {
			continue
		}
		if e := sys.SetOrganizationMember(ctx, admin, o.ID, u.ID, role, nil, true, false); e != nil {
			t.Fatal(e)
		}
	}
	super, manager, officer, developer, member := users[org.Supermanager].ID, users[org.Manager].ID, users[org.ContentOfficer].ID, users[org.Developer].ID, users[org.MemberRole].ID
	for _, target := range []string{super, officer, manager} {
		if e := sys.SetOrganizationMember(ctx, manager, o.ID, target, org.MemberRole, nil, true, false); !errors.Is(e, org.ErrForbidden) {
			t.Fatalf("manager changed privileged role: %v", e)
		}
	}
	if e := sys.SetOrganizationMember(ctx, super, o.ID, super, org.Manager, nil, true, false); !errors.Is(e, org.ErrConflict) {
		t.Fatalf("last supermanager demoted: %v", e)
	}
	for _, actor := range []string{officer, developer, member} {
		if e := sys.SetOrganizationMember(ctx, actor, o.ID, member, org.Developer, nil, true, false); !errors.Is(e, org.ErrForbidden) {
			t.Fatalf("lower role assigned role: %v", e)
		}
	}
	if e := sys.SetOrganizationMember(ctx, manager, o.ID, member, org.Developer, nil, true, false); e != nil {
		t.Fatal(e)
	}
	if e := sys.SetOrganizationMember(ctx, manager, o.ID, member, org.MemberRole, nil, true, false); e != nil {
		t.Fatal(e)
	}
	keys := NewAPIKeyRepository(db)
	issue := func(issuer, recipient string) (apikey.Key, string, error) {
		k, secret, e := apikey.Generate(apikey.CreateInput{Name: "Assigned", OrganizationID: o.ID, UserID: recipient, ResourceMode: "shared", StorageMode: "shared"}, time.Now())
		if e == nil {
			e = keys.Create(ctx, issuer, k, apikey.Hash(secret))
		}
		return k, secret, e
	}
	for _, pair := range [][2]string{{member, member}, {officer, officer}, {developer, officer}, {developer, manager}, {manager, super}, {manager, officer}} {
		if _, _, e := issue(pair[0], pair[1]); !errors.Is(e, apikey.ErrForbidden) {
			t.Fatalf("unauthorized issuance %v: %v", pair, e)
		}
	}
	key, secret, err := issue(developer, member)
	if err != nil {
		t.Fatal(err)
	}
	user, credential, err := keys.Authenticate(ctx, apikey.Hash(secret))
	if err != nil || user.ID != member || credential.OrganizationRole != org.MemberRole {
		t.Fatalf("wrong key recipient: %+v %+v %v", user, credential, err)
	}
	records, err := keys.List(ctx, member)
	if err != nil || len(records) != 1 || records[0].CreatedBy != developer || records[0].UserID != member {
		t.Fatalf("key attribution: %+v %v", records, err)
	}
	if e := keys.RevokeForActor(ctx, member, key.ID); !errors.Is(e, apikey.ErrForbidden) {
		t.Fatalf("member revoked key: %v", e)
	}
	if _, _, e := keys.Authenticate(ctx, apikey.Hash(secret)); e != nil {
		t.Fatal("denied revocation mutated key", e)
	}
	taskID := uuid.NewString()
	_, err = db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,api_key_id,status,provider,model,prompt,cost,provider_owner_id,storage_owner_id) VALUES($1,$2,$3,$4,'succeeded','mock','mock-image-v1','Sensitive prompt','{"actual_source":"free_provider"}',$5,$5)`, taskID, o.ID, member, key.ID, identity.SharedOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskRepository(db)
	memberCtx := identity.WithOrganizationRole(identity.WithUser(ctx, users[org.MemberRole]), o.ID, org.MemberRole)
	developerCtx := identity.WithOrganizationRole(identity.WithUser(ctx, users[org.Developer]), o.ID, org.Developer)
	if _, e := tasks.Get(memberCtx, taskID); e != nil {
		t.Fatal(e)
	}
	if _, e := tasks.Get(developerCtx, taskID); !errors.Is(e, core.ErrTaskNotFound) {
		t.Fatalf("developer read colleague content: %v", e)
	}
	if list, e := tasks.ListRecent(developerCtx, 100); e != nil || len(list) != 0 {
		t.Fatalf("developer journal leaked: %+v %v", list, e)
	}
	usage, e := keys.Usage(ctx, member, time.Now())
	if e != nil || len(usage) != 1 || usage[0].Tasks != 1 {
		t.Fatalf("recipient usage: %+v %v", usage, e)
	}
	if usage, e := keys.Usage(ctx, developer, time.Now()); e != nil || len(usage) != 0 {
		t.Fatalf("issuer billed for recipient: %+v %v", usage, e)
	}
	for _, actor := range []string{manager, officer, member, developer} {
		usage, e := keys.OrganizationUsage(ctx, actor, o.ID, time.Now())
		expected := 1
		if actor == developer {
			expected = 0
		}
		if e != nil || len(usage) != expected || (expected == 1 && usage[0].Tasks != 1) {
			t.Fatalf("organization usage scope for %s: %+v %v", actor, usage, e)
		}
	}
	if list, e := tasks.ListRecentPage(memberCtx, 1, 0); e != nil || len(list) != 1 {
		t.Fatalf("own journal first page: %+v %v", list, e)
	}
	if list, e := tasks.ListRecentPage(memberCtx, 1, 1); e != nil || len(list) != 0 {
		t.Fatalf("own journal next page: %+v %v", list, e)
	}
	refs := NewReferenceRepository(db)
	image := references.Image{ID: uuid.NewString(), Name: "Input", ContentType: "image/png", SizeBytes: 68, StorageKey: "fixture.png", CreatedAt: time.Now()}
	if e := refs.Insert(memberCtx, image); e != nil {
		t.Fatal(e)
	}
	if _, e := refs.Get(developerCtx, image.ID); !errors.Is(e, storage.ErrObjectNotFound) {
		t.Fatalf("developer read colleague input: %v", e)
	}
	officerCtx := identity.WithOrganizationRole(identity.WithUser(ctx, users[org.ContentOfficer]), o.ID, org.ContentOfficer)
	if _, e := refs.Get(officerCtx, image.ID); e != nil {
		t.Fatal(e)
	}
	if e := sys.SetOrganizationMember(ctx, super, o.ID, member, org.ContentOfficer, nil, true, false); e != nil {
		t.Fatal(e)
	}
	_, credential, e = keys.Authenticate(ctx, apikey.Hash(secret))
	if e != nil || credential.OrganizationRole != org.ContentOfficer {
		t.Fatalf("existing key retained stale role: %+v %v", credential, e)
	}
	if e := sys.SetOrganizationMember(ctx, manager, o.ID, member, org.MemberRole, nil, true, false); !errors.Is(e, org.ErrForbidden) {
		t.Fatal("manager changed newly privileged recipient", e)
	}
	if e := keys.RevokeForActor(ctx, developer, key.ID); !errors.Is(e, apikey.ErrForbidden) {
		t.Fatal("developer revoked newly privileged recipient key", e)
	}
	if e := sys.SetOrganizationMember(ctx, super, o.ID, member, org.MemberRole, nil, true, true); e != nil {
		t.Fatal(e)
	}
	if _, _, e := keys.Authenticate(ctx, apikey.Hash(secret)); !errors.Is(e, apikey.ErrNotFound) {
		t.Fatal("removed recipient key still works", e)
	}
}
