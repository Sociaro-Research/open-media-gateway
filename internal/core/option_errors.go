package core

import (
	"fmt"
	"strings"
)

// OptionValidationError identifies a model option without retaining request data.
// Field uses the provider-neutral MediaOptions name; transports map public aliases.
type OptionValidationError struct {
	Field string
	Hint  string
}

func (e *OptionValidationError) Error() string {
	return fmt.Sprintf("%s: %s %s", ErrOptionsInvalid, e.Field, e.Hint)
}

func (e *OptionValidationError) Unwrap() error { return ErrOptionsInvalid }

func optionError(field, hint string) error {
	return &OptionValidationError{Field: field, Hint: hint}
}

func optionChoices(field string, choices []string) error {
	if len(choices) == 0 {
		return optionError(field, "is not supported by this model; omit it")
	}
	return optionError(field, "must be one of: "+strings.Join(choices, ", "))
}

func durationError(model MediaModel) error {
	hint := fmt.Sprintf("must be between %d and %d seconds", model.MinDuration, model.MaxDuration)
	if model.DurationStep > 0 {
		hint += fmt.Sprintf(" in increments of %d starting at %d", model.DurationStep, model.MinDuration)
	}
	if model.DurationMayBeInferred && model.MediaType != "audio" && model.MediaType != "music" {
		hint += "; omit it to infer from input"
	}
	return optionError("duration", hint)
}
