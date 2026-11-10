package core

import (
	"errors"
	"fmt"
)

type promptValidationError struct{ hint string }

func (e *promptValidationError) Error() string { return ErrPromptTooLong.Error() + ": " + e.hint }
func (e *promptValidationError) Unwrap() error { return ErrPromptTooLong }

// PromptValidationHint exposes only diagnostics produced by the local policy,
// never arbitrary wrapped provider text or any part of the prompt.
func PromptValidationHint(err error) (string, bool) {
	var detail *promptValidationError
	if errors.As(err, &detail) {
		return detail.hint, true
	}
	return "", false
}

func promptLengthError(length, limit int) error {
	return &promptValidationError{fmt.Sprintf("has %d characters; maximum %d Unicode code points under the effective Gateway limit. Shorten the prompt", length, limit)}
}

// PromptPolicy is an explicitly reviewed native admission policy. ASCII/non-ASCII
// budgets describe Gateway's enforced classification, not an undocumented vendor
// tokenizer, UTF-16 rule or linguistic definition of English characters.
type PromptPolicy struct {
	// PreserveWhitespace retains reviewed native binding separators verbatim.
	PreserveWhitespace bool `json:"preserve_whitespace,omitempty"`
	MaxCharacters      int  `json:"max_characters"`
	ASCIIBudget        int  `json:"ascii_budget,omitempty"`
	NonASCIIBudget     int  `json:"non_ascii_budget,omitempty"`
}

// MaxReviewedPromptCharacters is a parser safety bound, not the ordinary or
// manual-model admission limit. Only an explicit non-manual PromptPolicy may
// opt into a larger reviewed native ceiling.
const MaxReviewedPromptCharacters = 40000

func ValidatePromptPolicy(model MediaModel, prompt string) error {
	if model.NoPrompt {
		return nil
	}
	if len([]rune(prompt)) > model.EffectivePromptCharacterLimit() {
		return promptLengthError(len([]rune(prompt)), model.EffectivePromptCharacterLimit())
	}
	if model.Manual {
		return nil
	}
	p := model.PromptPolicy
	if p == nil {
		return nil
	}
	if p.MaxCharacters <= 0 || p.MaxCharacters > MaxReviewedPromptCharacters || p.ASCIIBudget < 0 || p.NonASCIIBudget < 0 {
		return optionError("prompt", "has an invalid reviewed model policy; ask an operator to check the model configuration")
	}
	if p.ASCIIBudget == 0 && p.NonASCIIBudget == 0 {
		return nil
	}
	if p.ASCIIBudget == 0 || p.NonASCIIBudget == 0 || p.ASCIIBudget > MaxReviewedPromptCharacters || p.NonASCIIBudget > MaxReviewedPromptCharacters {
		return optionError("prompt", "has an invalid reviewed model budget; ask an operator to check the model configuration")
	}
	ascii, other := int64(0), int64(0)
	for _, r := range prompt {
		if r < 128 {
			ascii++
		} else {
			other++
		}
	}
	if ascii*int64(p.NonASCIIBudget)+other*int64(p.ASCIIBudget) > int64(p.ASCIIBudget)*int64(p.NonASCIIBudget) {
		return &promptValidationError{fmt.Sprintf("has %d ASCII and %d non-ASCII code points; the reviewed Gateway budget requires ASCII/%d + non-ASCII/%d <= 1. Shorten the prompt to fit this mixed-script budget", ascii, other, p.ASCIIBudget, p.NonASCIIBudget)}
	}
	return nil
}
