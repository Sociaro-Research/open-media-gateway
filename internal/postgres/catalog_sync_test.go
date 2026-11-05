package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

type catalogTestProvider struct{ capture catalogsync.Capture }

func (p *catalogTestProvider) CaptureCatalog(context.Context, string) (catalogsync.Capture, error) {
	return p.capture, nil
}
func (p *catalogTestProvider) CheckCatalogRate(context.Context, string, core.MediaModel, catalogsync.Rate) error {
	return nil
}

type catalogTestTransport struct{ body string }

func (p *catalogTestTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(p.body)), Header: http.Header{}}, nil
}

type catalogTestMailer struct {
	keys, subjects, text, markup []string
	fail                         bool
}

func (m *catalogTestMailer) SendCatalogReport(_ context.Context, _ string, subject, text, markup string, key string) error {
	m.keys = append(m.keys, key)
	m.subjects = append(m.subjects, subject)
	m.text = append(m.text, text)
	m.markup = append(m.markup, markup)
	if m.fail {
		return errors.New("transport down")
	}
	return nil
}

func TestCatalogSyncPersistedWorkflow(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewCatalogRepository(db)
	user, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Catalog admin", "")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := repo.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.Timezone = "Asia/Nicosia"
	settings.Email = "reports@example.test"
	if _, err = repo.SaveSettings(ctx, user.ID, settings); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("nonadmin changed settings", err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	settings, err = repo.SaveSettings(ctx, user.ID, settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.SaveSettings(ctx, user.ID, catalogsync.Settings{Timezone: "UTC", Version: 1}); !errors.Is(err, system.ErrConflict) {
		t.Fatal("missing CAS", err)
	}
	schedule := catalogsync.Schedule{Provider: "fixture", Enabled: true, Time: "04:30", Version: 1, Sources: []catalogsync.Source{{ID: "releases", URL: "https://example.test/releases", Parser: "manifest-v1"}}}
	if err = repo.EnsureSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	provider := &catalogTestProvider{capture: catalogsync.Capture{Models: []core.MediaModel{{ID: "known", Metadata: &core.ModelMetadata{Description: "Known model"}}}, IDs: []string{"known", "new"}}}
	transport := &catalogTestTransport{body: `{"models":[{"id":"known","metadata":{"release_date":"2026-06"}},{"id":"new","metadata":{"release_date":"2026-10-09"}}]}`}
	mailer := &catalogTestMailer{fail: true}
	runner := &catalogsync.Runner{Repo: repo, Providers: provider, Client: &http.Client{Transport: transport}, Mailer: mailer, CheckOrigin: "https://ORIGINAL.example.test:443/"}
	check := func() {
		t.Helper()
		if err := repo.Request(ctx, user.ID, "fixture"); err != nil {
			t.Fatal(err)
		}
		if err := runner.Tick(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	check()
	state, err := repo.State(ctx, "fixture")
	if err != nil || state.Version != 1 || state.Models["known"].Metadata.ReleaseDate != "2026-06" {
		t.Fatal("state", state, err)
	}
	if len(mailer.keys) != 1 {
		t.Fatal("missing email attempt")
	}
	key := mailer.keys[0]
	firstRuns, err := repo.Runs(ctx, "fixture", 50, 0)
	if err != nil || len(firstRuns) != 1 || firstRuns[0].CheckOrigin != "https://original.example.test" || key != "catalog-"+firstRuns[0].ID {
		t.Fatal("persisted check provenance/correlation", firstRuns, err)
	}
	runner.CheckOrigin = "https://current-sender.example.test"
	mailer.fail = false
	if _, err = db.Exec(`UPDATE catalog_sync_runs SET email_retry_at=NOW()`); err != nil {
		t.Fatal(err)
	}
	if err = runner.Tick(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(mailer.keys) != 2 || mailer.keys[1] != key {
		t.Fatal("email retry lost idempotency")
	}
	if mailer.subjects[0] != mailer.subjects[1] || mailer.text[0] != mailer.text[1] || mailer.markup[0] != mailer.markup[1] || strings.Contains(mailer.text[1], runner.CheckOrigin) {
		t.Fatal("configuration change relabeled the stored check or changed an idempotent retry payload")
	}
	check()
	runs, err := repo.Runs(ctx, "fixture", 50, 0)
	if err != nil || len(runs) != 2 || runs[0].Version != 1 || runs[0].EmailStatus != "sent" || runs[0].Completeness == nil || runs[0].Completeness.Status != "incomplete" || len(mailer.keys) != 3 {
		t.Fatal("quiet check", runs, err)
	}
	transport.body = strings.Replace(transport.body, "2026-06", "2026-07", 1)
	check()
	if len(mailer.keys) != 4 || !strings.Contains(mailer.text[3], "PREVIOUS WARNINGS") || !strings.Contains(mailer.text[3], "checks: 3") {
		t.Fatal("old warnings missing from change report")
	}
	provider.capture.Models = append(provider.capture.Models, core.MediaModel{ID: "new"})
	check()
	state, err = repo.State(ctx, "fixture")
	if err != nil || len(state.Warnings) != 0 || state.Models["new"].Metadata.ReleaseDate != "2026-10-09" {
		t.Fatal("code support did not resolve candidates", state, err)
	}
	versions, err := repo.Versions(ctx, "fixture", 50, 0)
	if err != nil || len(versions) != 3 || versions[2].Models["known"].Metadata.ReleaseDate != "2026-06" {
		t.Fatal("immutable versions", versions, err)
	}
	for _, ob := range state.Observations {
		if ob.Hash == "" {
			continue
		}
		text, err := repo.Document(ctx, ob.Hash)
		if err != nil || text == "" {
			t.Fatal("stored source snapshot unavailable", ob.Hash, err)
		}
	}
	var documents int
	if err = db.QueryRow(`SELECT COUNT(*) FROM catalog_sync_documents`).Scan(&documents); err != nil || documents != 3 {
		t.Fatal("source document deduplication", documents, err)
	}
	all, err := repo.Schedules(ctx)
	if err != nil || len(all) != 1 || all[0].NextCheck == nil || all[0].LastCheck == nil {
		t.Fatal("schedule", all, err)
	}
	stale := all[0]
	stale.Time = "06:45"
	saved, err := repo.SaveSchedule(ctx, user.ID, stale)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.SaveSchedule(ctx, user.ID, stale); !errors.Is(err, system.ErrConflict) {
		t.Fatal("schedule CAS", err)
	}
	if err = repo.Commit(ctx, stale, state, catalogsync.Run{ID: uuid.NewString(), Provider: "fixture", CompletedAt: time.Now()}, time.Now()); !errors.Is(err, system.ErrConflict) {
		t.Fatal("obsolete sources published", err)
	}
	loc, _ := time.LoadLocation(settings.Timezone)
	if saved.NextCheck.In(loc).Hour() != 6 {
		t.Fatal("timezone schedule", saved.NextCheck)
	}
	transport.body = "<html><main>Unknown replacement for the structured catalogue manifest.</main></html>"
	check()
	state, err = repo.State(ctx, "fixture")
	ob := state.Observations["releases"]
	if err != nil || ob.FetchedHash == "" || state.Models["known"].Metadata.ReleaseDate != "2026-07" {
		t.Fatal("failed source replaced confirmed metadata", state, err)
	}
	text, err := repo.Document(ctx, ob.FetchedHash)
	if err != nil || !strings.Contains(text, "Unknown replacement") {
		t.Fatal("failed source snapshot was not stored", err)
	}
}

func TestCatalogRunOriginSurvivesDatabaseCopyAndLegacyRetry(t *testing.T) {
	ctx := context.Background()
	sourceDB := auditDatabase(t, true)
	source := NewCatalogRepository(sourceDB)
	schedule := catalogsync.Schedule{Provider: "fixture", Time: "04:30", Version: 1, Sources: []catalogsync.Source{}}
	if err := source.EnsureSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := catalogsync.Run{ID: uuid.NewString(), CheckOrigin: "https://original.example.test", Provider: "fixture", StartedAt: now, CompletedAt: now, Version: 6, Status: "changed", EmailStatus: "pending"}
	if err := source.Commit(ctx, schedule, catalogsync.State{}, run, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	readPayload := func(db *sql.DB, id string) []byte {
		t.Helper()
		var payload []byte
		if err := db.QueryRow(`SELECT payload FROM catalog_sync_runs WHERE id=$1`, id).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	payload := readPayload(sourceDB, run.ID)
	copyDB := auditDatabase(t, true)
	if _, err := copyDB.Exec(`UPDATE catalog_sync_settings SET email='reports@example.test' WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	insert := func(id string, data []byte) {
		t.Helper()
		if _, err := copyDB.Exec(`INSERT INTO catalog_sync_runs(id,provider,completed_at,payload,email_status) VALUES($1,'fixture',$2,$3,'pending')`, id, now, data); err != nil {
			t.Fatal(err)
		}
	}
	insert(run.ID, payload)
	savedPayloads := map[string]string{run.ID: string(readPayload(copyDB, run.ID))}
	assertPayloadsUnchanged := func() {
		t.Helper()
		if string(readPayload(sourceDB, run.ID)) != string(payload) {
			t.Fatal("source run JSON was rewritten")
		}
		for id, expected := range savedPayloads {
			if string(readPayload(copyDB, id)) != expected {
				t.Fatal("copied run JSON was rewritten", id)
			}
		}
	}
	copyRepo := NewCatalogRepository(copyDB)
	pending, err := copyRepo.PendingEmails(ctx)
	if err != nil || len(pending) != 1 || pending[0].CheckOrigin != run.CheckOrigin || pending[0].ID != run.ID {
		t.Fatal("database copy lost original provenance", pending, err)
	}
	mailer := &catalogTestMailer{fail: true}
	runner := &catalogsync.Runner{Repo: copyRepo, Mailer: mailer, CheckOrigin: "https://different-sender.example.test"}
	if err := runner.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	mailer.fail = false
	if _, err := copyDB.Exec(`UPDATE catalog_sync_runs SET email_retry_at=NOW() WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := runner.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	if len(mailer.keys) != 2 || mailer.keys[0] != "catalog-"+run.ID || mailer.keys[1] != mailer.keys[0] || mailer.subjects[0] != mailer.subjects[1] || mailer.text[0] != mailer.text[1] || mailer.markup[0] != mailer.markup[1] {
		t.Fatal("copied report retry changed payload or idempotency")
	}
	if !strings.Contains(mailer.text[0], "Check instance: "+run.CheckOrigin) || strings.Contains(mailer.text[0], runner.CheckOrigin) {
		t.Fatal("copied report was relabeled by its sender")
	}
	for _, origin := range []string{"", "https://user:synthetic-secret@example.test"} {
		legacy := run
		legacy.ID, legacy.CheckOrigin = uuid.NewString(), origin
		data, err := json.Marshal(legacy)
		if err != nil {
			t.Fatal(err)
		}
		if origin == "" && strings.Contains(string(data), "check_origin") {
			t.Fatal("legacy JSON must omit the optional origin")
		}
		insert(legacy.ID, data)
		savedPayloads[legacy.ID] = string(readPayload(copyDB, legacy.ID))
		if err := runner.Tick(ctx, now); err != nil {
			t.Fatal(err)
		}
		last := len(mailer.text) - 1
		if last < 2 || !strings.Contains(mailer.text[last], "Check instance: unknown (not recorded)\nRun ID: "+legacy.ID) || !strings.Contains(mailer.markup[last], "Check instance: unknown (not recorded)") || strings.Contains(mailer.text[last], "synthetic-secret") || strings.Contains(mailer.markup[last], "synthetic-secret") || strings.Contains(mailer.text[last], runner.CheckOrigin) {
			t.Fatal("legacy/invalid stored origin was misattributed or reflected")
		}
		assertPayloadsUnchanged()
	}
}
func TestCatalogSyncCrossReplicaLock(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewCatalogRepository(db)
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := repo.WithLock(ctx, func(context.Context) error { close(entered); <-release; return nil })
		done <- err
	}()
	<-entered
	called := false
	locked, err := repo.WithLock(ctx, func(context.Context) error { called = true; return nil })
	close(release)
	if err != nil || locked || called {
		t.Fatal("overlapping replica", locked, called, err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	locked, err = repo.WithLock(ctx, func(context.Context) error { return nil })
	if err != nil || !locked {
		t.Fatal("lock not released", err)
	}
}
