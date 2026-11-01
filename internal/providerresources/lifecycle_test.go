package providerresources

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

type lifecycleMemory struct {
	Repository
	mu                                                               sync.Mutex
	binding                                                          Binding
	jobs                                                             map[string]Job
	keys                                                             map[string]string
	beginCalls, startCalls, uncertainCalls, failCalls, completeCalls int
	busy                                                             bool
	saveError                                                        error
}

func (m *lifecycleMemory) GetBinding(_ context.Context, id string) (Binding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != m.binding.ID {
		return Binding{}, ErrNotFound
	}
	b := m.binding
	b.Sources = CloneSources(b.Sources)
	return b, nil
}
func (m *lifecycleMemory) GetJob(_ context.Context, id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return j, ErrNotFound
	}
	j.Intent = CloneIntent(j.Intent)
	return j, nil
}
func (m *lifecycleMemory) BeginDelete(ctx context.Context, id, key string) (Job, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.beginCalls++
	if prior, ok := m.keys[key]; ok {
		j := m.jobs[prior]
		if j.BindingID != id {
			return j, false, ErrConflict
		}
		return j, false, nil
	}
	if m.busy {
		return Job{}, false, ErrInUse
	}
	if m.binding.Status != Succeeded && m.binding.Status != Failed {
		return Job{}, false, ErrConflict
	}
	b := m.binding
	j := Job{ID: uuid.NewString(), BindingID: b.ID, Kind: b.Kind, OwnerID: b.OwnerID, ActorID: identity.ActorID(ctx), ProviderID: b.ProviderID, ConnectionID: b.ConnectionID, ProviderOwnerID: b.ProviderOwnerID, Revision: b.Revision, Operation: Delete, Status: Queued, Intent: Intent{Kind: b.Kind, Subtype: b.Subtype, Name: b.Name, Sources: CloneSources(b.Sources)}}
	m.jobs[j.ID] = j
	m.keys[key] = j.ID
	m.binding.Status = Deleting
	return j, true, nil
}
func (m *lifecycleMemory) StartJob(_ context.Context, id string) (Job, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalls++
	j := m.jobs[id]
	if j.Status != Queued {
		return j, false, nil
	}
	now := time.Now()
	j.Status = Running
	j.StartedAt = &now
	m.jobs[id] = j
	return j, true, nil
}
func (m *lifecycleMemory) SaveHandle(_ context.Context, id, handle, warning string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveError != nil {
		return m.saveError
	}
	j := m.jobs[id]
	if j.Status != Running || (j.Handle != "" && j.Handle != handle) {
		return ErrConflict
	}
	j.Handle = handle
	j.Warning = SafeWarning(warning)
	m.jobs[id] = j
	return nil
}
func (m *lifecycleMemory) RecordUncertain(_ context.Context, id, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.uncertainCalls++
	j := m.jobs[id]
	if j.Status != Running {
		return ErrConflict
	}
	j.Warning = "The provider outcome is uncertain. Check this job before submitting another operation."
	m.jobs[id] = j
	return nil
}
func (m *lifecycleMemory) FailJob(_ context.Context, id, raw, warning string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCalls++
	j := m.jobs[id]
	j.Status = Failed
	j.SafeError = SafeFailure(raw)
	j.Warning = SafeWarning(warning)
	m.jobs[id] = j
	m.binding.Status = Failed
	return nil
}
func (m *lifecycleMemory) CompleteDelete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completeCalls++
	j := m.jobs[id]
	if j.Status != Running && j.Status != Succeeded {
		return ErrConflict
	}
	j.Status = Succeeded
	m.jobs[id] = j
	m.binding.Status = Deleted
	now := time.Now()
	m.binding.DeletedAt = &now
	return nil
}
func (m *lifecycleMemory) counts() (int, int, int, int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.beginCalls, m.startCalls, m.uncertainCalls, m.failCalls, m.completeCalls
}

type lifecycleProvider struct {
	mu                       sync.Mutex
	deleteCalls, pollCalls   int
	deleteResult, pollResult core.ResourceJob
	deleteErr, pollErr       error
	expectedScope            core.ProviderConnectionScope
	owner                    string
	t                        *testing.T
	mutateSources            bool
}

func (p *lifecycleProvider) Name() string { return "synthetic-resources" }
func (*lifecycleProvider) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	return core.ProviderTask{}, errors.New("unexpected generation")
}
func (*lifecycleProvider) GetTask(context.Context, string) (core.ProviderResult, error) {
	return core.ProviderResult{}, errors.New("unexpected generation poll")
}
func (*lifecycleProvider) CreateResource(context.Context, core.ResourceCreateRequest) (core.ResourceJob, error) {
	return core.ResourceJob{}, errors.New("unmetered creation prohibited")
}
func (*lifecycleProvider) ListResources(context.Context, string, bool, int, int) (core.ResourcePage, error) {
	return core.ResourcePage{}, errors.New("live inventory import prohibited")
}
func (p *lifecycleProvider) check(ctx context.Context) {
	p.t.Helper()
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if !ok || scope != p.expectedScope || core.ProviderRevision(ctx) != p.expectedScope.Revision || identity.OwnerID(ctx) != p.owner {
		p.t.Errorf("provider called without pinned exact scope: %+v %t owner=%s", scope, ok, identity.OwnerID(ctx))
	}
}
func (p *lifecycleProvider) DeleteResource(ctx context.Context, s core.ResourceSnapshot) (core.ResourceJob, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.check(ctx)
	p.deleteCalls++
	if s.Kind != "voice" || s.RemoteID != "9007199254740993" || s.ConnectionIdentity != p.expectedScope.Revision || s.IsPreset {
		p.t.Errorf("invalid trusted deletion snapshot: %+v", s)
	}
	if p.mutateSources && len(s.Sources) > 0 {
		s.Sources[0].SHA256 = "provider-mutated"
		if s.Sources[0].Duration != nil {
			*s.Sources[0].Duration = 99
		}
	}
	return p.deleteResult, p.deleteErr
}
func (p *lifecycleProvider) GetResourceJob(ctx context.Context, kind, handle string) (core.ResourceJob, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.check(ctx)
	p.pollCalls++
	if kind != "voice" || handle != "private-signed-delete" {
		p.t.Errorf("invalid poll identity: %s %s", kind, handle)
	}
	return p.pollResult, p.pollErr
}
func (p *lifecycleProvider) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.deleteCalls, p.pollCalls
}

type lifecycleDirectory struct {
	provider core.MediaProvider
	err      error
	scope    core.ProviderConnectionScope
	t        *testing.T
}

func (d lifecycleDirectory) Providers(context.Context) (map[string]core.MediaProvider, error) {
	return nil, errors.New("unexpected full directory lookup")
}
func (d lifecycleDirectory) Provider(ctx context.Context, id string) (core.MediaProvider, error) {
	d.t.Helper()
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if !ok || scope != d.scope || core.ProviderRevision(ctx) != scope.Revision || id != d.scope.ID {
		d.t.Errorf("directory did not receive exact pinned connection: %s %+v", id, scope)
	}
	return d.provider, d.err
}
func lifecycleFixture(t *testing.T) (context.Context, *lifecycleMemory, *lifecycleProvider, ScopeResolver, *Lifecycle) {
	t.Helper()
	owner, powner := uuid.NewString(), uuid.NewString()
	ctx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), owner), powner, powner)
	duration := 10.0
	b := Binding{ID: uuid.NewString(), OwnerID: owner, ActorID: owner, ProviderID: "kling--actual", ConnectionID: "kling--actual", ProviderOwnerID: powner, Revision: "immutable-fixture-revision", RemoteID: "9007199254740993", Kind: "voice", Subtype: "custom", Name: "Synthetic voice", Status: Succeeded, Sources: []core.ResourceSourceEvidence{{ReferenceID: uuid.NewString(), MediaType: "audio", SHA256: strings.Repeat("a", 64), Duration: &duration}}}
	repo := &lifecycleMemory{binding: b, jobs: map[string]Job{}, keys: map[string]string{}}
	scope := core.ProviderConnectionScope{ID: b.ConnectionID, OwnerID: powner, Revision: b.Revision}
	current := func(context.Context, string) (core.ProviderConnectionScope, error) { return scope, nil }
	provider := &lifecycleProvider{expectedScope: scope, owner: owner, t: t, deleteResult: core.ResourceJob{Handle: "private-signed-delete", Status: core.ProviderQueued, Warning: "private-provider-body-or-token"}, pollResult: core.ResourceJob{Handle: "private-signed-delete", Status: core.ProviderRunning, Warning: "private-provider-body-or-token"}}
	directory := lifecycleDirectory{provider: provider, scope: scope, t: t}
	return ctx, repo, provider, current, NewLifecycle(repo, current, directory)
}

func TestLifecycleDeleteIsAtomicScopedAndNeverResubmitsPending(t *testing.T) {
	ctx, repo, provider, _, l := lifecycleFixture(t)
	provider.mutateSources = true
	job, err := l.Delete(ctx, repo.binding.ID, "delete-once")
	if err != nil || job.Status != Running || job.Handle != "private-signed-delete" || !strings.Contains(job.Warning, "pending") {
		t.Fatal(job, err)
	}
	binding, _ := repo.GetBinding(ctx, repo.binding.ID)
	if binding.Status != Deleting || binding.Sources[0].SHA256 != strings.Repeat("a", 64) || *binding.Sources[0].Duration != 10 {
		t.Fatal(binding)
	}
	for i := 0; i < 3; i++ {
		replay, err := l.Delete(ctx, binding.ID, "delete-once")
		if err != nil || replay.ID != job.ID || replay.Status != Running {
			t.Fatal(replay, err)
		}
	}
	if _, err := l.Delete(ctx, binding.ID, "different-pending-key"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	polled, err := l.Poll(ctx, job.ID)
	if err != nil || polled.Status != Running || !strings.Contains(polled.Warning, "polling route") {
		t.Fatal(polled, err)
	}
	d, p := provider.counts()
	if d != 1 || p != 1 {
		t.Fatal(d, p)
	}
	encoded, _ := json.Marshal(polled)
	for _, private := range []string{"private-signed-delete", "private-provider-body-or-token", repo.binding.RemoteID, repo.binding.Revision, repo.binding.Sources[0].SHA256} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("public job exposed private evidence: %s", private)
		}
	}
}
func TestLifecycleDeleteCompletesOnlyOnTerminalProviderSuccess(t *testing.T) {
	ctx, repo, provider, _, l := lifecycleFixture(t)
	job, err := l.Delete(ctx, repo.binding.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	provider.pollResult = core.ResourceJob{Handle: "private-signed-delete", Status: core.ProviderSucceeded}
	done, err := l.Poll(ctx, job.ID)
	if err != nil || done.Status != Succeeded {
		t.Fatal(done, err)
	}
	binding, _ := repo.GetBinding(ctx, repo.binding.ID)
	if binding.Status != Deleted || binding.DeletedAt == nil {
		t.Fatal(binding)
	}
	again, err := l.Delete(ctx, binding.ID, "")
	if err != nil || again.ID != job.ID || again.Status != Succeeded {
		t.Fatal(again, err)
	}
	if _, err := l.Poll(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	d, p := provider.counts()
	if d != 1 || p != 1 {
		t.Fatal(d, p)
	}
}
func TestLifecycleDeleteAmbiguousOutcomeStaysRunningWithoutResubmit(t *testing.T) {
	ctx, repo, provider, _, l := lifecycleFixture(t)
	provider.deleteErr = errors.New("request timed out with private-token")
	job, err := l.Delete(ctx, repo.binding.ID, "uncertain")
	if err != nil || job.Status != Running || job.Handle != "" || !strings.Contains(job.Warning, "uncertain") {
		t.Fatal(job, err)
	}
	if _, err := l.Delete(ctx, repo.binding.ID, "uncertain"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Poll(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Delete(ctx, repo.binding.ID, "other"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	d, p := provider.counts()
	if d != 1 || p != 0 {
		t.Fatal(d, p)
	}
	binding, _ := repo.GetBinding(ctx, repo.binding.ID)
	if binding.Status != Deleting {
		t.Fatal(binding)
	}
	_, _, uncertain, failed, completed := repo.counts()
	if uncertain < 1 || failed != 0 || completed != 0 {
		t.Fatal(uncertain, failed, completed)
	}
}
func TestLifecycleKnownPreIOFailureAllowsOnlyExplicitNewKeyRetry(t *testing.T) {
	ctx, repo, provider, _, l := lifecycleFixture(t)
	provider.deleteErr = core.ErrOptionsInvalid
	failed, err := l.Delete(ctx, repo.binding.ID, "known-failure")
	if err != nil || failed.Status != Failed || failed.SafeError == "" {
		t.Fatal(failed, err)
	}
	if _, err := l.Delete(ctx, repo.binding.ID, "known-failure"); err != nil {
		t.Fatal(err)
	}
	d, _ := provider.counts()
	if d != 1 {
		t.Fatal("failed replay submitted again", d)
	}
	provider.deleteErr = nil
	provider.deleteResult = core.ResourceJob{Handle: "private-signed-delete", Status: core.ProviderSucceeded}
	done, err := l.Delete(ctx, repo.binding.ID, "explicit-new-action")
	if err != nil || done.Status != Succeeded || done.ID == failed.ID {
		t.Fatal(done, err)
	}
	d, _ = provider.counts()
	if d != 2 {
		t.Fatal(d)
	}
}
func TestLifecyclePollReadFailureCannotDeclareDeletionFailed(t *testing.T) {
	ctx, repo, provider, _, l := lifecycleFixture(t)
	job, err := l.Delete(ctx, repo.binding.ID, "pending")
	if err != nil {
		t.Fatal(err)
	}
	provider.pollErr = core.ErrOptionsInvalid
	state, err := l.Poll(ctx, job.ID)
	if err != nil || state.Status != Running || !strings.Contains(state.Warning, "uncertain") {
		t.Fatal(state, err)
	}
	binding, _ := repo.GetBinding(ctx, repo.binding.ID)
	if binding.Status != Deleting {
		t.Fatal(binding)
	}
	_, _, _, failed, completed := repo.counts()
	if failed != 0 || completed != 0 {
		t.Fatal(failed, completed)
	}
}
func TestLifecyclePresetBusyAndForeignScopeAreBlockedBeforeProviderIO(t *testing.T) {
	for _, mutation := range []string{"preset", "in-use", "data-owner", "provider-owner", "connection", "revision", "unsupported-kind"} {
		t.Run(mutation, func(t *testing.T) {
			ctx, repo, provider, current, l := lifecycleFixture(t)
			switch mutation {
			case "preset":
				repo.binding.IsPreset = true
			case "in-use":
				repo.busy = true
			case "data-owner":
				repo.binding.OwnerID = uuid.NewString()
			case "provider-owner":
				repo.binding.ProviderOwnerID = uuid.NewString()
			case "connection":
				repo.binding.ConnectionID = "same-key-other-connection"
			case "revision":
				repo.binding.Revision = "rotated"
			case "unsupported-kind":
				repo.binding.Kind = "face_session"
			}
			_, err := l.Delete(ctx, repo.binding.ID, "blocked")
			if err == nil {
				t.Fatal("invalid deletion admitted")
			}
			if mutation == "in-use" && !errors.Is(err, ErrInUse) {
				t.Fatal(err)
			}
			d, p := provider.counts()
			if d != 0 || p != 0 {
				t.Fatal(d, p)
			}
			_ = current
		})
	}
}
func TestLifecyclePollRejectsChangedOwnerConnectionAndRevision(t *testing.T) {
	ctx, repo, provider, _, l := lifecycleFixture(t)
	job, err := l.Delete(ctx, repo.binding.ID, "pending")
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"owner", "connection", "revision"} {
		var scope = provider.expectedScope
		if mutation == "connection" {
			scope.ID = "same-key-other-connection"
		}
		if mutation == "revision" {
			scope.Revision = "rotated"
		}
		l.current = func(context.Context, string) (core.ProviderConnectionScope, error) { return scope, nil }
		requestCtx := ctx
		if mutation == "owner" {
			requestCtx = identity.WithOwnerID(ctx, uuid.NewString())
		}
		if _, err := l.Poll(requestCtx, job.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal(mutation, err)
		}
	}
	d, p := provider.counts()
	if d != 1 || p != 0 {
		t.Fatal(d, p)
	}
}
func TestLifecycleStartGateSurvivesConcurrentRequestsAndControllerRestart(t *testing.T) {
	ctx, repo, provider, current, l := lifecycleFixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := l.Delete(ctx, repo.binding.ID, "same-request"); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	d, p := provider.counts()
	if d != 1 || p != 0 {
		t.Fatal(d, p)
	}
	restarted := NewLifecycle(repo, current, l.directory)
	var jobID string
	repo.mu.Lock()
	for id := range repo.jobs {
		jobID = id
	}
	repo.mu.Unlock()
	if _, err := restarted.Poll(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	d, p = provider.counts()
	if d != 1 || p != 1 {
		t.Fatal(d, p)
	}
}
func TestLifecycleQueuedReplayMayStartButPollNeverSubmits(t *testing.T) {
	ctx, repo, provider, _, l := lifecycleFixture(t)
	queued, _, err := repo.BeginDelete(ctx, repo.binding.ID, "reserved-before-crash")
	if err != nil {
		t.Fatal(err)
	}
	state, err := l.Poll(ctx, queued.ID)
	if err != nil || state.Status != Queued {
		t.Fatal(state, err)
	}
	d, p := provider.counts()
	if d != 0 || p != 0 {
		t.Fatal(d, p)
	}
	started, err := l.Delete(ctx, repo.binding.ID, "reserved-before-crash")
	if err != nil || started.Status != Running {
		t.Fatal(started, err)
	}
	d, p = provider.counts()
	if d != 1 || p != 0 {
		t.Fatal(d, p)
	}
}
func TestLifecycleLostHandlePersistenceRemainsUncertain(t *testing.T) {
	ctx, repo, provider, _, l := lifecycleFixture(t)
	repo.saveError = errors.New("synthetic database write failure")
	job, err := l.Delete(ctx, repo.binding.ID, "save-failure")
	if err == nil || job.Status != Running || job.Handle != "" || !strings.Contains(job.Warning, "uncertain") {
		t.Fatal(job, err)
	}
	if _, err := l.Delete(ctx, repo.binding.ID, "save-failure"); err != nil {
		t.Fatal(err)
	}
	d, p := provider.counts()
	if d != 1 || p != 0 {
		t.Fatal(d, p)
	}
}

func TestLifecycleExplicitProviderFailureDoesNotBecomeUnknownOrSuccessful(t *testing.T) {
	ctx, repo, provider, _, l := lifecycleFixture(t)
	provider.deleteResult = core.ResourceJob{Handle: "private-signed-delete", Status: core.ProviderFailed, Error: "private-token-and-upstream-detail"}
	job, err := l.Delete(ctx, repo.binding.ID, "terminal-failure")
	if err != nil || job.Status != Failed || job.SafeError == "" || strings.Contains(job.SafeError, "private-token") {
		t.Fatal(job, err)
	}
	if _, err := l.Delete(ctx, repo.binding.ID, "terminal-failure"); err != nil {
		t.Fatal(err)
	}
	d, p := provider.counts()
	if d != 1 || p != 0 {
		t.Fatal(d, p)
	}
	_, _, uncertain, failed, completed := repo.counts()
	if uncertain != 0 || failed != 1 || completed != 0 {
		t.Fatal(uncertain, failed, completed)
	}
}
