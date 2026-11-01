package providerresources

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

type resourceBindingMemory struct {
	Repository
	bindings map[string]Binding
}

func (m *resourceBindingMemory) GetBinding(_ context.Context, id string) (Binding, error) {
	b, ok := m.bindings[id]
	if !ok {
		return b, ErrNotFound
	}
	return b, nil
}
func resourceResolverFixture() (context.Context, *resourceBindingMemory, Binding, ScopeResolver) {
	owner, powner := uuid.NewString(), uuid.NewString()
	ctx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), owner), powner, powner)
	duration := 4.0
	b := Binding{ID: uuid.NewString(), OwnerID: owner, ProviderID: "actual-connection", ConnectionID: "actual-connection", ProviderOwnerID: powner, Revision: "immutable-canonical-revision", RemoteID: "9007199254740993123456789", Kind: "element", Subtype: "video_character", Status: Succeeded, IsPreset: true, Sources: []core.ResourceSourceEvidence{{ReferenceID: uuid.NewString(), MediaType: "video", SHA256: strings.Repeat("a", 64), Duration: &duration}}}
	repo := &resourceBindingMemory{bindings: map[string]Binding{b.ID: b}}
	scope := func(context.Context, string) (core.ProviderConnectionScope, error) {
		return core.ProviderConnectionScope{ID: b.ConnectionID, OwnerID: b.ProviderOwnerID, Revision: b.Revision}, nil
	}
	return ctx, repo, b, scope
}
func TestProviderResourceResolverRequiresExactCurrentScopeAndCopiesEvidence(t *testing.T) {
	ctx, repo, b, scope := resourceResolverFixture()
	refs := []core.ResourceReference{{BindingID: b.ID, ReferenceID: "Character1"}}
	got, err := New(repo, scope).ResolveResourceReferences(ctx, b.ProviderID, refs)
	if err != nil || len(got) != 1 || got[0].RemoteID != b.RemoteID || got[0].ConnectionIdentity != b.Revision || !got[0].IsPreset {
		t.Fatal(got, err)
	}
	*got[0].Sources[0].Duration = 99
	got[0].Sources[0].SHA256 = "changed"
	if *repo.bindings[b.ID].Sources[0].Duration != 4 || repo.bindings[b.ID].Sources[0].SHA256 != strings.Repeat("a", 64) {
		t.Fatal("resolver shared persisted evidence")
	}
	for _, mutation := range []string{"data owner", "provider owner", "connection", "revision", "kind", "subtype", "pending deletion", "deleted", "empty remote"} {
		changed := b
		switch mutation {
		case "data owner":
			changed.OwnerID = uuid.NewString()
		case "provider owner":
			changed.ProviderOwnerID = uuid.NewString()
		case "connection":
			changed.ConnectionID = "other-same-credentials"
		case "revision":
			changed.Revision = "rotated"
		case "kind":
			changed.Kind = "external_import"
		case "subtype":
			changed.Subtype = ""
		case "pending deletion":
			changed.Status = Deleting
		case "deleted":
			changed.Status = Deleted
		case "empty remote":
			changed.RemoteID = ""
		}
		repo.bindings[b.ID] = changed
		if _, err := New(repo, scope).ResolveResourceReferences(ctx, b.ProviderID, refs); !errors.Is(err, ErrNotFound) {
			t.Fatal(mutation, err)
		}
	}
}
func TestProviderResourceSessionExpiryAndFacesAreTrustedCopies(t *testing.T) {
	ctx, repo, b, scope := resourceResolverFixture()
	b.Kind = "face_session"
	b.Subtype = ""
	b.Session = &core.MediaSessionEvidence{ExpiresAt: time.Now().Add(time.Hour), SourceVideoDuration: 4, SourceSHA256: strings.Repeat("a", 64), Faces: []core.SessionFace{{ID: "private-face-id", StartMS: 0, EndMS: 4000}}}
	repo.bindings[b.ID] = b
	refs := []core.ResourceReference{{BindingID: b.ID, ReferenceID: "Session1"}}
	got, err := New(repo, scope).ResolveResourceReferences(ctx, b.ProviderID, refs)
	if err != nil {
		t.Fatal(err)
	}
	got[0].Session.Faces[0].ID = "changed"
	got[0].Session.ExpiresAt = time.Time{}
	if repo.bindings[b.ID].Session.Faces[0].ID != "private-face-id" || repo.bindings[b.ID].Session.ExpiresAt.IsZero() {
		t.Fatal("session evidence was shared")
	}
	expired := *b.Session
	expired.ExpiresAt = time.Now().Add(-time.Second)
	b.Session = &expired
	repo.bindings[b.ID] = b
	if _, err = New(repo, scope).ResolveResourceReferences(ctx, b.ProviderID, refs); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired session accepted", err)
	}
}
func TestProviderResourcePublicMetadataAndIntentAreBounded(t *testing.T) {
	ctx, _, b, _ := resourceResolverFixture()
	_ = ctx
	j := Job{ID: uuid.NewString(), BindingID: b.ID, ProviderID: b.ProviderID, ConnectionID: b.ConnectionID, ProviderOwnerID: b.ProviderOwnerID, Revision: b.Revision, Handle: "private-signed-handle", Operation: Create, Intent: Intent{Kind: b.Kind, Subtype: b.Subtype, Name: "Synthetic", Sources: b.Sources}}
	before, err := HashIntent(j)
	if err != nil {
		t.Fatal(err)
	}
	j.Revision = "different"
	after, _ := HashIntent(j)
	if before == after {
		t.Fatal("canonical revision was absent from immutable intent")
	}
	b.OriginTaskID = uuid.NewString()
	b.Session = &core.MediaSessionEvidence{Faces: []core.SessionFace{{ID: "private-face"}}}
	for _, value := range []any{b, j} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{b.RemoteID, b.Revision, b.ProviderOwnerID, "private-signed-handle", "private-face", b.OriginTaskID} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("private resource evidence leaked", string(raw))
			}
		}
	}
	invalid := j.Intent
	nan := math.NaN()
	invalid.Sources = CloneSources(invalid.Sources)
	invalid.Sources[0].Duration = &nan
	if !errors.Is(ValidateIntent(invalid), ErrInvalid) {
		t.Fatal("non-finite source metadata accepted")
	}
	if SafeFailure("secret-provider-raw") == "secret-provider-raw" || SafeWarning("private-url") == "private-url" {
		t.Fatal("raw adapter diagnostic retained")
	}
}

func TestProviderResourceSessionSelectionEvidenceIsIndependentAndExact(t *testing.T) {
	session := &core.MediaSessionEvidence{ExpiresAt: time.Now().Add(time.Hour), SourceVideoDuration: 4, SourceSHA256: strings.Repeat("a", 64), FrameCount: 120, FPS: 30, SelectionRevision: 1, HasSelection: true, Selections: []core.FrameSelection{{FrameIndex: 1, Points: []core.SelectionPoint{{X: 0.5, Y: 0.5}}}}}
	if err := ValidateSession("editing_session", session); err != nil {
		t.Fatal(err)
	}
	copy := CloneSession(session)
	if !EqualSession(session, copy) {
		t.Fatal("cloning changed session evidence")
	}
	copy.Selections[0].Points[0].X = 0.25
	if session.Selections[0].Points[0].X != 0.5 || EqualSession(session, copy) {
		t.Fatal("selection points were shared or ignored by snapshot comparison")
	}
	copy.Selections[0].Points[0].X = math.NaN()
	if !errors.Is(ValidateSession("editing_session", copy), ErrInvalid) {
		t.Fatal("non-finite selection was trusted")
	}
	copy = CloneSession(session)
	copy.Selections[0].FrameIndex = 120
	if !errors.Is(ValidateSession("editing_session", copy), ErrInvalid) {
		t.Fatal("selection exceeded inspected session frames")
	}
}

func TestProviderResourceSessionCannotTrustUninspectedHashOrDuration(t *testing.T) {
	ctx, repo, b, scope := resourceResolverFixture()
	b.Kind = "editing_session"
	b.Subtype = ""
	b.Session = &core.MediaSessionEvidence{ExpiresAt: time.Now().Add(time.Hour), SourceVideoDuration: 4, SourceSHA256: strings.Repeat("a", 64), FrameCount: 120, FPS: 30}
	repo.bindings[b.ID] = b
	refs := []core.ResourceReference{{BindingID: b.ID, ReferenceID: "editing_session"}}
	if _, err := New(repo, scope).ResolveResourceReferences(ctx, b.ProviderID, refs); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"provider-only hash", "provider-only duration", "unknown inspected duration"} {
		changed := b
		changed.Session = CloneSession(b.Session)
		changed.Sources = CloneSources(b.Sources)
		switch mutation {
		case "provider-only hash":
			changed.Session.SourceSHA256 = strings.Repeat("b", 64)
		case "provider-only duration":
			changed.Session.SourceVideoDuration = 99
		case "unknown inspected duration":
			changed.Sources[0].Duration = nil
		}
		repo.bindings[b.ID] = changed
		if _, err := New(repo, scope).ResolveResourceReferences(ctx, b.ProviderID, refs); !errors.Is(err, ErrNotFound) {
			t.Fatal(mutation, err)
		}
	}
}
