package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Generation is the single public representation shared by HTTP and webhooks.
type Generation struct {
	Task
	Prompt      string `json:"prompt"`
	Object      string `json:"object"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	CompletedAt *int64 `json:"completed_at"`
	Error       any    `json:"error"`
	StatusURL   string `json:"status_url"`
	ContentURL  string `json:"content_url"`
	Seconds     string `json:"seconds,omitempty"`
	Size        string `json:"size,omitempty"`
}

func GenerationView(task Task) Generation {
	base := "/v1/generations/" + task.ID
	object := "generation"
	if task.MediaType == "video" {
		base = "/v1/videos/" + task.ID
		object = "video"
	}
	v := Generation{Task: task, Prompt: task.Prompt, Object: object, Status: string(task.Status), CreatedAt: task.CreatedAt.Unix(), UpdatedAt: task.UpdatedAt.Unix(), StatusURL: base, ContentURL: base + "/content"}
	// Stored jobs may predate these routes; always derive public URLs from the ID.
	v.ProviderResultURL = nil
	v.ResultURL = nil
	v.LastFrameURL = nil
	if task.ResultDeletedAt == nil {
		if task.Status == TaskSucceeded && (task.ResultURL != nil || task.HasStoredResult || task.StorageKey != nil) {
			v.ResultURL = &v.ContentURL
		}
		if task.LastFrameURL != nil || task.LastFrameStorageKey != nil {
			lastFrame := "/v1/generations/" + task.ID + "/last-frame"
			v.LastFrameURL = &lastFrame
		}
	}
	switch task.Status {
	case TaskRunning:
		v.Status = "in_progress"
	case TaskSucceeded:
		v.Status = "completed"
	}
	if task.Status == TaskSucceeded && task.StoredAt != nil {
		t := task.StoredAt.Unix()
		v.CompletedAt = &t
	}
	if task.Error != nil {
		v.Error = map[string]string{"code": "generation_failed", "message": *task.Error}
	}
	if task.MediaType == "video" {
		v.Seconds = strconv.Itoa(task.Options.Duration)
	}
	v.Size = strings.ReplaceAll(task.Options.Resolution, "*", "x")
	if task.MediaType == "video" {
		// Express a native p-resolution as dimensions only when the ratio gives
		// an exact integer size; never invent rounded provider dimensions.
		ratio := strings.Split(task.Options.Ratio, ":")
		short, err := strconv.Atoi(strings.TrimSuffix(task.Options.Resolution, "p"))
		if err == nil && short > 0 && len(ratio) == 2 {
			a, e1 := strconv.Atoi(ratio[0])
			b, e2 := strconv.Atoi(ratio[1])
			if e1 == nil && e2 == nil && a > 0 && b > 0 && short <= 10000 && a <= 10000 && b <= 10000 {
				unit := min(a, b)
				if short*a%unit == 0 && short*b%unit == 0 {
					v.Size = fmt.Sprintf("%dx%d", short*a/unit, short*b/unit)
				}
			}
		}
	}
	return v
}

// MarshalJSON keeps storage/provider fields out of the public contract and emits
// standard controls in exactly the same locations as generation requests.
func (v Generation) MarshalJSON() ([]byte, error) {
	type plain Generation
	b, err := json.Marshal(plain(v))
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if err = decoder.Decode(&fields); err != nil {
		return nil, err
	}
	fields["model"] = v.PublicModel
	if v.PublicModel == "" {
		fields["model"] = v.Provider + "/" + v.Model
	}
	fields["provider_model"] = v.Model
	if v.Options.TimeoutSeconds != nil {
		fields["timeout_seconds"] = *v.Options.TimeoutSeconds
	}
	delete(fields, "result_url")
	options, _ := fields["options"].(map[string]any)
	for _, key := range []string{"timeout_seconds", "duration", "resolution", "image_urls", "video_url", "audio_url", "voice", "instructions", "quality", "background"} {
		delete(options, key)
	}
	if v.MediaType == "speech" {
		delete(fields, "prompt")
		fields["input"] = v.Prompt
		fields["voice"] = v.Options.Voice
		if v.Options.Instructions != "" {
			fields["instructions"] = v.Options.Instructions
		}
		if v.Options.Format != "" {
			fields["response_format"] = v.Options.Format
		}
		delete(options, "format")
	}
	if v.MediaType == "image" {
		if v.Options.Quality != "" {
			fields["quality"] = v.Options.Quality
		}
		if v.Options.Background != "" {
			fields["background"] = v.Options.Background
		}
		if v.Options.Format != "" {
			fields["output_format"] = v.Options.Format
		}
		delete(options, "format")
	}
	if len(v.Options.ImageURLs) > 0 {
		key := "image"
		if v.MediaType == "video" {
			key = "input_reference"
		}
		fields[key] = v.Options.ImageURLs
	}
	if v.MinimizeRequestLogging {
		// The response and persisted webhook share this representation. Never
		// copy in-flight content into a second long-lived record.
		for _, key := range []string{"prompt", "input", "instructions", "image", "input_reference", "voice", "webhook_url"} {
			delete(fields, key)
		}
		fields["options"] = map[string]any{}
		if usage, ok := fields["usage"].(map[string]any); ok {
			delete(usage, "raw")
		}
	}
	return json.Marshal(fields)
}

// GenerationEventType maps persisted lifecycle events to the public vocabulary.
func GenerationEventType(event TaskEventType) string {
	switch event {
	case EventTaskCreated:
		return "generation.queued"
	case EventTaskStarted:
		return "generation.in_progress"
	case EventTaskSucceeded:
		return "generation.completed"
	case EventTaskFailed:
		return "generation.failed"
	default:
		return ""
	}
}
