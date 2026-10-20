package postgres

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

func TestModelSettingsPersistenceGrantsAndAdmission(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	base, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "model_access_" + uuid.NewString()
	if _, err = base.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	defer base.Exec(`DROP SCHEMA "` + schema + `" CASCADE`)
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	auth := NewAuthRepository(db)
	admin, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), "admin@example.test", "Admin", "")
	if err != nil {
		t.Fatal(err)
	}
	member, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), "member@example.test", "Member", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=true WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	repo := NewSystemRepository(db)
	org, err := repo.CreateOrganization(ctx, admin.ID, "Allowed company", member.ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateOrganization(ctx, admin.ID, "Other company", member.ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	refs := []modelaccess.Ref{{Provider: "mock", Model: "video", Name: "Video"}, {Provider: "other", Model: "video", Name: "Other video"}}
	if err = repo.Ensure(ctx, refs); err != nil {
		t.Fatal(err)
	}
	all, err := repo.List(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatal(all, err)
	}
	first := all[0]
	on, off := true, false
	if !first.EnabledByDefault || first.PublicID != "mock/video" {
		t.Fatal("new model is not enabled with generated ID", first)
	}
	first, err = repo.Update(ctx, admin.ID, first.ID, modelaccess.Update{PublicID: "cinema", EnabledByDefault: &off, Version: first.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Update(ctx, member.ID, first.ID, modelaccess.Update{PublicID: "stolen", EnabledByDefault: &on, Version: first.Version}); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("non-admin changed model", err)
	}
	if _, err = repo.Update(ctx, admin.ID, first.ID, modelaccess.Update{PublicID: "other/video", EnabledByDefault: &off, Version: first.Version}); !errors.Is(err, system.ErrConflict) {
		t.Fatal("duplicate name accepted", err)
	}
	if _, err = repo.Update(ctx, admin.ID, first.ID, modelaccess.Update{PublicID: "stale", EnabledByDefault: &on, Version: 1}); !errors.Is(err, system.ErrConflict) {
		t.Fatal("stale version accepted", err)
	}
	if err = repo.Ensure(ctx, refs); err != nil {
		t.Fatal(err)
	}
	all, err = repo.List(ctx, "")
	if err != nil || all[0].PublicID != "cinema" || all[0].EnabledByDefault {
		t.Fatal("discovery reset edited settings", all, err)
	}
	access, err := repo.Organization(ctx, admin.ID, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	access.ModelIDs = []string{first.ID}
	access, err = repo.SaveOrganization(ctx, admin.ID, org.ID, access)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.SaveOrganization(ctx, member.ID, org.ID, access); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("company unlocked itself", err)
	}
	if _, err = repo.SaveOrganization(ctx, admin.ID, org.ID, modelaccess.Access{ModelIDs: []string{first.ID}, Version: 1}); !errors.Is(err, system.ErrConflict) {
		t.Fatal("stale grants overwrite", err)
	}
	allowed, err := repo.List(ctx, org.ID)
	if err != nil || !allowed[0].Granted {
		t.Fatal(allowed, err)
	}
	denied, err := repo.List(ctx, other.ID)
	if err != nil || denied[0].Granted {
		t.Fatal("grant leaked", denied, err)
	}
	tasks := NewTaskRepository(db)
	create := func(orgID, public, key string) (core.Task, error) {
		c := identity.WithOrganization(identity.WithUser(ctx, member), orgID, true)
		now := time.Now().UTC()
		task := core.Task{ID: uuid.NewString(), Status: core.TaskQueued, Provider: "mock", Model: "video", PublicModel: public, MediaType: "video", Prompt: "test", CreatedAt: now, UpdatedAt: now, OwnerID: orgID, OrganizationID: orgID, ActorID: member.ID}
		task, _, e := tasks.CreateOrGet(c, task, key, "identical-request")
		return task, e
	}
	if _, err = create(other.ID, "cinema", "denied"); !errors.Is(err, core.ErrModelUnavailable) {
		t.Fatal("direct admission bypassed disable", err)
	}
	var deniedRows int
	if err = db.QueryRow(`SELECT count(*) FROM tasks WHERE owner_id=$1`, other.ID).Scan(&deniedRows); err != nil || deniedRows != 0 {
		t.Fatal("denied admission persisted", deniedRows, err)
	}
	task, err := create(org.ID, "cinema", "accepted")
	if err != nil {
		t.Fatal(err)
	}
	if task.PublicModel != "cinema" {
		t.Fatal("missing accepted name")
	}
	first, err = repo.Update(ctx, admin.ID, first.ID, modelaccess.Update{PublicID: "cinema-v2", EnabledByDefault: &off, Version: first.Version})
	if err != nil {
		t.Fatal(err)
	}
	old, err := tasks.Get(identity.WithOrganization(identity.WithUser(ctx, member), org.ID, true), task.ID)
	if err != nil || old.PublicModel != "cinema" {
		t.Fatal("history renamed", old.PublicModel, err)
	}
	if _, err = create(org.ID, "cinema", "old-name"); !errors.Is(err, core.ErrModelUnavailable) {
		t.Fatal("stale public name passed transactional check", err)
	}
	replay, err := create(org.ID, "cinema-v2", "accepted")
	if err != nil || replay.ID != task.ID || replay.PublicModel != "cinema" {
		t.Fatal("rename broke idempotent original", replay, err)
	}
	access.ModelIDs = []string{}
	if _, err = repo.SaveOrganization(ctx, admin.ID, org.ID, access); err != nil {
		t.Fatal(err)
	}
	if _, err = create(org.ID, "cinema-v2", "revoked"); !errors.Is(err, core.ErrModelUnavailable) {
		t.Fatal("revoked grant accepted", err)
	}
	// Discovery must resolve a future generated-name collision once, then keep it.
	first, err = repo.Update(ctx, admin.ID, first.ID, modelaccess.Update{PublicID: "mock/future", EnabledByDefault: &on, Version: first.Version})
	if err != nil {
		t.Fatal(err)
	}
	refs = append(refs, modelaccess.Ref{Provider: "mock", Model: "future", Name: "Future"})
	for i := 0; i < 2; i++ {
		if err = repo.Ensure(ctx, refs); err != nil {
			t.Fatal(err)
		}
	}
	all, err = repo.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.Model == "future" && (m.PublicID != "mock/future-2" || !m.EnabledByDefault) {
			t.Fatal("unstable generated name", m)
		}
	}
	// Admission holds the model lock until its transaction finishes.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	check := core.Task{Provider: "mock", Model: "video", PublicModel: "mock/future"}
	if err = checkTaskModelAccess(ctx, tx, &check); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	changed := make(chan error, 1)
	go func() {
		_, e := repo.Update(ctx, admin.ID, first.ID, modelaccess.Update{PublicID: first.PublicID, EnabledByDefault: &off, Version: first.Version})
		changed <- e
	}()
	select {
	case e := <-changed:
		tx.Rollback()
		t.Fatal("disable did not wait for admission", e)
	case <-time.After(80 * time.Millisecond):
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-changed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disable stuck after admission")
	}
	var audits int
	if err = db.QueryRow(`SELECT count(*) FROM audit_events WHERE organization_id=$1 AND action='model_access'`, org.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("missing grant audit", audits, err)
	}
}
