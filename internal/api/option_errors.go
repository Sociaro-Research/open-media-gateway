package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// Generation option diagnostics use the same public field names as admission.
func generationValidationError(w http.ResponseWriter, input core.CreateMediaInput, err error) {
	if field, hint, ok := core.ResourceValidationDetails(err); ok {
		path := "options.resource_intent"
		if field != "" {
			path += "." + field
		}
		invalidParameter(w, path, path+" "+hint)
		return
	}
	if hint, ok := core.PromptValidationHint(err); ok {
		field := "prompt"
		if input.MediaType == "speech" {
			field = "input"
		}
		invalidParameter(w, field, core.ErrPromptTooLong.Error()+": "+field+" "+hint)
		return
	}
	var option *core.OptionValidationError
	if !errors.As(err, &option) {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	hint := option.Hint
	if option.Field == "resolution" {
		hint = strings.ReplaceAll(hint, "*", "x")
	}
	field := "options." + option.Field
	switch option.Field {
	case "prompt":
		field = "prompt"
		if input.MediaType == "speech" {
			field = "input"
		}
	case "voice", "quality", "background":
		field = option.Field
	case "format":
		switch input.MediaType {
		case "speech":
			field = "response_format"
		case "image":
			field = "output_format"
		}
	case "duration":
		if input.MediaType == "video" {
			field = "seconds"
		}
	case "resolution":
		field = "size"
	case "output_count":
		field = "n"
	case "image_urls":
		field = "image"
		if input.MediaType == "video" {
			field = "input_reference"
		}
	}
	// Keep any indexed path suffix, and never include the rejected request value.
	if strings.HasPrefix(option.Field, "image_urls.") {
		field = "image." + strings.TrimPrefix(option.Field, "image_urls.")
		if input.MediaType == "video" {
			field = "input_reference." + strings.TrimPrefix(option.Field, "image_urls.")
		}
	}
	invalidParameter(w, field, core.ErrOptionsInvalid.Error()+": "+field+" "+hint)
}
