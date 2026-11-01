package core

import "fmt"

// PromptPolicy is an explicitly reviewed native admission policy. ASCII/non-ASCII
// budgets describe Gateway's enforced classification, not an undocumented vendor
// tokenizer, UTF-16 rule or linguistic definition of English characters.
type PromptPolicy struct {
	MaxCharacters  int `json:"max_characters"`
	ASCIIBudget    int `json:"ascii_budget,omitempty"`
	NonASCIIBudget int `json:"non_ascii_budget,omitempty"`
}

const MaxReviewedPromptCharacters = 26000

func ValidatePromptPolicy(model MediaModel, prompt string) error {
	if model.NoPrompt {
		return nil
	}
	if len([]rune(prompt)) > model.EffectivePromptCharacterLimit() {
		return ErrPromptTooLong
	}
	if model.Manual {
		return nil
	}
	p := model.PromptPolicy
	if p == nil {
		return nil
	}
	if p.MaxCharacters <= 0 || p.MaxCharacters > MaxReviewedPromptCharacters || p.ASCIIBudget < 0 || p.NonASCIIBudget < 0 {
		return fmt.Errorf("%w: invalid reviewed prompt policy", ErrOptionsInvalid)
	}
	if p.ASCIIBudget == 0 && p.NonASCIIBudget == 0 {
		return nil
	}
	if p.ASCIIBudget == 0 || p.NonASCIIBudget == 0 || p.ASCIIBudget > MaxReviewedPromptCharacters || p.NonASCIIBudget > MaxReviewedPromptCharacters {
		return fmt.Errorf("%w: invalid reviewed prompt budget", ErrOptionsInvalid)
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
		return fmt.Errorf("%w: mixed-script prompt budget exceeded", ErrPromptTooLong)
	}
	return nil
}
