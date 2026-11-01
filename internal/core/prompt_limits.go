package core

// MaxPromptCharacters is the gateway admission ceiling in Unicode code points.
// Admission trims surrounding whitespace before counting. This is not a claim
// about an upstream provider's unknown or token-based limits.
const MaxPromptCharacters = 10000

// EffectivePromptCharacterLimit reports the known enforced input ceiling.
// Native adapters may expose a lower bound from the same rule their validator
// uses. Manual model requests bypass those native rules. A model without a
// prompt has no applicable character-limit capability.
func (m MediaModel) EffectivePromptCharacterLimit() int {
	if m.NoPrompt {
		return 0
	}
	if !m.Manual && m.PromptPolicy != nil && m.PromptPolicy.MaxCharacters > 0 && m.PromptPolicy.MaxCharacters <= MaxReviewedPromptCharacters {
		return m.PromptPolicy.MaxCharacters
	}
	if !m.Manual && m.MaxPromptCharacters > 0 && m.MaxPromptCharacters < MaxPromptCharacters {
		return m.MaxPromptCharacters
	}
	return MaxPromptCharacters
}
