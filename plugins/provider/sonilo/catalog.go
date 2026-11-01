package sonilo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

type operation struct {
	id, name, media, endpoint, service, rate, priceField string
	maxDuration, floor                                   int
}

var operations = []operation{
	{"text-to-music", "Sonilo Text to Music", "music", "text-to-music", "text_to_music", "0.00225", "", 360, 10},
	{"text-to-sfx", "Sonilo Text to Sound Effects", "audio", "text-to-sfx", "text_to_sfx", "0.0018", "", 180, 3},
	{"video-to-music", "Sonilo Video to Music", "music", "video-to-music", "video_to_music", "0.009", "", 360, 10},
	{"video-to-sfx", "Sonilo Video to Sound Effects", "audio", "video-to-sfx", "video_to_sfx", "0.009", "", 480, 3},
	{"video-to-video-music", "Sonilo Video with Music", "video", "video-to-video-music", "video_to_video_music", "", "video_music_price", 360, 10},
	{"video-to-video-sfx", "Sonilo Video with Sound Effects", "video", "video-to-video-sfx", "video_to_video_sfx", "", "video_sfx_price", 480, 3},
	{"video-to-sound", "Sonilo Combined Music and Sound Effects", "audio", "video-to-sound", "video_to_sound", "", "sound_price", 480, 10},
	{"video-to-video-sound", "Sonilo Video with Music and Sound Effects", "video", "video-to-video-sound", "video_to_video_sound", "", "video_sound_price", 480, 10},
	{"audio-ducking", "Sonilo Narration and Music Mix", "audio", "audio-ducking", "audio_ducking", "0.0006", "", 360, 10},
	{"video-ducking", "Sonilo Video Narration and Music Mix", "video", "audio-ducking", "audio_ducking", "0.0006", "", 360, 10},
}

func findOperation(id string) (operation, bool) {
	for _, o := range operations {
		if o.id == id {
			return o, true
		}
	}
	return operation{}, false
}
func bound(n float64) *float64 { return &n }
func boolean(key, label string) core.ParameterField {
	return core.ParameterField{Key: key, Label: label, Type: "boolean", Default: false}
}

func catalog() []core.MediaModel {
	models := make([]core.MediaModel, 0, len(operations))
	for _, op := range operations {
		m := core.MediaModel{ID: op.id, DisplayName: op.name, MediaType: op.media, NoSeed: true, NoWatermark: true, NoLanguage: true, NoInstructions: true, MaxDuration: op.maxDuration,
			Pricing: core.MediaModelPricing{Known: op.rate != "", ReferenceDescription: "Standard per-second estimate with a minimum billed duration; account tariffs and trials may differ."}}
		m.PromptOptional = op.id != "text-to-music" && op.id != "text-to-sfx"
		m.DurationFromInput = m.PromptOptional
		switch op.id {
		case "text-to-music":
			m.MinDuration = 5
			m.DefaultDuration = 30
			m.Formats = []string{"m4a", "wav", "mp3"}
		case "text-to-sfx":
			// Fractional effects use the existing schema number control.
			m.MaxDuration = 0
			m.Formats = []string{"aac", "wav", "mp3", "flac"}
			m.Parameters = []core.ParameterField{{Key: "duration_seconds", Label: "Duration (s)", Type: "number", Default: 8, Min: bound(0.5), Max: bound(180)}}
		case "audio-ducking", "video-ducking":
			m.NoPrompt = true
			m.SupportsAudioInput = true
			m.RequiresAudioInput = true
			m.Parameters = []core.ParameterField{{Key: "music_url", Label: "Background music URL", Type: "text", Required: true, MaxLength: 4096}}
			if op.id == "video-ducking" {
				m.SupportsVideoInput = true
				m.RequiresVideoInput = true
				m.RequiresAudioInput = false
				m.SupportsAudioInput = false
			}
		default:
			m.SupportsVideoInput = true
			m.RequiresVideoInput = true
		}
		if op.media == "video" {
			m.SupportsAudio = true
			m.AudioAlwaysOn = true
		}
		if strings.Contains(op.id, "music") {
			m.Parameters = append(m.Parameters, core.ParameterField{Key: "segments", Label: "Music timeline (JSON, optional)", Type: "text", MaxLength: 16384})
			if op.id != "text-to-music" {
				m.Parameters = append(m.Parameters, boolean("preserve_speech", "Preserve speech"), boolean("ducking", "Duck music under speech"), core.ParameterField{Key: "prompt_influence", Label: "Prompt influence", Type: "number", Min: bound(0), Max: bound(1), Default: 0.5})
				if op.media == "video" {
					m.Parameters = append(m.Parameters, boolean("keep_original_sound", "Keep original sound"))
				} else {
					m.Formats = []string{"m4a", "wav", "mp3"}
				}
			}
		} else if strings.HasSuffix(op.id, "sfx") || strings.HasSuffix(op.id, "sound") {
			if op.id != "text-to-sfx" {
				m.Parameters = append(m.Parameters, core.ParameterField{Key: "segments", Label: "Sound effects timeline (JSON, optional)", Type: "text", MaxLength: 16384})
			}
			if strings.HasSuffix(op.id, "sound") {
				m.Parameters = append(m.Parameters, core.ParameterField{Key: "sfx_prompt", Label: "Sound effects direction (optional)", Type: "text", MaxLength: 2000}, boolean("preserve_speech", "Preserve speech"), boolean("ducking", "Duck music under speech"))
				if op.media == "video" {
					m.Parameters = append(m.Parameters, boolean("keep_original_sound", "Keep original sound"))
				} else {
					m.Formats = []string{"wav", "m4a", "mp3"}
				}
			} else if op.id == "video-to-sfx" {
				m.Formats = []string{"aac", "wav", "mp3", "flac"}
			}
		}
		if !m.NoPrompt {
			m.MaxPromptCharacters = promptMaxCharacters
		}
		models = append(models, m)
	}
	return models
}

// Account service discovery verifies credentials and filters the native catalog.
// Failure never substitutes an unauthenticated fallback catalog.
func (a *Adapter) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Since(a.modelsAt) < 5*time.Minute {
		return cloneModels(a.models), nil
	}
	data, err := a.do(ctx, "GET", "/account/services", nil, "")
	if err != nil {
		return nil, err
	}
	var response struct {
		Services *[]string `json:"available_services"`
	}
	if json.Unmarshal(data, &response) != nil || response.Services == nil {
		return nil, errors.New("Sonilo account services response is malformed")
	}
	enabled := map[string]bool{}
	for _, id := range *response.Services {
		enabled[strings.ReplaceAll(id, "-", "_")] = true
	}
	models := []core.MediaModel{}
	for _, m := range catalog() {
		op, _ := findOperation(m.ID)
		if enabled[op.service] {
			models = append(models, m)
		}
	}
	a.models = models
	a.modelsAt = time.Now()
	return cloneModels(models), nil
}
func cloneModels(models []core.MediaModel) []core.MediaModel {
	data, _ := json.Marshal(models)
	var result []core.MediaModel
	_ = json.Unmarshal(data, &result)
	return result
}
