package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
)

// A cost limit of the wrong type or format must name its field, as a negative
// one already does, instead of blaming the whole body.
func TestGenerationCostLimitFormatNamesTheField(t *testing.T) {
	newServer := func() (http.Handler, *admissionRepository, *admissionQueue) {
		repo, queue := &admissionRepository{}, &admissionQueue{}
		service := core.NewTaskServiceWithProviders(repo, queue,
			map[string]core.MediaProvider{"mock": mock.New(0, "embedded")},
			map[string]string{"mock": "mock-video-v1"}, "mock")
		service.EnableAccounting()
		return New(service, healthStub{}, "secret"), repo, queue
	}
	rejected := func(t *testing.T, w *httptest.ResponseRecorder, repo *admissionRepository, queue *admissionQueue) {
		t.Helper()
		var got struct {
			Error struct{ Code, Message, Param string }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err, w.Body)
		}
		if w.Code != 400 || got.Error.Code != "invalid_request" || got.Error.Param != "max_expected_cost_usd" || !strings.Contains(got.Error.Message, "max_expected_cost_usd") {
			t.Fatalf("field is not named: %d %s", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "private-value") || repo.creates != 0 || len(queue.ids) != 0 {
			t.Fatal("reject echoed the value or reached admission", w.Body)
		}
	}
	for _, endpoint := range []struct{ path, model, text string }{
		{"/v1/videos", "mock-video-v1", `"prompt":"test"`},
		{"/v1/images/generations", "mock-image-v1", `"prompt":"test"`},
		{"/v1/audio/speech", "mock-speech-v1", `"input":"test"`},
		{"/v1/generations/estimate", "mock-video-v1", `"prompt":"test"`},
	} {
		for _, limit := range []string{`"private-value"`, `"0.5"`, `{}`, `[1]`, `true`, `0.1234567`, `1e999`} {
			t.Run(endpoint.path+"/"+limit, func(t *testing.T) {
				h, repo, queue := newServer()
				w := openAIRequest(h, endpoint.path, fmt.Sprintf(`{"model":"mock/%s",%s,"max_expected_cost_usd":%s}`, endpoint.model, endpoint.text, limit))
				rejected(t, w, repo, queue)
			})
		}
	}
	t.Run("multipart", func(t *testing.T) {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for _, field := range [][2]string{{"model", "mock/mock-video-v1"}, {"prompt", "test"}, {"max_expected_cost_usd", "private-value"}} {
			_ = form.WriteField(field[0], field[1])
		}
		_ = form.Close()
		r := httptest.NewRequest("POST", "/v1/videos", &body)
		r.Header.Set("Authorization", "Bearer secret")
		r.Header.Set("Content-Type", form.FormDataContentType())
		h, repo, queue := newServer()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		rejected(t, w, repo, queue)
	})
	for _, limit := range []string{"null", "0.25", "1e-6"} {
		t.Run("accepted/"+limit, func(t *testing.T) {
			h, repo, _ := newServer()
			w := openAIRequest(h, "/v1/videos", `{"model":"mock/mock-video-v1","prompt":"test","max_expected_cost_usd":`+limit+`}`)
			if w.Code != 202 || repo.creates != 1 {
				t.Fatalf("valid limit rejected: %d %s", w.Code, w.Body)
			}
		})
	}
}
