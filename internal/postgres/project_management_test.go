package postgres

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
)

func TestProjectManagementPreservesGenerationsAndMedia(t *testing.T) {
	db := auditDatabase(t, true)
	r := NewTaskRepository(db)
	actor := uuid.NewString()
	ctx := identity.WithOwnerID(context.Background(), actor)
	other := identity.WithOwnerID(context.Background(), uuid.NewString())
	first, err := r.CreateProject(ctx, "Original")
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.CreateProject(ctx, "Destination")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []context.Context{other, identity.WithOrganizationRole(ctx, uuid.NewString(), "developer")} {
		if _, err := r.RenameProject(c, first.ID, "Stolen", first.Version); !errors.Is(err, core.ErrProjectInvalid) {
			t.Fatal("foreign rename", err)
		}
		if err := r.DeleteProject(c, first.ID, first.Version); !errors.Is(err, core.ErrProjectInvalid) {
			t.Fatal("foreign deletion", err)
		}
	}
	renamed, err := r.RenameProject(ctx, first.ID, "  Renamed 🎬  ", first.Version)
	if err != nil || renamed.Name != "Renamed 🎬" || renamed.Version != 2 {
		t.Fatal(renamed, err)
	}
	if _, err := r.RenameProject(ctx, first.ID, "Old draft", first.Version); !errors.Is(err, core.ErrProjectConflict) {
		t.Fatal("stale rename", err)
	}
	if err := r.DeleteProject(ctx, first.ID, first.Version); !errors.Is(err, core.ErrProjectConflict) {
		t.Fatal("stale delete", err)
	}
	if _, err := r.RenameProject(ctx, first.ID, "destination", renamed.Version); !errors.Is(err, core.ErrProjectNameTaken) {
		t.Fatal("duplicate", err)
	}
	if _, err := r.RenameProject(ctx, first.ID, "\n", renamed.Version); !errors.Is(err, core.ErrProjectName) {
		t.Fatal("invalid name", err)
	}
	makeTask := func(project string) core.Task {
		return core.Task{ID: uuid.NewString(), ActorID: actor, ProjectID: project, Status: core.TaskQueued, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "Private retained scene", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	}
	local, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	media, err := local.Archive(ctx, uuid.NewString(), "data:video/mp4;base64,c3ludGhldGljLW1lZGlh")
	if err != nil {
		t.Fatal(err)
	}
	frame, err := local.Archive(ctx, uuid.NewString(), "data:image/png;base64,c3ludGhldGljLWZyYW1l")
	if err != nil {
		t.Fatal(err)
	}
	media.LastFrameStorageKey = frame.StorageKey
	tasks := []core.Task{}
	for _, status := range []core.TaskStatus{core.TaskQueued, core.TaskRunning, core.TaskSucceeded, core.TaskFailed} {
		task := makeTask(first.ID)
		saved, _, err := r.CreateOrGet(ctx, task, task.ID, "original-hash")
		if err != nil {
			t.Fatal(err)
		}
		if status != core.TaskQueued {
			if err = r.MarkRunning(context.Background(), task.ID); err != nil {
				t.Fatal(err)
			}
		}
		if status == core.TaskSucceeded {
			if err = r.MarkSucceeded(context.Background(), task.ID, media, ""); err != nil {
				t.Fatal(err)
			}
		}
		if status == core.TaskFailed {
			if err = r.MarkFailed(context.Background(), task.ID, "fixture failure"); err != nil {
				t.Fatal(err)
			}
		}
		if status == core.TaskSucceeded {
			for _, table := range []string{"stored_objects", "result_objects"} {
				for _, object := range []core.StoredMedia{media, frame} {
					if _, err = db.Exec(`INSERT INTO `+table+`(owner_id,storage_key,task_id,size_bytes) VALUES($1,$2,$3,$4)`, actor, object.StorageKey, task.ID, object.SizeBytes); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		saved, err = r.Get(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, saved)
	}
	// An ungrouped request can be assigned without changing any generation data.
	legacy := makeTask("")
	if _, _, err = r.CreateOrGet(ctx, legacy, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = r.AssignTaskProject(other, legacy.ID, second.ID, 0); !errors.Is(err, core.ErrProjectInvalid) {
		t.Fatal("foreign project assignment", err)
	}
	foreignProject, err := r.CreateProject(other, "Other user's project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.AssignTaskProject(other, legacy.ID, foreignProject.ID, 0); !errors.Is(err, core.ErrTaskNotFound) {
		t.Fatal("foreign generation assignment", err)
	}
	assigned, err := r.AssignTaskProject(ctx, legacy.ID, second.ID, 0)
	if err != nil || assigned.ProjectID != second.ID || assigned.Version != 1 {
		t.Fatal(assigned, err)
	}
	if _, err = r.AssignTaskProject(ctx, legacy.ID, first.ID, 0); !errors.Is(err, core.ErrProjectConflict) {
		t.Fatal("stale reassignment", err)
	}
	if _, err = r.AssignTaskProject(ctx, legacy.ID, "", assigned.Version); err != nil {
		t.Fatal(err)
	}
	before := map[string]core.Task{}
	for _, task := range tasks {
		task.ProjectID = ""
		task.ProjectVersion = 0
		before[task.ID] = task
	}
	if err = r.DeleteProject(ctx, first.ID, renamed.Version); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		stored, err := r.Get(ctx, task.ID)
		if err != nil || stored.ProjectID != "" || stored.ProjectVersion != 1 {
			t.Fatal("generation lost/tag retained", stored, err)
		}
		stored.ProjectVersion = 0
		if !reflect.DeepEqual(stored, before[task.ID]) {
			t.Fatal("delete changed generation or result metadata", stored, before[task.ID])
		}
	}
	if list, err := r.ListRecent(ctx, 50); err != nil || len(list) != 5 {
		t.Fatal("delete lost history", list, err)
	}
	for _, table := range []string{"stored_objects", "result_objects"} {
		var count int
		if err = db.QueryRow(`SELECT count(*) FROM `+table+` WHERE owner_id=$1 AND deleted_at IS NULL`, actor).Scan(&count); err != nil || count != 2 {
			t.Fatal("media index changed", table, count, err)
		}
	}
	for _, object := range []core.StoredMedia{media, frame} {
		stored, err := local.Open(ctx, object.StorageKey)
		if err != nil {
			t.Fatal("media file lost", err)
		}
		body, err := io.ReadAll(stored.Reader)
		stored.Reader.Close()
		if err != nil || len(body) != int(object.SizeBytes) {
			t.Fatal("media file changed", err)
		}
	}
	// An uncertain retry after label deletion still returns the accepted task.
	original := tasks[0]
	original.ProjectID = first.ID
	replay, created, err := r.CreateOrGet(ctx, original, original.ID, "original-hash")
	if err != nil || created || replay.ID != original.ID || replay.ProjectID != "" {
		t.Fatal("lost accepted submission replay", replay, created, err)
	}
	if _, _, err = r.CreateOrGet(ctx, makeTask(first.ID), "new-after-delete", ""); !errors.Is(err, core.ErrProjectInvalid) {
		t.Fatal("accepted deleted project", err)
	}
	if _, err = r.AssignTaskProject(ctx, legacy.ID, first.ID, 2); !errors.Is(err, core.ErrProjectInvalid) {
		t.Fatal("reassigned to deleted project", err)
	}
	t.Log("rename/version/name validation, actor/tenant isolation, reassignment, ungrouping and populated queued/running/completed/failed deletion preserve generation and result records")
}

func TestProjectDeletionRacesSubmissionAndAssignment(t *testing.T) {
	db := auditDatabase(t, true)
	r := NewTaskRepository(db)
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	for i := 0; i < 12; i++ {
		project, err := r.CreateProject(ctx, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		task := core.Task{ID: uuid.NewString(), ActorID: identity.ActorID(ctx), Status: core.TaskQueued, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "Concurrent fixture", CreatedAt: time.Now(), UpdatedAt: time.Now()}
		if _, _, err = r.CreateOrGet(ctx, task, "", ""); err != nil {
			t.Fatal(err)
		}
		next := task
		next.ID = uuid.NewString()
		next.ProjectID = project.ID
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make(chan error, 3)
		for _, run := range []func() error{
			func() error { return r.DeleteProject(ctx, project.ID, project.Version) },
			func() error { _, _, err := r.CreateOrGet(ctx, next, next.ID, "hash"); return err },
			func() error { _, err := r.AssignTaskProject(ctx, task.ID, project.ID, 0); return err },
		} {
			wg.Add(1)
			go func(run func() error) { defer wg.Done(); <-start; errs <- run() }(run)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil && !errors.Is(err, core.ErrProjectInvalid) {
				t.Fatal("concurrent operation", err)
			}
		}
		saved, err := r.Get(ctx, task.ID)
		if err != nil || saved.ProjectID != "" {
			t.Fatal("old generation lost or dangling tag", saved, err)
		}
		accepted, err := r.Get(ctx, next.ID)
		if err != nil && !errors.Is(err, core.ErrTaskNotFound) {
			t.Fatal(err)
		}
		if err == nil && accepted.ProjectID != "" {
			t.Fatal("new generation retained deleted tag", accepted)
		}
	}
	t.Log("concurrent delete/submit/reassign has no deadlock, data loss or dangling assignment")
}
