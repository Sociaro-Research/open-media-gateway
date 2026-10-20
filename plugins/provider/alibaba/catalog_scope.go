package alibaba

import (
	"strings"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/catalogsync"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/modelcatalog"
)

// Reviewed on 2026-10-12 for the file-generation catalogue. These exact services
// remain visible as non-executable history, with their scope and source evidence.
func (a *Adapter) CatalogExclusions() []catalogsync.Exclusion {
	var result []catalogsync.Exclusion
	add := func(ids, reason, source string) {
		for _, id := range strings.Fields(ids) {
			if modelcatalog.IsInactive("alibaba", id, time.Now()) {
				continue
			}
			result = append(result, catalogsync.Exclusion{ID: id, Reason: reason, Metadata: &core.ModelMetadata{CheckedAt: "2026-10-12", Facts: []core.ModelFact{{Label: "Catalogue scope", Value: reason}}, Sources: []core.ModelSource{{Title: "Alibaba service contract", URL: source}}}})
		}
	}
	add(`happyoyster-1.0-adventure happyoyster-1.0-directing`, "Interactive world lifecycle and RTC video sessions are outside file generation.", "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/happyoyster-overview")
	add(`qwen-voice-enrollment voice-enrollment qwen-voice-design`, "Creates or manages reusable voice profiles; this is not a media generation job.", "https://www.alibabacloud.com/help/en/model-studio/voice-clone-design-http-api")
	add(`wan2.2-s2v-detect`, "Portrait compliance analysis returns a JSON validation result, not a generated file.", "https://www.alibabacloud.com/help/en/model-studio/wan-s2v-detect-api")
	add(`qwen-audio-3.0-realtime-flash qwen-audio-3.0-realtime-plus qwen-audio-3.1-realtime-plus
 qwen3-omni-flash qwen3-omni-flash-realtime-2025-09-15
 qwen3.5-omni-flash qwen3.5-omni-flash-2026-03-15 qwen3.5-omni-flash-realtime qwen3.5-omni-flash-realtime-2026-03-15
 qwen3.5-omni-plus qwen3.5-omni-plus-2026-03-15 qwen3.5-omni-plus-realtime qwen3.5-omni-plus-realtime-2026-03-15
 qwen3.8-omni-flash-realtime`, "Multimodal conversation or bidirectional audio sessions are outside file generation.", "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/realtime-api-overview")
	add(`qwen3-livetranslate-flash qwen3-livetranslate-flash-2025-12-01
 qwen3.5-livetranslate-flash-realtime qwen3.5-livetranslate-flash-realtime-2026-05-19 qwen3.8-livetranslate-flash-realtime`, "Live speech translation sessions are outside file generation.", "https://www.alibabacloud.com/help/en/model-studio/s2s-model")
	add(`qwen3-tts-flash-realtime`, "Streaming speech sessions are outside file generation; use a reviewed HTTP TTS model for an audio file.", "https://www.alibabacloud.com/help/en/model-studio/tts-model")
	return result
}
