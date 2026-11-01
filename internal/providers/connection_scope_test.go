package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/config"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	registry "github.com/Sociaro-Research/open-media-gateway/plugins/provider"
)

type connectionScopeCall struct {
	method, key string
	scope       core.ProviderConnectionScope
	owner       string
}
type connectionScopeCalls struct {
	mu    sync.Mutex
	calls []connectionScopeCall
}

func (c *connectionScopeCalls) add(method, key string, ctx context.Context) (core.ProviderConnectionScope, error) {
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if !ok {
		return scope, core.ErrOptionsInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, connectionScopeCall{method, key, scope, identity.OwnerID(ctx)})
	return scope, nil
}
func (c *connectionScopeCalls) snapshot() []connectionScopeCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]connectionScopeCall(nil), c.calls...)
}

type connectionScopeHandle struct {
	Scope core.ProviderConnectionScope
	Owner string
}
type connectionScopeProbe struct {
	key   string
	calls *connectionScopeCalls
}

func (connectionScopeProbe) Name() string { return "scopeprobe" }
func (connectionScopeProbe) ListModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{{ID: "scoped-image", MediaType: "image"}}, nil
}
func (p connectionScopeProbe) ValidateRequest(ctx context.Context, _ core.MediaRequest) error {
	_, err := p.calls.add("validate", p.key, ctx)
	return err
}
func (p connectionScopeProbe) Quote(ctx context.Context, _ core.MediaModel, _ core.MediaRequest) (core.TaskCost, string, error) {
	_, err := p.calls.add("quote", p.key, ctx)
	return core.TaskCost{ExpectedUSD: 123, Status: "estimated", QuotedAt: time.Now()}, "", err
}
func (p connectionScopeProbe) Generate(ctx context.Context, _ core.MediaRequest) (core.ProviderTask, error) {
	scope, err := p.calls.add("submit", p.key, ctx)
	if err != nil {
		return core.ProviderTask{}, err
	}
	raw, _ := json.Marshal(connectionScopeHandle{scope, identity.OwnerID(ctx)})
	return core.ProviderTask{ID: base64.RawURLEncoding.EncodeToString(raw)}, nil
}
func (p connectionScopeProbe) check(ctx context.Context, id string) error {
	raw, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return core.ErrOptionsInvalid
	}
	var handle connectionScopeHandle
	if json.Unmarshal(raw, &handle) != nil {
		return core.ErrOptionsInvalid
	}
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if !ok || handle.Scope != scope || handle.Owner != identity.OwnerID(ctx) {
		return core.ErrOptionsInvalid
	}
	return nil
}
func (p connectionScopeProbe) GetTask(ctx context.Context, id string) (core.ProviderResult, error) {
	if err := p.check(ctx, id); err != nil {
		return core.ProviderResult{}, err
	}
	_, err := p.calls.add("poll", p.key, ctx)
	return core.ProviderResult{Status: core.ProviderSucceeded}, err
}
func (p connectionScopeProbe) OpenArtifact(ctx context.Context, id string) (core.ArtifactSource, error) {
	if err := p.check(ctx, id); err != nil {
		return core.ArtifactSource{}, err
	}
	if _, err := p.calls.add("open", p.key, ctx); err != nil {
		return core.ArtifactSource{}, err
	}
	return core.ArtifactSource{Reader: io.NopCloser(strings.NewReader("synthetic")), ContentType: "text/plain", SizeBytes: 9}, nil
}
func connectionScopeFixture(t *testing.T, revisions bool) (*Manager, *connectionScopeCalls, context.Context) {
	t.Helper()
	memory := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
	var repo providerconfig.Repository = memory
	if revisions {
		repo = &revisionRepository{memoryProviderRepository: memory, revisions: map[string][]byte{}}
	}
	cipher, err := providerconfig.NewCipher("synthetic-scope-fixture")
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(repo, cipher, config.Config{})
	calls := &connectionScopeCalls{}
	manager.registry = registry.MustRegistry(providerkit.Definition{ID: "scopeprobe", Name: "Scope probe", Fields: []providerkit.Field{{Key: "api_key", Secret: true, Required: true}}, Factory: func(c Credentials, _ providerkit.Settings, _ *http.Client) (core.MediaProvider, error) {
		return connectionScopeProbe{key: c.APIKey, calls: calls}, nil
	}})
	return manager, calls, identity.WithOwnerID(context.Background(), "scope-owner-one")
}
func saveScopeConnection(t *testing.T, m *Manager, ctx context.Context, id, key string) {
	t.Helper()
	raw, err := m.encryptCredentials(Credentials{APIKey: key, Connection: &providerkit.Connection{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.repo.Upsert(ctx, providerconfig.Record{ProviderID: id, Enabled: true, CredentialsCiphertext: raw}); err != nil {
		t.Fatal(err)
	}
}
func scopeProvider(t *testing.T, m *Manager, ctx context.Context, id string) core.MediaProvider {
	t.Helper()
	p, err := m.Provider(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConnectionScopeDuplicateKeysRemainIsolated(t *testing.T) {
	m, calls, ctx := connectionScopeFixture(t, true)
	first, second := "scopeprobe--11111111-1111-4111-8111-111111111111", "scopeprobe--22222222-2222-4222-8222-222222222222"
	saveScopeConnection(t, m, ctx, first, "same-synthetic-key")
	saveScopeConnection(t, m, ctx, second, "same-synthetic-key")
	p1, p2 := scopeProvider(t, m, ctx, first), scopeProvider(t, m, ctx, second)
	r := core.MediaRequest{Model: "scoped-image"}
	_, revision1, err := p1.(core.CostQuoter).Quote(ctx, core.MediaModel{}, r)
	if err != nil {
		t.Fatal(err)
	}
	_, revision2, err := p2.(core.CostQuoter).Quote(ctx, core.MediaModel{}, r)
	if err != nil || revision1 == revision2 {
		t.Fatal("duplicate-key connection revisions collapsed", err)
	}
	task, err := p1.Generate(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	before := len(calls.snapshot())
	if _, err := p2.GetTask(ctx, task.ID); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("foreign connection accepted handle", err)
	}
	if _, err := p2.(core.ArtifactOpener).OpenArtifact(ctx, task.ID); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("foreign connection opened handle", err)
	}
	foreign := identity.WithOwnerID(context.Background(), "scope-owner-two")
	saveScopeConnection(t, m, foreign, first, "same-synthetic-key")
	if _, err := scopeProvider(t, m, foreign, first).GetTask(foreign, task.ID); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("foreign owner accepted handle", err)
	}
	if len(calls.snapshot()) != before {
		t.Fatal("scope rejection reached simulated provider operation")
	}
	if _, err := p1.GetTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
}
func TestConnectionScopePinnedCallsRemainConsistentAfterRotationAndRecovery(t *testing.T) {
	m, calls, ctx := connectionScopeFixture(t, true)
	id := "scopeprobe--11111111-1111-4111-8111-111111111111"
	saveScopeConnection(t, m, ctx, id, "old-synthetic-key")
	p := scopeProvider(t, m, ctx, id)
	request := core.MediaRequest{Model: "scoped-image"}
	_, revision, err := p.(core.CostQuoter).Quote(ctx, core.MediaModel{}, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.(core.RequestValidator).ValidateRequest(ctx, request); err != nil {
		t.Fatal(err)
	}
	direct, err := p.Generate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetTask(core.WithProviderRevision(ctx, revision), direct.ID); err != nil {
		t.Fatal("direct and quoted scopes differ", err)
	}
	saveScopeConnection(t, m, ctx, id, "new-synthetic-key")
	// A fresh manager represents process restart; encrypted revision history is retained.
	restarted := NewManager(m.repo, m.cipher, config.Config{})
	restarted.registry = m.registry
	recovered := scopeProvider(t, restarted, ctx, id)
	pinned := core.WithProviderRevision(ctx, revision)
	if err := recovered.(core.RequestValidator).ValidateRequest(pinned, request); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"context only", "request only", "queued both"} {
		t.Run(mode, func(t *testing.T) {
			callCtx := ctx
			r := request
			if mode != "request only" {
				callCtx = pinned
			}
			if mode != "context only" {
				r.ProviderIdentity = revision
			}
			task, err := recovered.Generate(callCtx, r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := recovered.GetTask(pinned, task.ID); err != nil {
				t.Fatal(err)
			}
			source, err := recovered.(core.ArtifactOpener).OpenArtifact(pinned, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			source.Reader.Close()
		})
	}
	for _, call := range calls.snapshot() {
		if call.scope.ID != id || call.scope.OwnerID != identity.ProviderOwnerID(ctx) || call.scope.Revision != revision || call.owner != identity.OwnerID(ctx) || call.key != "old-synthetic-key" {
			t.Fatal("mint/validate/submit/poll/open did not retain accepted scope", call.method)
		}
	}
	before := len(calls.snapshot())
	bad := request
	bad.ProviderIdentity = "conflicting-revision"
	if _, err := recovered.Generate(pinned, bad); !errors.Is(err, core.ErrProviderChanged) {
		t.Fatal("conflicting request/context revisions accepted", err)
	}
	if len(calls.snapshot()) != before {
		t.Fatal("revision conflict reached simulated provider operation")
	}
}
func TestConnectionScopeMissingPinnedRevisionRejectsBeforeProviderOperation(t *testing.T) {
	m, calls, ctx := connectionScopeFixture(t, false)
	id := "scopeprobe--11111111-1111-4111-8111-111111111111"
	saveScopeConnection(t, m, ctx, id, "old-synthetic-key")
	p := scopeProvider(t, m, ctx, id)
	_, revision, err := p.(core.CostQuoter).Quote(ctx, core.MediaModel{}, core.MediaRequest{Model: "scoped-image"})
	if err != nil {
		t.Fatal(err)
	}
	saveScopeConnection(t, m, ctx, id, "new-synthetic-key")
	before := len(calls.snapshot())
	pinned := core.WithProviderRevision(ctx, revision)
	if _, err := p.Generate(pinned, core.MediaRequest{Model: "scoped-image"}); !errors.Is(err, core.ErrProviderChanged) {
		t.Fatal("context-only pinned rotation silently submitted current credentials", err)
	}
	if err := p.(core.RequestValidator).ValidateRequest(pinned, core.MediaRequest{Model: "scoped-image"}); !errors.Is(err, core.ErrProviderChanged) {
		t.Fatal("validation mislabeled current credentials with old revision", err)
	}
	if _, err := p.GetTask(pinned, "opaque"); !errors.Is(err, core.ErrProviderChanged) {
		t.Fatal(err)
	}
	if _, err := p.(core.ArtifactOpener).OpenArtifact(pinned, "opaque"); !errors.Is(err, core.ErrProviderChanged) {
		t.Fatal(err)
	}
	if len(calls.snapshot()) != before {
		t.Fatal("missing historical revision reached simulated provider operation")
	}
}
