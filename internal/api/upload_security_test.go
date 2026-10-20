package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/references"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
)

type blockedUploadBody struct {
	io.ReadCloser
	started, release chan struct{}
	once             bool
}

func (b *blockedUploadBody) Read(p []byte) (int, error) {
	if !b.once {
		b.once = true
		close(b.started)
		<-b.release
	}
	return b.ReadCloser.Read(p)
}

type unreadUploadBody struct{ read bool }

func (b *unreadUploadBody) Read([]byte) (int, error) { b.read = true; return 0, io.EOF }
func (*unreadUploadBody) Close() error               { return nil }

func TestReferenceAdmissionPrecedesMultipartBodyConsumption(t *testing.T) {
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 30<<20)
	if err != nil {
		t.Fatal(err)
	}
	library := references.New(&referenceMemory{items: map[string]references.Image{}}, store)
	handler := NewWithReferenceLibrary(serviceStub{}, healthStub{}, "secret", store, "signing-secret", time.Hour, nil, nil, library)
	first := referenceUploadRequest(t, referencePNG(t))
	gate := &blockedUploadBody{ReadCloser: first.Body, started: make(chan struct{}), release: make(chan struct{})}
	first.Body = gate
	var released sync.Once
	release := func() { released.Do(func() { close(gate.release) }) }
	t.Cleanup(release)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { w := httptest.NewRecorder(); handler.ServeHTTP(w, first); done <- w }()
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first upload never started consuming the admitted body")
	}
	second := referenceUploadRequest(t, referencePNG(t))
	probe := &unreadUploadBody{}
	second.Body = probe
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, second)
	release()
	firstResponse := <-done
	if firstResponse.Code != 201 {
		t.Fatal(firstResponse.Code, firstResponse.Body.String())
	}
	if w.Code != 429 || probe.read || w.Header().Get("Retry-After") == "" {
		t.Fatalf("upload was consumed before admission: status=%d read=%v", w.Code, probe.read)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, referenceUploadRequest(t, referencePNG(t)))
	if w.Code != 200 {
		t.Fatal("upload slot was not released", w.Code, w.Body.String())
	}
}

func TestGenerationMultipartSharesEarlyReferenceAdmission(t *testing.T) {
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 30<<20)
	if err != nil {
		t.Fatal(err)
	}
	library := references.New(&referenceMemory{items: map[string]references.Image{}}, store)
	handler := NewWithReferenceLibrary(serviceStub{}, healthStub{}, "secret", store, "signing-secret", time.Hour, nil, nil, library)
	for _, route := range []string{"/v1/images/edits", "/v1/videos"} {
		t.Run(route, func(t *testing.T) {
			release, err := library.AdmitMultipart()
			if err != nil {
				t.Fatal(err)
			}
			probe := &unreadUploadBody{}
			r := httptest.NewRequest("POST", route, probe)
			r.Header.Set("Content-Type", "multipart/form-data; boundary=fixture")
			r.Header.Set("Authorization", "Bearer secret")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			release()
			if w.Code != 429 || probe.read {
				t.Fatalf("multipart route consumed a busy body: status=%d read=%v", w.Code, probe.read)
			}
		})
	}
}
