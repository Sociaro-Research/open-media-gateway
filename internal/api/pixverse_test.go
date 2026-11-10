package api

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
)

func pixverseAdmissionFixture(t *testing.T, reviewed bool) (*Server, *omniAdmissionRepository) {
	t.Helper()
	s, _, _, _ := templateFixture(t)
	c := providerkit.Credentials{APIKey: "synthetic-pixverse-key", BaseURL: "https://fixture.ap-southeast-1.maas.aliyuncs.com/api/v1"}
	settings := providerkit.Settings{}
	if reviewed {
		settings["pixverse_reviewed_connection"] = alibaba.Definition().Identity(c)
	}
	a, err := alibaba.Definition().Factory(c, settings, &http.Client{Transport: omniMockTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			t.Fatal("admission submitted generation", r.Method, r.URL.Path)
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

func TestPixverseHTTPAdmissionPreservesEachNativeMode(t *testing.T) {
	for _, mode := range []string{"t2v", "it2v", "kf2v", "r2v"} {
		t.Run(mode, func(t *testing.T) {
			s, repo := pixverseAdmissionFixture(t, true)
			h := s.handler()
			_, key := issueTestKey(t, h, "alice", `{"name":"Offline PixVerse"}`)
			ctx := omniContext("alice")
			count := map[string]int{"t2v": 0, "it2v": 1, "kf2v": 2, "r2v": 2}[mode]
			var urls []string
			for i := range count {
				im := image.NewRGBA(image.Rect(0, 0, 512, 512))
				draw.Draw(im, im.Bounds(), &image.Uniform{C: color.RGBA{R: byte(30 + i), G: 90, B: 180, A: 255}}, image.Point{}, draw.Src)
				var encoded bytes.Buffer
				if err := png.Encode(&encoded, im); err != nil {
					t.Fatal(err)
				}
				ref, err := s.references.Upload(ctx, "synthetic.png", &encoded)
				if err != nil {
					t.Fatal(err)
				}
				urls = append(urls, s.referenceAbsoluteURL(httptest.NewRequest("GET", "https://gateway.example.test", nil).WithContext(ctx), ref))
			}
			options := map[string]any{}
			prompt := " Synthetic motion "
			switch mode {
			case "t2v":
				options["ratio"] = "16:9"
			case "it2v":
				prompt = ""
			case "kf2v":
				options["reference_roles"] = map[string]string{"image_1": "last_frame", "image_2": "first_frame"}
			case "r2v":
				prompt = " @person holds @guitar "
				options["ratio"] = "16:9"
				options["parameters"] = map[string]any{"reference_names": []string{"person", "guitar"}}
			}
			body := map[string]any{"model": "alibaba/pixverse/pixverse-v6-" + mode, "prompt": prompt, "seconds": "5", "size": "720p", "options": options}
			if len(urls) > 0 {
				body["input_reference"] = urls
			}
			if w := omniRequest(h, "/v1/generations/estimate", key, body, ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			for range 2 {
				if w := omniRequest(h, "/v1/videos", key, body, "pixverse-once"); w.Code != 202 {
					t.Fatal(w.Code, w.Body)
				}
			}
			if repo.creates != 1 || repo.task.Prompt != prompt || !reflect.DeepEqual(repo.task.Options.ImageURLs, urls) || repo.task.Cost == nil || repo.task.Cost.ExpectedUSD != 200000 {
				t.Fatal("native intent, quote or idempotency changed", repo.task)
			}
			for _, raw := range urls {
				if e := repo.task.Options.ReferenceMediaEvidence[raw]; e.Type != "image" || e.Width != 512 || len(e.SHA256) != 64 {
					t.Fatal("owned image inspection lost", e)
				}
			}
			options["audio_urls"] = []string{"https://media.example.test/unsupported.wav"}
			if w := omniRequest(h, "/v1/videos", key, body, "invalid"); w.Code != 400 || repo.creates != 1 {
				t.Fatal("unsupported reference admitted", w.Code, w.Body)
			}
		})
	}
}

func TestPixverseHTTPUnreviewedConnectionKeepsAdmissionBlocked(t *testing.T) {
	s, repo := pixverseAdmissionFixture(t, false)
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"Unreviewed PixVerse"}`)
	models := sendTest(h, "GET", "/v1/models", "", "", key)
	if models.Code != 200 || strings.Contains(models.Body.String(), "pixverse-v6") || !strings.Contains(models.Body.String(), "qwen-audio-3.1-tts-next") {
		t.Fatal("connection guard changed Qwen visibility", models.Code, models.Body)
	}
	body := map[string]any{"model": "alibaba/pixverse/pixverse-v6-t2v", "prompt": "Synthetic", "seconds": "5", "size": "720p", "options": map[string]any{"ratio": "16:9"}}
	if w := omniRequest(h, "/v1/videos", key, body, "blocked"); w.Code != 400 || repo.creates != 0 {
		t.Fatal("unreviewed connection admitted PixVerse", w.Code, w.Body)
	}
}
