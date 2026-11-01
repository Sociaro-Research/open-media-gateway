package alibaba

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
)

const (
	imagePath           = "/services/aigc/image-generation/generation"
	legacyTextImagePath = "/services/aigc/text2image/image-synthesis"
	imageEditPath       = "/services/aigc/image2image/image-synthesis"
	videoPath           = "/services/aigc/video-generation/video-synthesis"
	ttsPath             = "/services/aigc/multimodal-generation/generation"
	musicPath           = "/services/audio/music/generation"
)

// Adapter calls Alibaba Cloud Model Studio through the native DashScope HTTP API.
// Video and legacy image jobs use the asynchronous task API. Synchronous-only
// image models, HTTP TTS and Fun-Music return immediate results.
type Adapter struct {
	apiKey       string
	baseURL      string
	musicAPIKey  string
	musicBaseURL string
	musicEnabled bool
	client       *http.Client
	modelsMu     sync.Mutex
	models       []core.MediaModel
	modelsAt     time.Time
}

func New(apiKey, baseURL, musicAPIKey, musicBaseURL string, client *http.Client) (*Adapter, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("DashScope API key is required")
	}
	baseURL, err := validBaseURL(baseURL, "DashScope")
	if err != nil {
		return nil, err
	}
	explicitMusicKey := strings.TrimSpace(musicAPIKey) != ""
	if !explicitMusicKey {
		musicAPIKey = apiKey
	}
	if strings.TrimSpace(musicBaseURL) == "" {
		musicBaseURL = baseURL
	}
	musicBaseURL, err = validBaseURL(musicBaseURL, "DashScope music")
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	musicHost := strings.ToLower(mustURLHost(musicBaseURL))
	musicEnabled := explicitMusicKey || musicHost == "alibaba.aliyuncs.com" || strings.Contains(musicHost, "cn-beijing")
	return &Adapter{apiKey: apiKey, baseURL: baseURL, musicAPIKey: musicAPIKey, musicBaseURL: musicBaseURL, musicEnabled: musicEnabled, client: client}, nil
}

func mustURLHost(value string) string {
	parsed, _ := url.Parse(value)
	return parsed.Hostname()
}

func validBaseURL(value, label string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(value), "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("%s base URL must be an absolute HTTPS URL", label)
	}
	return parsed.String(), nil
}

func (a *Adapter) Name() string { return "alibaba" }

func (a *Adapter) CheckCredentials(ctx context.Context) error {
	_, err := a.listRemoteModels(ctx)
	return err
}

func (a *Adapter) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	a.modelsMu.Lock()
	if len(a.models) > 0 && time.Since(a.modelsAt) < 10*time.Minute {
		models := cloneModels(a.models)
		a.modelsMu.Unlock()
		return models, nil
	}
	a.modelsMu.Unlock()

	models := mediaModels(a.musicEnabled)
	if remote, err := a.listRemoteModels(ctx); err == nil && len(remote) > 0 {
		models = mergeModels(remote, musicModels(a.musicEnabled))
	}
	slices.SortStableFunc(models, func(a, b core.MediaModel) int {
		if a.MediaType != b.MediaType {
			return strings.Compare(a.MediaType, b.MediaType)
		}
		return strings.Compare(a.ID, b.ID)
	})
	a.modelsMu.Lock()
	a.models = cloneModels(models)
	a.modelsAt = time.Now()
	a.modelsMu.Unlock()
	return models, nil
}

func cloneModels(models []core.MediaModel) []core.MediaModel {
	result := make([]core.MediaModel, len(models))
	copy(result, models)
	return result
}

func mergeModels(groups ...[]core.MediaModel) []core.MediaModel {
	seen := make(map[string]bool)
	var result []core.MediaModel
	for _, group := range groups {
		for _, model := range group {
			if model.ID != "" && !seen[model.ID] {
				seen[model.ID] = true
				result = append(result, model)
			}
		}
	}
	return result
}

func (a *Adapter) listRemoteModels(ctx context.Context) ([]core.MediaModel, error) {
	var result []core.MediaModel
	for page := 1; ; page++ {
		var response struct {
			Output struct {
				Total  int `json:"total"`
				Models []struct {
					ID           string   `json:"model"`
					Name         string   `json:"name"`
					Capabilities []string `json:"capabilities"`
					Inference    struct {
						Request  []string `json:"request_modality"`
						Response []string `json:"response_modality"`
					} `json:"inference_metadata"`
				} `json:"models"`
			} `json:"output"`
		}
		path := fmt.Sprintf("/models?page_no=%d&page_size=200", page)
		if err := a.do(ctx, a.baseURL, a.apiKey, http.MethodGet, path, nil, false, &response); err != nil {
			return nil, err
		}
		for _, item := range response.Output.Models {
			model, ok := describeRemoteModel(item.ID, item.Name, item.Capabilities, item.Inference.Request, item.Inference.Response)
			if ok {
				model.Metadata = &core.ModelMetadata{Facts: []core.ModelFact{{Label: "API capabilities", Value: strings.Join(item.Capabilities, ", ")}, {Label: "API input modalities", Value: strings.Join(item.Inference.Request, ", ")}, {Label: "API output modalities", Value: strings.Join(item.Inference.Response, ", ")}}}
				result = append(result, model)
			}
		}
		if len(response.Output.Models) == 0 || page*200 >= response.Output.Total || response.Output.Total == 0 {
			break
		}
	}
	return mergeModels(result), nil
}

func (a *Adapter) Generate(ctx context.Context, request core.MediaRequest) (core.ProviderTask, error) {
	model, ok := a.model(ctx, request.Model)
	if !ok {
		return core.ProviderTask{}, fmt.Errorf("unsupported DashScope media model %q", request.Model)
	}
	switch model.MediaType {
	case "video":
		if err := a.ValidateRequest(ctx, request); err != nil {
			return core.ProviderTask{}, err
		}
		if request.Model == "wan2.2-s2v" {
			return a.createAsync(ctx, "/services/aigc/image2video/video-synthesis", map[string]any{"model": request.Model, "input": map[string]any{"image_url": request.Options.ImageURLs[0], "audio_url": request.Options.Audios()[0]}, "parameters": map[string]any{"resolution": strings.ToUpper(request.Options.Resolution)}})
		}
		if strings.Contains(request.Model, "animate") {
			return a.createAsync(ctx, "/services/aigc/image2video/video-synthesis", animatePayload(request))
		}
		if strings.Contains(request.Model, "-kf2v") {
			return a.createAsync(ctx, "/services/aigc/image2video/video-synthesis", videoPayload(request))
		}
		return a.createAsync(ctx, videoPath, videoPayload(request))
	case "image":
		if strings.HasPrefix(request.Model, "qwen-mt-image") {
			if err := a.ValidateRequest(ctx, request); err != nil {
				return core.ProviderTask{}, err
			}
			source, _ := request.Options.Parameters["source_lang"].(string)
			if strings.TrimSpace(source) == "" {
				source = "auto"
			}
			return a.createAsync(ctx, imageEditPath, map[string]any{"model": request.Model, "input": map[string]any{"image_url": request.Options.ImageURLs[0], "source_lang": source, "target_lang": request.Options.Parameters["target_lang"]}})
		}
		path, payload := imageRequest(request)
		if synchronousImage(request.Model) {
			return a.createImmediate(ctx, a.baseURL, a.apiKey, ttsPath, payload)
		}
		return a.createAsync(ctx, path, payload)
	case "speech":
		if isBatchSpeech(request.Model) {
			return a.createImmediate(ctx, a.baseURL, a.apiKey, "/services/audio/tts/SpeechSynthesizer", map[string]any{"model": request.Model, "input": map[string]any{"text": request.Prompt, "voice": request.Options.Voice, "format": "wav", "sample_rate": 24000}})
		}
		return a.createImmediate(ctx, a.baseURL, a.apiKey, ttsPath, ttsPayload(request))
	case "music":
		return a.createImmediate(ctx, a.musicBaseURL, a.musicAPIKey, musicPath, musicPayload(request))
	default:
		return core.ProviderTask{}, fmt.Errorf("unsupported DashScope media type %q", model.MediaType)
	}
}

func (a *Adapter) model(ctx context.Context, id string) (core.MediaModel, bool) {
	if modelcatalog.IsInactive("alibaba", id, time.Now()) {
		return core.MediaModel{}, false
	}
	a.modelsMu.Lock()
	for _, model := range a.models {
		if model.ID == id {
			a.modelsMu.Unlock()
			return model, true
		}
	}
	a.modelsMu.Unlock()
	if model, ok := describeDashModel(id, id); ok {
		return model, true
	}

	// Gateway and worker are separate processes. The worker therefore cannot
	// rely on the catalog cache populated by the HTTP process.
	if models, err := a.ListModels(ctx); err == nil {
		for _, model := range models {
			if model.ID == id {
				return model, true
			}
		}
	}
	return core.MediaModel{}, false
}

func (a *Adapter) createAsync(ctx context.Context, path string, payload any) (core.ProviderTask, error) {
	var response dashResponse
	if err := a.do(ctx, a.baseURL, a.apiKey, http.MethodPost, path, payload, true, &response); err != nil {
		return core.ProviderTask{}, err
	}
	if response.Output.TaskID == "" {
		return core.ProviderTask{}, responseError("DashScope task response has no task_id", response)
	}
	return core.ProviderTask{ID: response.Output.TaskID, Usage: core.NormalizeProviderUsage(response.Usage)}, nil
}

func (a *Adapter) createImmediate(ctx context.Context, baseURL, apiKey, path string, payload any) (core.ProviderTask, error) {
	var response dashResponse
	if err := a.do(ctx, baseURL, apiKey, http.MethodPost, path, payload, false, &response); err != nil {
		return core.ProviderTask{}, err
	}
	resultURL := response.resultURL()
	if resultURL == "" {
		return core.ProviderTask{}, responseError("DashScope response has no media URL", response)
	}
	id := response.RequestID
	if response.Output.Audio.ID != "" {
		id = response.Output.Audio.ID
	}
	if id == "" {
		id = "immediate"
	}
	return core.ProviderTask{ID: id, ResultURL: resultURL, Usage: core.NormalizeProviderUsage(response.Usage)}, nil
}

func (a *Adapter) GetTask(ctx context.Context, providerTaskID string) (core.ProviderResult, error) {
	var response dashResponse
	if err := a.do(ctx, a.baseURL, a.apiKey, http.MethodGet, "/tasks/"+url.PathEscape(providerTaskID), nil, false, &response); err != nil {
		return core.ProviderResult{}, err
	}
	switch strings.ToUpper(response.Output.TaskStatus) {
	case "PENDING":
		return core.ProviderResult{Status: core.ProviderQueued, Usage: core.NormalizeProviderUsage(response.Usage)}, nil
	case "RUNNING":
		return core.ProviderResult{Status: core.ProviderRunning, Usage: core.NormalizeProviderUsage(response.Usage)}, nil
	case "SUCCEEDED":
		resultURL := response.resultURL()
		if resultURL == "" {
			return core.ProviderResult{}, fmt.Errorf("DashScope task succeeded without a media URL")
		}
		return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: resultURL, Usage: core.NormalizeProviderUsage(response.Usage)}, nil
	case "FAILED", "CANCELED", "CANCELLED", "UNKNOWN":
		message := response.Output.Message
		if message == "" {
			message = response.Message
		}
		if message == "" {
			message = "DashScope task ended with status " + response.Output.TaskStatus
		}
		return core.ProviderResult{Status: core.ProviderFailed, Error: message, Usage: core.NormalizeProviderUsage(response.Usage)}, nil
	default:
		return core.ProviderResult{}, fmt.Errorf("unknown DashScope task status %q", response.Output.TaskStatus)
	}
}

type dashResponse struct {
	RequestID string         `json:"request_id"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Usage     map[string]any `json:"usage"`
	Output    struct {
		TaskID     string `json:"task_id"`
		TaskStatus string `json:"task_status"`
		VideoURL   string `json:"video_url"`
		ImageURL   string `json:"image_url"`
		Code       string `json:"code"`
		Message    string `json:"message"`
		Audio      struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"audio"`
		Results json.RawMessage `json:"results"`
		Choices []struct {
			Message struct {
				Content []struct {
					Image string `json:"image"`
					URL   string `json:"url"`
				} `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	} `json:"output"`
}

func (r dashResponse) resultURL() string {
	if r.Output.ImageURL != "" {
		return r.Output.ImageURL
	}
	if r.Output.VideoURL != "" {
		return r.Output.VideoURL
	}
	if r.Output.Audio.URL != "" {
		return r.Output.Audio.URL
	}
	if len(r.Output.Results) > 0 {
		var images []struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(r.Output.Results, &images) == nil && len(images) > 0 {
			return images[0].URL
		}
		var video struct {
			VideoURL string `json:"video_url"`
		}
		if json.Unmarshal(r.Output.Results, &video) == nil && video.VideoURL != "" {
			return video.VideoURL
		}
	}
	for _, choice := range r.Output.Choices {
		for _, content := range choice.Message.Content {
			if content.Image != "" {
				return content.Image
			}
			if content.URL != "" {
				return content.URL
			}
		}
	}
	return ""
}

func responseError(fallback string, response dashResponse) error {
	message := strings.TrimSpace(response.Message)
	if message == "" {
		message = strings.TrimSpace(response.Output.Message)
	}
	if message == "" {
		message = fallback
	}
	code := response.Code
	if code == "" {
		code = response.Output.Code
	}
	if code != "" {
		return fmt.Errorf("%s: %s", code, message)
	}
	return fmt.Errorf("%s", message)
}

func (a *Adapter) do(ctx context.Context, baseURL, apiKey, method, path string, input any, async bool, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	if async {
		request.Header.Set("X-DashScope-Async", "enable")
	}
	response, err := httpclient.Do(a.client, request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	const limit = 2 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return fmt.Errorf("read DashScope response: %w", err)
	}
	if len(data) > limit {
		return fmt.Errorf("DashScope response exceeds %d bytes", limit)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("DashScope HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode DashScope response: %w", err)
	}
	return nil
}

func videoPayload(request core.MediaRequest) any {
	if strings.HasPrefix(request.Model, "vidu/") {
		parameters := map[string]any{"resolution": strings.ToUpper(request.Options.Resolution), "duration": request.Options.Duration}
		if request.Options.Seed != nil {
			parameters["seed"] = *request.Options.Seed
		}
		if request.Options.Watermark != nil {
			parameters["watermark"] = *request.Options.Watermark
		}
		if !strings.Contains(request.Model, "-drama_") {
			parameters["audio"] = request.Options.GenerateAudio != nil && *request.Options.GenerateAudio
		}
		if !strings.Contains(request.Model, "img2video") {
			parameters["size"] = viduVideoSize(request.Options.Resolution, request.Options.Ratio)
		}
		media := []map[string]string{}
		for _, u := range request.Options.ImageURLs {
			media = append(media, map[string]string{"type": "image", "url": u})
		}
		return map[string]any{"model": request.Model, "input": map[string]any{"prompt": request.Prompt, "media": media}, "parameters": parameters}
	}
	if strings.Contains(request.Model, "vace-plus") {
		roles := make([]string, len(request.Options.ImageURLs))
		for i := range roles {
			roles[i] = "obj"
		}
		parameters := map[string]any{"obj_or_bg": roles, "size": legacyVideoSize(request.Options.Resolution, request.Options.Ratio)}
		if request.Options.Seed != nil {
			parameters["seed"] = *request.Options.Seed
		}
		return map[string]any{"model": request.Model, "input": map[string]any{"function": "image_reference", "prompt": request.Prompt, "ref_images_url": request.Options.ImageURLs}, "parameters": parameters}
	}
	lower := contractID(strings.ToLower(request.Model))
	parameters := map[string]any{
		"resolution": strings.ToUpper(request.Options.Resolution),
		"ratio":      request.Options.Ratio,
		"duration":   request.Options.Duration,
	}
	// Omission uses HappyHorse's documented 16:9 default. Do not send an empty
	// ratio or substitute for an explicit value; native validation checks it.
	if lower == "happyhorse-1.1-r2v" && request.Options.Ratio == "" {
		delete(parameters, "ratio")
	}
	if request.Options.Watermark != nil {
		parameters["watermark"] = *request.Options.Watermark
	}
	if request.Options.GenerateAudio != nil {
		parameters["audio"] = *request.Options.GenerateAudio
	}
	if request.Options.Seed != nil {
		parameters["seed"] = *request.Options.Seed
	}
	input := map[string]any{"prompt": request.Prompt}
	if request.Options.NegativePrompt != "" {
		input["negative_prompt"] = request.Options.NegativePrompt
	}
	legacyWan := strings.HasPrefix(lower, "wan2.6-") || strings.HasPrefix(lower, "wan2.5-") || strings.HasPrefix(lower, "wan2.2-") || strings.HasPrefix(lower, "wan2.1-")
	switch {
	case legacyWan && strings.Contains(lower, "-kf2v"):
		if len(request.Options.ImageURLs) > 0 {
			input["first_frame_url"] = request.Options.ImageURLs[0]
		}
		if len(request.Options.ImageURLs) > 1 {
			input["last_frame_url"] = request.Options.ImageURLs[1]
		}
	case legacyWan && strings.Contains(lower, "-i2v"):
		if len(request.Options.ImageURLs) > 0 {
			input["img_url"] = request.Options.ImageURLs[0]
		}
	case legacyWan && strings.Contains(lower, "-r2v"):
		references := append([]string{}, request.Options.ImageURLs...)
		references = append(references, request.Options.Videos()...)
		input["reference_urls"] = references
	case strings.Contains(lower, "videoedit") || strings.Contains(lower, "video-edit"):
		media := make([]map[string]string, 0, len(request.Options.ImageURLs)+1)
		if request.Options.VideoURL != "" {
			media = append(media, map[string]string{"type": "video", "url": request.Options.VideoURL})
		}
		for _, imageURL := range request.Options.ImageURLs {
			media = append(media, map[string]string{"type": "reference_image", "url": imageURL})
		}
		input["media"] = media
	case strings.Contains(lower, "animate"):
		if len(request.Options.ImageURLs) > 0 {
			input["image_url"] = request.Options.ImageURLs[0]
		}
		if request.Options.VideoURL != "" {
			input["video_url"] = request.Options.VideoURL
		}
	default:
		media := videoMedia(request)
		if len(media) > 0 {
			input["media"] = media
		}
	}
	if legacyWan && request.Options.AudioURL != "" {
		input["audio_url"] = request.Options.AudioURL
	}
	if legacyWan && (strings.Contains(lower, "-t2v") || strings.Contains(lower, "-r2v")) {
		delete(parameters, "ratio")
		parameters["size"] = legacyVideoSize(request.Options.Resolution, request.Options.Ratio)
		delete(parameters, "resolution")
	} else if strings.Contains(lower, "-i2v") || strings.Contains(lower, "-kf2v") || strings.Contains(lower, "videoedit") || strings.Contains(lower, "video-edit") {
		delete(parameters, "ratio")
	}
	if supportsAudioToggle(lower) {
		// Gateway's omitted audio preference means false, matching its quote.
		// DashScope defaults some toggle models to true if the field is absent.
		enabled := request.Options.GenerateAudio != nil && *request.Options.GenerateAudio
		parameters["audio"] = enabled
	} else {
		delete(parameters, "audio")
	}
	if strings.Contains(lower, "video-edit") || strings.Contains(lower, "videoedit") {
		delete(parameters, "duration")
	}
	return map[string]any{"model": request.Model, "input": input, "parameters": parameters}
}

func videoMedia(request core.MediaRequest) []map[string]string {
	var media []map[string]string
	lower := contractID(strings.ToLower(request.Model))
	imageType := "reference_image"
	videoType := "reference_video"
	audioType := "reference_audio"
	if strings.Contains(lower, "-i2v") || strings.Contains(lower, "-kf2v") {
		imageType = "first_frame"
		videoType = "first_clip"
		audioType = "driving_audio"
	}
	if request.Options.ReferenceMode == "first_frame" || request.Options.ReferenceMode == "first_last_frames" {
		imageType = "first_frame"
	}
	for index, imageURL := range request.Options.ImageURLs {
		kind := imageType
		if request.Options.ReferenceMode == "first_frame_references" && index == 0 {
			kind = "first_frame"
		}
		if strings.HasPrefix(lower, "wan2.7-i2v") && request.Options.VideoURL != "" {
			kind = "last_frame"
		}
		if index == 1 && imageType == "first_frame" {
			kind = "last_frame"
		}
		item := map[string]string{"type": kind, "url": imageURL}
		if voice := request.Options.ReferenceVoices[imageURL]; voice != "" {
			item["reference_voice"] = voice
		}
		media = append(media, item)
	}
	for _, url := range request.Options.Videos() {
		item := map[string]string{"type": videoType, "url": url}
		if voice := request.Options.ReferenceVoices[url]; voice != "" {
			item["reference_voice"] = voice
		}
		media = append(media, item)
	}
	if !(strings.Contains(lower, "-t2v") && !strings.HasPrefix(lower, "wan3.0-")) {
		for _, url := range request.Options.Audios() {
			media = append(media, map[string]string{"type": audioType, "url": url})
		}
	}
	return media
}

func legacyVideoSize(resolution, ratio string) string {
	values := map[string]map[string]string{
		"480p":  {"16:9": "832*480", "9:16": "480*832", "1:1": "624*624"},
		"720p":  {"16:9": "1280*720", "9:16": "720*1280", "1:1": "960*960", "4:3": "1088*832", "3:4": "832*1088"},
		"1080p": {"16:9": "1920*1080", "9:16": "1080*1920", "1:1": "1440*1440", "4:3": "1632*1248", "3:4": "1248*1632"},
	}
	if value := values[strings.ToLower(resolution)][ratio]; value != "" {
		return value
	}
	return resolution
}

func imageRequest(request core.MediaRequest) (string, any) {
	lower := contractID(strings.ToLower(request.Model))
	if strings.HasPrefix(lower, "wan2.5-t2i") || strings.HasPrefix(lower, "wan2.2-t2i") || strings.HasPrefix(lower, "wan2.1-t2i") {
		return legacyTextImagePath, legacyTextImagePayload(request)
	}
	if strings.HasPrefix(lower, "wan2.5-i2i") {
		return imageEditPath, imageEditPayload(request, false)
	}
	if strings.HasPrefix(lower, "wanx2.1-imageedit") {
		return imageEditPath, imageEditPayload(request, true)
	}
	return imagePath, imagePayload(request)
}

func imagePayload(request core.MediaRequest) any {
	content := make([]map[string]string, 0, len(request.Options.ImageURLs)+1)
	for _, imageURL := range request.Options.ImageURLs {
		content = append(content, map[string]string{"image": imageURL})
	}
	content = append(content, map[string]string{"text": request.Prompt})
	parameters := map[string]any{"n": 1}
	if strings.HasPrefix(request.Model, "wan2.6-image") || strings.HasPrefix(request.Model, "wan2.7-image") {
		parameters["enable_interleave"] = false
		if request.Model == "wan2.6-image" && len(request.Options.ImageURLs) == 0 {
			parameters["enable_interleave"] = true
			parameters["max_images"] = 1
		}
	}
	if request.Model == "z-image-turbo" {
		delete(parameters, "n")
	}
	if request.Options.Resolution != "" {
		parameters["size"] = request.Options.Resolution
	}
	if request.Options.NegativePrompt != "" {
		parameters["negative_prompt"] = request.Options.NegativePrompt
	}
	if request.Options.Watermark != nil {
		parameters["watermark"] = *request.Options.Watermark
	}
	if request.Options.Seed != nil {
		parameters["seed"] = *request.Options.Seed
	}
	return map[string]any{
		"model":      request.Model,
		"input":      map[string]any{"messages": []any{map[string]any{"role": "user", "content": content}}},
		"parameters": parameters,
	}
}

func legacyTextImagePayload(request core.MediaRequest) any {
	input := map[string]any{"prompt": request.Prompt}
	if request.Options.NegativePrompt != "" {
		input["negative_prompt"] = request.Options.NegativePrompt
	}
	return map[string]any{"model": request.Model, "input": input, "parameters": imageParameters(request)}
}

func imageEditPayload(request core.MediaRequest, legacy21 bool) any {
	input := map[string]any{"prompt": request.Prompt}
	if legacy21 {
		input["function"] = "description_edit"
		if len(request.Options.ImageURLs) > 0 {
			input["base_image_url"] = request.Options.ImageURLs[0]
		}
	} else {
		input["images"] = request.Options.ImageURLs
	}
	if request.Options.NegativePrompt != "" {
		input["negative_prompt"] = request.Options.NegativePrompt
	}
	return map[string]any{"model": request.Model, "input": input, "parameters": imageParameters(request)}
}

func imageParameters(request core.MediaRequest) map[string]any {
	parameters := map[string]any{"n": 1}
	if request.Options.Resolution != "" {
		parameters["size"] = request.Options.Resolution
	}
	if request.Options.Watermark != nil {
		parameters["watermark"] = *request.Options.Watermark
	}
	if request.Options.Seed != nil {
		parameters["seed"] = *request.Options.Seed
	}
	return parameters
}

func ttsPayload(request core.MediaRequest) any {
	input := map[string]any{"text": request.Prompt, "voice": request.Options.Voice}
	if request.Options.Language != "" {
		input["language_type"] = request.Options.Language
	}
	if request.Options.Instructions != "" {
		input["instructions"] = request.Options.Instructions
	}
	return map[string]any{"model": request.Model, "input": input}
}

func musicPayload(request core.MediaRequest) any {
	input := map[string]any{"format": request.Options.Format}
	if request.Prompt != "" {
		input["prompt"] = request.Prompt
	}
	if request.Options.Gender != "" {
		input["gender"] = request.Options.Gender
	}
	if request.Options.Lyrics != "" {
		input["lyrics"] = request.Options.Lyrics
	}
	if request.Options.Watermark != nil {
		input["enable_aigc_watermark"] = *request.Options.Watermark
	}
	return map[string]any{"model": request.Model, "input": input}
}

func describeDashModel(id, displayName string) (core.MediaModel, bool) {
	if modelcatalog.IsInactive("alibaba", id, time.Now()) {
		return core.MediaModel{}, false
	}
	for _, model := range mediaModels(true) {
		if model.ID == id {
			if displayName != "" {
				model.DisplayName = displayName
			}
			return model, true
		}
	}
	if strings.HasPrefix(strings.ToLower(id), "fun-music") {
		return musicModel(id, displayName), true
	}
	return core.MediaModel{}, false
}

func describeRemoteModel(id, displayName string, capabilities, requestModalities, responseModalities []string) (core.MediaModel, bool) {
	if id == "" || strings.Contains(strings.ToLower(id), "realtime") {
		return core.MediaModel{}, false
	}
	model, ok := describeDashModel(id, displayName)
	if !ok {
		return core.MediaModel{}, false
	}
	if !containsFold(responseModalities, "Image") && !containsFold(responseModalities, "Video") && !containsFold(responseModalities, "Audio") && !containsFold(capabilities, "IG") && !containsFold(capabilities, "VG") && !containsFold(capabilities, "TTS") {
		return core.MediaModel{}, false
	}

	return model, true
}

func containsFold(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(value, expected) {
			return true
		}
	}
	return false
}

func mediaModels(includeMusic bool) []core.MediaModel {
	var result []core.MediaModel
	addImage := func(id string, editing bool, maxInputs int, resolutions []string) {
		result = append(result, imageModel(id, id, editing, maxInputs, resolutions))
	}
	qwenImages := []string{"qwen-image-3.0-pro", "qwen-image-3.0", "qwen-image-2.0-pro", "qwen-image-2.0-pro-2026-06-22", "qwen-image-2.0-pro-2026-04-22", "qwen-image-2.0-pro-2026-03-03", "qwen-image-2.0", "qwen-image-2.0-2026-03-03"}
	for _, id := range qwenImages {
		addImage(id, true, 3, []string{"1024*1024", "1536*1024", "1024*1536", "2048*2048"})
	}
	for _, id := range []string{"qwen-image-max", "qwen-image-max-2025-12-30", "qwen-image-plus", "qwen-image-plus-2026-01-09", "qwen-image", "z-image-turbo"} {
		addImage(id, false, 0, []string{"1328*1328", "1664*928", "928*1664", "1472*1104", "1104*1472"})
	}
	addImage("wan2.7-image-pro", true, 9, []string{"1024*1024", "2048*2048", "4096*4096"})
	addImage("wan2.7-image", true, 9, []string{"1024*1024", "2048*2048"})
	addImage("wan2.6-image", true, 4, []string{"1280*1280", "1280*720", "720*1280"})
	addImage("wan2.6-t2i", false, 0, []string{"1280*1280", "1472*1104", "1104*1472", "1696*960", "960*1696"})
	addImage("wan2.5-t2i-preview", false, 0, []string{"1280*1280", "1472*1104", "1104*1472", "1696*960", "960*1696"})
	for _, id := range []string{"wan2.2-t2i-plus", "wan2.2-t2i-flash", "wan2.1-t2i-plus", "wan2.1-t2i-turbo"} {
		addImage(id, false, 0, []string{"1024*1024", "1280*720", "720*1280", "1440*1440"})
	}
	for _, id := range []string{"qwen-image-edit-max", "qwen-image-edit-max-2026-01-16", "qwen-image-edit-plus", "qwen-image-edit-plus-2025-12-15", "qwen-image-edit-plus-2025-10-30", "qwen-image-edit"} {
		addImage(id, true, 3, []string{"1024*1024", "1280*720", "720*1280", "2048*2048"})
		result[len(result)-1].RequiresImageInput = true
	}
	addImage("wan2.5-i2i-preview", true, 3, []string{"1280*1280", "1280*720", "720*1280"})
	result[len(result)-1].RequiresImageInput = true
	addImage("wanx2.1-imageedit", true, 1, []string{"1024*1024"})
	result[len(result)-1].RequiresImageInput = true
	addVideo := func(id string, minDuration, maxDuration int, resolutions []string, audio bool) {
		result = append(result, videoModel(id, id, minDuration, maxDuration, resolutions, audio))
	}
	addVideo("wan3.0-video", 2, 30, []string{"480p", "720p", "1080p"}, true)
	addVideo("wan3.0-video-prime", 2, 30, []string{"480p", "720p", "1080p"}, true)
	for _, id := range []string{"happyhorse-1.1-t2v", "happyhorse-1.1-i2v", "happyhorse-1.1-r2v"} {
		addVideo(id, 3, 15, []string{"480p", "720p", "1080p"}, true)
	}
	for _, id := range []string{"happyhorse-1.0-t2v", "happyhorse-1.0-i2v", "happyhorse-1.0-r2v", "happyhorse-1.0-video-edit"} {
		addVideo(id, 3, 15, []string{"720p", "1080p"}, true)
	}
	for _, id := range []string{"wan2.7-t2v", "wan2.7-t2v-2026-06-12", "wan2.7-t2v-2026-04-25", "wan2.6-t2v"} {
		addVideo(id, 2, 15, []string{"720p", "1080p"}, true)
	}
	addVideo("wan2.5-t2v-preview", 5, 10, []string{"480p", "720p", "1080p"}, true)
	for _, id := range []string{"wan2.7-i2v", "wan2.7-i2v-2026-04-25", "wan2.6-i2v", "wan2.6-i2v-flash", "wan2.5-i2v-preview"} {
		addVideo(id, 2, 15, []string{"480p", "720p", "1080p"}, true)
	}
	for _, id := range []string{"wan2.7-r2v-2026-06-12", "wan2.6-r2v", "wan2.6-r2v-flash", "wan2.7-videoedit"} {
		addVideo(id, 2, 10, []string{"720p", "1080p"}, true)
	}
	for _, id := range []string{"wan2.2-i2v-plus", "wan2.2-i2v-flash", "wan2.2-kf2v-flash", "wan2.1-i2v-plus", "wan2.1-i2v-turbo", "wan2.1-kf2v-plus"} {
		addVideo(id, 3, 5, []string{"480p", "720p", "1080p"}, false)
	}
	for _, id := range []string{"wan2.2-animate-move", "wan2.2-animate-mix"} {
		addVideo(id, 2, 30, []string{"720p"}, false)
	}
	addVideo("wan2.2-t2v-plus", 5, 5, []string{"480p", "1080p"}, false)
	addVideo("wan2.1-t2v-turbo", 5, 5, []string{"480p", "720p"}, false)
	addVideo("wan2.1-t2v-plus", 5, 5, []string{"720p"}, false)
	voices := []string{"Cherry", "Serena", "Ethan", "Chelsie"}
	for _, id := range []string{"qwen3-tts-instruct-flash", "qwen3-tts-instruct-flash-2026-01-26", "qwen3-tts-flash", "qwen3-tts-flash-2025-11-27", "qwen3-tts-flash-2025-09-18", "qwen-tts", "qwen-tts-latest", "qwen-tts-2025-05-22", "qwen-tts-2025-04-10"} {
		model := speechModel(id, id)
		model.Voices = voices
		result = append(result, model)
	}
	for _, id := range []string{"qwen3-tts-vd-2026-01-26", "qwen3-tts-vc-2026-01-22"} {
		model := speechModel(id, id)
		model.Voices = nil
		model.RequiresVoice = true
		result = append(result, model)
	}
	if includeMusic {
		result = append(result, musicModels(true)...)
	}
	result = append(result, additionalMediaModels()...)
	for index := range result {
		configureInputCapabilities(&result[index])
		applyModelContract(&result[index])
	}
	return slices.DeleteFunc(result, func(m core.MediaModel) bool {
		return modelcatalog.IsInactive("alibaba", m.ID, time.Now())
	})
}

func imageModel(id, displayName string, editing bool, maxInputs int, resolutions []string) core.MediaModel {
	return core.MediaModel{ID: id, DisplayName: displayName, MediaType: "image", Resolutions: resolutions, SupportsImageInput: editing, MaxImageInputs: maxInputs, SupportsNegativePrompt: true, Pricing: core.MediaModelPricing{ReferenceDescription: "Price depends on the region and model"}}
}

func videoModel(id, displayName string, minDuration, maxDuration int, resolutions []string, audio bool) core.MediaModel {
	return core.MediaModel{ID: id, DisplayName: displayName, MediaType: "video", Resolutions: resolutions, Ratios: []string{"16:9", "9:16", "1:1", "4:3", "3:4", "adaptive"}, MinDuration: minDuration, MaxDuration: maxDuration, DefaultDuration: 5, SupportsAudio: audio, SupportsNegativePrompt: true, Pricing: core.MediaModelPricing{ReferenceDescription: "Price depends on duration, resolution and region"}}
}

func speechModel(id, displayName string) core.MediaModel {
	return core.MediaModel{ID: id, DisplayName: displayName, MediaType: "speech", SupportsVoice: true, RequiresVoice: true, Voices: []string{"Cherry", "Serena", "Ethan", "Chelsie"}, Formats: []string{"wav"}, Pricing: core.MediaModelPricing{ReferenceDescription: "Price depends on characters or tokens"}}
}

func musicModel(id, displayName string) core.MediaModel {
	return core.MediaModel{ID: id, DisplayName: displayName, MediaType: "music", SupportsLyrics: true, SupportsGender: id == "fun-music-v1", Formats: []string{"mp3", "wav"}, Pricing: core.MediaModelPricing{ReferenceDescription: "Price depends on duration; available only in Beijing"}}
}

func musicModels(enabled bool) []core.MediaModel {
	if !enabled {
		return nil
	}
	return []core.MediaModel{musicModel("fun-music-v1", "fun-music-v1"), musicModel("fun-music-preview", "fun-music-preview")}
}

func configureInputCapabilities(model *core.MediaModel) {
	lower := strings.ToLower(model.ID)
	if strings.HasPrefix(lower, "wan3.0-") {
		model.SupportsImageInput, model.SupportsVideoInput, model.SupportsAudioInput = true, true, true
		model.MaxImageInputs = 10
	}
	if strings.Contains(lower, "-i2v") || strings.Contains(lower, "-kf2v") || strings.Contains(lower, "animate") {
		model.SupportsImageInput, model.RequiresImageInput = true, true
		model.MinImageInputs = 1
		model.MaxImageInputs = 2
	}
	if strings.Contains(lower, "-kf2v") {
		model.MinImageInputs = 2
	}
	if strings.Contains(lower, "-r2v") {
		model.SupportsImageInput, model.SupportsVideoInput, model.SupportsAudioInput = true, true, true
		model.MaxImageInputs = 5
	}
	if strings.Contains(lower, "videoedit") || strings.Contains(lower, "video-edit") || strings.Contains(lower, "vace") {
		model.SupportsImageInput, model.SupportsVideoInput, model.RequiresVideoInput = true, true, true
		model.MaxImageInputs = 3
	}
	if strings.Contains(lower, "animate") {
		model.SupportsVideoInput, model.RequiresVideoInput = true, true
	}
	if strings.HasPrefix(lower, "wan2.7-i2v") || strings.HasPrefix(lower, "wan2.6-i2v") || strings.HasPrefix(lower, "wan2.5-i2v") {
		model.SupportsAudioInput, model.SupportsVideoInput = true, true
	}
}
