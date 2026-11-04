package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

func TestKlingFlashAdmissionRejectsUnsupportedAndUnquotedIntent(t *testing.T) {
	for _, model := range []string{kling.TextToVideo4Flash, kling.ImageToVideo4Flash, kling.OmniVideo4Flash} {
		for _, path := range []string{"/v1/videos", "/v1/generations"} {
			for _, variation := range []string{"1080p", "21 seconds", "omitted duration"} {
				t.Run(model+path+"/"+variation, func(t *testing.T) {
					adapter, err := kling.Definition().Factory(providerkit.Credentials{APIKey: "fixture-secret"}, nil, &http.Client{Transport: klingAdmissionTransport(func(*http.Request) (*http.Response, error) {
						t.Fatal("invalid or unquoted admission contacted provider")
						return nil, core.ErrOptionsInvalid
					})})
					if err != nil {
						t.Fatal(err)
					}
					// Use the existing transactional fixture: a new accounted task
					// with no verified quote is rejected, as in the real repository.
					repo, queue := &omniAdmissionRepository{}, &admissionQueue{}
					svc := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"kling": adapter}, map[string]string{"kling": kling.TextToVideo4}, "kling")
					svc.EnableAccounting()
					handler := New(svc, healthStub{}, "secret")
					body := map[string]any{"model": "kling/" + model, "prompt": "Synthetic offline fixture", "seconds": "10"}
					if model == kling.ImageToVideo4Flash {
						body["input_reference"] = []string{"https://cdn.example/first.png"}
					}
					want := 400
					switch variation {
					case "1080p":
						body["options"] = map[string]any{"resolution": "1080p"}
					case "21 seconds":
						body["seconds"] = "21"
					case "omitted duration":
						delete(body, "seconds")
						want = 422
					}
					raw, _ := json.Marshal(body)
					for _, endpoint := range []string{"/v1/generations/estimate", path} {
						response := openAIRequest(handler, endpoint, string(raw))
						if response.Code != want || repo.creates != 0 || len(queue.ids) != 0 {
							t.Fatal("unsupported/unquoted intent accepted", response.Code, response.Body)
						}
						if variation == "omitted duration" && (!strings.Contains(response.Body.String(), "pricing_unavailable") || strings.Contains(response.Body.String(), "expected_usd")) {
							t.Fatal("omitted duration received a fabricated quote", response.Body)
						}
					}
				})
			}
		}
	}
}
