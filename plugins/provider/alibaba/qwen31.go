package alibaba

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

const qwen31Path = "/services/audio/tts/SpeechSynthesizer"
const qwen31Next = "qwen-audio-3.1-tts-next"

var qwen31Workspace = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.(ap-southeast-1|cn-beijing)\.maas\.aliyuncs\.com$`)
var qwen31VoiceSlot = regexp.MustCompile(`@voice([0-9]+)`)

func isQwen31(id string) bool {
	switch id {
	case "qwen-audio-3.1-tts-flash", "qwen-audio-3.1-tts-plus", qwen31Next:
		return true
	}
	return false
}

// Public documentation is not evidence that the connected account is activated.
// Only an existing explicit workspace endpoint can use the reviewed contract.
func qwen31Region(endpoint string) (string, bool) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "/api/v1" || u.RawPath != "" {
		return "", false
	}
	match := qwen31Workspace.FindStringSubmatch(u.Host)
	if len(match) != 2 {
		return "", false
	}
	if match[1] == "ap-southeast-1" {
		return "Singapore", true
	}
	return "Beijing", true
}

// Start with the four reviewed shared voices. Broader voice categories and raw
// PCM/Opus output remain unadvertised until their full Gateway paths are proven.
func qwen31Models() []core.MediaModel {
	voices := []string{"longanlingxin_v3.1", "xunanchuan_v3.1", "longanhuan_v3.1", "longanfengyue_v3.1"}
	var result []core.MediaModel
	for _, id := range []string{"qwen-audio-3.1-tts-flash", "qwen-audio-3.1-tts-plus", qwen31Next} {
		m := speechModel(id, id)
		m.Voices = slices.Clone(voices)
		m.Formats = []string{"wav", "mp3"}
		m.NoWatermark = false
		m.MaxSeed = 65535
		m.Languages = []string{"zh", "en", "fr", "de", "ja", "ko", "ru", "pt", "th", "id", "vi", "es", "it", "ms", "fil", "ar"}
		if id == qwen31Next {
			m.SupportsVoice, m.RequiresVoice = false, false
			m.Voices, m.Languages = nil, nil
			m.NoLanguage, m.NoInstructions = true, true
			m.NoSeed = true // Native seed range is not specified; do not guess it.
			m.MaxSeed = 0
			m.MaxPromptCharacters = 3000
			m.SupportsAudioInput = true
			m.ReferenceModes = []core.ReferenceMode{{ID: "references", Label: "Ordered audio references", MaxAudios: 3}}
			m.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 3, Audio: core.MediaFileLimits{
				ContentTypes: []string{"audio/wav", "audio/wave", "audio/x-wav", "audio/mpeg", "audio/ogg"},
				MaxBytes:     10_000_000, MaxBytesInclusive: true, MaxDuration: 30,
			}}
		}
		result = append(result, m)
	}
	return result
}

func qwen31URL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 16384 || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Fragment != "" || u.Port() != "" && u.Port() != "443" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return net.ParseIP(host) == nil && host != "localhost" && !strings.HasSuffix(host, ".localhost") && !strings.HasSuffix(host, ".local") && !strings.HasSuffix(host, ".internal")
}

type qwen31Reference struct {
	AudioURL  string `json:"audio_url,omitempty"`
	AudioData string `json:"audio_data,omitempty"`
}

func (r qwen31Reference) validURLOnly() bool {
	// The HTTP API accepts either URL or data, never both. The current public
	// Gateway reference pipeline exposes inspected URLs only, so data is rejected
	// explicitly rather than passed through an uninspected parameters override.
	return r.AudioURL != "" && r.AudioData == "" && qwen31URL(r.AudioURL)
}

func qwen31Validate(endpoint string, r core.MediaRequest) error {
	bad := func() error {
		return fmt.Errorf("%w: unsupported Qwen Audio 3.1 input or setting", core.ErrOptionsInvalid)
	}
	if !isQwen31(r.Model) {
		return bad()
	}
	if _, ok := qwen31Region(endpoint); !ok {
		return failurePolicy.InvalidConnection()
	}
	o := r.Options
	if !utf8.ValidString(r.Prompt) || strings.TrimSpace(r.Prompt) == "" || utf8.RuneCountInString(r.Prompt) > core.MaxPromptCharacters || len(o.Parameters) != 0 || len(o.ImageURLs) != 0 || len(o.Videos()) != 0 || len(o.ReferenceVoices) != 0 || o.NegativePrompt != "" || o.Lyrics != "" || o.Gender != "" || o.Duration != 0 || o.Resolution != "" || o.Ratio != "" || o.GenerateAudio != nil || o.Quality != "" || o.Background != "" || o.OutputCount < 0 || o.OutputCount > 1 || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || len(o.ReferenceRoles) != 0 || o.SessionAction != nil || o.ResourceIntent != nil || o.ResourceCreation != nil || len(o.ResourceReferences) != 0 {
		return bad()
	}
	if o.Format != "" && o.Format != "wav" && o.Format != "mp3" {
		return bad()
	}
	if len(o.AudioURLs) > 0 && o.AudioURL != "" && o.AudioURL != o.AudioURLs[0] {
		return bad() // Do not silently choose one reference source over another.
	}
	if r.Model != qwen31Next {
		if len(o.Audios()) != 0 || o.ReferenceMode != "" && o.ReferenceMode != "none" {
			return bad()
		}
		m := qwen31Models()[0]
		if !slices.Contains(m.Voices, o.Voice) || o.Language != "" && !slices.Contains(m.Languages, o.Language) || o.Seed != nil && (*o.Seed < 0 || *o.Seed > 65535) || !utf8.ValidString(o.Instructions) || utf8.RuneCountInString(o.Instructions) > core.MaxPromptCharacters {
			return bad()
		}
		return nil
	}
	if utf8.RuneCountInString(r.Prompt) > 3000 || o.Voice != "" || o.Instructions != "" || o.Language != "" || o.Seed != nil || len(o.Audios()) > 3 || o.ReferenceMode != "" && o.ReferenceMode != "references" {
		return bad()
	}
	for _, slot := range qwen31VoiceSlot.FindAllStringSubmatch(r.Prompt, -1) {
		if slot[1] != "1" && slot[1] != "2" && slot[1] != "3" || int(slot[1][0]-'0') > len(o.Audios()) {
			return bad()
		}
	}
	for _, raw := range o.Audios() {
		if !(qwen31Reference{AudioURL: raw}).validURLOnly() {
			return bad()
		}
		e, ok := o.ReferenceMediaEvidence[raw]
		hash, hashErr := hex.DecodeString(e.SHA256)
		if !ok || e.Type != "audio" || e.SizeBytes <= 0 || e.SizeBytes > 10_000_000 || e.Duration <= 0 || e.Duration > 30 || math.IsNaN(e.Duration) || math.IsInf(e.Duration, 0) || hashErr != nil || len(hash) != 32 {
			return bad()
		}
		switch e.ContentType {
		case "audio/wav", "audio/wave", "audio/x-wav", "audio/mpeg":
		case "audio/ogg":
			if len(e.AudioCodecs) != 1 || e.AudioCodecs[0] != "opus" {
				return bad()
			}
		default:
			return bad()
		}
	}
	return nil
}

func qwen31Payload(r core.MediaRequest) any {
	format := r.Options.Format
	if format == "" {
		format = "wav"
	}
	input := map[string]any{"format": format}
	if r.Options.Watermark != nil {
		input["enable_aigc_tag"] = *r.Options.Watermark
	}
	if r.Model == qwen31Next {
		input["text_prompt"], input["sample_rate"], input["channels"] = r.Prompt, 48000, 2
		if len(r.Options.Audios()) != 0 {
			references := make([]qwen31Reference, 0, len(r.Options.Audios()))
			for _, raw := range r.Options.Audios() {
				references = append(references, qwen31Reference{AudioURL: raw})
			}
			input["references"] = references
		}
	} else {
		input["text"], input["voice"], input["sample_rate"] = r.Prompt, r.Options.Voice, 24000
		if r.Options.Language != "" {
			input["language_hints"] = []string{r.Options.Language}
		}
		if r.Options.Instructions != "" {
			input["instruction"] = r.Options.Instructions
		}
		if r.Options.Seed != nil {
			input["seed"] = *r.Options.Seed
		}
	}
	return map[string]any{"model": r.Model, "input": input}
}

type qwen31Response struct {
	RequestID string         `json:"request_id"`
	Code      string         `json:"code"`
	Usage     map[string]any `json:"usage"`
	Output    struct {
		Code         string `json:"code"`
		FinishReason string `json:"finish_reason"`
		Audio        struct {
			ID        string   `json:"id"`
			URL       string   `json:"url"`
			Data      string   `json:"data"`
			ExpiresAt int64    `json:"expires_at"`
			Duration  *float64 `json:"duration"`
		} `json:"audio"`
	} `json:"output"`
}

func qwen31Usage(id string, raw map[string]any) *core.ProviderUsage {
	if len(raw) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(raw)
	u := &core.ProviderUsage{Raw: encoded, CostStatus: "reported"}
	add := func(name string) bool {
		n, ok := raw[name].(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1<<53-1 || math.Trunc(n) != n {
			return false
		}
		unit := "token"
		if name == "characters" {
			unit = "character"
		}
		u.Metrics = append(u.Metrics, core.UsageMetric{Name: name, Unit: unit, Quantity: n})
		return true
	}
	switch id {
	case "qwen-audio-3.1-tts-plus":
		if add("characters") {
			u.CostStatus = "metered"
		}
	case "qwen-audio-3.1-tts-flash":
		if add("input_tokens") && add("output_tokens") {
			u.CostStatus = "metered"
		} else {
			u.Metrics = nil // Never make a partial two-component monetary claim.
		}
	case qwen31Next:
		// The reviewed Next file API specifies usage.duration only and states
		// that it is not the token-charge basis. Token response field semantics
		// are unconfirmed; retain raw evidence without inventing metering.
	}
	return u
}

func qwen31Result(r core.MediaRequest, response qwen31Response, now time.Time) (core.ProviderTask, error) {
	invalid := func(issue providerkit.ResponseIssue) (core.ProviderTask, error) {
		return core.ProviderTask{}, failurePolicy.InvalidResponse(providerkit.FailureSubmit, issue)
	}
	if response.Code != "" {
		return core.ProviderTask{}, failurePolicy.NativeFailure(providerkit.FailureSubmit, response.Code)
	}
	if response.Output.Code != "" {
		return core.ProviderTask{}, failurePolicy.NativeFailure(providerkit.FailureSubmit, response.Output.Code)
	}
	audio := response.Output.Audio
	if response.Output.FinishReason != "stop" {
		return invalid(providerkit.ResponseStatus)
	}
	if audio.Data != "" || !qwen31URL(audio.URL) || audio.ExpiresAt <= now.Unix() {
		return invalid(providerkit.ResponseMediaURL)
	}
	id := audio.ID
	if id == "" {
		id = response.RequestID
	}
	if id == "" || len(id) > 4096 || !utf8.ValidString(id) {
		return invalid(providerkit.ResponseTaskID)
	}
	duration := 0.0
	if audio.Duration != nil {
		duration = *audio.Duration
		if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) || r.Model == qwen31Next && duration > 240 {
			return invalid(providerkit.ResponseStatus)
		}
	}
	format := r.Options.Format
	if format == "" {
		format = "wav"
	}
	return core.ProviderTask{ID: id, ResultURL: audio.URL, Artifacts: []core.ProviderArtifact{{ID: "audio", MediaType: "audio", Format: format, URL: audio.URL, Duration: duration}}, Usage: qwen31Usage(r.Model, response.Usage)}, nil
}

func (a *Adapter) generateQwen31(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if err := qwen31Validate(a.baseURL, r); err != nil {
		return core.ProviderTask{}, err
	}
	var response qwen31Response
	if err := a.do(ctx, a.baseURL, a.apiKey, http.MethodPost, qwen31Path, qwen31Payload(r), false, &response); err != nil {
		return core.ProviderTask{}, err
	}
	// The worker persists this immediate result before downloading it. Do not
	// perform an eager download or poll/retry a non-streaming synthesis here.
	result, err := qwen31Result(r, response, time.Now())
	if err != nil {
		return core.ProviderTask{}, err
	}
	return a.sealQwen31Result(ctx, result, response.Output.Audio.ExpiresAt)
}
