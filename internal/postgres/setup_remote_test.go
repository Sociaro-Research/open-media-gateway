package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestRemoteConnectionPersistenceRetryDisconnectAndIsolation(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	db, e := Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = Migrate(db); e != nil {
		t.Fatal(e)
	}
	repo := NewSystemRepository(db)
	auth := NewAuthRepository(db)
	a, e := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Remote owner", "")
	if e != nil {
		t.Fatal(e)
	}
	b, e := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Other owner", "")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Exec(`DELETE FROM users WHERE id IN ($1,$2)`, a.ID, b.ID)
	db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, a.ID)
	before, _ := repo.Settings(ctx)
	defer db.Exec(`UPDATE system_settings SET setup_pending=$1`, before.SetupPending)
	next := remote.Record{Owner: a.ID, Active: true, Origin: "https://remote.example.test", OrganizationID: uuid.NewString(), OrganizationName: "Private company", UserID: uuid.NewString(), KeyID: uuid.NewString(), TokenHash: []byte("fixture-digest"), Ciphertext: []byte("fixture-encrypted-data")}
	saved, e := repo.SaveRemoteWorkspace(ctx, next, 0)
	if e != nil || saved.Version != 1 {
		t.Fatal(saved, e)
	}
	replay, e := repo.SaveRemoteWorkspace(ctx, next, 0)
	if e != nil || replay.Version != 1 {
		t.Fatal("lost reply created another version", replay, e)
	}
	if _, e = repo.RemoteWorkspace(ctx, b.ID); !errors.Is(e, remote.ErrNotFound) {
		t.Fatal("foreign owner read connection", e)
	}
	forged := next
	forged.Owner = b.ID
	if _, e = repo.SaveRemoteWorkspace(ctx, forged, 0); !errors.Is(e, system.ErrForbidden) {
		t.Fatal("non-admin saved connection", e)
	}
	rotated := next
	rotated.KeyID = uuid.NewString()
	rotated.TokenHash = []byte("rotated-fixture-digest")
	rotated.Ciphertext = []byte("rotated-fixture-ciphertext")
	rotation, e := repo.SaveRemoteWorkspace(ctx, rotated, 1)
	if e != nil || rotation.Version != 2 || rotation.KeyID == saved.KeyID {
		t.Fatal("key rotation failed", rotation, e)
	}
	if _, e = repo.SaveRemoteWorkspace(ctx, next, 1); !errors.Is(e, remote.ErrConflict) {
		t.Fatal("old key overwrote rotation", e)
	}
	disconnected, e := repo.DisconnectRemoteWorkspace(ctx, a.ID, 2)
	if e != nil || disconnected.Active || disconnected.Ciphertext != nil || disconnected.Version != 3 {
		t.Fatal(disconnected, e)
	}
	if _, e = repo.SaveRemoteWorkspace(ctx, next, 0); !errors.Is(e, remote.ErrConflict) {
		t.Fatal("late connection resurrected disconnected state", e)
	}
	again, e := repo.DisconnectRemoteWorkspace(ctx, a.ID, 2)
	if e != nil || again.Version != 3 {
		t.Fatal("disconnect retry is not idempotent", again, e)
	}
	state, _ := repo.Settings(ctx)
	if state.SharedResources != before.SharedResources || state.PersonalProviders != before.PersonalProviders || state.MarkupPercent != before.MarkupPercent {
		t.Fatal("remote connection changed local funding policy")
	}
}

func TestSetupDisableSerializesWithWorkAdmission(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	db, e := Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = Migrate(db); e != nil {
		t.Fatal(e)
	}
	repo := NewSystemRepository(db)
	auth := NewAuthRepository(db)
	a, e := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Setup owner", "")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, a.ID)
	db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, a.ID)
	before, _ := repo.Settings(ctx)
	defer db.Exec(`UPDATE system_settings SET shared_resources=$1,personal_providers=$2,setup_pending=$3`, before.SharedResources, before.PersonalProviders, before.SetupPending)
	for i := 0; i < 12; i++ {
		db.Exec(`UPDATE system_settings SET shared_resources=TRUE WHERE id=TRUE`)
		settings, e := repo.Settings(ctx)
		if e != nil {
			t.Fatal(e)
		}
		settings.SharedResources = false
		id := uuid.NewString()
		start := make(chan struct{})
		var wg sync.WaitGroup
		var admission, disable error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			requestCtx := system.WithAdmissionCheck(identity.WithResourceOwners(identity.WithOwnerID(ctx, a.ID), identity.SharedOwnerID, identity.SharedOwnerID))
			tx, e := db.BeginTx(requestCtx, nil)
			if e != nil {
				admission = e
				return
			}
			defer tx.Rollback()
			p, e := lockAdmissionPolicy(requestCtx, tx)
			if e == nil {
				e = checkAdmissionPolicy(requestCtx, tx, p)
			}
			if e == nil {
				_, e = tx.Exec(`INSERT INTO tasks(id,status,provider,model,prompt,owner_id,provider_owner_id,storage_owner_id) VALUES($1,'queued','mock','mock-image-v1','fixture',$2,$3,$3)`, id, a.ID, identity.SharedOwnerID)
			}
			if e == nil {
				e = tx.Commit()
			}
			admission = e
		}()
		go func() { defer wg.Done(); <-start; _, disable = repo.SaveSettings(ctx, a.ID, settings) }()
		close(start)
		wg.Wait()
		db.Exec(`DELETE FROM tasks WHERE id=$1`, id)
		if admission == nil && disable == nil {
			t.Fatal("profile disable and new work both committed")
		}
		if disable != nil && !errors.Is(disable, system.ErrSetupBusy) {
			t.Fatal(disable)
		}
	}
}

// Exercise an actual upgrade and both profile directions with nonempty records,
// rather than treating unchanged booleans as evidence that customer data survived.
func TestSetupUpgradeAndSwitchPreserveResources(t *testing.T) {
	db := auditDatabase(t, false)
	ctx := context.Background()
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 72); err != nil {
		t.Fatal(err)
	}
	owner, companyID, taskID, connectionID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,email,display_name,is_system_admin) VALUES($1,'setup-preservation@fixture.test','Owner',TRUE)`, owner)
	exec(`INSERT INTO organizations(id,name,domain,credit_limit_micros,default_weekly_limit_micros) VALUES($1,'Kept company','kept.fixture.test',123456,45678)`, companyID)
	exec(`INSERT INTO organization_members(organization_id,user_id,role,weekly_limit_micros) VALUES($1,$2,'admin',23456)`, companyID, owner)
	exec(`INSERT INTO organization_credits(id,organization_id,actor_id,amount_micros,kind,external_id) VALUES($1,$2,$3,5000000,'payment','kept-credit')`, uuid.NewString(), companyID, owner)
	exec(`INSERT INTO provider_settings(owner_id,provider_id,enabled,credentials_ciphertext,credential_source,name) VALUES($1,'mock',TRUE,$2,'console','Kept personal connection')`, owner, []byte("synthetic-encrypted-provider-secret"))
	exec(`INSERT INTO storage_connections(owner_id,id,config_ciphertext) VALUES($1,$2,$3)`, owner, connectionID, []byte("synthetic-encrypted-storage-secret"))
	exec(`INSERT INTO storage_settings(owner_id,driver,connection_id) VALUES($1,'s3',$2)`, owner, connectionID)
	exec(`INSERT INTO tasks(id,status,provider,model,prompt,owner_id,actor_id,provider_owner_id,storage_owner_id,storage_key) VALUES($1,'succeeded','mock','mock-image-v1','Kept prompt',$2,$3,$4,$4,'fixture/result.png')`, taskID, companyID, owner, identity.SharedOwnerID)
	exec(`INSERT INTO reference_images(owner_id,id,name,content_type,size_bytes,storage_key,actor_id) VALUES($1,$2,'Kept file','image/png',123,'fixture/reference.png',$3)`, companyID, uuid.NewString(), owner)
	exec(`UPDATE system_settings SET shared_resources=TRUE,personal_providers=TRUE,generation_ui=FALSE,allow_inline_references=FALSE,markup_basis_points=1200`)
	snapshot := func() string {
		t.Helper()
		var result string
		for _, table := range []string{"organizations", "organization_members", "organization_credits", "provider_settings", "storage_connections", "storage_settings", "tasks", "reference_images"} {
			var rows string
			row := "to_jsonb(t)"
			if table == "reference_images" {
				row = "(to_jsonb(t)-'deleting'-'template_published'-'storage_owner_id')"
			}
			if table == "tasks" {
				// The combined Templates migration adds only nullable metadata.
				row = "(to_jsonb(t)-'template_snapshot')"
			}
			if err := db.QueryRow(`SELECT COALESCE(jsonb_agg(` + row + ` ORDER BY ` + row + `::text),'[]')::text FROM ` + table + ` t`).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			result += table + rows
		}
		return result
	}
	before := snapshot()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var snapshots int
	if err := db.QueryRow(`SELECT count(*) FROM tasks WHERE template_snapshot IS NOT NULL`).Scan(&snapshots); err != nil || snapshots != 0 {
		t.Fatal("upgrade populated existing task template metadata", snapshots, err)
	}
	repo := NewSystemRepository(db)
	state, err := repo.Settings(ctx)
	if err != nil || state.SetupPending || !state.PersonalProviders || !state.SharedResources || state.GenerationUI || state.AllowInlineReferences || state.MarkupPercent != 1200 {
		t.Fatal("upgrade changed existing policy", state, err)
	}
	for _, shared := range []bool{false, true} {
		state.SharedResources = shared
		state.PersonalProviders = !shared
		state, err = repo.SaveSettings(ctx, owner, state)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot() != before {
			t.Fatal("profile switch changed credentials, companies, funding, limits, history or file metadata")
		}
	}
}
