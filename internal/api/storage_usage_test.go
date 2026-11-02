package api

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type quotaStorage struct {
	storageAdminStub
	usage storage.Usage
	reads int
}

func (q *quotaStorage) SharedUsage(context.Context) (storage.Usage, error) {
	q.reads++
	return q.usage, nil
}
func TestStorageQuotaAdmissionBoundary(t *testing.T) {
	limit := int64(10)
	store := &quotaStorage{usage: storage.Usage{LimitBytes: &limit}}
	s := &Server{storageAdmin: store, system: &systemFixture{settings: system.Settings{SharedResources: true}, admins: map[string]bool{}}}
	for _, tc := range []struct {
		used   int64
		shared bool
		status int
	}{{9, true, 200}, {10, true, 200}, {11, true, 403}, {11, false, 200}} {
		store.usage.UsedBytes = tc.used
		s.system.(*systemFixture).settings = system.Settings{SharedResources: tc.shared, PersonalProviders: !tc.shared}
		s.system.(*systemFixture).admins["consumer"] = !tc.shared
		for _, path := range []string{"/v1/generations", "/v1/videos", "/v1/generations/estimate", "/v1/references", "/v1/references/uploads"} {
			ctx := identity.WithOwnerID(context.Background(), "consumer")
			if tc.shared {
				ctx = identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
			}
			r := httptest.NewRequest("POST", path, strings.NewReader("{}")).WithContext(ctx)
			w := httptest.NewRecorder()
			called := false
			reads := store.reads
			s.systemPolicy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(200) })).ServeHTTP(w, r)
			if called != (tc.status == 200) || tc.shared && store.reads != reads+1 || !tc.shared && store.reads != reads {
				t.Fatal("wrong quota path or downstream admission", tc, called, store.reads-reads)
			}
			if w.Code != tc.status {
				t.Fatalf("%s %+v: %d %s", path, tc, w.Code, w.Body)
			}
		}
	}
	// Company mode must reject personal admission even for administrators,
	// independently of shared quota state and before downstream work.
	s.system.(*systemFixture).settings = system.Settings{SharedResources: true, PersonalProviders: false}
	for _, admin := range []bool{false, true} {
		s.system.(*systemFixture).admins["consumer"] = admin
		called := false
		w := httptest.NewRecorder()
		s.systemPolicy(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, httptest.NewRequest("POST", "/v1/generations", nil).WithContext(identity.WithOwnerID(context.Background(), "consumer")))
		if w.Code != 403 || called || !strings.Contains(w.Body.String(), "personal_providers_disabled") {
			t.Fatal("personal admission bypassed company policy", admin, w.Code, called)
		}
	}
	// Finishing an admitted direct upload (including a lost-response retry)
	// must stay available after another upload crosses the storage threshold.
	store.usage.UsedBytes = 11
	finishCtx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), "consumer"), identity.SharedOwnerID, identity.SharedOwnerID)
	finish := httptest.NewRecorder()
	s.systemPolicy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(finish, httptest.NewRequest("POST", "/v1/references/uploads/test/complete", nil).WithContext(finishCtx))
	if finish.Code != 200 {
		t.Fatal("quota blocked an admitted upload completion", finish.Code)
	}
	// Existing history and deletion stay available over quota.
	ctx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), "consumer"), identity.SharedOwnerID, identity.SharedOwnerID)
	for _, method := range []string{"GET", "DELETE"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/v1/generations/task/content", nil).WithContext(ctx)
		s.systemPolicy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("quota blocked cleanup")
		}
	}
}

func TestDeletedResultProviderLinkVisibility(t *testing.T) {
	source := "https://provider.example/result"
	task := core.Task{ProviderResultURL: &source, Prompt: "Keep prompt"}
	if core.GenerationView(task).ProviderResultURL != nil {
		t.Fatal("provider link exposed before deletion")
	}
	now := time.Now()
	task.ResultDeletedAt = &now
	if core.GenerationView(task).ProviderResultURL != nil {
		t.Fatal("provider URL leaked after deletion")
	}
	for _, invalid := range []string{"data:video/mp4;base64,secret", "javascript:alert(1)", "/private"} {
		task.ProviderResultURL = &invalid
		if core.GenerationView(task).ProviderResultURL != nil {
			t.Fatal("unsafe fallback link")
		}
	}
}
