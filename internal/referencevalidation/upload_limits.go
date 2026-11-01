package referencevalidation

import "github.com/Sociaro-Research/open-media-gateway/internal/core"

const DefaultVideoMaxBytes int64 = 200 << 20
const HardVideoMaxBytes int64 = 300000000

// ReviewedVideoMaxBytes returns the inclusive video byte ceiling. Only explicit
// reviewed native metadata can extend the ordinary limit; manual model metadata
// never grants that authority. Native limits still apply independently.
func ReviewedVideoMaxBytes(model core.MediaModel) int64 {
	limit := DefaultVideoMaxBytes
	if !model.Manual && model.ReferenceMediaLimits != nil {
		rule := model.ReferenceMediaLimits.Video
		if rule.MaxBytes > limit {
			limit = rule.MaxBytes
			if !rule.MaxBytesInclusive {
				limit--
			}
		}
	}
	if limit > HardVideoMaxBytes {
		limit = HardVideoMaxBytes
	}
	return limit
}
