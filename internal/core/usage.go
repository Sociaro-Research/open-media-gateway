package core

import (
	"encoding/json"
	"strings"
)

var usageUnits = map[string]string{
	"total_tokens": "token", "completion_tokens": "token", "input_tokens": "token", "output_tokens": "token",
	"audio_tokens": "token", "generated_images": "image", "image_count": "image", "video_count": "video",
	"video_duration": "second", "duration": "second", "output_video_duration": "second", "input_video_duration": "second",
	"audio_duration": "second", "characters": "character", "character_count": "character",
}

// NormalizeProviderUsage preserves the vendor payload and extracts common
// billable counters without pretending all media products use the same unit.
func NormalizeProviderUsage(raw map[string]any) *ProviderUsage {
	if len(raw) == 0 {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	usage := &ProviderUsage{Raw: encoded, CostStatus: "metered"}
	seen := make(map[string]bool)
	var walk func(map[string]any)
	walk = func(value map[string]any) {
		for name, item := range value {
			key := strings.ToLower(name)
			if nested, ok := item.(map[string]any); ok {
				walk(nested)
				continue
			}
			unit, ok := usageUnits[key]
			if !ok || seen[key] {
				continue
			}
			quantity, ok := numericUsage(item)
			if !ok {
				continue
			}
			seen[key] = true
			usage.Metrics = append(usage.Metrics, UsageMetric{Name: key, Unit: unit, Quantity: quantity})
		}
	}
	walk(raw)
	return usage
}

func numericUsage(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
