package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/config"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func TestAcceptedReceiptConnectionValidationRetainsPinnedScopeWithoutProviderCalls(t *testing.T) {
	m, calls, ctx := connectionScopeFixture(t, true)
	first := "scopeprobe--11111111-1111-4111-8111-111111111111"
	second := "scopeprobe--22222222-2222-4222-8222-222222222222"
	saveScopeConnection(t, m, ctx, first, "same-synthetic-key")
	saveScopeConnection(t, m, ctx, second, "same-synthetic-key")
	p := scopeProvider(t, m, ctx, first)
	_, revision, err := p.(core.CostQuoter).Quote(ctx, core.MediaModel{}, core.MediaRequest{Model: "scoped-image"})
	if err != nil {
		t.Fatal(err)
	}
	task := core.Task{Provider: first, ProviderIdentity: revision, OwnerID: identity.OwnerID(ctx), ProviderOwnerID: identity.ProviderOwnerID(ctx), StorageOwnerID: identity.StorageOwnerID(ctx)}
	pinned := core.WithProviderRevision(ctx, revision)
	before := len(calls.snapshot())
	if err = p.(core.AcceptedReceiptValidator).ValidateAcceptedReceipt(pinned, task); err != nil {
		t.Fatal(err)
	}
	saveScopeConnection(t, m, ctx, first, "rotated-synthetic-key")
	restarted := NewManager(m.repo, m.cipher, config.Config{})
	restarted.registry = m.registry
	recovered := scopeProvider(t, restarted, ctx, first)
	if err = recovered.(core.AcceptedReceiptValidator).ValidateAcceptedReceipt(pinned, task); err != nil {
		t.Fatal("accepted historical credentials lost", err)
	}
	for _, variant := range []string{"other connection", "other owner", "other provider owner", "changed revision", "missing revision", "changed task scope"} {
		t.Run(variant, func(t *testing.T) {
			target := recovered
			checkCtx := pinned
			checkTask := task
			switch variant {
			case "other connection":
				target = scopeProvider(t, restarted, ctx, second)
			case "other owner":
				checkCtx = identity.WithOwnerID(pinned, "other-data-owner")
			case "other provider owner":
				checkCtx = identity.WithResourceOwners(pinned, "other-provider-owner", task.StorageOwnerID)
			case "changed revision":
				checkCtx = core.WithProviderRevision(pinned, "other-revision")
			case "missing revision":
				checkCtx = context.WithValue(ctx, struct{}{}, true)
			case "changed task scope":
				checkTask.ProviderOwnerID = "other-provider-owner"
			}
			if err := target.(core.AcceptedReceiptValidator).ValidateAcceptedReceipt(checkCtx, checkTask); !errors.Is(err, core.ErrProviderChanged) {
				t.Fatal("foreign receipt scope accepted", err)
			}
		})
	}
	if len(calls.snapshot()) != before {
		t.Fatal("receipt validation reached provider operation")
	}
}
