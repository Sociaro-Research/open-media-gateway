package catalogsync

import (
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"math"
	"strings"
)

func nativeOperationalFields(model core.MediaModel) map[string]json.RawMessage {
	result := map[string]json.RawMessage{}
	if model.ReferencePromptRecommendation != nil {
		result["reference_prompt_recommendation"], _ = json.Marshal(model.ReferencePromptRecommendation)
	}
	assessment := AssessCompleteness(State{}, Capture{Models: []core.MediaModel{model}}, nil, nil)
	if len(assessment.Models) > 0 {
		for _, field := range assessment.Models[0].Fields {
			if len(field.Value) > 0 {
				result[field.Field] = field.Value
			}
		}
	}
	return result
}

// compatibleOperationalField verifies the gateway's supported subset. Extra
// upstream values never enable options or overwrite a native contract.
func compatibleOperationalField(field string, native, published json.RawMessage) bool {
	if strings.HasSuffix(field, videoDurationSumSuffix) {
		var envelope videoDurationEnvelope
		var limit VideoDurationSumLimit
		return json.Unmarshal(native, &envelope) == nil && json.Unmarshal(published, &limit) == nil && envelope.InputMaxSeconds > 0 && limit.Allows(envelope.InputMaxSeconds, envelope.OutputMaxSeconds)
	}
	if field == "reference_prompt_recommendation" {
		var n, p core.ReferencePromptRecommendation
		return json.Unmarshal(native, &n) == nil && json.Unmarshal(published, &p) == nil && n.AcceptsObservation(p)
	}
	if sameOperationalValue(field, native, published) {
		return true
	}
	var nStrings, pStrings []string
	if json.Unmarshal(native, &nStrings) == nil && json.Unmarshal(published, &pStrings) == nil && nStrings != nil && pStrings != nil {
		allowed := map[string]bool{}
		for _, v := range pStrings {
			allowed[v] = true
		}
		for _, v := range nStrings {
			if !allowed[v] {
				return false
			}
		}
		return true
	}
	if field == "prompt_policy" {
		var n, p map[string]bool
		if json.Unmarshal(native, &n) != nil || json.Unmarshal(published, &p) != nil {
			return false
		}
		return (!n["no_prompt"] || p["no_prompt"] || p["optional"]) && (!n["optional"] || p["optional"] || p["no_prompt"]) && (!p["no_prompt"] || n["no_prompt"] || n["optional"])
	}
	if strings.HasSuffix(field, ".count") || strings.HasSuffix(field, ".visual_count") || field == "output.duration" {
		var n, p map[string]float64
		if json.Unmarshal(native, &n) != nil || json.Unmarshal(published, &p) != nil {
			return false
		}
		min, hasMin := p["min"]
		max, hasMax := p["max"]
		if !hasMin || !hasMax || max < min || n["min"] < min || n["max"] > max || n["max"] < n["min"] {
			return false
		}
		if field == "output.duration" {
			if n["default"] < min || n["default"] > max {
				return false
			}
			if step := p["step"]; step > 0 && math.Mod(math.Max(1, n["step"]), step) != 0 {
				return false
			}
		}
		return true
	}
	if strings.HasSuffix(field, ".max_bytes") || strings.HasSuffix(field, ".max_duration_with_video") || field == "reference_media_limits.max_files" {
		var n, p float64
		return json.Unmarshal(native, &n) == nil && json.Unmarshal(published, &p) == nil && n > 0 && n <= p
	}
	if strings.HasSuffix(field, ".dimensions") || strings.Contains(field, ".video.duration") || strings.Contains(field, ".audio.duration") || strings.HasSuffix(field, ".fps") {
		var n, p map[string]float64
		if json.Unmarshal(native, &n) != nil || json.Unmarshal(published, &p) != nil {
			return false
		}
		for key, limit := range n {
			if limit != 0 && p[key] == 0 {
				return false
			}
		}
		for key, limit := range p {
			if limit == 0 {
				continue
			}
			v, ok := n[key]
			if !ok || v <= 0 {
				return false
			}
			if strings.HasPrefix(key, "min") && v < limit || strings.HasPrefix(key, "max") && v > limit {
				return false
			}
		}
		return true
	}

	return false
}

func operationalFieldEvidence(field string, value json.RawMessage, candidate operationalCandidate) ([]CoverageSource, string, string, bool) {
	if len(candidate.refs) == 0 {
		return nil, "unknown", "", false
	}
	if candidate.conflict {
		return candidate.refs, "unknown", "Authoritative operational source values disagree; last-confirmed publication is preserved", true
	}
	if !candidate.current {
		status := "unknown"
		for _, ref := range candidate.refs {
			if ref.LastConfirmedHash != "" {
				status = "stale"
			}
			if ref.Status == "preserved" && ref.LastConfirmedHash != "" {
				status = "preserved"
				break
			}
		}
		return candidate.refs, status, "Current agreement is unavailable; missing, stale or failed source evidence cannot verify the field", true
	}
	if compatibleOperationalField(field, value, candidate.value) {
		return candidate.refs, "verified", "Supported native value is covered by typed interpretation of the current source snapshot", true
	}
	if strings.HasSuffix(field, videoDurationSumSuffix) {
		return candidate.refs, "unknown", "Source combined-duration rule is known, but independent native maxima do not establish that rule; whole-model verification remains incomplete", true
	}
	return candidate.refs, "unknown", "Published value does not cover the current native contract; adapter review is required", true
}

const videoDurationSumSuffix = ".video.input_output_duration"

// VideoDurationSumLimit is a documented relation for workflows accepting video
// references. It is source evidence, not a dynamically installed admission rule.
type VideoDurationSumLimit struct {
	MaxSeconds float64 `json:"max_seconds"`
}

func (limit VideoDurationSumLimit) Valid() bool {
	return limit.MaxSeconds > 0 && !math.IsNaN(limit.MaxSeconds) && !math.IsInf(limit.MaxSeconds, 0)
}
func (limit VideoDurationSumLimit) Allows(inputSeconds, outputSeconds float64) bool {
	return limit.Valid() && inputSeconds >= 0 && outputSeconds > 0 && !math.IsNaN(inputSeconds) && !math.IsNaN(outputSeconds) && !math.IsInf(inputSeconds, 0) && !math.IsInf(outputSeconds, 0) && inputSeconds+outputSeconds <= limit.MaxSeconds
}

type videoDurationEnvelope struct {
	InputMaxSeconds  float64 `json:"input_max_seconds"`
	OutputMaxSeconds float64 `json:"output_max_seconds"`
}

func videoDurationEnvelopeFor(model core.MediaModel, field string) (videoDurationEnvelope, bool) {
	if !strings.HasPrefix(field, "workflow.") || !strings.HasSuffix(field, videoDurationSumSuffix) {
		return videoDurationEnvelope{}, false
	}
	modeID := strings.TrimSuffix(strings.TrimPrefix(field, "workflow."), videoDurationSumSuffix)
	for _, mode := range core.ReferenceModesFor(model) {
		if mode.ID != modeID || mode.MaxVideos <= 0 {
			continue
		}
		input := 0.0
		if model.ReferenceMediaLimits != nil {
			rule := model.ReferenceMediaLimits.Video
			input = rule.MaxTotalDuration
			if input <= 0 && rule.MaxDuration > 0 {
				input = rule.MaxDuration * float64(mode.MaxVideos)
			}
		}
		output := model.MaxDuration
		if mode.MaxDurationWithVideo > 0 && (output == 0 || mode.MaxDurationWithVideo < output) {
			output = mode.MaxDurationWithVideo
		}
		return videoDurationEnvelope{InputMaxSeconds: input, OutputMaxSeconds: float64(output)}, true
	}
	return videoDurationEnvelope{}, false
}
func validVideoDurationSum(field string, value json.RawMessage) bool {
	if !strings.HasSuffix(field, videoDurationSumSuffix) {
		return false
	}
	var limit VideoDurationSumLimit
	return json.Unmarshal(value, &limit) == nil && limit.Valid()
}
func hasOperationalField(model, field string, previous State, observations []Observation) bool {
	for _, ob := range observations {
		for _, entry := range ob.Entries {
			if entry.ID == model {
				if _, ok := entry.OperationalFields[field]; ok {
					return true
				}
			}
		}
	}
	for _, ob := range previous.Observations {
		for _, entry := range ob.Entries {
			if entry.ID == model {
				if _, ok := entry.OperationalFields[field]; ok {
					return true
				}
			}
		}
	}
	if metadata := previous.Models[model].Metadata; metadata != nil {
		_, ok := metadata.Operational[field]
		return ok
	}
	return false
}
