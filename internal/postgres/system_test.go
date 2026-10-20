package postgres

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/audit"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
	"os"
	"sync"
	"testing"
	"time"
)

func TestSystemRolesAndConcurrentChanges(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewSystemRepository(db)
	auth := NewAuthRepository(db)
	a, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), "admin-a@example.test", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), "admin-b@example.test", "B", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, a.ID, b.ID)
	before, err := repo.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `UPDATE system_settings SET generation_ui=TRUE,personal_providers=TRUE,version=$1,allow_inline_references=$2`, before.Version, before.AllowInlineReferences)
	if err = repo.SetAdmin(ctx, "", a.ID, true, true); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetAdmin(ctx, "", b.ID, true, true); !errors.Is(err, system.ErrBootstrapped) {
		t.Fatalf("bootstrap: %v", err)
	}
	if err = repo.SetAdmin(ctx, a.ID, a.ID, false, false); !errors.Is(err, system.ErrLastAdmin) {
		t.Fatalf("last admin: %v", err)
	}
	if err = repo.SetAdmin(ctx, b.ID, b.ID, true, false); !errors.Is(err, system.ErrForbidden) {
		t.Fatalf("self escalation: %v", err)
	}
	if err = repo.SetAdmin(ctx, a.ID, b.ID, true, false); err != nil {
		t.Fatal(err)
	}
	changed := before
	changed.GenerationUI = false
	changed.AllowInlineReferences = !before.AllowInlineReferences
	saved, err := repo.SaveSettings(ctx, a.ID, changed)
	if err != nil || saved.Version != before.Version+1 {
		t.Fatalf("save: %+v %v", saved, err)
	}
	stored, err := repo.Settings(ctx)
	if err != nil || stored.AllowInlineReferences != changed.AllowInlineReferences {
		t.Fatalf("reference policy persistence: %+v %v", stored, err)
	}
	if _, err = repo.SaveSettings(ctx, a.ID, changed); !errors.Is(err, system.ErrConflict) {
		t.Fatalf("concurrent edit: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{a.ID, b.ID} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); results <- repo.SetAdmin(ctx, id, id, false, false) }(id)
	}
	wg.Wait()
	close(results)
	success, blocked := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, system.ErrLastAdmin) {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || blocked != 1 {
		t.Fatalf("concurrent removals: %d/%d", success, blocked)
	}
	aa, _ := repo.IsAdmin(ctx, a.ID)
	revoked := a.ID
	if aa {
		revoked = b.ID
	}
	if _, err = repo.SaveSettings(ctx, revoked, saved); !errors.Is(err, system.ErrForbidden) {
		t.Fatalf("revoked writer: %v", err)
	}
	users, err := repo.Users(ctx, "admin-", true, 50, 0)
	if err != nil || len(users) != 1 {
		t.Fatalf("admin search: %+v %v", users, err)
	}
	events, err := repo.AuditEvents(ctx, audit.Filter{From: time.Unix(0, 0), Until: time.Now().Add(time.Hour), Limit: 50})
	if err != nil || len(events) < 4 {
		t.Fatalf("audit: %+v %v", events, err)
	}
}
