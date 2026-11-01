package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providers"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

type serviceStub struct{ task core.Task }

func (s serviceStub) ValidateMediaRequest(context.Context, core.CreateMediaInput) error { return nil }

func (s serviceStub) CreateMediaRequest(context.Context, core.CreateMediaInput, string) (core.Task, error) {
	return s.task, nil
}
func (s serviceStub) GetTask(context.Context, string) (core.Task, error) { return s.task, nil }
func (s serviceStub) ListTasks(context.Context, int) ([]core.Task, error) {
	return []core.Task{s.task}, nil
}
func (s serviceStub) Info() core.ServiceInfo {
	return core.ServiceInfo{Provider: "mock", Model: "mock-video-v1", Live: false}
}
func (s serviceStub) ListProviders(context.Context) []core.ProviderDescriptor {
	return []core.ProviderDescriptor{{ID: "mock", Name: "Mock", DefaultModel: "mock-video-v1", Models: []core.MediaModel{{ID: "mock-video-v1", MediaType: "video"}, {ID: "mock-image-v1", MediaType: "image"}}}}
}

type healthStub struct{ err error }

func (h healthStub) Check(context.Context) error { return h.err }

type providerAdminStub struct {
	provider providers.AdminProvider
	update   providers.ProviderUpdate
}

func (s *providerAdminStub) List(context.Context) ([]providers.AdminProvider, error) {
	return []providers.AdminProvider{s.provider}, nil
}
func (s *providerAdminStub) Get(context.Context, string) (providers.AdminProvider, error) {
	return s.provider, nil
}
func (s *providerAdminStub) Check(context.Context, string) (providers.AdminProvider, error) {
	return s.provider, nil
}
func (s *providerAdminStub) Update(_ context.Context, _ string, update providers.ProviderUpdate) (providers.AdminProvider, error) {
	s.update = update
	return s.provider, nil
}

func TestCreateVideoRequiresAPIKey(t *testing.T) {
	handler := New(serviceStub{}, healthStub{}, "secret")
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d", response.Code)
	}
}

func TestCreateAndGetVideoTask(t *testing.T) {
	task := core.Task{ID: "24e1c949-9161-4c83-a7ac-fdb83eddb44b", Status: core.TaskQueued, Provider: "mock", Model: "mock-video-v1", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	handler := New(serviceStub{task: task}, healthStub{}, "secret")

	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"mock/mock-video-v1","prompt":"sunrise"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("got status %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/generations/"+task.ID, nil)
	request.Header.Set("Authorization", "Bearer "+"secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got status %d: %s", response.Code, response.Body.String())
	}
}

func TestCreateMediaTask(t *testing.T) {
	task := core.Task{ID: "94e1c949-9161-4c83-a7ac-fdb83eddb44b", Status: core.TaskQueued, Provider: "mock", Model: "mock-image-v1", MediaType: "image", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	handler := New(serviceStub{task: task}, healthStub{}, "secret")
	request := httptest.NewRequest(http.MethodPost, "/v1/generations", strings.NewReader(`{"prompt":"sunrise","model":"mock/mock-image-v1","options":{},"size":"1024x1024"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"media_type":"image"`) {
		t.Fatalf("got status %d: %s", response.Code, response.Body.String())
	}
}

func TestHealthIsPublic(t *testing.T) {
	handler := New(serviceStub{}, healthStub{}, "secret")
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got status %d", response.Code)
	}
}

func TestInfoIsPublic(t *testing.T) {
	handler := New(serviceStub{}, healthStub{}, "secret")
	request := httptest.NewRequest(http.MethodGet, "/v1/info", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got status %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"provider":"mock"`) {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
}

func TestBackendDoesNotServeFrontend(t *testing.T) {
	h := New(serviceStub{}, healthStub{}, "secret")
	for _, path := range []string{"/", "/system", "/organizations", "/profile", "/docs", "/terms", "/privacy", "/branding.css", "/ui/index.html", "/ui/app.js", "/ui/styles.css"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestListTasksRequiresAPIKeyAndReturnsData(t *testing.T) {
	task := core.Task{ID: "24e1c949-9161-4c83-a7ac-fdb83eddb44b", Status: core.TaskQueued}
	handler := New(serviceStub{task: task}, healthStub{}, "secret")

	request := httptest.NewRequest(http.MethodGet, "/v1/generations", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d without API key", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/generations?limit=10", nil)
	request.Header.Set("Authorization", "Bearer "+"secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got status %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), task.ID) {
		t.Fatal("task was not returned")
	}
}

func TestListProviders(t *testing.T) {
	handler := New(serviceStub{}, healthStub{}, "secret")
	request := httptest.NewRequest(http.MethodGet, "/v1/providers", nil)
	request.Header.Set("Authorization", "Bearer "+"secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got status %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "mock-video-v1") {
		t.Fatalf("provider model was not returned: %s", response.Body.String())
	}
}

func TestProviderAdministrationAPI(t *testing.T) {
	admin := &providerAdminStub{provider: providers.AdminProvider{ID: "alibaba", Name: "Alibaba Cloud Model Studio", Configured: true}}
	handler := NewWithProviderAdmin(serviceStub{}, healthStub{}, "secret", nil, "", time.Hour, admin)

	request := httptest.NewRequest(http.MethodGet, "/v1/admin/providers", nil)
	request.Header.Set("Authorization", "Bearer "+"secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "alibaba") {
		t.Fatalf("list status %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/v1/admin/providers/alibaba", strings.NewReader(`{"enabled":false,"api_key":"new-key"}`))
	request.Header.Set("Authorization", "Bearer "+"secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || admin.update.Enabled == nil || *admin.update.Enabled || admin.update.APIKey == nil || *admin.update.APIKey != "new-key" {
		t.Fatalf("update status %d, payload %#v: %s", response.Code, admin.update, response.Body.String())
	}
}

func TestStoredMediaAccessIsSignedAndSupportsRanges(t *testing.T) {
	mediaStorage, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := mediaStorage.Archive(context.Background(), "24e1c949-9161-4c83-a7ac-fdb83eddb44b", "data:video/mp4;base64,c21hbGwtdmlkZW8=")
	if err != nil {
		t.Fatal(err)
	}
	resultURL := "/v1/generations/24e1c949-9161-4c83-a7ac-fdb83eddb44b/content"
	task := core.Task{
		ID: "24e1c949-9161-4c83-a7ac-fdb83eddb44b", Status: core.TaskSucceeded,
		ResultURL: &resultURL, StorageKey: &stored.StorageKey,
	}
	handler := NewWithStorage(serviceStub{task: task}, healthStub{}, "secret", mediaStorage, "media-secret", time.Hour)

	request := httptest.NewRequest(http.MethodGet, "/v1/generations/"+task.ID+"/media-access", nil)
	request.Header.Set("Authorization", "Bearer "+"secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("access status %d: %s", response.Code, response.Body.String())
	}
	var access struct {
		PreviewURL string `json:"preview_url"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &access); err != nil || access.PreviewURL == "" {
		t.Fatalf("decode access URL: %#v, %v", access, err)
	}

	request = httptest.NewRequest(http.MethodGet, access.PreviewURL, nil)
	request.Header.Set("Range", "bytes=0-4")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent {
		t.Fatalf("range status %d: %s", response.Code, response.Body.String())
	}
	body, _ := io.ReadAll(response.Result().Body)
	if string(body) != "small" {
		t.Fatalf("unexpected range body %q", body)
	}

	request = httptest.NewRequest(http.MethodGet, "/media/"+task.ID+"?expires=1&signature=invalid", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unsigned media status %d", response.Code)
	}
}
