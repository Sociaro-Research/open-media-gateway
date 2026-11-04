package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

func TestKlingVideoAndGenericAdmissionUseReviewedContracts(t *testing.T) {
	for _, path := range []string{"/v1/videos", "/v1/generations"} {
		for _, model := range []string{kling.TextToVideo4Flash, kling.ImageToVideo4Flash, kling.OmniVideo4Flash, kling.TextToVideo, kling.ImageToVideo, kling.TextToVideo4, kling.ImageToVideo4, kling.TextToVideo3Turbo, kling.ImageToVideo3Turbo, kling.TextToVideo25Turbo, kling.ImageToVideo25Turbo, kling.OmniVideo4} {
			t.Run(path+"/"+model, func(t *testing.T) {
				allowDispatch, posts := false, 0
				adapter, err := kling.Definition().Factory(providerkit.Credentials{APIKey: "fixture-secret", Extra: map[string]string{"kling4_1080p_price": "0.200001"}}, nil, &http.Client{Transport: klingAdmissionTransport(func(req *http.Request) (*http.Response, error) {
					if !allowDispatch {
						t.Fatal("admission or estimate contacted provider")
					}
					expectedPath := map[string]string{kling.TextToVideo4Flash: "/text-to-video/kling-4.0-flash", kling.ImageToVideo4Flash: "/image-to-video/kling-4.0-flash", kling.OmniVideo4Flash: "/omni-video/kling-4.0-flash"}[model]
					if req.Method != "POST" || expectedPath == "" || req.URL.Path != expectedPath {
						t.Fatal("accepted Flash intent reached wrong mock route", req.URL.Path)
					}
					posts++
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"id":"admitted_flash","status":"submitted"}}`))}, nil
				})})
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
				if model == kling.ImageToVideo3Turbo || model == kling.ImageToVideo25Turbo || model == kling.ImageToVideo4Flash {
					body["input_reference"] = []string{"https://cdn.example/first.png"}
				}
				if model == kling.OmniVideo4 {
					body["seconds"] = "10"
					body["input_reference"] = []string{"https://cdn.example/subject.png"}
					body["options"] = map[string]any{"reference_mode": "references"}
				}
				if model == kling.TextToVideo4Flash || model == kling.ImageToVideo4Flash || model == kling.OmniVideo4Flash {
					body["seconds"] = "10"
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
				if (model == kling.TextToVideo4Flash || model == kling.ImageToVideo4Flash || model == kling.OmniVideo4Flash) && int64(repo.task.Cost.ExpectedUSD) != 940000 {
					t.Fatal("Flash inherited standard account override or wrong list estimate", repo.task.Cost)
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
				expected := reviewedKlingAPIIDs()
				sort.Strings(got)
				sort.Strings(expected)
				if !slices.Equal(got, expected) {
					t.Fatal("exact operation catalogue differs", got, expected)
				}
				if model == kling.TextToVideo4Flash || model == kling.ImageToVideo4Flash || model == kling.OmniVideo4Flash {
					if posts != 0 || repo.task.Options.Duration != 10 || repo.task.Options.Resolution != "720p" {
						t.Fatal("admission changed Flash intent or dispatched early")
					}
					// Simulate the worker boundary explicitly after admission; the
					// transport remains a local function, never a network socket.
					allowDispatch = true
					ctx := core.WithProviderConnectionScope(identity.WithOwnerID(context.Background(), "fixture-owner"), core.ProviderConnectionScope{ID: "kling", OwnerID: "fixture-provider-owner", Revision: "fixture-revision"})
					task, err := adapter.Generate(ctx, core.MediaRequest{TaskID: repo.task.ID, Model: repo.task.Model, Prompt: repo.task.Prompt, Options: repo.task.Options.ProviderOptions()})
					if err != nil || posts != 1 || !strings.HasPrefix(task.ID, "kling-v1.") {
						t.Fatal("accepted Flash mock dispatch failed", err, posts)
					}
				}
			})
		}
	}
}

type klingAdmissionTransport func(*http.Request) (*http.Response, error)

func (f klingAdmissionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestKlingFlashPublicModelsExposeDistinctContracts(t *testing.T) {
	adapter, err := kling.Definition().Factory(providerkit.Credentials{APIKey: "fixture-secret"}, nil, &http.Client{Transport: klingAdmissionTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("catalogue contacted provider")
		return nil, core.ErrOptionsInvalid
	})})
	if err != nil {
		t.Fatal(err)
	}
	repo, queue := &admissionRepository{}, &admissionQueue{}
	svc := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"kling": adapter}, map[string]string{"kling": kling.TextToVideo4}, "kling")
	h := New(svc, healthStub{}, "secret")
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var response struct {
		Data []struct {
			ID           string          `json:"id"`
			Capabilities core.MediaModel `json:"capabilities"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"kling/" + kling.TextToVideo4Flash: false, "kling/" + kling.ImageToVideo4Flash: false, "kling/" + kling.OmniVideo4Flash: false}
	if len(response.Data) != 37 {
		t.Fatal("unexpected HTTP media catalogue size", len(response.Data))
	}
	gotIDs := []string{}
	for _, row := range response.Data {
		gotIDs = append(gotIDs, row.ID)
		if _, ok := wanted[row.ID]; !ok {
			continue
		}
		wanted[row.ID] = true
		m := row.Capabilities
		if !slices.Equal(m.Resolutions, []string{"720p"}) || m.MinDuration != 3 || m.MaxDuration != 20 || !m.DurationMayBeInferred {
			t.Fatal("incorrect public Flash contract", row.ID, m)
		}
		if row.ID == "kling/"+kling.ImageToVideo4Flash && (m.MaxImageInputs != 1 || len(m.Ratios) != 0 || len(m.ReferenceModes) != 1 || m.ReferenceModes[0].ID != "first_frame") {
			t.Fatal("image route inherited standard keyframes/ratio", m)
		}
		if row.ID == "kling/"+kling.OmniVideo4Flash && (!m.SupportsVideoInput || m.ReferenceMediaLimits.Video.MaxTotalDuration != 20) {
			t.Fatal("Omni lost independent input contract", m)
		}
	}
	expectedIDs := []string{}
	for _, id := range reviewedKlingAPIIDs() {
		expectedIDs = append(expectedIDs, "kling/"+id)
	}
	sort.Strings(gotIDs)
	sort.Strings(expectedIDs)
	if !slices.Equal(gotIDs, expectedIDs) {
		t.Fatal("HTTP catalogue lost prior IDs or exposed unreviewed operations", gotIDs)
	}
	for id, found := range wanted {
		if !found {
			t.Fatal("missing public Flash model", id)
		}
	}
	if repo.creates != 0 || len(queue.ids) != 0 || strings.Contains(w.Body.String(), "fixture-secret") {
		t.Fatal("catalogue read mutated tasks or exposed key")
	}
}

func reviewedKlingAPIIDs() []string {
	return []string{kling.TextToVideo4Flash, kling.ImageToVideo4Flash, kling.OmniVideo4Flash, kling.SessionPreview, kling.OmniVideo3, kling.OmniVideoO1, kling.LipSync, kling.MultiElementsEditing, kling.TextToVideo, kling.ImageToVideo, kling.TextToVideo4, kling.ImageToVideo4, kling.TextToVideo3Turbo, kling.ImageToVideo3Turbo, kling.TextToVideo25Turbo, kling.ImageToVideo25Turbo, kling.OmniVideo4, kling.ImageGeneration3, kling.ImageGeneration21, kling.ImageOmni3, kling.ImageO1, kling.MultiImage21, kling.ImageOutpainting, kling.ImageMultiShot, kling.TextToAudio, kling.VideoToAudio, kling.TextToSpeech, kling.AvatarImageToVideo, kling.TextToVideo26, kling.ImageToVideo26, kling.MotionControl3, kling.MotionControl26, kling.KlingEffects, kling.ApparelReplicator, kling.GoodsStudio, kling.VideoCommerce, kling.VirtualTryOn}
}
