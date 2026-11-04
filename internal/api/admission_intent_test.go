package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
)

type admissionReceiptService struct {
	serviceStub
	intent     string
	recoveries int
}

func (s *admissionReceiptService) RecoverAcceptedIntent(_ context.Context, key, intent string) (core.Task, bool, error) {
	s.recoveries++
	return s.task, key == "accepted" && intent == s.intent, nil
}
func TestGenerationAdmissionQuotaDoesNotReadmitAcceptedReceipt(t *testing.T) {
	ctx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), "consumer"), identity.SharedOwnerID, identity.SharedOwnerID)
	intent, e := core.FingerprintAdmissionIntent(ctx, "json:/v1/videos?", map[string]any{"model": "mock/mock-video-v1", "prompt": "test"})
	if e != nil {
		t.Fatal(e)
	}
	service := &admissionReceiptService{serviceStub: serviceStub{task: core.Task{ID: "accepted-task", Status: core.TaskQueued}}, intent: intent}
	limit := int64(1)
	store := &quotaStorage{usage: storage.Usage{LimitBytes: &limit, UsedBytes: 2}}
	s := &Server{service: service, storageAdmin: store, system: &systemFixture{settings: system.Settings{SharedResources: true}, admins: map[string]bool{}}}
	calls := 0
	handler := s.systemPolicy(s.generationIntent(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(202) })))
	for _, unknown := range []bool{false, true} {
		if unknown {
			store.usage.UnknownObjects = 1
		}
		for _, key := range []string{"accepted", "new"} {
			r := httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{"model":"mock/mock-video-v1","prompt":"test"}`)).WithContext(ctx)
			r.Header.Set("Idempotency-Key", key)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			want := 403
			if unknown {
				want = 503
			}
			if key == "accepted" {
				want = 202
			}
			if w.Code != want || calls != 0 {
				t.Fatal(key, unknown, w.Code, w.Body.String(), calls)
			}
		}
	}
}
func TestGenerationAdmissionKeyedMultipartRetainsEarlyS3Rejection(t *testing.T) {
	repo := &admissionRepository{}
	svc := core.NewTaskServiceWithProviders(repo, &admissionQueue{}, map[string]core.MediaProvider{"mock": mock.New(time.Millisecond, "")}, map[string]string{"mock": "mock-video-v1"}, "mock")
	store := &directMedia{}
	library := references.New(&referenceMemory{items: map[string]references.Image{}}, store)
	h := NewWithReferenceLibrary(svc, healthStub{}, "secret", store, "signing-secret", time.Hour, nil, nil, library)
	r := httptest.NewRequest("POST", "/v1/images/edits", unreadBody{})
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("Idempotency-Key", "new-upload")
	r.Header.Set("Content-Type", "multipart/form-data; boundary=fixture")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code < 400 || !strings.Contains(w.Body.String(), "direct_upload_required") || repo.creates != 0 {
		t.Fatal(w.Code, w.Body.String(), repo.creates)
	}
}

func TestGenerationAdmissionDeferredQuotaCoversPassThroughEncodings(t *testing.T) {
	limit := int64(1)
	store := &quotaStorage{usage: storage.Usage{LimitBytes: &limit, UsedBytes: 2}}
	s := &Server{service: &admissionReceiptService{}, storageAdmin: store, system: &systemFixture{settings: system.Settings{SharedResources: true}, admins: map[string]bool{}}}
	calls := 0
	handler := s.systemPolicy(s.generationIntent(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(202) })))
	ctx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), "consumer"), identity.SharedOwnerID, identity.SharedOwnerID)
	for _, kind := range []string{"multipart/mixed", "multipart/form-data; boundary=fixture", "application/json"} {
		for _, used := range []int64{0, 2} {
			store.usage.UsedBytes = used
			before := calls
			r := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{"model":"mock/mock-image-v1","prompt":"test"}`)).WithContext(ctx)
			r.Header.Set("Content-Type", kind)
			r.Header.Set("Idempotency-Key", "new")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			want, delta := 202, 1
			if used > limit {
				want, delta = 403, 0
			}
			if w.Code != want || calls != before+delta {
				t.Fatal(kind, used, w.Code, calls-before, w.Body.String())
			}
		}
	}
	// Accepted recovery uses the supported JSON intent branch; unsupported
	// multipart pass-through has no receipt fingerprint and must enforce quota.
	service := s.service.(*admissionReceiptService)
	service.task = core.Task{ID: "accepted-image", Status: core.TaskQueued}
	intent, e := core.FingerprintAdmissionIntent(ctx, "json:/v1/images/generations?", map[string]any{"model": "mock/mock-image-v1", "prompt": "test"})
	if e != nil {
		t.Fatal(e)
	}
	service.intent = intent
	before := calls
	r := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{"model":"mock/mock-image-v1","prompt":"test"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "accepted")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 202 || calls != before || !strings.Contains(w.Body.String(), "accepted-image") {
		t.Fatal("accepted retry was re-admitted", w.Code, calls-before, w.Body.String())
	}

}

type admissionChangedService struct{ serviceStub }

func (admissionChangedService) CreateMediaRequest(context.Context, core.CreateMediaInput, string) (core.Task, error) {
	return core.Task{}, core.ErrProviderChanged
}
func TestGenerationAdmissionChangedProviderIsConflict(t *testing.T) {
	h := New(admissionChangedService{}, healthStub{}, "secret")
	w := openAIRequest(h, "/v1/videos", `{"model":"mock/mock-video-v1","prompt":"fixture"}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "provider_changed") {
		t.Fatal(w.Code, w.Body.String())
	}
	s := &Server{}
	w = httptest.NewRecorder()
	s.resourceAdmissionError(w, core.ErrProviderChanged)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "provider_changed") {
		t.Fatal(w.Code, w.Body.String())
	}
}
