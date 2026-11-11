package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/legal"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

func TestLegalFooterPersistencePermissionsAndIsolation(t *testing.T) {
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
	before, err := repo.LegalFooter(ctx)
	if err != nil || before.CopyrightHolder != "" || before.Version != 1 {
		t.Fatal("neutral fresh default", before, err)
	}
	defer db.ExecContext(ctx, "UPDATE legal_footer SET copyright_holder=$1,version=$2 WHERE id=TRUE", before.CopyrightHolder, before.Version)
	user, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Footer administrator", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, "DELETE FROM users WHERE id=$1", user.ID)
	next := legal.Footer{CopyrightHolder: "  Configured <operator> & company  ", Version: before.Version}
	if _, err = repo.SaveLegalFooter(ctx, user.ID, next); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("ordinary user", err)
	}
	if _, err = db.ExecContext(ctx, "UPDATE users SET is_system_admin=TRUE WHERE id=$1", user.ID); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.SaveLegalFooter(ctx, user.ID, next)
	if err != nil || saved.CopyrightHolder != "Configured <operator> & company" {
		t.Fatal(saved, err)
	}
	if _, err = repo.SaveLegalFooter(ctx, user.ID, next); !errors.Is(err, system.ErrConflict) {
		t.Fatal("stale write", err)
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := NewSystemRepository(reopened).LegalFooter(ctx)
	if err != nil || stored != saved {
		t.Fatal("restart/migration lost configured holder", stored, err)
	}
	var originalBrand []byte
	var originalVersion int64
	if err = db.QueryRowContext(ctx, "SELECT configuration,version FROM site_branding WHERE id=TRUE").Scan(&originalBrand, &originalVersion); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, "UPDATE site_branding SET configuration=$1,version=$2 WHERE id=TRUE", originalBrand, originalVersion)
	brand, err := repo.Branding(ctx)
	if err != nil {
		t.Fatal(err)
	}
	brand.Name = "Independent site name"
	if _, err = repo.SaveBranding(ctx, user.ID, brand); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.LegalFooter(ctx); err != nil || got != saved {
		t.Fatal("appearance overwrote footer", got, err)
	}
	doc, err := repo.LegalDocument(ctx, "privacy")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Text != "" {
		t.Fatal("footer changed document", doc)
	}
	for _, bad := range []string{strings.Repeat("x", 201), "a\nb"} {
		if _, err = repo.SaveLegalFooter(ctx, user.ID, legal.Footer{CopyrightHolder: bad, Version: saved.Version}); err == nil {
			t.Fatal("invalid persisted", bad)
		}
	}
	if _, err = db.ExecContext(ctx, "UPDATE legal_footer SET copyright_holder=$1", strings.Repeat("x", 201)); err == nil {
		t.Fatal("database byte limit missing")
	}
	if _, err = db.ExecContext(ctx, "UPDATE users SET is_system_admin=FALSE WHERE id=$1", user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.SaveLegalFooter(ctx, user.ID, legal.Footer{Version: saved.Version}); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("revoked administrator", err)
	}
}
