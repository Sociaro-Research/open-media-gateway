package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
)

func TestCatalogNumericContractsDoNotPublishDuplicateVersions(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewCatalogRepository(db)
	schedule := catalogsync.Schedule{Provider: "numeric-fixture", Time: "03:00", Version: 1, Sources: []catalogsync.Source{}}
	if err := repo.EnsureSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	model := core.MediaModel{ID: "numeric", MediaType: "audio", Parameters: []core.ParameterField{{Key: "value", Default: uint64(9007199254740993)}}}
	capture := catalogsync.Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}}
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	var state catalogsync.State
	for i := 0; i < 4; i++ {
		if i == 2 {
			capture.Models[0].Parameters[0].Default = uint64(9007199254740994)
		}
		at := now.Add(time.Duration(i) * time.Hour)
		next, run := catalogsync.Reconcile(state, capture, nil, at, nil)
		wantVersion := int64(1)
		if i >= 2 {
			wantVersion = 2
		}
		if next.Version != wantVersion || (i%2 == 1 && len(run.Changes) != 0) || (i == 2 && (len(run.Changes) != 1 || run.Changes[0].Field != "contract")) {
			t.Fatalf("publication %d: version=%d changes=%+v", i, next.Version, run.Changes)
		}
		run.ID, run.Provider, run.StartedAt, run.CompletedAt = uuid.NewString(), schedule.Provider, at, at
		if err := repo.Commit(ctx, schedule, next, run, at.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		repo = NewCatalogRepository(db)
		var err error
		state, err = repo.State(ctx, schedule.Provider)
		if err != nil {
			t.Fatal(err)
		}
		versions, err := repo.Versions(ctx, schedule.Provider, 50, 0)
		if err != nil || len(versions) != int(wantVersion) {
			t.Fatalf("duplicate/lost versions: %d %v", len(versions), err)
		}
		for _, saved := range append(versions, state) {
			value, err := json.Marshal(saved.Models[model.ID].Contract.Parameters[0].Default)
			want := "9007199254740993"
			if saved.Version == 2 {
				want = "9007199254740994"
			}
			if err != nil || string(value) != want {
				t.Fatalf("large default lost precision: %s want %s: %v", value, want, err)
			}
		}
	}
}
