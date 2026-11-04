package references

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"time"
)

// Deletion is server-private custody of an already admitted physical operation.
// It is never accepted as request input or used to authorize a new deletion.
type Deletion struct {
	Owner string `json:"-"`
	Item  Image  `json:"-"`
	Lease string `json:"-"`
}
type DeletionRepository interface {
	ClaimDeletions(context.Context, int) ([]Deletion, error)
	FinishDeletion(context.Context, Deletion, bool) error
}

// CleanupDeletions is called only by the trusted worker. A restarted worker can
// finish an admitted operation after the originating family/grant was revoked.
func (l *Library) CleanupDeletions(ctx context.Context) error {
	repo, ok := l.repo.(DeletionRepository)
	if !ok {
		return nil
	}
	var failures []error
	for n := 0; n < 100; n++ {
		// Claim only the operation about to run; a batch must not expire leases
		// while earlier storage calls consume their bounded time budget.
		items, e := repo.ClaimDeletions(ctx, 1)
		if e != nil {
			return errors.Join(append(failures, e)...)
		}
		if len(items) == 0 {
			break
		}
		d := items[0]
		operation, cancel := context.WithTimeout(identity.WithOwnerID(ctx, d.Owner), time.Minute)
		e = l.store.DeleteReference(StorageContext(operation, d.Item), d.Item.StorageKey)
		success := e == nil || errors.Is(e, storage.ErrObjectNotFound)
		settle, done := context.WithTimeout(context.WithoutCancel(operation), 10*time.Second)
		finish := repo.FinishDeletion(settle, d, success)
		done()
		cancel()
		if !success {
			failures = append(failures, e)
		}
		if finish != nil {
			failures = append(failures, finish)
		}
		if !success || finish != nil {
			break
		}
	}

	return errors.Join(failures...)
}
