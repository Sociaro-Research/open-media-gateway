package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

func TestBrandingPersistenceConflictAndAudit(t *testing.T) {
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
	before, err := repo.Branding(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `UPDATE site_branding SET configuration='{}',version=1 WHERE id=TRUE`)
	user, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Brand admin", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, user.ID)
	if _, err = repo.SaveBranding(ctx, user.ID, before); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("ordinary user saved branding", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE users SET is_system_admin=TRUE WHERE id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	next := branding.Defaults()
	next.Version = before.Version
	next.Name = "White Label"
	contentWidth := 1000
	next.SiteMaxWidth = nil
	next.ContentMaxWidth = &contentWidth
	next.SidebarSubsections = true
	next.Light["accent"] = "#aa11bb"
	next.Assets["logo"] = branding.Asset{MIME: "image/svg+xml", Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 20"><path d="M0 0h100v20H0z"/></svg>`)}
	saved, err := repo.SaveBranding(ctx, user.ID, next)
	if err != nil || saved.Version != before.Version+1 {
		t.Fatal(saved, err)
	}
	stored, err := repo.Branding(ctx)
	if err != nil || stored.Name != next.Name || stored.Light["accent"] != "#aa11bb" || string(stored.Assets["logo"].Data) != string(next.Assets["logo"].Data) {
		t.Fatal("roundtrip", stored, err)
	}
	if stored.SiteMaxWidth != nil || stored.ContentMaxWidth == nil || *stored.ContentMaxWidth != contentWidth || !stored.SidebarSubsections {
		t.Fatal("layout roundtrip", stored)
	}
	if _, err = repo.SaveBranding(ctx, user.ID, next); !errors.Is(err, system.ErrConflict) {
		t.Fatal("stale update accepted", err)
	}
	var details string
	err = db.QueryRowContext(ctx, `SELECT details::text FROM audit_events WHERE action='branding' ORDER BY id DESC LIMIT 1`).Scan(&details)
	if err != nil || !strings.Contains(details, "White Label") || strings.Contains(details, "PHN2Zy") || strings.Contains(details, `"data"`) {
		t.Fatal("audit", details, err)
	}
	if !strings.Contains(details, `"sidebar_subsections": true`) || !strings.Contains(details, `"content_max_width": 1000`) {
		t.Fatal("layout audit missing", details)
	}
	next = branding.Defaults()
	next.Version = saved.Version
	if _, err = repo.SaveBranding(ctx, user.ID, next); err != nil {
		t.Fatal(err)
	}
	stored, err = repo.Branding(ctx)
	if err != nil || stored.Name != "" || len(stored.Assets) != 0 {
		t.Fatal("reset", stored, err)
	}
}
