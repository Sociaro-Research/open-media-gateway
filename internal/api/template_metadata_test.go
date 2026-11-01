package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

type metadataGateStore struct {
	storage.Store
	opens   atomic.Int64
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	fail    bool
}

func (s *metadataGateStore) Open(ctx context.Context, key string) (storage.Object, error) {
	s.opens.Add(1)
	if s.fail {
		return storage.Object{}, errors.New("synthetic media unavailable")
	}
	object, err := s.Store.Open(ctx, key)
	if err == nil {
		object.Reader = &metadataGateReader{ReadSeekCloser: object.Reader, ctx: ctx, store: s}
	}
	return object, err
}

type metadataGateReader struct {
	storage.ReadSeekCloser
	ctx   context.Context
	store *metadataGateStore
}

func (r *metadataGateReader) Read(data []byte) (int, error) {
	r.store.once.Do(func() { close(r.store.entered) })
	select {
	case <-r.store.release:
		return r.ReadSeekCloser.Read(data)
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
}
func metadataFixture(t *testing.T) (*Server, *templateMemory, string) {
	t.Helper()
	s, repo, _, _ := templateFixture(t)
	id := uuid.NewString()
	d := templates.Definition{Title: "Lightweight title", Description: "Lightweight description", MediaType: "video", Provider: "mock", Model: "dance", Prompt: "Image 1 and Image 2 follow Video 1", Syntax: "numbered"}
	d.Options.ReferenceMode = "references"
	d.Slots, _ = templates.Infer(d.Prompt, d.Syntax, []string{"image", "video"}, templateTestModel().ReferenceModes[0])
	repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
	cover := templateCopy(t, s, id, referencePNG(t))
	fixed := templateCopy(t, s, id, templateVideo())
	d.CoverID = cover.ID
	d.Slots[2].AssetID = fixed.ID
	repo.items[id] = templates.Record{ID: id, Version: 1, Revision: 1, Draft: d, Published: &d}
	return s, repo, id
}
func metadataCall(ctx context.Context, s *Server, path, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil).WithContext(ctx)
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	return w
}
func metadataJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal("invalid JSON response")
	}
	return body
}
func TestTemplateMetadataDoesNotReadFixedMedia(t *testing.T) {
	s, _, id := metadataFixture(t)
	store := &metadataGateStore{Store: s.storage, entered: make(chan struct{}), release: make(chan struct{}), fail: true}
	s.storage = store
	w := metadataCall(context.Background(), s, "/v1/templates/"+id+"/metadata", "service-secret")
	if w.Code != 200 {
		t.Fatalf("metadata status=%d", w.Code)
	}
	body := metadataJSON(t, w)
	if body["validation_status"] != "pending" || body["revision"] != float64(1) || body["template"].(map[string]any)["title"] != "Lightweight title" {
		t.Fatal("missing canonical pending metadata")
	}
	for _, field := range []string{"catalog", "fixed_references", "default_available", "asset_error"} {
		if _, ok := body[field]; ok {
			t.Fatal("unvalidated field exposed", field)
		}
	}
	if store.opens.Load() != 0 {
		t.Fatal("metadata touched storage")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("metadata must not be cached")
	}
}
func TestTemplateMetadataAuthorizationAndPublication(t *testing.T) {
	s, repo, id := metadataFixture(t)
	path := "/v1/templates/" + id + "/metadata"
	if w := metadataCall(context.Background(), s, path, ""); w.Code != http.StatusUnauthorized {
		t.Fatal("anonymous metadata", w.Code)
	}
	if w := sendTest(s.handler(), "GET", path, "", "alice", ""); w.Code != http.StatusUnauthorized {
		t.Fatal("browser session authorized generation metadata", w.Code)
	}
	issued, key := issueTestKey(t, s.handler(), "alice", `{"name":"Scoped metadata fixture","scopes":["generation","storage:manage"]}`)
	// Normal creation requires generation. Model a stored credential whose scope
	// was subsequently removed, without changing public creation policy.
	memory := s.keys.(*memoryKeys)
	memory.mu.Lock()
	for i := range memory.records {
		if memory.records[i].key.ID == issued.ID {
			memory.records[i].key.Scopes = []string{"storage:manage"}
		}
	}
	memory.mu.Unlock()
	if w := metadataCall(context.Background(), s, path, key); w.Code != http.StatusForbidden {
		t.Fatal("non-generation scope authorized metadata", w.Code)
	}
	item := repo.items[id]
	item.Published = nil
	repo.items[id] = item
	if w := metadataCall(context.Background(), s, path, "service-secret"); w.Code != 404 {
		t.Fatal("unpublished metadata visible", w.Code)
	}
	delete(repo.items, id)
	if w := metadataCall(context.Background(), s, path, "service-secret"); w.Code != 404 {
		t.Fatal("missing metadata visible", w.Code)
	}
}
func TestTemplateMetadataModelPolicyStillFailsClosed(t *testing.T) {
	s, _, id := metadataFixture(t)
	s.modelAccess = &modelAccessMemory{fail: true}
	w := metadataCall(context.Background(), s, "/v1/templates/"+id+"/metadata", "service-secret")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("metadata bypassed failed model policy", w.Code)
	}
	if _, ok := metadataJSON(t, w)["template"]; ok {
		t.Fatal("content escaped failed policy")
	}
}

func TestTemplateDetailRevisionGuardAndLegacyCompatibility(t *testing.T) {
	s, _, id := metadataFixture(t)
	path := "/v1/templates/" + id
	for _, q := range []string{"?revision=0", "?revision=2"} {
		if w := metadataCall(context.Background(), s, path+q, "service-secret"); w.Code != 409 {
			t.Fatal("stale revision accepted", q, w.Code)
		}
	}
	for _, q := range []string{"?revision=", "?revision=-1", "?revision=not-a-number", "?revision=1&revision=1"} {
		if w := metadataCall(context.Background(), s, path+q, "service-secret"); w.Code != 400 {
			t.Fatal("invalid revision accepted", q, w.Code)
		}
	}
	for _, q := range []string{"", "?revision=1"} {
		w := metadataCall(context.Background(), s, path+q, "service-secret")
		if w.Code != 200 {
			t.Fatal("compatible detail", w.Code)
		}
		body := metadataJSON(t, w)
		if body["validation_status"] != "ready" || body["default_available"] != true {
			t.Fatal("valid detail not ready")
		}
	}
	store := &metadataGateStore{Store: s.storage, fail: true}
	s.storage = store
	w := metadataCall(context.Background(), s, path+"?revision=1", "service-secret")
	body := metadataJSON(t, w)
	if w.Code != 200 || body["validation_status"] != "failed" || body["asset_error"] == "" || len(body["catalog"].([]any)) != 0 {
		t.Fatal("unavailable media reported ready")
	}
}
func TestTemplateMetadataCompletesWhileValidationIsHung(t *testing.T) {
	for _, change := range []string{"none", "revision", "unpublish", "cancel"} {
		t.Run(change, func(t *testing.T) {
			s, repo, id := metadataFixture(t)
			gate := &metadataGateStore{Store: s.storage, entered: make(chan struct{}), release: make(chan struct{})}
			s.storage = gate
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- metadataCall(ctx, s, "/v1/templates/"+id+"?revision=1", "service-secret") }()
			select {
			case <-gate.entered:
			case <-time.After(time.Second):
				t.Fatal("validation never inspected fixed bytes")
			}
			before := gate.opens.Load()
			metaCtx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			started := time.Now()
			w := metadataCall(metaCtx, s, "/v1/templates/"+id+"/metadata", "service-secret")
			metadataElapsed := time.Since(started)
			if w.Code != 200 || metadataJSON(t, w)["validation_status"] != "pending" || gate.opens.Load() != before {
				t.Fatal("metadata blocked on validation")
			}
			select {
			case <-done:
				t.Fatal("validation completed without bytes")
			default:
			}
			if change == "revision" || change == "unpublish" {
				item := repo.items[id]
				if change == "revision" {
					item.Revision++
				} else {
					item.Published = nil
				}
				repo.items[id] = item
			}
			if change == "cancel" {
				cancel()
			} else {
				close(gate.release)
			}
			select {
			case full := <-done:
				switch change {
				case "none":
					if full.Code != 200 || metadataJSON(t, full)["validation_status"] != "ready" {
						t.Fatal("valid media did not become ready")
					}
				case "revision":
					if full.Code != 409 {
						t.Fatal("revision changed during validation", full.Code)
					}
				case "unpublish":
					if full.Code != 404 {
						t.Fatal("unpublished during validation", full.Code)
					}
				case "cancel":
					if body := metadataJSON(t, full); body["validation_status"] == "ready" {
						t.Fatal("cancelled media validation became ready")
					}
				}
			case <-time.After(time.Second):
				t.Fatal("validation leaked after release/cancellation")
			}
			t.Log("pending metadata returned before fixed-media reader was released; transition=", change, "revision=", strconv.FormatInt(1, 10), "metadata_elapsed=", metadataElapsed)
		})
	}
}
