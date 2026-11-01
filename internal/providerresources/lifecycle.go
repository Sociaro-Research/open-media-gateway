package providerresources

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

const pendingDeletionWarning = "Provider deletion remains pending. A provider without a documented deletion-task polling route cannot confirm completion from submission alone."
const uncertainDeletionWarning = "The provider outcome is uncertain. Check this job before submitting another deletion; this job will not be resubmitted."

// Lifecycle orchestrates already-authorized deletion jobs. Paid resource
// creation remains a generation operation admitted and metered by TaskService.
type Lifecycle struct {
	repo      Repository
	current   ScopeResolver
	directory core.ProviderDirectory
}

func NewLifecycle(repo Repository, current ScopeResolver, directory core.ProviderDirectory) *Lifecycle {
	return &Lifecycle{repo: repo, current: current, directory: directory}
}
func (l *Lifecycle) bindingScope(ctx context.Context, b Binding) (core.ProviderConnectionScope, error) {
	if l == nil || l.repo == nil || l.current == nil {
		return core.ProviderConnectionScope{}, ErrInvalid
	}
	if b.OwnerID != identity.OwnerID(ctx) || b.ProviderOwnerID != identity.ProviderOwnerID(ctx) || b.ProviderID == "" || b.ConnectionID == "" || b.Revision == "" || !ValidRemoteID(b.RemoteID) {
		return core.ProviderConnectionScope{}, ErrNotFound
	}
	if filter := identity.ContentAuthorFilter(ctx); filter != "" && b.ActorID != filter {
		return core.ProviderConnectionScope{}, ErrNotFound
	}
	scope, err := l.current(ctx, b.ProviderID)
	if err != nil {
		return scope, err
	}
	if scope.ID != b.ConnectionID || scope.OwnerID != b.ProviderOwnerID || scope.Revision != b.Revision {
		return core.ProviderConnectionScope{}, ErrNotFound
	}
	return scope, nil
}
func localSessionRevocation(kind string) bool {
	return kind == "face_session" || kind == "editing_session"
}

func lifecycleJobMatches(ctx context.Context, j Job, b Binding) bool {
	return j.Operation == Delete && j.BindingID == b.ID && j.OwnerID == identity.OwnerID(ctx) && j.OwnerID == b.OwnerID && j.ProviderOwnerID == identity.ProviderOwnerID(ctx) && j.ProviderOwnerID == b.ProviderOwnerID && j.ProviderID == b.ProviderID && j.ConnectionID == b.ConnectionID && j.Revision == b.Revision && j.Kind == b.Kind && j.Intent.Kind == b.Kind && (identity.ContentAuthorFilter(ctx) == "" || j.ActorID == identity.ContentAuthorFilter(ctx))
}
func lifecyclePublicJob(j Job) Job {
	uncertain := strings.HasPrefix(j.Warning, "The provider outcome is uncertain.")
	j.Intent = CloneIntent(j.Intent)
	j.SafeError = SafeFailure(j.SafeError)
	if j.Operation == Delete && localSessionRevocation(j.Kind) {
		switch j.Status {
		case Succeeded:
			j.Warning = "Local session binding revoked. Provider session expiry is unchanged."
		case Queued, Running:
			j.Warning = "Local session revocation is pending. No provider deletion is performed."
		case Failed:
			j.Warning = "Local session revocation failed. Provider session expiry is unchanged."
		}
		return j
	}
	if j.Operation == Delete && j.Status == Queued && j.Handle == "" {
		j.Warning = "Deletion has not been submitted. Repeat the original request to resume this queued job."
		return j
	}
	if j.Warning != "" {
		j.Warning = SafeWarning(j.Warning)
		if j.Operation == Delete && j.Status == Running {
			j.Warning = pendingDeletionWarning
			if j.Handle == "" || uncertain {
				j.Warning = uncertainDeletionWarning
			}
		}
	}
	return j
}
func (l *Lifecycle) refresh(ctx context.Context, id string) (Job, error) {
	j, err := l.repo.GetJob(ctx, id)
	return lifecyclePublicJob(j), err
}
func lifecycleProviderContext(ctx context.Context, j Job, scope core.ProviderConnectionScope) context.Context {
	return core.WithProviderConnectionScope(core.WithProviderRevision(ctx, j.Revision), scope)
}
func (l *Lifecycle) provider(ctx context.Context, j Job, b Binding) (context.Context, core.ResourceProvider, error) {
	scope, err := l.bindingScope(ctx, b)
	if err != nil {
		return ctx, nil, err
	}
	if !lifecycleJobMatches(ctx, j, b) {
		return ctx, nil, ErrNotFound
	}
	if l.directory == nil {
		return ctx, nil, core.ErrProviderUnavailable
	}
	pinned := lifecycleProviderContext(ctx, j, scope)
	provider, err := l.directory.Provider(pinned, j.ProviderID)
	if err != nil {
		return pinned, nil, err
	}
	resource, ok := provider.(core.ResourceProvider)
	if !ok {
		return pinned, nil, core.ErrProviderUnavailable
	}
	return pinned, resource, nil
}
func (l *Lifecycle) uncertain(ctx context.Context, j Job) (Job, error) {
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := l.repo.RecordUncertain(persist, j.ID, uncertainDeletionWarning); err != nil {
		return lifecyclePublicJob(j), err
	}
	return l.refresh(persist, j.ID)
}
func (l *Lifecycle) failed(ctx context.Context, j Job) (Job, error) {
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := l.repo.FailJob(persist, j.ID, "Provider resource deletion failed.", ""); err != nil {
		return lifecyclePublicJob(j), err
	}
	return l.refresh(persist, j.ID)
}
func (l *Lifecycle) apply(ctx context.Context, j Job, result core.ResourceJob) (Job, error) {
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if result.Handle != "" {
		if err := l.repo.SaveHandle(persist, j.ID, result.Handle, result.Warning); err != nil {
			pending, saveErr := l.uncertain(persist, j)
			if saveErr != nil {
				return pending, errors.Join(err, saveErr)
			}
			return pending, err
		}
		j.Handle = result.Handle
	}
	switch result.Status {
	case core.ProviderSucceeded:
		if err := l.repo.CompleteDelete(persist, j.ID); err != nil {
			return lifecyclePublicJob(j), err
		}
	case core.ProviderFailed:
		return l.failed(persist, j)
	case core.ProviderQueued, core.ProviderRunning:
		if result.Handle == "" && j.Handle == "" {
			return l.uncertain(persist, j)
		}
	default:
		return l.uncertain(persist, j)
	}
	return l.refresh(persist, j.ID)
}

// revokeSession completes only the local binding lifecycle. Native session
// identities, source evidence and expiry remain untouched in the tombstone.
// A missing native task handle is expected because this path has no provider I/O.
func (l *Lifecycle) revokeSession(ctx context.Context, job Job) (Job, error) {
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	b, err := l.repo.GetBinding(persist, job.BindingID)
	if err != nil {
		return lifecyclePublicJob(job), err
	}
	// A concurrent caller may already have completed the same local job after
	// our initial queued/running read. Refresh after the binding so a deleted
	// tombstone is paired with its current terminal job, not stale pending state.
	currentJob, err := l.repo.GetJob(persist, job.ID)
	if err != nil {
		return lifecyclePublicJob(job), err
	}
	job = currentJob
	if !localSessionRevocation(b.Kind) || !lifecycleJobMatches(persist, job, b) {
		return Job{}, ErrNotFound
	}
	if _, err = l.bindingScope(persist, b); err != nil {
		return lifecyclePublicJob(job), err
	}
	if b.IsPreset {
		return lifecyclePublicJob(job), ErrConflict
	}
	if job.Status == Succeeded || job.Status == Failed {
		return lifecyclePublicJob(job), nil
	}
	if b.Status != Deleting {
		return lifecyclePublicJob(job), ErrConflict
	}
	if job.Status == Queued {
		job, _, err = l.repo.StartJob(persist, job.ID)
		if err != nil {
			return lifecyclePublicJob(job), err
		}
		if !lifecycleJobMatches(persist, job, b) {
			return Job{}, ErrNotFound
		}
	}
	if job.Status == Succeeded || job.Status == Failed {
		return lifecyclePublicJob(job), nil
	}
	if job.Status != Running {
		return lifecyclePublicJob(job), ErrConflict
	}
	if err = l.repo.CompleteDelete(persist, job.ID); err != nil {
		return lifecyclePublicJob(job), err
	}
	return l.refresh(persist, job.ID)
}

func lifecycleDeletionSnapshot(b Binding) core.ResourceSnapshot {
	return core.ResourceSnapshot{Session: CloneSession(b.Session), BindingID: b.ID, ReferenceID: "resource", Kind: b.Kind, RemoteID: b.RemoteID, IsPreset: b.IsPreset, Subtype: b.Subtype, ConnectionIdentity: b.Revision, Sources: CloneSources(b.Sources)}
}

// Preflight is pure adapter validation under the exact persisted connection.
// It runs before reservation only for ready/failed remote bindings; accepted
// pending/completed deletions keep their original replay semantics.
func (l *Lifecycle) deletionPreflight(ctx context.Context, b Binding) error {
	scope, err := l.bindingScope(ctx, b)
	if err != nil {
		return err
	}
	pinned := core.WithProviderConnectionScope(core.WithProviderRevision(ctx, b.Revision), scope)
	provider, err := l.directory.Provider(pinned, b.ProviderID)
	if err != nil {
		return err
	}
	if validator, ok := provider.(core.ResourceDeletionValidator); ok {
		return validator.ValidateResourceDeletion(pinned, lifecycleDeletionSnapshot(b))
	}
	return nil
}

func (l *Lifecycle) Delete(ctx context.Context, bindingID, idem string) (Job, error) {
	if l == nil || l.repo == nil || l.current == nil {
		return Job{}, ErrInvalid
	}
	if _, err := uuid.Parse(bindingID); err != nil {
		return Job{}, ErrInvalid
	}
	b, err := l.repo.GetBinding(ctx, bindingID)
	if err != nil {
		return Job{}, err
	}
	if _, err = l.bindingScope(ctx, b); err != nil {
		return Job{}, err
	}
	if b.IsPreset {
		return Job{}, ErrConflict
	}
	if b.Kind != "element" && b.Kind != "voice" && b.Kind != "file_resource" && !localSessionRevocation(b.Kind) {
		return Job{}, ErrInvalid
	}
	if !localSessionRevocation(b.Kind) && l.directory == nil {
		return Job{}, ErrInvalid
	}
	switch b.Status {
	case Succeeded, Failed, Deleting, Deleted:
	default:
		return Job{}, ErrConflict
	}
	if idem == "" {
		idem = b.ID
	}
	if len(idem) > 200 {
		return Job{}, ErrInvalid
	}
	if !localSessionRevocation(b.Kind) && (b.Status == Succeeded || b.Status == Failed) {
		if err := l.deletionPreflight(ctx, b); err != nil {
			return Job{}, err
		}
	}
	// BeginDelete owns in-use checks and replay-before-state-check ordering under
	// a row lock. An outstanding job cannot be replaced with another key.
	job, _, err := l.repo.BeginDelete(ctx, b.ID, idem)
	if err != nil {
		return lifecyclePublicJob(job), err
	}
	if !lifecycleJobMatches(ctx, job, b) {
		return Job{}, ErrNotFound
	}
	if localSessionRevocation(b.Kind) {
		return l.revokeSession(ctx, job)
	}
	if job.Status != Queued {
		return lifecyclePublicJob(job), nil
	}
	job, started, err := l.repo.StartJob(ctx, job.ID)
	if err != nil {
		return lifecyclePublicJob(job), err
	}
	if !lifecycleJobMatches(ctx, job, b) {
		return Job{}, ErrNotFound
	}
	if !started {
		return lifecyclePublicJob(job), nil
	}
	pinned, provider, err := l.provider(ctx, job, b)
	if err != nil {
		return l.failed(ctx, job)
	}
	snapshot := lifecycleDeletionSnapshot(b)
	result, err := provider.DeleteResource(pinned, snapshot)
	if err != nil {
		// Only adapter-declared pre-I/O rejections prove that no remote deletion was
		// attempted. A transport error after invocation always remains uncertain.
		if errors.Is(err, core.ErrOptionsInvalid) || errors.Is(err, core.ErrProviderChanged) || errors.Is(err, core.ErrProviderUnavailable) {
			return l.failed(ctx, job)
		}
		return l.uncertain(ctx, job)
	}
	return l.apply(ctx, job, result)
}
func (l *Lifecycle) Poll(ctx context.Context, jobID string) (Job, error) {
	if l == nil || l.repo == nil || l.current == nil {
		return Job{}, ErrInvalid
	}
	if _, err := uuid.Parse(jobID); err != nil {
		return Job{}, ErrInvalid
	}
	job, err := l.repo.GetJob(ctx, jobID)
	if err != nil {
		return Job{}, err
	}
	b, err := l.repo.GetBinding(ctx, job.BindingID)
	if err != nil {
		return Job{}, err
	}
	if !lifecycleJobMatches(ctx, job, b) {
		return Job{}, ErrNotFound
	}
	if _, err = l.bindingScope(ctx, b); err != nil {
		return Job{}, err
	}
	if !localSessionRevocation(b.Kind) && l.directory == nil {
		return Job{}, ErrInvalid
	}
	if job.Status == Succeeded || job.Status == Failed {
		return lifecyclePublicJob(job), nil
	}
	if localSessionRevocation(b.Kind) {
		return l.revokeSession(ctx, job)
	}
	if b.Status != Deleting {
		return Job{}, ErrConflict
	}
	if job.Status == Queued {
		return lifecyclePublicJob(job), nil
	}
	if job.Status != Running {
		return Job{}, ErrConflict
	}
	if job.Handle == "" {
		return l.uncertain(ctx, job)
	}
	pinned, provider, err := l.provider(ctx, job, b)
	if err != nil {
		return lifecyclePublicJob(job), err
	}
	result, err := provider.GetResourceJob(pinned, job.Kind, job.Handle)
	if err != nil {
		return l.uncertain(ctx, job)
	}
	// Poll is read-only at the provider boundary. It never calls DeleteResource,
	// even when the submission handle or a previous transport outcome is unknown.
	return l.apply(ctx, job, result)
}
