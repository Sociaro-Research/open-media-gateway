package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/references"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/google/uuid"
)

type referenceMemory struct {
	items      map[string]references.Image
	failInsert bool
}

func (m *referenceMemory) List(ctx context.Context, limit, offset int) ([]references.Image, error) {
	items := []references.Image{}
	for key, item := range m.items {
		if strings.HasPrefix(key, identity.OwnerID(ctx)+"/") {
			items = append(items, item)
		}
	}
	if offset >= len(items) {
		return []references.Image{}, nil
	}
	items = items[offset:]
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
func (m *referenceMemory) Get(ctx context.Context, id string) (references.Image, error) {
	item, ok := m.items[identity.OwnerID(ctx)+"/"+id]
	if !ok {
		return item, storage.ErrObjectNotFound
	}
	return item, nil
}
func (m *referenceMemory) Insert(ctx context.Context, item references.Image) error {
	if item.SHA256 != "" {
		if _, err := m.FindByHash(ctx, item.SHA256); err == nil {
			return references.ErrDuplicate
		}
	}
	if m.failInsert {
		return errors.New("database unavailable")
	}
	m.items[identity.OwnerID(ctx)+"/"+item.ID] = item
	return nil
}
func (m *referenceMemory) Delete(ctx context.Context, id string) error {
	delete(m.items, identity.OwnerID(ctx)+"/"+id)
	return nil
}
func referencePNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.RGBA{255, 0, 0, 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func referenceUploadRequest(t *testing.T, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "reference.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = writer.Close()
	request := httptest.NewRequest("POST", "/v1/references", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+"secret")
	return request
}
func TestReferenceHTTPLifecycle(t *testing.T) {
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 30<<20)
	if err != nil {
		t.Fatal(err)
	}
	repo := &referenceMemory{items: map[string]references.Image{}}
	library := references.New(repo, store)
	handler := NewWithReferenceLibrary(serviceStub{}, healthStub{}, "secret", store, "signing-secret", time.Hour, nil, nil, library)
	data := referencePNG(t)
	request := referenceUploadRequest(t, data)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 201 {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	var item referenceResponse
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.Name != "reference.png" || item.SizeBytes != int64(len(data)) || strings.Contains(response.Body.String(), "storage_key") {
		t.Fatal("unexpected metadata", response.Body.String())
	}
	for _, method := range []string{"GET", "HEAD"} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, item.URL, nil))
		if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" {
			t.Fatal("provider access failed", response.Code)
		}
		if method == "GET" && !bytes.Equal(response.Body.Bytes(), data) {
			t.Fatal("image bytes changed")
		}
	}
	for _, tc := range []struct {
		method, path, key string
		status            int
	}{
		{"GET", "/v1/references", "", 401}, {"DELETE", "/v1/references/" + item.ID, "", 401},
		{"GET", strings.Replace(item.URL, "signature=", "signature=bad", 1), "", 403},
		{"GET", strings.Replace(item.URL, identity.SystemOwnerID, uuid.NewString(), 1), "", 403},
		{"GET", "/v1/references?offset=-1", "secret", 400},
		{"DELETE", "/v1/references/not-a-uuid", "secret", 404},
		{"GET", "/v1/references", "secret", 200},
	} {
		request = httptest.NewRequest(tc.method, tc.path, nil)
		if tc.key != "" {
			request.Header.Set("Authorization", "Bearer "+tc.key)
		}
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("%s %s: %d", tc.method, tc.path, response.Code)
		}
	}
	bob := identity.WithOwnerID(context.Background(), uuid.NewString())
	if _, _, err := library.Open(bob, item.ID); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatal("cross-owner read", err)
	}
	if err := library.Delete(bob, item.ID); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatal("cross-owner deletion", err)
	}
	rows, err := library.List(bob, 48, 0)
	if err != nil || len(rows) != 0 {
		t.Fatal("cross-owner list")
	}
	request = httptest.NewRequest("DELETE", "/v1/references/"+item.ID, nil)
	request.Header.Set("Authorization", "Bearer "+"secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatal("delete failed", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", item.URL, nil))
	if response.Code != 404 {
		t.Fatal("deleted capability still works")
	}
}
func TestReferenceRejectsInvalidUploadsAndCompensates(t *testing.T) {
	root := t.TempDir()
	store, _ := storage.NewLocal(root, http.DefaultClient, 30<<20)
	repo := &referenceMemory{items: map[string]references.Image{}}
	lib := references.New(repo, store)
	for _, data := range [][]byte{nil, []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"), []byte("not an image"), bytes.Repeat([]byte("x"), int(references.MaxBytes+1))} {
		if _, err := lib.Upload(context.Background(), "fake.png", bytes.NewReader(data)); !errors.Is(err, references.ErrInvalidImage) {
			t.Fatal("bad image accepted", err)
		}
	}
	repo.failInsert = true
	if _, err := lib.Upload(context.Background(), "test.png", bytes.NewReader(referencePNG(t))); err == nil {
		t.Fatal("database failure ignored")
	}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			t.Errorf("orphan file after failed metadata insert: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(repo.items) != 0 {
		t.Fatal("failed insert became visible")
	}
}
func TestReferenceCapabilityRangeRead(t *testing.T) {
	// Existing provider inputs use ordinary HTTP(S) URLs; no storage credentials
	// or data URLs are embedded in the generation JSON.
	raw := referencePNG(t)
	store, _ := storage.NewLocal(t.TempDir(), http.DefaultClient, 30<<20)
	repo := &referenceMemory{items: map[string]references.Image{}}
	handler := NewWithReferenceLibrary(serviceStub{}, healthStub{}, "secret", store, "signing-secret", time.Hour, nil, nil, references.New(repo, store))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, referenceUploadRequest(t, raw))
	var item referenceResponse
	_ = json.Unmarshal(response.Body.Bytes(), &item)
	if strings.Contains(item.URL, base64.StdEncoding.EncodeToString(raw)) {
		t.Fatal("inline image leaked")
	}
	request := httptest.NewRequest("GET", item.URL, nil)
	request.Header.Set("Range", "bytes=0-7")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 206 {
		t.Fatal("range fetch failed", response.Code)
	}
	got, _ := io.ReadAll(response.Result().Body)
	if !bytes.Equal(got, raw[:8]) {
		t.Fatal("wrong image range")
	}
}

func (m *referenceMemory) FindByHash(ctx context.Context, digest string) (references.Image, error) {
	for key, item := range m.items {
		if strings.HasPrefix(key, identity.OwnerID(ctx)+"/") && item.SHA256 == digest {
			return item, nil
		}
	}
	return references.Image{}, storage.ErrObjectNotFound
}
func (m *referenceMemory) UnhashedBySize(ctx context.Context, size int64) ([]references.Image, error) {
	var items []references.Image
	for key, item := range m.items {
		if strings.HasPrefix(key, identity.OwnerID(ctx)+"/") && item.SHA256 == "" && item.SizeBytes == size {
			items = append(items, item)
		}
	}
	return items, nil
}
func (m *referenceMemory) SetHash(ctx context.Context, id, digest string) error {
	if _, err := m.FindByHash(ctx, digest); err == nil {
		return references.ErrDuplicate
	}
	key := identity.OwnerID(ctx) + "/" + id
	item := m.items[key]
	item.SHA256 = digest
	m.items[key] = item
	return nil
}

func TestReferenceDuplicateContentAndLegacyFiles(t *testing.T) {
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 30<<20)
	if err != nil {
		t.Fatal(err)
	}
	repo := &referenceMemory{items: map[string]references.Image{}}
	library := references.New(repo, store)
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	data := referencePNG(t)
	first, err := library.Upload(ctx, "original.png", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	second, err := library.Upload(ctx, "renamed.png", bytes.NewReader(data))
	if err != nil || !second.Duplicate || second.ID != first.ID || len(repo.items) != 1 {
		t.Fatalf("duplicate was copied: %+v %v", second, err)
	}
	// Pre-migration files are matched by their contents, never by size alone.
	key := identity.OwnerID(ctx) + "/" + first.ID
	legacy := repo.items[key]
	legacy.SHA256 = ""
	repo.items[key] = legacy
	second, err = library.Upload(ctx, "legacy.png", bytes.NewReader(data))
	if err != nil || second.ID != first.ID || !second.Duplicate || repo.items[key].SHA256 == "" {
		t.Fatalf("legacy dedup: %+v %v", second, err)
	}
	different := bytes.Clone(data)
	different[len(different)-1] ^= 1
	third, err := library.Upload(ctx, "same-size.png", bytes.NewReader(different))
	if err != nil || third.ID == first.ID || third.Duplicate {
		t.Fatalf("size-only false positive: %+v %v", third, err)
	}
	other := identity.WithOwnerID(context.Background(), uuid.NewString())
	private, err := library.Upload(other, "private.png", bytes.NewReader(data))
	if err != nil || private.ID == first.ID || private.Duplicate {
		t.Fatalf("cross-account disclosure: %+v %v", private, err)
	}
}

func TestAudioVideoLibraryContentIdentity(t *testing.T) {
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 200<<20)
	if err != nil {
		t.Fatal(err)
	}
	repo := &referenceMemory{items: map[string]references.Image{}}
	library := references.New(repo, store)
	fixtures := []struct {
		kind string
		data []byte
	}{
		{"video/mp4", append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0, 0, 0, 0, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}, make([]byte, 64)...)},
		{"video/quicktime", append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'q', 't', ' ', ' ', 0, 0, 0, 0, 'q', 't', ' ', ' ', 0, 0, 0, 0}, make([]byte, 64)...)},
		{"audio/mpeg", append([]byte("ID3"), make([]byte, 64)...)},
		{"audio/wave", append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 64)...)},
	}
	for _, f := range fixtures {
		t.Run(f.kind, func(t *testing.T) {
			ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
			item, err := library.Upload(ctx, "misleading.png", bytes.NewReader(f.data))
			if err != nil || item.ContentType != f.kind || item.SizeBytes != int64(len(f.data)) {
				t.Fatalf("item=%+v error=%v", item, err)
			}
			duplicate, err := library.Upload(ctx, "another-name", bytes.NewReader(f.data))
			if err != nil || !duplicate.Duplicate || duplicate.ID != item.ID {
				t.Fatalf("duplicate=%+v err=%v", duplicate, err)
			}
			_, object, err := library.Open(ctx, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(object.Reader)
			object.Reader.Close()
			if err != nil || !bytes.Equal(data, f.data) {
				t.Fatal("stored bytes changed", err)
			}
			if err = library.Delete(ctx, item.ID); err != nil {
				t.Fatal(err)
			}
			if _, _, err = library.Open(ctx, item.ID); !errors.Is(err, storage.ErrObjectNotFound) {
				t.Fatal("file still exists", err)
			}
		})
	}
}
