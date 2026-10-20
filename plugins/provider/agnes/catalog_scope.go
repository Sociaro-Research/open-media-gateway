package agnes

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// These exact text-output IDs were reviewed on 2026-10-14. Image understanding
// does not make a chat model an image-generation model. Unknown IDs still warn.
func (*Adapter) CatalogExclusions() []catalogsync.Exclusion {
	var result []catalogsync.Exclusion
	for _, item := range []struct{ id, source string }{
		{"agnes-2.5-flash", priceSource},
		{"agnes-2.5-pro", "https://www.agnes-ai.com/en/docs/agnes-25-pro"},
		{"agnes-2.5-pro-alpha", "https://huggingface.co/Agnes-AI/Agnes-2.5-Pro-Alpha"},
		{"agnes-2.5-pro-beta", priceSource},
		{"agnes-3.0-flash", "https://www.agnes-ai.com/en/docs/agnes-30-flash"},
		{"agnes-3.0-flash-max", priceSource},
	} {
		reason := "Text-output chat/reasoning model, outside image, audio and video file generation."
		result = append(result, catalogsync.Exclusion{ID: item.id, Reason: reason, Metadata: &core.ModelMetadata{CheckedAt: "2026-10-14", Facts: []core.ModelFact{{Label: "Catalogue scope", Value: reason}}, Sources: []core.ModelSource{{Title: "Agnes text model contract", URL: item.source}}}})
	}
	return result
}
