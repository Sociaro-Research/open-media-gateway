package postgres

import (
	"context"
	"reflect"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/sonilo"
)

func TestCatalogSourceDefaultsPreserveSavedAdministratorOverrides(t *testing.T) {
	repo := NewCatalogRepository(auditDatabase(t, true))
	ctx := context.Background()
	// The administrator disabled this schedule, changed its time, renamed the
	// core source, added a private review source and removed API/pricing defaults.
	saved := catalogsync.Schedule{Provider: "sonilo", Enabled: false, Time: "19:45", Version: 1, Sources: []catalogsync.Source{
		{ID: "admin-core", URL: "https://sonilo.com/openapi.json", Parser: "document"},
		{ID: "admin-review", URL: "https://review.example.test/sonilo", Parser: "document"},
	}}
	if err := repo.EnsureSchedule(ctx, saved); err != nil {
		t.Fatal(err)
	}
	before, err := repo.Schedules(ctx)
	if err != nil || len(before) != 1 {
		t.Fatal(before, err)
	}
	// Startup after upgrading the module must not adopt any sources implicitly.
	defaults := catalogsync.Schedule{Provider: "sonilo", Enabled: true, Time: "03:00", Version: 1, Sources: sonilo.Definition().CatalogSources}
	if err := repo.EnsureSchedule(ctx, defaults); err != nil {
		t.Fatal(err)
	}
	after, err := repo.Schedules(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("upgraded defaults changed saved sources, state, version or next check: before=%+v after=%+v err=%v", before, after, err)
	}
}
