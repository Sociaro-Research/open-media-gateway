package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
)

type videoInputRequiredProvider struct{ *mock.Adapter }

func (p videoInputRequiredProvider) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	models, err := p.Adapter.ListModels(ctx)
	for i := range models {
		if models[i].MediaType == "video" {
			models[i].RequiresImageInput = true
		}
	}
	return models, err
}

func TestVideoMultipartAdmissionAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name, format, storage, path, model, fileKey string
		fields                                      [][2]string
		files                                       int
		nonImage, requireImage                      bool
		want                                        int
		wantMessage                                 string
	}{
		{name: "json_text", format: "json", want: 202},
		{name: "multipart_text", want: 202},
		{name: "no_reference_library", storage: "none", want: 202},
		{name: "no_media_signer", storage: "no_signer", want: 202},
		{name: "direct_upload_storage", storage: "direct", want: 202},
		{name: "reference_planning_failure_does_not_block_text", storage: "broken_direct", want: 202},
		{name: "multipart_image_reference", files: 1, want: 202},
		{name: "json_image_reference", format: "json", fields: [][2]string{{"input_reference", "https://example.test/reference.png"}}, want: 202},
		{name: "multipart_reference_url", fields: [][2]string{{"input_reference", "https://example.test/reference.png"}}, want: 202},
		{name: "multipart_reference_url_direct_storage", storage: "direct", fields: [][2]string{{"input_reference", "https://example.test/reference.png"}}, want: 202},
		{name: "image_edit_reference", path: "/v1/images/edits", model: "mock/mock-image-v1", fileKey: "image[]", files: 1, want: 202},
		{name: "image_edit_requires_image", path: "/v1/images/edits", model: "mock/mock-image-v1", want: 400, wantMessage: "send 1–16 images"},
		{name: "unknown_model", model: "mock/unknown", want: 400, wantMessage: "model"},
		{name: "unknown_field", fields: [][2]string{{"unknown", "value"}}, want: 400, wantMessage: "unsupported parameter"},
		{name: "duplicate_prompt", fields: [][2]string{{"prompt", "duplicate"}}, want: 400, wantMessage: "field must appear once"},
		{name: "invalid_options_json", fields: [][2]string{{"options", "{"}}, want: 400},
		{name: "invalid_seconds", fields: [][2]string{{"seconds", "0"}}, want: 400, wantMessage: "seconds"},
		{name: "oversize_field", fields: [][2]string{{"extra", strings.Repeat("x", (64<<10)+1)}}, want: 400, wantMessage: "field exceeds 64 KiB"},
		{name: "two_video_references", files: 2, want: 400, wantMessage: "one video input reference"},
		{name: "unsupported_file_field", fileKey: "other", files: 1, want: 400, wantMessage: "unsupported file field"},
		{name: "file_and_url", fields: [][2]string{{"input_reference", "https://example.test/image.png"}}, files: 1, want: 400, wantMessage: "send files or URLs"},
		{name: "video_file_not_image", files: 1, nonImage: true, want: 400, wantMessage: "must be an image"},
		{name: "file_requires_reference_library", storage: "none", files: 1, want: 503, wantMessage: "reference storage is unavailable"},
		{name: "file_requires_media_signer", storage: "no_signer", files: 1, want: 503, wantMessage: "reference storage is unavailable"},
		{name: "file_requires_direct_upload", storage: "direct", files: 1, want: 409, wantMessage: "direct"},
		{name: "reference_required_model_stays_rejected", requireImage: true, want: 400},
		{name: "reference_required_model_accepts_image", requireImage: true, files: 1, want: 202},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &admissionRepository{}
			queue := &admissionQueue{}
			var provider core.MediaProvider = mock.New(time.Second, "")
			if tc.requireImage {
				provider = videoInputRequiredProvider{provider.(*mock.Adapter)}
			}
			service := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"mock": provider}, map[string]string{"mock": "mock-video-v1"}, "mock")
			service.EnableAccounting()
			libraryRepo := &referenceMemory{items: map[string]references.Image{}}
			var h http.Handler = New(service, healthStub{}, "secret")
			if tc.storage != "none" {
				local, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 32<<20)
				if err != nil {
					t.Fatal(err)
				}
				var store references.Store = local
				if tc.storage == "direct" {
					store = &directMedia{Local: local}
				}
				if tc.storage == "broken_direct" {
					store = &brokenVideoReferenceStore{Local: local}
				}
				library := references.New(libraryRepo, store)
				h = NewGateway(Dependencies{Service: service, Health: healthStub{}, APIKey: "secret", Storage: local, SigningSecret: "signature-secret", MediaURLTTL: time.Hour, References: library, AssetPublicURL: "https://gateway.example"})
				if tc.storage == "no_signer" {
					h = (&Server{service: service, health: healthStub{}, apiKey: "secret", references: library}).handler()
				}
			}
			route := tc.path
			if route == "" {
				route = "/v1/videos"
			}
			model := tc.model
			if model == "" {
				model = "mock/mock-video-v1"
			}
			fields := [][2]string{{"model", model}, {"prompt", "Sunrise over a quiet lake"}}
			fields = append(fields, tc.fields...)
			var request *http.Request
			if tc.format == "json" {
				object := map[string]string{}
				for _, f := range fields {
					object[f[0]] = f[1]
				}
				body, _ := json.Marshal(object)
				request = httptest.NewRequest("POST", "https://gateway.example"+route, bytes.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
			} else {
				var body bytes.Buffer
				writer := multipart.NewWriter(&body)
				for _, f := range fields {
					if err := writer.WriteField(f[0], f[1]); err != nil {
						t.Fatal(err)
					}
				}
				for i := 0; i < tc.files; i++ {
					key := tc.fileKey
					if key == "" {
						key = "input_reference"
					}
					part, err := writer.CreateFormFile(key, "fixture.png")
					if err != nil {
						t.Fatal(err)
					}
					data := referencePNG(t)
					if tc.nonImage {
						data = []byte("not an image")
					}
					if _, err = part.Write(data); err != nil {
						t.Fatal(err)
					}
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				request = httptest.NewRequest("POST", "https://gateway.example"+route, &body)
				request.Header.Set("Content-Type", writer.FormDataContentType())
			}
			request.Header.Set("Authorization", "Bearer secret")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status=%d want=%d response=%s", response.Code, tc.want, response.Body.String())
			}
			if tc.wantMessage != "" && !strings.Contains(response.Body.String(), tc.wantMessage) {
				t.Fatalf("unexpected error: %s", response.Body.String())
			}
			if tc.want == 202 {
				if repo.creates != 1 || len(queue.ids) != 1 || repo.task.Status != core.TaskQueued || repo.task.Cost == nil || repo.task.Cost.ExpectedUSD != 0 {
					t.Fatal("admission must only queue one free mock task", repo, queue)
				}
				expectedReferences := tc.files
				if len(tc.fields) > 0 && tc.fields[0][0] == "input_reference" {
					expectedReferences = 1
				}
				if len(repo.task.Options.ImageURLs) != expectedReferences {
					t.Fatalf("references=%v want=%d", repo.task.Options.ImageURLs, expectedReferences)
				}
			} else if repo.creates != 0 || len(queue.ids) != 0 {
				t.Fatal("invalid request admitted")
			}
			if tc.files == 0 && len(libraryRepo.items) != 0 {
				t.Fatal("text-only form stored a reference")
			}
		})
	}
}

type brokenVideoReferenceStore struct{ *storage.Local }

func (*brokenVideoReferenceStore) PlanUpload(context.Context, string, string, string, int64) (*storage.PendingUpload, error) {
	panic("text-only video must not plan a reference upload")
}
func (*brokenVideoReferenceStore) StartUpload(context.Context, *storage.PendingUpload) (storage.UploadTarget, error) {
	panic("unexpected upload")
}
func (*brokenVideoReferenceStore) CompleteUpload(context.Context, storage.PendingUpload) (core.StoredMedia, error) {
	panic("unexpected completion")
}
func (*brokenVideoReferenceStore) AbortUpload(context.Context, storage.PendingUpload, bool) error {
	panic("unexpected abort")
}

// Video forms must inspect part headers to distinguish optional file uploads from
// SDK text-only forms. S3 rejection still must not consume the large file body.
func TestVideoMultipartDirectStorageRejectsBeforeFileDrain(t *testing.T) {
	store := &directMedia{}
	library := references.New(&referenceMemory{items: map[string]references.Image{}}, store)
	h := NewWithReferenceLibrary(openAIFixture(), healthStub{}, "secret", store, "signature-secret", time.Hour, nil, nil, library)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "mock/video"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("prompt", "test"); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("input_reference", "reference.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(make([]byte, 1<<20)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	measured := &videoMultipartReadCounter{Reader: bytes.NewReader(body.Bytes())}
	r := httptest.NewRequest("POST", "https://gateway.example/v1/videos", measured)
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "direct_upload_required") {
		t.Fatal(w.Code, w.Body.String())
	}
	if measured.read == 0 || measured.read > 16<<10 {
		t.Fatalf("read %d bytes of a %d-byte form; only buffered headers may be inspected", measured.read, body.Len())
	}
}

type videoMultipartReadCounter struct {
	io.Reader
	read int
}

func (r *videoMultipartReadCounter) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}

func TestVideoMultipartRejectsTooManyParts(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for i := 0; i < 1001; i++ {
		if err := form.WriteField("prompt", "x"); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/videos", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	_, files, ok := videoMultipartFields(w, r)
	if ok || files || w.Code != 400 || !strings.Contains(w.Body.String(), "1000 parts") {
		t.Fatal(ok, files, w.Code, w.Body.String())
	}
}
