package providers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/config"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

type lookupFailureRepository struct {
	*memoryProviderRepository
	err error
}

func (r *lookupFailureRepository) Get(ctx context.Context, id string) (providerconfig.Record, error) {
	if r.err != nil {
		return providerconfig.Record{}, r.err
	}
	return r.memoryProviderRepository.Get(ctx, id)
}

func TestProviderLookupDistinguishesMissingConnectionFromReadFailure(t *testing.T) {
	readErr := errors.New("temporary database read failure")
	for _, tc := range []struct {
		name, id string
		readErr  error
		wantErr  error
		missing  bool
	}{
		{name: "existing", id: "mock"},
		{name: "unsupported", id: "unsupported", wantErr: ErrUnsupportedProvider, missing: true},
		{name: "missing connection", id: "bytedance--missing", wantErr: providerconfig.ErrNotFound, missing: true},
		{name: "read failure", id: "mock", readErr: fmt.Errorf("get settings: %w", readErr), wantErr: readErr},
		{name: "cancelled", id: "mock", readErr: context.Canceled, wantErr: context.Canceled},
		{name: "deadline", id: "mock", readErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &lookupFailureRepository{memoryProviderRepository: &memoryProviderRepository{records: map[string]providerconfig.Record{"mock": {ProviderID: "mock", Enabled: true}}}, err: tc.readErr}
			manager := NewManager(repo, nil, config.Config{})
			provider, err := manager.Provider(context.Background(), tc.id)
			if !errors.Is(err, tc.wantErr) || errors.Is(err, core.ErrProviderUnavailable) != tc.missing {
				t.Fatalf("lookup error=%v, want cause=%v missing=%v", err, tc.wantErr, tc.missing)
			}
			if (provider != nil) != (tc.wantErr == nil) {
				t.Fatal("lookup returned an adapter after failure or lost a healthy adapter")
			}
		})
	}
}
