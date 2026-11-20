package minimax

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

const h3Image = "MiniMax-H3-Image"
const h3ImageHighspeed = "MiniMax-H3-Image-Highspeed"

var h3Dimensions = regexp.MustCompile(`^([1-9][0-9]{0,7})x([1-9][0-9]{0,7})$`)
var h3Aspect = regexp.MustCompile(`^([1-9][0-9]{0,2}):([1-9][0-9]{0,2})$`)

// h3Images is intentionally absent from the registry and adapter constructor.
// Its only wiring is the isolated contract fixture.
type h3Images struct {
	*Adapter
	tariffs map[string]core.PricingSnapshot
}

func h3ImageModel(id string) (core.MediaModel, bool) {
	if id != h3Image && id != h3ImageHighspeed {
		return core.MediaModel{}, false
	}
	return core.MediaModel{ID: id, DisplayName: id, MediaType: "image", NoSeed: true, NoWatermark: true, NoLanguage: true, NoInstructions: true,
		MaxOutputs: 10, MaxPromptCharacters: 32000, SupportsImageInput: true, MaxImageInputs: 16, MaxReferenceInputs: 16,
		ReferenceModes: []core.ReferenceMode{{ID: "none", Label: "Text to image"}, {ID: "references", Label: "Edit with reference images", MinImages: 1, MaxImages: 16}},
		Formats:        []string{"jpeg", "jpg", "png", "webp"},
		Parameters: []core.ParameterField{
			{Key: "size", Label: "Output dimensions", Type: "text", MaxLength: 17},
			{Key: "aspect_ratio", Label: "Output aspect ratio", Type: "text", MaxLength: 7},
			{Key: "area", Label: "Output area", Type: "select", Choices: []string{"1K", "2K", "4K"}},
			{Key: "response_format", Label: "Provider response format", Type: "select", Choices: []string{"url", "b64_json"}},
			{Key: "output_compression", Label: "Output compression", Type: "integer", Min: number(0), Max: number(100)},
			{Key: "disable_prompt_enhanced", Label: "Disable text-to-image prompt enhancement", Type: "boolean"},
		},
		ReferenceMediaLimits: &core.ReferenceMediaLimits{MaxFiles: 16, Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png", "image/webp", "image/heic", "image/heif"}, MaxBytesInclusive: true, MaxBytes: 64 << 20, MinDimension: 128, MaxDimension: 8192, MinRatio: 0.25, MaxRatio: 4}},
		Metadata:             &core.ModelMetadata{Description: "Synchronous JSON. Access depends on the provider account. Edits always include prompt enhancement. Public reference URLs require inspected image metadata."},
	}, true
}

func h3Pair(value string, pattern *regexp.Regexp) (int64, int64, bool) {
	fields := pattern.FindStringSubmatch(value)
	if fields == nil {
		return 0, 0, false
	}
	x, _ := strconv.ParseInt(fields[1], 10, 64)
	y, _ := strconv.ParseInt(fields[2], 10, 64)
	return x, y, x <= 3*y && y <= 3*x
}

func h3OutputArea(p map[string]any) (int64, error) {
	size, _ := p["size"].(string)
	ratio, hasRatio := p["aspect_ratio"].(string)
	area, hasArea := p["area"].(string)
	if _, ok := p["size"]; ok && size != "auto" {
		x, y, ok := h3Pair(size, h3Dimensions)
		if !ok || hasRatio || hasArea || x*y < 655360 || x*y > 20000000 {
			return 0, invalid("invalid output dimensions or conflicting size options")
		}
		return x * y, nil
	}
	if hasRatio && ratio != "auto" {
		x, y, ok := h3Pair(ratio, h3Aspect)
		if !ok || x > 300 || y > 300 {
			return 0, invalid("invalid output aspect ratio")
		}
	}
	if p["disable_prompt_enhanced"] == true && (ratio == "" || ratio == "auto") {
		return 0, invalid("a concrete size or aspect ratio is required without prompt enhancement")
	}
	switch area {
	case "1K":
		return 1024 * 1024, nil
	case "2K":
		return 2048 * 2048, nil
	default:
		return 4096 * 4096, nil
	}
}

func h3ImageRequest(r core.MediaRequest) (string, []byte, error) {
	m, ok := h3ImageModel(r.Model)
	if !ok {
		return "", nil, invalid("unsupported H3 image model")
	}
	o := r.Options
	if o.TimeoutSeconds != nil && (*o.TimeoutSeconds < 600 || *o.TimeoutSeconds > 86400) {
		return "", nil, invalid("synchronous image requests require a timeout of at least 600 seconds within the Gateway limit")
	}
	// All fields outside the reviewed Gateway projection fail closed, including
	// new provider-neutral controls until their native semantics are reviewed.
	allowed := core.MediaOptions{OutputCount: o.OutputCount, Parameters: o.Parameters, Format: o.Format, ImageURLs: o.ImageURLs, ReferenceMode: o.ReferenceMode, ReferenceMediaEvidence: o.ReferenceMediaEvidence, ReferenceRoles: o.ReferenceRoles, TimeoutSeconds: o.TimeoutSeconds}
	if !reflect.DeepEqual(o, allowed) {
		return "", nil, invalid("unsupported H3 image option")
	}
	prompt := strings.TrimSpace(r.Prompt)
	if !utf8.ValidString(prompt) || prompt == "" || utf8.RuneCountInString(prompt) > 32000 {
		return "", nil, invalid("prompt must contain 1 to 32000 characters")
	}
	mode, err := effectiveMode(m, o)
	if err != nil {
		return "", nil, err
	}
	if err := core.ValidateReferenceRoles(m, o); err != nil {
		return "", nil, err
	}
	n := o.OutputCount
	if n == 0 {
		n = 1
	}
	if n < 1 || n > 10 {
		return "", nil, invalid("output count must be 1 to 10")
	}
	format := o.Format
	if format == "" {
		format = "jpeg"
	}
	if !slices.Contains(m.Formats, format) {
		return "", nil, invalid("unsupported image format")
	}
	p, err := core.NormalizeParameters(m.Parameters, o.Parameters)
	if err != nil {
		return "", nil, err
	}
	if p == nil {
		p = map[string]any{}
	}
	pixels, err := h3OutputArea(p)
	if err != nil {
		return "", nil, err
	}
	if p["response_format"] == "b64_json" {
		limit := int64(80000000)
		if format == "png" {
			limit = 20000000
		}
		if pixels*int64(n) > limit {
			return "", nil, invalid("total output area exceeds inline encoding limit")
		}
	}
	path := "/v1/images/generations"
	if mode.ID == "references" {
		path = "/v1/images/edits"
		if _, exists := p["disable_prompt_enhanced"]; exists {
			return "", nil, invalid("image edits always apply prompt enhancement")
		}
		refs := make([]map[string]string, 0, len(o.ImageURLs))
		for _, ref := range o.ImageURLs {
			evidence := o.ReferenceMediaEvidence[ref]
			lim := m.ReferenceMediaLimits.Image
			if !publicURL(ref) || evidence.Type != "image" || !slices.Contains(lim.ContentTypes, evidence.ContentType) || evidence.SizeBytes < 1 || evidence.SizeBytes > lim.MaxBytes || evidence.Width < 128 || evidence.Width > 8192 || evidence.Height < 128 || evidence.Height > 8192 || evidence.Width > 4*evidence.Height || evidence.Height > 4*evidence.Width {
				return "", nil, invalid("reference requires an inspected HTTPS image within documented limits")
			}
			refs = append(refs, map[string]string{"image_url": ref})
		}
		p["images"] = refs
	}
	p["model"], p["prompt"], p["n"], p["output_format"], p["stream"] = r.Model, prompt, n, format, false
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > 64<<20 {
		return "", nil, invalid("invalid or oversized JSON request")
	}
	return path, raw, nil
}

func (a *h3Images) ValidateRequest(_ context.Context, r core.MediaRequest) error {
	_, _, err := h3ImageRequest(r)
	return err
}
