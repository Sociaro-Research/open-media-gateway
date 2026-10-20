package sonilo

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

func (a *Adapter) ValidateRequest(_ context.Context, r core.MediaRequest) error {
	op, ok := findOperation(r.Model)
	if !ok {
		return fmt.Errorf("%w: unsupported Sonilo operation", core.ErrModelUnavailable)
	}
	m := modelFor(r.Model)
	o := r.Options
	invalid := func(message string) error { return fmt.Errorf("%w: %s", core.ErrOptionsInvalid, message) }
	if len([]rune(r.Prompt)) > 2000 || !m.PromptOptional && strings.TrimSpace(r.Prompt) == "" {
		return invalid("Sonilo text prompts require 1-2000 characters")
	}
	if op.endpoint == "audio-ducking" && r.Prompt != "" {
		return invalid("audio ducking uses foreground and background media, without a prompt")
	}
	if o.Resolution != "" || o.Ratio != "" || o.Quality != "" || o.Background != "" || o.Seed != nil || o.Watermark != nil || o.Draft != nil || o.CameraFixed != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || len(o.ImageURLs) > 0 || len(o.ReferenceVoices) > 0 || o.Lyrics != "" || o.Gender != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" {
		return invalid("unsupported Sonilo media control")
	}
	if o.GenerateAudio != nil && (!m.AudioAlwaysOn || !*o.GenerateAudio) {
		return invalid("Sonilo video output always includes generated audio")
	}
	if o.Format != "" && !slices.Contains(m.Formats, o.Format) {
		return invalid("unsupported Sonilo output format")
	}
	if err := core.ValidateReferenceMode(m, o); err != nil {
		return err
	}
	if o.VideoURL != "" && len(o.VideoURLs) > 0 && o.VideoURL != o.VideoURLs[0] || o.AudioURL != "" && len(o.AudioURLs) > 0 && o.AudioURL != o.AudioURLs[0] {
		return invalid("conflicting media references")
	}
	if m.SupportsVideoInput && len(o.Videos()) != 1 || !m.SupportsVideoInput && len(o.Videos()) != 0 || m.SupportsAudioInput && len(o.Audios()) != 1 || !m.SupportsAudioInput && len(o.Audios()) != 0 {
		return invalid("provide exactly one source for each required input")
	}
	for _, s := range append(o.Videos(), o.Audios()...) {
		if !publicURL(s) {
			return invalid("Sonilo requires a public HTTP or HTTPS media URL")
		}
	}
	if op.id == "text-to-music" {
		if d := duration(o); d < 5 || d > 360 {
			return invalid("music duration must be 5-360 seconds")
		}
	} else if o.Duration != 0 {
		return invalid("duration follows the source; text effects use parameters.duration_seconds")
	}
	p, err := core.NormalizeParameters(m.Parameters, o.Parameters)
	if err != nil {
		return err
	}
	if op.endpoint == "audio-ducking" && !publicURL(p["music_url"].(string)) {
		return invalid("background music requires a public HTTP or HTTPS URL")
	}
	if timeline, ok := p["segments"].(string); ok && timeline != "" {
		maxDuration := float64(op.maxDuration)
		if op.id == "text-to-music" {
			maxDuration = float64(duration(o))
		}
		if err := validateSegments(timeline, strings.Contains(op.id, "music"), maxDuration); err != nil {
			return invalid(err.Error())
		}
	}
	return nil
}

// Music uses start/label/prompt boundaries; SFX uses contiguous start/end/prompt
// spans. The public OpenAPI's generic Segment schema conflates these contracts.
func validateSegments(value string, music bool, maxDuration float64) error {
	var segments []map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &segments) != nil || len(segments) < 1 || len(segments) > 30 {
		return fmt.Errorf("timeline requires a JSON array of 1-30 segments")
	}
	previousStart, previousEnd := -5.0, 0.0
	for i, s := range segments {
		allowed := map[string]bool{"start": true, "prompt": true}
		if music {
			allowed["label"] = true
		} else {
			allowed["end"] = true
		}
		for key := range s {
			if !allowed[key] {
				return fmt.Errorf("unsupported timeline field")
			}
		}
		var start, end float64
		var prompt string
		if json.Unmarshal(s["start"], &start) != nil || string(s["start"]) == "null" || math.IsNaN(start) || math.IsInf(start, 0) || start < 0 || i == 0 && start != 0 || json.Unmarshal(s["prompt"], &prompt) != nil || len([]rune(strings.TrimSpace(prompt))) < 1 || len([]rune(prompt)) > 200 {
			return fmt.Errorf("timeline requires finite start times and 1-200 character prompts, starting at zero")
		}
		if music {
			if start-previousStart < 5 || start > maxDuration-5 {
				return fmt.Errorf("music boundaries require five-second spacing and room before the end")
			}
			if raw, ok := s["label"]; ok {
				var label string
				if json.Unmarshal(raw, &label) != nil || !slices.Contains([]string{"intro", "verse", "pre-chorus", "chorus", "bridge", "break", "silence", "outro", "none"}, label) {
					return fmt.Errorf("unsupported music segment label")
				}
			}
		} else if json.Unmarshal(s["end"], &end) != nil || string(s["end"]) == "null" || math.IsNaN(end) || math.IsInf(end, 0) || end <= start || end > maxDuration || start != previousEnd {
			return fmt.Errorf("sound effect segments must be contiguous with end after start within the video limit")
		}
		previousStart, previousEnd = start, end
	}
	return nil
}
