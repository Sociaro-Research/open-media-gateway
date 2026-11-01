package minimax

import (
	"context"
	"errors"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// CreateVideoAgent preserves the API's ordered input arrays. The official index
// declares required input kinds but not every template's placeholder cardinality;
// callers supply those arrays explicitly rather than the adapter inventing counts.
func (a *Adapter) CreateVideoAgent(ctx context.Context, model string, images, texts []string) (core.ProviderTask, error) {
	if a.requestConnectionScope(ctx) == "" {
		return core.ProviderTask{}, errors.New("MiniMax provider connection identity is not bound")
	}
	op, ok := operationFor(model)
	if !ok || op.kind != "agent" {
		return core.ProviderTask{}, invalid("unreviewed Video Agent template")
	}
	if op.template.image != (len(images) > 0) || op.template.text != (len(texts) > 0) || len(images)+len(texts) > 64 {
		return core.ProviderTask{}, invalid("template inputs do not match the documented required kinds or Gateway input ceiling")
	}
	mediaInputs := []map[string]string{}
	textInputs := []map[string]string{}
	for _, image := range images {
		if !validInput(image, "image", 20_000_000-1) {
			return core.ProviderTask{}, invalid("Video Agent images require documented HTTPS/Base64 input")
		}
		if strings.HasPrefix(image, "data:") && !strings.HasPrefix(image, "data:image/jpeg;") && !strings.HasPrefix(image, "data:image/png;") && !strings.HasPrefix(image, "data:image/webp;") {
			return core.ProviderTask{}, invalid("Video Agent accepts only JPEG/PNG/WebP")
		}
		mediaInputs = append(mediaInputs, map[string]string{"value": image})
	}
	for _, text := range texts {
		if strings.TrimSpace(text) == "" || len([]rune(text)) > core.MaxPromptCharacters {
			return core.ProviderTask{}, invalid("template text is empty or exceeds the Gateway character ceiling")
		}
		textInputs = append(textInputs, map[string]string{"value": text})
	}
	payload := map[string]any{"template_id": op.template.id}
	if len(mediaInputs) > 0 {
		payload["media_inputs"] = mediaInputs
	}
	if len(textInputs) > 0 {
		payload["text_inputs"] = textInputs
	}
	var out struct {
		ID string `json:"task_id"`
	}
	if err := a.json(ctx, "POST", op.endpoint, payload, &out); err != nil {
		return core.ProviderTask{}, err
	}
	if !numericID.MatchString(out.ID) {
		return core.ProviderTask{}, errors.New("MiniMax Video Agent returned an invalid task ID")
	}
	return core.ProviderTask{ID: a.seal(ctx, handle{ID: out.ID, Model: model, Kind: "agent"})}, nil
}
