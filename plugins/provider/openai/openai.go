package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
)

const maxResponseBytes = 64 << 20
const maxImageBytes = 50 << 20

type Adapter struct {
	apiKey, baseURL   string
	client            *http.Client
	generationTimeout time.Duration
	mu                sync.Mutex
	models            []core.MediaModel
	modelsAt          time.Time
}

func New(apiKey, baseURL string, client *http.Client, generationTimeout time.Duration) (*Adapter, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("OpenAI API key is required")
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || u.Scheme != "https" || u.Host != "api.openai.com" || u.Path != "/v1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("OpenAI base URL must be https://api.openai.com/v1")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copied := *client
	// Never forward credentials or replay a paid POST to a redirect destination.
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if generationTimeout <= 0 {
		generationTimeout = 10 * time.Minute
	}
	return &Adapter{apiKey: strings.TrimSpace(apiKey), baseURL: u.String(), client: &copied, generationTimeout: generationTimeout}, nil
}

func (a *Adapter) Name() string { return "openai" }

func (a *Adapter) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if err := a.ValidateRequest(ctx, r); err != nil {
		return core.ProviderTask{}, err
	}
	if strings.Contains(r.Model, "image") {
		return a.image(ctx, r)
	}
	if strings.HasPrefix(r.Model, "gpt-audio") {
		return a.audio(ctx, r)
	}
	return a.speech(ctx, r)
}

func (a *Adapter) GetTask(context.Context, string) (core.ProviderResult, error) {
	// These endpoints return final bytes synchronously and have no recoverable
	// provider task to poll. The worker persists and archives the immediate result.
	return core.ProviderResult{}, errors.New("OpenAI media endpoints return immediate results and cannot be polled")
}

func (a *Adapter) image(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	format := valueOr(r.Options.Format, "png")
	payload := map[string]any{"model": r.Model, "prompt": r.Prompt, "n": 1, "size": valueOr(r.Options.Resolution, "1024x1024"), "quality": valueOr(r.Options.Quality, "medium"), "output_format": format, "background": valueOr(r.Options.Background, "opaque")}
	path := "/images/generations"
	if len(r.Options.ImageURLs) > 0 {
		path = "/images/edits"
		images := make([]map[string]string, 0, len(r.Options.ImageURLs))
		for _, u := range r.Options.ImageURLs {
			images = append(images, map[string]string{"image_url": u})
		}
		payload["images"] = images
	}
	data, requestID, err := a.do(ctx, http.MethodPost, path, payload, true)
	if err != nil {
		return core.ProviderTask{}, err
	}
	var response struct {
		Data []struct {
			Base64 string `json:"b64_json"`
		} `json:"data"`
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Data) != 1 {
		return core.ProviderTask{}, errors.New("OpenAI image response is malformed or has no single result")
	}
	result, err := encodedResult(response.Data[0].Base64, "image/"+format)
	if err != nil {
		return core.ProviderTask{}, err
	}
	return core.ProviderTask{ID: valueOr(requestID, "immediate"), ResultURL: result, Usage: imageUsage(response.Usage, r.Model)}, nil
}

func (a *Adapter) speech(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	format := valueOr(r.Options.Format, "mp3")
	payload := map[string]any{"model": r.Model, "input": r.Prompt, "voice": valueOr(r.Options.Voice, "alloy"), "response_format": format}
	if r.Options.Instructions != "" {
		payload["instructions"] = r.Options.Instructions
	}
	data, requestID, err := a.do(ctx, http.MethodPost, "/audio/speech", payload, true)
	if err != nil {
		return core.ProviderTask{}, err
	}
	// The binary Speech response has no token usage. Do not invent metered
	// counters from the prompt or confuse estimated characters with provider usage.
	return core.ProviderTask{ID: valueOr(requestID, "immediate"), ResultURL: "data:" + audioMIME(format) + ";base64," + base64.StdEncoding.EncodeToString(data)}, nil
}

func (a *Adapter) audio(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	format := valueOr(r.Options.Format, "mp3")
	messages := []map[string]string{}
	if r.Options.Instructions != "" {
		messages = append(messages, map[string]string{"role": "system", "content": r.Options.Instructions})
	}
	messages = append(messages, map[string]string{"role": "user", "content": r.Prompt})
	payload := map[string]any{"model": r.Model, "modalities": []string{"text", "audio"}, "audio": map[string]string{"voice": valueOr(r.Options.Voice, "alloy"), "format": format}, "messages": messages, "store": false, "max_completion_tokens": 4096}
	data, requestID, err := a.do(ctx, http.MethodPost, "/chat/completions", payload, true)
	if err != nil {
		return core.ProviderTask{}, err
	}
	var response struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Audio struct {
					Data string `json:"data"`
				} `json:"audio"`
			} `json:"message"`
		} `json:"choices"`
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Choices) != 1 || response.Choices[0].FinishReason != "stop" {
		return core.ProviderTask{}, errors.New("OpenAI audio response is missing, refused, or incomplete")
	}
	result, err := encodedResult(response.Choices[0].Message.Audio.Data, audioMIME(format))
	if err != nil {
		return core.ProviderTask{}, err
	}
	return core.ProviderTask{ID: valueOr(requestID, "immediate"), ResultURL: result, Usage: audioUsage(response.Usage)}, nil
}

func (a *Adapter) do(ctx context.Context, method, path string, payload any, generation bool) ([]byte, string, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, "", err
		}
		if len(data) > maxResponseBytes {
			return nil, "", errors.New("OpenAI request exceeds 64 MiB")
		}
		body = bytes.NewReader(data)
	}
	client := a.client
	if generation {
		copied := *client
		copied.Timeout = a.generationTimeout
		client = &copied
	}
	request, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, body)
	if err != nil {
		return nil, "", errors.New("could not build OpenAI request")
	}
	request.Header.Set("Authorization", "Bearer "+a.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Sociaro-Media-Gateway/OpenAI")
	response, err := httpclient.Do(client, request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", errors.New("OpenAI request failed or timed out; request was not retried")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Vendor messages can echo prompts, signed URLs, or keys. Return a stable
		// classification, never the raw error body, to the persisted task error.
		message := "request failed"
		switch response.StatusCode {
		case 400, 422:
			message = "request rejected; check model options and content"
		case 401:
			message = "API key is invalid"
		case 403:
			message = "access denied; check project permissions and organization verification"
		case 404:
			message = "model or endpoint is unavailable"
		case 429:
			message = "rate limit or quota exceeded"
		}
		return nil, "", fmt.Errorf("OpenAI %s (HTTP %d)", message, response.StatusCode)
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if path == "/audio/speech" {
		if !strings.HasPrefix(kind, "audio/") && kind != "application/octet-stream" {
			return nil, "", errors.New("OpenAI returned a non-audio speech response")
		}
	} else if kind != "application/json" {
		return nil, "", errors.New("OpenAI returned a non-JSON response")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, "", errors.New("could not read OpenAI response")
	}
	if len(data) == 0 || len(data) > maxResponseBytes {
		return nil, "", errors.New("OpenAI response is empty or exceeds 64 MiB")
	}
	return data, response.Header.Get("x-request-id"), nil
}

func encodedResult(value, kind string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(data) == 0 || len(data) > maxImageBytes {
		return "", errors.New("OpenAI returned empty, oversized, or invalid base64 media")
	}
	return "data:" + kind + ";base64," + value, nil
}

func validateImageURL(value string) error {
	if strings.HasPrefix(value, "data:") {
		header, body, ok := strings.Cut(value, ",")
		if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64" && header != "data:image/webp;base64") {
			return errors.New("image references must be PNG, JPEG, or WebP")
		}
		if len(body) > base64.StdEncoding.EncodedLen(maxImageBytes) {
			return errors.New("image reference exceeds 50 MiB")
		}
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err != nil || len(decoded) == 0 || len(decoded) >= maxImageBytes {
			return errors.New("invalid image reference")
		}
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return errors.New("image reference must be an HTTP(S) URL or image data URL")
	}
	// Remote URLs are fetched by OpenAI, not by the Gateway (no new SSRF path).
	return nil
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
func audioMIME(format string) string {
	switch format {
	case "mp3":
		return "audio/mpeg"
	case "opus":
		return "audio/ogg"
	default:
		return "audio/" + format
	}
}

func (a *Adapter) PreferInlineImages() bool { return true }
