package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPublicGenerationHasOneRepresentation(t *testing.T) {
	for _, kind := range []string{"video", "image", "speech"} {
		t.Run(kind, func(t *testing.T) {
			oldURL := "/v1/tasks/job/media"
			view := GenerationView(Task{ID: "job", Provider: "mock", Model: kind, MediaType: kind, Prompt: "text", Status: TaskSucceeded, ResultURL: &oldURL, CreatedAt: time.Unix(123, 0), Options: MediaOptions{Duration: 5, Resolution: "720p", Ratio: "16:9", Voice: "alloy", Format: "png", ImageURLs: []string{"https://example.test/image.png"}, AudioURL: "https://example.test/a.wav", VideoURL: "https://example.test/v.mp4"}})
			b, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			_ = json.Unmarshal(b, &got)
			if got["model"] != "mock/"+kind || got["status"] != "completed" || got["created_at"] != float64(123) {
				t.Fatal(string(b))
			}
			if _, ok := got["result_url"]; ok {
				t.Fatal("duplicate result URL", string(b))
			}
			opts := got["options"].(map[string]any)
			for _, name := range []string{"duration", "resolution", "image_urls", "voice", "audio_url", "video_url"} {
				if _, ok := opts[name]; ok {
					t.Fatal("internal option exposed", name)
				}
			}
			if kind == "speech" {
				if got["input"] != "text" || got["prompt"] != nil {
					t.Fatal("speech alias", string(b))
				}
			}
		})
	}
}

func TestMinimizedGenerationOmitsRequestContent(t *testing.T) {
	webhook := "https://private.example.test/callback"
	for _, mediaType := range []string{"image", "video", "speech", "music"} {
		t.Run(mediaType, func(t *testing.T) {
			view := GenerationView(Task{
				ID: "job", MediaType: mediaType, Prompt: "private prompt", WebhookURL: &webhook,
				MinimizeRequestLogging: true,
				Usage:                  &ProviderUsage{Raw: json.RawMessage(`{"echo":"private provider usage"}`), Metrics: []UsageMetric{{Name: "total_tokens", Unit: "token", Quantity: 3}}},
				Options: MediaOptions{
					Instructions: "private instructions", NegativePrompt: "private negative prompt",
					Lyrics: "private lyrics", Voice: "private voice",
					ImageURLs:  []string{"https://private.example.test/image"},
					VideoURLs:  []string{"https://private.example.test/video"},
					AudioURLs:  []string{"https://private.example.test/audio"},
					Parameters: map[string]any{"private": "private parameter"},
				},
			})
			encoded, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"private prompt", "private instructions", "private negative prompt", "private lyrics", "private voice", "private.example.test", "private parameter", "private provider usage"} {
				if strings.Contains(string(encoded), private) {
					t.Fatalf("request content leaked into serialized generation: %s", encoded)
				}
			}
		})
	}
}
