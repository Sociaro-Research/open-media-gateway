package core

import "math"

// InputMediaEvidence is populated only from inspected authorized source bytes.
type InputMediaEvidence struct {
	Type     string  `json:"type"`
	Duration float64 `json:"duration,omitempty"`
}

// InputVideoDuration requires evidence for every input; an absent duration is
// unknown, never zero. External URLs are not fetched as a side effect of quoting.
func (o MediaOptions) InputVideoDuration() (float64, bool) {
	total := 0.0
	for _, raw := range o.Videos() {
		evidence, ok := o.ReferenceMediaEvidence[raw]
		if !ok || evidence.Type != "video" || evidence.Duration <= 0 || math.IsNaN(evidence.Duration) || math.IsInf(evidence.Duration, 0) {
			return 0, false
		}
		total += evidence.Duration
	}
	return total, true
}
