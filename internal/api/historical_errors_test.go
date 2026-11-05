package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/diagnostics"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"github.com/google/uuid"
)

// Insert old producer output directly: newer write-side error mapping cannot
// conceal a broken read boundary in this regression.
func TestHistoricalDatabaseErrorsAreProjectedOnAPIAndJournalReads(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "historical_errors_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := postgres.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = postgres.Migrate(db); err != nil {
		t.Fatal(err)
	}
	service := core.NewTaskService(postgres.NewTaskRepository(db), nil, "mock", "fixture")
	h := New(service, healthStub{}, "fixture")
	for _, tc := range []struct{ raw, action string }{
		{"private-body https://private.invalid/?token=private-token /private/server/path", "Stored diagnostic details"},
		{"provider polling failed: get ByteDance video task: HTTP 401: private-body private-token", "API key"},
		{"provider submission failed: DashScope HTTP 429: private-body private-token", "frequency"},
		{"store provider result: S3 upload failed: storage endpoint hostname could not be resolved", "DNS"},
	} {
		id := uuid.NewString()
		_, err := db.ExecContext(ctx, `INSERT INTO tasks(id,status,provider,model,media_type,prompt,owner_id,provider_owner_id,storage_owner_id,error) VALUES($1,'failed','mock','fixture','video','historical fixture',$2,$2,$2,$3)`, id, identity.SystemOwnerID, tc.raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/v1/generations/" + id, "/v1/videos/" + id, "/v1/generations", "/v1/generations?cursor="} {
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("Authorization", "Bearer fixture")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body)
			}
			if strings.Contains(w.Body.String(), "private-body") || strings.Contains(w.Body.String(), "private-token") || strings.Contains(w.Body.String(), "/private/server/path") || !strings.Contains(w.Body.String(), tc.action) || !strings.Contains(w.Body.String(), `"generation_failed"`) {
				t.Fatalf("unsafe or unhelpful historical read %s: %s", path, w.Body)
			}
		}
		var saved string
		if err := db.QueryRowContext(ctx, `SELECT error FROM tasks WHERE id=$1`, id).Scan(&saved); err != nil || saved != tc.raw {
			t.Fatal("reading changed persisted diagnostic", err)
		}
	}
}

func TestHistoricalReportErrorProjectionPreservesAccessAndInputs(t *testing.T) {
	s, _ := newPersonalTestServer()
	task := detailTask()
	task.Status = core.TaskFailed
	raw := "private-body /private/server/path"
	task.Error = &raw
	task.Options.Parameters = map[string]any{"object": "generation", "error": "user-sentinel"}
	s.service = &detailReportService{&accountService{tasks: []core.Task{task}}}
	h := s.handler()
	path := "/v1/admin/reports/" + task.ID
	w := sendTest(h, "GET", path, "", "alice", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-body") || !strings.Contains(w.Body.String(), "user-sentinel") || !strings.Contains(w.Body.String(), diagnostics.UnknownGeneration) {
		t.Fatal(w.Code, w.Body)
	}
	for _, tc := range []struct {
		session string
		status  int
	}{{"bob", 404}, {"", 401}} {
		w := sendTest(h, "GET", path, "", tc.session, "")
		if w.Code != tc.status || strings.Contains(w.Body.String(), "user-sentinel") {
			t.Fatal("historical projection changed access", w.Code)
		}
	}
	if *task.Error != raw {
		t.Fatal("report mutated saved error")
	}
}

type historicalAssetRepository struct {
	providerassets.Repository
	record providerassets.Record
}

func (f *historicalAssetRepository) Get(context.Context, string) (providerassets.Record, error) {
	return f.record, nil
}
func (f *historicalAssetRepository) List(context.Context, string, string, int) ([]providerassets.Record, error) {
	return []providerassets.Record{f.record}, nil
}

type historicalAssetClient struct {
	providerassets.Client
	items []providerassets.Asset
}

func (*historicalAssetClient) Scope() string { return "fixture" }
func (f *historicalAssetClient) List(context.Context, string, string, string) (providerassets.Page, error) {
	return providerassets.Page{Items: f.items}, nil
}

type historicalAssetDirectory struct{ client *historicalAssetClient }

func (f historicalAssetDirectory) AssetLibrary(context.Context, string) (providerassets.Client, error) {
	return f.client, nil
}

func TestHistoricalProviderAssetReadsProjectWithoutSaving(t *testing.T) {
	s, _ := newPersonalTestServer()
	raw := "private-body https://private.invalid/?token=private-token"
	repo := &historicalAssetRepository{record: providerassets.Record{ID: uuid.NewString(), Provider: "bytedance", Scope: "fixture", Asset: providerassets.Asset{Status: "Failed", Failure: raw}}}
	client := &historicalAssetClient{items: []providerassets.Asset{{ID: "native-fixture", Status: "Failed", Failure: raw}}}
	s.providerAssets = providerassets.New(repo, historicalAssetDirectory{client})
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"Asset reader"}`)
	for _, path := range []string{"/v1/references/providers/bytedance/assets", "/v1/references/providers/bytedance/assets/" + repo.record.ID, "/v1/references/providers/bytedance/inventory"} {
		w := sendTest(h, "GET", path, "", "", key)
		var fields map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &fields) != nil || strings.Contains(w.Body.String(), "private-body") || !strings.Contains(w.Body.String(), "Stored asset diagnostics") {
			t.Fatal(path, w.Code, w.Body)
		}
	}
	if repo.record.Failure != raw || client.items[0].Failure != raw {
		t.Fatal("read projection mutated asset source")
	}
}
