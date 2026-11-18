package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"github.com/google/uuid"
)

// Media type names are case-insensitive (RFC 9110 section 8.3.1): a multipart
// form must not be read as JSON because its Content-Type is capitalised.
func TestMultipartContentTypeIgnoresCase(t *testing.T) {
	for _, spelling := range []string{"multipart/form-data", "Multipart/Form-Data", "multipart/Form-Data"} {
		// With an Idempotency-Key the admission-intent middleware reads the body first.
		for _, key := range []string{"", "retry-1"} {
			t.Run("video/"+spelling+"/key="+key, func(t *testing.T) {
				repo, queue := &admissionRepository{}, &admissionQueue{}
				service := core.NewTaskServiceWithProviders(repo, queue,
					map[string]core.MediaProvider{"mock": mock.New(0, "embedded")},
					map[string]string{"mock": "mock-video-v1"}, "mock")
				service.EnableAccounting()
				var body bytes.Buffer
				form := multipart.NewWriter(&body)
				_ = form.WriteField("model", "mock/mock-video-v1")
				_ = form.WriteField("prompt", "test")
				_ = form.Close()
				r := httptest.NewRequest("POST", "/v1/videos", &body)
				r.Header.Set("Authorization", "Bearer secret")
				r.Header.Set("Content-Type", spelling+"; boundary="+form.Boundary())
				if key != "" {
					r.Header.Set("Idempotency-Key", key)
				}
				w := httptest.NewRecorder()
				New(service, healthStub{}, "secret").ServeHTTP(w, r)
				if w.Code != 202 || repo.creates != 1 || len(queue.ids) != 1 {
					t.Fatalf("multipart form was not admitted: %d %s", w.Code, w.Body)
				}
			})
		}
		t.Run("template asset/"+spelling, func(t *testing.T) {
			s, repo, _, _ := templateFixture(t)
			id := uuid.NewString()
			repo.items[id] = templates.Record{ID: id, Version: 1}
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			file, _ := form.CreateFormFile("file", "fixture")
			_, _ = file.Write(referencePNG(t))
			_ = form.Close()
			r := httptest.NewRequest("POST", "/v1/system/templates/"+id+"/assets", &body)
			r.Header.Set("Content-Type", spelling+"; boundary="+form.Boundary())
			r.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
			w := httptest.NewRecorder()
			s.handler().ServeHTTP(w, r)
			if w.Code != 201 {
				t.Fatalf("multipart upload was not stored: %d %s", w.Code, w.Body)
			}
		})
	}
}
