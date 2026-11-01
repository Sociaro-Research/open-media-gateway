package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/google/uuid"
)

func revokedSessionFixture(t *testing.T, kind string) (*providerResourceFixture, providerresources.Binding) {
	t.Helper()
	f := newProviderResourceFixture(t)
	f.job.Intent.Kind = kind
	f.job.Intent.Subtype = "selection"
	f.job.Intent.Name = "Local session"
	j := f.start(t, "local-session")
	session := &core.MediaSessionEvidence{ExpiresAt: time.Now().UTC().Add(time.Hour), SourceSHA256: f.job.Intent.Sources[0].SHA256, SourceVideoDuration: *f.job.Intent.Sources[0].Duration, FrameCount: 120, FPS: 30, SelectionRevision: 2}
	b, err := f.repo.CompleteResource(f.ctx, j.ID, core.ProviderResource{Kind: kind, Subtype: "selection", RemoteID: "private-session-identity", Status: core.ProviderSucceeded, Session: session})
	if err != nil {
		t.Fatal(err)
	}
	return f, b
}

func TestSessionRevocationPostgresPreservesTombstoneAndBlocksReuse(t *testing.T) {
	for _, kind := range []string{"face_session", "editing_session"} {
		t.Run(kind, func(t *testing.T) {
			f, b := revokedSessionFixture(t, kind)
			l := providerresources.NewLifecycle(f.repo, f.scope, nil)
			job, err := l.Delete(f.ctx, b.ID, "revoke-owned-session")
			if err != nil || job.Status != providerresources.Succeeded || job.Handle != "" {
				t.Fatal("local session revocation required provider", job, err)
			}
			stored, err := NewProviderResourceRepository(f.db).GetBinding(f.ctx, b.ID)
			if err != nil || stored.Status != providerresources.Deleted || stored.DeletedAt == nil || stored.RemoteID != b.RemoteID || stored.Revision != b.Revision || !providerresources.EqualSources(stored.Sources, b.Sources) || !providerresources.EqualSession(stored.Session, b.Session) {
				t.Fatal("local tombstone lost immutable proof", stored, err)
			}
			resolver := providerresources.New(f.repo, f.scope)
			if _, err = resolver.ResolveResourceReferences(f.ctx, b.ProviderID, []core.ResourceReference{{BindingID: b.ID, ReferenceID: "session"}}); !errors.Is(err, providerresources.ErrNotFound) {
				t.Fatal("revoked session reused", err)
			}
			again, err := providerresources.NewLifecycle(NewProviderResourceRepository(f.db), f.scope, nil).Delete(f.ctx, b.ID, "revoke-owned-session")
			if err != nil || again.ID != job.ID || again.Status != providerresources.Succeeded {
				t.Fatal("restart did not replay local revoke", again, err)
			}
			foreign := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), "11111111-1111-4111-8111-111111111111"), b.ProviderOwnerID, b.OwnerID)
			if _, err = l.Poll(foreign, job.ID); !errors.Is(err, providerresources.ErrNotFound) {
				t.Fatal("foreign user read local revocation", err)
			}
		})
	}
}

func TestSessionRevocationPostgresPollResumesLocalJobAndHonorsInUse(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "running"}[running], func(t *testing.T) {
			f, b := revokedSessionFixture(t, "editing_session")
			job, created, err := f.repo.BeginDelete(f.ctx, b.ID, "interrupted-local")
			if err != nil || !created {
				t.Fatal(job, created, err)
			}
			if running {
				if _, started, err := f.repo.StartJob(f.ctx, job.ID); err != nil || !started {
					t.Fatal(started, err)
				}
			}
			completed, err := providerresources.NewLifecycle(NewProviderResourceRepository(f.db), f.scope, nil).Poll(f.ctx, job.ID)
			if err != nil || completed.Status != providerresources.Succeeded || completed.Handle != "" {
				t.Fatal("local restart required native handle", completed, err)
			}
		})
	}
	f, b := revokedSessionFixture(t, "editing_session")
	now := time.Now().UTC()
	task := core.Task{ID: uuid.NewString(), OwnerID: b.OwnerID, ProviderOwnerID: b.ProviderOwnerID, StorageOwnerID: b.OwnerID, Provider: b.ProviderID, ProviderIdentity: b.Revision, Model: "reviewed-session-render", MediaType: "video", Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now, Options: core.MediaOptions{ResourceReferences: []core.ResourceReference{{BindingID: b.ID, ReferenceID: "session"}}, ResourceSnapshots: []core.ResourceSnapshot{{BindingID: b.ID, ReferenceID: "session", Kind: b.Kind, Subtype: b.Subtype, RemoteID: b.RemoteID, ConnectionIdentity: b.Revision, Sources: providerresources.CloneSources(b.Sources), Session: providerresources.CloneSession(b.Session)}}}}
	accepted, created, err := NewTaskRepository(f.db).CreateOrGet(f.ctx, task, "active-generation", "active-generation")
	if err != nil || !created {
		t.Fatal("active generation not admitted", accepted, created, err)
	}
	l := providerresources.NewLifecycle(f.repo, f.scope, nil)
	if _, err := l.Delete(f.ctx, b.ID, "blocked"); !errors.Is(err, providerresources.ErrInUse) {
		t.Fatal("active generation session revoked", err)
	}
	unchanged, err := f.repo.GetBinding(f.ctx, b.ID)
	if err != nil || unchanged.Status != providerresources.Succeeded {
		t.Fatal("blocked revocation changed binding", unchanged, err)
	}
	if err = NewTaskRepository(f.db).MarkFailed(f.ctx, accepted.ID, "synthetic terminal task"); err != nil {
		t.Fatal(err)
	}
	if done, err := l.Delete(f.ctx, b.ID, "blocked"); err != nil || done.Status != providerresources.Succeeded {
		t.Fatal("completed generation retained session", done, err)
	}
}

type revocationPreflightProvider struct {
	core.MediaProvider
	checks int
}

func (p *revocationPreflightProvider) ValidateResourceDeletion(context.Context, core.ResourceSnapshot) error {
	p.checks++
	return core.ErrOptionsInvalid
}

type revocationPreflightDirectory struct {
	core.ProviderDirectory
	p *revocationPreflightProvider
}

func (d revocationPreflightDirectory) Provider(context.Context, string) (core.MediaProvider, error) {
	return d.p, nil
}

func TestSessionRevocationPostgresUnsupportedRemoteDeleteDoesNotReserve(t *testing.T) {
	f := newProviderResourceFixture(t)
	f.job.Intent.Kind = "file_resource"
	f.job.Intent.Subtype = "image"
	f.job.Intent.Sources[0].MediaType = "image"
	f.job.Intent.Sources[0].Duration = nil
	j := f.start(t, "owned-input-file")
	b, err := f.repo.CompleteResource(f.ctx, j.ID, core.ProviderResource{Kind: "file_resource", Subtype: "image", RemoteID: "private-input-file-capability", Status: core.ProviderSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	p := &revocationPreflightProvider{}
	l := providerresources.NewLifecycle(f.repo, f.scope, revocationPreflightDirectory{p: p})
	if _, err = l.Delete(f.ctx, b.ID, "unsupported-delete"); !errors.Is(err, core.ErrOptionsInvalid) || p.checks != 1 {
		t.Fatal("known unsupported deletion was admitted", err, p.checks)
	}
	current, err := f.repo.GetBinding(f.ctx, b.ID)
	if err != nil || current.Status != providerresources.Succeeded {
		t.Fatal("pure preflight destroyed usable binding", current, err)
	}
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM provider_resource_jobs WHERE owner_id=$1 AND operation='delete'`, b.OwnerID).Scan(&count); err != nil || count != 0 {
		t.Fatal("pure rejection left a deletion job", count, err)
	}
}
