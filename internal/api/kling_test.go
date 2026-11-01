package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

func TestKlingVideoAndGenericAdmissionUseReviewedContracts(t *testing.T) {
	for _, path := range []string{"/v1/videos", "/v1/generations"} {
		for _, model := range []string{kling.TextToVideo, kling.ImageToVideo, kling.TextToVideo4, kling.ImageToVideo4} {
			t.Run(path+"/"+model, func(t *testing.T) {
				adapter, err := kling.Definition().Factory(providerkit.Credentials{APIKey: "fixture-secret", Extra: map[string]string{"kling4_1080p_price": "0.200001"}}, nil, &http.Client{})
				if err != nil {
					t.Fatal(err)
				}
				repo, queue := &admissionRepository{}, &admissionQueue{}
				svc := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"kling": adapter}, map[string]string{"kling": kling.TextToVideo4}, "kling")
				svc.EnableAccounting()
				h := New(svc, healthStub{}, "secret")
				body := map[string]any{"model": "kling/" + model, "prompt": "Synthetic fixture"}
				if model == kling.ImageToVideo || model == kling.ImageToVideo4 {
					body["input_reference"] = []string{"https://cdn.example/first.png", "https://cdn.example/last.png"}
				}
				raw, _ := json.Marshal(body)
				w := openAIRequest(h, "/v1/generations/estimate", string(raw))
				if w.Code != 200 || repo.creates != 0 || len(queue.ids) != 0 {
					t.Fatal(w.Code, w.Body)
				}
				w = openAIRequest(h, path, string(raw))
				if w.Code != 202 || repo.creates != 1 || len(queue.ids) != 1 {
					t.Fatal(w.Code, w.Body)
				}
				if repo.task.Provider != "kling" || repo.task.Model != model || repo.task.Cost == nil || repo.task.Cost.ActualUSD != nil {
					t.Fatal(repo.task)
				}
				if strings.Contains(w.Body.String(), "fixture-secret") {
					t.Fatal("key exposed")
				}
				// Admission and estimate never contact the provider; worker dispatch is separate.
				models, _ := adapter.(core.ModelCatalog).ListModels(context.Background())
				if len(models) != 4 {
					t.Fatal(models)
				}
			})
		}
	}
}
