package minimax

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func invalid(s string) error { return fmt.Errorf("%w: %s", core.ErrOptionsInvalid, s) }
func effectiveMode(m core.MediaModel, o core.MediaOptions) (core.ReferenceMode, error) {
	modes := core.ReferenceModesFor(m)
	for _, mode := range modes {
		if o.ReferenceMode != "" && mode.ID != o.ReferenceMode {
			continue
		}
		i, v, a := len(o.ImageURLs), len(o.Videos()), len(o.Audios())
		if i >= mode.MinImages && i <= mode.MaxImages && v >= mode.MinVideos && v <= mode.MaxVideos && a >= mode.MinAudios && a <= mode.MaxAudios {
			return mode, nil
		}
	}
	return core.ReferenceMode{}, invalid("reference counts or selected workflow are unsupported")
}
func (a *Adapter) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	if r.Model == ResourceUpload || r.Model == ResourceOutputFile || r.Model == ResourceSourceTask {
		_, e := a.validateFileCreation(ctx, r)
		return e
	}
	var managedErr error
	r, managedErr = a.expandManagedFiles(ctx, r)
	if managedErr != nil {
		return managedErr
	}
	if r.Model == RegenerationVideo {
		_, err := a.validateBaseRegeneration(ctx, r)
		return err
	}
	if r.Model == Regeneration {
		return a.validateRegeneration(ctx, r)
	}
	op, ok := operationFor(r.Model)
	if !ok {
		return invalid("unsupported MiniMax model or operation")
	}
	h3Input := op.kind == "h3" || op.kind == "context"
	m := describe(op)
	o := r.Options
	if o.OutputCount != 0 && (op.kind != "image" || o.OutputCount < 1 || o.OutputCount > 9) {
		return invalid("output count is supported only for 1–9 images")
	}
	if len(o.VideoURLs) > 0 && o.VideoURL != "" && o.VideoURL != o.VideoURLs[0] || len(o.AudioURLs) > 0 && o.AudioURL != "" && o.AudioURL != o.AudioURLs[0] {
		return invalid("conflicting singular and plural references")
	}
	mode, e := effectiveMode(m, o)
	if e != nil {
		return e
	}
	promptRequired := !m.PromptOptional || op.kind == "legacy" && mode.ID == "none"
	if m.NoPrompt && r.Prompt != "" || promptRequired && strings.TrimSpace(r.Prompt) == "" || utf8.RuneCountInString(minimaxPrompt(r.Prompt)) > m.MaxPromptCharacters {
		return invalid("prompt is empty, unsupported or exceeds the model character limit")
	}

	if o.Resolution != "" && !slices.Contains(m.Resolutions, o.Resolution) {
		return invalid("unsupported resolution")
	}
	if o.Ratio != "" && !slices.Contains(m.Ratios, o.Ratio) {
		return invalid("unsupported aspect ratio")
	}
	if o.Duration != 0 && (m.MaxDuration == 0 || o.Duration < m.MinDuration || o.Duration > m.MaxDuration || m.DurationStep > 0 && (o.Duration-m.MinDuration)%m.DurationStep != 0) {
		return invalid("unsupported duration")
	}
	if o.Seed != nil && m.NoSeed || o.Watermark != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || len(o.ReferenceVoices) > 0 || len(o.ResourceReferences) > 0 || len(o.ResourceSnapshots) > 0 {
		return invalid("unsupported MiniMax option")
	}
	if o.GenerateAudio != nil && (!m.SupportsAudio || !*o.GenerateAudio) {
		return invalid("this operation has no generated-audio switch")
	}
	if err := core.ValidateReferenceRoles(m, o); err != nil {
		return err
	}

	if h3Input && len(o.ImageURLs)+len(o.Videos())+len(o.Audios()) > 12 {
		return invalid("at most 12 input files are supported")
	}
	if h3Input && len(o.ImageURLs)+len(o.Videos())+len(o.Audios()) == 0 && o.Ratio == "adaptive" {
		return invalid("text-only H3 content requires a concrete ratio")
	}
	if len(mode.Ratios) > 0 && o.Ratio != "" && !slices.Contains(mode.Ratios, o.Ratio) {
		return invalid("aspect ratio does not match the selected workflow")
	}
	p, e := core.NormalizeParameters(m.Parameters, o.Parameters)
	if e != nil {
		return e
	}
	if op.kind == "agent" {
		inputCount := len(o.ImageURLs)
		if op.template.text {
			inputCount++
		}
		if texts, ok := p["additional_texts"].([]string); ok {
			inputCount += len(texts)
			for _, text := range texts {
				if strings.TrimSpace(text) == "" {
					return invalid("template text inputs must not be blank")
				}
			}
		}
		if inputCount > 64 {
			return invalid("Video Agent inputs exceed the Gateway aggregate ceiling of 64")
		}
	}
	res := resolution(m, o)
	dur := duration(m, o)
	if op.kind == "legacy" {
		if res == "1080p" && dur != 6 {
			return invalid("1080P legacy video supports only 6 seconds")
		}
		if res == "512p" && (mode.ID != "first_frame") {
			return invalid("512P is supported only for Hailuo-02 first-frame generation")
		}
		if _, fast := p["fast_pretreatment"]; fast && p["fast_pretreatment"] == true && mode.ID != "none" && mode.ID != "first_frame" {
			return invalid("fast_pretreatment is not documented for this workflow")
		}
		if p["fast_pretreatment"] == true && p["prompt_optimizer"] == false {
			return invalid("fast prompt preparation requires prompt optimization")
		}
	}
	if h3Input {
		if total, known := o.InputVideoDuration(); known && total > 15 {
			return invalid("total reference-video duration exceeds 15 seconds")
		}
		for _, raw := range o.Videos() {
			if evidence, ok := o.ReferenceMediaEvidence[raw]; ok && (evidence.Type != "video" || evidence.Duration < 2 || evidence.Duration > 15 || math.IsNaN(evidence.Duration) || math.IsInf(evidence.Duration, 0)) {
				return invalid("reference videos require 2–15 seconds per clip")
			}
			if evidence, ok := o.ReferenceMediaEvidence[raw]; ok && !core.AudioCodecsAllowed(evidence.AudioStreams, evidence.AudioCodecs, m.ReferenceMediaLimits.Video.AllowedAudioCodecs) {
				return invalid("reference-video embedded audio requires inspected AAC/MP3 or verified silence")
			}
		}
	}
	if h3Input {
		total := 0.
		for _, raw := range o.Audios() {
			if evidence, ok := o.ReferenceMediaEvidence[raw]; ok {
				if evidence.Type != "audio" || evidence.Duration < 2 || evidence.Duration > 15 || math.IsNaN(evidence.Duration) || math.IsInf(evidence.Duration, 0) {
					return invalid("reference audio requires 2–15 seconds per clip")
				}
				total += evidence.Duration
			}
		}
		if total > 15 {
			return invalid("total reference-audio duration exceeds 15 seconds")
		}
	}
	if h3Input && op.id == "MiniMax-H3-Lite" && mode.ID == "references" && o.Ratio == "adaptive" {
		return invalid("H3 Lite references require a concrete aspect ratio")
	}
	if op.kind == "image" {
		w, wok := p["width"]
		h, hok := p["height"]
		if wok != hok {
			return invalid("custom image width and height must be supplied together")
		}
		if wok && (int(w.(float64))%8 != 0 || int(h.(float64))%8 != 0) {
			return invalid("custom image dimensions must be divisible by 8")
		}
		if wok && o.Ratio != "" {
			return invalid("choose custom dimensions or aspect ratio; aspect ratio would override the dimensions")
		}
	}
	for _, item := range []struct {
		urls  []string
		kind  string
		limit int
	}{{o.ImageURLs, "image", 30_000_000}, {o.Videos(), "video", 50_000_000}, {o.Audios(), "audio", 15_000_000}} {
		for _, s := range item.urls {
			limit := item.limit
			if op.kind == "image" {
				limit = 10_000_000 - 1
			} else if op.kind == "legacy" || op.kind == "agent" {
				limit = 20_000_000 - 1
			}
			if strings.HasPrefix(s, "asset://") && h3Input {
				if _, e := a.resolveFileInput(ctx, s, item.kind); e != nil {
					return e
				}
				continue
			}
			if strings.HasPrefix(s, "data:") && item.kind == "image" && m.ReferenceMediaLimits != nil {
				header, _, _ := strings.Cut(s, ",")
				typ := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
				if !slices.Contains(m.ReferenceMediaLimits.Image.ContentTypes, typ) {
					return invalid("unsupported inline image format")
				}
			}
			if !validInput(s, item.kind, limit) {
				return invalid("references require HTTPS URLs or documented, bounded Base64 data URLs; raw provider file IDs are not owner-scoped")
			}
		}
	}
	return nil
}
func validInput(s, kind string, limit int) bool {
	if publicURL(s) {
		return true
	}
	header, b64, ok := strings.Cut(s, ",")
	if !ok || !strings.HasSuffix(header, ";base64") || len(b64) > base64.StdEncoding.EncodedLen(limit) {
		return false
	}
	formats := map[string][]string{"image": {"image/jpeg", "image/png", "image/webp", "image/heic", "image/heif"}, "video": {"video/mp4"}, "audio": {"audio/wav", "audio/mpeg", "audio/mp3"}}
	typ := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	if !strings.HasPrefix(header, "data:") || !slices.Contains(formats[kind], typ) {
		return false
	}
	b, e := base64.StdEncoding.DecodeString(b64)
	return e == nil && len(b) > 0 && len(b) <= limit
}
func duration(m core.MediaModel, o core.MediaOptions) int {
	if o.Duration > 0 {
		return o.Duration
	}
	return m.DefaultDuration
}
func resolution(m core.MediaModel, o core.MediaOptions) string {
	if o.Resolution != "" {
		return o.Resolution
	}
	if len(m.Resolutions) > 0 {
		return m.Resolutions[0]
	}
	return ""
}
func wireResolution(s string) string { return strings.ToUpper(s) }

func (a *Adapter) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if a.requestConnectionScope(ctx) == "" {
		return core.ProviderTask{}, errors.New("MiniMax provider connection identity is not bound")
	}
	if r.Model == ResourceUpload || r.Model == ResourceOutputFile || r.Model == ResourceSourceTask {
		return a.generateFileResource(ctx, r)
	}
	var managedErr error
	r, managedErr = a.expandManagedFiles(ctx, r)
	if managedErr != nil {
		return core.ProviderTask{}, managedErr
	}
	if e := a.ValidateRequest(ctx, r); e != nil {
		return core.ProviderTask{}, e
	}
	op, _ := operationFor(r.Model)
	if op.kind == "agent" {
		texts := []string{}
		if op.template.text {
			texts = append(texts, r.Prompt)
			parameters, _ := core.NormalizeParameters(describe(op).Parameters, r.Options.Parameters)
			if extras, ok := parameters["additional_texts"].([]string); ok {
				texts = append(texts, extras...)
			}
		}
		return a.CreateVideoAgent(ctx, r.Model, r.Options.ImageURLs, texts)
	}
	if op.kind == "base_regeneration" {
		return a.generateBaseRegeneration(ctx, r)
	}
	if op.kind == "regeneration" {
		source, e := a.regenerationSource(ctx, r)
		if e != nil {
			return core.ProviderTask{}, e
		}
		token, e := a.RegenerateTask(ctx, source, r.Options.Watermark)
		if e != nil {
			return core.ProviderTask{}, e
		}
		return core.ProviderTask{ID: token}, nil
	}
	if op.kind == "context" {
		r.Model = "MiniMax-H3"
		token, e := a.CreateContext(ctx, r)
		if e != nil {
			return core.ProviderTask{}, e
		}
		return core.ProviderTask{ID: token}, nil
	}
	if op.kind == "image" {
		count := imageCount(r.Options)
		format := "url"
		images, e := a.GenerateImages(ctx, r, count, format)
		if e != nil {
			return core.ProviderTask{}, e
		}
		artifacts := make([]core.ProviderArtifact, 0, images.Success)
		for i, u := range images.URLs {
			artifacts = append(artifacts, core.ProviderArtifact{ID: "image-" + strconv.Itoa(i+1), MediaType: "image", URL: u})
		}

		if len(artifacts) == 0 {
			return core.ProviderTask{}, errors.New("MiniMax returned no image artifacts")
		}
		return core.ProviderTask{ID: a.seal(ctx, handle{ID: "0", Kind: "immediate", Model: r.Model}), ResultURL: artifacts[0].URL, Artifacts: artifacts, Usage: images.Usage}, nil
	}
	r, e := a.resolveRequestFiles(ctx, r)
	if e != nil {
		return core.ProviderTask{}, e
	}
	payload, e := requestBody(op, r)
	if e != nil {
		return core.ProviderTask{}, e
	}
	var out struct {
		ID string `json:"task_id"`
	}
	if e = a.json(ctx, "POST", op.endpoint, payload, &out); e != nil {
		return core.ProviderTask{}, e
	}
	if !numericID.MatchString(out.ID) {
		return core.ProviderTask{}, errors.New("MiniMax submission returned an invalid task ID")
	}
	return core.ProviderTask{ID: a.seal(ctx, handle{ID: out.ID, Model: r.Model, Kind: op.kind, Duration: duration(describe(op), r.Options), ExpiresAt: taskExpiry(op.kind), InputHash: contentHash(payload["content"])})}, nil
}
func requestBody(op operation, r core.MediaRequest) (map[string]any, error) {
	m := describe(op)
	o := r.Options
	if len(o.VideoURLs) > 0 && o.VideoURL != "" && o.VideoURL != o.VideoURLs[0] || len(o.AudioURLs) > 0 && o.AudioURL != "" && o.AudioURL != o.AudioURLs[0] {
		return nil, invalid("conflicting singular and plural references")
	}
	mode, e := effectiveMode(m, o)
	if e != nil {
		return nil, e
	}
	p, e := core.NormalizeParameters(m.Parameters, o.Parameters)
	if e != nil {
		return nil, e
	}
	payload := map[string]any{"model": op.upstream}
	switch op.kind {
	case "h3":
		payload["content"] = contentFor(minimaxPrompt(r.Prompt), o, mode)
		payload["resolution"] = wireResolution(resolution(m, o))
		payload["duration"] = duration(m, o)
		ratio := o.Ratio
		if ratio == "" {
			ratio = "16:9"
			if len(o.ImageURLs)+len(o.Videos())+len(o.Audios()) > 0 && mode.ID != "none" && !(op.id == "MiniMax-H3-Lite" && mode.ID == "references") {
				ratio = "adaptive"
			}
		}
		payload["ratio"] = ratio
		if op.id == "MiniMax-H3-Max" {
			payload["extra"] = map[string]any{"prompt_expansion_mode": p["prompt_expansion_mode"]}
		}
	case "legacy":
		if r.Prompt != "" {
			payload["prompt"] = minimaxPrompt(r.Prompt)
		}
		payload["prompt_optimizer"] = p["prompt_optimizer"]
		if op.id != "S2V-01" {
			payload["resolution"] = wireResolution(resolution(m, o))
			payload["duration"] = duration(m, o)
		}
		if v, ok := p["fast_pretreatment"]; ok && (mode.ID == "none" || mode.ID == "first_frame") {
			payload["fast_pretreatment"] = v
		}
		for i, u := range o.ImageURLs {
			role := core.ReferenceImageRole(mode, i+1, len(o.ImageURLs))
			switch role {
			case "first_frame":
				payload["first_frame_image"] = u
			case "last_frame":
				payload["last_frame_image"] = u
			default:
				payload["subject_reference"] = []any{map[string]any{"type": "character", "image": []string{u}}}
			}
		}

	}
	return payload, nil
}
func contentFor(prompt string, o core.MediaOptions, mode core.ReferenceMode) []any {
	content := []any{map[string]any{"type": "text", "text": prompt}}
	for i, u := range o.ImageURLs {
		role := core.ReferenceImageRole(mode, i+1, len(o.ImageURLs))
		if role == "reference" {
			role = "reference_image"
		}
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": u}, "role": role})
	}
	for _, u := range o.Videos() {
		content = append(content, map[string]any{"type": "video_url", "video_url": map[string]string{"url": u}, "role": "reference_video"})
	}
	for _, u := range o.Audios() {
		content = append(content, map[string]any{"type": "audio_url", "audio_url": map[string]string{"url": u}, "role": "reference_audio"})
	}
	return content
}

// ImageBatch retains every upstream result and safety counter. The media worker's
// singular result contract uses Generate (n=1); callers supporting a result set
// must archive every member before exposing it as durable content.
type ImageBatch struct {
	URLs, Base64    []string
	Success, Failed int
	Usage           *core.ProviderUsage
}

func (a *Adapter) GenerateImages(ctx context.Context, r core.MediaRequest, count int, format string) (ImageBatch, error) {
	if a.requestConnectionScope(ctx) == "" {
		return ImageBatch{}, errors.New("MiniMax provider connection identity is not bound")
	}
	if e := a.ValidateRequest(ctx, r); e != nil {
		return ImageBatch{}, e
	}
	op, ok := operationFor(r.Model)
	if !ok || op.kind != "image" || count < 1 || count > 9 || r.Options.OutputCount > 0 && r.Options.OutputCount != count || (format != "url" && format != "base64") {
		return ImageBatch{}, invalid("invalid image batch model, size or response format")
	}
	m := describe(op)
	p, _ := core.NormalizeParameters(m.Parameters, r.Options.Parameters)
	payload := map[string]any{"model": op.upstream, "prompt": minimaxPrompt(r.Prompt), "n": count, "response_format": format, "prompt_optimizer": p["prompt_optimizer"]}
	if w, ok := p["width"]; ok {
		payload["width"] = w
		payload["height"] = p["height"]
	} else {
		ratio := r.Options.Ratio
		if ratio == "" {
			ratio = "1:1"
		}
		payload["aspect_ratio"] = ratio
	}
	if r.Options.Seed != nil {
		payload["seed"] = *r.Options.Seed
	}
	if len(r.Options.ImageURLs) > 0 {
		payload["subject_reference"] = []any{map[string]any{"type": "character", "image_file": r.Options.ImageURLs[0]}}
	}
	var out struct {
		Data struct {
			URLs   []string `json:"image_urls"`
			Base64 []string `json:"image_base64"`
		} `json:"data"`
		Metadata struct {
			Success json.RawMessage `json:"success_count"`
			Failed  json.RawMessage `json:"failed_count"`
		} `json:"metadata"`
	}
	if e := a.json(ctx, "POST", op.endpoint, payload, &out); e != nil {
		return ImageBatch{}, e
	}
	success, e := integerValue(out.Metadata.Success)
	if e != nil {
		return ImageBatch{}, errors.New("MiniMax image success count is invalid")
	}
	failed, e := integerValue(out.Metadata.Failed)
	if e != nil {
		return ImageBatch{}, errors.New("MiniMax image failure count is invalid")
	}
	if format == "url" && len(out.Data.Base64) > 0 || format == "base64" && len(out.Data.URLs) > 0 {
		return ImageBatch{}, errors.New("MiniMax returned mixed image encodings")
	}
	images := len(out.Data.URLs)
	if format == "base64" {
		images = len(out.Data.Base64)
	}
	if images == 0 || images > count || success != images || success+failed != count {
		return ImageBatch{}, errors.New("MiniMax image result set or safety counters are incomplete")
	}
	for _, u := range out.Data.URLs {
		if !publicURL(u) {
			return ImageBatch{}, errors.New("MiniMax returned an invalid image result URL")
		}
	}
	for _, s := range out.Data.Base64 {
		if _, e := base64.StdEncoding.DecodeString(s); e != nil || s == "" {
			return ImageBatch{}, errors.New("MiniMax returned invalid Base64 image content")
		}
	}
	raw, _ := json.Marshal(out.Metadata)
	return ImageBatch{URLs: out.Data.URLs, Base64: out.Data.Base64, Success: success, Failed: failed, Usage: &core.ProviderUsage{Raw: raw, Metrics: []core.UsageMetric{{Name: "image_count", Unit: "image", Quantity: float64(success)}}}}, nil
}
func integerValue(b json.RawMessage) (int, error) {
	var n json.Number
	if json.Unmarshal(b, &n) != nil {
		return 0, errors.New("invalid integer")
	}
	v, e := n.Int64()
	if e != nil || v < 0 || v > 1_000_000 {
		return 0, errors.New("invalid integer")
	}
	return int(v), nil
}

func contentHash(content any) string {
	if content == nil {
		return ""
	}
	b, _ := json.Marshal(content)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (a *Adapter) resolveRequestFiles(ctx context.Context, r core.MediaRequest) (core.MediaRequest, error) {
	o := r.Options
	var e error
	resolve := func(values []string, kind string) ([]string, error) {
		out := make([]string, len(values))
		for i, v := range values {
			out[i], e = a.resolveFileInput(ctx, v, kind)
			if e != nil {
				return nil, e
			}
		}
		return out, nil
	}
	if o.ImageURLs, e = resolve(o.ImageURLs, "image"); e != nil {
		return r, e
	}
	if o.VideoURLs, e = resolve(o.Videos(), "video"); e != nil {
		return r, e
	}
	o.VideoURL = ""
	if o.AudioURLs, e = resolve(o.Audios(), "audio"); e != nil {
		return r, e
	}
	o.AudioURL = ""
	r.Options = o
	return r, nil
}

// Shared UI addresses become the ordinary reference language shown in MiniMax's
// official examples. Vendor camera commands and arbitrary @names remain intact.
var uiMention = regexp.MustCompile(`(?i)(^|[^\pL\pN_@])@(image|video|audio)_?([1-9][0-9]*)\b`)

func minimaxPrompt(prompt string) string {
	return uiMention.ReplaceAllStringFunc(prompt, func(value string) string {
		p := uiMention.FindStringSubmatch(value)
		return p[1] + "reference " + strings.ToLower(p[2]) + " " + p[3]
	})
}

func imageCount(o core.MediaOptions) int {
	if o.OutputCount > 0 {
		return o.OutputCount
	}
	return 1
}

func taskExpiry(kind string) int64 {
	if kind == "h3" {
		return time.Now().Add(7 * 24 * time.Hour).Unix()
	}
	return 0
}
