package alibaba

import (
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func additionalMediaModels() []core.MediaModel {
	var result []core.MediaModel
	for _, id := range []string{"qwen-image-2.1-pro", "qwen-image-2.1-turbo"} {
		m := imageModel(id, id, true, 10, []string{"1024*1024", "1536*1024", "1024*1536", "2048*2048"})
		m.SupportsNegativePrompt = false
		m.MaxSeed = 2147483647
		result = append(result, m)
	}
	for _, id := range []string{"qwen-audio-3.0-tts-plus", "qwen-audio-3.0-tts-flash", "cosyvoice-v3-plus", "cosyvoice-v3-flash", "cosyvoice-v3.5-plus", "cosyvoice-v3.5-flash", "cosyvoice-v2"} {
		m := speechModel(id, id)
		m.Voices = nil
		m.NoSeed = true
		m.NoWatermark = true
		result = append(result, m)
	}
	m := imageModel("vidu/vidu-image_reference2image", "Vidu Image", true, 14, []string{"1024*1024", "2048*2048", "4096*4096"})
	m.SupportsNegativePrompt = false
	m.MaxSeed = 2147483647
	result = append(result, m)
	for _, id := range []string{"vidu/viduq3-ad_reference2video", "vidu/viduq3-drama_reference2video", "vidu/viduq3-mix_reference2video", "vidu/viduq2-pro-fast_img2video"} {
		min, max := 1, 16
		if strings.Contains(id, "-ad_") {
			min, max = 3, 15
		}
		if strings.Contains(id, "-drama_") {
			min, max = 2, 15
		}
		if strings.Contains(id, "img2video") {
			max = 10
		}
		m := videoModel(id, id, min, max, []string{"720p", "1080p"}, true)
		m.Ratios = []string{"16:9", "9:16", "1:1", "4:3", "3:4"}
		m.SupportsImageInput = true
		m.RequiresImageInput = true
		m.MinImageInputs = 1
		m.MaxImageInputs = 7
		m.SupportsNegativePrompt = false
		if strings.Contains(id, "img2video") {
			m.MaxImageInputs = 1
			m.Ratios = []string{"adaptive"}
		}
		if strings.Contains(id, "-drama_") {
			m.Ratios = []string{"16:9", "9:16"}
		}
		result = append(result, m)
	}
	m = imageModel("qwen-mt-image-2.0", "Qwen Image Translation 2.0", true, 1, nil)
	m.RequiresImageInput = true
	m.SupportsNegativePrompt = false
	m.NoSeed = true
	m.NoWatermark = true
	m.Formats = []string{"jpeg"}
	m.PromptOptional = true
	m.Parameters = []core.ParameterField{{Key: "source_lang", Label: "Source language", Type: "text", Default: "auto", MaxLength: 40}, {Key: "target_lang", Label: "Target language", Type: "text", Required: true, MaxLength: 40}}
	result = append(result, m)
	translation := m
	translation.ID = "qwen-mt-image"
	translation.DisplayName = "Qwen Image Translation"
	result = append(result, translation)
	m = videoModel("wan2.1-vace-plus", "Wan 2.1 VACE (image reference)", 5, 5, []string{"720p"}, false)
	m.Ratios = []string{"16:9", "9:16", "1:1", "4:3", "3:4"}
	m.SupportsImageInput = true
	m.RequiresImageInput = true
	m.MinImageInputs = 1
	m.MaxImageInputs = 3
	m.NoWatermark = true
	m.SupportsNegativePrompt = false
	result = append(result, m)
	for _, id := range []string{"wan2.6-t2v-us", "wan2.6-i2v-us"} {
		result = append(result, videoModel(id, id, 2, 15, []string{"720p", "1080p"}, true))
	}
	m = videoModel("wan2.2-s2v", "Wan 2.2 Digital Human", 1, 20, []string{"480p", "720p"}, true)
	m.SupportsImageInput = true
	m.RequiresImageInput = true
	m.MinImageInputs = 1
	m.MaxImageInputs = 1
	m.SupportsAudioInput = true
	m.RequiresAudioInput = true
	m.DurationFromInput = true
	m.NoPrompt = true
	m.NoSeed = true
	m.NoWatermark = true
	m.SupportsNegativePrompt = false
	result = append(result, m)
	// Regional IDs are distinct upstream model names, not price aliases.
	for _, pair := range [][2]string{{"wanx2.0-t2i-turbo", "wan2.1-t2i-turbo"}, {"wanx2.1-t2i-turbo", "wan2.1-t2i-turbo"}, {"wanx2.1-t2i-plus", "wan2.1-t2i-plus"}, {"wanx2.1-kf2v-plus", "wan2.1-kf2v-plus"}, {"wanx2.1-t2v-plus", "wan2.1-t2v-plus"}, {"wanx2.1-t2v-turbo", "wan2.1-t2v-turbo"}, {"wanx2.1-vace-plus", "wan2.1-vace-plus"}} {
		// Clone only an exact reviewed contract; keep the regional ID on the wire.
		var source *core.MediaModel
		for i := range result {
			if result[i].ID == pair[1] {
				source = &result[i]
				break
			}
		}
		if source == nil {
			switch {
			case strings.Contains(pair[1], "t2i"):
				x := imageModel(pair[0], pair[0], false, 0, []string{"1024*1024", "1280*720", "720*1280", "1440*1440"})
				source = &x
			default:
				x := videoModel(pair[0], pair[0], 5, 5, []string{"720p"}, false)
				if strings.Contains(pair[1], "turbo") {
					x.Resolutions = []string{"480p", "720p"}
				}
				source = &x
			}
		}
		x := *source
		x.ID = pair[0]
		x.DisplayName = pair[0]
		result = append(result, x)
	}
	m = videoModel("wan2.7-r2v", "Wan 2.7 Reference-to-Video", 2, 10, []string{"720p", "1080p"}, true)
	result = append(result, m)
	return result
}

func isBatchSpeech(id string) bool {
	switch id {
	case "qwen-audio-3.0-tts-plus", "qwen-audio-3.0-tts-flash", "cosyvoice-v3-plus", "cosyvoice-v3-flash", "cosyvoice-v3.5-plus", "cosyvoice-v3.5-flash", "cosyvoice-v2":
		return true
	}
	return false
}

// Dispatch reviewed regional versions through the same protocol while retaining
// their exact model ID in every upstream request and pricing record.
func contractID(id string) string {
	switch id {
	case "wan2.6-t2v-us":
		return "wan2.6-t2v"
	case "wan2.6-i2v-us":
		return "wan2.6-i2v"
	case "wanx2.0-t2i-turbo":
		return "wan2.1-t2i-turbo"
	case "wanx2.1-t2i-plus":
		return "wan2.1-t2i-plus"
	case "wanx2.1-t2i-turbo":
		return "wan2.1-t2i-turbo"
	case "wanx2.1-kf2v-plus":
		return "wan2.1-kf2v-plus"
	case "wanx2.1-t2v-plus":
		return "wan2.1-t2v-plus"
	case "wanx2.1-t2v-turbo":
		return "wan2.1-t2v-turbo"
	case "wanx2.1-vace-plus":
		return "wan2.1-vace-plus"
	}
	return id
}

func viduVideoSize(resolution, ratio string) string {
	sizes := map[string]map[string]string{
		"720p":  {"16:9": "1280*720", "9:16": "720*1280", "4:3": "1280*960", "3:4": "960*1280", "1:1": "1280*1280"},
		"1080p": {"16:9": "1920*1080", "9:16": "1080*1920", "4:3": "1920*1440", "3:4": "1440*1920", "1:1": "1920*1920"},
	}
	return sizes[resolution][ratio]
}

func (a *Adapter) ReviewedCatalogModels() []core.MediaModel { return mediaModels(a.musicEnabled) }
