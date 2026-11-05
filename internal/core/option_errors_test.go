package core

import (
	"errors"
	"strings"
	"testing"
)

func TestModelOptionDiagnostics(t *testing.T) {
	video := MediaModel{MediaType: "video", Resolutions: []string{"720p"}, Ratios: []string{"16:9"}, MinDuration: 5, MaxDuration: 15, DefaultDuration: 5, DurationStep: 5}
	audio := MediaModel{MediaType: "audio", MinDuration: 10, MaxDuration: 30, DefaultDuration: 10, DurationStep: 10}
	for _, tc := range []struct {
		name, field, hint string
		model             MediaModel
		options           MediaOptions
	}{
		{"voice", "voice", "Alice, Bob", MediaModel{MediaType: "speech", SupportsVoice: true, Voices: []string{"Alice", "Bob"}}, MediaOptions{Voice: "private-rejected-value"}},
		{"format", "format", "mp3, wav", MediaModel{MediaType: "speech", Formats: []string{"mp3", "wav"}}, MediaOptions{Format: "private-rejected-value"}},
		{"quality", "quality", "low, high", MediaModel{MediaType: "image", Qualities: []string{"low", "high"}}, MediaOptions{Quality: "private-rejected-value"}},
		{"unsupported-quality", "quality", "omit it", MediaModel{MediaType: "image"}, MediaOptions{Quality: "private-rejected-value"}},
		{"background", "background", "opaque, transparent", MediaModel{MediaType: "image", Backgrounds: []string{"opaque", "transparent"}}, MediaOptions{Background: "private-rejected-value"}},
		{"video-duration-min", "duration", "between 5 and 15 seconds", video, MediaOptions{Duration: 4}},
		{"video-duration-max", "duration", "between 5 and 15 seconds", video, MediaOptions{Duration: 16}},
		{"video-duration-step", "duration", "increments of 5 starting at 5", video, MediaOptions{Duration: 7}},
		{"audio-duration-step", "duration", "increments of 10 starting at 10", audio, MediaOptions{Duration: 15}},
		{"ratio", "ratio", "16:9", video, MediaOptions{Ratio: "private-rejected-value"}},
		{"resolution", "resolution", "720p", video, MediaOptions{Resolution: "private-rejected-value"}},
		{"audio-toggle", "generate_audio", "false or omitted", video, MediaOptions{GenerateAudio: new(true)}},
		{"draft", "draft", "false or omitted", video, MediaOptions{Draft: new(true)}},
		{"camera", "camera_fixed", "omit it", video, MediaOptions{CameraFixed: new(false)}},
		{"last-frame", "return_last_frame", "false or omitted", video, MediaOptions{ReturnLastFrame: new(true)}},
		{"seed", "seed", "4294967295", video, MediaOptions{Seed: new(int64(-1))}},
		{"negative-prompt", "negative_prompt", "omit it", video, MediaOptions{NegativePrompt: "private-rejected-value"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeModelOptions(tc.model, tc.options)
			var detail *OptionValidationError
			if !errors.Is(err, ErrOptionsInvalid) || !errors.As(err, &detail) || detail.Field != tc.field || !strings.Contains(err.Error(), tc.hint) {
				t.Fatalf("diagnostic = %v; want %s / %s", err, tc.field, tc.hint)
			}
			if strings.Contains(err.Error(), "private-rejected-value") {
				t.Fatal("diagnostic echoed request data")
			}
		})
	}
	for _, model := range []MediaModel{video, audio} {
		for _, duration := range []int{0, model.MinDuration, model.MaxDuration} {
			options, err := NormalizeModelOptions(model, MediaOptions{Duration: duration})
			if err != nil || duration != 0 && options.Duration != duration || duration == 0 && options.Duration != model.DefaultDuration {
				t.Fatalf("valid duration/default changed: %+v, %v", options, err)
			}
		}
		model.DurationFromInput = true
		if _, err := NormalizeModelOptions(model, MediaOptions{Duration: 10}); err == nil || !strings.Contains(err.Error(), "omit it") {
			t.Fatalf("input-derived duration: %v", err)
		}
	}
	inferred := video
	inferred.DurationMayBeInferred = true
	if options, err := NormalizeModelOptions(inferred, MediaOptions{}); err != nil || options.Duration != 0 {
		t.Fatalf("inferred duration changed: %+v, %v", options, err)
	}
}
