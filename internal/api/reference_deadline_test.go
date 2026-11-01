package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
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

type delayedReferenceStore struct {
	data            []byte
	requireDeadline bool
}

func (s *delayedReferenceStore) Open(ctx context.Context, _ string) (storage.Object, error) {
	if s.requireDeadline {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 151*time.Second {
			return storage.Object{}, errors.New("missing bounded inspection context")
		}
	}
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

func TestStoredReferenceValidationSurvivesOrdinaryWriteDeadline(t *testing.T) {
	s, _, _, _ := templateFixture(t)
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"Slow validation"}`)
	data := testmedia.MP4(240000, 24000, 240)
	id := uuid.NewString()
	repo := &referenceMemory{items: map[string]references.Image{identity.PersonalOwnerID("alice") + "/" + id: {ID: id, Name: "slow.mp4", ContentType: "video/mp4", StorageKey: "fixture", SizeBytes: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}}}
	s.references = references.New(repo, &delayedReferenceStore{data: data, requireDeadline: true})
	server := httptest.NewUnstartedServer(h)
	server.Config = NewHTTPServer("", h)
	server.Config.WriteTimeout = 50 * time.Millisecond
	server.Start()
	defer server.Close()
	body, _ := json.Marshal(map[string]any{"reference_ids": []string{id}})
	req, _ := http.NewRequest("POST", server.URL+"/v1/references/validate", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal("inspection lost its response after ordinary deadline", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode, string(raw))
	}
}

type inspectionDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *inspectionDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func TestTemplateInspectionRoutesExtendWriteBudget(t *testing.T) {
	s, _, _, _ := templateFixture(t)
	for _, handler := range []http.HandlerFunc{s.templateValidateInputs, s.templateValidateMedia} {
		w := &inspectionDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		handler(w, httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{}`))))
		if time.Until(w.deadline) < 2*time.Minute {
			t.Fatal("stored-media inspection has ordinary write budget", w.deadline)
		}
	}
}

func TestReferenceInspectionBudgetCancelsOnReturnAndPreservesEarlierDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w := httptest.NewRecorder()
	request, finish := referenceInspectionRequest(w, httptest.NewRequest("POST", "/", nil).WithContext(parent))
	deadline, ok := request.Context().Deadline()
	if !ok || time.Until(deadline) > time.Second {
		t.Fatal("earlier caller deadline lost")
	}
	finish()
	if !errors.Is(request.Context().Err(), context.Canceled) {
		t.Fatal("inspection work context survived handler return")
	}
}
