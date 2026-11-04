package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"github.com/google/uuid"
)

type admissionHealth struct{}

func (admissionHealth) Check(context.Context) error { return nil }

type admissionProvider struct {
	core.MediaProvider
	enabled bool
	submits int
}

func (p *admissionProvider) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	return p.MediaProvider.(core.ModelCatalog).ListModels(ctx)
}

func (p *admissionProvider) Availability(context.Context) (bool, bool, error) {
	return p.enabled, true, nil
}
func (p *admissionProvider) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	p.submits++
	panic("admission must never submit to a provider")
}

// Real HTTP middleware, core and PostgreSQL admission; no worker/provider call.
func TestGenerationAdmissionHTTPAcceptedRetryBeforeMutableValidation(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/v1/videos", `{"model":"mock/mock-video-v1","prompt":"test","seconds":"5"}`},
		{"/v1/generations", `{"model":"mock/mock-video-v1","prompt":"test"}`},
		{"/v1/images/generations", `{"model":"mock/mock-image-v1","prompt":"test"}`},
		{"/v1/images/edits", `{"model":"mock/mock-image-v1","prompt":"test","image":"https://fixture.invalid/a.png"}`},
		{"/v1/audio/speech", `{"model":"mock/mock-speech-v1","input":"test","voice":"Mock","response_format":"mp3"}`},
		{"/v1/audio/generations", `{"model":"mock/mock-music-v1","prompt":"test"}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			db := auditDatabase(t, true)
			repo := NewTaskRepository(db)
			q := &quantityQueue{}
			p := &admissionProvider{MediaProvider: mock.New(time.Millisecond, ""), enabled: true}
			svc := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"mock": p}, map[string]string{"mock": "mock-video-v1"}, "mock")
			h := api.New(svc, admissionHealth{}, "synthetic-secret")
			send := func(body, key string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", tc.path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer synthetic-secret")
				r.Header.Set("Idempotency-Key", key)
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			first := send(tc.body, "same-intent")
			if first.Code != 202 {
				t.Fatal(first.Code, first.Body.String())
			}
			var accepted map[string]any
			if e := json.Unmarshal(first.Body.Bytes(), &accepted); e != nil {
				t.Fatal(e)
			}
			p.enabled = false
			retry := send(tc.body, "same-intent")
			if retry.Code != 202 {
				t.Fatal("accepted retry revalidated unavailable provider", retry.Code, retry.Body.String())
			}
			var replay map[string]any
			json.Unmarshal(retry.Body.Bytes(), &replay)
			if replay["id"] != accepted["id"] {
				t.Fatal("replay changed task")
			}
			if w := send(tc.body, "new-intent"); w.Code < 400 {
				t.Fatal("new request admitted disabled provider", w.Code)
			}
			if w := send(strings.Replace(tc.body, `"test"`, `"changed"`, 1), "same-intent"); w.Code < 400 {
				t.Fatal("changed caller intent recovered")
			}
			for _, table := range []string{"tasks", "task_queue_outbox"} {
				var n int
				if e := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil || table == "tasks" && n != 1 || table == "task_queue_outbox" && n != 0 {
					t.Fatal(table, n, e)
				}
			}
			if p.submits != 0 {
				t.Fatal("provider submitted during admission")
			}
		})
	}
}

func TestGenerationAdmissionHTTPMultipartRecoveryAndEncodingSeparation(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewTaskRepository(db)
	q := &quantityQueue{}
	p := &admissionProvider{MediaProvider: mock.New(time.Millisecond, ""), enabled: true}
	svc := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"mock": p}, map[string]string{"mock": "mock-video-v1"}, "mock")
	h := api.New(svc, admissionHealth{}, "synthetic-secret")
	send := func(data []byte, contentType string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/videos", bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer synthetic-secret")
		r.Header.Set("Idempotency-Key", "multipart-intent")
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	form := func() ([]byte, string) {
		var b bytes.Buffer
		m := multipart.NewWriter(&b)
		m.WriteField("model", "mock/mock-video-v1")
		m.WriteField("prompt", "test")
		m.WriteField("seconds", "5")
		m.Close()
		return b.Bytes(), m.FormDataContentType()
	}
	data, kind := form()
	first := send(data, kind)
	if first.Code != 202 {
		t.Fatal(first.Code, first.Body.String())
	}
	p.enabled = false
	data, kind = form()
	retry := send(data, kind)
	if retry.Code != 202 {
		t.Fatal("new multipart boundary lost committed intent", retry.Code, retry.Body.String())
	}
	collision := []byte(`{"Values":{"model":["mock/mock-video-v1"],"prompt":["test"],"seconds":["5"]},"Files":{}}`)
	if w := send(collision, "application/json"); w.Code < 400 {
		t.Fatal("JSON impersonated multipart upload", w.Code)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&n)
	if n != 1 || p.submits != 0 {
		t.Fatal(n, p.submits)
	}
}

func TestGenerationAdmissionHTTPPreservesNormalizedReplay(t *testing.T) {
	db := auditDatabase(t, true)
	svc := core.NewTaskServiceWithProviders(NewTaskRepository(db), &quantityQueue{}, map[string]core.MediaProvider{"mock": mock.New(time.Millisecond, "")}, map[string]string{"mock": "mock-video-v1"}, "mock")
	h := api.New(svc, admissionHealth{}, "synthetic-secret")
	for _, tc := range []struct{ path, body string }{
		{"/v1/videos", `{"model":"mock/mock-video-v1","prompt":"test","seconds":"5"}`},
		{"/v1/videos", `{"model":"mock/mock-video-v1","prompt":"test","seconds":"05","webhook_url":null}`},
		{"/v1/generations", `{"model":"mock/mock-video-v1","prompt":"test","seconds":"+5"}`},
	} {
		r := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer synthetic-secret")
		r.Header.Set("Idempotency-Key", "normalized")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 202 {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&n)
	if n != 1 {
		t.Fatal("equivalent normalization duplicated task", n)
	}
}

// The hook runs after API inspection/core preparation, immediately before the
// real PostgreSQL admission transaction. It changes only disposable metadata.
type admissionReferenceRaceRepository struct {
	*TaskRepository
	before func()
}

func (r *admissionReferenceRaceRepository) CreateOrGet(ctx context.Context, task core.Task, key, hash string) (core.Task, bool, error) {
	if r.before != nil {
		f := r.before
		r.before = nil
		f()
	}
	return r.TaskRepository.CreateOrGet(ctx, task, key, hash)
}

func TestGenerationAdmissionHTTPReferenceCapabilities(t *testing.T) {
	db := auditDatabase(t, true)
	store, e := storage.NewLocal(t.TempDir(), http.DefaultClient, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	library := references.New(NewReferenceRepository(db), store)
	var pngBytes bytes.Buffer
	if e = png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 512, 512))); e != nil {
		t.Fatal(e)
	}
	owned := identity.WithOwnerID(context.Background(), identity.SystemOwnerID)
	upload := func(ctx context.Context) references.Image {
		t.Helper()
		ref, e := library.Upload(ctx, "synthetic.png", bytes.NewReader(pngBytes.Bytes()))
		if e != nil {
			t.Fatal(e)
		}
		return ref
	}
	ref := upload(owned)
	foreignOwner := uuid.NewString()
	foreign := upload(identity.WithOwnerID(context.Background(), foreignOwner))
	urlFor := func(owner, id string) string {
		t.Helper()
		u, e := references.CanonicalReferenceURL("https://gateway.example.test", []byte("signing-secret"), owner, id)
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	original := urlFor(identity.SystemOwnerID, ref.ID)
	foreignURL := urlFor(foreignOwner, foreign.ID)
	q := &quantityQueue{}
	provider := &admissionProvider{MediaProvider: mock.New(time.Millisecond, ""), enabled: true}
	repo := &admissionReferenceRaceRepository{TaskRepository: NewTaskRepository(db)}
	svc := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"mock": provider}, map[string]string{"mock": "mock-image-v1"}, "mock")
	h := api.NewGateway(api.Dependencies{Service: svc, Health: admissionHealth{}, APIKey: "synthetic-secret", Storage: store, References: library, SigningSecret: "signing-secret", MediaURLTTL: time.Hour, AssetPublicURL: "https://gateway.example.test"})
	send := func(raw, key string) *httptest.ResponseRecorder {
		t.Helper()
		data, e := json.Marshal(map[string]any{"model": "mock/mock-image-v1", "prompt": "Literal @Image9 stays unchanged", "image": raw})
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer synthetic-secret")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	counts := func() [4]int {
		t.Helper()
		var n [4]int
		for i, table := range []string{"tasks", "task_queue_outbox"} {
			if e := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n[i]); e != nil {
				t.Fatal(e)
			}
		}
		n[2] = q.enqueued
		n[3] = provider.submits
		return n
	}
	// Ordinary owned capability plus harmless query encoding remains usable.
	signatureStart := strings.Index(original, "signature=") + len("signature=")
	positive := original[:signatureStart] + fmt.Sprintf("%%%02X", original[signatureStart]) + original[signatureStart+1:] + "&download=1"
	w := send(positive, "owned")
	if w.Code != 202 {
		t.Fatal("owned positive", w.Code, w.Body.String())
	}
	var accepted struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &accepted)
	var prompt string
	var options core.MediaOptions
	var rawOptions []byte
	if e := db.QueryRow(`SELECT prompt,request_options FROM tasks WHERE id=$1`, accepted.ID).Scan(&prompt, &rawOptions); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(rawOptions, &options); e != nil {
		t.Fatal(e)
	}
	if prompt != "Literal @Image9 stays unchanged" || len(options.ReferenceSources) != 1 || options.ReferenceSources[0].ReferenceID != ref.ID || len(options.ImageURLs) != 1 || options.ImageURLs[0] != original {
		t.Fatal("literal prompt/canonical signed source changed", prompt, options)
	}
	before := counts()
	for _, tc := range []struct {
		name, raw  string
		readStatus int
	}{
		{"encoded-route", strings.Replace(original, "references", "refer%65nces", 1), 200},
		{"encoded-owner", strings.Replace(original, "/"+identity.SystemOwnerID, "/%30"+identity.SystemOwnerID[1:], 1), 200},
		{"encoded-id", strings.Replace(original, "/"+ref.ID, "/%"+fmt.Sprintf("%02X", ref.ID[0])+ref.ID[1:], 1), 200},
		{"foreign", foreignURL, 200},
		{"encoded-foreign", strings.Replace(foreignURL, "references", "refer%65nces", 1), 200},
		{"double-encoded-id", strings.Replace(original, "/"+ref.ID, "/%25"+fmt.Sprintf("%02X", ref.ID[0])+ref.ID[1:], 1), 403},
		{"encoded-separator", strings.Replace(original, "/"+ref.ID, "%2F"+ref.ID, 1), 404},
		{"encoded-dot-target", strings.Replace(original, "/"+ref.ID, "/%2e%2e%2f"+foreign.ID, 1), 403},
		{"duplicate-signature", original + "&signature=other", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// This is the actual net/http router and signed media handler, not a URL
			// classifier stub. It makes its decoding behavior explicit.
			read := httptest.NewRecorder()
			h.ServeHTTP(read, httptest.NewRequest("GET", tc.raw, nil))
			if read.Code != tc.readStatus {
				t.Fatal("router interpretation", read.Code, read.Body.String())
			}
			w := send(tc.raw, "reject-"+tc.name)
			if w.Code < 400 || counts() != before {
				t.Fatal("invalid capability reached admission", w.Code, w.Body.String(), counts(), before)
			}
		})
	}
	// External URL and optional textual marker retain their exact transport.
	external := "https://vendor.example/references/external/file?signature=a%2Bb&x=1"
	externalResponse := send(external, "external")
	if externalResponse.Code != 202 {
		t.Fatal("external contract", externalResponse.Code, externalResponse.Body.String())
	}
	var externalTask struct {
		ID string `json:"id"`
	}
	if e := json.Unmarshal(externalResponse.Body.Bytes(), &externalTask); e != nil {
		t.Fatal(e)
	}
	if e := db.QueryRow(`SELECT prompt,request_options FROM tasks WHERE id=$1`, externalTask.ID).Scan(&prompt, &rawOptions); e != nil {
		t.Fatal(e)
	}
	options = core.MediaOptions{}
	if e := json.Unmarshal(rawOptions, &options); e != nil {
		t.Fatal(e)
	}
	if prompt != "Literal @Image9 stays unchanged" || len(options.ReferenceSources) != 0 || len(options.ImageURLs) != 1 || options.ImageURLs[0] != external {
		t.Fatal("external URL or literal prompt rewritten", prompt, options)
	}

	// Metadata disappearance and revision change are separately tested through
	// the same HTTP path; no physical deletion/custody operation is invoked.
	for _, change := range []string{"revision", "deleted"} {
		t.Run(change, func(t *testing.T) {
			if _, e := db.Exec(`UPDATE reference_images SET deleting=false,content_sha256=$2 WHERE id=$1`, ref.ID, ref.SHA256); e != nil {
				t.Fatal(e)
			}
			baseline := counts()
			if change == "deleted" {
				if _, e := db.Exec(`UPDATE reference_images SET deleting=true WHERE id=$1`, ref.ID); e != nil {
					t.Fatal(e)
				}
				for i, raw := range []string{original, strings.Replace(original, "references", "refer%65nces", 1)} {
					if w := send(raw, fmt.Sprintf("deleted-%d", i)); w.Code < 400 {
						t.Fatal(w.Code, w.Body.String())
					}
				}
			} else {
				repo.before = func() {
					if _, e := db.Exec(`UPDATE reference_images SET content_sha256=repeat('b',64) WHERE id=$1`, ref.ID); e != nil {
						t.Fatal(e)
					}
				}
				if w := send(original, "revision-race"); w.Code < 400 {
					t.Fatal(w.Code, w.Body.String())
				}
				if repo.before != nil {
					t.Fatal("request did not reach real admission")
				}
			}
			if counts() != baseline {
				t.Fatal("reference rejection caused side effects", counts(), baseline)
			}
		})
	}
	if provider.submits != 0 {
		t.Fatal("provider called")
	}
}
