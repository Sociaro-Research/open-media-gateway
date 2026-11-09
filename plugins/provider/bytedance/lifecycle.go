package bytedance

import (
	"context"
	"net/url"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
)

// WithClock supplies an instance-local clock for deterministic lifecycle/cache
// evaluation. A nil clock keeps the production time.Now default.
func WithClock(clock func() time.Time) Option {
	return func(a *Adapter) { a.clock = clock }
}

// The optional clock is local to an adapter, never a process-wide test override.
func (a *Adapter) now() time.Time {
	if a.clock != nil {
		return a.clock()
	}
	return time.Now()
}

// Keep raw cached descriptors and serializers for already accepted tasks.
// Availability is evaluated on every read, including a cache hit across the cutoff.
func activeModels(models []core.MediaModel, now time.Time) []core.MediaModel {
	result := make([]core.MediaModel, 0, len(models))
	for _, model := range models {
		if !modelcatalog.IsInactive("bytedance", model.ID, now) {
			result = append(result, model)
		}
	}
	return result
}

// HistoricalUsageModel retains the original public tariff for accepted legacy
// Seedance tasks. This is not an executable catalogue or a fresh quotation.
func (a *Adapter) HistoricalUsageModel(ctx context.Context, id string) (core.MediaModel, bool, error) {
	if err := ctx.Err(); err != nil {
		return core.MediaModel{}, false, err
	}
	if id != "seedance-1-5-pro-251215" && id != "seedance-1-0-lite-t2v-250428" && id != "seedance-1-0-lite-i2v-250428" {
		return core.MediaModel{}, false, nil
	}
	model := describeModel(id)
	endpoint, err := url.Parse(a.baseURL)
	model.Pricing.Known = err == nil && endpoint.Hostname() == "ark.ap-southeast.bytepluses.com"
	return model, true, nil
}
