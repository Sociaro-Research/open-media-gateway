package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

type storageAdminStub struct {
	owner    string
	update   storage.Update
	checks   int
	checkErr error
}

func (a *storageAdminStub) Get(ctx context.Context) (storage.Settings, error) {
	a.owner = identity.OwnerID(ctx)
	return storage.Settings{Driver: "local"}, nil
}
func (a *storageAdminStub) Update(ctx context.Context, in storage.Update) (storage.Settings, error) {
	a.owner = identity.OwnerID(ctx)
	a.update = in
	return storage.Settings{Driver: in.Driver}, nil
}
func (a *storageAdminStub) Check(ctx context.Context, in storage.Update) error {
	a.owner = identity.OwnerID(ctx)
	a.update = in
	a.checks++
	return a.checkErr
}
func TestStorageAdminScopesAndOwner(t *testing.T) {
	s, _ := newPersonalTestServer()
	admin := &storageAdminStub{}
	s.storageAdmin = admin
	h := s.handler()
	_, basic := issueTestKey(t, h, "alice", `{"name":"Basic"}`)
	_, manager := issueTestKey(t, h, "alice", `{"name":"Storage","scopes":["generation","storage:manage"]}`)
	for _, method := range []string{"GET", "PUT", "POST"} {
		url := "/v1/admin/storage"
		body := ""
		if method != "GET" {
			body = `{"driver":"local"}`
		}
		if method == "POST" {
			url += "/check"
		}
		if w := sendTest(h, method, url, body, "", ""); w.Code != 401 {
			t.Fatal(w.Code)
		}
		if w := sendTest(h, method, url, body, "", basic); w.Code != 403 {
			t.Fatalf("generation key managed storage: %d", w.Code)
		}
		if w := sendTest(h, method, url, body, "", manager); w.Code != 200 || admin.owner != "alice" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("storage scope failed: %d %s", w.Code, w.Body)
		}
	}
	if w := sendTest(h, "GET", "/v1/admin/storage", "", "bob", ""); w.Code != 200 || admin.owner != "bob" {
		t.Fatal("session storage owner lost")
	}
	if w := sendTest(h, "GET", "/v1/admin/storage", "", "", "service-secret"); w.Code != 200 || admin.owner != identity.SystemOwnerID {
		t.Fatal("system storage owner lost")
	}
	for _, body := range []string{`{"driver":"local","unknown":true}`, `{"driver":"local"} {}`, `[]`, strings.Repeat("x", 17000)} {
		if w := sendTest(h, "PUT", "/v1/admin/storage", body, "alice", ""); w.Code != 400 {
			t.Fatalf("accepted invalid body: %d", w.Code)
		}
	}
}

func TestStorageCheckUsesBrowserOriginFromRequest(t *testing.T) {
	s, _ := newPersonalTestServer()
	admin := &storageAdminStub{}
	s.storageAdmin = admin
	h := s.handler()
	for _, method := range []string{"POST", "PUT"} {
		endpoint := "/v1/admin/storage"
		if method == "POST" {
			endpoint += "/check"
		}
		r := httptest.NewRequest(method, "https://example.com"+endpoint, strings.NewReader(`{"driver":"local"}`))
		r.Header.Set("Authorization", "Bearer service-secret")
		r.Header.Set("Origin", "https://example.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || admin.update.BrowserOrigin != "https://example.com" {
			t.Fatalf("browser check omitted origin: %d %s", w.Code, w.Body)
		}
	}
	if w := sendTest(h, "POST", "/v1/admin/storage/check", `{"driver":"local","BrowserOrigin":"https://forged.test"}`, "", "service-secret"); w.Code != 400 {
		t.Fatal("JSON must not override the browser origin", w.Code)
	}
}

type memoryMedia struct{ owner, key string }

func (m *memoryMedia) Archive(context.Context, string, string) (core.StoredMedia, error) {
	return core.StoredMedia{}, errors.New("unused")
}
func (m *memoryMedia) Open(ctx context.Context, key string) (storage.Object, error) {
	m.owner = identity.OwnerID(ctx)
	m.key = key
	return storage.Object{Key: "frame.png", ContentType: "image/png", SizeBytes: 10, Reader: seekCloser{strings.NewReader("0123456789")}}, nil
}

type seekCloser struct{ *strings.Reader }

func (seekCloser) Close() error { return nil }
func TestSignedLastFrameUsesTaskOwnerAndRange(t *testing.T) {
	key, frame, videoType, hash := "s3:main/video.mp4", "s3:frame/image.png", "video/mp4", "main-video-hash"
	task := core.Task{ID: "24e1c949-9161-4c83-a7ac-fdb83eddb44b", OwnerID: "alice", Status: core.TaskSucceeded, StorageKey: &key, LastFrameStorageKey: &frame, ResultType: &videoType, ResultSHA256: &hash}
	media := &memoryMedia{}
	h := NewWithStorage(serviceStub{task: task}, healthStub{}, "secret", media, "signing-secret", time.Hour)
	access := sendTest(h, "GET", "/v1/generations/"+task.ID+"/media-access", "", "", "secret")
	if !strings.Contains(access.Body.String(), "last_frame_url") {
		t.Fatal(access.Body.String())
	}
	signer := newMediaURLSigner("signing-secret", time.Hour)
	url, _ := signer.create(task.ID)
	r := httptest.NewRequest("GET", url+"&artifact=last-frame", nil)
	r.Header.Set("Range", "bytes=2-5")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "2345" || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("ETag") != "" {
		t.Fatalf("bad last frame response: %d %s %v", w.Code, w.Body, w.Header())
	}
	if media.owner != "alice" || media.key != frame {
		t.Fatal("signed request did not use task owner/frame")
	}
	w = sendTest(h, "HEAD", "/v1/generations/"+task.ID+"/last-frame", "", "", "secret")
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("invalid HEAD")
	}
	w = sendTest(h, "GET", "/media/"+task.ID+"?artifact=last-frame", "", "", "")
	if w.Code != http.StatusForbidden {
		t.Fatal("unsigned frame accessible")
	}
}

func TestStorageUsesConfiguredFrontendOrigin(t *testing.T) {
	s, _ := newPersonalTestServer()
	admin := &storageAdminStub{}
	s.storageAdmin = admin
	h, err := WithPublicAPI(s.handler(), "https://api.example.test", "https://ui.example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"PUT", "POST"} {
		endpoint := "/v1/admin/storage"
		if method == "POST" {
			endpoint += "/check"
		}
		for _, origin := range []string{"", "https://api.example.test"} {
			r := httptest.NewRequest(method, "https://api.example.test"+endpoint, strings.NewReader(`{"driver":"local"}`))
			r.Header.Set("Authorization", "Bearer service-secret")
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 || admin.update.BrowserOrigin != "https://ui.example.test" {
				t.Fatalf("configured origin lost: %d %s %q", w.Code, w.Body, admin.update.BrowserOrigin)
			}
		}
	}
	admin.update = storage.Update{}
	r := httptest.NewRequest("PUT", "https://untrusted.example.test/v1/admin/storage", strings.NewReader(`{"driver":"local"}`))
	r.Header.Set("Authorization", "Bearer service-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 421 || admin.update.BrowserOrigin != "" {
		t.Fatal("untrusted host reached storage", w.Code)
	}
}

func TestStorageCheckSignalsAutomaticBrowserSetup(t *testing.T) {
	s, _ := newPersonalTestServer()
	admin := &storageAdminStub{checkErr: storage.ErrBrowserCORS}
	s.storageAdmin = admin
	w := sendTest(s.handler(), "POST", "/v1/admin/storage/check", `{"driver":"s3"}`, "", "service-secret")
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"storage_browser_setup_required"`) {
		t.Fatal(w.Code, w.Body)
	}
}
