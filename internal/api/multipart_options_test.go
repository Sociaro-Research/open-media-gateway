package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
)

// In a multipart form options is JSON text. Text that is not JSON must be
// reported on that field, not as an empty request body.
func TestMultipartMalformedOptionsNameTheField(t *testing.T) {
	send := func(options string) (*httptest.ResponseRecorder, *admissionRepository) {
		repo := &admissionRepository{}
		service := core.NewTaskServiceWithProviders(repo, &admissionQueue{},
			map[string]core.MediaProvider{"mock": mock.New(0, "embedded")},
			map[string]string{"mock": "mock-video-v1"}, "mock")
		service.EnableAccounting()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for _, field := range [][2]string{{"model", "mock/mock-video-v1"}, {"prompt", "test"}, {"options", options}} {
			_ = form.WriteField(field[0], field[1])
		}
		_ = form.Close()
		r := httptest.NewRequest("POST", "/v1/videos", &body)
		r.Header.Set("Authorization", "Bearer secret")
		r.Header.Set("Content-Type", form.FormDataContentType())
		w := httptest.NewRecorder()
		New(service, healthStub{}, "secret").ServeHTTP(w, r)
		return w, repo
	}
	for _, options := range []string{"{", "ratio=16:9", ""} {
		t.Run("malformed/"+options, func(t *testing.T) {
			w, repo := send(options)
			var got struct {
				Error struct{ Code, Message, Param string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err, w.Body)
			}
			if w.Code != 400 || got.Error.Code != "invalid_request" || got.Error.Param != "options" || !strings.Contains(got.Error.Message, "options") || strings.Contains(got.Error.Message, "empty") || repo.creates != 0 {
				t.Fatalf("malformed options are not reported on the field: %d %s", w.Code, w.Body)
			}
		})
	}
	if w, repo := send(`{"seed":7}`); w.Code != 202 || repo.task.Options.Seed == nil || *repo.task.Options.Seed != 7 {
		t.Fatalf("valid options text rejected or dropped: %d %s", w.Code, w.Body)
	}
}
