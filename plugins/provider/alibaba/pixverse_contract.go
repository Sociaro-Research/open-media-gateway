package alibaba

import (
	"fmt"
	"mime"
	"net"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

const (
	pixverseT2V                  = "pixverse/pixverse-v6-t2v"
	pixverseI2V                  = "pixverse/pixverse-v6-it2v"
	pixverseKF2V                 = "pixverse/pixverse-v6-kf2v"
	pixverseR2V                  = "pixverse/pixverse-v6-r2v"
	pixverseAccountReviewSetting = "pixverse_reviewed_connection"
	// Gateway uses a conservative decimal cap; the public API says "20 MB".
	pixverseImageMaxBytes int64 = 20_000_000
)

var pixverseIDs = []string{pixverseT2V, pixverseI2V, pixverseKF2V, pixverseR2V}
var pixverseRatios = []string{"16:9", "4:3", "1:1", "3:4", "9:16", "3:2", "2:3", "21:9"}

// T2V and R2V independently publish this exact table. Do not use Wan/Vidu sizes
// or infer the pricing tier from the shorter side (1024*1024 belongs to 540P).
var pixverseSizes = map[string][]string{
	"360p":  {"640*360", "640*480", "640*640", "480*640", "360*640", "640*432", "432*640", "640*288"},
	"540p":  {"1024*576", "1024*768", "1024*1024", "768*1024", "576*1024", "1024*688", "688*1024", "1024*448"},
	"720p":  {"1280*720", "1108*832", "960*960", "832*1108", "720*1280", "1200*800", "800*1200", "1280*560"},
	"1080p": {"1920*1080", "1664*1248", "1440*1440", "1248*1664", "1080*1920", "1776*1184", "1184*1776", "1920*832"},
}
var pixverseAPI = map[string]string{
	pixverseT2V:  "https://www.alibabacloud.com/help/en/model-studio/pixverse-text-to-video-api-reference",
	pixverseI2V:  "https://www.alibabacloud.com/help/en/model-studio/pixverse-image-to-video-api-reference",
	pixverseKF2V: "https://www.alibabacloud.com/help/en/model-studio/pixverse-keyframe-to-video-api-reference",
	pixverseR2V:  "https://www.alibabacloud.com/help/en/model-studio/pixverse-reference-to-video-api-reference",
}

func isPixverseModel(id string) bool { return slices.Contains(pixverseIDs, id) }

func pixverseModel(id string) (core.MediaModel, bool) {
	if !isPixverseModel(id) {
		return core.MediaModel{}, false
	}
	m := core.MediaModel{
		ID: id, DisplayName: id, MediaType: "video",
		Resolutions: []string{"360p", "540p", "720p", "1080p"},
		MinDuration: 1, MaxDuration: 15, DefaultDuration: 5, DurationStep: 1,
		SupportsAudio: true, MaxSeed: 2147483647, MaxPromptCharacters: 5000,
		Formats:      []string{"mp4"},
		PromptPolicy: &core.PromptPolicy{MaxCharacters: 5000, PreserveWhitespace: true},
		Metadata: &core.ModelMetadata{
			ReleaseDate: "2026-10-22", CheckedAt: "2026-11-08",
			Facts: []core.ModelFact{{Label: "Availability", Value: "Requires reviewed Singapore workspace configuration and separately verified account activation. Public documentation does not verify this account."}},
			Sources: []core.ModelSource{
				{Title: "Exact native API", URL: pixverseAPI[id]},
				{Title: "Singapore resolution/audio tariffs", URL: alibabaPriceSource},
				{Title: "Official releases", URL: "https://www.alibabacloud.com/help/en/model-studio/newly-released-models"},
			},
		},
	}
	switch id {
	case pixverseT2V:
		m.Ratios = slices.Clone(pixverseRatios)
		m.ReferenceModes = []core.ReferenceMode{{ID: "none", Label: "Text only"}}
	case pixverseI2V:
		m.PromptOptional, m.RatioMayBeInferred = true, true
		m.SupportsImageInput, m.RequiresImageInput = true, true
		m.MinImageInputs, m.MaxImageInputs = 1, 1
		m.ReferenceModes = []core.ReferenceMode{{ID: "first_frame", Label: "First frame", MinImages: 1, MaxImages: 1, ImageRoles: []string{"First frame"}}}
	case pixverseKF2V:
		m.RatioMayBeInferred = true
		m.SupportsImageInput, m.RequiresImageInput = true, true
		m.MinImageInputs, m.MaxImageInputs = 2, 2
		m.ReferenceModes = []core.ReferenceMode{{ID: "first_last_frames", Label: "First and last frames", MinImages: 2, MaxImages: 2, ImageRoles: []string{"First frame", "Last frame"}}}
		m.ReferenceRoleSchemas = []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"first_frame", "last_frame"}}}
	case pixverseR2V:
		m.Ratios = slices.Clone(pixverseRatios)
		m.MaxPromptCharacters = 2048
		m.PromptPolicy.MaxCharacters = 2048
		m.SupportsImageInput, m.RequiresImageInput = true, true
		// Deliberate Gateway guard, not a claim about a proven API minimum.
		// Release notes say 2..7; detailed API states only max 7. Keep both visible.
		m.MinImageInputs, m.MaxImageInputs = 2, 7
		m.ReferenceModes = []core.ReferenceMode{{ID: "references", Label: "References", MinImages: 2, MaxImages: 7}}
		m.Parameters = []core.ParameterField{{Key: "reference_names", Label: "Ordered reference names (empty item means unnamed)", Type: "text_list", MaxItems: 7, MaxLength: 30}}
		m.Metadata.Facts = append(m.Metadata.Facts, core.ModelFact{Label: "Unresolved input minimum", Value: "Release notes: 2–7 images. Detailed API: maximum seven, minimum unspecified. Gateway conservatively requires two pending clarification."})
	}
	if id == pixverseT2V || id == pixverseI2V {
		m.Parameters = append(m.Parameters, core.ParameterField{Key: "shot_type", Label: "Shot type", Type: "select", Choices: []string{"single", "multi"}})
	}
	if m.SupportsImageInput {
		m.ReferenceMediaLimits = &core.ReferenceMediaLimits{
			MaxFiles: m.MaxImageInputs,
			Image: core.MediaFileLimits{
				ContentTypes: []string{"image/jpeg", "image/png", "image/webp"},
				MaxBytes:     pixverseImageMaxBytes, MaxBytesInclusive: true,
				MinDimension: 1, MaxDimension: 10000,
			},
		}
	}
	return m, true
}

func pixverseModels() []core.MediaModel {
	var result []core.MediaModel
	for _, id := range pixverseIDs {
		m, _ := pixverseModel(id)
		result = append(result, m)
	}
	return result
}

func pixverseEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" || u.Path != "/api/v1" || u.RawPath != "" {
		return false
	}
	const suffix = ".ap-southeast-1.maas.aliyuncs.com"
	host := u.Hostname()
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	workspace := strings.TrimSuffix(host, suffix)
	// DNS-label checks are transport safety, not a claim about account ID format.
	if workspace == "" || len(workspace) > 63 || strings.Contains(workspace, ".") || workspace[0] == '-' || workspace[len(workspace)-1] == '-' {
		return false
	}
	for _, c := range workspace {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func (a *Adapter) pixverseReady() bool {
	return a.pixverseAccountReviewed && pixverseEndpoint(a.baseURL)
}

func (a *Adapter) validatePixverseConnection() error {
	if !a.pixverseReady() {
		return fmt.Errorf("%w: PixVerse requires a reviewed Singapore workspace endpoint and separately verified account activation; service activation is never automatic", core.ErrProviderUnavailable)
	}
	return nil
}

func (a *Adapter) pixverseVisibleModels(models []core.MediaModel) []core.MediaModel {
	return slices.DeleteFunc(models, func(m core.MediaModel) bool { return isPixverseModel(m.ID) && !a.pixverseReady() })
}

type pixverseCommonParameters struct {
	Duration  int    `json:"duration"`
	Audio     bool   `json:"audio"`
	Watermark *bool  `json:"watermark,omitempty"`
	Seed      *int64 `json:"seed,omitempty"`
}
type pixverseImage struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}
type pixverseNamedImage struct {
	Type    string `json:"type"`
	URL     string `json:"url"`
	RefName string `json:"ref_name,omitempty"`
}
type pixverseT2VPayload struct {
	Model string `json:"model"`
	Input struct {
		Prompt string `json:"prompt"`
	} `json:"input"`
	Parameters struct {
		pixverseCommonParameters
		Size     string `json:"size"`
		ShotType string `json:"shot_type,omitempty"`
	} `json:"parameters"`
}
type pixverseI2VPayload struct {
	Model string `json:"model"`
	Input struct {
		Prompt string          `json:"prompt,omitempty"`
		Media  []pixverseImage `json:"media"`
	} `json:"input"`
	Parameters struct {
		pixverseCommonParameters
		Resolution string `json:"resolution"`
		ShotType   string `json:"shot_type,omitempty"`
	} `json:"parameters"`
}
type pixverseKF2VPayload struct {
	Model string `json:"model"`
	Input struct {
		Prompt string          `json:"prompt"`
		Media  []pixverseImage `json:"media"`
	} `json:"input"`
	Parameters struct {
		pixverseCommonParameters
		Resolution string `json:"resolution"`
	} `json:"parameters"`
}
type pixverseR2VPayload struct {
	Model string `json:"model"`
	Input struct {
		Prompt string               `json:"prompt"`
		Media  []pixverseNamedImage `json:"media"`
	} `json:"input"`
	Parameters struct {
		pixverseCommonParameters
		Size string `json:"size"`
	} `json:"parameters"`
}

func pixverseSize(tier, ratio string) (string, bool) {
	i := slices.Index(pixverseRatios, ratio)
	values, ok := pixverseSizes[tier]
	if !ok || i < 0 {
		return "", false
	}
	return values[i], true
}

func pixverseMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return false
	}
	return true
}

func pixverseBindings(prompt string, names []string) error {
	declared, used := map[string]bool{}, map[string]bool{}
	for _, name := range names {
		if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 30 {
			return fmt.Errorf("%w: reference name exceeds 30 characters", core.ErrOptionsInvalid)
		}
		if name == "" {
			continue
		}
		if declared[name] {
			return fmt.Errorf("%w: duplicate reference name", core.ErrOptionsInvalid)
		}
		declared[name] = true
	}
	for offset := 0; offset < len(prompt); {
		at := strings.IndexByte(prompt[offset:], '@')
		if at < 0 {
			break
		}
		start := offset + at + 1
		matched := ""
		for name := range declared {
			if strings.HasPrefix(prompt[start:], name+" ") {
				if matched != "" {
					return fmt.Errorf("%w: ambiguous reference names", core.ErrOptionsInvalid)
				}
				matched = name
			}
		}
		if matched == "" {
			return fmt.Errorf("%w: each named reference must match its image and end with a separating space", core.ErrOptionsInvalid)
		}
		used[matched] = true
		offset = start + len(matched) + 1
	}
	if len(used) != len(declared) {
		return fmt.Errorf("%w: unused named reference", core.ErrOptionsInvalid)
	}
	return nil
}

// pixversePayload is deliberately exact-ID scoped. Unsupported inputs are
// rejected before submission rather than dropped/coerced by a shared serializer.
func pixversePayload(r core.MediaRequest) (any, error) {
	m, ok := pixverseModel(r.Model)
	bad := func() (any, error) {
		return nil, fmt.Errorf("%w: unsupported PixVerse input or option", core.ErrOptionsInvalid)
	}
	if !ok {
		return bad()
	}
	o := r.Options
	if !utf8.ValidString(r.Prompt) || utf8.RuneCountInString(r.Prompt) > m.MaxPromptCharacters ||
		(!m.PromptOptional && strings.TrimSpace(r.Prompt) == "") {
		return bad()
	}
	if o.Duration < 1 || o.Duration > 15 || !slices.Contains(m.Resolutions, o.Resolution) ||
		o.Seed != nil && (*o.Seed < 0 || *o.Seed > 2147483647) {
		return bad()
	}
	if len(o.ImageURLs) < m.MinImageInputs || len(o.ImageURLs) > m.MaxImageInputs {
		return bad()
	}
	if len(o.VideoURLs) > 0 || o.VideoURL != "" || len(o.AudioURLs) > 0 || o.AudioURL != "" ||
		len(o.ReferenceVoices) > 0 || o.NegativePrompt != "" || o.Draft != nil ||
		o.CameraFixed != nil || o.ReturnLastFrame != nil || o.OutputCount != 0 ||
		o.Quality != "" || o.Background != "" || o.Voice != "" || o.Language != "" ||
		o.Instructions != "" || o.Lyrics != "" || o.Gender != "" ||
		o.Format != "" && o.Format != "mp4" || len(o.ResourceReferences) > 0 ||
		len(o.ResourceSnapshots) > 0 || o.ResourceIntent != nil || o.ResourceCreation != nil || o.SessionAction != nil {
		return bad()
	}
	if err := core.ValidateReferenceMode(m, o); err != nil {
		return nil, err
	}
	if err := core.ValidateReferenceRoles(m, o); err != nil {
		return nil, err
	}
	for _, raw := range o.ImageURLs {
		if !pixverseMediaURL(raw) {
			return bad()
		}
		// Owned uploads carry trusted inspected evidence. External HTTP(S) inputs
		// retain Gateway's provider-fetched contract; do not fetch client URLs here
		// or pretend their bytes were inspected when metadata is absent.
		if e, present := o.ReferenceMediaEvidence[raw]; present {
			contentType, _, err := mime.ParseMediaType(e.ContentType)
			if err != nil || e.Type != "image" || !slices.Contains([]string{"image/jpeg", "image/png", "image/webp"}, contentType) ||
				e.SizeBytes <= 0 || e.SizeBytes > pixverseImageMaxBytes ||
				e.Width <= 0 || e.Height <= 0 || e.Width > 10000 || e.Height > 10000 {
				return bad()
			}
		}
	}
	parameters, err := core.NormalizeParameters(m.Parameters, o.Parameters)
	if err != nil {
		return nil, err
	}
	shot, _ := parameters["shot_type"].(string)
	names, _ := parameters["reference_names"].([]string)
	if names != nil && len(names) != len(o.ImageURLs) {
		return bad()
	}
	if r.Model == pixverseR2V {
		if err := pixverseBindings(r.Prompt, names); err != nil {
			return nil, err
		}
	}
	size := ""
	if r.Model == pixverseT2V || r.Model == pixverseR2V {
		var ok bool
		size, ok = pixverseSize(o.Resolution, o.Ratio)
		if !ok {
			return bad()
		}
	} else if o.Ratio != "" {
		return bad()
	}
	common := pixverseCommonParameters{Duration: o.Duration, Audio: o.GenerateAudio != nil && *o.GenerateAudio, Watermark: o.Watermark, Seed: o.Seed}
	switch r.Model {
	case pixverseT2V:
		var p pixverseT2VPayload
		p.Model, p.Input.Prompt = r.Model, r.Prompt
		p.Parameters.pixverseCommonParameters, p.Parameters.Size, p.Parameters.ShotType = common, size, shot
		return p, nil
	case pixverseI2V:
		var p pixverseI2VPayload
		p.Model, p.Input.Prompt = r.Model, r.Prompt
		p.Input.Media = []pixverseImage{{Type: "image_url", URL: o.ImageURLs[0]}}
		p.Parameters.pixverseCommonParameters, p.Parameters.Resolution, p.Parameters.ShotType = common, strings.ToUpper(o.Resolution), shot
		return p, nil
	case pixverseKF2V:
		var p pixverseKF2VPayload
		p.Model, p.Input.Prompt = r.Model, r.Prompt
		roles := []string{"first_frame", "last_frame"}
		for i := range o.ImageURLs {
			if explicit := o.ReferenceRoles[fmt.Sprintf("image_%d", i+1)]; explicit != "" {
				roles[i] = explicit
			}
		}
		if roles[0] == roles[1] {
			return bad()
		}
		for i, raw := range o.ImageURLs {
			p.Input.Media = append(p.Input.Media, pixverseImage{Type: roles[i], URL: raw})
		}
		p.Parameters.pixverseCommonParameters, p.Parameters.Resolution = common, strings.ToUpper(o.Resolution)
		return p, nil
	case pixverseR2V:
		var p pixverseR2VPayload
		p.Model, p.Input.Prompt = r.Model, r.Prompt
		for i, raw := range o.ImageURLs {
			item := pixverseNamedImage{Type: "image_url", URL: raw}
			if names != nil {
				item.RefName = names[i]
			}
			p.Input.Media = append(p.Input.Media, item)
		}
		p.Parameters.pixverseCommonParameters, p.Parameters.Size = common, size
		return p, nil
	}
	return bad()
}
