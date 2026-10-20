package alibaba

import (
	"context"
	"fmt"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// Native contracts follow the reviewed model and request specifications registered by this module.
func synchronousImage(id string) bool {
	return (strings.HasPrefix(id, "qwen-image") && !strings.HasPrefix(id, "qwen-image-3.0") && !strings.HasPrefix(id, "qwen-image-2.1")) || id == "z-image-turbo"
}

func supportsAudioToggle(id string) bool {
	return strings.HasPrefix(id, "wan3.0-") || id == "wan2.6-i2v-flash" || id == "wan2.6-r2v-flash" || strings.HasPrefix(id, "vidu/") && !strings.Contains(id, "-drama_")
}

func animatePayload(r core.MediaRequest) any {
	input := map[string]any{"image_url": r.Options.ImageURLs[0], "video_url": r.Options.VideoURL}
	if r.Options.Watermark != nil {
		input["watermark"] = *r.Options.Watermark
	}
	return map[string]any{"model": r.Model, "input": input, "parameters": map[string]any{"mode": "wan-std"}}
}

func applyModelContract(m *core.MediaModel) {
	defer func() {
		m.ReferenceModes = referenceModes(*m)
		if strings.HasPrefix(m.ID, "wan3.0-") || strings.HasPrefix(m.ID, "wan2.7-") && strings.Contains(m.ID, "r2v") {
			m.ReferencePromptSyntax = "numbered"
		}
	}()
	id := contractID(m.ID)
	if m.MediaType == "image" {
		m.Formats = []string{"png"}
		if strings.HasPrefix(id, "qwen-mt-image") {
			m.Formats = []string{"jpeg"}
		}
		if id == "z-image-turbo" {
			m.SupportsNegativePrompt = false
			m.NoWatermark = true
			m.Resolutions = []string{"1024*1024", "1024*1536", "1536*1024"}
		}
		if id == "qwen-image-edit" {
			m.Resolutions = nil
		}
		if id == "wanx2.1-imageedit" {
			m.Resolutions = nil
			m.SupportsNegativePrompt = false
			m.NoWatermark = true
		}
		return
	}
	if m.MediaType != "video" {
		return
	}
	m.MaxSeed = 2147483647
	m.AudioAlwaysOn = m.SupportsAudio && !supportsAudioToggle(id)
	if strings.HasPrefix(id, "happyhorse-") {
		m.SupportsNegativePrompt = false
		m.SupportsAudioInput = false
		m.SupportsVideoInput = false
		if strings.Contains(id, "-i2v") {
			m.MaxImageInputs = 1
			m.Ratios = []string{"adaptive"}
		}
		if strings.Contains(id, "-r2v") {
			m.RequiresImageInput = true
			m.MinImageInputs = 1
			m.MaxImageInputs = 9
		}
		if strings.Contains(id, "video-edit") {
			m.SupportsVideoInput = true
			m.MaxImageInputs = 5
			m.DurationFromInput = true
			m.Ratios = []string{"adaptive"}
		}
	}
	if strings.HasPrefix(id, "wan2.") {
		m.SupportsAudioInput = false
		m.SupportsVideoInput = m.RequiresVideoInput
		if strings.Contains(id, "-i2v") || strings.Contains(id, "-kf2v") {
			m.Ratios = []string{"adaptive"}
			m.MaxImageInputs = 1
		}
		if strings.Contains(id, "-kf2v") {
			m.MaxImageInputs = 2
		}
		if strings.HasPrefix(id, "wan2.5-i2v") || strings.HasPrefix(id, "wan2.6-i2v") || strings.HasPrefix(id, "wan2.5-t2v") || strings.HasPrefix(id, "wan2.6-t2v") {
			m.SupportsAudioInput = true
		}
		if strings.HasPrefix(id, "wan2.7-i2v") {
			m.SupportsAudioInput = true
			m.SupportsVideoInput = true
			m.RequiresImageInput = false
			m.MinImageInputs = 0
			m.MaxImageInputs = 2
		}
		if strings.Contains(id, "-r2v") {
			m.SupportsVideoInput = true
			m.MaxImageInputs = 5
			if strings.HasPrefix(id, "wan2.7-") {
				m.MaxImageInputs = 6
			}
			m.Ratios = []string{"16:9", "9:16", "1:1", "4:3", "3:4"}
		}
		if strings.Contains(id, "-t2v") && !strings.HasPrefix(id, "wan2.7-") {
			m.Ratios = []string{"16:9", "9:16", "1:1"}
			if !strings.Contains(id, "2.5") {
				m.Ratios = append(m.Ratios, "4:3", "3:4")
			}
		}
		if strings.HasPrefix(id, "wan2.6-") || strings.HasPrefix(id, "wan2.7-") {
			m.Resolutions = []string{"720p", "1080p"}
		}
		if strings.HasPrefix(id, "wan2.5-") {
			m.MinDuration = 5
			m.MaxDuration = 10
			m.DurationStep = 5
		}
		if strings.HasPrefix(id, "wan2.1-") || strings.HasPrefix(id, "wan2.2-") {
			m.MinDuration = 5
			m.MaxDuration = 5
		}
	}
	switch id {
	case "wan2.1-vace-plus":
		m.SupportsVideoInput = false
		m.RequiresVideoInput = false
		m.SupportsImageInput = true
		m.RequiresImageInput = true
		m.MinImageInputs = 1
		m.MaxImageInputs = 3
	case "wan2.2-s2v":
		m.Resolutions = []string{"480p", "720p"}
		m.SupportsAudioInput = true
		m.DurationFromInput = true
		m.MaxDuration = 20
		m.MinDuration = 1
		m.Ratios = []string{"adaptive"}
	case "wan2.2-i2v-flash":
		m.Resolutions = []string{"480p", "720p"}
		m.MaxDuration = 5
	case "wan2.2-i2v-plus":
		m.Resolutions = []string{"480p", "1080p"}
	case "wan2.1-i2v-turbo":
		m.MinDuration = 3
		m.Resolutions = []string{"480p", "720p"}
	case "wan2.1-i2v-plus", "wan2.1-kf2v-plus":
		m.Resolutions = []string{"720p"}
	case "wan2.2-kf2v-flash":
		m.MaxDuration = 5
	case "wan2.7-videoedit":
		m.DurationFromInput = true
		m.Ratios = []string{"adaptive"}
		m.MaxImageInputs = 4
	case "wan2.2-animate-move", "wan2.2-animate-mix":
		m.MinDuration = 2
		m.MaxDuration = 30
		m.DurationFromInput = true
		m.MaxImageInputs = 1
		m.NoSeed = true
		m.Resolutions = nil
		m.Ratios = []string{"adaptive"}
		m.SupportsNegativePrompt = false
	}
}

func (a *Adapter) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	if core.IsManualCall(ctx) {
		return nil
	}
	bad := func(message string) error { return fmt.Errorf("%w: %s", core.ErrOptionsInvalid, message) }
	id, o := r.Model, r.Options
	if id == "wan2.2-s2v" && (len(o.ImageURLs) != 1 || len(o.Audios()) != 1) {
		return bad("digital human generation requires one compliant portrait image and one audio file shorter than 20 seconds")
	}
	if strings.HasPrefix(id, "qwen-mt-image") {
		if len(o.ImageURLs) != 1 {
			return bad("image translation requires exactly one source image")
		}
		target, _ := o.Parameters["target_lang"].(string)
		source, _ := o.Parameters["source_lang"].(string)
		if strings.TrimSpace(target) == "" || strings.EqualFold(source, target) {
			return bad("specify a target language different from the source language")
		}
		if id == "qwen-mt-image" && !strings.EqualFold(source, "en") && !strings.EqualFold(source, "english") && !strings.EqualFold(source, "zh") && !strings.EqualFold(source, "chinese") && !strings.EqualFold(target, "en") && !strings.EqualFold(target, "english") && !strings.EqualFold(target, "zh") && !strings.EqualFold(target, "chinese") {
			return bad("this translation version requires Chinese or English as source or target")
		}
	}
	for _, m := range mediaModels(true) {
		if m.ID == id {
			if err := core.ValidateReferenceMode(m, o); err != nil {
				return err
			}
			break
		}
	}
	if strings.Contains(id, "animate") && (len(o.ImageURLs) != 1 || o.VideoURL == "") {
		return bad("animation requires one image and one video")
	}
	if strings.Contains(id, "-r2v") && len(o.ImageURLs) == 0 && o.VideoURL == "" {
		return bad("reference-to-video requires a reference image or video")
	}
	if strings.HasPrefix(id, "wan2.6-r2v") && len(o.ImageURLs)+len(o.Videos()) > 5 {
		return bad("at most five image and video references combined")
	}
	if strings.HasPrefix(id, "wan2.7-i2v") {
		if len(o.ImageURLs) == 0 && o.VideoURL == "" {
			return bad("provide a first frame or a video to extend")
		}
		if o.VideoURL != "" && len(o.ImageURLs) > 1 {
			return bad("video continuation accepts at most one last-frame image")
		}
	}
	if id == "wan2.7-image-pro" && len(o.ImageURLs) > 0 && o.Resolution == "4096*4096" {
		return bad("4K is supported for text-to-image only")
	}
	if (strings.HasPrefix(id, "wan2.1-t2v") || strings.HasPrefix(id, "wan2.2-t2v") || strings.HasPrefix(id, "wan2.5-t2v")) && o.Resolution == "480p" && (o.Ratio == "4:3" || o.Ratio == "3:4") {
		return bad("480p does not support 4:3 or 3:4")
	}
	return nil
}
func boolCount(v bool) int {
	if v {
		return 1
	}
	return 0
}
