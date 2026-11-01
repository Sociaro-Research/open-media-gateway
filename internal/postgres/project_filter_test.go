package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

func TestUnassignedProjectFilterBeforePaginationAndIsolation(t *testing.T) {
	db := auditDatabase(t, true)
	r := NewTaskRepository(db)
	alice, bob, company, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{alice, bob} {
		if _, err := db.Exec(`INSERT INTO users(id,google_subject,email,display_name) VALUES($1,$2,$3,'Fixture')`, id, id, id+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{company, foreign} {
		if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Fixture')`, id); err != nil {
			t.Fatal(err)
		}
	}
	ctx := func(actor, owner, role string) context.Context {
		return identity.WithOrganizationRole(identity.WithOwnerID(context.Background(), actor), owner, role)
	}
	a := ctx(alice, company, "supermanager")
	first, err := r.CreateProject(a, "First")
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.CreateProject(a, "Second")
	if err != nil {
		t.Fatal(err)
	}
	// The first unassigned row is older than 60 assigned rows. There are also
	// more than one page of unassigned rows, across both company authors.
	unassigned, firstIDs, secondIDs := []string{}, []string{}, []string{}
	for i := 0; i < 125; i++ {
		id, project, actor := uuid.NewString(), "", alice
		if i < 30 {
			project = first.ID
			firstIDs = append(firstIDs, id)
		} else if i < 60 {
			project = second.ID
			secondIDs = append(secondIDs, id)
		} else {
			unassigned = append(unassigned, id)
			if i%2 == 0 {
				actor = bob
			}
		}
		if _, err := db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,provider_owner_id,storage_owner_id,status,provider,model,media_type,prompt,project_id,created_at) VALUES($1,$2,$3,$2,$2,'succeeded','mock','mock-video-v1','video',$4,$5,$6)`, id, company, actor, fmt.Sprintf("Retained %d", i), nullableString(project), time.Unix(2000-int64(i), 0)); err != nil {
			t.Fatal(err)
		}
	}
	insertForeign := func(owner string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,provider_owner_id,storage_owner_id,status,provider,model,media_type,prompt) VALUES($1,$2,$3,$2,$2,'succeeded','mock','mock-video-v1','video','Foreign')`, uuid.NewString(), owner, alice); err != nil {
			t.Fatal(err)
		}
	}
	insertForeign(foreign)
	personal := identity.WithPersonalData(identity.WithOwnerID(context.Background(), alice))
	insertForeign(identity.OwnerID(personal))
	snapshot := func() string {
		t.Helper()
		var s string
		if err := db.QueryRow(`SELECT json_agg(t ORDER BY id)::text FROM tasks t`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()
	check := func(c context.Context, filter string, limit, offset int, want []string) {
		t.Helper()
		tasks, err := r.ListProjectTasks(c, filter, limit, offset)
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, task := range tasks {
			ids = append(ids, task.ID)
			if filter == core.NoProjectFilter && task.ProjectID != "" {
				t.Fatal("assigned row in No project")
			}
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("filter=%s offset=%d got %v want %v", filter, offset, ids, want)
		}
	}
	check(a, core.NoProjectFilter, 50, 0, unassigned[:50])
	check(a, core.NoProjectFilter, 50, 50, unassigned[50:])
	check(a, core.NoProjectFilter, 50, 65, []string{})
	check(a, first.ID, 50, 0, firstIDs)
	check(a, second.ID, 50, 0, secondIDs)
	all, err := r.ListRecentPage(a, 100, 0)
	if err != nil || len(all) != 100 {
		t.Fatal("All first page", len(all), err)
	}
	allNext, err := r.ListRecentPage(a, 100, 100)
	if err != nil || len(allNext) != 25 {
		t.Fatal("All second page", len(allNext), err)
	}
	own := []string{}
	for i, id := range unassigned {
		if (i+60)%2 != 0 {
			own = append(own, id)
		}
	}
	check(ctx(alice, company, "member"), core.NoProjectFilter, 100, 0, own)
	check(ctx(alice, company, "developer"), core.NoProjectFilter, 100, 0, own)
	check(ctx(bob, company, "content_officer"), core.NoProjectFilter, 100, 0, unassigned)
	check(personal, core.NoProjectFilter, 50, 1, []string{})
	check(ctx(bob, foreign, "supermanager"), core.NoProjectFilter, 50, 1, []string{})
	if got, err := r.ListProjectTasks(identity.WithPersonalData(identity.WithOwnerID(context.Background(), bob)), core.NoProjectFilter, 50, 0); err != nil || len(got) != 0 {
		t.Fatal("empty personal filter", got, err)
	}
	if _, err := r.ListProjectTasks(ctx(alice, foreign, "supermanager"), first.ID, 50, 0); !errors.Is(err, core.ErrProjectInvalid) {
		t.Fatal("foreign project", err)
	}
	// Listing never changes rows; the sentinel cannot be assigned or managed.
	if snapshot() != before {
		t.Fatal("filter changed task data")
	}
	if _, err := r.AssignTaskProject(a, unassigned[0], core.NoProjectFilter, 0); !errors.Is(err, core.ErrProjectInvalid) {
		t.Fatal("filter accepted as assignment", err)
	}
	if _, err := r.RenameProject(a, core.NoProjectFilter, "Rename", 1); !errors.Is(err, core.ErrProjectInvalid) {
		t.Fatal("filter accepted as project", err)
	}
	if err := r.DeleteProject(a, core.NoProjectFilter, 1); !errors.Is(err, core.ErrProjectInvalid) {
		t.Fatal("filter accepted as deletion", err)
	}
	if snapshot() != before {
		t.Fatal("rejected filter mutation changed data")
	}
}
