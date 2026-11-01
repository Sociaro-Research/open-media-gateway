package providerresources

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

type sessionRevocationDirectory struct{ calls atomic.Int32 }

func (d *sessionRevocationDirectory) Providers(context.Context) (map[string]core.MediaProvider, error) {
	d.calls.Add(1)
	return nil, errors.New("local revocation must not enumerate providers")
}
func (d *sessionRevocationDirectory) Provider(context.Context, string) (core.MediaProvider, error) {
	d.calls.Add(1)
	return nil, errors.New("local revocation must not resolve an adapter")
}

func sessionRevocationFixture(t *testing.T, kind string) (context.Context, *lifecycleMemory, ScopeResolver, *sessionRevocationDirectory, *Lifecycle) {
	t.Helper()
	ctx, repo, _, current, _ := lifecycleFixture(t)
	repo.binding.Kind = kind
	repo.binding.Subtype = "selection"
	repo.binding.Sources[0].MediaType = "video"
	repo.binding.Session = &core.MediaSessionEvidence{ExpiresAt: time.Now().UTC().Add(time.Hour), SourceSHA256: repo.binding.Sources[0].SHA256, SourceVideoDuration: *repo.binding.Sources[0].Duration, FrameCount: 300, FPS: 30, SelectionRevision: 7, HasSelection: true, Selections: []core.FrameSelection{{FrameIndex: 1, Points: []core.SelectionPoint{{X: .25, Y: .5}}}}}
	directory := &sessionRevocationDirectory{}
	return ctx, repo, current, directory, NewLifecycle(repo, current, directory)
}

func TestSessionRevocationDeletesOnlyLocalBindingAndReplays(t *testing.T) {
	for _, kind := range []string{"face_session", "editing_session"} {
		t.Run(kind, func(t *testing.T) {
			ctx, repo, current, directory, l := sessionRevocationFixture(t, kind)
			original := repo.binding
			original.Session = CloneSession(original.Session)
			original.Sources = CloneSources(original.Sources)
			job, err := l.Delete(ctx, repo.binding.ID, "revoke-once")
			if err != nil || job.Operation != Delete || job.Status != Succeeded || job.Handle != "" || !strings.Contains(job.Warning, "Local session") {
				t.Fatal("local session revocation was not terminal", job, err)
			}
			b, err := repo.GetBinding(ctx, original.ID)
			if err != nil || b.Status != Deleted || b.DeletedAt == nil || b.RemoteID != original.RemoteID || b.Revision != original.Revision || !EqualSources(b.Sources, original.Sources) || !EqualSession(b.Session, original.Session) {
				t.Fatal("revocation destroyed private tombstone evidence", b, err)
			}
			restarted := NewLifecycle(repo, current, nil)
			replay, err := restarted.Delete(ctx, b.ID, "revoke-once")
			if err != nil || replay.ID != job.ID || replay.Status != Succeeded {
				t.Fatal("revocation replay required a native adapter", replay, err)
			}
			polled, err := restarted.Poll(ctx, job.ID)
			if err != nil || polled.Status != Succeeded {
				t.Fatal(polled, err)
			}
			_, _, uncertain, failed, complete := repo.counts()
			if directory.calls.Load() != 0 || uncertain != 0 || failed != 0 || complete != 1 {
				t.Fatal("local revocation invoked a provider or uncertainty path", directory.calls.Load(), uncertain, failed, complete)
			}
			for _, value := range []any{job, b} {
				raw, _ := json.Marshal(value)
				for _, secret := range []string{original.RemoteID, original.Revision, original.Session.SourceSHA256} {
					if strings.Contains(string(raw), secret) {
						t.Fatal("private session tombstone leaked", string(raw))
					}
				}
			}
		})
	}
}

func TestSessionRevocationPollResumesQueuedAndRunningWithoutHandle(t *testing.T) {
	for _, status := range []Status{Queued, Running} {
		t.Run(string(status), func(t *testing.T) {
			ctx, repo, current, directory, _ := sessionRevocationFixture(t, "editing_session")
			job, created, err := repo.BeginDelete(ctx, repo.binding.ID, "restart-local")
			if err != nil || !created {
				t.Fatal(job, err)
			}
			if status == Running {
				if _, started, err := repo.StartJob(ctx, job.ID); err != nil || !started {
					t.Fatal(started, err)
				}
			}
			restarted := NewLifecycle(repo, current, directory)
			done, err := restarted.Poll(ctx, job.ID)
			if err != nil || done.Status != Succeeded || done.Handle != "" {
				t.Fatal("missing native handle blocked local revocation", done, err)
			}
			_, _, uncertain, _, _ := repo.counts()
			if uncertain != 0 || directory.calls.Load() != 0 {
				t.Fatal("local restart entered remote uncertainty", uncertain, directory.calls.Load())
			}
			if repo.binding.Status != Deleted {
				t.Fatal("restart did not revoke local session")
			}
		})
	}
}

func TestSessionRevocationPreservesScopePresetAndInUseGuards(t *testing.T) {
	for _, mutation := range []string{"preset", "in-use", "data-owner", "provider-owner", "connection", "revision", "unknown-kind", "missing-scope"} {
		t.Run(mutation, func(t *testing.T) {
			ctx, repo, current, directory, l := sessionRevocationFixture(t, "face_session")
			switch mutation {
			case "preset":
				repo.binding.IsPreset = true
			case "in-use":
				repo.busy = true
			case "data-owner":
				repo.binding.OwnerID = "other-data-owner"
			case "provider-owner":
				repo.binding.ProviderOwnerID = "other-provider-owner"
			case "connection":
				repo.binding.ConnectionID = "other-connection"
			case "revision":
				repo.binding.Revision = "rotated"
			case "unknown-kind":
				repo.binding.Kind = "unreviewed_session"
			case "missing-scope":
				l.current = nil
			}
			_, err := l.Delete(ctx, repo.binding.ID, "guard")
			if err == nil {
				t.Fatal("invalid local revocation admitted", mutation)
			}
			if mutation == "in-use" && !errors.Is(err, ErrInUse) {
				t.Fatal(err)
			}
			if repo.binding.Status != Succeeded || len(repo.jobs) != 0 || directory.calls.Load() != 0 {
				t.Fatal("guard changed binding or invoked adapter", mutation)
			}
			_ = current
		})
	}
	// Host scope remains mandatory, while a provider directory is unnecessary for
	// local revocation. Remote operations keep their existing directory guard.
	ctx, repo, current, _, _ := sessionRevocationFixture(t, "editing_session")
	l := NewLifecycle(repo, current, nil)
	if _, err := l.Delete(ctx, repo.binding.ID, "nil-directory"); err != nil {
		t.Fatal(err)
	}
	ctx, repo, current, _, _ = sessionRevocationFixture(t, "editing_session")
	repo.binding.Kind = "voice"
	if _, err := NewLifecycle(repo, current, nil).Delete(ctx, repo.binding.ID, "remote"); !errors.Is(err, ErrInvalid) || len(repo.jobs) != 0 {
		t.Fatal("remote deletion silently bypassed provider availability", err)
	}
}

type sessionRevocationRetryRepository struct {
	*lifecycleMemory
	mu     sync.Mutex
	reject bool
}

func (r *sessionRevocationRetryRepository) CompleteDelete(ctx context.Context, id string) error {
	r.mu.Lock()
	reject := r.reject
	r.reject = false
	r.mu.Unlock()
	if reject {
		return errors.New("synthetic local commit failure")
	}
	return r.lifecycleMemory.CompleteDelete(ctx, id)
}

func TestSessionRevocationLocalCommitFailureIsRetryableWithoutProviderIO(t *testing.T) {
	ctx, memory, current, directory, _ := sessionRevocationFixture(t, "editing_session")
	repo := &sessionRevocationRetryRepository{lifecycleMemory: memory, reject: true}
	l := NewLifecycle(repo, current, directory)
	job, err := l.Delete(ctx, memory.binding.ID, "retry-commit")
	if err == nil || job.Status != Running || memory.binding.Status != Deleting {
		t.Fatal("local commit error became terminal", job, err)
	}
	done, err := NewLifecycle(repo, current, nil).Poll(ctx, job.ID)
	if err != nil || done.Status != Succeeded || memory.binding.Status != Deleted {
		t.Fatal("local commit retry did not finish", done, err)
	}
	_, _, uncertain, _, _ := memory.counts()
	if uncertain != 0 || directory.calls.Load() != 0 {
		t.Fatal("local storage error was misclassified as remote uncertainty")
	}
}

func TestSessionRevocationConcurrentReplayHasOneLocalJob(t *testing.T) {
	ctx, repo, _, directory, l := sessionRevocationFixture(t, "editing_session")
	start := make(chan struct{})
	jobs := make(chan Job, 12)
	errs := make(chan error, 12)
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			j, e := l.Delete(ctx, repo.binding.ID, "same-local-action")
			jobs <- j
			errs <- e
		}()
	}
	close(start)
	group.Wait()
	close(jobs)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for j := range jobs {
		if j.Status != Succeeded {
			t.Fatal("revocation remained pending", j)
		}
		if id != "" && j.ID != id {
			t.Fatal("concurrent revocation minted another job")
		}
		id = j.ID
	}
	if directory.calls.Load() != 0 || repo.binding.Status != Deleted || len(repo.jobs) != 1 {
		t.Fatal("local revocation duplicated native work")
	}
}

func TestSessionRevocationPollRevalidatesCurrentScope(t *testing.T) {
	ctx, repo, current, directory, _ := sessionRevocationFixture(t, "editing_session")
	job, _, err := repo.BeginDelete(ctx, repo.binding.ID, "queued-local")
	if err != nil {
		t.Fatal(err)
	}
	changed := func(ctx context.Context, id string) (core.ProviderConnectionScope, error) {
		scope, _ := current(ctx, id)
		scope.Revision = "rotated"
		return scope, nil
	}
	if _, err = NewLifecycle(repo, changed, directory).Poll(ctx, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale connection revoked queued session", err)
	}
	_, starts, uncertain, _, completes := repo.counts()
	if starts != 0 || uncertain != 0 || completes != 0 || directory.calls.Load() != 0 || repo.binding.Status != Deleting {
		t.Fatal("scope mismatch progressed local revocation")
	}
}

type sessionDeletionPreflightProvider struct {
	*lifecycleProvider
	checks        int
	validationErr error
	mutate        bool
}

func (p *sessionDeletionPreflightProvider) ValidateResourceDeletion(ctx context.Context, s core.ResourceSnapshot) error {
	p.check(ctx)
	p.checks++
	if s.BindingID == "" || s.RemoteID != "9007199254740993" || s.ConnectionIdentity != p.expectedScope.Revision {
		p.t.Fatal("preflight did not receive trusted persisted snapshot", s)
	}
	if p.mutate && len(s.Sources) > 0 {
		s.Sources[0].SHA256 = "preflight-attempted-mutation"
		if s.Sources[0].Duration != nil {
			*s.Sources[0].Duration = 999
		}
	}
	return p.validationErr
}

func TestSessionRevocationRemotePreflightRejectsBeforeReservation(t *testing.T) {
	ctx, repo, native, current, l := lifecycleFixture(t)
	repo.binding.Kind = "file_resource"
	repo.binding.Subtype = "image"
	original := CloneSources(repo.binding.Sources)
	p := &sessionDeletionPreflightProvider{lifecycleProvider: native, validationErr: core.ErrOptionsInvalid, mutate: true}
	scope, _ := current(ctx, repo.binding.ProviderID)
	l.directory = lifecycleDirectory{provider: p, scope: scope, t: t}
	if _, err := l.Delete(ctx, repo.binding.ID, "unsupported-input-delete"); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("known unsupported delete was accepted", err)
	}
	begins, starts, uncertain, failed, completed := repo.counts()
	deletes, polls := native.counts()
	if p.checks != 1 || begins != 0 || starts != 0 || uncertain != 0 || failed != 0 || completed != 0 || deletes != 0 || polls != 0 || len(repo.jobs) != 0 || repo.binding.Status != Succeeded || !EqualSources(repo.binding.Sources, original) {
		t.Fatal("pure rejection mutated resource or performed I/O", p.checks, begins, starts, deletes, polls, repo.binding)
	}
}

func TestSessionRevocationRemotePreflightDoesNotRevalidateAcceptedPendingOrDeleted(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "deleted"}[terminal], func(t *testing.T) {
			ctx, repo, native, current, l := lifecycleFixture(t)
			p := &sessionDeletionPreflightProvider{lifecycleProvider: native}
			if terminal {
				native.deleteResult.Status = core.ProviderSucceeded
			}
			scope, _ := current(ctx, repo.binding.ProviderID)
			l.directory = lifecycleDirectory{provider: p, scope: scope, t: t}
			job, err := l.Delete(ctx, repo.binding.ID, "accepted")
			if err != nil {
				t.Fatal(err)
			}
			p.validationErr = core.ErrOptionsInvalid
			replay, err := l.Delete(ctx, repo.binding.ID, "accepted")
			if err != nil || replay.ID != job.ID || p.checks != 1 {
				t.Fatal("accepted delete replay depended on new validation", replay, err, p.checks)
			}
			deletes, polls := native.counts()
			if deletes != 1 || polls != 0 {
				t.Fatal("accepted delete resubmitted", deletes, polls)
			}
		})
	}
}

func TestSessionRevocationConcurrentPollCompletesOneLocalJob(t *testing.T) {
	ctx, repo, current, directory, _ := sessionRevocationFixture(t, "editing_session")
	job, _, err := repo.BeginDelete(ctx, repo.binding.ID, "concurrent-poll")
	if err != nil {
		t.Fatal(err)
	}
	l := NewLifecycle(repo, current, directory)
	start := make(chan struct{})
	errs := make(chan error, 12)
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			done, e := l.Poll(ctx, job.ID)
			if e == nil && done.Status != Succeeded {
				e = errors.New("local poll did not complete")
			}
			errs <- e
		}()
	}
	close(start)
	group.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if directory.calls.Load() != 0 || repo.binding.Status != Deleted || len(repo.jobs) != 1 {
		t.Fatal("concurrent polling created native work")
	}
}
