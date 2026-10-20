package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
)

type delayedReferenceReader struct {
	*bytes.Reader
	delayed bool
}

func (r *delayedReferenceReader) Read(p []byte) (int, error) {
	if !r.delayed {
		r.delayed = true
		time.Sleep(150 * time.Millisecond)
	}
	return r.Reader.Read(p)
}
func (*delayedReferenceReader) Close() error { return nil }

type delayedReferenceStore struct{ data []byte }

func (s *delayedReferenceStore) Open(context.Context, string) (storage.Object, error) {
	return storage.Object{Reader: &delayedReferenceReader{Reader: bytes.NewReader(s.data)}, ContentType: "video/mp4", SizeBytes: int64(len(s.data))}, nil
}
func (*delayedReferenceStore) StoreReference(context.Context, string, []byte, string) (core.StoredMedia, error) {
	return core.StoredMedia{}, errors.New("unexpected upload")
}
func (*delayedReferenceStore) DeleteReference(context.Context, string) error {
	return errors.New("unexpected deletion")
}

func TestLocalReferenceSurvivesOrdinaryWriteDeadline(t *testing.T) {
	data := bytes.Repeat([]byte("synthetic-reference-media"), 4096)
	owner, id := identity.SystemOwnerID, uuid.NewString()
	repo := &referenceMemory{items: map[string]references.Image{owner + "/" + id: {ID: id, ContentType: "video/mp4", StorageKey: "fixture"}}}
	library := references.New(repo, &delayedReferenceStore{data: data})
	media, err := storage.NewLocal(t.TempDir(), http.DefaultClient, references.MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewWithReferenceLibrary(serviceStub{}, healthStub{}, "synthetic-key", media, "synthetic-signing-key", time.Hour, nil, nil, library)
	server := httptest.NewUnstartedServer(handler)
	server.Config = NewHTTPServer("", handler)
	server.Config.WriteTimeout = 50 * time.Millisecond
	server.Start()
	defer server.Close()
	signed := "/references/" + owner + "/" + id + "?signature=" + references.Signature([]byte("synthetic-signing-key"), owner, id)
	for _, ranged := range []bool{false, true} {
		t.Run(map[bool]string{false: "full", true: "range"}[ranged], func(t *testing.T) {
			request, _ := http.NewRequest(http.MethodGet, server.URL+signed, nil)
			want := data
			status := http.StatusOK
			if ranged {
				request.Header.Set("Range", "bytes=100-199")
				want = data[100:200]
				status = http.StatusPartialContent
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal("ordinary JSON deadline interrupted a valid local reference", err)
			}
			defer response.Body.Close()
			got, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != status || !bytes.Equal(got, want) {
				t.Fatalf("incomplete local reference: status=%d bytes=%d err=%v", response.StatusCode, len(got), err)
			}
			if response.Header.Get("X-Content-Type-Options") != "nosniff" || response.Header.Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("reference security headers lost")
			}
		})
	}
}
