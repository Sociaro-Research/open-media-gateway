package providers

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

type scheduleFailureRepository struct {
	catalogsync.Repository
	attempts []string
	failure  error
	shared   error
	cancel   context.CancelFunc
}

func (r *scheduleFailureRepository) EnsureSchedule(_ context.Context, s catalogsync.Schedule) error {
	r.attempts = append(r.attempts, s.Provider)
	if s.Provider == "alibaba" {
		if r.cancel != nil {
			r.cancel()
		}
		return r.failure
	}
	return nil
}
func (r *scheduleFailureRepository) Settings(context.Context) (catalogsync.Settings, error) {
	return catalogsync.Settings{}, r.shared
}

type recordFailureRepository struct {
	providerconfig.Repository
	failure error
	shared  error
	lists   int
}

func (r *recordFailureRepository) Get(ctx context.Context, id string) (providerconfig.Record, error) {
	if id == "alibaba" {
		return providerconfig.Record{}, r.failure
	}
	return r.Repository.Get(ctx, id)
}
func (r *recordFailureRepository) List(ctx context.Context) ([]providerconfig.Record, error) {
	r.lists++
	if r.lists > 1 && r.shared != nil {
		return nil, r.shared
	}
	return r.Repository.List(ctx)
}
func TestEnsureCatalogSchedulesIsolatesProviderFailures(t *testing.T) {
	for _, stage := range []string{"record", "schedule"} {
		t.Run(stage, func(t *testing.T) {
			manager, records, ctx := fixtureConnection(t)
			failure := errors.New("one provider failed")
			catalog := &scheduleFailureRepository{}
			if stage == "record" {
				manager.repo = &recordFailureRepository{Repository: records, failure: failure}
			} else {
				catalog.failure = failure
			}
			manager.SetCatalog(catalog)
			if err := manager.EnsureCatalogSchedules(ctx); !errors.Is(err, failure) {
				t.Fatalf("lost bootstrap error: %v", err)
			}
			if !slices.Contains(catalog.attempts, "bytedance") || !slices.Contains(catalog.attempts, "openai") {
				t.Fatalf("blocked later providers: %v", catalog.attempts)
			}
		})
	}
}
func TestEnsureCatalogSchedulesStopsSharedFailureAndCancellation(t *testing.T) {
	for _, stage := range []string{"record-store", "catalog-store", "cancel"} {
		t.Run(stage, func(t *testing.T) {
			manager, records, base := fixtureConnection(t)
			ctx, cancel := context.WithCancel(base)
			defer cancel()
			local := errors.New("one provider failed")
			shared := errors.New("store unavailable")
			catalog := &scheduleFailureRepository{failure: local}
			switch stage {
			case "record-store":
				manager.repo = &recordFailureRepository{Repository: records, failure: local, shared: shared}
			case "catalog-store":
				catalog.shared = shared
			case "cancel":
				catalog.cancel = cancel
			}
			manager.SetCatalog(catalog)
			err := manager.EnsureCatalogSchedules(ctx)
			if !errors.Is(err, local) {
				t.Fatal("lost local error", err)
			}
			if stage == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("lost cancellation", err)
				}
			} else if !errors.Is(err, shared) {
				t.Fatal("lost shared error", err)
			}
			if slices.Contains(catalog.attempts, "bytedance") {
				t.Fatalf("continued shared failure: %v", catalog.attempts)
			}
		})
	}
}
