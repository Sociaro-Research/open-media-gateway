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
	if err := core.ValidateReferenceURLs(describeModel(r.Model), r.Options); err != nil {
		return err
	}
	if core.IsManualCall(ctx) {
		return nil
	}
	return core.ValidateReferenceMode(describeModel(r.Model), r.Options)
}
