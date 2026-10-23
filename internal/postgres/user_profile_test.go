package postgres

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/google/uuid"
)

func TestUserProfilePersistsAndSurvivesGoogleLogin(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
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
	repo := NewAuthRepository(db)
	subject := uuid.NewString()
	email := subject + "@example.test"
	user, err := repo.UpsertGoogleUser(ctx, subject, email, "Google name", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, user.ID)
	if user.Name != "Google name" || user.Theme != "system" {
		t.Fatal(user)
	}
	user, err = repo.UpsertGoogleUser(ctx, subject, email, "Updated Google name", "")
	if err != nil || user.Name != "Updated Google name" {
		t.Fatal(user, err)
	}
	manager, err := auth.New(ctx, repo, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "https://example.test"})
	if err != nil {
		t.Fatal(err)
	}
	name, theme, preset := "  Custom name  ", "system", "original"
	columns := []string{"date", "generation_id", "status"}
	if _, err = manager.UpdateProfile(ctx, user.ID, auth.ProfileUpdate{Name: &name, Theme: &theme, ReportColumns: &columns, ColorPreset: &preset}); err != nil {
		t.Fatal(err)
	}
	user, err = repo.UpsertGoogleUser(ctx, subject, email, "Google again", "")
	if err != nil || user.Name != "Custom name" || user.Theme != "system" || user.ColorPreset != preset || !reflect.DeepEqual(user.ReportColumns, columns) {
		t.Fatal(user, err)
	}
	token := []byte(uuid.NewString())
	if err = repo.CreateSession(ctx, token, user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	read, err := NewAuthRepository(db).UserBySession(ctx, token, time.Now())
	if err != nil || read.Name != "Custom name" || read.Theme != "system" || read.ColorPreset != preset || !reflect.DeepEqual(read.ReportColumns, columns) {
		t.Fatal(read, err)
	}
	theme = "light"
	if _, err = manager.UpdateProfile(ctx, user.ID, auth.ProfileUpdate{Theme: &theme}); err != nil {
		t.Fatal(err)
	}
	read, err = repo.UserBySession(ctx, token, time.Now())
	if err != nil || read.Name != "Custom name" || read.Theme != "light" || read.ColorPreset != preset || !reflect.DeepEqual(read.ReportColumns, columns) {
		t.Fatal(read, err)
	}
	// Email-only accounts retain their generated initial name until edited.
	id := uuid.NewString()
	defer db.Exec(`DELETE FROM users WHERE id=$1`, id)
	if _, err = db.Exec(`INSERT INTO users(id,email,display_name,created_at,updated_at) VALUES($1,$2,'email-prefix',NOW(),NOW())`, id, id+"@example.test"); err != nil {
		t.Fatal(err)
	}
	name = "Email customer"
	changed, err := manager.UpdateProfile(ctx, id, auth.ProfileUpdate{Name: &name})
	if err != nil || changed.Name != name || changed.Theme != "system" || changed.ReportColumns != nil {
		t.Fatal(changed, err)
	}
	read, err = repo.UserBySession(ctx, token, time.Now())
	if err != nil || read.Name != "Custom name" {
		t.Fatal("another account changed", read, err)
	}
}
