package kling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
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
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

const maxResponseBytes = 4 << 20

type Adapter struct {
	apiKey          string
	client          *http.Client
	kling4Prices    map[string]money.USD
	operationPrices map[string]money.USD
	checkMu         sync.Mutex
	checkGate       chan struct{}
	check           providerkit.CredentialCheck
}

func New(key, base string, client *http.Client) (*Adapter, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, "\r\n\x00") {
		return nil, errors.New("Kling API key is required and must not contain control characters")
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base != "" && base != DefaultBaseURL {
		return nil, errors.New("Kling endpoint must be https://api-singapore.klingai.com")
	}
	if client == nil {
		client = httpclient.NewPublic(30 * time.Second)
	}
	copied := *client
	if copied.Timeout <= 0 {
		copied.Timeout = 30 * time.Second
	}
	// Never forward bearer credentials across redirects or automatically retry
	// paid submissions whose outcome is unknown.
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Adapter{apiKey: key, client: &copied, kling4Prices: map[string]money.USD{}, operationPrices: map[string]money.USD{}, checkGate: make(chan struct{}, 1)}, nil
}

func (*Adapter) Name() string { return "kling" }

func (a *Adapter) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if err := a.ValidateRequest(ctx, r); err != nil {
		return core.ProviderTask{}, err
	}
	m, _ := modelFor(r.Model)
	if isSessionOperation(r.Model) {
		return a.generateSessionOperation(ctx, m, r)
	}
	if isLegacyOmniOperation(r.Model) {
		return a.generateLegacyOmni(ctx, r)
	}
	if isResourceGenerationOperation(r.Model) {
		return a.generateResource(ctx, r)
	}
	if isAuxiliaryOperation(r.Model) {
		return a.generateAuxiliary(ctx, r)
	}
	if isImageOperation(r.Model) {
		return a.generateImage(ctx, r)
	}
	if isAudioOperation(r.Model) {
		return a.generateAudio(ctx, r)
	}
	if isMotionOperation(r.Model) {
		return a.generateMotion(ctx, r)
	}
	if isSolutionOperation(r.Model) {
		return a.generateSolution(ctx, r)
	}
	if r.Model == OmniVideo4 {
		return a.generateOmni4(ctx, m, r)
	}
	if f, ok := additionalNativeFamily(r.Model); ok {
		return a.generateAdditionalNative(ctx, f, m, r)
	}
	settings := map[string]any{"duration": durationFor(m, r.Options), "resolution": resolutionFor(m, r.Options)}
	version := "kling-3.0"
	if isKling4(r.Model) {
		version = "kling-4.0"
	} else {
		parameters, _ := core.NormalizeParameters(m.Parameters, r.Options.Parameters)
		settings["audio"], settings["multi_shot"] = "off", parameters["multi_shot"]
		if r.Options.GenerateAudio != nil && *r.Options.GenerateAudio {
			settings["audio"] = "native"
		}
	}
	payload := map[string]any{"settings": settings}
	path := "/text-to-video/" + version
	if r.Model == TextToVideo || r.Model == TextToVideo4 {
		payload["prompt"] = r.Prompt
		ratio := r.Options.Ratio
		if ratio == "" {
			ratio = "16:9"
		}
		settings["aspect_ratio"] = ratio
	} else {
		path = "/image-to-video/" + version
		contents := []map[string]string{{"type": "prompt", "text": r.Prompt}}
		for i, raw := range r.Options.ImageURLs {
			role := "middle_frame"
			if i == 0 {
				role = "first_frame"
			} else if i == len(r.Options.ImageURLs)-1 {
				role = "last_frame"
			}
			frame := map[string]string{"type": role, "url": raw}
			if isKling4(r.Model) {
				frame["id"] = "image_" + strconv.Itoa(i+1)
			}
			contents = append(contents, frame)
		}
		payload["contents"] = contents
	}
	return a.submit(ctx, http.MethodPost, path, payload)
}

func (a *Adapter) submit(ctx context.Context, method, path string, payload any) (core.ProviderTask, error) {
	raw, err := a.do(ctx, method, path, payload)
	if err != nil {
		return core.ProviderTask{}, err
	}
	var task taskData
	if json.Unmarshal(raw, &task) != nil || !validTaskID(task.ID) {
		return core.ProviderTask{}, errors.New("Kling submission response has no valid task ID; check provider history before resubmitting")
	}
	switch task.Status {
	case "submitted", "processing", "succeeded":
		return core.ProviderTask{ID: task.ID}, nil
	case "failed":
		return core.ProviderTask{}, errors.New("Kling task failed during submission; check provider task details")
	default:
		return core.ProviderTask{}, errors.New("Kling submission returned an unknown task status; check provider history before resubmitting")
	}
}

type taskData struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Outputs []struct {
		ID           legacyOpaqueID `json:"id"`
		WatermarkURL string         `json:"watermark_url"`
		Type         string         `json:"type"`
		URL          string         `json:"url"`
		Duration     string         `json:"duration"`
	} `json:"outputs"`
	Usage struct {
		InputDuration string `json:"input_duration"`
	} `json:"usage"`
	Billing []struct {
		ChargeType  string `json:"charge_type"`
		CashType    string `json:"cash_type"`
		Amount      string `json:"amount"`
		Currency    string `json:"currency"`
		PackageType string `json:"package_type"`
		ListPrice   string `json:"list_price"`
	} `json:"billing"`
}

var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validTaskID(id string) bool { return len(id) <= 1024 && taskIDPattern.MatchString(id) }

func (a *Adapter) GetTask(ctx context.Context, id string) (core.ProviderResult, error) {
	if strings.HasPrefix(id, legacyTaskPrefix) {
		handle, err := a.parseLegacy(ctx, id)
		if err != nil {
			return core.ProviderResult{}, err
		}
		if handle.Operation == resourceOperation("element", "create") {
			return a.getResourceGenerationTask(ctx, "element", id)
		}
		if handle.Operation == resourceOperation("voice", "create") {
			return a.getResourceGenerationTask(ctx, "voice", id)
		}
		if isAuxiliaryOperation(handle.Operation) {
			return a.getAuxiliaryTask(ctx, handle.Operation, handle.ID)
		}
		if isImageOperation(handle.Operation) {
			return a.getImageTask(ctx, handle.Operation, handle.ID)
		}
		if isAudioOperation(handle.Operation) {
			return a.getAudioTask(ctx, handle.Operation, handle.ID)
		}
		if isSolutionOperation(handle.Operation) {
			return a.getSolutionTask(ctx, handle.Operation, handle.ID)
		}
		return core.ProviderResult{}, errors.New("unsupported Kling task handle operation")
	}

	if !validTaskID(id) {
		return core.ProviderResult{}, errors.New("invalid Kling task ID")
	}
	raw, err := a.do(ctx, http.MethodGet, "/tasks?"+url.Values{"task_ids": {id}}.Encode(), nil)
	if err != nil {
		return core.ProviderResult{}, err
	}
	var tasks []taskData
	if json.Unmarshal(raw, &tasks) != nil || len(tasks) != 1 || tasks[0].ID != id {
		return core.ProviderResult{}, errors.New("Kling task response is malformed or does not match the requested task")
	}
	task := tasks[0]
	switch task.Status {
	case "submitted":
		return core.ProviderResult{Status: core.ProviderQueued}, nil
	case "processing":
		return core.ProviderResult{Status: core.ProviderRunning}, nil
	case "failed":
		return core.ProviderResult{Status: core.ProviderFailed, Error: "Kling video generation failed; check provider task details"}, nil
	case "succeeded":
		if len(task.Outputs) != 1 || task.Outputs[0].Type != "video" || !publicURL(task.Outputs[0].URL) {
			return core.ProviderResult{}, errors.New("Kling completed task has no single valid video output")
		}
		seconds, err := strconv.ParseFloat(task.Outputs[0].Duration, 64)
		if err != nil || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return core.ProviderResult{}, errors.New("Kling completed task has an invalid video duration")
		}
		usage := map[string]any{"output_video_duration": seconds}
		if task.Usage.InputDuration != "" {
			input, e := strconv.ParseFloat(task.Usage.InputDuration, 64)
			if e != nil || input <= 0 || math.IsNaN(input) || math.IsInf(input, 0) {
				return core.ProviderResult{}, errors.New("Kling returned an invalid input duration")
			}
			usage["input_video_duration"] = input
		}
		// Retain only recognized billing counters. Units and CNY remain in their
		// original denomination; none of these are promoted to confirmed USD.
		billing := []map[string]string{}
		for _, b := range task.Billing {
			if !decimalPattern.MatchString(b.Amount) {
				continue
			}
			if b.ChargeType == "unit" && (b.PackageType == "video" || b.PackageType == "image" || b.PackageType == "audio") {
				billing = append(billing, map[string]string{"charge_type": "unit", "package_type": b.PackageType, "amount": b.Amount})
			} else if b.ChargeType == "cash" && (b.Currency == "USD" || b.Currency == "CNY") && (b.CashType == "balance" || b.CashType == "test_balance") {
				entry := map[string]string{"charge_type": "cash", "cash_type": b.CashType, "currency": b.Currency, "amount": b.Amount}
				if decimalPattern.MatchString(b.ListPrice) {
					entry["list_price"] = b.ListPrice
				}
				billing = append(billing, entry)
			}
		}
		if len(billing) > 0 {
			usage["billing"] = billing
		}
		artifacts := []core.ProviderArtifact{{ID: string(task.Outputs[0].ID), MediaType: "video", Format: "mp4", URL: task.Outputs[0].URL, Duration: seconds, GroupID: string(task.Outputs[0].ID)}}
		if watermark := task.Outputs[0].WatermarkURL; watermark != "" {
			if !publicURL(watermark) {
				return core.ProviderResult{}, errors.New("Kling returned an invalid watermarked output")
			}
			artifacts = append(artifacts, core.ProviderArtifact{ID: string(task.Outputs[0].ID) + ":watermark", MediaType: "video", Format: "mp4", URL: watermark, Duration: seconds, GroupID: string(task.Outputs[0].ID)})
		}
		return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: task.Outputs[0].URL, Artifacts: artifacts, Usage: core.NormalizeProviderUsage(usage)}, nil
	default:
		return core.ProviderResult{}, errors.New("Kling returned an unknown task status")
	}
}

var decimalPattern = regexp.MustCompile(`^[0-9]{1,18}(\.[0-9]{1,12})?$`)

func (a *Adapter) do(ctx context.Context, method, path string, payload any) (json.RawMessage, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, errors.New("invalid Kling request")
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, DefaultBaseURL+path, body)
	if err != nil {
		return nil, errors.New("could not build Kling request")
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Sociaro-Media-Gateway/Kling")
	response, err := httpclient.Do(a.client, req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Kling request failed or timed out; request was not retried; check provider history before resubmitting")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		var failure struct {
			Code *int `json:"code"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Code != nil && *failure.Code != 0 {
			return nil, serviceError(*failure.Code)
		}
		if response.StatusCode == http.StatusUnauthorized {
			return nil, errors.New("Kling authentication failed (HTTP 401); check the current API key")
		}
		if response.StatusCode == http.StatusForbidden {
			return nil, errors.New("Kling request denied (HTTP 403); account-query or model permission is unverified")
		}
		return nil, fmt.Errorf("Kling request rejected (HTTP %d); check credentials, quota and provider status; submission was not retried", response.StatusCode)
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if kind != "application/json" {
		return nil, errors.New("Kling returned a non-JSON response")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxResponseBytes {
		return nil, errors.New("Kling response is unreadable, empty or exceeds 4 MiB")
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Code == nil {
		return nil, errors.New("Kling response envelope is malformed; check provider history before resubmitting")
	}
	if *envelope.Code != 0 {
		// Vendor messages may echo credentials or user content. Report only the
		// numeric machine code, including failures inside HTTP 200 responses.
		return nil, serviceError(*envelope.Code)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, errors.New("Kling response has no data")
	}
	return envelope.Data, nil
}

func serviceError(code int) error {
	message := "request rejected; check provider error-code documentation"
	switch code {
	case 1000, 1001, 1002:
		message = "authentication failed; check the current API key"
	case 1003:
		message = "authentication token is not yet valid"
	case 1004:
		message = "authentication token expired"
	case 1100:
		message = "account status requires attention"
	case 1101:
		message = "account balance is insufficient"
	case 1102:
		message = "resource package is exhausted or expired"
	case 1103:
		message = "resource access denied; account-query or model permission is unverified"
	case 1200, 1201:
		message = "request parameters were rejected"
	case 1202, 1203:
		message = "requested method, model or resource is unavailable"
	case 1300, 1301:
		message = "request blocked by provider policy"
	case 1302, 1303:
		message = "request or concurrency limit exceeded; retry later"
	case 1304:
		message = "provider IP allowlist rejected this request"
	case 5000, 5001, 5002:
		message = "provider service is unavailable or timed out; submission was not retried"
	}
	return fmt.Errorf("Kling %s (code %d)", message, code)
}

func publicURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || !strings.Contains(host, ".") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return false
	}
	return true
}
