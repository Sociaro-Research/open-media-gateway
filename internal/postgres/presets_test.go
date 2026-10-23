package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/google/uuid"
)

func TestColorPresetPersistenceAndLegacyWritePreservation(t *testing.T) {
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
	var original []byte
	var version int64
	if err = db.QueryRow(`SELECT configuration,version FROM site_branding WHERE id=TRUE`).Scan(&original, &version); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`UPDATE site_branding SET configuration=$1,version=$2 WHERE id=TRUE`, original, version)

	// The migration seeds an empty object; reads expose only Original without
	// persisting a catalog or rewriting existing administrator configuration.
	if _, err = db.Exec(`UPDATE site_branding SET configuration='{}' WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	fresh, err := NewSystemRepository(db).Branding(ctx)
	if err != nil || len(fresh.Presets) != 1 || fresh.DefaultPreset != "original" || fresh.Presets[0].ID != "original" || fresh.Presets[0].Name != "Original" {
		t.Fatal("unexpected fresh catalog", fresh, err)
	}
	var untouched []byte
	if err = db.QueryRow(`SELECT configuration FROM site_branding WHERE id=TRUE`).Scan(&untouched); err != nil || !bytes.Equal(untouched, []byte("{}")) {
		t.Fatal("read persisted settings", string(untouched), err)
	}
	legacy := branding.Defaults()
	legacy.Dark["accent"] = "#abcd12"
	legacy.Light["switch-off"] = "#eef7e0"
	raw, _ := json.Marshal(legacy)
	if _, err = db.Exec(`UPDATE site_branding SET configuration=$1 WHERE id=TRUE`, raw); err != nil {
		t.Fatal(err)
	}
	people, repo := NewAuthRepository(db), NewSystemRepository(db)
	user, err := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Palette admin", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, user.ID)
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	settings, err := repo.Branding(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Presets[0].Dark["accent"] != "#abcd12" || settings.DefaultPreset != "original" {
		t.Fatal("legacy colors lost", settings)
	}
	// A previously persisted Clear Lime remains an ordinary editable theme.
	savedPalette := branding.Defaults().Normalize()
	savedPalette.Light["accent"] = "#b8dc00"
	settings.Presets = append(settings.Presets, branding.Preset{ID: "clear-lime", Name: "Clear Lime", Dark: savedPalette.Dark, Light: savedPalette.Light})
	settings.DefaultPreset = "clear-lime"
	saved, err := repo.SaveBranding(ctx, user.ID, settings)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Branding(ctx)
	if err != nil || stored.DefaultPreset != "clear-lime" || stored.Presets[0].Dark["accent"] != "#abcd12" {
		t.Fatal("saved catalog lost original", stored, err)
	}
	// An older editor still changes only the default pair and retains other themes.
	old := branding.Defaults()
	old.Version = saved.Version
	old.Light["accent"] = "#123456"
	if _, err = repo.SaveBranding(ctx, user.ID, old); err != nil {
		t.Fatal(err)
	}
	stored, err = repo.Branding(ctx)
	if err != nil || stored.DefaultPreset != "clear-lime" || stored.Light["accent"] != "#123456" || stored.Presets[0].Dark["accent"] != "#abcd12" {
		t.Fatal("legacy write erased catalog", stored, err)
	}
	id := "missing"
	if _, err = people.UpdateProfile(ctx, user.ID, auth.ProfileUpdate{ColorPreset: &id}); !errors.Is(err, auth.ErrInvalidProfile) {
		t.Fatal("unpublished selection accepted", err)
	}
	id = "original"
	selected, err := people.UpdateProfile(ctx, user.ID, auth.ProfileUpdate{ColorPreset: &id})
	if err != nil || selected.ColorPreset != id {
		t.Fatal(selected, err)
	}
	// Removing a published preset keeps the user's saved ID for frontend fallback,
	// but rejects any new selection of that removed preset.
	stored.Presets = stored.Presets[1:]
	stored, err = repo.SaveBranding(ctx, user.ID, stored)
	if err != nil || len(stored.Presets) != 1 || stored.DefaultPreset != "clear-lime" {
		t.Fatal("catalog deletion failed", stored, err)
	}
	if _, err = people.UpdateProfile(ctx, user.ID, auth.ProfileUpdate{ColorPreset: &id}); !errors.Is(err, auth.ErrInvalidProfile) {
		t.Fatal("deleted preset selection accepted", err)
	}
	var personalPreset string
	if err = db.QueryRow(`SELECT color_preset FROM users WHERE id=$1`, user.ID).Scan(&personalPreset); err != nil || personalPreset != "original" {
		t.Fatal("deletion changed personal preference", personalPreset, err)
	}
	id = ""
	selected, err = people.UpdateProfile(ctx, user.ID, auth.ProfileUpdate{ColorPreset: &id})
	if err != nil || selected.ColorPreset != "" {
		t.Fatal("site default not restored", selected, err)
	}
	var audit string
	if err = db.QueryRow(`SELECT details::text FROM audit_events WHERE action='profile' AND object_id=$1 ORDER BY id DESC LIMIT 1`, user.ID).Scan(&audit); err != nil {
		t.Fatal("preset choice audit missing", err)
	}
}
