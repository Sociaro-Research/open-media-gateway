package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestTemplatePublishedSaveUsesCurrentContent(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewTemplateRepository(db)
	t.Cleanup(func() { _ = repo.Close() })
	ctx := context.Background()
	item, err := repo.Save(ctx, templates.Record{ID: uuid.NewString(), Draft: templates.Definition{Title: "Single content", Prompt: "Before save", MediaType: "image"}}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	item, err = repo.Save(ctx, item, item.Version, "publish")
	if err != nil {
		t.Fatal(err)
	}
	previous := item.Revision
	item.Draft.Prompt = "After save"
	saved, err := repo.Save(ctx, item, item.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Published == nil || saved.Published.Prompt != "After save" || saved.Draft.Prompt != "After save" || saved.Revision <= previous {
		t.Fatalf("published Save kept a second active source: current=%q published=%+v token=%d previous=%d", saved.Draft.Prompt, saved.Published, saved.Revision, previous)
	}
}

func TestTemplateCurrentContentKeepsAdmittedJobSnapshot(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewTemplateRepository(db)
	defer repo.Close()
	item, err := repo.Save(ctx, templates.Record{ID: uuid.NewString(), Draft: templates.Definition{Title: "Snapshot", Prompt: "Accepted prompt", MediaType: "video", Options: core.MediaOptions{Duration: 5, Ratio: "16:9"}}}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	item, err = repo.Save(ctx, item, item.Version, "publish")
	if err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskRepository(db)
	now := time.Now().UTC()
	var accepted core.Task
	err = repo.WithPublished(ctx, item.ID, item.Revision, func(current templates.Record) error {
		raw, _ := json.Marshal(map[string]any{"id": current.ID, "revision": current.Revision, "prompt": current.Draft.Prompt, "options": current.Draft.Options})
		accepted, _, err = tasks.CreateOrGet(ctx, core.Task{ID: uuid.NewString(), Status: core.TaskQueued, Provider: "mock", Model: "fixture", MediaType: "image", Prompt: current.Draft.Prompt, Options: current.Draft.Options, TemplateSnapshot: raw, CreatedAt: now, UpdatedAt: now}, "single-current-job", "immutable-request")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = tasks.MarkRunning(ctx, accepted.ID); err != nil {
		t.Fatal(err)
	}
	item.Draft.Prompt = "Next prompt"
	item.Draft.Options.Duration = 9
	item.Draft.Options.Ratio = "9:16"
	item, err = repo.Save(ctx, item, item.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	running, err := tasks.Get(ctx, accepted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != core.TaskRunning || running.Prompt != "Accepted prompt" || running.Options.Duration != 5 || running.Options.Ratio != "16:9" || !templateJSONEqual(running.TemplateSnapshot, accepted.TemplateSnapshot) {
		t.Fatal("published Save changed running job")
	}
	if item.Published.Prompt != "Next prompt" {
		t.Fatal("new admissions did not get current content")
	}
}

func TestTemplateCurrentContentMigrationPreservesLegacyData(t *testing.T) {
	db := auditDatabase(t, false)
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 80); err != nil {
		t.Fatal(err)
	}
	id, unpublished := uuid.NewString(), uuid.NewString()
	oldDraft := `{"title":"Unvalidated later edit","prompt":"unreviewed","slots":[]}`
	oldPublished := `{"title":"Visible valid content","prompt":"visible","slots":[]}`
	if _, err := db.Exec(`INSERT INTO generation_templates(id,version,revision,draft,published) VALUES($1,9,4,$2,$3),($4,3,0,$2,NULL)`, id, oldDraft, oldPublished, unpublished); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewTemplateRepository(db)
	defer repo.Close()
	ctx := context.Background()
	item, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if item.Version != 10 || item.Revision != 4 || item.Published == nil || item.Draft.Prompt != "visible" || item.Published.Prompt != "visible" {
		t.Fatal("migration activated divergent draft", item)
	}
	var preserved bool
	var archive []byte
	if err = db.QueryRow(`SELECT legacy_state->'draft'=$2::jsonb AND legacy_state->'published'=$3::jsonb,legacy_state FROM generation_templates WHERE id=$1`, id, oldDraft, oldPublished).Scan(&preserved, &archive); err != nil || !preserved {
		t.Fatal("legacy data not preserved", err)
	}
	off, err := repo.Get(ctx, unpublished)
	if err != nil || off.Published != nil || off.Draft.Prompt != "unreviewed" || off.Version != 3 {
		t.Fatal("unpublished migration changed content", off, err)
	}
	if _, err = repo.Save(ctx, item, 9, ""); !errors.Is(err, templates.ErrConflict) {
		t.Fatal("pre-migration editor accepted", err)
	}
	item.Draft.Prompt = "Current changed content"
	item, err = repo.Save(ctx, item, item.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	// Editing/toggling never reads the inert archive.
	item, err = repo.Save(ctx, item, item.Version, "unpublish")
	if err != nil {
		t.Fatal(err)
	}
	item, err = repo.Save(ctx, item, item.Version, "publish")
	if err != nil || item.Published.Prompt != "Current changed content" {
		t.Fatal("legacy state reactivated", err)
	}
	if err = goose.DownTo(db, "migrations", 80); err != nil {
		t.Fatal(err)
	}
	var sameCurrent bool
	if err = db.QueryRow(`SELECT draft=published AND draft->>'prompt'='Current changed content' FROM generation_templates WHERE id=$1`, id).Scan(&sameCurrent); err != nil || !sameCurrent {
		t.Fatal("schema rollback restored stale content", err)
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	var after []byte
	if err = db.QueryRow(`SELECT legacy_state FROM generation_templates WHERE id=$1`, id).Scan(&after); err != nil || !templateJSONEqual(archive, after) {
		t.Fatal("schema roundtrip lost inert recovery data", err)
	}
}

func TestTemplatePublishedSaveWaitsForAdmission(t *testing.T) {
	db := auditDatabase(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	repo := NewTemplateRepository(db)
	defer repo.Close()
	item, err := repo.Save(ctx, templates.Record{ID: uuid.NewString(), Draft: templates.Definition{Title: "Atomic admission", Prompt: "Before"}}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	item, err = repo.Save(ctx, item, item.Version, "publish")
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	admitted := make(chan error, 1)
	go func() {
		admitted <- repo.WithPublished(ctx, item.ID, item.Revision, func(current templates.Record) error {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			if current.Draft.Prompt != "Before" {
				return templates.ErrInvalid
			}
			return nil
		})
	}()
	<-entered
	editDone := make(chan error, 1)
	next := item
	next.Draft.Prompt = "After"
	go func() { _, e := repo.Save(ctx, next, item.Version, ""); editDone <- e }()
	select {
	case err := <-editDone:
		t.Fatal("published Save passed an active admission", err)
	case <-time.After(75 * time.Millisecond):
	}
	close(release)
	if err = <-admitted; err != nil {
		t.Fatal(err)
	}
	if err = <-editDone; err != nil {
		t.Fatal(err)
	}
	if err = repo.WithPublished(ctx, item.ID, item.Revision, func(templates.Record) error { t.Error("old token admitted"); return nil }); !errors.Is(err, templates.ErrConflict) {
		t.Fatal(err)
	}
	saved, err := repo.Get(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.WithPublished(ctx, item.ID, saved.Revision, func(current templates.Record) error {
		if current.Draft.Prompt != "After" {
			return templates.ErrInvalid
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
