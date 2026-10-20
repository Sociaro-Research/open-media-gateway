package providers

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/config"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/references"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

type referenceSettingsStub struct {
	value system.Settings
	err   error
}

func (s *referenceSettingsStub) Settings(context.Context) (system.Settings, error) {
	return s.value, s.err
}

type referenceResolverStub struct{ calls int }

func (r *referenceResolverStub) Resolve(_ context.Context, refs []string) ([]string, error) {
	r.calls++
	return []string{"data:image/png;base64,aW1hZ2U="}, nil
}

type referenceAdapterStub struct {
	core.MediaProvider
	inline   bool
	calls    int
	received core.MediaOptions
}

func (a *referenceAdapterStub) PreferInlineImages() bool { return a.inline }
func (a *referenceAdapterStub) Generate(_ context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	a.calls++
	a.received = r.Options
	return core.ProviderTask{ID: "test"}, nil
}

func TestReferenceTransferPolicyAtDispatch(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "personal", true: "shared"}[shared], func(t *testing.T) {
			ctx := identity.WithOwnerID(context.Background(), "reference-owner")
			if shared {
				ctx = identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
			}
			repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
			cipher, _ := providerconfig.NewCipher("reference-test-secret")
			resolver := &referenceResolverStub{}
			m := NewManager(repo, cipher, config.Config{}, resolver)
			settings := &referenceSettingsStub{value: system.Defaults()}
			m.SetSystemSettings(settings)
			adapter := &referenceAdapterStub{inline: true}
			m.cacheAdapter(ctx, "mock", Credentials{}, adapter)
			p := m.ManagedProviders()["mock"]
			request := core.MediaRequest{Options: core.MediaOptions{ImageURLs: []string{"https://gateway.test/references/image"}}}
			for _, enabled := range []bool{true, false, true} {
				settings.value.AllowInlineReferences = enabled
				before := resolver.calls
				if _, err := p.Generate(ctx, request); err != nil {
					t.Fatal(err)
				}
				if enabled {
					if resolver.calls != before+1 || adapter.received.ImageURLs[0] != "data:image/png;base64,aW1hZ2U=" {
						t.Fatal("inline reference missing")
					}
				} else if resolver.calls != before || !reflect.DeepEqual(adapter.received, request.Options) {
					t.Fatal("URL-only mode changed or loaded references")
				}
				if request.Options.ImageURLs[0] != "https://gateway.test/references/image" {
					t.Fatal("stored request was mutated")
				}
			}
			adapter.inline = false
			before := resolver.calls
			if _, err := p.Generate(ctx, request); err != nil || resolver.calls != before {
				t.Fatalf("provider without inline support: %v", err)
			}
			settings.err = errors.New("database unavailable")
			before = adapter.calls
			if _, err := p.Generate(ctx, request); err == nil || adapter.calls != before {
				t.Fatal("sent request without reading policy")
			}
		})
	}
}

func TestURLOnlyPolicyRejectsInlineReferencesAtAdmissionAndDispatch(t *testing.T) {
	const data = "data:image/png;base64,aW1hZ2U="
	for _, options := range []core.MediaOptions{
		{ImageURLs: []string{data}}, {VideoURLs: []string{data}}, {AudioURLs: []string{data}}, {VideoURL: data}, {AudioURL: data}, {VideoURL: data, VideoURLs: []string{"https://example.test/video"}}, {AudioURL: data, AudioURLs: []string{"https://example.test/audio"}}, {ReferenceVoices: map[string]string{"https://example.test/image": data}},
	} {
		repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
		cipher, _ := providerconfig.NewCipher("reference-test-secret")
		m := NewManager(repo, cipher, config.Config{})
		settings := &referenceSettingsStub{value: system.Defaults()}
		m.SetSystemSettings(settings)
		adapter := &referenceAdapterStub{inline: true}
		m.cacheAdapter(context.Background(), "mock", Credentials{}, adapter)
		p := m.ManagedProviders()["mock"]
		request := core.MediaRequest{Options: options}
		if err := p.(core.RequestValidator).ValidateRequest(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		settings.value.AllowInlineReferences = false
		if err := p.(core.RequestValidator).ValidateRequest(context.Background(), request); !errors.Is(err, core.ErrOptionsInvalid) {
			t.Fatalf("admission accepted inline data: %v", err)
		}
		if _, err := p.Generate(context.Background(), request); !errors.Is(err, core.ErrOptionsInvalid) || adapter.calls != 0 {
			t.Fatalf("dispatch accepted queued inline data: %v", err)
		}
	}
}

// A nil library would panic if a provider-managed reference were read as a file.
func TestRegisteredAssetsThroughManagedProvider(t *testing.T) {
	for _, shared := range []bool{false, true} {
		ctx := identity.WithOwnerID(context.Background(), "asset-owner")
		if shared {
			ctx = identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
		}
		cipher, _ := providerconfig.NewCipher("asset-test-secret")
		m := NewManager(&memoryProviderRepository{records: map[string]providerconfig.Record{}}, cipher, config.Config{}, references.NewResolver(nil, "test-signing-secret"))
		settings := &referenceSettingsStub{value: system.Defaults()}
		m.SetSystemSettings(settings)
		adapter := &referenceAdapterStub{inline: true}
		m.cacheAdapter(ctx, "mock", Credentials{}, adapter)
		p := m.ManagedProviders()["mock"]
		options := core.MediaOptions{ImageURLs: []string{"https://example.test/image.png", "asset://asset-image"}, VideoURLs: []string{"asset://asset-video"}, AudioURLs: []string{"asset://asset-audio"}, VideoURL: "asset://asset-video", AudioURL: "asset://asset-audio"}
		for _, enabled := range []bool{true, false} {
			settings.value.AllowInlineReferences = enabled
			request := core.MediaRequest{Options: options}
			if err := p.(core.RequestValidator).ValidateRequest(ctx, request); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Generate(ctx, request); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(adapter.received, options) {
				t.Fatalf("references altered: %+v", adapter.received)
			}
		}
		for _, bad := range []string{"asset://", "asset://asset-one/path", "asset://user@asset-one", "asset://asset-one?query=1"} {
			_, err := m.referencePolicy(ctx, core.MediaOptions{ImageURLs: []string{bad}})
			if !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatalf("policy accepted %q: %v", bad, err)
			}
		}
	}
}
