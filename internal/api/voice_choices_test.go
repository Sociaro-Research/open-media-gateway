package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
)

func TestKlingTTSExistingVoiceLanguageMetadataOnPublicModels(t *testing.T) {
	adapter, err := kling.New("fixture-key", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	service := core.NewTaskServiceWithProviders(&admissionRepository{}, &admissionQueue{}, map[string]core.MediaProvider{"kling": adapter, "mock": mock.New(time.Second, "")}, map[string]string{"kling": kling.TextToSpeech, "mock": "mock-video-v1"}, "kling")
	h := New(service, healthStub{}, "secret")
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Data []struct {
			ID           string          `json:"id"`
			Capabilities core.MediaModel `json:"capabilities"`
		}
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range response.Data {
		if row.ID != "kling/"+kling.TextToSpeech {
			if len(row.Capabilities.VoiceChoices) != 0 {
				t.Fatal("unrelated provider behavior changed", row.ID)
			}
			continue
		}
		found = true
		m := row.Capabilities
		if len(m.Voices) != 46 || len(m.VoiceChoices) != 46 {
			t.Fatal("legacy voice catalogue incomplete", len(m.Voices), len(m.VoiceChoices))
		}
		pairs, defaults := 0, 0
		seen := map[string]bool{}
		for i, c := range m.VoiceChoices {
			if c.ID != m.Voices[i] || seen[c.ID] || len(c.Languages) == 0 {
				t.Fatal("choices differ from existing voices", c)
			}
			seen[c.ID] = true
			defaultAllowed := false
			for _, lang := range c.Languages {
				pairs++
				if lang == c.DefaultLanguage {
					defaultAllowed = true
				}
				req := core.MediaRequest{Model: kling.TextToSpeech, Prompt: "Reviewed existing control", Options: core.MediaOptions{Voice: c.ID, Language: lang}}
				if err = adapter.ValidateRequest(context.Background(), req); err != nil {
					t.Fatal("advertised pair rejected", c.ID, lang, err)
				}
			}
			if !defaultAllowed {
				t.Fatal("default outside declared voice languages", c)
			}
			if c.IsDefault {
				defaults++
				if c.ID != "oversea_male1" || c.DefaultLanguage != "en" {
					t.Fatal("existing valid default changed", c)
				}
			}
		}
		if pairs != 62 || defaults != 1 {
			t.Fatal("invalid pair/default metadata", pairs, defaults)
		}
	}
	if !found {
		t.Fatal("TTS missing from actual public models response")
	}
}
