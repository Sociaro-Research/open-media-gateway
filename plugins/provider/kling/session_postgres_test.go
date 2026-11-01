package kling_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/internal/queue"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/internal/worker"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
	"github.com/google/uuid"
)

type sessionPGRoundTrip func(*http.Request) (*http.Response, error)

func (f sessionPGRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func sessionPGJSON(v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: int64(len(b)), Body: io.NopCloser(bytes.NewReader(b))}
}

type sessionPGHealth struct{}

func (sessionPGHealth) Check(context.Context) error { return nil }

type sessionPGProvider struct {
	*kling.Adapter
	cdn      *http.Client
	revision *atomic.Value
}

func (p sessionPGProvider) scope(ctx context.Context) context.Context {
	return core.WithProviderConnectionScope(ctx, core.ProviderConnectionScope{ID: "kling", OwnerID: identity.ProviderOwnerID(ctx), Revision: p.revision.Load().(string)})
}
func (p sessionPGProvider) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	return p.Adapter.ValidateRequest(p.scope(ctx), r)
}
func (p sessionPGProvider) Quote(ctx context.Context, m core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	q, _, e := p.Adapter.Quote(p.scope(ctx), m, r)
	return q, p.revision.Load().(string), e
}
func (p sessionPGProvider) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	return p.Adapter.Generate(p.scope(ctx), r)
}
func (p sessionPGProvider) GetTask(ctx context.Context, id string) (core.ProviderResult, error) {
	return p.Adapter.GetTask(p.scope(ctx), id)
}
func (p sessionPGProvider) OpenArtifact(ctx context.Context, ref string) (core.ArtifactSource, error) {
	return kling.SessionOpenTrackingForTest(p.Adapter, p.scope(ctx), ref, p.cdn)
}

type sessionPGDirectory struct{ p sessionPGProvider }

func (d sessionPGDirectory) Providers(context.Context) (map[string]core.MediaProvider, error) {
	return map[string]core.MediaProvider{"kling": d.p}, nil
}
func (d sessionPGDirectory) Provider(_ context.Context, id string) (core.MediaProvider, error) {
	if id != "kling" {
		return nil, core.ErrProviderUnavailable
	}
	return d.p, nil
}

func sessionPGServices(t *testing.T) (context.Context, *sql.DB, string, *queue.RedisQueue) {
	t.Helper()
	dsn, redisURL := os.Getenv("GATEWAY_TEST_DATABASE_URL"), os.Getenv("GATEWAY_TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("requires disposable PostgreSQL and Redis")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	base, e := postgres.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { base.Close() })
	schema := "kling_session_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = base.ExecContext(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { base.Exec("DROP SCHEMA " + schema + " CASCADE") })
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	v := u.Query()
	v.Set("search_path", schema)
	u.RawQuery = v.Encode()
	dsn = u.String()
	db, e := postgres.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = postgres.Migrate(db); e != nil {
		t.Fatal(e)
	}
	q, e := queue.Open(ctx, redisURL, "kling-session-"+uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { q.Close() })
	return ctx, db, dsn, q
}
func sessionPGRequest(h http.Handler, method, path, key string, body any, auth bool) *httptest.ResponseRecorder {
	var b bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&b).Encode(body)
	}
	r := httptest.NewRequest(method, path, &b)
	r.Header.Set("Content-Type", "application/json")
	if auth {
		r.Header.Set("Authorization", "Bearer session-gateway-fixture")
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func sessionPGID(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != 202 {
		t.Fatalf("admission %d: %s", w.Code, w.Body.String())
	}
	var v struct {
		ID string `json:"id"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil || v.ID == "" {
		t.Fatal("missing task", e)
	}
	return v.ID
}
func sessionPGRun(t *testing.T, ctx context.Context, repo *postgres.TaskRepository, q *queue.RedisQueue, p sessionPGProvider, store storage.Store, id string) core.Task {
	t.Helper()
	run, cancel := context.WithCancel(ctx)
	w := worker.New(repo, q, p, store, time.Millisecond, 2*time.Second, 2, time.Millisecond)
	w.OperationConcurrency = 1
	w.ArchiveConcurrency = 1
	done := make(chan error, 1)
	go func() { done <- w.Run(run) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	for {
		task, e := repo.Get(identity.WithOwnerID(ctx, identity.SystemOwnerID), id)
		if e != nil {
			t.Fatal(e)
		}
		if task.Status == core.TaskSucceeded || task.Status == core.TaskFailed {
			return task
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}
func sessionPGPrivate(t *testing.T, body string) {
	t.Helper()
	for _, s := range []string{"90071992547409931", "session-private-revision", "session-provider-fixture", "https://cdn.fixture", "source_sha256", `"source_reference"`, "_reference_resources", "session_id"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(s)) {
			t.Fatalf("private evidence exposed: %s", s)
		}
	}
}

// This exercises public admission, real task/resource transactions, Redis work,
// multi-artifact archival, process reconstruction and authenticated downloads.
// Only provider and CDN transports are mocked; no provider endpoint is contacted.
func TestKlingSessionPostgresPublicWorkflowArchiveAndRestart(t *testing.T) {
	ctx, db, dsn, q := sessionPGServices(t)
	owner := identity.WithOwnerID(ctx, identity.SystemOwnerID)
	video := testmedia.MP4(120, 30, 120)
	i := bytes.Index(video, []byte("tkhd"))
	binary.BigEndian.PutUint32(video[i+4+76:], 1280<<16)
	binary.BigEndian.PutUint32(video[i+4+80:], 720<<16)
	var mask bytes.Buffer
	if e := png.Encode(&mask, image.NewGray(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	cover := testmedia.PNG512("selection cover")
	tracking := []byte{0, 255, 13, 10, 1, 2, 3, 4}
	var submissions, downloads atomic.Int32
	var failNext atomic.Bool
	var revision atomic.Value
	revision.Store("session-private-revision")
	const nativeID = "90071992547409931"
	const exactX = 0.7738498789346246
	const exactY = 0.297142857142857
	native := &http.Client{Transport: sessionPGRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api-singapore.klingai.com" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer session-provider-fixture" {
			return nil, fmt.Errorf("unexpected provider transport")
		}
		submissions.Add(1)
		var b map[string]any
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			return nil, e
		}
		data := map[string]any{"status": 0, "session_id": nativeID}
		switch r.URL.Path {
		case "/v1/videos/multi-elements/init-selection":
			if len(b) != 1 || !strings.HasPrefix(fmt.Sprint(b["video_url"]), "https://gateway.fixture/references/") {
				return nil, fmt.Errorf("init source is not signed local inspected media: %v", b)
			}
			data["fps"] = 30
			data["total_frame"] = 120
			data["width"] = 1280
			data["height"] = 720
			data["original_duration"] = 4000
			data["normalized_video"] = "https://cdn.fixture/normalized.mp4"
		case "/v1/videos/multi-elements/add-selection", "/v1/videos/multi-elements/delete-selection":
			if failNext.Swap(false) {
				return nil, errors.New("https://cdn.fixture/private-transport?token=secret")
			}
			if b["session_id"] != nativeID || b["frame_index"] != float64(0) {
				return nil, errors.New("native session/frame changed")
			}
			points, _ := b["points"].([]any)
			if len(points) != 1 {
				return nil, errors.New("point cardinality changed")
			}
			point, _ := points[0].(map[string]any)
			if point["x"] != exactX || point["y"] != exactY {
				return nil, errors.New("point precision changed")
			}
			masks := []any{}
			if strings.Contains(r.URL.Path, "/add-") {
				masks = append(masks, map[string]any{"object_id": 0, "rle_mask": map[string]any{"size": []int{2, 2}, "counts": "04"}, "png_mask": map[string]any{"size": []int{2, 2}, "base64": base64.StdEncoding.EncodeToString(mask.Bytes())}})
			}
			data["res"] = map[string]any{"frame_index": 0, "rle_mask_list": masks}
		case "/v1/videos/multi-elements/clear-selection":
			if len(b) != 1 || b["session_id"] != nativeID {
				return nil, errors.New("clear body changed")
			}
		case "/v1/videos/multi-elements/preview-selection":
			if len(b) != 1 || b["session_id"] != nativeID {
				return nil, errors.New("preview body changed")
			}
			data["res"] = map[string]any{"video": "https://cdn.fixture/preview.mp4", "video_cover": "https://cdn.fixture/cover.png", "tracking_output": "https://cdn.fixture/opaque-track"}
		default:
			return nil, fmt.Errorf("unexpected upstream path %s", r.URL.Path)
		}
		return sessionPGJSON(map[string]any{"code": 0, "data": data}), nil
	})}
	cdn := &http.Client{Transport: sessionPGRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "cdn.fixture" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			return nil, errors.New("CDN credentials or destination invalid")
		}
		downloads.Add(1)
		b, typ := video, "video/mp4"
		switch r.URL.Path {
		case "/cover.png":
			b, typ = cover, "image/png"
		case "/opaque-track":
			b, typ = tracking, "application/octet-stream"
		case "/preview.mp4", "/normalized.mp4":
		default:
			return nil, errors.New("unexpected CDN path")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {typ}}, ContentLength: int64(len(b)), Body: io.NopCloser(bytes.NewReader(b))}, nil
	})}
	extra := map[string]string{}
	for _, op := range []string{"init", "add", "delete", "clear", "preview"} {
		extra["selection_"+op+"_price"] = "0.001"
	}
	raw, e := kling.Definition().Factory(providerkit.Credentials{APIKey: "session-provider-fixture", Extra: extra}, nil, native)
	if e != nil {
		t.Fatal(e)
	}
	p := sessionPGProvider{Adapter: raw.(*kling.Adapter), cdn: cdn, revision: &revision}
	directory := sessionPGDirectory{p}
	root := t.TempDir()
	makeHost := func(db *sql.DB) (*postgres.TaskRepository, *postgres.ProviderResourceRepository, *storage.Local, *references.Library, http.Handler) {
		repo := postgres.NewTaskRepository(db)
		resources := postgres.NewProviderResourceRepository(db)
		store, e := storage.NewLocal(root, cdn, 1<<20)
		if e != nil {
			t.Fatal(e)
		}
		library := references.New(postgres.NewReferenceRepository(db), store)
		service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"kling": p}, map[string]string{"kling": kling.SessionInit}, "kling")
		service.EnableAccounting()
		scopes := func(ctx context.Context, _ string) (core.ProviderConnectionScope, error) {
			return core.ProviderConnectionScope{ID: "kling", OwnerID: identity.ProviderOwnerID(ctx), Revision: revision.Load().(string)}, nil
		}
		h := api.NewGateway(api.Dependencies{Service: service, Health: sessionPGHealth{}, APIKey: "session-gateway-fixture", Storage: store, SigningSecret: "session-signing-fixture", MediaURLTTL: time.Hour, References: library, AssetPublicURL: "https://gateway.fixture", ResourceRepository: resources, ResourceScopes: scopes, ResourceDirectory: directory})
		return repo, resources, store, library, h
	}
	repo, resources, store, library, h := makeHost(db)
	ref, e := library.Upload(owner, "Inspected selection.mp4", bytes.NewReader(video))
	if e != nil {
		t.Fatal(e)
	}
	initBody := map[string]any{"provider": "kling", "model": kling.SessionInit, "kind": "editing_session", "subtype": "selection", "name": "Owned selection", "reference_ids": []string{ref.ID}}
	initReply := sessionPGRequest(h, "POST", "/v1/provider-resources", "init-once", initBody, true)
	sessionPGPrivate(t, initReply.Body.String())
	initID := sessionPGID(t, initReply)
	initialized := sessionPGRun(t, ctx, repo, q, p, store, initID)
	if initialized.Status != core.TaskSucceeded || len(initialized.StoredResourceReferences) != 1 {
		t.Fatalf("initialization did not persist: %+v error %v", initialized.Status, initialized.Error)
	}
	bindingID := initialized.StoredResourceReferences[0].BindingID
	b, e := resources.GetBinding(owner, bindingID)
	if e != nil || b.RemoteID != nativeID || b.Session.SelectionRevision != 0 || b.Session.FPS != 30 || b.Session.FrameCount != 120 || b.OriginTaskID != initID {
		t.Fatal("init binding evidence", b, e)
	}
	if got := sessionPGID(t, sessionPGRequest(h, "POST", "/v1/provider-resources", "init-once", initBody, true)); got != initID {
		t.Fatal("init replay duplicated")
	}
	action := func(h http.Handler, op string, rev int64, key string, frame bool, recover bool) *httptest.ResponseRecorder {
		body := map[string]any{"expected_revision": rev}
		if frame {
			body["frame"] = map[string]any{"frame_index": 0, "points": []map[string]float64{{"x": exactX, "y": exactY}}}
		}
		if recover {
			body["recover"] = true
		}
		return sessionPGRequest(h, "POST", "/v1/provider-resources/"+bindingID+"/actions/"+op, key, body, true)
	}
	run := func(h http.Handler, repo *postgres.TaskRepository, store *storage.Local, op string, rev int64, frame bool) core.Task {
		posted := action(h, op, rev, uuid.NewString(), frame, false)
		sessionPGPrivate(t, posted.Body.String())
		task := sessionPGRun(t, ctx, repo, q, p, store, sessionPGID(t, posted))
		if task.Status != core.TaskSucceeded {
			t.Fatalf("%s failed: %v", op, task.Error)
		}
		return task
	}
	added := run(h, repo, store, "add", 0, true)
	if len(added.StoredArtifacts) != 3 {
		t.Fatal("mask variants lost", added.StoredArtifacts)
	}
	b, e = resources.GetBinding(owner, bindingID)
	if e != nil || b.Session.SelectionRevision != 1 || len(b.Session.Selections) != 1 || b.Session.Selections[0].Points[0].X != exactX {
		t.Fatal("point/revision not persisted", b, e)
	}
	// Reopen repositories, API and storage. The same binding and exact state survive.
	reopened, e := postgres.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	repo, resources, store, _, h = makeHost(reopened)
	var rejectedBefore, rejectedAfter int
	if e = db.QueryRowContext(ctx, "SELECT count(*) FROM tasks").Scan(&rejectedBefore); e != nil {
		t.Fatal(e)
	}
	stale := action(h, "add", 0, "stale", true, false)
	if stale.Code < 400 {
		t.Fatal("stale revision admitted")
	}
	beforeCalls := submissions.Load()
	rounded := map[string]any{"expected_revision": 1, "frame": map[string]any{"frame_index": 0, "points": []map[string]float64{{"x": math.Nextafter(exactX, 1), "y": exactY}}}}
	bad := sessionPGRequest(h, "POST", "/v1/provider-resources/"+bindingID+"/actions/delete", "rounded", rounded, true)
	if bad.Code != 400 || submissions.Load() != beforeCalls {
		t.Fatal("inexact deletion dispatched", bad.Code)
	}
	if e = db.QueryRowContext(ctx, "SELECT count(*) FROM tasks").Scan(&rejectedAfter); e != nil || rejectedBefore != rejectedAfter {
		t.Fatal("rejected action reserved task", rejectedBefore, rejectedAfter, e)
	}
	deleted := run(h, repo, store, "delete", 1, true)
	_ = deleted
	b, e = resources.GetBinding(owner, bindingID)
	if e != nil || b.Session.SelectionRevision != 2 || b.Session.HasSelection {
		t.Fatal("delete changed binding", b, e)
	}
	_ = run(h, repo, store, "add", 2, true)
	_ = run(h, repo, store, "clear", 3, false)
	b, e = resources.GetBinding(owner, bindingID)
	if e != nil || b.Session.SelectionRevision != 4 || b.Session.HasSelection {
		t.Fatal("clear changed binding", b, e)
	}
	beforePreview := b
	preview := run(h, repo, store, "preview", 4, false)
	if len(preview.StoredArtifacts) != 4 || len(preview.StoredResourceReferences) != 0 {
		t.Fatalf("preview terminalized with wrong artifacts/resources: %d/%d", len(preview.StoredArtifacts), len(preview.StoredResourceReferences))
	}
	b, e = resources.GetBinding(owner, bindingID)
	if e != nil || !providerresources.EqualSession(beforePreview.Session, b.Session) || b.OriginTaskID != initID {
		t.Fatal("preview mutated resource", b, e)
	}
	var bindingCount int
	if e = db.QueryRowContext(ctx, "SELECT count(*) FROM provider_resources").Scan(&bindingCount); e != nil || bindingCount != 1 {
		t.Fatal("session cloned binding", bindingCount, e)
	}
	// Restart after archival, then deliver every preserved output without CDN work.
	restarted, e := postgres.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	readRepo, readResources, _, _, readAPI := makeHost(restarted)
	beforeDownloads := downloads.Load()
	status := sessionPGRequest(readAPI, "GET", "/v1/generations/"+preview.ID, "", nil, true)
	if status.Code != 200 {
		t.Fatal(status.Code, status.Body.String())
	}
	sessionPGPrivate(t, status.Body.String())
	var view struct {
		Artifacts []struct {
			ContentURL string `json:"content_url"`
			MediaType  string `json:"media_type"`
			Format     string `json:"format"`
		} `json:"artifacts"`
	}
	if e = json.Unmarshal(status.Body.Bytes(), &view); e != nil || len(view.Artifacts) != 4 {
		t.Fatal("restart variants", view, e)
	}
	for index, a := range view.Artifacts {
		if a.ContentURL == "" {
			t.Fatal("missing signed content URL", index)
		}
		got := sessionPGRequest(readAPI, "GET", a.ContentURL, "", nil, true)
		if got.Code != 200 {
			t.Fatal("download", index, got.Code, got.Body.String())
		}
		expected := [][]byte{video, cover, tracking, nil}[index]
		if index < 3 && !bytes.Equal(got.Body.Bytes(), expected) {
			t.Fatal("archived bytes changed", index)
		}
		if index == 3 {
			var state struct {
				Action   string `json:"action"`
				Revision int64  `json:"selection_revision"`
			}
			if json.Unmarshal(got.Body.Bytes(), &state) != nil || state.Action != "preview" || state.Revision != 4 {
				t.Fatal("state summary changed", got.Body.String())
			}
			sessionPGPrivate(t, got.Body.String())
		}
		if index == 2 && (got.Header().Get("Content-Type") != "application/octet-stream" || !strings.Contains(got.Header().Get("Content-Disposition"), "attachment")) {
			t.Fatal("tracking is not safe binary attachment", got.Header())
		}
		head := sessionPGRequest(readAPI, "HEAD", a.ContentURL, "", nil, true)
		if head.Code != 200 || head.Body.Len() != 0 {
			t.Fatal("HEAD", index, head.Code)
		}
		unauth := sessionPGRequest(readAPI, "GET", a.ContentURL, "", nil, false)
		if unauth.Code != 401 && unauth.Code != 403 {
			t.Fatal("unauthenticated download", index, unauth.Code)
		}
	}
	accessReply := sessionPGRequest(readAPI, "GET", "/v1/generations/"+preview.ID+"/media-access", "", nil, true)
	if accessReply.Code != 200 {
		t.Fatal("signed access", accessReply.Code, accessReply.Body.String())
	}
	sessionPGPrivate(t, accessReply.Body.String())
	var access struct {
		Artifacts []struct {
			ID       string `json:"id"`
			Preview  string `json:"preview_url"`
			Download string `json:"download_url"`
		} `json:"artifacts"`
	}
	if e = json.Unmarshal(accessReply.Body.Bytes(), &access); e != nil || len(access.Artifacts) != 4 {
		t.Fatal("signed artifact variants", access, e)
	}
	for index, granted := range access.Artifacts {
		for _, link := range []string{granted.Preview, granted.Download} {
			parsed, parseErr := url.Parse(link)
			if parseErr != nil || parsed.Path != "/media/"+preview.ID || parsed.Query().Get("artifact") != granted.ID || parsed.Query().Get("signature") == "" {
				t.Fatal("signed route lacks local artifact identity", link, parseErr)
			}
			for _, method := range []string{"GET", "HEAD"} {
				got := sessionPGRequest(readAPI, method, link, "", nil, false)
				if got.Code != 200 || method == "HEAD" && got.Body.Len() != 0 {
					t.Fatal("signed delivery", index, method, got.Code)
				}
				if method == "GET" && index < 3 && !bytes.Equal(got.Body.Bytes(), [][]byte{video, cover, tracking}[index]) {
					t.Fatal("signed delivery changed bytes", index)
				}
				if link == granted.Download && !strings.HasPrefix(got.Header().Get("Content-Disposition"), "attachment;") {
					t.Fatal("signed download disposition", index, got.Header())
				}
			}
			query := parsed.Query()
			query.Set("signature", "invalid")
			parsed.RawQuery = query.Encode()
			if denied := sessionPGRequest(readAPI, "GET", parsed.String(), "", nil, false); denied.Code != 403 {
				t.Fatal("tampered signature accepted", index, denied.Code)
			}
		}
	}
	if downloads.Load() != beforeDownloads {
		t.Fatal("restart content fetched provider again")
	}
	foreign := identity.WithOwnerID(ctx, uuid.NewString())
	if _, e = readRepo.Get(foreign, preview.ID); !errors.Is(e, core.ErrTaskNotFound) {
		t.Fatal("foreign task read", e)
	}
	if _, e = readResources.GetBinding(foreign, bindingID); !errors.Is(e, providerresources.ErrNotFound) {
		t.Fatal("foreign binding read", e)
	}
	// A dispatched mutation failure fences the binding until an explicit clear.
	failureBefore := submissions.Load()
	failNext.Store(true)
	failed := sessionPGRun(t, ctx, repo, q, p, store, sessionPGID(t, action(h, "add", 4, "uncertain-add", true, false)))
	if submissions.Load() != failureBefore+1 {
		t.Fatal("uncertain mutation retried", submissions.Load()-failureBefore)
	}
	if failed.Status != core.TaskFailed {
		t.Fatal("uncertain dispatch reported success")
	}
	b, e = resources.GetBinding(owner, bindingID)
	if e != nil || b.Status != providerresources.Failed || b.Session.SelectionRevision != 4 {
		t.Fatal("uncertain binding not fenced", b, e)
	}
	denied := action(h, "clear", 4, "implicit-recovery", false, false)
	if denied.Code < 400 {
		t.Fatal("implicit recovery admitted")
	}
	recovered := sessionPGRun(t, ctx, repo, q, p, store, sessionPGID(t, action(h, "clear", 4, "explicit-recovery", false, true)))
	if recovered.Status != core.TaskSucceeded {
		t.Fatal("recovery failed", recovered.Error)
	}
	b, e = resources.GetBinding(owner, bindingID)
	if e != nil || b.Status != providerresources.Succeeded || b.Session.SelectionRevision != 5 || b.Session.HasSelection {
		t.Fatal("recovery state", b, e)
	}
	failedView := sessionPGRequest(h, "GET", "/v1/generations/"+failed.ID, "", nil, true)
	sessionPGPrivate(t, failedView.Body.String())
	if strings.Contains(failedView.Body.String(), "token=secret") {
		t.Fatal("transport detail leaked")
	}
	revision.Store("replacement-revision")
	beforeCalls = submissions.Load()
	changed := action(readAPI, "preview", 5, "changed-connection", false, false)
	if changed.Code < 400 || submissions.Load() != beforeCalls {
		t.Fatal("replaced connection reused session", changed.Code)
	}
}
