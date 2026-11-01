package modelcatalog

import (
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"strings"
	"testing"
)

func TestReferenceRecommendationEvidenceAndOldCatalogues(t *testing.T) {
	for _, p := range []struct{ provider, id string }{{"bytedance", "dreamina-seedance-2-5-260628"}, {"alibaba", "wan3.0-video"}, {"agnes", "agnes-video-2.5"}, {"kling", "kling-4.0-image-to-video"}} {
		var old core.MediaModel
		if err := json.Unmarshal([]byte(`{"id":"`+p.id+`","media_type":"video"}`), &old); err != nil {
			t.Fatal(err)
		}
		m := Enrich(p.provider, old)
		if m.ReferencePromptRecommendation == nil {
			t.Fatal(p)
		}
		b, _ := json.Marshal(m)
		var round core.MediaModel
		if json.Unmarshal(b, &round) != nil || round.ReferencePromptRecommendation.SourceURL == "" {
			t.Fatal(string(b))
		}
		m.ReferencePromptRecommendation.Modes[0] = "changed"
		if Enrich(p.provider, old).ReferencePromptRecommendation.Modes[0] == "changed" {
			t.Fatal("cross-account mutable hint")
		}
		if old.ReferencePromptRecommendation != nil {
			t.Fatal("saved descriptor changed")
		}
	}
	for provider, ids := range map[string][]string{"alibaba": {"qwen-tts", "qwen-tts-latest", "qwen-tts-2025-04-10", "qwen-tts-2025-05-22", "happyhorse-1.0-i2v", "happyhorse-1.0-r2v", "wan2.1-t2i-plus", "wan2.1-t2i-turbo", "wan2.6-i2v-us", "wan2.6-t2v-us", "wan2.7-i2v", "wan2.7-t2v-2026-04-25"}, "bytedance": {"seedream-4-0-20260415", "seedance-1-0-lite-i2v-250428", "seedance-1-0-lite-t2v-250428"}, "openai": {"tts-1-1106", "tts-1-hd-1106"}} {
		for _, id := range ids {
			if ReferenceRecommendation(provider, id) != nil {
				t.Fatal("unconfirmed ID given advice", id)
			}
		}
	}
}
func TestReferenceRecommendationEndpointParsing(t *testing.T) {
	id := "dreamina-seedance-2-5-260628"
	hint := ReferenceRecommendation("bytedance", id)
	block := func(model, prompt string) string {
		return "```json\n{\"model\":\"" + model + "\",\"content\":[{\"type\":\"text\",\"text\":\"" + prompt + "\"}]}\n```"
	}
	for _, tc := range []struct {
		name, source, text string
		want               bool
	}{
		{"exact", hint.SourceURL, block(id, "Use @Image1"), true},
		{"video", hint.SourceURL, block(id, "Extend @Video 1"), true},
		{"wrong model", hint.SourceURL, id + block("other-model", "Use @Image1"), false},
		{"omitted", hint.SourceURL, block(id, "no advice"), false},
		{"guide", "https://ai.byteplus.com/ark/region:ap-southeast-1/docs/seedance-2-5", block(id, "@Image1"), false},
		{"unscoped", hint.SourceURL, "Request body " + id + " @Image1", false},
		{"cross example", hint.SourceURL, block(id, "no advice") + block("other-model", "@Image1"), false},
		{"two model bindings", hint.SourceURL, "```json\n{\"model\":\"" + id + "\",\"text\":\"@Image1\"} {\"model\":\"other\"}\n```", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParsedReferenceRecommendations("bytedance", tc.source, tc.text)
			if (got[id] != nil) != tc.want {
				t.Fatal(got)
			}
			if tc.want && tc.name == "exact" && strings.Contains(got[id].Text, "@Video") {
				t.Fatal("unobserved example claimed", got[id])
			}
		})
	}
}
