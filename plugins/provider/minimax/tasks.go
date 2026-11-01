package minimax

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// NativeTask retains text output for Context-IR as well as video output. The
// caller must archive a video URL before presenting it as a durable result.
type NativeTask struct {
	ID         string `json:"id"`
	Model      string `json:"model"`
	Status     string `json:"status"`
	TaskType   string `json:"task_type"`
	Modality   string `json:"modality"`
	Resolution string `json:"resolution"`
	Duration   int    `json:"duration"`
	Content    struct {
		URL    string `json:"url"`
		Prompt string `json:"prompt"`
	} `json:"content"`
	Usage json.RawMessage `json:"usage"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (a *Adapter) queryNative(ctx context.Context, h handle) (NativeTask, error) {
	var out struct {
		Task NativeTask `json:"task"`
	}
	if e := a.json(ctx, "GET", "/v2/query/video_generation/"+h.ID, nil, &out); e != nil {
		return NativeTask{}, e
	}
	if e := validateNativeTask(out.Task, h); e != nil {
		return NativeTask{}, e
	}
	return out.Task, nil
}
func validateNativeTask(t NativeTask, h handle) error {
	kind := "generation"
	if h.Kind == "context" {
		kind = "h3_context_ir"
	}
	if h.Kind == "regeneration" {
		kind = "regeneration"
	}
	if t.ID != h.ID || t.Model != h.Model || t.TaskType != kind || !slices.Contains([]string{"queued", "running", "succeeded", "failed", "cancelled"}, t.Status) {
		return errors.New("MiniMax task identity, type or state does not match its handle")
	}
	if t.Status == "succeeded" {
		if kind == "generation" {
			m, ok := modelFor(h.Model)
			if !ok || !slices.Contains(m.Resolutions, strings.ToLower(t.Resolution)) || t.Duration < m.MinDuration || t.Duration > m.MaxDuration {
				return errors.New("MiniMax task output resolution or duration violates its model contract")
			}
		}
		if kind == "regeneration" && (t.Resolution != "2K" || t.Duration < 4 || t.Duration > 15) {
			return errors.New("MiniMax regeneration output must be 2K and 4–15 seconds")
		}
		if kind == "h3_context_ir" {
			if t.Modality != "text" || strings.TrimSpace(t.Content.Prompt) == "" {
				return errors.New("MiniMax context task has no enhanced prompt")
			}
		} else if t.Modality != "video" || !publicURL(t.Content.URL) {
			return errors.New("MiniMax video task has no valid output URL")
		}
	}
	return nil
}

func (a *Adapter) QueryNativeTask(ctx context.Context, token string) (NativeTask, error) {
	h, e := a.open(ctx, token)
	if e != nil {
		return NativeTask{}, e
	}
	if h.Kind != "h3" && h.Kind != "context" && h.Kind != "regeneration" {
		return NativeTask{}, errors.New("not an H3 task handle")
	}
	t, e := a.queryNative(ctx, h)
	if e != nil {
		return NativeTask{}, e
	}
	t.ID = token
	if t.Error != nil {
		t.Error.Message = "MiniMax task failed"
	}
	return t, nil
}
func (a *Adapter) GetTask(ctx context.Context, token string) (core.ProviderResult, error) {
	h, e := a.open(ctx, token)
	if e != nil {
		return core.ProviderResult{}, e
	}
	if h.Kind == "h3" || h.Kind == "regeneration" || h.Kind == "context" {
		t, e := a.queryNative(ctx, h)
		if e != nil {
			return core.ProviderResult{}, e
		}
		status := map[string]core.ProviderStatus{"queued": core.ProviderQueued, "running": core.ProviderRunning, "succeeded": core.ProviderSucceeded, "failed": core.ProviderFailed, "cancelled": core.ProviderFailed}[t.Status]
		out := core.ProviderResult{Status: status}
		if status == core.ProviderFailed {
			out.Error = "MiniMax task " + t.Status
		}
		if status == core.ProviderSucceeded {
			if h.Kind == "context" {
				out.Artifacts = []core.ProviderArtifact{{ID: "prepared-prompt", MediaType: "text", Format: "txt", Text: t.Content.Prompt}}
			} else {
				out.ResultURL = t.Content.URL
			}
			out.Usage, e = normalizeH3Usage(t.Usage, h.Model)
			if e != nil {
				return core.ProviderResult{}, e
			}
		}
		return out, nil
	}
	if h.Kind != "legacy" && h.Kind != "agent" {
		return core.ProviderResult{}, errors.New("MiniMax resource is not a video-generation handle")
	}
	op, ok := operationFor(h.Model)
	if !ok || op.kind != h.Kind {
		return core.ProviderResult{}, errors.New("MiniMax task model does not match its operation")
	}
	path := "/v1/query/video_generation"
	if h.Kind == "agent" {
		path = "/v1/query/video_template_generation"
	}
	var out struct {
		ID     string `json:"task_id"`
		Status string `json:"status"`
		File   string `json:"file_id"`
		URL    string `json:"video_url"`
	}
	if e = a.json(ctx, "GET", path+"?task_id="+url.QueryEscape(h.ID), nil, &out); e != nil {
		return core.ProviderResult{}, e
	}
	if out.ID != h.ID {
		return core.ProviderResult{}, errors.New("MiniMax legacy task identity does not match its handle")
	}
	state, ok := map[string]core.ProviderStatus{"Preparing": core.ProviderQueued, "Queueing": core.ProviderQueued, "Processing": core.ProviderRunning, "Success": core.ProviderSucceeded, "Fail": core.ProviderFailed}[out.Status]
	if !ok {
		return core.ProviderResult{}, errors.New("MiniMax returned an unknown legacy task state")
	}
	result := core.ProviderResult{Status: state}
	if state == core.ProviderFailed {
		result.Error = "MiniMax task failed"
	}
	if state == core.ProviderSucceeded {
		if h.Kind == "agent" {
			result.ResultURL = out.URL
		} else {
			if !numericID.MatchString(out.File) {
				return core.ProviderResult{}, errors.New("MiniMax legacy result file ID is invalid")
			}
			file, e := a.retrieveFile(ctx, out.File)
			if e != nil {
				return core.ProviderResult{}, e
			}
			if file.Purpose != "video_generation" {
				return core.ProviderResult{}, errors.New("MiniMax generation returned a file with an unexpected purpose")
			}
			if file.DownloadURL == "" {
				file.MediaKind = "video"
				file = a.fileView(ctx, file)
				result.Artifacts = []core.ProviderArtifact{{ID: "video", MediaType: "video", Format: "mp4", SourceReference: file.Handle}}
			} else {
				result.ResultURL = file.DownloadURL
			}
		}
		if len(result.Artifacts) == 0 && !publicURL(result.ResultURL) {
			return core.ProviderResult{}, errors.New("MiniMax returned an invalid video result URL")
		}
	}
	return result, nil
}
func normalizeH3Usage(raw json.RawMessage, model string) (*core.ProviderUsage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return nil, errors.New("MiniMax usage is malformed")
	}
	counters := map[string]float64{}
	fields := []struct{ field, metric, unit string }{{"input_seconds", "input_video_duration", "second"}, {"output_seconds", "output_video_duration", "second"}, {"input_image_count", "input_image_count", "image"}, {"input_audio_seconds", "input_audio_duration", "second"}, {"prompt_tokens", "prompt_tokens", "token"}, {"completion_tokens", "completion_tokens", "token"}, {"total_tokens", "total_tokens", "token"}, {"total_seconds", "total_video_duration", "second"}}
	metrics := []core.UsageMetric{}
	for _, f := range fields {
		member, ok := members[f.field]
		if !ok || string(member) == "null" {
			continue
		}
		var q float64
		if json.Unmarshal(member, &q) != nil || q < 0 || math.Trunc(q) != q || q > 1<<53 {
			return nil, errors.New("MiniMax usage counter is not a non-negative integer")
		}
		counters[f.field] = q
		metrics = append(metrics, core.UsageMetric{Name: f.metric, Unit: f.unit, Quantity: q})
	}
	for _, sum := range [][3]string{{"total_seconds", "input_seconds", "output_seconds"}, {"total_tokens", "prompt_tokens", "completion_tokens"}} {
		total, tok := counters[sum[0]]
		a, aok := counters[sum[1]]
		b, bok := counters[sum[2]]
		if tok && aok && bok && total != a+b {
			return nil, errors.New("MiniMax usage counters are inconsistent")
		}
	}
	if images, ok := counters["input_image_count"]; ok {
		free := 5.
		if model == "MiniMax-H3-Max" {
			free = 2
		}
		metrics = append(metrics, core.UsageMetric{Name: "billable_input_images", Unit: "image", Quantity: max(0, images-free)})
	}

	return &core.ProviderUsage{Raw: append(json.RawMessage(nil), raw...), Metrics: metrics}, nil
}

// CreateContext prepares a prompt without generating video. It uses H3's same
// reference contract; resolution is inapplicable and is deliberately omitted.
func (a *Adapter) CreateContext(ctx context.Context, r core.MediaRequest) (string, error) {
	if a.requestConnectionScope(ctx) == "" {
		return "", errors.New("MiniMax provider connection identity is not bound")
	}
	if r.Model != "MiniMax-H3" {
		return "", invalid("Context-IR is documented only for MiniMax-H3")
	}
	if r.Options.Resolution != "" || r.Options.GenerateAudio != nil || len(r.Options.Parameters) > 0 {
		return "", invalid("Context-IR does not accept video output controls")
	}
	if e := a.ValidateRequest(ctx, r); e != nil {
		return "", e
	}
	r, e := a.resolveRequestFiles(ctx, r)
	if e != nil {
		return "", e
	}
	op, _ := operationFor(r.Model)
	payload, e := requestBody(op, r)
	if e != nil {
		return "", e
	}
	delete(payload, "resolution")
	var out struct {
		ID string `json:"task_id"`
	}
	if e = a.json(ctx, "POST", "/v2/h3_context_ir", payload, &out); e != nil {
		return "", e
	}
	if !numericID.MatchString(out.ID) {
		return "", errors.New("MiniMax Context-IR task ID is invalid")
	}
	return a.seal(ctx, handle{ID: out.ID, Model: r.Model, Kind: "context"}), nil
}

// RegenerateTask uses the documented whitelist-gated source-task method. A raw
// upstream ID is never accepted: the source must be a succeeded H3 768P task
// owned by this Gateway owner and the same configured provider account.
func (a *Adapter) RegenerateTask(ctx context.Context, source string, watermark *bool) (string, error) {
	h, e := a.open(ctx, source)
	if e != nil {
		return "", e
	}
	if h.Kind != "h3" || h.Model != "MiniMax-H3" {
		return "", invalid("regeneration requires an original MiniMax-H3 task")
	}
	t, e := a.queryNative(ctx, h)
	if e != nil {
		return "", e
	}
	if t.Status != "succeeded" || t.Resolution != "768P" {
		return "", invalid("regeneration source must be a succeeded H3 768P task")
	}
	payload := map[string]any{"model": "MiniMax-H3", "source_task_id": h.ID, "resolution": "2K"}
	if watermark != nil {
		payload["aigc_watermark"] = *watermark
	}
	var out struct {
		ID string `json:"task_id"`
	}
	if e = a.json(ctx, "POST", "/v2/video_regeneration", payload, &out); e != nil {
		return "", e
	}
	if !numericID.MatchString(out.ID) {
		return "", errors.New("MiniMax regeneration task ID is invalid")
	}
	return a.seal(ctx, handle{ID: out.ID, Model: "MiniMax-H3", Kind: "regeneration"}), nil
}

// CancelOrDeleteTask lets MiniMax enforce its current-state gate: queued tasks
// can be cancelled; succeeded/failed records deleted. Running tasks are rejected.
// It is never called as cleanup after generation or a polling failure.
func (a *Adapter) CancelOrDeleteTask(ctx context.Context, token string) (string, error) {
	h, e := a.open(ctx, token)
	if e != nil {
		return "", e
	}
	if h.Kind != "h3" && h.Kind != "context" && h.Kind != "regeneration" {
		return "", invalid("this task family has no documented cancellation endpoint")
	}
	var out struct {
		ID     string `json:"task_id"`
		Action string `json:"action"`
		Status string `json:"status"`
	}
	if e = a.json(ctx, "DELETE", "/v2/video_generation/"+h.ID, nil, &out); e != nil {
		return "", e
	}
	if out.ID != h.ID || out.Action != out.Status || !slices.Contains([]string{"cancelled", "deleted"}, out.Action) {
		return "", errors.New("MiniMax cancellation result is malformed; outcome is uncertain")
	}
	return out.Action, nil
}

// ListTasks filters the remote account inventory to explicitly owner-bound task
// handles. Unbound or foreign tasks never become discoverable via this method.
func (a *Adapter) ListTasks(ctx context.Context, tokens []string, page, size int) ([]NativeTask, int, error) {
	if len(tokens) == 0 || len(tokens) > 100 || page < 1 || size < 1 || size > 100 {
		return nil, 0, invalid("list requires 1–100 owned handles and valid pagination")
	}
	q := url.Values{"page_num": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(size)}}
	allowed := map[string]string{}
	for _, token := range tokens {
		h, e := a.open(ctx, token)
		if e != nil {
			return nil, 0, e
		}
		if h.Kind != "h3" && h.Kind != "context" && h.Kind != "regeneration" {
			return nil, 0, invalid("invalid task list family")
		}
		q.Add("filter.task_ids", h.ID)
		allowed[h.ID] = token
	}
	var out struct {
		Items []NativeTask `json:"items"`
		Total int          `json:"total"`
	}
	if e := a.json(ctx, "GET", "/v2/query/video_generation?"+q.Encode(), nil, &out); e != nil {
		return nil, 0, e
	}
	if out.Total < 0 || out.Total > len(allowed) || len(out.Items) > size {
		return nil, 0, errors.New("MiniMax task list exceeds the requested owner scope")
	}
	seen := map[string]bool{}
	for i := range out.Items {
		token, ok := allowed[out.Items[i].ID]
		if !ok {
			return nil, 0, errors.New("MiniMax task list contains an unrequested resource")
		}
		h, _ := a.open(ctx, token)
		if seen[h.ID] {
			return nil, 0, errors.New("MiniMax task list duplicates a resource")
		}
		seen[h.ID] = true
		if e := validateNativeTask(out.Items[i], h); e != nil {
			return nil, 0, e
		}
		out.Items[i].ID = token
		if out.Items[i].Error != nil {
			out.Items[i].Error.Message = "MiniMax task failed"
		}
	}
	return out.Items, out.Total, nil
}

// RegenerateVideo sends the exact original content plus the owned source output
// as base_video. The submission digest prevents caller changes to original inputs.
func (a *Adapter) RegenerateVideo(ctx context.Context, source string, original core.MediaRequest, watermark *bool) (string, error) {
	h, e := a.open(ctx, source)
	if e != nil {
		return "", e
	}
	if h.Kind != "h3" || h.Model != "MiniMax-H3" || original.Model != h.Model || h.InputHash == "" {
		return "", invalid("base-video regeneration requires an owned original H3 task")
	}
	if e = a.ValidateRequest(ctx, original); e != nil {
		return "", e
	}
	original, e = a.resolveRequestFiles(ctx, original)
	if e != nil {
		return "", e
	}
	op, _ := operationFor(original.Model)
	payload, e := requestBody(op, original)
	if e != nil {
		return "", e
	}
	if contentHash(payload["content"]) != h.InputHash {
		return "", invalid("regeneration must reproduce the exact original prompt and input materials")
	}
	t, e := a.queryNative(ctx, h)
	if e != nil {
		return "", e
	}
	if t.Status != "succeeded" || t.Resolution != "768P" {
		return "", invalid("base video must be the succeeded 768P H3 output")
	}
	content := payload["content"].([]any)
	content = append(content, map[string]any{"type": "video_url", "video_url": map[string]string{"url": t.Content.URL}, "role": "base_video"})
	body := map[string]any{"model": "MiniMax-H3", "content": content, "resolution": "2K"}
	if watermark != nil {
		body["aigc_watermark"] = *watermark
	}
	var out struct {
		ID string `json:"task_id"`
	}
	if e = a.json(ctx, "POST", "/v2/video_regeneration", body, &out); e != nil {
		return "", e
	}
	if !numericID.MatchString(out.ID) {
		return "", errors.New("MiniMax regeneration task ID is invalid")
	}
	return a.seal(ctx, handle{ID: out.ID, Model: "MiniMax-H3", Kind: "regeneration"}), nil
}
