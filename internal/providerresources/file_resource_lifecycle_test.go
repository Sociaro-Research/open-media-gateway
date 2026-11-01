package providerresources

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

type fileResourceLifecycleProvider struct {
	*lifecycleProvider
	expected Binding
}

func (p *fileResourceLifecycleProvider) DeleteResource(ctx context.Context, snapshot core.ResourceSnapshot) (core.ResourceJob, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.check(ctx)
	p.deleteCalls++
	if snapshot.BindingID != p.expected.ID || snapshot.Kind != "file_resource" || snapshot.RemoteID != p.expected.RemoteID || snapshot.Subtype != p.expected.Subtype || snapshot.ConnectionIdentity != p.expected.Revision || snapshot.IsPreset {
		p.t.Errorf("file deletion changed trusted local/opaque identity: kind=%q subtype=%q", snapshot.Kind, snapshot.Subtype)
	}
	if p.mutateSources && len(snapshot.Sources) > 0 {
		snapshot.Sources[0].SHA256 = "provider-mutated"
		if snapshot.Sources[0].Duration != nil {
			*snapshot.Sources[0].Duration = 99
		}
	}
	return p.deleteResult, p.deleteErr
}
func (p *fileResourceLifecycleProvider) GetResourceJob(ctx context.Context, kind, handle string) (core.ResourceJob, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.check(ctx)
	p.pollCalls++
	if kind != "file_resource" || handle != "private-file-delete-handle" {
		p.t.Errorf("file poll changed trusted kind/handle: kind=%q", kind)
	}
	return p.pollResult, p.pollErr
}
func fileResourceLifecycleFixture(t *testing.T) (context.Context, *lifecycleMemory, *fileResourceLifecycleProvider, ScopeResolver, *Lifecycle) {
	t.Helper()
	ctx, repo, _, _, _ := lifecycleFixture(t)
	repo.binding.Kind = "file_resource"
	repo.binding.Subtype = "voice_clone"
	repo.binding.Name = "Synthetic file"
	repo.binding.ProviderID = "minimax--actual-connection"
	repo.binding.ConnectionID = repo.binding.ProviderID
	repo.binding.RemoteID = "000900719925474099312345678901234567890"
	scope := core.ProviderConnectionScope{ID: repo.binding.ConnectionID, OwnerID: repo.binding.ProviderOwnerID, Revision: repo.binding.Revision}
	current := func(context.Context, string) (core.ProviderConnectionScope, error) { return scope, nil }
	provider := &fileResourceLifecycleProvider{lifecycleProvider: &lifecycleProvider{expectedScope: scope, owner: repo.binding.OwnerID, t: t, deleteResult: core.ResourceJob{Handle: "private-file-delete-handle", Status: core.ProviderQueued}, pollResult: core.ResourceJob{Handle: "private-file-delete-handle", Status: core.ProviderRunning}}, expected: repo.binding}
	directory := lifecycleDirectory{provider: provider, scope: scope, t: t}
	return ctx, repo, provider, current, NewLifecycle(repo, current, directory)
}

func TestFileResourceLifecycleDeleteSupportsConfirmedSuccessAndOpaqueIDs(t *testing.T) {
	ctx, repo, provider, _, lifecycle := fileResourceLifecycleFixture(t)
	provider.deleteResult = core.ResourceJob{Status: core.ProviderSucceeded}
	provider.mutateSources = true
	job, err := lifecycle.Delete(ctx, repo.binding.ID, "file-delete-confirmed")
	if err != nil || job.Status != Succeeded || job.Kind != "file_resource" {
		t.Fatal("file resource did not reach the real deletion gate", job, err)
	}
	binding, err := repo.GetBinding(ctx, repo.binding.ID)
	if err != nil || binding.Status != Deleted || binding.DeletedAt == nil || binding.RemoteID != provider.expected.RemoteID || binding.Sources[0].SHA256 != strings.Repeat("a", 64) || *binding.Sources[0].Duration != 10 {
		t.Fatal("file deletion changed retained identity/evidence", binding.Status, err)
	}
	replay, err := lifecycle.Delete(ctx, binding.ID, "file-delete-confirmed")
	if err != nil || replay.ID != job.ID || replay.Status != Succeeded {
		t.Fatal(replay, err)
	}
	if _, err = lifecycle.Poll(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	deletes, polls := provider.counts()
	if deletes != 1 || polls != 0 {
		t.Fatal("confirmed deletion was resubmitted", deletes, polls)
	}
	public, _ := json.Marshal(job)
	for _, private := range []string{binding.RemoteID, binding.Revision, "private-file-delete-handle", binding.Sources[0].SHA256} {
		if strings.Contains(string(public), private) {
			t.Fatal("public file delete job leaked private evidence")
		}
	}
}
func TestFileResourceLifecyclePendingHandlePollsWithoutResubmission(t *testing.T) {
	ctx, repo, provider, current, lifecycle := fileResourceLifecycleFixture(t)
	job, err := lifecycle.Delete(ctx, repo.binding.ID, "file-delete-pending")
	if err != nil || job.Status != Running || job.Handle != "private-file-delete-handle" {
		t.Fatal(job, err)
	}
	binding, _ := repo.GetBinding(ctx, repo.binding.ID)
	if binding.Status != Deleting || binding.DeletedAt != nil {
		t.Fatal("submitted file deletion was falsely terminal")
	}
	for range 3 {
		replay, err := lifecycle.Delete(ctx, binding.ID, "file-delete-pending")
		if err != nil || replay.ID != job.ID {
			t.Fatal(replay, err)
		}
	}
	restarted := NewLifecycle(repo, current, lifecycle.directory)
	state, err := restarted.Poll(ctx, job.ID)
	if err != nil || state.Status != Running {
		t.Fatal(state, err)
	}
	provider.pollResult = core.ResourceJob{Handle: "private-file-delete-handle", Status: core.ProviderSucceeded}
	state, err = restarted.Poll(ctx, job.ID)
	if err != nil || state.Status != Succeeded {
		t.Fatal(state, err)
	}
	deletes, polls := provider.counts()
	if deletes != 1 || polls != 2 {
		t.Fatal("file poll submitted another deletion", deletes, polls)
	}
}
func TestFileResourceLifecycleUncertainTransportNeverDuplicatesDelete(t *testing.T) {
	for _, kind := range []string{"transport failure", "pending without handle"} {
		t.Run(kind, func(t *testing.T) {
			ctx, repo, provider, current, lifecycle := fileResourceLifecycleFixture(t)
			if kind == "transport failure" {
				provider.deleteErr = errors.New("private transport token and URL")
			} else {
				provider.deleteResult = core.ResourceJob{Status: core.ProviderQueued}
			}
			job, err := lifecycle.Delete(ctx, repo.binding.ID, "file-delete-uncertain")
			if err != nil || job.Status != Running || job.Handle != "" || !strings.Contains(job.Warning, "uncertain") {
				t.Fatal(job, err)
			}
			restarted := NewLifecycle(repo, current, lifecycle.directory)
			for range 3 {
				replay, err := restarted.Delete(ctx, repo.binding.ID, "file-delete-uncertain")
				if err != nil || replay.ID != job.ID {
					t.Fatal(replay, err)
				}
				if _, err = restarted.Poll(ctx, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = restarted.Delete(ctx, repo.binding.ID, "new-key-while-uncertain"); !errors.Is(err, ErrConflict) {
				t.Fatal("uncertain file delete accepted another submission", err)
			}
			deletes, polls := provider.counts()
			if deletes != 1 || polls != 0 {
				t.Fatal("uncertain file delete was resubmitted", deletes, polls)
			}
			binding, _ := repo.GetBinding(ctx, repo.binding.ID)
			_, _, uncertain, failed, completed := repo.counts()
			if binding.Status != Deleting || uncertain == 0 || failed != 0 || completed != 0 {
				t.Fatal("uncertainty weakened deletion state", binding.Status, uncertain, failed, completed)
			}
		})
	}
}
func TestFileResourceLifecycleRejectsScopePresetBusyAndUnknownKindsBeforeIO(t *testing.T) {
	for _, kind := range []string{"owner", "provider owner", "connection", "revision", "preset", "in-use", "queued binding", "running binding", "unreviewed_session", "file", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			ctx, repo, provider, _, lifecycle := fileResourceLifecycleFixture(t)
			expected := ErrNotFound
			switch kind {
			case "owner":
				repo.binding.OwnerID = uuid.NewString()
			case "provider owner":
				repo.binding.ProviderOwnerID = uuid.NewString()
			case "connection":
				repo.binding.ConnectionID = "same-credentials-other-connection"
			case "revision":
				repo.binding.Revision = "changed-revision"
			case "preset":
				repo.binding.IsPreset = true
				expected = ErrConflict
			case "in-use":
				repo.busy = true
				expected = ErrInUse
			case "queued binding":
				repo.binding.Status = Queued
				expected = ErrConflict
			case "running binding":
				repo.binding.Status = Running
				expected = ErrConflict
			default:
				repo.binding.Kind = kind
				expected = ErrInvalid
			}
			_, err := lifecycle.Delete(ctx, repo.binding.ID, "file-delete-blocked")
			if !errors.Is(err, expected) {
				t.Fatal(kind, err)
			}
			deletes, polls := provider.counts()
			if deletes != 0 || polls != 0 {
				t.Fatal("invalid file deletion reached provider I/O", kind, deletes, polls)
			}
		})
	}
}
func TestFileResourceLifecyclePollRejectsChangedScopeBeforeIO(t *testing.T) {
	for _, kind := range []string{"owner", "provider owner", "connection", "revision"} {
		t.Run(kind, func(t *testing.T) {
			ctx, repo, provider, _, lifecycle := fileResourceLifecycleFixture(t)
			job, err := lifecycle.Delete(ctx, repo.binding.ID, "file-delete-poll-scope")
			if err != nil {
				t.Fatal(err)
			}
			scope := provider.expectedScope
			request := ctx
			switch kind {
			case "owner":
				request = identity.WithOwnerID(ctx, uuid.NewString())
			case "provider owner":
				scope.OwnerID = uuid.NewString()
			case "connection":
				scope.ID = "same-credentials-other-connection"
			case "revision":
				scope.Revision = "changed-revision"
			}
			lifecycle.current = func(context.Context, string) (core.ProviderConnectionScope, error) { return scope, nil }
			if _, err = lifecycle.Poll(request, job.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal(kind, err)
			}
			deletes, polls := provider.counts()
			if deletes != 1 || polls != 0 {
				t.Fatal("file scope mismatch polled or resubmitted", kind, deletes, polls)
			}
		})
	}
}
