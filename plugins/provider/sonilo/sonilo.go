package sonilo

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
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

const maxResponseBytes = 1 << 20

var taskID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

type Adapter struct {
	apiKey, baseURL string
	client          *http.Client
	mu              sync.Mutex
	models          []core.MediaModel
	modelsAt        time.Time
	prices          map[string]money.USD
}

func New(key, base string, client *http.Client) (*Adapter, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("Sonilo API key is required")
	}
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/"))
	if err != nil || u.Scheme != "https" || u.Host != "api.sonilo.com" || u.Path != "/v1" || u.RawPath != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("Sonilo base URL must be https://api.sonilo.com/v1")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copied := *client
	// A redirect must never replay a paid submission or forward the API key.
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Adapter{apiKey: strings.TrimSpace(key), baseURL: u.String(), client: &copied, prices: map[string]money.USD{}}, nil
}
func (*Adapter) Name() string { return "sonilo" }

type taskReference struct {
	ID       string `json:"id"`
	Model    string `json:"model"`
	Preserve bool   `json:"preserve,omitempty"`
	Duck     bool   `json:"duck,omitempty"`
}

func (a *Adapter) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if err := a.ValidateRequest(ctx, r); err != nil {
		return core.ProviderTask{}, err
	}
	op, _ := findOperation(r.Model)
	m := modelFor(r.Model)
	p, err := core.NormalizeParameters(m.Parameters, r.Options.Parameters)
	if err != nil {
		return core.ProviderTask{}, err
	}
	form := map[string]string{}
	if r.Prompt != "" {
		key := "prompt"
		if strings.HasSuffix(op.id, "sound") {
			key = "music_prompt"
		}
		form[key] = r.Prompt
	}
	if m.SupportsVideoInput {
		form["video_url"] = r.Options.Videos()[0]
	}
	if op.id == "text-to-music" {
		form["duration"] = strconv.Itoa(duration(r.Options))
	}
	if op.id == "text-to-sfx" {
		form["duration"] = strconv.FormatFloat(p["duration_seconds"].(float64), 'f', -1, 64)
	}
	if op.id == "text-to-music" || op.id == "video-to-music" {
		form["mode"] = "async"
	}
	if len(m.Formats) > 0 {
		key := "output_format"
		if strings.HasSuffix(op.id, "sfx") {
			key = "audio_format"
		}
		form[key] = valueOr(r.Options.Format, m.Formats[0])
	}
	for k, v := range p {
		if k == "duration_seconds" {
			continue
		}
		switch x := v.(type) {
		case string:
			if x != "" {
				form[k] = x
			}
		case bool:
			form[k] = strconv.FormatBool(x)
		case float64:
			form[k] = strconv.FormatFloat(x, 'f', -1, 64)
		}
	}
	if op.endpoint == "audio-ducking" {
		delete(form, "prompt")
		delete(form, "video_url")
		if op.id == "video-ducking" {
			form["voice_url"] = r.Options.Videos()[0]
		} else {
			form["voice_url"] = r.Options.Audios()[0]
		}
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range form {
		if err := w.WriteField(k, v); err != nil {
			return core.ProviderTask{}, err
		}
	}
	if err := w.Close(); err != nil {
		return core.ProviderTask{}, err
	}
	data, err := a.do(ctx, "POST", "/"+op.endpoint, &body, w.FormDataContentType())
	if err != nil {
		return core.ProviderTask{}, err
	}
	var response struct {
		ID     string `json:"task_id"`
		Status string `json:"status"`
	}
	if json.Unmarshal(data, &response) != nil || !taskID.MatchString(response.ID) || response.Status != "processing" {
		return core.ProviderTask{}, errors.New("Sonilo submission response is malformed")
	}
	preserve, _ := p["preserve_speech"].(bool)
	duck, _ := p["ducking"].(bool)
	ref, _ := json.Marshal(taskReference{ID: response.ID, Model: op.id, Preserve: preserve, Duck: duck})
	// Persist result selection with the upstream ID so a restarted worker chooses
	// the same final mix without relying on in-memory submission state.
	return core.ProviderTask{ID: "sonilo:" + base64.RawURLEncoding.EncodeToString(ref)}, nil
}

type mediaResult struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	StreamIndex int    `json:"stream_index"`
}

func (a *Adapter) GetTask(ctx context.Context, id string) (core.ProviderResult, error) {
	var ref taskReference
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, "sonilo:"))
	if !strings.HasPrefix(id, "sonilo:") || err != nil || len(data) > 1024 || json.Unmarshal(data, &ref) != nil || !taskID.MatchString(ref.ID) {
		return core.ProviderResult{}, errors.New("invalid Sonilo task reference")
	}
	op, ok := findOperation(ref.Model)
	if !ok {
		return core.ProviderResult{}, errors.New("invalid Sonilo task operation")
	}
	data, err = a.do(ctx, "GET", "/tasks/"+ref.ID, nil, "")
	if err != nil {
		return core.ProviderResult{}, err
	}
	var response struct {
		ID          string          `json:"task_id"`
		Type        string          `json:"type"`
		Status      string          `json:"status"`
		Audio       json.RawMessage `json:"audio"`
		Video       mediaResult     `json:"video"`
		Mux         []mediaResult   `json:"mux"`
		Ducked      []mediaResult   `json:"ducked"`
		OutputURL   string          `json:"output_url"`
		OutputType  string          `json:"output_type"`
		ContentType string          `json:"content_type"`
		Duration    *float64        `json:"duration_seconds"`
		Cost        *float64        `json:"cost"`
		Refunded    *bool           `json:"refunded"`
		Error       struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &response) != nil || response.ID != ref.ID || response.Type != op.service {
		return core.ProviderResult{}, errors.New("Sonilo task response is malformed or belongs to another operation")
	}
	switch response.Status {
	case "processing":
		return core.ProviderResult{Status: core.ProviderRunning}, nil
	case "failed":
		return core.ProviderResult{Status: core.ProviderFailed, Error: "Sonilo " + safeError(response.Error.Code), Usage: usage(response.Duration, response.Cost, response.Refunded, op)}, nil
	case "succeeded":
	default:
		return core.ProviderResult{}, errors.New("Sonilo returned an unknown task status")
	}
	var media mediaResult
	if op.endpoint == "audio-ducking" || strings.HasSuffix(op.id, "sound") {
		kind := "audio"
		if op.media == "video" {
			kind = "video"
		}
		if response.OutputType != kind {
			return core.ProviderResult{}, errors.New("Sonilo returned the wrong output type")
		}
		media = mediaResult{URL: response.OutputURL, ContentType: response.ContentType}
	} else if op.media == "video" {
		media = response.Video
	} else {
		media, err = selectAudio(response.Audio)
		if err != nil {
			return core.ProviderResult{}, err
		}
		if ref.Duck && len(response.Ducked) > 0 {
			media, err = firstStream(response.Ducked)
		} else if ref.Preserve {
			media, err = firstStream(response.Mux)
		}
		if err != nil {
			return core.ProviderResult{}, err
		}
	}
	if !publicURL(media.URL) || (media.ContentType != "" && !strings.HasPrefix(media.ContentType, func() string {
		if op.media == "video" {
			return "video/"
		}
		return "audio/"
	}())) {
		return core.ProviderResult{}, errors.New("Sonilo returned an invalid media result")
	}
	return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: media.URL, Usage: usage(response.Duration, response.Cost, nil, op)}, nil
}

func selectAudio(raw json.RawMessage) (mediaResult, error) {
	if len(raw) > 0 && raw[0] == '[' {
		var streams []mediaResult
		if json.Unmarshal(raw, &streams) == nil {
			return firstStream(streams)
		}
	} else {
		var media mediaResult
		if json.Unmarshal(raw, &media) == nil && media.URL != "" {
			return media, nil
		}
	}
	return mediaResult{}, errors.New("Sonilo returned no audio result")
}
func firstStream(streams []mediaResult) (mediaResult, error) {
	for _, m := range streams {
		if m.StreamIndex == 0 && m.URL != "" {
			return m, nil
		}
	}
	return mediaResult{}, errors.New("Sonilo returned no primary audio stream or requested speech mix")
}

func usage(seconds, cost *float64, refunded *bool, op operation) *core.ProviderUsage {
	u := &core.ProviderUsage{}
	// Keep only scalar billing data. Vendor responses contain signed links,
	// generated titles and free-form errors, which must not become usage records.
	raw := map[string]any{}
	if seconds != nil && !math.IsNaN(*seconds) && !math.IsInf(*seconds, 0) && *seconds > 0 && *seconds <= float64(op.maxDuration) {
		u.Metrics = []core.UsageMetric{{Name: "audio_duration", Unit: "second", Quantity: math.Max(*seconds, float64(op.floor))}}
		raw["duration_seconds"] = *seconds
	}
	if cost != nil && !math.IsNaN(*cost) && !math.IsInf(*cost, 0) && *cost >= 0 {
		raw["cost"] = *cost
	}
	if refunded != nil {
		raw["refunded"] = *refunded
	}
	if len(raw) == 0 {
		return nil
	}
	u.Raw, _ = json.Marshal(raw)
	return u
}

func (a *Adapter) do(ctx context.Context, method, path string, body io.Reader, contentType string) ([]byte, error) {
	r, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, body)
	if err != nil {
		return nil, errors.New("could not build Sonilo request")
	}
	r.Header.Set("Authorization", "Bearer "+a.apiKey)
	r.Header.Set("Accept", "application/json")
	r.Header.Set("User-Agent", "Sociaro-Media-Gateway/Sonilo")
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	response, err := httpclient.Do(a.client, r)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Sonilo request failed or timed out; submission was not retried")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := "request failed"
		switch response.StatusCode {
		case 400, 422:
			message = "request rejected; check inputs, timeline and content"
		case 401:
			message = "API key is invalid"
		case 402:
			message = "insufficient balance or credits"
		case 403:
			message = "service disabled or permission denied"
		case 404:
			message = "endpoint or task is unavailable"
		case 413:
			message = "input exceeds account upload limits"
		case 429:
			message = "rate or concurrency limit exceeded; retry later"
		}
		return nil, fmt.Errorf("Sonilo %s (HTTP %d)", message, response.StatusCode)
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if kind != "application/json" {
		return nil, errors.New("Sonilo returned a non-JSON response; async mode is required")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, errors.New("could not read Sonilo response")
	}
	if len(data) == 0 || len(data) > maxResponseBytes {
		return nil, errors.New("Sonilo response is empty or exceeds 1 MiB")
	}
	return data, nil
}
func safeError(code string) string {
	switch code {
	case "auth_invalid":
		return "API key is invalid"
	case "payment_required", "insufficient_balance":
		return "insufficient balance or credits"
	case "forbidden":
		return "service disabled or permission denied"
	case "rate_limit_exceeded":
		return "rate or concurrency limit exceeded"
	case "invalid_request", "validation_error":
		return "input rejected"
	default:
		return "generation failed"
	}
}
func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
func duration(o core.MediaOptions) int {
	if o.Duration != 0 {
		return o.Duration
	}
	return 30
}
func modelFor(id string) core.MediaModel {
	for _, m := range catalog() {
		if m.ID == id {
			return m
		}
	}
	return core.MediaModel{}
}
func publicURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || !strings.Contains(h, ".") {
		return false
	}
	if ip := net.ParseIP(h); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()) {
		return false
	}
	return true
}
