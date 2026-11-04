package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

// Run only against the disposable database provided by with-test-services.py.
func TestDangerBrandingPersistence(t *testing.T) {
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
	var original []byte
	var originalVersion int64
	if err = db.QueryRowContext(ctx, `SELECT configuration,version FROM site_branding WHERE id=TRUE`).Scan(&original, &originalVersion); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `UPDATE site_branding SET configuration=$1,version=$2 WHERE id=TRUE`, original, originalVersion)
	if _, err = db.ExecContext(ctx, `UPDATE site_branding SET configuration='{}',version=1 WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	repo := NewSystemRepository(db)
	read, err := repo.Branding(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	var version int64
	if err = db.QueryRowContext(ctx, `SELECT configuration,version FROM site_branding WHERE id=TRUE`).Scan(&raw, &version); err != nil {
		t.Fatal(err)
	}
	var rawObject map[string]any
	if err = json.Unmarshal(raw, &rawObject); err != nil {
		t.Fatal(err)
	}
	if len(rawObject) != 0 || version != 1 || read.Dark["danger"] != "#f08088" {
		t.Fatal("read rewrote legacy row or missed defaults")
	}
	user, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Danger fixture admin", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, user.ID)
	if _, err = db.ExecContext(ctx, `UPDATE users SET is_system_admin=TRUE WHERE id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	seed := branding.Defaults().Normalize()
	seed.Dark["danger"], seed.Dark["on-danger"] = "#abcdef", "#012345"
	seed.Light["danger"], seed.Light["on-danger"] = "#fedcba", "#543210"
	second := branding.Defaults()
	second.Dark["danger"], second.Dark["on-danger"] = "#112233", "#445566"
	second.Light["danger"], second.Light["on-danger"] = "#778899", "#aabbcc"
	seed.Presets = append(seed.Presets, branding.Preset{ID: "second", Name: "Second", Dark: second.Dark, Light: second.Light})
	stored, err := repo.SaveBranding(ctx, user.ID, seed)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise exactly the legacy API decoder strategy, not only direct maps.
	legacy := branding.Defaults()
	payload, err := json.Marshal(map[string]any{"version": stored.Version, "name": "Older client", "dark": map[string]string{"accent": "#b8dc00"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(payload, &legacy); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.SaveBranding(ctx, user.ID, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != stored.Version+1 || saved.Name != "Older client" || len(saved.Presets) != 2 || saved.DefaultPreset != "original" || saved.Dark["accent"] != "#b8dc00" || saved.Dark["danger"] != "#abcdef" || saved.Dark["on-danger"] != "#012345" || saved.Light["danger"] != "#fedcba" || saved.Light["on-danger"] != "#543210" || !reflect.DeepEqual(saved.Presets[1], stored.Presets[1]) {
		t.Fatal("catalog-less save lost colors, identity or version", saved)
	}
	// Reorder and rename; switch default; introduce an unmatched ID.
	next := saved.Normalize()
	next.Presets[0], next.Presets[1] = next.Presets[1], next.Presets[0]
	next.Presets[0].Name = "Renamed"
	next.DefaultPreset = "second"
	fresh := branding.Defaults()
	next.Presets = append(next.Presets, branding.Preset{ID: "new", Name: "New", Dark: fresh.Dark, Light: fresh.Light})
	for _, p := range next.Presets {
		for _, palette := range []map[string]string{p.Dark, p.Light} {
			delete(palette, "danger")
			delete(palette, "on-danger")
		}
	}
	next.Presets[0].Dark["danger"] = "#f08088" // An explicit reset is different from omission.
	updated, err := repo.SaveBranding(ctx, user.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != saved.Version+1 || updated.DefaultPreset != "second" || updated.Dark["danger"] != "#f08088" || updated.Dark["on-danger"] != "#445566" || updated.Light["danger"] != "#778899" || updated.Presets[1].Dark["danger"] != "#abcdef" || updated.Presets[2].Dark["danger"] != "#f08088" || updated.Presets[2].Light["danger"] != "#b42336" {
		t.Fatal("explicit catalog failed per-ID preservation", updated)
	}
	var auditRaw []byte
	if err = db.QueryRowContext(ctx, `SELECT details FROM audit_events WHERE action='branding' ORDER BY id DESC LIMIT 1`).Scan(&auditRaw); err != nil {
		t.Fatal(err)
	}
	var audit struct {
		Before branding.Settings `json:"before"`
		After  branding.Settings `json:"after"`
	}
	if err = json.Unmarshal(auditRaw, &audit); err != nil {
		t.Fatal(err)
	}
	if audit.Before.Presets[1].Dark["danger"] != "#112233" || audit.After.Dark["danger"] != "#f08088" {
		t.Fatal("audit before was mutated or after was wrong")
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE action='branding'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	unchanged := func() {
		t.Helper()
		got, e := repo.Branding(ctx)
		if e != nil || !reflect.DeepEqual(got, updated) {
			t.Fatal("rejected write changed stored state", e)
		}
		var gotCount int
		if e = db.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE action='branding'`).Scan(&gotCount); e != nil || gotCount != count {
			t.Fatal("rejected write changed audit", e)
		}
	}
	if _, err = repo.SaveBranding(ctx, user.ID, legacy); !errors.Is(err, system.ErrConflict) {
		t.Fatal("stale valid update was not conflict", err)
	}
	unchanged()
	for _, catalog := range []bool{false, true} {
		for _, value := range []any{"", nil, "#abc", "not-a-color"} {
			invalid := branding.Defaults()
			body := map[string]any{"version": updated.Version, "dark": map[string]any{"danger": value}}
			if catalog {
				invalid = updated.Normalize()
				body = map[string]any{"presets": []any{map[string]any{"id": "second", "name": "Renamed", "dark": map[string]any{"danger": value}, "light": invalid.Light}}}
				// Use a complete old-role palette so danger is the invalid field.
				palette := map[string]any{}
				for k, v := range invalid.Dark {
					palette[k] = v
				}
				palette["danger"] = value
				body["presets"].([]any)[0].(map[string]any)["dark"] = palette
			}
			data, e := json.Marshal(body)
			if e != nil {
				t.Fatal(e)
			}
			if e = json.Unmarshal(data, &invalid); e != nil {
				t.Fatal(e)
			}
			if _, e = repo.SaveBranding(ctx, user.ID, invalid); e == nil {
				t.Fatal("invalid present value saved", catalog, value)
			}
			unchanged()
		}
	}
	deleted := updated.Normalize()
	deleted.Presets = deleted.Presets[:1]
	final, err := repo.SaveBranding(ctx, user.ID, deleted)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Presets) != 1 || final.Presets[0].ID != "second" || final.Version != updated.Version+1 {
		t.Fatal("deleted presets restored")
	}
}
