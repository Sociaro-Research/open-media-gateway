package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
)

type directMedia struct {
	*storage.Local
	owner, key, method, disposition string
	destination                     string
	fail                            bool
}

func (s *directMedia) DirectURL(ctx context.Context, key, method, kind, disposition string, ttl time.Duration) (string, error) {
	s.owner = identity.OwnerID(ctx)
	s.key = key
	s.method = method
	s.disposition = disposition
	if s.fail {
		return "", errors.New("signing failed")
	}
	if s.destination != "" {
		return s.destination, nil
	}
	return "https://storage.example/direct?signature=storage", nil
}

func TestDirectMediaRejectsConsoleHostname(t *testing.T) {
	key := "s3:main/active.svg"
	store := &directMedia{destination: "https://EXAMPLE.com:9443/object.svg"}
	task := core.Task{ID: uuid.NewString(), OwnerID: uuid.NewString(), Status: core.TaskSucceeded, StorageKey: &key}
	handler := NewWithStorage(serviceStub{task: task}, healthStub{}, "secret", store, "signing-secret", time.Hour)
	signed, _ := newMediaURLSigner("signing-secret", time.Hour).create(task.ID)
	r := httptest.NewRequest("GET", "https://example.com"+signed, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get("Location") != "" {
		t.Fatal("same-host active storage was served", w.Code, w.Header())
	}
	owner, id := identity.SystemOwnerID, uuid.NewString()
	repo := &referenceMemory{items: map[string]references.Image{owner + "/" + id: {ID: id, StorageKey: key, ContentType: "image/svg+xml"}}}
	handler = NewWithReferenceLibrary(serviceStub{}, healthStub{}, "secret", store, "signing-secret", time.Hour, nil, nil, references.New(repo, store))
	path := "https://example.com/references/" + owner + "/" + id + "?signature=" + references.Signature([]byte("signing-secret"), owner, id)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 503 || w.Header().Get("Location") != "" {
		t.Fatal("same-host reference was served", w.Code, w.Header())
	}
}
func (*directMedia) Open(context.Context, string) (storage.Object, error) {
	panic("S3 object was opened by gateway")
}
func (*directMedia) PlanUpload(context.Context, string, string, string, int64) (*storage.PendingUpload, error) {
	return &storage.PendingUpload{}, nil
}
func (*directMedia) StartUpload(context.Context, *storage.PendingUpload) (storage.UploadTarget, error) {
	panic("unexpected upload")
}
func (*directMedia) CompleteUpload(context.Context, storage.PendingUpload) (core.StoredMedia, error) {
	panic("unexpected completion")
}
func (*directMedia) AbortUpload(context.Context, storage.PendingUpload, bool) error {
	panic("unexpected cleanup")
}

type unreadBody struct{}

func (unreadBody) Read([]byte) (int, error) { panic("S3 multipart body was parsed or spooled") }
func TestS3ReferenceRedirectAndEarlyUploadRejection(t *testing.T) {
	owner := identity.SystemOwnerID
	id := uuid.NewString()
	store := &directMedia{}
	repo := &referenceMemory{items: map[string]references.Image{owner + "/" + id: {ID: id, StorageKey: "s3:fixture/file.png", ContentType: "image/png"}}}
	library := references.New(repo, store)
	handler := NewWithReferenceLibrary(serviceStub{}, healthStub{}, "secret", store, "signing-secret", time.Hour, nil, nil, library)
	signed := "/references/" + owner + "/" + id + "?signature=" + references.Signature([]byte("signing-secret"), owner, id)
	for _, method := range []string{"GET", "HEAD"} {
		w := sendTest(handler, method, signed, "", "", "")
		if w.Code != 307 || w.Header().Get("Location") == "" || store.owner != owner || store.method != method {
			t.Fatalf("bad redirect: %d %v", w.Code, w.Header())
		}
	}
	store.fail = true
	if w := sendTest(handler, "GET", signed, "", "", ""); w.Code != 503 {
		t.Fatal("signing failure should not proxy", w.Code)
	}
	store.fail = false
	for _, endpoint := range []string{"/v1/references", "/v1/images/edits", "/v1/videos"} {
		r := httptest.NewRequest("POST", endpoint, unreadBody{})
		r.Header.Set("Content-Type", "multipart/form-data; boundary=fixture")
		r.Header.Set("Authorization", "Bearer "+"secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "direct_upload_required") {
			t.Fatalf("%s: %d %s", endpoint, w.Code, w.Body)
		}
	}
}
func TestS3ResultRedirectsPreserveOwnerAndArtifact(t *testing.T) {
	key, frame := "s3:main/video.mp4", "s3:frame/image.png"
	task := core.Task{ID: uuid.NewString(), OwnerID: uuid.NewString(), Status: core.TaskSucceeded, StorageKey: &key, LastFrameStorageKey: &frame}
	store := &directMedia{}
	h := NewWithStorage(serviceStub{task: task}, healthStub{}, "secret", store, "signing-secret", time.Hour)
	signed, _ := newMediaURLSigner("signing-secret", time.Hour).create(task.ID)
	for _, tc := range []struct{ method, suffix, key string }{{"GET", "&download=1", key}, {"HEAD", "&artifact=last-frame", frame}} {
		r := httptest.NewRequest(tc.method, signed+tc.suffix, nil)
		r.Header.Set("Range", "bytes=2-5")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 307 || store.owner != task.OwnerID || store.key != tc.key || store.method != tc.method {
			t.Fatal("wrong direct artifact", w.Code, store)
		}
	}
	if w := sendTest(h, "GET", "/media/"+task.ID, "", "", ""); w.Code != 403 {
		t.Fatal("unsigned result accessible")
	}
}
