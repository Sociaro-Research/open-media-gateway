package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestGenerationCursorPaginationAndCounts(t *testing.T) {
	db := auditDatabase(t, true)
	r := NewTaskRepository(db)
	alice, bob, company, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{alice, bob} {
		if _, err := db.Exec(`INSERT INTO users(id,google_subject,email,display_name) VALUES($1::uuid,$1::text,$2,'Fixture')`, id, id+"@example.test"); err != nil {
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
	c := ctx(alice, company, "supermanager")
	project, err := r.CreateProject(c, "Cursor fixture")
	if err != nil {
		t.Fatal(err)
	}
	ids, own, assigned, unassigned := []string{}, []string{}, []string{}, []string{}
	// Timestamp ties deliberately cross every page. Database microseconds and
	// descending UUIDs, rather than rounded public timestamps, define the walk.
	at := time.Date(2026, 10, 1, 12, 0, 0, 123456000, time.UTC)
	insert := func(id, actor, owner, projectID string, created time.Time) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,provider_owner_id,storage_owner_id,status,provider,model,media_type,prompt,project_id,created_at) VALUES($1,$2,$3,$2,$2,'succeeded','mock','mock-video-v1','video','Synthetic history',$4,$5)`, id, owner, actor, nullableString(projectID), created); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 43; i++ {
		id, actor, p := fmt.Sprintf("00000000-0000-4000-8000-%012d", 1000-i), alice, ""
		if i%2 == 0 {
			actor = bob
		} else {
			own = append(own, id)
		}
		if i < 20 {
			p = project.ID
			assigned = append(assigned, id)
		} else {
			unassigned = append(unassigned, id)
		}
		insert(id, actor, company, p, at)
		ids = append(ids, id)
	}
	insert(uuid.NewString(), alice, foreign, "", at.Add(time.Hour))
	personal := identity.WithPersonalData(identity.WithOwnerID(context.Background(), alice))
	insert(uuid.NewString(), alice, identity.OwnerID(personal), "", at)
	check := func(context context.Context, filter string, want []string) {
		t.Helper()
		q := core.GenerationPageQuery{Limit: 10, ProjectID: filter}
		got := []string{}
		for {
			page, err := r.ListGenerationPage(context, q)
			if err != nil || page.Total != int64(len(want)) || len(page.Tasks) > 10 {
				t.Fatalf("filter=%q total=%d tasks=%d err=%v", filter, page.Total, len(page.Tasks), err)
			}
			for _, task := range page.Tasks {
				got = append(got, task.ID)
			}
			if !page.HasMore {
				break
			}
			last := page.Tasks[len(page.Tasks)-1]
			q.Before = &core.GenerationPosition{CreatedAt: last.CreatedAt, ID: last.ID}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("filter=%q got=%v want=%v", filter, got, want)
		}
	}
	check(c, "", ids)
	check(c, project.ID, assigned)
	check(c, core.NoProjectFilter, unassigned)
	check(ctx(alice, company, "member"), "", own)
	check(ctx(alice, company, "developer"), "", own)
	check(ctx(bob, company, "content_officer"), "", ids)
	versions, err := r.ListGenerationPage(c, core.GenerationPageQuery{Limit: 100, VersionsOnly: true})
	if err != nil || len(versions.Tasks) != 0 || len(versions.Versions) != 43 || versions.Total != 43 {
		t.Fatal("lightweight inventory", versions, err)
	}
	firstRevision := versions.Versions[0].Revision
	// Same public timestamp, but every writer still advances the private revision.
	if _, err := db.Exec(`UPDATE tasks SET status='failed' WHERE id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	changed, err := r.ListGenerationPage(c, core.GenerationPageQuery{Limit: 50, IDs: []string{ids[0], uuid.NewString()}})
	if err != nil || len(changed.Tasks) != 1 || changed.Total != 43 || changed.Versions[0].Revision <= firstRevision || changed.Tasks[0].Status != core.TaskFailed {
		t.Fatal("changed ID payload and full filtered count", changed, err)
	}
	if _, err := db.Exec(`INSERT INTO stored_objects(owner_id,storage_key,task_id,size_bytes) VALUES($1,'synthetic-result',$2,1)`, company, ids[0]); err != nil {
		t.Fatal(err)
	}
	storageChange, err := r.ListGenerationPage(c, core.GenerationPageQuery{Limit: 50, IDs: []string{ids[0]}, SkipTotal: true})
	if err != nil || storageChange.Total != 0 || len(storageChange.Tasks) != 1 || !storageChange.Tasks[0].HasStoredResult || storageChange.Versions[0].Revision <= changed.Versions[0].Revision {
		t.Fatal("storage changes invalidate journal revision", storageChange, err)
	}
	foreignIDs, err := r.ListGenerationPage(ctx(alice, foreign, "supermanager"), core.GenerationPageQuery{Limit: 50, IDs: ids[:3]})
	if err != nil || len(foreignIDs.Tasks) != 0 || foreignIDs.Total != 1 {
		t.Fatal("ID reads never bypass owner filter", foreignIDs, err)
	}
	if _, err := r.ListGenerationPage(ctx(alice, foreign, "supermanager"), core.GenerationPageQuery{ProjectID: project.ID, Limit: 10}); !errors.Is(err, core.ErrProjectInvalid) {
		t.Fatal("foreign project", err)
	}
	if _, err := r.ListGenerationPage(c, core.GenerationPageQuery{ProjectID: "invalid", Limit: 10}); !errors.Is(err, core.ErrProjectInvalid) {
		t.Fatal("invalid project", err)
	}
	page, err := r.ListGenerationPage(c, core.GenerationPageQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	anchor := page.Tasks[9]
	position := &core.GenerationPosition{CreatedAt: anchor.CreatedAt, ID: anchor.ID}
	insert(uuid.NewString(), alice, company, "", at.Add(time.Microsecond))
	// Removing the boundary row must not invalidate its cursor or skip its
	// successor. The original first page can safely stay attached in the UI.
	if _, err := db.Exec(`DELETE FROM tasks WHERE id=$1`, anchor.ID); err != nil {
		t.Fatal(err)
	}
	next, err := r.ListGenerationPage(c, core.GenerationPageQuery{Limit: 10, Before: position})
	if err != nil || len(next.Tasks) != 10 || next.Tasks[0].ID != ids[10] || next.Total != 43 {
		t.Fatal("insert/delete stable continuation", next, err)
	}
	refresh, err := r.ListGenerationPage(c, core.GenerationPageQuery{Limit: 10, Through: position})
	if err != nil || len(refresh.Tasks) != 10 || refresh.HasMore || refresh.Total != 43 {
		t.Fatal("refresh range and full filter total", refresh, err)
	}
	last := &core.GenerationPosition{CreatedAt: at, ID: ids[len(ids)-1]}
	empty, err := r.ListGenerationPage(c, core.GenerationPageQuery{Limit: 10, Before: last})
	if err != nil || len(empty.Tasks) != 0 || empty.Total != 43 || empty.HasMore {
		t.Fatal("empty page retains exact full total", empty, err)
	}
	// Reassignment changes both filtered counts, without touching other scopes.
	if _, err := db.Exec(`UPDATE tasks SET project_id=NULL WHERE id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	filtered, err := r.ListGenerationPage(c, core.GenerationPageQuery{Limit: 10, ProjectID: project.ID})
	if err != nil || filtered.Total != 18 { // One deleted anchor and one moved row.
		t.Fatal("reassignment count", filtered.Total, err)
	}
	personalPage, err := r.ListGenerationPage(personal, core.GenerationPageQuery{Limit: 10})
	if err != nil || personalPage.Total != 1 {
		t.Fatal("personal isolation", personalPage.Total, err)
	}
	// Result ordering remains deterministic on an exact timestamp tie.
	got := []string{}
	for _, task := range next.Tasks {
		got = append(got, task.ID)
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i] > got[j] }) {
		t.Fatal("unstable UUID order", got)
	}
}

func TestGenerationCursorMigrationRollbackPreservesHistory(t *testing.T) {
	db := auditDatabase(t, true)
	id := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,provider_owner_id,storage_owner_id,status,provider,model,prompt) VALUES($1,$2,$2,$2,$2,'queued','mock','test-video','Preserved history')`, id, identity.SystemOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tasks SET prompt='Updated history' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := db.QueryRow(`SELECT to_jsonb(tasks)::text FROM tasks WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(db, "migrations", 86); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT to_jsonb(tasks)::text FROM tasks WHERE id=$1`, id).Scan(&after); err != nil || before != after {
		t.Fatal("rollback rewrote history/revision", err, before, after)
	}
	// A schema-86 writer remains compatible and can update ordinary tasks.
	if _, err := db.Exec(`UPDATE tasks SET status='failed' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT to_jsonb(tasks)::text FROM tasks WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT to_jsonb(tasks)::text FROM tasks WHERE id=$1`, id).Scan(&after); err != nil || before != after {
		t.Fatal("re-upgrade rewrote history/revision", err, before, after)
	}
	var revision int64
	if err := db.QueryRow(`UPDATE tasks SET prompt='After re-upgrade' WHERE id=$1 RETURNING journal_revision`, id).Scan(&revision); err != nil || revision != 3 {
		t.Fatal("re-upgrade did not resume revisions", revision, err)
	}
}
