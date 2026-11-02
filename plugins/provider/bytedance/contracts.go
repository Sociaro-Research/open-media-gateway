package bytedance

import (
	"context"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func knownModel(id string) bool {
	if len(imageFormats(id)) > 0 {
		return true
	}
	switch id {
	case "seedance-1-0-pro-fast-251015", "seedance-1-0-lite-t2v-250428", "seedance-1-0-lite-i2v-250428", "seedance-1-0-pro-250528", "seedance-1-5-pro-251215", "dreamina-seedance-2-0-mini-260615", "dreamina-seedance-2-0-fast-260128", "dreamina-seedance-2-0-260128", "dreamina-seedance-2-5-260628":
		return true
	default:
		return false
	}
}

func (a *Adapter) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	if r.Model == "dreamina-seedance-2-5-260628" && (r.Options.ReferenceMode == "first_frame" || r.Options.ReferenceMode == "first_last_frames") && r.Options.Ratio != "" && r.Options.Ratio != "adaptive" {
		return core.ErrOptionsInvalid
	}
	if err := core.ValidateReferenceURLs(describeModel(r.Model), r.Options); err != nil {
		return err
	}
	if core.IsManualCall(ctx) {
		return nil
	}
	if knownModel(r.Model) && describeModel(r.Model).MediaType == "video" {
		if _, err := normalizeVideoDuration(describeModel(r.Model), r.Options.Duration); err != nil {
			return err
		}
	}
	return core.ValidateReferenceMode(describeModel(r.Model), r.Options)
}

// normalizeVideoDuration uses Gateway omission/zero for inference, never a native sentinel.
func normalizeVideoDuration(model core.MediaModel, duration int) (int, error) {
	if duration == 0 {
		if model.DurationMayBeInferred {
			return 0, nil
		}
		return model.DefaultDuration, nil
	}
	step := model.DurationStep
	if step <= 0 {
		step = 1
	}
	if duration < model.MinDuration || duration > model.MaxDuration || (duration-model.MinDuration)%step != 0 {
		return 0, core.ErrOptionsInvalid
	}
	return duration, nil
}
