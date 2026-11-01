package core

import (
	"context"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func TestAdmissionPinsConnectionWithoutAccountingOrKnownQuote(t *testing.T) {
	provider := catalogProvider{models: []MediaModel{{ID: "reviewed", MediaType: "video"}}}
	svc := NewTaskServiceWithProviders(&memoryRepository{}, &memoryQueue{}, map[string]MediaProvider{"catalog": provider}, map[string]string{"catalog": "reviewed"}, "catalog")
	ctx := context.Background()
	calls := 0
	svc.SetAdmissionScopeResolver(func(ctx context.Context, id string) (ProviderConnectionScope, error) {
		calls++
		return ProviderConnectionScope{ID: id, OwnerID: identity.ProviderOwnerID(ctx), Revision: "canonical-host-revision"}, nil
	})
	for _, accounting := range []bool{false, true} {
		svc.accounting = accounting
		task, err := svc.prepare(ctx, CreateMediaInput{Provider: "catalog", Model: "reviewed", Prompt: "Synthetic"}, "")
		if err != nil || task.ProviderIdentity != "canonical-host-revision" || task.Cost != nil || task.AdmissionError != nil {
			t.Fatal("connection identity depended on cost", task, err)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	svc.SetAdmissionScopeResolver(func(ctx context.Context, id string) (ProviderConnectionScope, error) {
		return ProviderConnectionScope{ID: "another-connection", OwnerID: identity.ProviderOwnerID(ctx), Revision: "foreign"}, nil
	})
	task, err := svc.prepare(ctx, CreateMediaInput{Provider: "catalog", Model: "reviewed", Prompt: strings.Repeat("x", 2)}, "")
	if err != nil || task.AdmissionError == nil {
		t.Fatal("foreign host scope admitted", task, err)
	}
}
