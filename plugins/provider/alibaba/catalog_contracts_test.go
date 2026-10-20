package alibaba

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

func TestReviewedFileGenerationContracts(t *testing.T) {
	for _, tc := range []struct {
		id, path string
		options  core.MediaOptions
	}{
		{"qwen-image-2.1-turbo", imagePath, core.MediaOptions{Resolution: "1024*1024"}},
		{"vidu/vidu-image_reference2image", imagePath, core.MediaOptions{Resolution: "2048*2048"}},
		{"vidu/viduq3-ad_reference2video", videoPath, core.MediaOptions{Duration: 5, Resolution: "1080p", Ratio: "9:16", ImageURLs: []string{"https://example.test/a.png", "https://example.test/b.png"}}},
		{"vidu/viduq2-pro-fast_img2video", videoPath, core.MediaOptions{Duration: 5, Resolution: "720p", Ratio: "adaptive", ImageURLs: []string{"https://example.test/a.png"}}},
		{"wan2.1-vace-plus", videoPath, core.MediaOptions{Duration: 5, Resolution: "720p", Ratio: "16:9", ImageURLs: []string{"https://example.test/a.png"}}},
		{"wanx2.1-t2v-turbo", videoPath, core.MediaOptions{Duration: 5, Resolution: "720p", Ratio: "16:9"}},
		{"qwen-mt-image-2.0", imageEditPath, core.MediaOptions{ImageURLs: []string{"https://example.test/a.png"}, Parameters: map[string]any{"source_lang": "auto", "target_lang": "en"}}},
		{"qwen-mt-image", imageEditPath, core.MediaOptions{ImageURLs: []string{"https://example.test/a.png"}, Parameters: map[string]any{"target_lang": "en"}}},
		{"qwen-audio-3.0-tts-plus", "/services/audio/tts/SpeechSynthesizer", core.MediaOptions{Voice: "reviewed-user-voice", Format: "wav"}},
		{"cosyvoice-v3-flash", "/services/audio/tts/SpeechSynthesizer", core.MediaOptions{Voice: "reviewed-user-voice", Format: "wav"}},
		{"wan2.2-s2v", "/services/aigc/image2video/video-synthesis", core.MediaOptions{Resolution: "480p", ImageURLs: []string{"https://example.test/portrait.png"}, AudioURL: "https://example.test/speech.wav"}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if strings.HasPrefix(tc.id, "qwen-mt") {
				model, _ := describeDashModel(tc.id, tc.id)
				if len(model.Formats) != 1 || model.Formats[0] != "jpeg" {
					t.Fatal("translation output format must follow the provider contract", model.Formats)
				}
			}
			adapter := &Adapter{baseURL: "https://fixture.test", client: &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "POST" || r.URL.Path != tc.path {
					t.Fatal("wrong reviewed operation", r.Method, r.URL.Path)
				}
				var payload struct {
					Model      string         `json:"model"`
					Input      map[string]any `json:"input"`
					Parameters map[string]any `json:"parameters"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload.Model != tc.id {
					t.Fatal("regional or third-party model ID rewritten", payload.Model)
				}
				if strings.HasPrefix(tc.id, "vidu/viduq") {
					for _, item := range payload.Input["media"].([]any) {
						if item.(map[string]any)["type"] != "image" {
							t.Fatal("Vidu expects image assets")
						}
					}
					if tc.options.Ratio == "9:16" && payload.Parameters["size"] != "1080*1920" {
						t.Fatal("wrong Vidu portrait dimensions")
					}
					if tc.options.Ratio == "adaptive" && payload.Parameters["size"] != nil {
						t.Fatal("first-frame video must follow the input aspect ratio")
					}
				}
				if strings.Contains(tc.id, "vace") && payload.Input["function"] != "image_reference" {
					t.Fatal("VACE used an unreviewed workflow")
				}
				if strings.HasPrefix(tc.id, "qwen-mt") && (payload.Input["source_lang"] != "auto" || payload.Input["target_lang"] != "en" || payload.Input["image_url"] == nil) {
					t.Fatal("translation languages or source missing")
				}
				if isBatchSpeech(tc.id) {
					if payload.Input["voice"] != "reviewed-user-voice" || payload.Input["format"] != "wav" || payload.Input["sample_rate"] != float64(24000) || r.Header.Get("X-DashScope-Async") != "" {
						t.Fatal("wrong batch speech request")
					}
					return dashJSONResponse(200, `{"request_id":"speech","output":{"audio":{"url":"https://example.test/audio.wav"}},"usage":{"characters":8}}`), nil
				}
				if r.Header.Get("X-DashScope-Async") != "enable" {
					t.Fatal("asynchronous file generation header missing")
				}
				return dashJSONResponse(200, `{"output":{"task_id":"reviewed-task"}}`), nil
			})}}
			task, err := adapter.Generate(context.Background(), core.MediaRequest{Model: tc.id, Prompt: "test", Options: tc.options})
			if err != nil {
				t.Fatal(err)
			}
			if isBatchSpeech(tc.id) && task.ResultURL == "" {
				t.Fatal("audio URL was not archived by the normal immediate-result path")
			}
		})
	}
}

func TestNewVersionsStayUnreviewedAndRetiredModelsStayUnpriced(t *testing.T) {
	for _, id := range []string{"qwen-image-2.1-pro-2099-01-01", "qwen3-tts-future", "vidu/viduq9-mix_reference2video", "qwen-image", "qwen3-tts-instruct-flash"} {
		if _, ok := describeDashModel(id, id); ok {
			t.Fatal("unknown or retired model admitted", id)
		}
		if _, err := quotePublic("https://dashscope-intl.aliyuncs.com/api/v1", core.MediaModel{MediaType: "image"}, core.MediaRequest{Model: id}, time.Now()); !errors.Is(err, core.ErrPricingUnavailable) {
			t.Fatal("unknown or retired model inherited a price", id)
		}
	}
}

func TestRegionalAudioAndReferencePricing(t *testing.T) {
	for _, tc := range []struct {
		id, endpoint, res, want string
		options                 core.MediaOptions
	}{
		{"wan2.6-i2v-flash", "https://dashscope-intl.aliyuncs.com/api/v1", "720p", "0.125000", core.MediaOptions{Duration: 5}},
		{"qwen-image-3.0-pro", "https://dashscope-intl.aliyuncs.com/api/v1", "1024*1024", "0.046000", core.MediaOptions{ImageURLs: []string{"a", "b"}}},
		{"wan2.6-r2v", "https://dashscope-intl.aliyuncs.com/api/v1", "720p", "0.750000", core.MediaOptions{Duration: 5, ImageURLs: []string{"a"}, VideoURL: "https://example.test/v.mp4"}},
		{"wan2.7-r2v", "https://dashscope-intl.aliyuncs.com/api/v1", "720p", "1.000000", core.MediaOptions{Duration: 5, ImageURLs: []string{"a"}, VideoURL: "https://example.test/v.mp4"}},
		{"wan2.7-i2v", "https://dashscope-intl.aliyuncs.com/api/v1", "720p", "0.500000", core.MediaOptions{Duration: 5, VideoURL: "https://example.test/v.mp4"}},
		{"qwen-audio-3.0-tts-plus", "https://dashscope-intl.aliyuncs.com/api/v1", "", "0.000160", core.MediaOptions{}},
	} {
		model, ok := describeDashModel(tc.id, tc.id)
		if !ok {
			t.Fatal(tc.id)
		}
		tc.options.Resolution = tc.res
		cost, err := quotePublic(tc.endpoint, model, core.MediaRequest{Model: tc.id, Prompt: "中A文123", Options: tc.options}, time.Now())
		if err != nil || cost.ExpectedUSD.String() != tc.want {
			t.Fatal(tc.id, cost.ExpectedUSD, err)
		}
	}
}

func TestOmittedFlashAudioPreferenceMatchesSilentQuote(t *testing.T) {
	r := core.MediaRequest{Model: "wan2.6-i2v-flash", Options: core.MediaOptions{Duration: 5, Resolution: "720p", ImageURLs: []string{"https://example.test/first.png"}}}
	payload := videoPayload(r).(map[string]any)
	if payload["parameters"].(map[string]any)["audio"] != false {
		t.Fatal("provider default audio would contradict the silent quote")
	}
	enabled := true
	r.Options.GenerateAudio = &enabled
	if videoPayload(r).(map[string]any)["parameters"].(map[string]any)["audio"] != true {
		t.Fatal("explicit audio preference lost")
	}
	model, _ := describeDashModel(r.Model, r.Model)
	quote, err := quotePublic("https://dashscope-intl.aliyuncs.com/api/v1", model, r, time.Now())
	if err != nil || quote.ExpectedUSD.String() != "0.250000" {
		t.Fatal("audible tariff was collapsed into silent tariff", quote.ExpectedUSD, err)
	}
}

func TestViduAudioPreferenceAlwaysMatchesQuote(t *testing.T) {
	for _, id := range []string{"vidu/viduq3-ad_reference2video", "vidu/viduq3-mix_reference2video", "vidu/viduq2-pro-fast_img2video"} {
		model, _ := describeDashModel(id, id)
		r := core.MediaRequest{Model: id, Options: core.MediaOptions{Duration: 5, Resolution: "720p", Ratio: "16:9", ImageURLs: []string{"https://example.test/frame.png"}}}
		for _, audio := range []bool{false, true} {
			if audio {
				r.Options.GenerateAudio = &audio
			}
			payload := videoPayload(r).(map[string]any)
			if payload["parameters"].(map[string]any)["audio"] != audio {
				t.Fatal("Vidu audio preference depends on a provider default", id, audio)
			}
			cost, err := quotePublic("https://dashscope-intl.aliyuncs.com/api/v1", model, r, time.Now())
			if err != nil || cost.ExpectedUSD <= 0 {
				t.Fatal("Vidu audio tariff missing", id, audio, err)
			}
		}
	}
	drama := videoPayload(core.MediaRequest{Model: "vidu/viduq3-drama_reference2video"}).(map[string]any)
	if _, ok := drama["parameters"].(map[string]any)["audio"]; ok {
		t.Fatal("always-audible Vidu drama must not expose an audio toggle")
	}
}

func TestReviewedVideoRatiosHaveWireDimensions(t *testing.T) {
	for _, id := range []string{"vidu/viduq3-ad_reference2video", "vidu/viduq3-drama_reference2video", "vidu/viduq3-mix_reference2video", "wan2.1-vace-plus", "wanx2.1-vace-plus"} {
		model, _ := describeDashModel(id, id)
		for _, res := range model.Resolutions {
			for _, ratio := range model.Ratios {
				payload := videoPayload(core.MediaRequest{Model: id, Options: core.MediaOptions{Resolution: res, Ratio: ratio}}).(map[string]any)
				size, _ := payload["parameters"].(map[string]any)["size"].(string)
				if !strings.Contains(size, "*") {
					t.Fatal("advertised aspect ratio has no supported wire size", id, res, ratio, size)
				}
			}
		}
	}
}
