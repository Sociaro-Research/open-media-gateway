package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestTemplateExplicitRemovalVersionMediaAndMissingBinding(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewTemplateRepository(db)
	t.Cleanup(func() { _ = repo.Close() })
	ctx := context.Background()
	id := uuid.NewString()
	item, err := repo.Save(ctx, templates.Record{ID: id, Draft: templates.Definition{Title: "Incomplete draft", MediaType: "image"}}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	asset := templates.Asset{ID: uuid.NewString(), TemplateID: id, ContentType: "image/png", SizeBytes: 100, StorageKey: "fixture/" + uuid.NewString(), SHA256: "fixture-sha"}
	if err := repo.PutAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	item.Draft.CoverID = asset.ID
	item, err = repo.Save(ctx, item, item.Version, "publish")
	if err != nil {
		t.Fatal(err)
	}
	before, err := repo.Asset(ctx, asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Save(ctx, item, item.Version-1, "remove"); !errors.Is(err, templates.ErrConflict) {
		t.Fatal("stale removal accepted", err)
	}
	current, err := repo.Get(ctx, id)
	if err != nil || !reflect.DeepEqual(current, item) {
		t.Fatal("conflict changed current content", current, err)
	}
	removed, err := repo.Save(ctx, item, item.Version, "remove")
	if err != nil || removed.Version != item.Version+1 || removed.Published != nil {
		t.Fatal(removed, err)
	}
	if _, err := repo.Get(ctx, id); !errors.Is(err, templates.ErrNotFound) {
		t.Fatal("removed row visible", err)
	}
	after, err := repo.Asset(ctx, asset.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("removal altered retained media", before, after, err)
	}
	// Preserve the existing rejection when stored legacy content has missing media.
	badID := uuid.NewString()
	bad, err := repo.Save(ctx, templates.Record{ID: badID, Draft: templates.Definition{Title: "Legacy missing cover", MediaType: "image"}}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	missing := uuid.NewString()
	if _, err := db.Exec(`UPDATE generation_templates SET content=jsonb_set(content,'{cover_id}',to_jsonb($2::text)) WHERE id=$1`, badID, missing); err != nil {
		t.Fatal(err)
	}
	bad, err = repo.Get(ctx, badID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Save(ctx, bad, bad.Version, "remove"); !errors.Is(err, templates.ErrNotFound) {
		t.Fatal("missing binding validation bypassed", err)
	}
	retained, err := repo.Get(ctx, badID)
	if err != nil || !reflect.DeepEqual(retained, bad) {
		t.Fatal("failed removal changed saved row", retained, err)
	}
}
