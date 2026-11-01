package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestOverrideSoundBindingsAndActiveJobPin(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := identity.WithDataOwnerID(context.Background(), templates.LibraryOwnerID)
	refs := NewReferenceRepository(db)
	recipes := NewTemplateRepository(db)
	defer recipes.Close()
	ref := references.Image{ID: uuid.NewString(), Name: "Audio", ContentType: "audio/wav", SizeBytes: 12, StorageKey: "fixture/sound", SHA256: "hash", CreatedAt: time.Now()}
	if err := refs.Insert(ctx, ref); err != nil {
		t.Fatal(err)
	}
	recipe, err := recipes.Save(ctx, templates.Record{ID: uuid.NewString(), Draft: templates.Definition{Title: "Sound", OverrideSoundID: ref.ID}}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	recipe, err = recipes.Save(ctx, recipe, recipe.Version, "publish")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := recipes.Get(ctx, recipe.ID)
	if err != nil || loaded.Published.OverrideSoundID != ref.ID {
		t.Fatal("reload lost sound", err)
	}
	if err = refs.DeleteWithGuard(ctx, ref.ID, func() error { t.Fatal("template source deleted"); return nil }); !errors.Is(err, references.ErrInUse) {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	sound := core.SoundOverride{ReferenceID: ref.ID, OwnerID: templates.LibraryOwnerID, StorageKey: ref.StorageKey, SHA256: ref.SHA256, SizeBytes: ref.SizeBytes}
	tasks := NewTaskRepository(db)
	task := core.Task{ID: taskID, Status: core.TaskQueued, Provider: "mock", Model: "video", MediaType: "video", Prompt: "fixture", CreatedAt: time.Now(), UpdatedAt: time.Now(), Options: core.MediaOptions{OverrideSoundID: ref.ID, OverrideSound: &sound}}
	if _, _, err = tasks.CreateOrGet(ctx, task, "sound-fixture", "hash"); err != nil {
		t.Fatal(err)
	}
	recipe.Draft.OverrideSoundID = ""
	recipe, err = recipes.Save(ctx, recipe, recipe.Version, "unpublish")
	if err != nil {
		t.Fatal(err)
	}
	if err = refs.DeleteWithGuard(ctx, ref.ID, func() error { t.Fatal("active source deleted"); return nil }); !errors.Is(err, references.ErrInUse) {
		t.Fatal(err)
	}
	if err = tasks.MarkFailed(ctx, taskID, "fixture failure"); err != nil {
		t.Fatal(err)
	}
	removed := false
	if err = refs.DeleteWithGuard(ctx, ref.ID, func() error { removed = true; return nil }); err != nil || !removed {
		t.Fatal("terminal source still pinned", err)
	}
	if _, created, err := tasks.CreateOrGet(ctx, task, "sound-fixture", "hash"); err != nil || created {
		t.Fatal("idempotent replay needs deleted source", err)
	}
	task.ID = uuid.NewString()
	if _, _, err := tasks.CreateOrGet(ctx, task, "new-sound-fixture", "hash2"); err == nil {
		t.Fatal("deleted source admitted")
	}
}

func TestOverrideSoundDeletionWinsAdmissionRace(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := identity.WithDataOwnerID(context.Background(), templates.LibraryOwnerID)
	refs := NewReferenceRepository(db)
	ref := references.Image{ID: uuid.NewString(), Name: "Audio", ContentType: "audio/wav", SizeBytes: 12, StorageKey: "fixture/race", SHA256: "hash", CreatedAt: time.Now()}
	if err := refs.Insert(ctx, ref); err != nil {
		t.Fatal(err)
	}
	entered, release, deleted := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		deleted <- refs.DeleteWithGuard(ctx, ref.ID, func() error { close(entered); <-release; return nil })
	}()
	<-entered
	admitted := make(chan error, 1)
	go func() {
		_, _, err := NewTaskRepository(db).CreateOrGet(ctx, core.Task{ID: uuid.NewString(), Status: core.TaskQueued, Provider: "mock", Model: "video", MediaType: "video", Prompt: "fixture", CreatedAt: time.Now(), UpdatedAt: time.Now(), Options: core.MediaOptions{OverrideSoundID: ref.ID, OverrideSound: &core.SoundOverride{ReferenceID: ref.ID, OwnerID: templates.LibraryOwnerID, StorageKey: ref.StorageKey, SHA256: ref.SHA256, SizeBytes: ref.SizeBytes}}}, "race-sound", "hash")
		admitted <- err
	}()
	// Shared-library deletion commits a tombstone before physical I/O. A new
	// admission must already fail while that I/O is still pending.
	err := <-admitted
	close(release)
	if deleteErr := <-deleted; deleteErr != nil {
		t.Fatal(deleteErr)
	}
	if err == nil {
		t.Fatal("admitted source while physical deletion was pending")
	}
}
