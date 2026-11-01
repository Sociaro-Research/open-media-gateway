package kling

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func legacyScopeFixture(owner, providerOwner, connection, revision string) context.Context {
	ctx := identity.WithOwnerID(context.Background(), owner)
	return core.WithProviderConnectionScope(ctx, core.ProviderConnectionScope{ID: connection, OwnerID: providerOwner, Revision: revision})
}
func TestLegacyScopeHandleIsolatesOwnersConnectionsAndRevisions(t *testing.T) {
	good := legacyScopeFixture("task-owner", "provider-owner", "kling--connection-one", "revision-one")
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("handle checks must remain local")
		return nil, nil
	})
	handle, err := a.signLegacy(good, "fixture-operation", "9007199254740993123")
	if err != nil {
		t.Fatal(err)
	}
	restarted := adapter(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("handle checks must remain local")
		return nil, nil
	})
	parsed, err := restarted.parseLegacy(good, handle)
	if err != nil || parsed.ID != "9007199254740993123" || parsed.Operation != "fixture-operation" {
		t.Fatal(parsed, err)
	}
	for name, ctx := range map[string]context.Context{
		"duplicate key other connection": legacyScopeFixture("task-owner", "provider-owner", "kling--connection-two", "revision-one"),
		"other task owner":               legacyScopeFixture("other-owner", "provider-owner", "kling--connection-one", "revision-one"),
		"other provider owner":           legacyScopeFixture("task-owner", "other-provider-owner", "kling--connection-one", "revision-one"),
		"other revision":                 legacyScopeFixture("task-owner", "provider-owner", "kling--connection-one", "revision-two"),
		"blank revision":                 legacyScopeFixture("task-owner", "provider-owner", "kling--connection-one", ""),
		"missing scope":                  identity.WithOwnerID(context.Background(), "task-owner"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := restarted.parseLegacy(ctx, handle); err == nil {
				t.Fatal("foreign or incomplete scope accepted handle")
			}
		})
	}
	if _, err := a.parseLegacy(good, handle+"x"); err == nil {
		t.Fatal("tampered handle accepted")
	}
}
func TestLegacyScopeIncompleteScopeCannotMintOrSubmit(t *testing.T) {
	calls := 0
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"code":0,"data":{"task_id":"synthetic","task_status":"submitted"}}`), nil
	})
	for _, ctx := range []context.Context{
		context.Background(),
		legacyScopeFixture("task-owner", "provider-owner", "kling--connection", ""),
		legacyScopeFixture("task-owner", "", "kling--connection", "revision"),
		legacyScopeFixture("task-owner", "provider-owner", "", "revision"),
	} {
		if _, ok := core.ProviderConnectionScopeFromContext(ctx); ok {
			t.Fatal("incomplete scope reported authorized")
		}
		if _, err := a.signLegacy(ctx, "fixture-operation", "9007199254740993123"); !errors.Is(err, core.ErrOptionsInvalid) {
			t.Fatal("incomplete scope minted handle", err)
		}
		if _, err := a.submitLegacy(ctx, "fixture-operation", "/v1/images/generations", map[string]string{"fixture": "synthetic"}); !errors.Is(err, core.ErrOptionsInvalid) {
			t.Fatal("incomplete scope submitted", err)
		}
	}
	if calls != 0 {
		t.Fatal("scope failure reached provider transport", calls)
	}
	ctx := legacyScopeFixture("task-owner", "provider-owner", "kling--connection", "revision")
	task, err := a.submitLegacy(ctx, "fixture-operation", "/v1/images/generations", map[string]string{"fixture": "synthetic"})
	if err != nil || calls != 1 {
		t.Fatal(task, err, calls)
	}
	if _, err := a.parseLegacy(ctx, task.ID); err != nil {
		t.Fatal("submitted task did not retain its mint scope", err)
	}
}
