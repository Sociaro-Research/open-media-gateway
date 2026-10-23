package postgres

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/legal"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
	"os"
	"testing"
)

func TestLegalDocumentPersistenceAndConflict(t *testing.T) {
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
	before, err := repo.LegalDocument(ctx, "privacy")
	if err != nil || before.Text != "" {
		t.Fatal(before, err)
	}
	defer db.ExecContext(ctx, "UPDATE legal_documents SET body='',version=1")
	user, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Legal admin", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, "DELETE FROM users WHERE id=$1", user.ID)
	next := legal.Document{Text: "A <script>literal</script>\r\n\nSecond paragraph", Version: before.Version}
	if _, err = repo.SaveLegalDocument(ctx, user.ID, "privacy", next); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("ordinary user", err)
	}
	if _, err = db.ExecContext(ctx, "UPDATE users SET is_system_admin=TRUE WHERE id=$1", user.ID); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.SaveLegalDocument(ctx, user.ID, "privacy", next)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repo.LegalDocument(ctx, "privacy")
	if err != nil || stored != saved || stored.Version != before.Version+1 {
		t.Fatal("roundtrip", stored, err)
	}
	if _, err = repo.SaveLegalDocument(ctx, user.ID, "privacy", next); !errors.Is(err, system.ErrConflict) {
		t.Fatal("lost update", err)
	}
	terms, err := repo.LegalDocument(ctx, "terms")
	if err != nil || terms.Text != "" || terms.Version != 1 {
		t.Fatal("terms changed", terms, err)
	}
	stored.Text = ""
	if _, err = repo.SaveLegalDocument(ctx, user.ID, "privacy", stored); err != nil {
		t.Fatal("reset", err)
	}
	if _, err = db.ExecContext(ctx, "UPDATE users SET is_system_admin=FALSE WHERE id=$1", user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.SaveLegalDocument(ctx, user.ID, "privacy", legal.Document{Version: stored.Version + 1}); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("revoked admin", err)
	}
}
