package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestTemplatePublicationSnapshotsCASAndLifetime(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewTemplateRepository(db)
	t.Cleanup(func() { _ = repo.Close() })
	id := uuid.NewString()
	d := templates.Definition{Title: "Initial draft", Prompt: "Immutable scene", MediaType: "image", Slots: []templates.Slot{}, CoverID: ""}
	item, e := repo.Save(ctx, templates.Record{ID: id, Draft: d}, 0, "")
	if e != nil || item.Version != 1 || item.Published != nil {
		t.Fatal(item, e)
	}
	asset := templates.Asset{ID: uuid.NewString(), TemplateID: id, ContentType: "image/png", SizeBytes: 100, StorageKey: "fixture/" + uuid.NewString(), SHA256: "sha256-fixture"}
	if e = repo.PutAsset(ctx, asset); e != nil {
		t.Fatal(e)
	}
	item.Draft.CoverID = asset.ID
	item, e = repo.Save(ctx, item, 1, "publish")
	if e != nil || item.Revision != 2 || item.Published == nil {
		t.Fatal(item, e)
	}
	// A single winner, with no silent lost updates or unintended publication.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"Alice draft", "Bob draft"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			next := item
			next.Draft.Title = name
			_, err := repo.Save(ctx, next, 2, "")
			results <- err
		}(name)
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, templates.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal(wins, conflicts)
	}
	item, e = NewTemplateRepository(db).Get(ctx, id)
	if e != nil || item.Published.Title != "Initial draft" || item.Revision != 2 {
		t.Fatal("draft reinterpreted live revision", item, e)
	}
	user, e := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Author", "")
	if e != nil {
		t.Fatal(e)
	}
	owner := identity.WithPersonalData(identity.WithUser(ctx, user))
	tasks := NewTaskRepository(db)
	snapshot := json.RawMessage(`{"id":"` + id + `","revision":2,"prompt":"Immutable scene","references":[{"id":"image:1","url":"https://private.example.test"}],"model":"fixture"}`)
	task := core.Task{ID: uuid.NewString(), OwnerID: identity.OwnerID(owner), ActorID: user.ID, Status: core.TaskQueued, Provider: "mock", Model: "mock-image-v1", MediaType: "image", Prompt: "Immutable scene", TemplateSnapshot: snapshot, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	accepted, created, e := tasks.CreateOrGet(owner, task, "template-once", "same")
	if e != nil || !created || !templateJSONEqual(accepted.TemplateSnapshot, snapshot) {
		t.Fatal(accepted, created, e)
	}
	replay, created, e := tasks.CreateOrGet(owner, task, "template-once", "same")
	if e != nil || created || replay.ID != accepted.ID || len(replay.TemplateSnapshot) == 0 {
		t.Fatal(replay, created, e)
	}
	// An active admission holds publication stable, including against unpublish.
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- repo.WithPublished(ctx, id, 2, func(current templates.Record) error {
			close(entered)
			<-release
			if current.Published.Prompt != "Immutable scene" {
				return templates.ErrInvalid
			}
			return nil
		})
	}()
	<-entered
	editDone := make(chan error, 1)
	go func() { _, err := repo.Save(ctx, item, item.Version, "unpublish"); editDone <- err }()
	select {
	case err := <-editDone:
		t.Fatal("publication changed during admission", err)
	case <-time.After(75 * time.Millisecond):
	}
	close(release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if e = <-editDone; e != nil {
		t.Fatal(e)
	}
	if e = repo.WithPublished(ctx, id, 2, func(templates.Record) error { t.Fatal("unpublished admitted"); return nil }); !errors.Is(e, templates.ErrNotFound) {
		t.Fatal(e)
	}
	retained, e := tasks.Get(owner, accepted.ID)
	if e != nil || retained.Prompt != "Immutable scene" || !templateJSONEqual(retained.TemplateSnapshot, snapshot) {
		t.Fatal("history changed after unpublish", retained, e)
	}
	item, e = repo.Get(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.Save(ctx, item, item.Version, "remove"); e != nil {
		t.Fatal(e)
	}
	if _, e = repo.Get(ctx, id); !errors.Is(e, templates.ErrNotFound) {
		t.Fatal(e)
	}
	asset, e = repo.Asset(ctx, asset.ID)
	if e != nil || !asset.Published {
		t.Fatal("removal destroyed durable media", asset, e)
	}
	if _, e = db.Exec(`UPDATE tasks SET minimize_request_logging=true WHERE id=$1`, accepted.ID); e != nil {
		t.Fatal(e)
	}
	if e = tasks.MarkFailed(owner, accepted.ID, "fixture failed"); e != nil {
		t.Fatal(e)
	}
	retained, e = tasks.Get(owner, accepted.ID)
	if e != nil {
		t.Fatal(e)
	}
	var minimal map[string]any
	if e = json.Unmarshal(retained.TemplateSnapshot, &minimal); e != nil || len(minimal) != 2 || minimal["id"] != id {
		t.Fatal("logging minimization retained template content", string(retained.TemplateSnapshot), e)
	}
}

func templateJSONEqual(a, b []byte) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

func TestTemplateGenerationSettingsPersistence(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewTemplateRepository(db)
	t.Cleanup(func() { _ = repo.Close() })
	ctx := context.Background()
	for _, audio := range []bool{false, true} {
		d := templates.Definition{Title: "Output settings", MediaType: "video", Options: core.MediaOptions{ReferenceMode: "references", Ratio: "9:16", Resolution: "1080p", Duration: 9, GenerateAudio: &audio}}
		saved, err := repo.Save(ctx, templates.Record{ID: uuid.NewString(), Draft: d}, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		saved, err = repo.Save(ctx, saved, saved.Version, "publish")
		if err != nil {
			t.Fatal(err)
		}
		reloaded, err := NewTemplateRepository(db).Get(ctx, saved.ID)
		if err != nil || reloaded.Published == nil || !reflect.DeepEqual(reloaded.Draft.Options, d.Options) || !reflect.DeepEqual(reloaded.Published.Options, d.Options) {
			t.Fatal(reloaded, err)
		}
	}
}
