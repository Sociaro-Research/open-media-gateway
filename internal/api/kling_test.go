package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

func TestKlingVideoAndGenericAdmissionUseReviewedContracts(t *testing.T) {
	for _, path := range []string{"/v1/videos", "/v1/generations"} {
		for _, model := range []string{kling.TextToVideo, kling.ImageToVideo, kling.TextToVideo4, kling.ImageToVideo4, kling.TextToVideo3Turbo, kling.ImageToVideo3Turbo, kling.TextToVideo25Turbo, kling.ImageToVideo25Turbo, kling.OmniVideo4} {
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
				if model == kling.ImageToVideo3Turbo || model == kling.ImageToVideo25Turbo {
					body["input_reference"] = []string{"https://cdn.example/first.png"}
				}
				if model == kling.OmniVideo4 {
					body["seconds"] = "10"
					body["input_reference"] = []string{"https://cdn.example/subject.png"}
					body["options"] = map[string]any{"reference_mode": "references"}
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
				got := []string{}
				for _, m := range models {
					got = append(got, m.ID)
				}
				expected := []string{kling.SessionInit, kling.SessionAdd, kling.SessionDelete, kling.SessionClear, kling.SessionPreview, kling.OmniVideo3, kling.OmniVideoO1, kling.ResourceCreateElement, kling.ResourceCreateVoice, kling.ImageRecognition, kling.FaceDetection, kling.LipSync, kling.MultiElementsEditing, kling.TextToVideo, kling.ImageToVideo, kling.TextToVideo4, kling.ImageToVideo4, kling.TextToVideo3Turbo, kling.ImageToVideo3Turbo, kling.TextToVideo25Turbo, kling.ImageToVideo25Turbo, kling.OmniVideo4, kling.ImageGeneration3, kling.ImageGeneration21, kling.ImageOmni3, kling.ImageO1, kling.MultiImage21, kling.ImageOutpainting, kling.ImageMultiShot, kling.TextToAudio, kling.VideoToAudio, kling.TextToSpeech, kling.AvatarImageToVideo, kling.TextToVideo26, kling.ImageToVideo26, kling.MotionControl3, kling.MotionControl26, kling.KlingEffects, kling.ApparelReplicator, kling.GoodsStudio, kling.VideoCommerce, kling.VirtualTryOn}
				sort.Strings(got)
				sort.Strings(expected)
				if !slices.Equal(got, expected) {
					t.Fatal("exact operation catalogue differs", got, expected)
				}
			})
		}
	}
}
