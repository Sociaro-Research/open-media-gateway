package minimax

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"strings"
)

type operation struct {
	id, upstream, kind, endpoint string
	template                     *agentTemplate
}
type agentTemplate struct {
	id, name    string
	image, text bool
}

var agentTemplates = []agentTemplate{
	{"392747428568649728", "Diving", true, false}, {"393769180141805569", "Run for Life", true, true},
	{"397087679467597833", "Transformers", true, false}, {"393881433990066176", "Still rings routine", true, false},
	{"393498001241890824", "Weightlifting", true, false}, {"393488336655310850", "Climbing", true, false},
	{"394514820878671878", "Anime Life Sim", false, true}, {"393879757702918151", "McDonald's Delivery Pet", true, false},
	{"397017167949312007", "Pet Pilot", true, false}, {"394176968202485769", "Miniature Set Ad", true, false},
	{"393876118804459526", "Male Model Try-On Ad", true, false}, {"393866076583718914", "Female Model Try-On Ad", true, false},
	{"394875727173492739", "Art Fonts", false, true}, {"394220989629177861", "Drinkfall", true, false},
	{"393853165953970178", "E-commerce Display Ad", false, true}, {"401431836934868999", "3D character product presentation", true, true},
}

func operations() []operation {
	out := []operation{}
	for _, id := range []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max"} {
		out = append(out, operation{id: id, upstream: id, kind: "h3", endpoint: "/v2/video_generation"})
	}
	for _, id := range []string{"MiniMax-Hailuo-2.3", "MiniMax-Hailuo-2.3-Fast", "MiniMax-Hailuo-02", "T2V-01-Director", "T2V-01", "I2V-01-Director", "I2V-01-live", "I2V-01", "S2V-01"} {
		out = append(out, operation{id: id, upstream: id, kind: "legacy", endpoint: "/v1/video_generation"})
	}
	for _, id := range []string{"image-01", "image-01-live"} {
		out = append(out, operation{id: id, upstream: id, kind: "image", endpoint: "/v1/image_generation"})
	}
	out = append(out, operation{id: ContextIR, upstream: "MiniMax-H3", kind: "context", endpoint: "/v2/h3_context_ir"}, operation{id: Regeneration, upstream: "MiniMax-H3", kind: "regeneration", endpoint: "/v2/video_regeneration"})
	out = append(out, operation{id: RegenerationVideo, upstream: "MiniMax-H3", kind: "base_regeneration", endpoint: "/v2/video_regeneration"})
	out = append(out, operation{id: ResourceUpload, kind: "resource"}, operation{id: ResourceOutputFile, kind: "resource"}, operation{id: ResourceSourceTask, kind: "resource"})
	for i := range agentTemplates {
		t := &agentTemplates[i]
		out = append(out, operation{id: "video-agent-" + t.id, kind: "agent", endpoint: "/v1/video_template_generation", template: t})
	}
	return out
}
func operationFor(id string) (operation, bool) {
	for _, op := range operations() {
		if op.id == id {
			return op, true
		}
	}
	return operation{}, false
}

var concreteRatios = []string{"16:9", "21:9", "4:3", "1:1", "3:4", "9:16"}

func modelFor(id string) (core.MediaModel, bool) {
	op, ok := operationFor(id)
	if !ok {
		return core.MediaModel{}, false
	}
	return describe(op), true
}
func catalog() []core.MediaModel {
	out := []core.MediaModel{}
	for _, op := range operations() {
		out = append(out, describe(op))
	}
	return out
}
func (a *Adapter) ListModels(context.Context) ([]core.MediaModel, error) { return catalog(), nil }
func (a *Adapter) ReviewedCatalogModels() []core.MediaModel              { return catalog() }
func describe(op operation) core.MediaModel {
	if op.kind == "base_regeneration" {
		return baseRegenerationModel()
	}
	if op.kind == "resource" {
		return fileOperationModel(op.id)
	}
	if op.kind == "regeneration" {
		return regenerationModel()
	}
	if op.kind == "context" {
		m := describe(operation{id: "MiniMax-H3", upstream: "MiniMax-H3", kind: "h3"})
		m.ID = ContextIR
		m.DisplayName = "MiniMax H3 · Context preparation"
		m.MediaType = "text"
		m.Metadata.Description = "Prepares text from image, video and audio context. Audio references are inputs; this operation returns text and has no generated-audio control."
		m.Resolutions = nil
		m.SupportsAudio = false
		m.AudioAlwaysOn = false
		m.Pricing = core.MediaModelPricing{ReferenceDescription: "Context-IR costs USD 0.90 per million input tokens and USD 3.60 per million output tokens. Total preflight tokens are unknown; no total estimate is invented."}
		m.Metadata.Sources = append(m.Metadata.Sources, core.ModelSource{Title: "Context-IR contract", URL: docsBase + "api-reference/video-generation-v2-h3-context-ir"})
		return m
	}
	m := core.MediaModel{ID: op.id, DisplayName: op.id, MediaType: "video", NoSeed: true, NoWatermark: true, NoLanguage: true, NoInstructions: true,
		Metadata: &core.ModelMetadata{CheckedAt: "2026-10-31", Sources: []core.ModelSource{{Title: "Official model lifecycle", URL: docsBase + "faq/models"}, {Title: "Official MiniMax pricing", URL: priceSource}}},
		Pricing:  core.MediaModelPricing{ReferenceDescription: "Published price unavailable for this operation; no inferred or zero-cost tariff."}}
	switch op.kind {
	case "h3":
		m.Metadata.Sources = append(m.Metadata.Sources, core.ModelSource{Title: "H3 generation contract", URL: docsBase + "api-reference/video-generation-v2-create"})
		m.ResourceInputSchemas = []core.ResourceInputSchema{{Kind: "file_resource", MaxCount: 12, Subtypes: []string{"image", "video", "audio"}}}
		m.SupportsAssetReferences = true
		m.RatioMayBeInferred = true
		m.Metadata.Description = "H3 always produces native audio and has no audio on/off parameter. Audio references are separate inputs. Text requires a concrete ratio; frame modes use adaptive; reference modes support adaptive except on Lite."
		m.MaxPromptCharacters = 7000
		m.Resolutions = []string{"768p", "2k"}
		m.Ratios = append(append([]string{}, concreteRatios...), "adaptive")
		m.MinDuration = 4
		m.MaxDuration = 15
		m.DefaultDuration = 5
		m.DurationStep = 1
		m.SupportsAudio = true
		m.AudioAlwaysOn = true
		m.SupportsImageInput = true
		m.SupportsVideoInput = true
		m.SupportsAudioInput = true
		m.MaxImageInputs = 9
		m.ReferenceModes = []core.ReferenceMode{
			{ID: "none", Label: "Text only", Ratios: concreteRatios},
			{ID: "first_frame", Label: "First frame", MinImages: 1, MaxImages: 1, ImageRoles: []string{"first_frame"}, Ratios: []string{"adaptive"}},
			{ID: "last_frame", Label: "Last frame", MinImages: 1, MaxImages: 1, ImageRoles: []string{"last_frame"}, Ratios: []string{"adaptive"}},
			{ID: "first_last_frame", Label: "First and last frames", MinImages: 2, MaxImages: 2, ImageRoles: []string{"first_frame", "last_frame"}, Ratios: []string{"adaptive"}},
			{ID: "references", Label: "Image, video and audio references", MaxImages: 9, MaxVideos: 3, MaxAudios: 3},
		}
		// Examples use ordinary natural-language references, without mandatory tags.
		m.ReferencePromptRecommendation = &core.ReferencePromptRecommendation{Kind: "example", Text: "The character follows the motion in reference video 1; appearance follows reference image 1; voice timbre follows reference audio 1.", SourceURL: docsBase + "guides/video-generation", Modes: []string{"references"}}
		m.ReferenceMediaLimits = h3Limits()
		if op.id != "MiniMax-H3" {
			m.Resolutions = []string{"768p", "480p"}
		}
		if op.id == "MiniMax-H3-Max" {
			m.MinDuration = 5
			m.Parameters = []core.ParameterField{{Key: "prompt_expansion_mode", Label: "Prompt expansion", Type: "select", Choices: []string{"disabled", "balanced", "quality"}, Default: "balanced"}}
		}
		if op.id == "MiniMax-H3-Lite" {
			m.ReferenceModes[4].Ratios = concreteRatios
		}
		m.Pricing = core.MediaModelPricing{Known: true, ReferencePriceUSD: float64(h3Tariff(op.id, "768p").output) * 5 / 1e6, ReferenceDescription: "5-second 768P output with no input-video charges or images beyond the free allowance. Other inputs are priced separately."}
	case "legacy":
		m.Metadata.Description = "768P supports 6 or 10 seconds; 1080P supports only 6 seconds. Legacy models have no configurable generated-audio switch."
		m.MaxPromptCharacters = 2000
		m.DefaultDuration = 6
		m.MinDuration = 6
		m.MaxDuration = 10
		m.DurationStep = 4
		m.Resolutions = []string{"768p", "1080p"}
		m.ReferenceModes = []core.ReferenceMode{{ID: "none", Label: "Text only"}, {ID: "first_frame", Label: "First frame", MinImages: 1, MaxImages: 1, ImageRoles: []string{"first_frame"}}}
		m.SupportsImageInput = true
		m.MaxImageInputs = 1
		m.Parameters = []core.ParameterField{{Key: "prompt_optimizer", Label: "Optimize prompt", Type: "boolean", Default: true}}
		if strings.HasPrefix(op.id, "MiniMax-") {
			m.Parameters = append(m.Parameters, core.ParameterField{Key: "fast_pretreatment", Label: "Fast prompt preparation", Type: "boolean", Default: false})
			m.Pricing = core.MediaModelPricing{Known: true, ReferencePriceUSD: 0.28, ReferenceDescription: "One 768P 6-second video at the published list price."}
			if op.id == "MiniMax-Hailuo-2.3-Fast" {
				m.ReferenceModes = m.ReferenceModes[1:]
				m.RequiresImageInput = true
				m.MinImageInputs = 1
				m.PromptOptional = true
				m.Pricing.ReferencePriceUSD = 0.19
			}
			if op.id == "MiniMax-Hailuo-02" {
				m.Metadata.Description += " Hailuo-02 512P supports 6 or 10 seconds only with a first-frame input."
				m.Resolutions = append(m.Resolutions, "512p")
				m.MaxImageInputs = 2
				m.ReferenceModes = append(m.ReferenceModes, core.ReferenceMode{ID: "last_frame", Label: "Last frame", MinImages: 1, MaxImages: 1, ImageRoles: []string{"last_frame"}}, core.ReferenceMode{ID: "first_last_frame", Label: "First and last frames", MinImages: 2, MaxImages: 2, ImageRoles: []string{"first_frame", "last_frame"}})
			}
		} else {
			m.Metadata.Description = "The reviewed legacy endpoint exposes 720P and 6 seconds, without a generated-audio switch."
			m.Resolutions = []string{"720p"}
			m.MaxDuration = 6
			if strings.HasPrefix(op.id, "T2V-") {
				m.SupportsImageInput = false
				m.MaxImageInputs = 0
				m.ReferenceModes = m.ReferenceModes[:1]
			} else {
				m.ReferenceModes = m.ReferenceModes[1:]
				m.RequiresImageInput = true
				m.MinImageInputs = 1
				m.PromptOptional = true
			}
			if op.id == "S2V-01" {
				m.Metadata.Description = "One character portrait; the endpoint has no documented resolution, duration or generated-audio controls."
				m.Resolutions = nil
				m.MinDuration = 0
				m.MaxDuration = 0
				m.DefaultDuration = 0
				m.ReferenceModes = []core.ReferenceMode{{ID: "references", Label: "Character portrait", MinImages: 1, MaxImages: 1, ImageRoles: []string{"reference"}}}
			}
		}
		if m.SupportsImageInput {
			m.PromptOptional = true
		}
		m.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: m.MaxImageInputs, Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png", "image/webp"}, MaxBytes: 20_000_000, MinDimension: 301, MinRatio: .4, MaxRatio: 2.5}}
		endpoint := "video-generation-i2v"
		if strings.HasPrefix(op.id, "T2V-") {
			endpoint = "video-generation-t2v"
		} else if op.id == "S2V-01" {
			endpoint = "video-generation-s2v"
		}
		m.Metadata.Sources = append(m.Metadata.Sources, core.ModelSource{Title: "Legacy generation contract", URL: docsBase + "api-reference/" + endpoint})
		if strings.HasPrefix(op.id, "MiniMax-") && op.id != "MiniMax-Hailuo-2.3-Fast" {
			m.Metadata.Sources = append(m.Metadata.Sources, core.ModelSource{Title: "Legacy text generation", URL: docsBase + "api-reference/video-generation-t2v"})
		}
		if op.id == "MiniMax-Hailuo-02" {
			m.Metadata.Sources = append(m.Metadata.Sources, core.ModelSource{Title: "First/last frame generation", URL: docsBase + "api-reference/video-generation-fl2v"})
		}
	case "image":
		m.Metadata.Sources = append(m.Metadata.Sources, core.ModelSource{Title: "Portrait image generation", URL: docsBase + "api-reference/image-generation-i2i"})
		if op.id == "image-01" {
			m.Metadata.Sources = append(m.Metadata.Sources, core.ModelSource{Title: "Text image generation", URL: docsBase + "api-reference/image-generation-t2i"})
		}
		m.MediaType = "image"
		m.MaxOutputs = 9
		m.NoSeed = false
		m.MaxPromptCharacters = 1500
		m.Ratios = []string{"1:1", "16:9", "4:3", "3:2", "2:3", "3:4", "9:16", "21:9"}
		m.SupportsImageInput = true
		m.MaxImageInputs = 1
		m.ReferenceModes = []core.ReferenceMode{{ID: "none", Label: "Text only"}, {ID: "references", Label: "Character portrait", MinImages: 1, MaxImages: 1, ImageRoles: []string{"reference"}}}
		m.Parameters = []core.ParameterField{{Key: "prompt_optimizer", Label: "Optimize prompt", Type: "boolean", Default: false}, {Key: "width", Label: "Custom width (multiple of 8)", Type: "integer", Min: number(512), Max: number(2048)}, {Key: "height", Label: "Custom height (multiple of 8)", Type: "integer", Min: number(512), Max: number(2048)}}
		m.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 1, Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png"}, MaxBytes: 10_000_000}}
		if op.id == "image-01-live" {
			m.RequiresImageInput = true
			m.MinImageInputs = 1
			m.ReferenceModes = m.ReferenceModes[1:]
			m.Parameters = m.Parameters[:1]
		} else {
			m.Pricing = core.MediaModelPricing{Known: true, ReferencePriceUSD: .0035, ReferenceDescription: "One generated image at USD 0.0035 per image."}
		}
	case "agent":
		m.Metadata.Sources = append(m.Metadata.Sources, core.ModelSource{Title: "Video Agent generation", URL: docsBase + "api-reference/video-agent-create"})
		m.DisplayName = "Video Agent · " + op.template.name
		m.MaxPromptCharacters = core.MaxPromptCharacters
		m.NoPrompt = !op.template.text
		if op.template.text {
			maxExtraTexts := 63
			if op.template.image {
				maxExtraTexts = 62
			}
			m.Parameters = []core.ParameterField{{Key: "additional_texts", Label: "Additional template text inputs", Type: "text_list", MaxItems: maxExtraTexts, MaxLength: core.MaxPromptCharacters}}
		}
		m.PromptOptional = !op.template.text
		m.ReferenceModes = []core.ReferenceMode{{ID: "none", Label: "Text only"}}
		m.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 64, Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png", "image/webp"}, MaxBytes: 20_000_000, MinDimension: 301, MinRatio: .4, MaxRatio: 2.5}}
		if op.template.image {
			m.SupportsImageInput = true
			m.RequiresImageInput = true
			m.MinImageInputs = 1
			m.MaxImageInputs = 64
			if op.template.text {
				m.MaxImageInputs = 63
			}
			m.ReferenceModes = []core.ReferenceMode{{ID: "references", Label: "Template images", MinImages: 1, MaxImages: m.MaxImageInputs, ImageRoles: []string{"reference"}}}
		}
		m.Metadata.Sources = append(m.Metadata.Sources, core.ModelSource{Title: "Official Video Agent templates", URL: docsBase + "faq/video-agent-templates"})
	}
	return m
}
func h3Limits() *core.ReferenceMediaLimits {
	return &core.ReferenceMediaLimits{MaxFiles: 12,
		Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png", "image/webp", "image/heic", "image/heif"}, MaxBytes: 30_000_000, MaxBytesInclusive: true, MinDimension: 256, MaxDimension: 5760, MinRatio: .4, MaxRatio: 2.5},
		Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, Codecs: []string{"avc1", "avc3", "hvc1", "hev1"}, AllowedAudioCodecs: []string{"aac", "mp3"}, MaxBytes: 50_000_000, MaxBytesInclusive: true, MinDimension: 256, MaxDimension: 5760, MinRatio: .4, MaxRatio: 2.5, MinDuration: 2, MaxDuration: 15, MaxTotalDuration: 15, MinFPS: 23.976, MaxFPS: 60},
		Audio: core.MediaFileLimits{ContentTypes: []string{"audio/wav", "audio/mpeg"}, MaxBytes: 15_000_000, MaxBytesInclusive: true, MinDuration: 2, MaxDuration: 15, MaxTotalDuration: 15}}
}
func number(n float64) *float64 { return &n }
