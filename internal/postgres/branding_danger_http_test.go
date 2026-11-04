package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/google/uuid"
)

type dangerHTTPFixture struct {
	db           *sql.DB
	repo         *SystemRepository
	actor, token string
	handler      http.Handler
}

func newDangerHTTPFixture(t *testing.T) *dangerHTTPFixture {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	var original []byte
	var version int64
	if err = db.QueryRow(`SELECT configuration,version FROM site_branding WHERE id=TRUE`).Scan(&original, &version); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`UPDATE site_branding SET configuration=$1,version=$2 WHERE id=TRUE`, original, version); err != nil {
			t.Error(err)
		}
	})
	people := NewAuthRepository(db)
	user, err := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Danger palette admin", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id=$1`, user.ID) })
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString() + uuid.NewString()
	digest := sha256.Sum256([]byte(token))
	if err = people.CreateSession(ctx, digest[:], user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	login, err := auth.New(ctx, people, auth.Config{SessionSecret: strings.Repeat("d", 32), PublicURL: "https://branding.fixture.test"})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewSystemRepository(db)
	return &dangerHTTPFixture{db: db, repo: repo, actor: user.ID, token: token, handler: api.NewGateway(api.Dependencies{Auth: login, System: repo, Keys: NewAPIKeyRepository(db)})}
}

func (f *dangerHTTPFixture) call(method, path string, value any) *httptest.ResponseRecorder {
	var body string
	if value != nil {
		raw, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		body = string(raw)
	}
	r := httptest.NewRequest(method, "https://branding.fixture.test"+path, strings.NewReader(body))
	r.Header.Set("Origin", "https://branding.fixture.test")
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "media_gateway_session", Value: f.token})
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func (f *dangerHTTPFixture) current(t *testing.T) branding.Settings {
	t.Helper()
	s, err := f.repo.Branding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func dangerClone(t *testing.T, s branding.Settings) branding.Settings {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var result branding.Settings
	if err = json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func dangerOmit(palette map[string]string) { delete(palette, "danger"); delete(palette, "on-danger") }
func dangerSync(s *branding.Settings) {
	for _, p := range s.Presets {
		if p.ID == s.DefaultPreset {
			s.Dark = p.Dark
			s.Light = p.Light
			return
		}
	}
}
func (f *dangerHTTPFixture) snapshot(t *testing.T) string {
	t.Helper()
	var row string
	var count int
	if err := f.db.QueryRow(`SELECT row_to_json(b)::text FROM site_branding b WHERE id=TRUE`).Scan(&row); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='branding' AND actor_id=$1`, f.actor).Scan(&count); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]any{row, count})
	return string(raw)
}
func (f *dangerHTTPFixture) put(t *testing.T, next branding.Settings) branding.Settings {
	t.Helper()
	before := f.current(t)
	w := f.call("PUT", "/v1/system/branding", next)
	if w.Code != 200 {
		t.Fatalf("PUT: %d %s", w.Code, w.Body.String())
	}
	var result branding.Settings
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	stored := f.current(t)
	if !reflect.DeepEqual(stored, result) {
		t.Fatal("HTTP response differs from persisted settings")
	}
	if result.Version != before.Version+1 {
		t.Fatal("wrong version increment")
	}
	var auditRaw []byte
	if err := f.db.QueryRow(`SELECT details FROM audit_events WHERE action='branding' AND actor_id=$1 ORDER BY id DESC LIMIT 1`, f.actor).Scan(&auditRaw); err != nil {
		t.Fatal(err)
	}
	var audit struct{ Before, After branding.Settings }
	if err := json.Unmarshal(auditRaw, &audit); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]branding.Settings{{before, audit.Before}, {result, audit.After}} {
		if !reflect.DeepEqual(pair[0].Presets, pair[1].Presets) || !reflect.DeepEqual(pair[0].Dark, pair[1].Dark) || !reflect.DeepEqual(pair[0].Light, pair[1].Light) || pair[0].DefaultPreset != pair[1].DefaultPreset || pair[0].Version != pair[1].Version {
			t.Fatal("audit palette snapshot changed or lost", string(auditRaw))
		}
	}
	return result
}
func (f *dangerHTTPFixture) seed(t *testing.T) branding.Settings {
	t.Helper()
	s := branding.Defaults().Normalize()
	s.Version = f.current(t).Version
	s.Presets[0].Dark["danger"], s.Presets[0].Dark["on-danger"] = "#aa0011", "#fafafa"
	s.Presets[0].Light["danger"], s.Presets[0].Light["on-danger"] = "#bb0022", "#ffffff"
	custom := branding.Defaults().Normalize().Presets[0]
	custom.ID = "custom"
	custom.Name = "Saved custom"
	custom.Dark["danger"], custom.Dark["on-danger"] = "#cc0033", "#eeeeee"
	custom.Light["danger"], custom.Light["on-danger"] = "#dd0044", "#dddddd"
	s.Presets = append(s.Presets, custom)
	s.DefaultPreset = "custom"
	dangerSync(&s)
	saved := f.put(t, s)
	if _, err := f.db.Exec(`UPDATE users SET color_preset='original' WHERE id=$1`, f.actor); err != nil {
		t.Fatal(err)
	}
	return saved
}
func (f *dangerHTTPFixture) selected(t *testing.T) {
	t.Helper()
	var value string
	if err := f.db.QueryRow(`SELECT color_preset FROM users WHERE id=$1`, f.actor).Scan(&value); err != nil || value != "original" {
		t.Fatal("saved user choice changed", value, err)
	}
}

func TestDangerBrandingHTTPPreservesOmittedRolesAndCatalogIdentity(t *testing.T) {
	f := newDangerHTTPFixture(t)
	saved := f.seed(t)
	t.Run("catalogless actual decoder", func(t *testing.T) {
		old := dangerClone(t, saved)
		old.Presets = nil
		old.DefaultPreset = ""
		dangerOmit(old.Dark)
		dangerOmit(old.Light)
		old.Dark["accent"] = "#b8dc00"
		old.Name = "Legacy edit"
		want := dangerClone(t, saved)
		want.Name = old.Name
		want.Dark["accent"] = "#b8dc00"
		for i := range want.Presets {
			if want.Presets[i].ID == want.DefaultPreset {
				want.Presets[i].Dark["accent"] = "#b8dc00"
			}
		}
		want.Version++
		saved = f.put(t, old)
		if !reflect.DeepEqual(saved, want) {
			t.Fatal("catalogless decoder reset action colors or other presets", saved, want)
		}
		f.selected(t)
	})
	t.Run("same IDs reordered and one explicit role", func(t *testing.T) {
		next := dangerClone(t, saved)
		want := dangerClone(t, saved)
		next.Presets[0], next.Presets[1] = next.Presets[1], next.Presets[0]
		want.Presets[0], want.Presets[1] = want.Presets[1], want.Presets[0]
		next.Presets[0].Name = "Renamed custom"
		want.Presets[0].Name = next.Presets[0].Name
		for i := range next.Presets {
			dangerOmit(next.Presets[i].Dark)
			dangerOmit(next.Presets[i].Light)
		}
		next.Presets[0].Dark["danger"] = "#990099"
		want.Presets[0].Dark["danger"] = "#990099"
		next.Presets[0].Light["on-danger"] = "#fafafa"
		want.Presets[0].Light["on-danger"] = "#fafafa"
		next.DefaultPreset = "original"
		want.DefaultPreset = next.DefaultPreset
		dangerSync(&next)
		dangerSync(&want)
		want.Version++
		saved = f.put(t, next)
		if !reflect.DeepEqual(saved, want) {
			t.Fatal("same-ID or missing companion preservation failed", saved, want)
		}
		f.selected(t)
	})
	t.Run("new ID does not inherit removed preset by name", func(t *testing.T) {
		next := dangerClone(t, saved)
		custom := next.Presets[0]
		dangerOmit(custom.Dark)
		dangerOmit(custom.Light)
		fresh := branding.Defaults()
		newPreset := branding.Preset{ID: "new-id", Name: "Original", Dark: fresh.Dark, Light: fresh.Light}
		next.Presets = []branding.Preset{custom, newPreset}
		next.DefaultPreset = "new-id"
		dangerSync(&next)
		saved = f.put(t, next)
		if len(saved.Presets) != 2 || saved.Presets[0].ID != "custom" || saved.Presets[1].ID != "new-id" || saved.DefaultPreset != "new-id" {
			t.Fatal("catalog identity/order changed")
		}
		if saved.Dark["danger"] != "#f08088" || saved.Dark["on-danger"] != "#171717" || saved.Light["danger"] != "#b42336" || saved.Light["on-danger"] != "#ffffff" {
			t.Fatal("new ID inherited removed preset colors")
		}
		if saved.Presets[0].Dark["danger"] != "#990099" || saved.Presets[0].Dark["on-danger"] != "#eeeeee" || saved.Presets[0].Light["danger"] != "#dd0044" || saved.Presets[0].Light["on-danger"] != "#fafafa" {
			t.Fatal("retained ID lost values")
		}
		f.selected(t)
	})
}

func TestDangerBrandingHTTPInvalidAndStaleWritesHaveNoEffects(t *testing.T) {
	f := newDangerHTTPFixture(t)
	saved := f.seed(t)
	for _, catalog := range []bool{false, true} {
		for _, theme := range []string{"dark", "light"} {
			for _, role := range []string{"danger", "on-danger"} {
				for _, bad := range []string{"", "red", "#fff", "url(secret)"} {
					next := dangerClone(t, saved)
					if catalog {
						for i := range next.Presets {
							dangerOmit(next.Presets[i].Dark)
							dangerOmit(next.Presets[i].Light)
						}
						p := next.Presets[0].Dark
						if theme == "light" {
							p = next.Presets[0].Light
						}
						p[role] = bad
					} else {
						next.Presets = nil
						next.DefaultPreset = ""
						dangerOmit(next.Dark)
						dangerOmit(next.Light)
						p := next.Dark
						if theme == "light" {
							p = next.Light
						}
						p[role] = bad
					}
					before := f.snapshot(t)
					w := f.call("PUT", "/v1/system/branding", next)
					if w.Code != 400 || f.snapshot(t) != before {
						t.Fatalf("invalid %v/%s/%s/%q: %d %s", catalog, theme, role, bad, w.Code, w.Body.String())
					}
				}
			}
		}
	}
	// Explicit JSON null is not omission, including when the HTTP decoder starts
	// from defaults and when a legacy client omits the preset catalog.
	for _, catalog := range []bool{false, true} {
		for _, theme := range []string{"dark", "light"} {
			for _, role := range []string{"danger", "on-danger"} {
				raw, err := json.Marshal(saved)
				if err != nil {
					t.Fatal(err)
				}
				var request map[string]any
				if err = json.Unmarshal(raw, &request); err != nil {
					t.Fatal(err)
				}
				if catalog {
					request["presets"].([]any)[0].(map[string]any)[theme].(map[string]any)[role] = nil
				} else {
					delete(request, "presets")
					delete(request, "default_preset")
					request[theme].(map[string]any)[role] = nil
				}
				before := f.snapshot(t)
				w := f.call("PUT", "/v1/system/branding", request)
				if w.Code != 400 || f.snapshot(t) != before {
					t.Fatalf("explicit null %v/%s/%s: %d %s", catalog, theme, role, w.Code, w.Body.String())
				}
			}
		}
	}
	stale := dangerClone(t, saved)
	stale.Presets = nil
	stale.DefaultPreset = ""
	dangerOmit(stale.Dark)
	dangerOmit(stale.Light)
	next := dangerClone(t, saved)
	next.Name = "Concurrent accepted edit"
	saved = f.put(t, next)
	before := f.snapshot(t)
	w := f.call("PUT", "/v1/system/branding", stale)
	if w.Code != 409 || f.snapshot(t) != before {
		t.Fatal("stale client mutated settings/audit", w.Code, w.Body.String())
	}
	if _, err := f.db.Exec(`UPDATE users SET is_system_admin=FALSE WHERE id=$1`, f.actor); err != nil {
		t.Fatal(err)
	}
	w = f.call("PUT", "/v1/system/branding", saved)
	if w.Code != 403 || f.snapshot(t) != before {
		t.Fatal("demoted administrator retained write authority", w.Code, w.Body.String())
	}
}

func TestDangerBrandingHTTPReadsExposeDefaultsWithoutPersistence(t *testing.T) {
	f := newDangerHTTPFixture(t)
	legacy := branding.Defaults()
	legacy.Dark["accent"], legacy.Dark["red"] = "#b8dc00", "#0000ff"
	legacy.Light["highlight"] = "#00ff00"
	dangerOmit(legacy.Dark)
	dangerOmit(legacy.Light)
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE site_branding SET configuration=$1 WHERE id=TRUE`, raw); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot(t)
	for repeat := 0; repeat < 2; repeat++ {
		for _, path := range []string{"/v1/system/branding", "/site.json"} {
			w := f.call("GET", path, nil)
			if w.Code != 200 {
				t.Fatal(path, w.Code, w.Body.String())
			}
			var response struct{ Settings, Branding branding.Settings }
			if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			got := response.Settings
			if path == "/site.json" {
				got = response.Branding
			}
			if got.Dark["danger"] != "#f08088" || got.Dark["on-danger"] != "#171717" || got.Light["danger"] != "#b42336" || got.Light["on-danger"] != "#ffffff" || got.Dark["accent"] != "#b8dc00" || got.Dark["red"] != "#0000ff" || got.Light["highlight"] != "#00ff00" {
				t.Fatal("read fallback changed role values", path, got)
			}
			if f.snapshot(t) != before {
				t.Fatal("read persisted normalization", path)
			}
		}
	}
}
