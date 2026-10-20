package worker

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
)

type Worker struct {
	Directory            core.ProviderDirectory
	Observer             Observer
	repo                 core.TaskRepository
	queue                core.TaskQueue
	providers            map[string]core.MediaProvider
	pollInterval         time.Duration
	taskTimeout          time.Duration
	maxPollErrors        int
	dispatchInterval     time.Duration
	OperationConcurrency int
	ArchiveConcurrency   int
	ShutdownTimeout      time.Duration
	archiveSlots         chan struct{}
	storage              ResultStorage
}

// Observer receives operational counts only, without task or owner identities.
type Observer interface {
	ObserveWorkerLoop(string, error)
	WorkerClaimStarted() func(error)
}

type ResultStorage interface {
	Archive(context.Context, string, string) (core.StoredMedia, error)
}

func New(repo core.TaskRepository, queue core.TaskQueue, provider core.MediaProvider, mediaStorage ResultStorage, pollInterval, taskTimeout time.Duration, maxPollErrors int, dispatchInterval time.Duration) *Worker {
	return NewWithProviders(repo, queue, map[string]core.MediaProvider{provider.Name(): provider}, mediaStorage, pollInterval, taskTimeout, maxPollErrors, dispatchInterval)
}

func NewWithProviders(repo core.TaskRepository, queue core.TaskQueue, providers map[string]core.MediaProvider, mediaStorage ResultStorage, pollInterval, taskTimeout time.Duration, maxPollErrors int, dispatchInterval time.Duration) *Worker {
	return &Worker{
		repo: repo, queue: queue, providers: providers,
		pollInterval: pollInterval, taskTimeout: taskTimeout, maxPollErrors: maxPollErrors,
		dispatchInterval: dispatchInterval, storage: mediaStorage, ArchiveConcurrency: 4,
		OperationConcurrency: 64,
		ShutdownTimeout:      DefaultShutdownTimeout,
	}
}

func (w *Worker) process(ctx context.Context, id string) error {
	task, err := w.repo.Get(ctx, id)
	if errors.Is(err, core.ErrTaskNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if task.Status == core.TaskSucceeded || task.Status == core.TaskFailed {
		return nil
	}
	ctx = identity.WithOwnerID(ctx, task.OwnerID)
	ctx = identity.WithResourceOwners(ctx, task.ProviderOwnerID, task.StorageOwnerID)
	ctx = core.WithProviderRevision(ctx, task.ProviderIdentity)
	provider, ok := w.providers[task.Provider]
	if w.Directory != nil {
		var e error
		provider, e = w.Directory.Provider(ctx, task.Provider)
		if e != nil && !errors.Is(e, core.ErrProviderUnavailable) {
			return e
		}
		ok = e == nil
	}
	if !ok && task.Provider == "" && len(w.providers) == 1 {
		for _, onlyProvider := range w.providers {
			provider, ok = onlyProvider, true
		}
	}
	if !ok {
		return w.repo.MarkFailed(ctx, id, "provider "+task.Provider+" is not configured")
	}

	providerTaskID := ""
	if task.ProviderTaskID != nil {
		providerTaskID = *task.ProviderTaskID
	}
	if task.Status == core.TaskQueued {

		if err := w.repo.MarkRunning(ctx, id); err != nil {
			return err
		}
		providerTask, err := provider.Generate(ctx, core.MediaRequest{
			TaskID: task.ID, Prompt: task.Prompt, Model: task.Model, Options: task.Options, ProviderIdentity: task.ProviderIdentity,
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, core.ErrProviderChanged) {
				if accounting, ok := w.repo.(interface {
					ConfirmNoCharge(context.Context, string) error
				}); ok {
					if costErr := accounting.ConfirmNoCharge(ctx, id); costErr != nil {
						return costErr
					}
				}
			}
			return w.repo.MarkFailed(ctx, id, "provider submission failed: "+err.Error())
		}
		providerTaskID = providerTask.ID
		if err := w.repo.SetProviderTaskID(ctx, id, providerTaskID); err != nil {
			return err
		}
		if err := w.storeUsage(ctx, provider, task, providerTask.Usage); err != nil {
			return err
		}
		if providerTask.ResultURL != "" {
			return w.archiveResult(ctx, ctx, task, providerTask.ResultURL, providerTask.LastFrameURL)
		}
		return &pollLater{}
	} else if providerTaskID == "" {
		return w.repo.MarkFailed(ctx, id, "worker interrupted during provider submission; task was not resubmitted to avoid duplicate generation")
	}

	started := task.WorkerStartedAt
	if started == nil {
		now := time.Now()
		started = &now
	}
	deadline := started.Add(w.taskTimeout)
	if !time.Now().Before(deadline) {
		return w.repo.MarkFailed(ctx, id, "provider task timed out")
	}
	taskCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	result, err := provider.GetTask(taskCtx, providerTaskID)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if taskCtx.Err() != nil {
			return w.repo.MarkFailed(ctx, id, "provider task timed out")
		}
		n := task.WorkerPollErrors + 1
		if n >= w.maxPollErrors {
			return w.repo.MarkFailed(ctx, id, "provider polling failed: "+err.Error())
		}
		return &pollLater{errors: n}
	}
	// Pending polls without new usage do not write the task or refresh updated_at.
	if err := w.storeUsage(ctx, provider, task, result.Usage); err != nil {
		return err
	}
	switch result.Status {
	case core.ProviderSucceeded:
		if strings.TrimSpace(result.ResultURL) == "" {
			return w.repo.MarkFailed(ctx, id, "provider completed without a media URL")
		}
		return w.archiveResult(ctx, taskCtx, task, result.ResultURL, result.LastFrameURL)
	case core.ProviderFailed:
		if accounting, ok := w.repo.(interface {
			ConfirmNoCharge(context.Context, string) error
		}); ok && failureIsFree(provider) {
			if err := accounting.ConfirmNoCharge(ctx, id); err != nil {
				return err
			}
		}
		message := result.Error
		if message == "" {
			message = "provider task failed"
		}
		return w.repo.MarkFailed(ctx, id, message)
	case core.ProviderQueued, core.ProviderRunning:
		return &pollLater{}
	default:
		return w.repo.MarkFailed(ctx, id, "provider returned an invalid status")
	}
}

// A successful pending step, not a provider or processing error.
type pollLater struct{ errors int }

func (*pollLater) Error() string { return "provider task is pending" }

func (w *Worker) storeUsage(ctx context.Context, provider core.MediaProvider, task core.Task, usage *core.ProviderUsage) error {
	if usage == nil {
		return nil
	}
	if task.Cost != nil {
		copyCost := *task.Cost
		if copyCost.MeterUsage(*usage) {
			value := copyCost.MeteredUSD.Float64()
			usage.EstimatedCost = &value
			usage.Currency = "USD"
			usage.CostStatus = "estimated"
			usage.CostSource = "provider usage × accepted tariff"
		}
	}
	if task.Cost == nil && usage.EstimatedCost == nil {
		if catalog, ok := provider.(core.ModelCatalog); ok {
			if models, err := catalog.ListModels(ctx); err == nil {
				for _, model := range models {
					if model.ID != task.Model || !model.Pricing.Known {
						continue
					}
					tokens := usageMetric(usage.Metrics, "total_tokens")
					if tokens == 0 {
						tokens = usageMetric(usage.Metrics, "completion_tokens")
					}
					rate := model.Pricing.USDPerMillionByResolution[task.Options.Resolution]
					if rate == 0 {
						rate = model.Pricing.USDPerMillionTokens
					}
					if task.Options.GenerateAudio != nil && *task.Options.GenerateAudio && model.Pricing.USDPerMillionAudioTokens > 0 {
						rate = model.Pricing.USDPerMillionAudioTokens
					}
					if tokens > 0 && rate > 0 {
						cost := tokens * rate / 1_000_000
						usage.EstimatedCost = &cost
						usage.Currency = "USD"
						usage.CostStatus = "estimated"
						usage.CostSource = "provider usage × public model tariff"
					}
					break
				}
			}
		}
	}
	return w.repo.SetProviderUsage(ctx, task.ID, *usage)
}

func usageMetric(metrics []core.UsageMetric, name string) float64 {
	for _, metric := range metrics {
		if metric.Name == name {
			return metric.Quantity
		}
	}
	return 0
}

func (w *Worker) archiveResult(ctx, archiveCtx context.Context, task core.Task, resultURL, lastFrameURL string) error {
	if w.archiveSlots != nil {
		select {
		case w.archiveSlots <- struct{}{}:
			defer func() { <-w.archiveSlots }()
		case <-archiveCtx.Done():
			return archiveCtx.Err()
		}
	}
	if w.storage == nil {
		return w.repo.MarkFailed(ctx, task.ID, "result storage is not configured")
	}
	if sourceURL := storageSourceForDiagnostics(resultURL); sourceURL != "" {
		if err := w.repo.SetProviderResultURL(ctx, task.ID, sourceURL); err != nil {
			return err
		}
	}
	destination := w.storage
	if selector, ok := destination.(interface {
		ForArchive(context.Context) (storage.Store, error)
	}); ok {
		selected, err := selector.ForArchive(archiveCtx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return w.repo.MarkFailed(context.WithoutCancel(ctx), task.ID, "result storage configuration could not be loaded")
		}
		destination = selected
	}
	// Database fencing cannot undo a late filesystem rename or remote upload.
	// Give each delivery its own object paths so an expired/cancelled worker
	// cannot overwrite artifacts already committed by its successor.
	archiveID := task.ID
	if token := core.ClaimToken(ctx); token != "" {
		archiveID += "/attempts/" + token
	}
	media, archiveErr := destination.Archive(archiveCtx, archiveID, resultURL)
	if archiveErr != nil {
		if archiveCtx.Err() != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return w.repo.MarkFailed(context.WithoutCancel(ctx), task.ID, "store provider result: "+archiveErr.Error())
	}
	if lastFrameURL != "" {
		frame, err := destination.Archive(archiveCtx, archiveID+"/last-frame", lastFrameURL)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return w.repo.MarkFailed(context.WithoutCancel(ctx), task.ID, "store last frame: "+err.Error())
		}
		media.LastFrameStorageKey = frame.StorageKey
		lastFrameURL = "/v1/generations/" + task.ID + "/last-frame"
	}
	return w.repo.MarkSucceeded(ctx, task.ID, media, lastFrameURL)
}

func storageSourceForDiagnostics(resultURL string) string {
	if strings.HasPrefix(resultURL, "data:") {
		return ""
	}
	return resultURL
}

func failureIsFree(p core.MediaProvider) bool {
	v, ok := p.(interface{ FailureIsFree() bool })
	return ok && v.FailureIsFree()
}
