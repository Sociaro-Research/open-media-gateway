package core

// VoiceChoice describes the exact existing voice/language controls. Defaults
// are Gateway UI defaults, not claims about an upstream omitted-field default.
// An absent list preserves the provider's existing voice/language behavior.
type VoiceChoice struct {
	ID              string   `json:"id"`
	Languages       []string `json:"languages"`
	DefaultLanguage string   `json:"default_language"`
	IsDefault       bool     `json:"is_default,omitempty"`
}
