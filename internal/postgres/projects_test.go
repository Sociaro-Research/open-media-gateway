package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestGenerationProjectsMigrationIsolationAndRetention(t *testing.T) {
	db := auditDatabase(t, false)
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 70); err != nil {
		t.Fatal(err)
	}
	people := NewAuthRepository(db)
	alice, err := people.UpsertGoogleUser(context.Background(), uuid.NewString(), "projects-a@example.test", "Alice", "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := people.UpsertGoogleUser(context.Background(), uuid.NewString(), "projects-b@example.test", "Bob", "")
	if err != nil {
		t.Fatal(err)
	}
	a := identity.WithPersonalData(identity.WithUser(context.Background(), alice))
	b := identity.WithPersonalData(identity.WithUser(context.Background(), bob))
	old := uuid.NewString()
	if _, err = db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,provider_owner_id,storage_owner_id,status,provider,model,media_type,prompt) VALUES($1,$2,$3,$2,$2,'succeeded','mock','mock-video-v1','video','older ungrouped')`, old, identity.OwnerID(a), alice.ID); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	r := NewTaskRepository(db)
	project, err := r.CreateProject(a, "  My film  ")
	if err != nil || project.Name != "My film" {
		t.Fatal(project, err)
	}
	same, err := r.CreateProject(a, "MY FILM")
	if err != nil || same.ID != project.ID {
		t.Fatal("uncertain create retry duplicated project", same, err)
	}
	empty, err := r.CreateProject(a, "Empty")
	if err != nil {
		t.Fatal(err)
	}
	if list, err := r.ListProjectTasks(a, empty.ID, 50, 0); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	for _, other := range []context.Context{b, identity.WithOrganizationRole(identity.WithUser(context.Background(), bob), identity.OwnerID(a), "admin"), identity.WithOrganizationRole(identity.WithUser(context.Background(), alice), uuid.NewString(), "admin")} {
		if list, err := r.ListProjects(other); err != nil || len(list) != 0 {
			t.Fatal("project disclosure", list, err)
		}
		if _, err := r.ListProjectTasks(other, project.ID, 50, 0); !errors.Is(err, core.ErrProjectInvalid) {
			t.Fatal("foreign project filter", err)
		}
	}
	newTask := func(projectID string) core.Task {
		return core.Task{ID: uuid.NewString(), ProjectID: projectID, ActorID: alice.ID, Status: core.TaskQueued, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "retained scene", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	}
	task := newTask(project.ID)
	saved, created, err := r.CreateOrGet(a, task, "projects-submit", "same")
	if err != nil || !created || saved.ProjectID != project.ID {
		t.Fatal(saved, created, err)
	}
	replay, created, err := r.CreateOrGet(a, newTask(project.ID), "projects-submit", "same")
	if err != nil || created || replay.ID != task.ID || replay.ProjectID != project.ID {
		t.Fatal(replay, created, err)
	}
	if _, _, err := r.CreateOrGet(b, newTask(project.ID), "foreign", "same"); !errors.Is(err, core.ErrProjectInvalid) {
		t.Fatal("cross-user assignment", err)
	}
	for _, id := range []string{"all", uuid.NewString()} {
		if _, _, err := r.CreateOrGet(a, newTask(id), "invalid", "same"); !errors.Is(err, core.ErrProjectInvalid) {
			t.Fatal("invalid project", err)
		}
	}
	// Reconstruct the repository to exercise persistence across process lifetimes.
	r = NewTaskRepository(db)
	if list, err := r.ListProjects(a); err != nil || len(list) != 2 {
		t.Fatal(list, err)
	}
	if list, err := r.ListProjectTasks(a, project.ID, 1, 0); err != nil || len(list) != 1 || list[0].ID != task.ID {
		t.Fatal(list, err)
	}
	if list, err := r.ListProjectTasks(a, project.ID, 1, 1); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	if list, err := r.ListRecent(a, 50); err != nil || len(list) != 2 {
		t.Fatal("All must preserve ungrouped", list, err)
	}
	if task, err := r.Get(a, old); err != nil || task.ProjectID != "" || task.Prompt != "older ungrouped" {
		t.Fatal("migration changed old generation", task, err)
	}
	if list, err := r.ListRecent(b, 50); err != nil || len(list) != 0 {
		t.Fatal("history isolation", list, err)
	}
	// Project metadata must not change organization logging or result-storage policy.
	sys := NewSystemRepository(db)
	if err = sys.SetAdmin(context.Background(), "", alice.ID, true, true); err != nil {
		t.Fatal(err)
	}
	org, err := sys.CreateOrganization(context.Background(), alice.ID, "Projects privacy", alice.ID, "projects.example.test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithOrganizationRole(identity.WithUser(context.Background(), alice), org.ID, "supermanager")
	if err = sys.SetOrganizationRequestLogging(ctx, alice.ID, org.ID, true); err != nil {
		t.Fatal(err)
	}
	private, err := r.CreateProject(ctx, "Private group")
	if err != nil {
		t.Fatal(err)
	}
	minimized := newTask(private.ID)
	minimized.OrganizationID = org.ID
	accepted, _, err := r.CreateOrGet(ctx, minimized, "private", "same")
	if err != nil || !accepted.MinimizeRequestLogging {
		t.Fatal(accepted, err)
	}
	if err = r.MarkFailed(context.Background(), minimized.ID, "private provider content"); err != nil {
		t.Fatal(err)
	}
	stored, err := r.Get(ctx, minimized.ID)
	if err != nil || stored.ProjectID != private.ID || stored.Prompt != "" || stored.HasStoredResult {
		t.Fatal("privacy changed", stored, err)
	}
	encoded, err := json.Marshal(core.GenerationView(stored))
	if err != nil || strings.Contains(string(encoded), "private provider content") || strings.Contains(string(encoded), "retained scene") {
		t.Fatal("retention leak", string(encoded), err)
	}
	if list, err := r.ListProjectTasks(ctx, private.ID, 50, 0); err != nil || len(list) != 1 || list[0].Prompt != "" {
		t.Fatal("private history", list, err)
	}
	t.Log("70→71 upgrade, ungrouped history, persistent creation, duplicate retry, empty/paginated filtering, foreign user/tenant denial, assignment and minimized retention verified")
}
