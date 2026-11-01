package bytedance

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
	tasksPath = "/contents/generations/tasks"
)

type Adapter struct {
	apiKey       string
	baseURL      string
	client       *http.Client
	imageTimeout time.Duration
	modelsMu     sync.Mutex
	models       []core.MediaModel
	modelsAt     time.Time
}

type Option func(*Adapter)

// WithImageTimeout sets the complete synchronous image request deadline.
func WithImageTimeout(timeout time.Duration) Option {
	return func(a *Adapter) {
		if timeout > 0 {
			a.imageTimeout = timeout
		}
	}
}

func New(apiKey, baseURL string, client *http.Client, options ...Option) (*Adapter, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("ByteDance API key is required")
	}
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("ByteDance base URL must be an absolute HTTPS URL")
	}
	a := &Adapter{apiKey: apiKey, baseURL: parsed.String(), client: client, imageTimeout: 10 * time.Minute}
	for _, option := range options {
		option(a)
	}
	return a, nil
}

func (a *Adapter) Name() string { return "bytedance" }

func (a *Adapter) CheckCredentials(ctx context.Context) error {
	_, err := a.ListModels(ctx)
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

	var response struct {
		Data []struct {
			ID           string `json:"id"`
			Created      int64  `json:"created"`
			OwnedBy      string `json:"owned_by"`
			ShutdownDate string `json:"shutdown_date"`
		} `json:"data"`
	}
	if err := a.do(ctx, http.MethodGet, "/models", nil, &response); err != nil {
		return nil, fmt.Errorf("list ByteDance models: %w", err)
	}
	models := make([]core.MediaModel, 0)
	seen := map[string]bool{}
	for _, item := range response.Data {
		id := modelcatalog.CanonicalID("bytedance", strings.ToLower(strings.TrimSpace(item.ID)))
		if knownModel(id) && !seen[id] {
			seen[id] = true
			model := describeModel(id)
			model.Metadata = &core.ModelMetadata{APICreated: item.Created, OwnedBy: item.OwnedBy, ShutdownDate: item.ShutdownDate}
			models = append(models, model)
		}
	}
	slices.SortFunc(models, func(a, b core.MediaModel) int { return strings.Compare(a.ID, b.ID) })
	a.modelsMu.Lock()
	a.models = cloneModels(models)
	a.modelsAt = time.Now()
	a.modelsMu.Unlock()
	return models, nil
}

func (a *Adapter) Generate(ctx context.Context, request core.MediaRequest) (core.ProviderTask, error) {
	if err := a.ValidateRequest(ctx, request); err != nil {
		return core.ProviderTask{}, err
	}
	if strings.Contains(strings.ToLower(request.Model), "seedream") {
		return a.createImage(ctx, request)
	}
	text := request.Prompt
	legacyCommands := strings.HasPrefix(request.Model, "seedance-1-0-")
	if legacyCommands {
		text = appendCommand(text, "resolution", request.Options.Resolution)
		text = appendCommand(text, "ratio", request.Options.Ratio)
		text = appendCommand(text, "duration", request.Options.Duration)
		if request.Options.CameraFixed != nil {
			text = appendCommand(text, "camerafixed", *request.Options.CameraFixed)
		}
		if request.Options.Watermark != nil {
			text = appendCommand(text, "watermark", *request.Options.Watermark)
		}
	}
	content := []map[string]any{{"type": "text", "text": text}}
	for index, imageURL := range request.Options.ImageURLs {
		role := "reference_image"
		if request.Options.ReferenceMode == "first_frame" || request.Options.ReferenceMode == "first_last_frames" || (request.Options.ReferenceMode == "" && strings.HasPrefix(request.Model, "seedance-1-")) {
			role = "first_frame"
			if index == 1 {
				role = "last_frame"
			}
		}
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": imageURL}, "role": role})
	}
	for _, url := range request.Options.Videos() {
		content = append(content, map[string]any{"type": "video_url", "video_url": map[string]string{"url": url}, "role": "reference_video"})
	}
	for _, url := range request.Options.Audios() {
		content = append(content, map[string]any{"type": "audio_url", "audio_url": map[string]string{"url": url}, "role": "reference_audio"})
	}
	payload := struct {
		OmniReferenceTaskType string           `json:"omni_reference_task_type,omitempty"`
		Model                 string           `json:"model"`
		Content               []map[string]any `json:"content"`
		Resolution            string           `json:"resolution,omitempty"`
		CameraFixed           *bool            `json:"camera_fixed,omitempty"`
		Ratio                 string           `json:"ratio,omitempty"`
		Duration              int              `json:"duration,omitempty"`
		GenerateAudio         *bool            `json:"generate_audio,omitempty"`
		Watermark             *bool            `json:"watermark,omitempty"`
		Draft                 *bool            `json:"draft,omitempty"`
		Seed                  *int64           `json:"seed,omitempty"`
		ReturnLastFrame       *bool            `json:"return_last_frame,omitempty"`
	}{Model: request.Model, Content: content, GenerateAudio: request.Options.GenerateAudio,
		Draft: request.Options.Draft, Seed: request.Options.Seed,
		ReturnLastFrame: request.Options.ReturnLastFrame}
	// The selected workflow means new reference-based generation, not provider
	// inference of an edit/extend task with incompatible duration/ratio semantics.
	if request.Model == "dreamina-seedance-2-5-260628" && request.Options.ReferenceMode == "references" {
		payload.OmniReferenceTaskType = "reference"
	}
	if !legacyCommands {
		payload.CameraFixed = request.Options.CameraFixed
		payload.Resolution = request.Options.Resolution
		payload.Ratio = request.Options.Ratio
		payload.Duration = request.Options.Duration
		payload.Watermark = request.Options.Watermark
	}

	var response struct {
		ID string `json:"id"`
	}
	if err := a.do(ctx, http.MethodPost, tasksPath, payload, &response); err != nil {
		return core.ProviderTask{}, fmt.Errorf("create ByteDance video task: %w", err)
	}
	if response.ID == "" {
		return core.ProviderTask{}, fmt.Errorf("create ByteDance video task: response has no id")
	}
	return core.ProviderTask{ID: response.ID}, nil
}

func (a *Adapter) createImage(ctx context.Context, request core.MediaRequest) (core.ProviderTask, error) {
	formats := imageFormats(request.Model)
	if request.Options.Format != "" && !slices.Contains(formats, request.Options.Format) {
		return core.ProviderTask{}, fmt.Errorf("create ByteDance image: format %q is not supported by %s", request.Options.Format, request.Model)
	}
	imageValue := any(nil)
	if len(request.Options.ImageURLs) == 1 {
		imageValue = request.Options.ImageURLs[0]
	} else if len(request.Options.ImageURLs) > 1 {
		imageValue = request.Options.ImageURLs
	}
	payload := map[string]any{
		"model": request.Model, "prompt": request.Prompt,
		"size": request.Options.Resolution, "response_format": "url",
	}
	// Seedream 4.x produces JPEG but rejects output_format, even when set to jpeg.
	// Only the documented 5.0 models support selecting a file format.
	if slices.Contains(formats, "png") {
		payload["output_format"] = valueOr(request.Options.Format, "png")
	}
	if !strings.Contains(request.Model, "5-0-pro") && !strings.Contains(request.Model, "5-0-flash") {
		payload["sequential_image_generation"] = "disabled"
	}
	if imageValue != nil {
		payload["image"] = imageValue
	}
	if request.Options.Watermark != nil {
		payload["watermark"] = *request.Options.Watermark
	}
	if request.Options.Seed != nil {
		payload["seed"] = *request.Options.Seed
	}
	var response struct {
		ID    string `json:"id"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Usage map[string]any `json:"usage"`
		Data  []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := a.do(ctx, http.MethodPost, "/images/generations", payload, &response); err != nil {
		return core.ProviderTask{}, fmt.Errorf("create ByteDance image: %w", err)
	}
	if response.Error != nil {
		return core.ProviderTask{}, fmt.Errorf("create ByteDance image: %s: %s", response.Error.Code, response.Error.Message)
	}
	if len(response.Data) == 0 || response.Data[0].URL == "" {
		return core.ProviderTask{}, fmt.Errorf("create ByteDance image: response has no URL")
	}
	return core.ProviderTask{ID: valueOr(response.ID, "immediate"), ResultURL: response.Data[0].URL, Usage: core.NormalizeProviderUsage(response.Usage)}, nil
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func appendCommand(prompt, name string, value any) string {
	if value == "" || value == 0 {
		return prompt
	}
	return fmt.Sprintf("%s --%s %v", prompt, name, value)
}

func cloneModels(models []core.MediaModel) []core.MediaModel {
	result := make([]core.MediaModel, len(models))
	copy(result, models)
	return result
}

// https://docs.byteplus.com/api/docs/ModelArk/1824121
func imageFormats(id string) []string {
	id = strings.ToLower(id)
	switch {
	case id == "seedream-4-0-250828", id == "seedream-4-0-20260415", id == "seedream-4-5-251128":
		return []string{"jpeg"}
	case id == "seedream-5-0-260128", id == "seedream-5-0-lite-260128", id == "dola-seedream-5-0-pro-260628", id == "dola-seedream-5-0-flash-260915":
		return []string{"png", "jpeg"}
	default:
		return nil
	}
}

func describeModel(id string) (result core.MediaModel) {
	defer func() {
		result.ReferenceModes = referenceModes(result)
		if strings.HasPrefix(result.ID, "dreamina-seedance-2-") {
			// Reviewed input limits: ModelArk Create video generation task API,
			// checked 2026-10-26. The references workflow explicitly selects
			// reference generation for 2.5 rather than auto edit/extend inference.
			result.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 12,
				Image: core.MediaFileLimits{ContentTypes: []string{"image/png", "image/jpeg", "image/gif", "image/webp"}, MaxBytes: 30_000_000, MinDimension: 300, MaxDimension: 6000, MinRatio: 0.4, MaxRatio: 2.5},
				Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, Codecs: []string{"avc1", "avc3", "hvc1", "hev1"}, MaxBytes: 200_000_001, MinDimension: 300, MaxDimension: 6000, MinPixels: 407696, MaxPixels: 8295044, MinRatio: 0.4, MaxRatio: 2.5, MinDuration: 2, MaxDuration: 15, MaxTotalDuration: 15, MinFPS: 24, MaxFPS: 60},
				Audio: core.MediaFileLimits{ContentTypes: []string{"audio/mpeg", "audio/wave", "audio/wav", "audio/x-wav"}, MaxBytes: 15_000_001, MinDuration: 2, MaxDuration: 15, MaxTotalDuration: 15}}
			if result.ID == "dreamina-seedance-2-5-260628" {
				result.ReferenceMediaLimits.MaxFiles = 50
				result.ReferenceMediaLimits.Video.MaxDuration = 30
				result.ReferenceMediaLimits.Video.MaxTotalDuration = 30
				result.ReferenceMediaLimits.Audio.MaxDuration = 30
				result.ReferenceMediaLimits.Audio.MaxTotalDuration = 30
			}
			result.ReferencePromptSyntax = "numbered"
			result.ReferencePromptTypes = []string{"image"}
			if result.SupportsVideoInput {
				result.ReferencePromptTypes = append(result.ReferencePromptTypes, "video")
			}
			if result.SupportsAudioInput {
				result.ReferencePromptTypes = append(result.ReferencePromptTypes, "audio")
			}
		}
	}()
	if strings.Contains(strings.ToLower(id), "seedream") {
		resolutions := []string{"2K", "4K"}
		if strings.Contains(id, "4-0") {
			resolutions = []string{"1K", "2K", "4K"}
		}
		if strings.Contains(id, "5-0-pro") || strings.Contains(id, "5-0-flash") {
			resolutions = []string{"1K", "1.5K", "2K"}
		} else if strings.Contains(id, "5-0") {
			resolutions = []string{"2K", "3K", "4K"}
		}
		maxInputs := 14
		if strings.Contains(id, "5-0-pro") || strings.Contains(id, "5-0-flash") {
			maxInputs = 10
		}
		return core.MediaModel{
			ID: id, DisplayName: id, MediaType: "image", Resolutions: resolutions,
			// Image generation API, reviewed 2026-10-26. Templates inspects
			// the supported PNG/JPEG/WebP/GIF subset of native image formats.
			ReferenceMediaLimits: &core.ReferenceMediaLimits{MaxFiles: maxInputs, Image: core.MediaFileLimits{ContentTypes: []string{"image/png", "image/jpeg", "image/webp", "image/gif"}, MaxBytes: 30_000_001, MinDimension: 15, MinPixels: 196, MaxPixels: 36_000_000, MinRatio: 1.0 / 16, MaxRatio: 16}},
			Formats:              imageFormats(id),
			SupportsImageInput:   true, MaxImageInputs: maxInputs,
			Pricing: core.MediaModelPricing{ReferenceDescription: "Price depends on the model and image count"},
		}
	}
	ratios := []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"}
	model := core.MediaModel{
		ID: id, DisplayName: id, MediaType: "video", Resolutions: []string{"480p", "720p", "1080p"},
		Ratios: ratios, MinDuration: 2, MaxDuration: 12, DefaultDuration: 5,
		Pricing: core.MediaModelPricing{ReferenceDescription: "5 s · 480p · 16:9 · without audio"},
	}
	lowerID := strings.ToLower(id)
	if strings.Contains(lowerID, "seedance") && !strings.Contains(lowerID, "-t2v-") {
		model.SupportsImageInput = true
		model.MaxImageInputs = 2
	}
	if strings.Contains(lowerID, "i2v") {
		model.SupportsImageInput = true
		model.RequiresImageInput = true
		model.MinImageInputs = 1
		model.MaxImageInputs = 2
	}
	if strings.Contains(lowerID, "dreamina-seedance-2-") {
		model.SupportsImageInput = true
		model.MaxImageInputs = 9
		model.SupportsAudioInput = true
		model.SupportsVideoInput = true
	}
	switch id {
	case "seedance-1-0-pro-fast-251015":
		model.DisplayName = "Seedance 1.0 Pro Fast"
		model.MaxImageInputs = 1
		model.SupportsCameraFixed = true
		model.Pricing.Known = true
		model.Pricing.USDPerMillionTokens = 1
		model.Pricing.ReferencePriceUSD = 0.0486
	case "seedance-1-0-lite-i2v-250428":
		model.MaxImageInputs = 4
	case "seedance-1-0-lite-t2v-250428":
		model.DisplayName = "Seedance 1.0 Lite"
		model.Pricing.Known = true
		model.Pricing.USDPerMillionTokens = 1.8
		model.Pricing.ReferencePriceUSD = 0.08748
	case "seedance-1-0-pro-250528":
		model.DisplayName = "Seedance 1.0 Pro"
		model.SupportsCameraFixed = true
		model.Pricing.Known = true
		model.Pricing.USDPerMillionTokens = 2.5
		model.Pricing.ReferencePriceUSD = 0.1215
	case "seedance-1-5-pro-251215":
		model.DisplayName = "Seedance 1.5 Pro"
		model.SupportsAudio = true
		model.SupportsDraft = true
		model.DraftResolutions = []string{"480p"}
		model.SupportsCameraFixed = true
		model.Pricing.Known = true
		model.Pricing.USDPerMillionTokens = 1.2
		model.Pricing.USDPerMillionAudioTokens = 2.4
		model.Pricing.ReferencePriceUSD = 0.05832
		model.Pricing.DraftSilentFactor = 0.7
		model.Pricing.DraftAudioFactor = 0.6
	case "dreamina-seedance-2-0-mini-260615":
		model.DisplayName = "Seedance 2.0 Mini"
		configure2x(&model, 3.5, 0.1701, false)
	case "dreamina-seedance-2-0-fast-260128":
		model.DisplayName = "Seedance 2.0 Fast"
		configure2x(&model, 5.6, 0.27216, false)
	case "dreamina-seedance-2-0-260128":
		model.DisplayName = "Seedance 2.0"
		configure2x(&model, 7.0, 0.3402, true)
		model.Pricing.USDPerMillionByResolution = map[string]float64{"480p": 7.0, "720p": 7.0, "1080p": 7.7, "4k": 4.0}
	case "dreamina-seedance-2-5-260628":
		model.DisplayName = "Seedance 2.5"
		configure2x(&model, 10.7, 0.52002, false)
		model.Resolutions = append(model.Resolutions, "1080p")
		model.MaxDuration = 30
		model.MaxImageInputs = 30
		model.Pricing.USDPerMillionByResolution = map[string]float64{"480p": 10.7, "720p": 10.7, "1080p": 11.7}
	}
	return model
}

func configure2x(model *core.MediaModel, rate, reference float64, supports4K bool) {
	model.SupportsAssetReferences = true
	model.MinDuration = 4
	model.MaxDuration = 15
	model.SupportsAudio = true
	model.SupportsLastFrame = true
	model.Ratios = append(model.Ratios, "adaptive")
	if supports4K {
		model.Resolutions = append(model.Resolutions, "4k")
	} else {
		model.Resolutions = []string{"480p", "720p"}
	}
	if rate > 0 {
		model.Pricing.Known = true
		model.Pricing.USDPerMillionTokens = rate
		model.Pricing.ReferencePriceUSD = reference
	}
}

func (a *Adapter) GetTask(ctx context.Context, providerTaskID string) (core.ProviderResult, error) {
	var response struct {
		Status  string `json:"status"`
		Content struct {
			VideoURL     string `json:"video_url"`
			LastFrameURL string `json:"last_frame_url"`
		} `json:"content"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Usage map[string]any `json:"usage"`
	}
	path := tasksPath + "/" + url.PathEscape(providerTaskID)
	if err := a.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return core.ProviderResult{}, fmt.Errorf("get ByteDance video task: %w", err)
	}
	switch response.Status {
	case "queued":
		return core.ProviderResult{Status: core.ProviderQueued, Usage: core.NormalizeProviderUsage(response.Usage)}, nil
	case "running":
		return core.ProviderResult{Status: core.ProviderRunning, Usage: core.NormalizeProviderUsage(response.Usage)}, nil
	case "succeeded":
		if response.Content.VideoURL == "" {
			return core.ProviderResult{}, fmt.Errorf("ByteDance task succeeded without video_url")
		}
		return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: response.Content.VideoURL, LastFrameURL: response.Content.LastFrameURL, Usage: core.NormalizeProviderUsage(response.Usage)}, nil
	case "failed", "cancelled", "expired":
		message := strings.TrimSpace(response.Error.Message)
		if message == "" {
			message = "ByteDance task ended with status " + response.Status
		}
		return core.ProviderResult{Status: core.ProviderFailed, Error: message, Usage: core.NormalizeProviderUsage(response.Usage)}, nil
	default:
		return core.ProviderResult{}, fmt.Errorf("unknown ByteDance task status %q", response.Status)
	}
}

func (a *Adapter) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+a.apiKey)
	request.Header.Set("Content-Type", "application/json")
	client := a.client
	if path == "/images/generations" {
		imageClient := *client
		imageClient.Timeout = a.imageTimeout
		if imageClient.Timeout <= 0 {
			imageClient.Timeout = 10 * time.Minute
		}
		client = &imageClient
	}
	response, err := httpclient.Do(client, request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 1<<20)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(limited)
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	// Validate the entire HTTP body, not only the first JSON value. One extra
	// byte distinguishes an oversized response from EOF at the existing limit.
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("response exceeds 1 MiB limit")
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
