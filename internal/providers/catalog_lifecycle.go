package providers

import (
	"context"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func catalogUnavailable(state catalogsync.State, id string, now time.Time) bool {
	entry := state.Models[id]
	if entry.Lifecycle != nil {
		return entry.Lifecycle.Unavailable(now)
	}
	return entry.Inactive
}

// This guards only new admission. Polling, receipt recovery, historical usage
// and stored provider identities never pass through catalogue admission.
func (p *managedProvider) catalogAdmission(ctx context.Context, id string) error {
	state, err := p.catalogState(ctx)
	if err != nil {
		return err
	}
	if catalogUnavailable(state, id, time.Now()) {
		return core.ErrModelUnavailable
	}
	return nil
}
