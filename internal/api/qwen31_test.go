package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
)

func qwen31AdmissionFixture(t *testing.T) (*Server, *omniAdmissionRepository) {
	t.Helper()
	s, _, _, _ := templateFixture(t)
	a, err := alibaba.New("synthetic-qwen-key", "https://fixture.ap-southeast-1.maas.aliyuncs.com/api/v1", "", "", &http.Client{Transport: omniMockTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			t.Fatal("admission made a paid or unexpected request", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"output":{"total":0,"models":[]}}`))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	repo := &omniAdmissionRepository{}
	service := core.NewTaskServiceWithProviders(repo, &admissionQueue{}, map[string]core.MediaProvider{"alibaba": a}, nil, "alibaba")
	service.EnableAccounting()
	s.service = service
	return s, repo
}

func TestQwen31AuthenticatedSpeechAdmission(t *testing.T) {
	for _, id := range []string{"qwen-audio-3.1-tts-plus", "qwen-audio-3.1-tts-flash", "qwen-audio-3.1-tts-next"} {
		t.Run(id, func(t *testing.T) {
			s, repo := qwen31AdmissionFixture(t)
			h := s.handler()
			_, key := issueTestKey(t, h, "alice", `{"name":"Offline Qwen"}`)
			body := map[string]any{"model": "alibaba/" + id, "input": "Synthetic hello", "response_format": "mp3"}
			if id != "qwen-audio-3.1-tts-next" {
				body["voice"] = "longanhuan_v3.1"
				body["instructions"] = "Speak gently"
			}
			if w := omniRequest(h, "/v1/generations/estimate", key, body, ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			for range 2 {
				w := omniRequest(h, "/v1/audio/speech", key, body, "qwen-once")
				if w.Code != 202 || strings.Contains(w.Body.String(), "synthetic-qwen-key") {
					t.Fatal(w.Code, w.Body)
				}
			}
			if repo.creates != 1 || repo.task.Cost == nil || repo.task.Options.Format != "mp3" || repo.task.Model != id {
				t.Fatal("lost contract or duplicated admission", repo.task)
			}
			if id == "qwen-audio-3.1-tts-plus" {
				if repo.task.Cost.QuantityUnknown || repo.task.Cost.Pricing.Unit != "character" {
					t.Fatal(repo.task.Cost)
				}
			} else if !repo.task.Cost.QuantityUnknown || len(repo.task.Cost.Pricing.Components) != 2 {
				t.Fatal("unknown token quantity became a monetary estimate", repo.task.Cost)
			}
			body["voice"] = "Cherry"
			if w := omniRequest(h, "/v1/audio/speech", key, body, "other"); w.Code != 400 || repo.creates != 1 {
				t.Fatal("legacy voice accepted", w.Code, w.Body)
			}
		})
	}
}

func TestQwen31NextHTTPReferencesRetainInspectedOrder(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "foreign"}[foreign], func(t *testing.T) {
			s, repo := qwen31AdmissionFixture(t)
			h := s.handler()
			_, key := issueTestKey(t, h, "alice", `{"name":"Offline references"}`)
			owner := "alice"
			if foreign {
				owner = "bob"
			}
			ctx := omniContext(owner)
			var urls []string
			for _, duration := range []float64{1, 2} {
				ref, err := s.references.Upload(ctx, "synthetic.wav", bytes.NewReader(templateAudioWAV(duration)))
				if err != nil {
					t.Fatal(err)
				}
				urls = append(urls, s.referenceAbsoluteURL(httptest.NewRequest("GET", "https://gateway.example.test", nil).WithContext(ctx), ref))
			}
			body := map[string]any{"model": "alibaba/qwen-audio-3.1-tts-next", "input": "@voice2 replies to @voice1", "options": map[string]any{"audio_urls": urls}}
			w := omniRequest(h, "/v1/audio/speech", key, body, "ordered")
			if foreign {
				if w.Code < 400 || w.Code >= 500 || repo.creates != 0 {
					t.Fatal("foreign reference admitted", w.Code, w.Body)
				}
				return
			}
			if w.Code != 202 || !reflect.DeepEqual(repo.task.Options.AudioURLs, urls) {
				t.Fatal("reference order changed", w.Code, w.Body)
			}
			for i, raw := range urls {
				e := repo.task.Options.ReferenceMediaEvidence[raw]
				if e.Type != "audio" || e.Duration != float64(i+1) || len(e.SHA256) != 64 {
					t.Fatal("missing trusted media evidence", e)
				}
			}
			public, _ := json.Marshal(core.GenerationView(repo.task))
			if bytes.Contains(public, []byte("_reference_media")) {
				t.Fatal("private evidence exposed")
			}
		})
	}
}
