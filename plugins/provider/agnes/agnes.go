package agnes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
)

const maxResponseBytes = 64 << 20

type Adapter struct {
	apiKey, baseURL   string
	client            *http.Client
	generationTimeout time.Duration
	mu                sync.Mutex
	models            []core.MediaModel
	modelsAt          time.Time
}

func New(key, base string, client *http.Client, timeout time.Duration) (*Adapter, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("Agnes API key is required")
	}
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/"))
	if err != nil || u.Scheme != "https" || u.Host != "apihub.agnes-ai.com" || u.Path != "/v1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("Agnes base URL must be https://apihub.agnes-ai.com/v1")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if timeout <= 0 {
		timeout = 6 * time.Minute
	}
	return &Adapter{apiKey: strings.TrimSpace(key), baseURL: u.String(), client: &copied, generationTimeout: timeout}, nil
}

func (*Adapter) Name() string { return "agnes" }

func (a *Adapter) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if err := a.ValidateRequest(ctx, r); err != nil {
		return core.ProviderTask{}, err
	}
	o := r.Options
	payload := map[string]any{"model": r.Model, "prompt": r.Prompt}
	if describeModel(r.Model).MediaType == "image" {
		size := "1K"
		if r.Model == "agnes-image-2.0-flash" {
			size = "1024x1024"
		}
		payload["size"] = valueOr(o.Resolution, size)
		if o.Ratio != "" {
			payload["ratio"] = o.Ratio
		}
		extra := map[string]any{"response_format": "url"}
		if len(o.ImageURLs) > 0 {
			extra["image"] = o.ImageURLs
		}
		payload["extra_body"] = extra
		data, requestID, err := a.do(ctx, http.MethodPost, "/images/generations", payload, true)
		if err != nil {
			return core.ProviderTask{}, err
		}
		var response struct {
			Data []struct {
				URL    string `json:"url"`
				Base64 string `json:"b64_json"`
			} `json:"data"`
			Usage map[string]any `json:"usage"`
		}
		if json.Unmarshal(data, &response) != nil || len(response.Data) != 1 {
			return core.ProviderTask{}, errors.New("Agnes image response is malformed or has no single result")
		}
		result := response.Data[0].URL
		if result != "" {
			if !publicURL(result) {
				return core.ProviderTask{}, errors.New("Agnes returned an invalid image URL")
			}
		} else {
			decoded, err := base64.StdEncoding.DecodeString(response.Data[0].Base64)
			if err != nil || len(decoded) == 0 {
				return core.ProviderTask{}, errors.New("Agnes returned invalid image Base64")
			}
			kind := http.DetectContentType(decoded)
			if kind != "image/png" && kind != "image/jpeg" && kind != "image/webp" {
				return core.ProviderTask{}, errors.New("Agnes returned unsupported image bytes")
			}
			result = "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(decoded)
		}
		return core.ProviderTask{ID: valueOr(requestID, "immediate"), ResultURL: result, Usage: core.NormalizeProviderUsage(response.Usage)}, nil
	}
	duration := o.Duration
	if duration == 0 {
		duration = 5
	}
	payload["seconds"], payload["size"], payload["aspect_ratio"], payload["n"] = strconv.Itoa(duration), valueOr(o.Resolution, "720P"), valueOr(o.Ratio, "16:9"), 1
	if o.Seed != nil {
		payload["seed"] = *o.Seed
	}
	switch o.ReferenceMode {
	case "first_frame", "last_frame", "first_last_frame":
		payload["mode"] = "keyframe"
		if o.ReferenceMode == "last_frame" {
			payload["last_frame"] = o.ImageURLs[0]
		} else {
			payload["first_frame"] = o.ImageURLs[0]
		}
		if o.ReferenceMode == "first_last_frame" {
			payload["last_frame"] = o.ImageURLs[1]
		}
	default:
		payload["mode"] = "text"
		if len(o.ImageURLs)+len(o.Videos())+len(o.Audios()) > 0 {
			payload["mode"] = "reference"
			if len(o.ImageURLs) > 0 {
				payload["images"] = o.ImageURLs
			}
			if len(o.Audios()) > 0 {
				payload["audios"] = o.Audios()
			}
			if len(o.Videos()) > 0 {
				video := map[string]any{"url": o.Videos()[0]}
				if v, ok := o.Parameters["video_start_seconds"]; ok {
					video["start_seconds"] = v
				}
				if v, ok := o.Parameters["video_require_audio"]; ok {
					video["require_audio"] = v
				}
				payload["videos"] = []map[string]any{video}
			}
		}
	}
	data, _, err := a.do(ctx, http.MethodPost, "/videos", payload, true)
	if err != nil {
		return core.ProviderTask{}, err
	}
	var response struct {
		VideoID string         `json:"video_id"`
		Model   string         `json:"model"`
		Status  string         `json:"status"`
		Usage   map[string]any `json:"usage"`
	}
	if json.Unmarshal(data, &response) != nil || response.VideoID == "" || len(response.VideoID) > 1024 {
		return core.ProviderTask{}, errors.New("Agnes video response has no valid video_id")
	}
	if response.Status == "failed" {
		return core.ProviderTask{}, errors.New("Agnes video task failed during creation")
	}
	// Polling requires both the video_id and model_name. Persist an opaque handle
	// so polling still works after worker restart, including manual model profiles.
	handle, _ := json.Marshal(taskHandle{VideoID: response.VideoID, Model: valueOr(response.Model, r.Model)})
	return core.ProviderTask{ID: base64.RawURLEncoding.EncodeToString(handle), Usage: core.NormalizeProviderUsage(response.Usage)}, nil
}

type taskHandle struct {
	VideoID string `json:"video_id"`
	Model   string `json:"model"`
}

func (a *Adapter) GetTask(ctx context.Context, id string) (core.ProviderResult, error) {
	raw, err := base64.RawURLEncoding.DecodeString(id)
	var handle taskHandle
	if err != nil || len(raw) > 4096 || json.Unmarshal(raw, &handle) != nil || handle.VideoID == "" || handle.Model == "" {
		return core.ProviderResult{}, errors.New("invalid Agnes task handle")
	}
	query := url.Values{"video_id": {handle.VideoID}, "model_name": {handle.Model}}
	data, _, err := a.do(ctx, http.MethodGet, "/agnesapi?"+query.Encode(), nil, false)
	if err != nil {
		return core.ProviderResult{}, err
	}
	var response struct {
		Status  string          `json:"status"`
		URL     string          `json:"url"`
		Seconds json.RawMessage `json:"seconds"`
		Usage   map[string]any  `json:"usage"`
	}
	if json.Unmarshal(data, &response) != nil {
		return core.ProviderResult{}, errors.New("Agnes task response is malformed")
	}
	result := core.ProviderResult{}
	switch response.Status {
	case "queued":
		result.Status = core.ProviderQueued
	case "in_progress":
		result.Status = core.ProviderRunning
	case "failed":
		result.Status, result.Error = core.ProviderFailed, "Agnes video generation failed; check provider task details"
	case "completed":
		if !publicURL(response.URL) {
			return core.ProviderResult{}, errors.New("Agnes completed video has no valid result URL")
		}
		result.Status, result.ResultURL = core.ProviderSucceeded, response.URL
		result.Usage = completedUsage(response.Usage, response.Seconds)
	default:
		return core.ProviderResult{}, errors.New("Agnes returned an unknown task status")
	}
	return result, nil
}

func completedUsage(raw map[string]any, seconds json.RawMessage) *core.ProviderUsage {
	if raw == nil {
		raw = map[string]any{}
	}
	// Returned output duration is observable usage, not a receipt. Never fill
	// missing input-video duration with the estimated upper bound.
	var value string
	if json.Unmarshal(seconds, &value) != nil {
		value = string(seconds)
	}
	n, err := strconv.ParseFloat(value, 64)
	if err == nil && n > 0 && !math.IsInf(n, 0) && !math.IsNaN(n) {
		if _, exists := raw["output_video_duration"]; !exists {
			raw["output_video_duration"] = n
		}
	}
	return core.NormalizeProviderUsage(raw)
}

func (a *Adapter) do(ctx context.Context, method, path string, payload any, generation bool) ([]byte, string, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil || len(b) > maxResponseBytes {
			return nil, "", errors.New("Agnes request is invalid or exceeds 64 MiB")
		}
		body = bytes.NewReader(b)
	}
	endpoint := a.baseURL + path
	if strings.HasPrefix(path, "/agnesapi?") {
		endpoint = strings.TrimSuffix(a.baseURL, "/v1") + path
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, "", errors.New("could not build Agnes request")
	}
	request.Header.Set("Authorization", "Bearer "+a.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Sociaro-Media-Gateway/Agnes")
	client := a.client
	if generation {
		copied := *client
		copied.Timeout = a.generationTimeout
		client = &copied
	}
	response, err := httpclient.Do(client, request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", errors.New("Agnes request failed or timed out; request was not retried")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := "request failed"
		switch response.StatusCode {
		case 400, 422:
			message = "request rejected; check model options and content"
		case 401:
			message = "API key is invalid"
		case 403:
			message = "access denied"
		case 404:
			message = "model, task or endpoint is unavailable"
		case 429:
			message = "rate limit or quota exceeded"
		case 500:
			message = "internal provider error; try again later"
		case 502:
			message = "upstream service returned an invalid response; try again later"
		case 503:
			message = "service temporarily unavailable; try again later"
		case 504:
			message = "upstream service timed out"
		}
		// Preserve only recognized machine codes, never arbitrary vendor text:
		// error bodies may echo the prompt, reference URLs or credentials.
		data, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		if detail := knownFailure(data); detail != "" {
			message = detail
		}
		suffix := ""
		if generation && response.StatusCode >= 500 {
			suffix = "; submission was not retried; check provider history before resubmitting"
		}
		return nil, "", fmt.Errorf("Agnes %s (HTTP %d)%s", message, response.StatusCode, suffix)
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if kind != "application/json" {
		return nil, "", errors.New("Agnes returned a non-JSON response")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxResponseBytes {
		return nil, "", errors.New("Agnes response is unreadable, empty or exceeds 64 MiB")
	}
	return data, response.Header.Get("X-Request-ID"), nil
}

func knownFailure(data []byte) string {
	var response struct {
		Code  string `json:"code"`
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &response) != nil {
		return ""
	}
	for _, code := range []string{response.Error.Code, response.Error.Type, response.Code} {
		switch code {
		case "no_available_channel", "model_unavailable":
			return "no available route or capacity for this model; check model access or try again later"
		case "service_unavailable", "server_overloaded":
			return "service temporarily unavailable or overloaded; try again later"
		case "insufficient_quota", "insufficient_balance":
			return "account quota or balance is insufficient; check the Agnes account"
		case "rate_limit_exceeded":
			return "request rate limit exceeded; try again later"
		case "invalid_api_key":
			return "API key is invalid"
		}
	}
	return ""
}

func publicURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || !strings.Contains(host, ".") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return false
	}
	return true
}

func validReference(raw string, inline bool) bool {
	if !inline || !strings.HasPrefix(raw, "data:") {
		return publicURL(raw)
	}
	header, encoded, ok := strings.Cut(raw, ",")
	if !ok || !strings.HasSuffix(header, ";base64") || !strings.HasPrefix(header, "data:image/") {
		return false
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	return err == nil && len(data) > 0 && len(data) <= 15<<20 && strings.HasPrefix(http.DetectContentType(data), "image/")
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
