package postgres

import (
	"context"
	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestOrganizationMigrationPreservesOwnershipAndExistingKeys(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	base, e := Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer base.Close()
	schema := "org_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = base.Exec(`CREATE SCHEMA ` + schema); e != nil {
		t.Fatal(e)
	}
	defer base.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, e := Open(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	goose.SetBaseFS(migrations)
	if e = goose.SetDialect("postgres"); e != nil {
		t.Fatal(e)
	}
	if e = goose.UpTo(db, "migrations", 17); e != nil {
		t.Fatal(e)
	}
	// Seed the historical schema directly; the current repository expects all migrations.
	user := identity.User{ID: uuid.NewString()}
	_, e = db.Exec(`INSERT INTO users(id,google_subject,email,display_name,created_at,updated_at) VALUES($1,$2,'migration@example.test','Migrated user',NOW(),NOW())`, user.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	key, task, ref := uuid.NewString(), uuid.NewString(), uuid.NewString()
	secret := "gw_migration_fixture"
	if _, e = db.Exec(`INSERT INTO user_api_keys(id,user_id,name,prefix,token_hash,scopes,resource_mode,storage_mode) VALUES($1,$2,'Original key','gw_fixture',$3,'["generation"]','shared','shared')`, key, user.ID, apikey.Hash(secret)); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO tasks(id,owner_id,api_key_id,status,provider,model,prompt,provider_owner_id,storage_owner_id,cost,storage_key) VALUES($1,$2,$3,'succeeded','bytedance','seedream','Original prompt',$4,$4,'{"expected_usd":0.04,"metered_usd":0.04}', $5)`, task, user.ID, key, identity.SharedOwnerID, user.ID+"/results/old.png"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO reference_images(owner_id,id,name,content_type,size_bytes,storage_key) VALUES($1,$2,'Original reference','image/png',68,$3)`, user.ID, ref, "references/"+user.ID+"/old.png"); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(db); e != nil {
		t.Fatal(e)
	}
	_, credential, e := NewAPIKeyRepository(db).Authenticate(ctx, apikey.Hash(secret))
	if e != nil || credential.OrganizationID != user.ID || !credential.OrganizationAdmin {
		t.Fatalf("key lost access: %+v %v", credential, e)
	}
	orgs, e := NewSystemRepository(db).Organizations(ctx, user.ID, false)
	if e != nil || len(orgs) != 1 || orgs[0].Balance != 0 || orgs[0].Available != 0 || orgs[0].Role != "supermanager" {
		t.Fatalf("migration created false debt or credits: %+v %v", orgs, e)
	}
	scope := identity.WithOrganization(identity.WithUser(ctx, user), user.ID, true)
	original, e := NewTaskRepository(db).Get(scope, task)
	if e != nil || original.ActorID != user.ID || original.Prompt != "Original prompt" || original.Cost == nil || *original.StorageKey != user.ID+"/results/old.png" {
		t.Fatal("task metadata changed", original, e)
	}
	image, e := NewReferenceRepository(db).Get(scope, ref)
	if e != nil || image.StorageKey != "references/"+user.ID+"/old.png" {
		t.Fatal("reference path changed", image, e)
	}
}
