package providerresources

import (
	"context"
	"errors"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

type presetDirectory struct {
	core.ProviderDirectory
	p     core.MediaProvider
	calls int
}

func (d *presetDirectory) Provider(ctx context.Context, id string) (core.MediaProvider, error) {
	d.calls++
	return d.p, nil
}

type presetProvider struct {
	core.MediaProvider
	core.ResourceProvider
	list func(context.Context, string, bool, int, int) (core.ResourcePage, error)
}

func (p presetProvider) ListResources(ctx context.Context, k string, b bool, n, s int) (core.ResourcePage, error) {
	return p.list(ctx, k, b, n, s)
}
func presetTestScope() (context.Context, core.ProviderConnectionScope) {
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	scope := core.ProviderConnectionScope{ID: "actual-connection", OwnerID: identity.ProviderOwnerID(ctx), Revision: "canonical-revision"}
	return core.WithProviderConnectionScope(ctx, scope), scope
}
func presetTestBinding(ctx context.Context, scope core.ProviderConnectionScope) Binding {
	return Binding{ID: uuid.NewString(), OwnerID: identity.OwnerID(ctx), ActorID: identity.ActorID(ctx), ProviderID: "connection-catalog-id", ConnectionID: scope.ID, ProviderOwnerID: scope.OwnerID, Revision: scope.Revision, RemoteID: "900719925474099312345", Kind: "voice", Subtype: "preset", Name: "Official fixed voice", Status: Succeeded, IsPreset: true}
}
func TestOfficialPresetListPinsScopeAndRejectsRotation(t *testing.T) {
	ctx, scope := presetTestScope()
	revision := scope.Revision
	calls := 0
	p := presetProvider{list: func(c context.Context, k string, b bool, page, size int) (core.ResourcePage, error) {
		calls++
		actual, ok := core.ProviderConnectionScopeFromContext(c)
		if !ok || actual != scope || core.ProviderRevision(c) != scope.Revision || k != "voice" || !b || page != 2 || size != 1 {
			t.Fatal("catalogue request not pinned")
		}
		return core.ResourcePage{Page: page, PageSize: size, HasMore: true, Resources: []core.ProviderResource{{Kind: "voice", Subtype: "preset", Name: "Official", RemoteID: "large-private-id", IsPreset: true, Status: core.ProviderSucceeded}}}, nil
	}}
	directory := &presetDirectory{p: p}
	current := func(context.Context, string) (core.ProviderConnectionScope, error) {
		s := scope
		s.Revision = revision
		return s, nil
	}
	l := NewLifecycle(nil, current, directory)
	page, s, err := l.ListPresets(ctx, "connection-catalog-id", "voice", 2, 1)
	if err != nil || s != scope || len(page.Resources) != 1 || calls != 1 {
		t.Fatal(page, s, err)
	}
	p.list = func(c context.Context, k string, b bool, page, size int) (core.ResourcePage, error) {
		revision = "rotated"
		return core.ResourcePage{Page: page, PageSize: size}, nil
	}
	directory.p = p
	if _, _, err = l.ListPresets(ctx, "connection-catalog-id", "voice", 1, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("rotation accepted", err)
	}
	before := directory.calls
	for _, tc := range []struct {
		k    string
		p, s int
	}{{"file_resource", 1, 1}, {"voice", 0, 1}, {"voice", 1, 101}} {
		if _, _, err = l.ListPresets(ctx, "connection-catalog-id", tc.k, tc.p, tc.s); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if before != directory.calls {
		t.Fatal("invalid parameters reached provider")
	}
}
func TestOfficialPresetListRejectsCustomDeletedOrUnboundedMetadata(t *testing.T) {
	ctx, scope := presetTestScope()
	base := core.ProviderResource{Kind: "voice", Subtype: "preset", Name: "Official", RemoteID: "remote", IsPreset: true, Status: core.ProviderSucceeded}
	for _, change := range []func(*core.ProviderResource){func(r *core.ProviderResource) { r.IsPreset = false }, func(r *core.ProviderResource) { r.IsDeleted = true }, func(r *core.ProviderResource) { r.Name = "bad\nlabel" }, func(r *core.ProviderResource) { r.Status = core.ProviderRunning }, func(r *core.ProviderResource) { r.Subtype = "custom" }, func(r *core.ProviderResource) { r.BoundVoice = &core.ProviderResource{Kind: "voice"} }} {
		r := base
		change(&r)
		p := presetProvider{list: func(context.Context, string, bool, int, int) (core.ResourcePage, error) {
			return core.ResourcePage{Page: 1, PageSize: 1, Resources: []core.ProviderResource{r}}, nil
		}}
		l := NewLifecycle(nil, func(context.Context, string) (core.ProviderConnectionScope, error) { return scope, nil }, &presetDirectory{p: p})
		if _, _, err := l.ListPresets(ctx, "actual", "voice", 1, 1); !errors.Is(err, ErrInvalid) {
			t.Fatal("untrusted catalogue resource accepted", r, err)
		}
	}
}
func TestOfficialPresetBindingAndGenericCapabilityGate(t *testing.T) {
	ctx, scope := presetTestScope()
	base := presetTestBinding(ctx, scope)
	if err := ValidatePresetBinding(ctx, base); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Binding){func(b *Binding) { b.Revision = "foreign" }, func(b *Binding) { b.ConnectionID = "duplicate-key-other-connection" }, func(b *Binding) { b.OwnerID = uuid.NewString() }, func(b *Binding) { b.ActorID = "another" }, func(b *Binding) { b.IsPreset = false }, func(b *Binding) { b.Name = "renamed\nlabel" }, func(b *Binding) { b.Kind = "element"; b.Subtype = "video_character" }, func(b *Binding) { b.Sources = []core.ResourceSourceEvidence{{MediaType: "video"}} }} {
		b := base
		change(&b)
		if !errors.Is(ValidatePresetBinding(ctx, b), ErrInvalid) {
			t.Fatal("invalid binding accepted", b)
		}
	}
	o := core.MediaOptions{ResourceReferences: []core.ResourceReference{{BindingID: base.ID, ReferenceID: "voice_1"}}, ResourceSnapshots: []core.ResourceSnapshot{{BindingID: base.ID, ReferenceID: "voice_1", Kind: "voice", Subtype: "preset", RemoteID: base.RemoteID, ConnectionIdentity: scope.Revision, IsPreset: true}}}
	m := core.MediaModel{ResourceInputSchemas: []core.ResourceInputSchema{{Kind: "voice", MaxCount: 1}}}
	if !errors.Is(core.ValidateResourceReferences(m, o), core.ErrOptionsInvalid) {
		t.Fatal("preset bypassed capability")
	}
	m.ResourceInputSchemas[0].SupportsPresets = true
	if err := core.ValidateResourceReferences(m, o); err != nil {
		t.Fatal(err)
	}
	m.ResourceInputSchemas[0].SupportsPresets = false
	o.ResourceSnapshots[0].IsPreset = false
	o.ResourceSnapshots[0].Subtype = "custom"
	if err := core.ValidateResourceReferences(m, o); err != nil {
		t.Fatal("custom resources changed", err)
	}
}
