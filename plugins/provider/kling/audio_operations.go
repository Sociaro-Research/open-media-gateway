package kling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

// These IDs identify native operations, not undocumented model_name values.
const (
	TextToAudio        = "kling-text-to-audio"
	VideoToAudio       = "kling-video-to-audio"
	TextToSpeech       = "kling-tts"
	AvatarImageToVideo = "kling-avatar-image-to-video"
)

// Preset IDs and language pairs are the Voice ID/Voice Language columns of the
// public Voice Guide linked by the official TTS protocol, reviewed 2026-10-31:
// https://docs.qingque.cn/s/home/eZQDvafJ4vXQkP8T9ZPvmye8S?identityId=2E1MlYrrPk4
// Preview filenames are not authoritative: several differ from their row's ID.
var audioPresetVoiceLanguages = map[string][]string{
	"AOT":                        {"en"},
	"PeppaPig_platform":          {"en"},
	"ai_chenjiahao_712":          {"zh", "en"},
	"ai_huangyaoshi_712":         {"zh", "en"},
	"ai_huangzhong_712":          {"en"},
	"ai_kaiya":                   {"zh", "en"},
	"ai_laoguowang_712":          {"zh", "en"},
	"ai_shatang":                 {"zh", "en"},
	"ai_taiwan_man2_speech02":    {"zh"},
	"calm_story1":                {"en"},
	"cartoon-boy-07":             {"zh", "en"},
	"cartoon-girl-01":            {"zh", "en"},
	"chaoshandashu_speech02":     {"zh"},
	"chat1_female_new-3":         {"zh", "en"},
	"chat_0407_5-1":              {"en"},
	"chengshu_jiejie":            {"zh", "en"},
	"chongqingxiaohuo_speech02":  {"zh"},
	"chuanmeizi_speech02":        {"zh"},
	"commercial_lady_en_f-v1":    {"en"},
	"daopianyansang-v1":          {"zh"},
	"diyinnansang_DB_CN_M_04-v2": {"zh"},
	"dongbeilaotie_speech02":     {"zh"},
	"genshin_kirara":             {"zh", "en"},
	"genshin_klee2":              {"zh", "en"},
	"genshin_vindi2":             {"zh", "en"},
	"girlfriend_1_speech02":      {"zh"},
	"girlfriend_2_speech02":      {"zh"},
	"girlfriend_4_speech02":      {"en"},
	"guanxiaofang-v2":            {"zh"},
	"heainainai_speech02":        {"zh", "en"},
	"laopopo_speech02":           {"zh", "en"},
	"mengwa-v1":                  {"zh"},
	"oversea_male1":              {"en"},
	"reader_en_m-v1":             {"en"},
	"tianjinjiejie_speech02":     {"zh"},
	"tianmeixuemei-v1":           {"zh"},
	"tiexin_nanyou":              {"zh"},
	"tiyuxi_xuedi":               {"zh"},
	"uk_boy1":                    {"en"},
	"uk_man2":                    {"en"},
	"uk_oldman3":                 {"zh"},
	"xianzhanggui_speech02":      {"zh"},
	"yizhipiannan-v1":            {"zh"},
	"you_pingjing":               {"zh", "en"},
	"zhinen_xuesheng":            {"zh", "en"},
	"zhuxi_speech02":             {"zh"},
}

func audioPresetVoices() []string {
	// Keep the protocol's example first as the shared speech form's default.
	voices := []string{"oversea_male1"}
	others := make([]string, 0, len(audioPresetVoiceLanguages)-1)
	for voice := range audioPresetVoiceLanguages {
		if voice != "oversea_male1" {
			others = append(others, voice)
		}
	}
	slices.Sort(others)
	return append(voices, others...)
}

func audioModels() []core.MediaModel {
	number := func(key, label string, min, max float64, required bool) core.ParameterField {
		return core.ParameterField{Key: key, Label: label, Type: "number", Min: &min, Max: &max, Required: required}
	}
	base := func(id, label, media string) core.MediaModel {
		return core.MediaModel{ID: id, DisplayName: label, MediaType: media, NoSeed: true, NoInstructions: true, NoLanguage: true, NoWatermark: true}
	}
	text := base(TextToAudio, "Kling · Text to audio", "audio")
	text.MaxPromptCharacters = 200
	text.Parameters = []core.ParameterField{number("duration", "Audio duration (seconds; one decimal place)", 3, 10, true)}
	text.ReferenceModes = []core.ReferenceMode{{ID: "none", Label: "Text only"}}
	text.Pricing = core.MediaModelPricing{Known: true, ReferencePriceUSD: 0.035, ReferenceDescription: "Published USD balance list price per request; both MP3 and WAV results are retained."}
	video := base(VideoToAudio, "Kling · Video to audio", "audio")
	video.NoPrompt, video.SupportsVideoInput, video.RequiresVideoInput, video.DurationFromInput = true, true, true, true
	video.Parameters = []core.ParameterField{{Key: "sound_effect_prompt", Label: "Sound effect prompt", Type: "text", MaxLength: 200}, {Key: "bgm_prompt", Label: "Background music prompt", Type: "text", MaxLength: 200}, {Key: "asmr_mode", Label: "ASMR mode", Type: "boolean"}}
	video.ReferenceModes = []core.ReferenceMode{{ID: "video", Label: "Source video", MinVideos: 1, MaxVideos: 1}}
	video.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 1, Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, MaxBytes: 100 << 20, MinDuration: 3, MaxDuration: 20}}
	video.Pricing = text.Pricing
	tts := base(TextToSpeech, "Kling · Text to speech", "speech")
	tts.NoLanguage, tts.SupportsVoice, tts.RequiresVoice = false, true, true
	tts.MaxPromptCharacters = 1000
	tts.Voices = audioPresetVoices()
	tts.ResourceInputSchemas = []core.ResourceInputSchema{{Kind: "voice", MaxCount: 1}}
	tts.Parameters = []core.ParameterField{number("voice_speed", "Speech rate (one decimal place)", 0.8, 2, false)}
	tts.ReferenceModes = []core.ReferenceMode{{ID: "none", Label: "Text only"}}
	tts.Pricing = core.MediaModelPricing{Known: true, ReferencePriceUSD: 0.007, ReferenceDescription: "Published USD balance list price per TTS request; a reviewed preset voice or one managed voice and its matching zh/en language are required."}
	avatar := base(AvatarImageToVideo, "Kling · Avatar image to video", "video")
	avatar.NoWatermark, avatar.PromptOptional, avatar.MaxPromptCharacters = false, true, 2500
	avatar.SupportsImageInput, avatar.RequiresImageInput, avatar.MinImageInputs, avatar.MaxImageInputs = true, true, 1, 1
	avatar.SupportsAudioInput, avatar.RequiresAudioInput, avatar.DurationFromInput, avatar.RatioMayBeInferred = true, true, true, true
	avatar.Parameters = []core.ParameterField{{Key: "mode", Label: "Generation mode", Type: "select", Choices: []string{"std", "pro"}, Default: "std"}}
	avatar.ReferenceModes = []core.ReferenceMode{{ID: "image_audio", Label: "Avatar image and speech audio", MinImages: 1, MaxImages: 1, MinAudios: 1, MaxAudios: 1}}
	avatar.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 2, Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png"}, MaxBytes: 10 << 20, MinDimension: 300, MinRatio: 0.4, MaxRatio: 2.5}, Audio: core.MediaFileLimits{ContentTypes: []string{"audio/mpeg", "audio/wav", "audio/mp4", "audio/aac"}, MaxBytes: 5 << 20, MinDuration: 2, MaxDuration: 300}}
	// The protocol specifies std/pro, while the price table specifies 720p/1080p.
	// No reviewed source links these axes, so an automatic quote is unavailable.
	avatar.Pricing = core.MediaModelPricing{ReferenceDescription: "Published Avatar prices are USD 0.056/s at 720p and USD 0.112/s at 1080p; the reviewed protocol does not map std/pro to those resolutions."}
	return []core.MediaModel{text, video, tts, avatar}
}

func isAudioOperation(id string) bool {
	switch id {
	case TextToAudio, VideoToAudio, TextToSpeech, AvatarImageToVideo:
		return true
	}
	return false
}
func audioModel(id string) (core.MediaModel, bool) {
	for _, m := range audioModels() {
		if m.ID == id {
			return m, true
		}
	}
	return core.MediaModel{}, false
}
func audioInvalid(message string) error { return fmt.Errorf("%w: %s", core.ErrOptionsInvalid, message) }
func audioTenth(n float64) bool {
	return !math.IsNaN(n) && !math.IsInf(n, 0) && math.Abs(n*10-math.Round(n*10)) < 1e-8
}

func (a *Adapter) validateAudioRequest(r core.MediaRequest) error {
	m, ok := audioModel(r.Model)
	if !ok {
		return audioInvalid("unknown Kling audio operation")
	}
	o := r.Options
	if o.OutputCount != 0 || o.GenerateAudio != nil || o.Seed != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || len(o.ReferenceVoices) > 0 || o.Resolution != "" || o.Ratio != "" || o.Duration != 0 {
		return audioInvalid("unsupported control for this Kling audio operation")
	}
	if o.Watermark != nil && m.NoWatermark {
		return audioInvalid("this Kling operation has no watermark control")
	}
	if r.Model != TextToSpeech && (o.Voice != "" || o.Language != "") {
		return audioInvalid("this Kling operation has no voice or language control")
	}
	if o.AudioURL != "" && len(o.AudioURLs) > 0 && o.AudioURL != o.AudioURLs[0] || o.VideoURL != "" && len(o.VideoURLs) > 0 && o.VideoURL != o.VideoURLs[0] {
		return audioInvalid("conflicting singular and array references")
	}
	if err := core.ValidateResourceReferences(m, o); err != nil {
		return err
	}
	if err := core.ValidateReferenceMode(m, o); err != nil {
		return err
	}
	if err := core.ValidateReferenceRoles(m, o); err != nil {
		return err
	}
	for _, raw := range append(append(slices.Clone(o.ImageURLs), o.Audios()...), o.Videos()...) {
		if !publicURL(raw) {
			return audioInvalid("Kling media references require accessible public HTTPS URLs")
		}
	}
	p, err := core.NormalizeParameters(m.Parameters, o.Parameters)
	if err != nil {
		return err
	}
	if m.NoPrompt {
		if r.Prompt != "" {
			return audioInvalid("use sound_effect_prompt and bgm_prompt for video-to-audio")
		}
	} else if utf8.RuneCountInString(r.Prompt) > m.MaxPromptCharacters || (!m.PromptOptional && strings.TrimSpace(r.Prompt) == "") {
		minimum := 1
		if m.PromptOptional {
			minimum = 0
		}
		return audioInvalid(fmt.Sprintf("Kling prompt must contain %d–%d characters", minimum, m.MaxPromptCharacters))
	}
	switch r.Model {
	case TextToAudio:
		if !audioTenth(p["duration"].(float64)) {
			return audioInvalid("audio duration supports one decimal place")
		}
	case TextToSpeech:
		if len(o.ResourceSnapshots) > 0 {
			if o.Voice != "" || !validTaskID(o.ResourceSnapshots[0].RemoteID) {
				return audioInvalid("select exactly one managed voice or reviewed preset voice")
			}
		} else if !slices.Contains(audioPresetVoiceLanguages[o.Voice], o.Language) {
			return audioInvalid("select a reviewed preset with its documented language; custom voices require a managed binding")
		}
		if o.Language != "zh" && o.Language != "en" {
			return audioInvalid("TTS requires the voice's zh or en language")
		}
		if n, ok := p["voice_speed"].(float64); ok && !audioTenth(n) {
			return audioInvalid("voice_speed supports one decimal place")
		}
	case VideoToAudio:
		if err := audioValidateEvidence(o, o.Videos()[0], "video", 3, 20); err != nil {
			return err
		}
	case AvatarImageToVideo:
		if err := audioValidateEvidence(o, o.Audios()[0], "audio", 2, 300); err != nil {
			return err
		}
	}
	return nil
}

func audioValidateEvidence(o core.MediaOptions, raw, kind string, min, max float64) error {
	// Shared admission inspects bytes and enforces the advertised file constraints.
	// Missing duration stays unknown rather than accepting a caller-asserted value.
	if e, ok := o.ReferenceMediaEvidence[raw]; ok && (e.Type != kind || e.Duration < min || e.Duration > max || math.IsNaN(e.Duration) || math.IsInf(e.Duration, 0)) {
		return audioInvalid("inspected Kling input duration or media type is unsupported")
	}
	return nil
}

func (a *Adapter) generateAudio(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if err := a.validateAudioRequest(r); err != nil {
		return core.ProviderTask{}, err
	}
	m, _ := audioModel(r.Model)
	p, _ := core.NormalizeParameters(m.Parameters, r.Options.Parameters)
	payload := map[string]any{}
	switch r.Model {
	case TextToAudio:
		payload["prompt"], payload["duration"] = r.Prompt, p["duration"]
	case VideoToAudio:
		payload["video_url"] = r.Options.Videos()[0]
		for _, k := range []string{"sound_effect_prompt", "bgm_prompt", "asmr_mode"} {
			if v, ok := p[k]; ok {
				payload[k] = v
			}
		}
	case AvatarImageToVideo:
		payload["image"], payload["sound_file"], payload["mode"] = r.Options.ImageURLs[0], r.Options.Audios()[0], p["mode"]
		if r.Prompt != "" {
			payload["prompt"] = r.Prompt
		}
		if r.Options.Watermark != nil {
			payload["watermark_info"] = map[string]bool{"enabled": *r.Options.Watermark}
		}
	case TextToSpeech:
		voiceID := r.Options.Voice
		if len(r.Options.ResourceSnapshots) == 1 {
			voiceID = r.Options.ResourceSnapshots[0].RemoteID
		}
		payload["text"], payload["voice_id"], payload["voice_language"] = r.Prompt, voiceID, r.Options.Language
		if v, ok := p["voice_speed"]; ok {
			payload["voice_speed"] = v
		}
		raw, err := a.do(ctx, http.MethodPost, "/v1/audio/tts", payload)
		if err != nil {
			return core.ProviderTask{}, err
		}
		result, err := normalizeAudioTask(r.Model, "", raw)
		if err != nil {
			return core.ProviderTask{}, err
		}
		if result.Status != core.ProviderSucceeded {
			return core.ProviderTask{}, errors.New("Kling TTS did not return a completed synchronous result; check provider history before resubmitting")
		}
		return core.ProviderTask{ResultURL: result.ResultURL, Artifacts: result.Artifacts, Usage: result.Usage}, nil
	}
	if r.TaskID != "" {
		payload["external_task_id"] = r.TaskID
	}
	return a.submitLegacy(ctx, r.Model, audioPath(r.Model), payload)
}

func audioPath(operation string) string {
	switch operation {
	case TextToAudio:
		return "/v1/audio/text-to-audio"
	case VideoToAudio:
		return "/v1/audio/video-to-audio"
	case AvatarImageToVideo:
		return "/v1/videos/avatar/image2video"
	}
	return ""
}
func (a *Adapter) getAudioTask(ctx context.Context, operation, remoteID string) (core.ProviderResult, error) {
	route := audioPath(operation)
	if route == "" || !validTaskID(remoteID) {
		return core.ProviderResult{}, errors.New("invalid Kling audio task operation or ID")
	}
	raw, err := a.do(ctx, http.MethodGet, route+"/"+url.PathEscape(remoteID), nil)
	if err != nil {
		return core.ProviderResult{}, err
	}
	return normalizeAudioTask(operation, remoteID, raw)
}

// The legacy routes return task_id/task_status/task_result, independently of the
// unified id/status/outputs schema. Keep the two decoders separate.
type audioLegacyTask struct {
	ID     string `json:"task_id"`
	Status string `json:"task_status"`
	Result struct {
		Audios []struct {
			ID          string `json:"id"`
			URL         string `json:"url"`
			MP3         string `json:"url_mp3"`
			WAV         string `json:"url_wav"`
			Duration    string `json:"duration"`
			MP3Duration string `json:"duration_mp3"`
			WAVDuration string `json:"duration_wav"`
		} `json:"audios"`
		Videos []struct {
			ID           string `json:"id"`
			URL          string `json:"url"`
			WatermarkURL string `json:"watermark_url"`
			Duration     string `json:"duration"`
		} `json:"videos"`
	} `json:"task_result"`
	Units   string `json:"final_unit_deduction"`
	Balance struct {
		Quota     string `json:"quota"`
		ListPrice string `json:"list_price"`
	} `json:"final_balance_deduction"`
}

func audioOutputDuration(raw string) (float64, error) {
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, errors.New("Kling returned an invalid media duration")
	}
	return n, nil
}
func audioURLFormat(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(u.Path), "."))
	if slices.Contains([]string{"mp3", "wav", "m4a", "aac", "mp4", "mov"}, ext) {
		return ext
	}
	return ""
}
func normalizeAudioTask(operation, expectedID string, raw json.RawMessage) (core.ProviderResult, error) {
	var task audioLegacyTask
	if !isAudioOperation(operation) || json.Unmarshal(raw, &task) != nil || !validTaskID(task.ID) || expectedID != "" && task.ID != expectedID {
		return core.ProviderResult{}, errors.New("Kling legacy task response is malformed or does not match the requested task")
	}
	switch task.Status {
	case "submitted":
		return core.ProviderResult{Status: core.ProviderQueued}, nil
	case "processing":
		return core.ProviderResult{Status: core.ProviderRunning}, nil
	case "failed":
		return core.ProviderResult{Status: core.ProviderFailed, Error: "Kling media generation failed; check provider task details"}, nil
	case "succeed":
	default:
		return core.ProviderResult{}, errors.New("Kling legacy task returned an unknown task status")
	}
	result := core.ProviderResult{Status: core.ProviderSucceeded}
	audioSeconds, videoSeconds := 0.0, 0.0
	seen := map[string]bool{}
	appendOutput := func(id, group, kind, format, raw, duration string) (float64, error) {
		seconds, err := audioOutputDuration(duration)
		if err != nil {
			return 0, err
		}
		if id == "" || seen[id] || !publicURL(raw) {
			return 0, errors.New("Kling completed task has invalid or duplicate media outputs")
		}
		seen[id] = true
		result.Artifacts = append(result.Artifacts, core.ProviderArtifact{ID: id, GroupID: group, MediaType: kind, Format: format, URL: raw, Duration: seconds})
		return seconds, nil
	}
	for _, v := range task.Result.Audios {
		if operation == AvatarImageToVideo {
			return core.ProviderResult{}, errors.New("Kling Avatar returned unexpected audio outputs")
		}
		if operation == TextToSpeech {
			if v.MP3 != "" || v.WAV != "" {
				return core.ProviderResult{}, errors.New("Kling TTS returned an unexpected audio schema")
			}
			n, err := appendOutput(v.ID, v.ID, "audio", audioURLFormat(v.URL), v.URL, v.Duration)
			if err != nil {
				return core.ProviderResult{}, err
			}
			audioSeconds += n
		} else {
			if v.URL != "" || v.MP3 == "" && v.WAV == "" {
				return core.ProviderResult{}, errors.New("Kling audio generation returned an unexpected audio schema")
			}
			n := 0.0
			for _, format := range []string{"mp3", "wav"} {
				raw, duration := v.MP3, v.MP3Duration
				if format == "wav" {
					raw, duration = v.WAV, v.WAVDuration
				}
				if raw == "" {
					continue
				}
				value, err := appendOutput(v.ID+":"+format, v.ID, "audio", format, raw, duration)
				if err != nil {
					return core.ProviderResult{}, err
				}
				if n == 0 {
					n = value
				}
			}
			if v.ID == "" {
				return core.ProviderResult{}, errors.New("Kling audio output has no ID")
			}
			audioSeconds += n
		}
	}
	for _, v := range task.Result.Videos {
		if operation != VideoToAudio && operation != AvatarImageToVideo {
			return core.ProviderResult{}, errors.New("Kling audio operation returned unexpected video outputs")
		}
		n, err := appendOutput(v.ID, v.ID, "video", audioURLFormat(v.URL), v.URL, v.Duration)
		if err != nil {
			return core.ProviderResult{}, err
		}
		videoSeconds += n
		if v.WatermarkURL != "" {
			if operation != AvatarImageToVideo {
				return core.ProviderResult{}, errors.New("Kling video-to-audio returned an unexpected watermark output")
			}
			if _, err := appendOutput(v.ID+":watermark", v.ID, "video", audioURLFormat(v.WatermarkURL), v.WatermarkURL, v.Duration); err != nil {
				return core.ProviderResult{}, err
			}
		}
	}
	if len(result.Artifacts) == 0 || (operation != AvatarImageToVideo && audioSeconds == 0) || (operation == AvatarImageToVideo && videoSeconds == 0) {
		return core.ProviderResult{}, errors.New("Kling completed task has no expected media outputs")
	}
	desired := "audio"
	if operation == AvatarImageToVideo {
		desired = "video"
	}
	for _, v := range result.Artifacts {
		if v.MediaType == desired {
			result.ResultURL = v.URL
			break
		}
	}
	usage := map[string]any{}
	if audioSeconds > 0 {
		usage["audio_duration"] = audioSeconds
	}
	if videoSeconds > 0 {
		usage["output_video_duration"] = videoSeconds
	}
	if decimalPattern.MatchString(task.Units) {
		usage["final_unit_deduction"] = task.Units
	}
	balance := map[string]string{}
	if decimalPattern.MatchString(task.Balance.Quota) {
		balance["quota"] = task.Balance.Quota
	}
	if decimalPattern.MatchString(task.Balance.ListPrice) {
		balance["list_price"] = task.Balance.ListPrice
	}
	if len(balance) > 0 {
		usage["final_balance_deduction"] = balance
	}
	// Legacy balance deductions have no explicit currency. Preserve the values
	// without promoting them to actual USD or converting package units.
	result.Usage = core.NormalizeProviderUsage(usage)
	return result, nil
}

func (a *Adapter) quoteAudio(r core.MediaRequest) (core.TaskCost, string, error) {
	if err := a.validateAudioRequest(r); err != nil {
		return core.TaskCost{}, "", err
	}
	rate := money.USD(35000)
	switch r.Model {
	case TextToSpeech:
		rate = 7000
	case AvatarImageToVideo:
		m, _ := audioModel(r.Model)
		parameters, err := core.NormalizeParameters(m.Parameters, r.Options.Parameters)
		if err != nil {
			return core.TaskCost{}, "", err
		}
		return a.quoteFixedOperation("avatar_" + parameters["mode"].(string))
	}
	now := time.Now().UTC()
	return core.TaskCost{ExpectedUSD: rate, EstimatedUnits: 1, Status: "estimated", QuotedAt: now, Pricing: core.PricingSnapshot{Currency: "USD", Unit: "request", Price: rate, Per: 1, Source: priceSource, Version: "2026-10-31", Region: "Singapore", CheckedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), EstimateDescription: "Published USD balance list price per request; account access, discounts and actual charges remain unverified."}}, "", nil
}
