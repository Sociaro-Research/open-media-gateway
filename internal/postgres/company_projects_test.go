package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestCompanyProjectsMigrationSharingAndIsolation(t *testing.T) {
	db := auditDatabase(t, false)
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 76); err != nil {
		t.Fatal(err)
	}
	company, foreign := uuid.NewString(), uuid.NewString()
	alice, bob := uuid.NewString(), uuid.NewString()
	for _, id := range []string{company, foreign} {
		if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Fixture company')`, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []string{alice, bob} {
		if _, err := db.Exec(`INSERT INTO users(id,google_subject,email,display_name) VALUES($1,$2,$3,'Fixture')`, user, user, user+"@example.test"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'supermanager')`, company, user); err != nil {
			t.Fatal(err)
		}
	}
	ctx := func(actor, owner, role string) context.Context {
		return identity.WithOrganizationRole(identity.WithOwnerID(context.Background(), actor), owner, role)
	}
	a, b := ctx(alice, company, "supermanager"), ctx(bob, company, "supermanager")
	r := NewTaskRepository(db)
	first, second := uuid.NewString(), uuid.NewString()
	for i, id := range []string{first, second} {
		actor := alice
		if i == 1 {
			actor = bob
		}
		if _, err := db.Exec(`INSERT INTO generation_projects(id,owner_id,actor_id,name,created_at) VALUES($1,$2,$3,'Existing name',$4)`, id, company, actor, time.Unix(int64(i+1), 0)); err != nil {
			t.Fatal(err)
		}
	}
	oldTask := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,provider_owner_id,storage_owner_id,status,provider,model,media_type,prompt,project_id,project_version) VALUES($1,$2,$3,$2,$2,'succeeded','mock','mock-video-v1','video','Retained payload',$4,7)`, oldTask, company, alice, second); err != nil {
		t.Fatal(err)
	}
	var beforeProjects, beforeTasks string
	if err := db.QueryRow(`SELECT json_agg(x ORDER BY id)::text FROM generation_projects x`).Scan(&beforeProjects); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT row_to_json(t)::text FROM tasks t WHERE id=$1`, oldTask).Scan(&beforeTasks); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	// Index-retaining rollback and re-upgrade remain idempotent without data rewrites.
	if err := goose.DownTo(db, "migrations", 76); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal("re-upgrade after rollback", err)
	}
	var afterProjects, afterTasks string
	db.QueryRow(`SELECT json_agg(x ORDER BY id)::text FROM generation_projects x`).Scan(&afterProjects)
	db.QueryRow(`SELECT row_to_json(t)::text FROM tasks t WHERE id=$1`, oldTask).Scan(&afterTasks)
	if beforeProjects != afterProjects || beforeTasks != afterTasks {
		t.Fatal("76→77 rewrote existing project or task")
	}
	for _, c := range []context.Context{a, b} {
		list, err := r.ListProjects(c)
		if err != nil || len(list) != 2 {
			t.Fatal("legacy projects not shared", list, err)
		}
		retry, err := r.CreateProject(c, "EXISTING NAME")
		if err != nil || retry.ID != first {
			t.Fatal("duplicate retry not deterministic", retry, err)
		}
	}
	// Two keys with the same recipient plus a key issued to another recipient.
	keys := NewAPIKeyRepository(db)
	for i, recipient := range []string{bob, bob, alice} {
		k, secret, e := apikey.Generate(apikey.CreateInput{Name: "Fixture", UserID: recipient, OrganizationID: company, ResourceMode: "shared"}, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		if e = keys.Create(context.Background(), alice, k, apikey.Hash(secret)); e != nil {
			t.Fatal(e)
		}
		user, loaded, e := keys.Authenticate(context.Background(), apikey.Hash(secret))
		if e != nil || user.ID != recipient {
			t.Fatal("key recipient", e)
		}
		keyctx := identity.WithAPIKeyID(ctx(user.ID, loaded.OrganizationID, loaded.OrganizationRole), loaded.ID)
		if rows, e := r.ListProjects(keyctx); e != nil || len(rows) != 2 {
			t.Fatal("company list depends on key or recipient", i, e)
		}
	}
	assigned, err := r.AssignTaskProject(b, oldTask, first, 7)
	if err != nil || assigned.ProjectID != first || assigned.Version != 8 {
		t.Fatal("other author assignment", assigned, err)
	}
	list, err := r.ListProjectTasks(b, first, 50, 0)
	if err != nil || len(list) != 1 || list[0].ActorID != alice {
		t.Fatal("shared project history", list, err)
	}
	if _, err = r.AssignTaskProject(a, oldTask, second, 7); err == nil {
		t.Fatal("stale write accepted")
	}
	// Role-filtered content reads stay unchanged even though company grouping is shared.
	member := ctx(bob, company, "member")
	if rows, err := r.ListProjectTasks(member, first, 50, 0); err != nil || len(rows) != 0 {
		t.Fatal("project widened content read", rows, err)
	}
	if _, err = r.AssignTaskProject(member, oldTask, second, 8); err != nil {
		t.Fatal("company metadata incorrectly author-scoped", err)
	}
	for _, role := range []string{"admin", "content_officer"} {
		c := ctx(bob, company, role)
		if _, err = r.CreateProject(c, "Denied"); !errors.Is(err, core.ErrProjectForbidden) {
			t.Fatal(role, err)
		}
		if _, err = r.AssignTaskProject(c, oldTask, "", 9); !errors.Is(err, core.ErrProjectForbidden) {
			t.Fatal(role, err)
		}
		if err = r.DeleteProject(c, first, 1); !errors.Is(err, core.ErrProjectForbidden) {
			t.Fatal(role, err)
		}
	}
	other := ctx(bob, foreign, "supermanager")
	if rows, err := r.ListProjects(other); err != nil || len(rows) != 0 {
		t.Fatal("cross company list", rows, err)
	}
	if _, err = r.AssignTaskProject(other, oldTask, first, 9); !errors.Is(err, core.ErrProjectInvalid) {
		t.Fatal("cross company project", err)
	}
	foreignProject, err := r.CreateProject(other, "Foreign")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.AssignTaskProject(other, oldTask, foreignProject.ID, 9); !errors.Is(err, core.ErrTaskNotFound) {
		t.Fatal("cross company generation", err)
	}
	if _, err = r.RenameProject(b, second, "New company name", 1); err != nil {
		t.Fatal("rename another creator project", err)
	}
	// Deletion clears all company tags while retaining generation content and author.
	if err = r.DeleteProject(a, second, 2); err != nil {
		t.Fatal(err)
	}
	var prompt, actor string
	var count int
	if err = db.QueryRow(`SELECT prompt,actor_id::text FROM tasks WHERE id=$1`, oldTask).Scan(&prompt, &actor); err != nil || prompt != "Retained payload" || actor != alice {
		t.Fatal("delete damaged generation", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM tasks WHERE id=$1 AND project_id IS NULL AND project_version=10`, oldTask).Scan(&count); err != nil || count != 1 {
		t.Fatal("tag not cleared", err, count)
	}
	// Concurrent company users converge without a new unique index rewriting old duplicates.
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := a
			if i%2 == 1 {
				c = b
			}
			p, e := r.CreateProject(c, "Concurrent company project")
			ids <- p.ID
			errs <- e
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	for got := range ids {
		if id == "" {
			id = got
		}
		if id != got {
			t.Fatal("concurrent duplicate", id, got)
		}
	}
}
