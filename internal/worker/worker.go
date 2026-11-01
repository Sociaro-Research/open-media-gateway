package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
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

	if task.Status == core.TaskRunning {
		if receiptRepo, ok := w.repo.(core.AcceptedReceiptRepository); ok {
			receipt, err := receiptRepo.LoadAcceptedReceipt(ctx, task)
			if err != nil {
				return err
			}
			if receipt != nil {
				return w.resumeAcceptedResult(ctx, provider, task, *receipt)
			}
		}
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
			TaskID: task.ID, Prompt: task.Prompt, Model: task.Model, Options: task.Options.ProviderOptions(), ProviderIdentity: task.ProviderIdentity,
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
		if hasImmediateResult(providerTask) {
			return w.acceptImmediateResult(ctx, provider, task, providerTask)
		}
		persist, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err = w.repo.SetProviderTaskID(persist, id, providerTaskID)
		stop()
		if err != nil {
			return err
		}
		if err := w.storeUsage(ctx, provider, task, providerTask.Usage); err != nil {
			return err
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
		if len(result.Artifacts) > 0 {
			return w.archiveArtifacts(ctx, taskCtx, provider, task, result.ResultURL, result.LastFrameURL, result.Artifacts, result.Resources)
		}
		if len(result.Resources) > 0 && result.ResultURL == "" {
			return w.repo.MarkSucceeded(ctx, task.ID, core.StoredMedia{Resources: result.Resources}, "")
		}
		if strings.TrimSpace(result.ResultURL) == "" {
			return w.repo.MarkFailed(ctx, id, "provider completed without a media URL")
		}
		return w.archiveResult(ctx, taskCtx, task, result.ResultURL, result.LastFrameURL, result.Resources)
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

func (w *Worker) archiveResult(ctx, archiveCtx context.Context, task core.Task, resultURL, lastFrameURL string, resources ...[]core.ProviderResource) error {
	return w.archiveResults(ctx, archiveCtx, task, resultURL, lastFrameURL, nil, nil, firstResources(resources))
}

func (w *Worker) archiveArtifacts(ctx, archiveCtx context.Context, provider core.MediaProvider, task core.Task, resultURL, lastFrameURL string, artifacts []core.ProviderArtifact, resources ...[]core.ProviderResource) error {
	if len(artifacts) == 0 || len(artifacts) > 64 {
		return w.repo.MarkFailed(ctx, task.ID, "provider returned an invalid artifact count")
	}
	for _, a := range artifacts {
		if a.MediaType != "image" && a.MediaType != "video" && a.MediaType != "audio" && a.MediaType != "speech" && a.MediaType != "music" && a.MediaType != "text" && a.MediaType != "json" && a.MediaType != "binary" {
			return w.repo.MarkFailed(ctx, task.ID, "provider returned an unknown artifact media type")
		}
		if a.MediaType == "binary" && (a.SourceReference == "" || a.URL != "" || a.Text != "") {
			return w.repo.MarkFailed(ctx, task.ID, "binary artifacts require a private provider source reference")
		}
		sources := 0
		for _, value := range []string{a.URL, a.SourceReference, a.Text} {
			if value != "" {
				sources++
			}
		}
		if sources != 1 || (a.Text != "" && a.MediaType != "text" && a.MediaType != "json") || len(a.ID) > 1024 || len(a.GroupID) > 256 || len(a.Format) > 64 || a.Duration < 0 || math.IsNaN(a.Duration) || math.IsInf(a.Duration, 0) {
			return w.repo.MarkFailed(ctx, task.ID, "provider returned malformed artifact metadata")
		}
	}
	if resultURL != "" && resultURL != artifacts[0].URL {
		return w.repo.MarkFailed(ctx, task.ID, "provider primary artifact does not match its scalar result")
	}
	return w.archiveResults(ctx, archiveCtx, task, artifacts[0].URL, lastFrameURL, append([]core.ProviderArtifact(nil), artifacts...), provider, firstResources(resources))
}

func artifactMatchesMedia(a core.ProviderArtifact, m core.StoredMedia) bool {
	kind := a.MediaType
	if kind == "binary" {
		return m.ContentType == "application/octet-stream"
	}
	if kind == "json" {
		return m.ContentType == "application/json"
	}
	if kind == "text" {
		return strings.SplitN(m.ContentType, ";", 2)[0] == "text/plain"
	}
	if kind == "speech" || kind == "music" {
		kind = "audio"
	}
	return strings.HasPrefix(m.ContentType, kind+"/")
}

func storedArtifact(a core.ProviderArtifact, m core.StoredMedia, index int) core.StoredArtifact {
	return core.StoredArtifact{ID: strconv.Itoa(index + 1), ProviderID: a.ID, MediaType: a.MediaType, Format: a.Format, Duration: a.Duration, GroupID: a.GroupID, StorageKey: m.StorageKey, ContentType: m.ContentType, SizeBytes: m.SizeBytes, SHA256: m.SHA256}
}

func (w *Worker) archiveResults(ctx, archiveCtx context.Context, task core.Task, resultURL, lastFrameURL string, artifacts []core.ProviderArtifact, provider core.MediaProvider, resources []core.ProviderResource) error {
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
	progress, err := w.loadArchiveProgress(ctx, task)
	if err != nil {
		return err
	}
	// Database fencing cannot undo a late filesystem rename or remote upload.
	// Give each delivery its own object paths so an expired/cancelled worker
	// cannot overwrite artifacts already committed by its successor.
	archiveID := task.ID
	if token := core.ClaimToken(ctx); token != "" {
		archiveID += "/attempts/" + token
	}
	var media core.StoredMedia
	var archiveErr error
	var restored bool
	media, restored, archiveErr = progress.restored(archiveCtx, w.storage, 0, false)
	if archiveErr != nil {
		return archiveErr
	}
	if restored {
		// Reuse a verified, previously committed prefix without another fetch.
	} else if task.Options.OverrideSoundID != "" {
		if len(artifacts) > 0 && artifacts[0].URL == "" {
			return w.repo.MarkFailed(ctx, task.ID, "Override Sound requires an archived media URL")
		}
		processor, ok := w.storage.(interface {
			ArchiveOverride(context.Context, storage.Store, string, string, core.SoundOverride) (core.StoredMedia, error)
		})
		target, targetOK := destination.(storage.Store)
		if !ok || !targetOK || task.Options.OverrideSound == nil || task.MediaType != "video" {
			return w.repo.MarkFailed(ctx, task.ID, "Override Sound processing is not available")
		}
		media, archiveErr = processor.ArchiveOverride(archiveCtx, target, archiveID, resultURL, *task.Options.OverrideSound)
	} else {
		if len(artifacts) > 0 {
			media, archiveErr = archiveProviderArtifact(archiveCtx, destination, provider, archiveID, artifacts[0])
		} else {
			media, archiveErr = destination.Archive(archiveCtx, archiveID, resultURL)
		}
	}
	if archiveErr != nil {
		if archiveCtx.Err() != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return w.repo.MarkFailed(context.WithoutCancel(ctx), task.ID, "store provider result: "+archiveErr.Error())
	}

	primary := core.ProviderArtifact{URL: resultURL, MediaType: task.MediaType}
	if len(artifacts) > 0 {
		primary = artifacts[0]
	}
	if len(artifacts) > 0 && !artifactMatchesMedia(artifacts[0], media) {
		return w.repo.MarkFailed(ctx, task.ID, "stored primary artifact does not match its declared media type")
	}
	if err := progress.record(ctx, storedArtifact(primary, media, 0), 0, false); err != nil {
		return err
	}

	if len(artifacts) > 0 {
		if !artifactMatchesMedia(artifacts[0], media) {
			return w.repo.MarkFailed(ctx, task.ID, "stored primary artifact does not match its declared media type")
		}
		media.Artifacts = []core.StoredArtifact{storedArtifact(artifacts[0], media, 0)}
		for i, a := range artifacts[1:] {
			artifactID := archiveID + "/artifacts/" + strconv.Itoa(i+2)
			var item core.StoredMedia
			var err error
			var restored bool
			item, restored, err = progress.restored(archiveCtx, w.storage, i+1, false)
			if err != nil {
				return err
			}
			if restored {
				// The receipt owns this immutable completed artifact.
			} else if task.Options.OverrideSoundID != "" && a.MediaType == "video" {
				processor, ok := w.storage.(interface {
					ArchiveOverride(context.Context, storage.Store, string, string, core.SoundOverride) (core.StoredMedia, error)
				})
				target, targetOK := destination.(storage.Store)
				if !ok || !targetOK || task.Options.OverrideSound == nil {
					return w.repo.MarkFailed(ctx, task.ID, "Override Sound processing is not available")
				}
				item, err = processor.ArchiveOverride(archiveCtx, target, artifactID, a.URL, *task.Options.OverrideSound)
			} else {
				item, err = archiveProviderArtifact(archiveCtx, destination, provider, artifactID, a)
			}
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return w.repo.MarkFailed(context.WithoutCancel(ctx), task.ID, fmt.Sprintf("store provider artifact %d: %s", i+2, err.Error()))
			}
			if !artifactMatchesMedia(a, item) {
				return w.repo.MarkFailed(ctx, task.ID, "stored artifact does not match its declared media type")
			}
			archived := storedArtifact(a, item, i+1)
			if err := progress.record(ctx, archived, i+1, false); err != nil {
				return err
			}
			media.Artifacts = append(media.Artifacts, archived)
		}
	}
	if lastFrameURL != "" {
		frame, restored, err := progress.restored(archiveCtx, w.storage, 0, true)
		if err != nil {
			return err
		}
		if !restored {
			frame, err = destination.Archive(archiveCtx, archiveID+"/last-frame", lastFrameURL)
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// The primary video is already safely archived. A missing optional
			// frame must not turn that completed result into a provider failure.
			slog.Warn("optional last frame could not be archived; primary result preserved")
			lastFrameURL = ""
		} else {
			frameArtifact := storedArtifact(core.ProviderArtifact{MediaType: "image"}, frame, 64)
			frameArtifact.ID = "last-frame"
			if err := progress.record(ctx, frameArtifact, 0, true); err != nil {
				return err
			}
			media.LastFrameStorageKey = frame.StorageKey
			lastFrameURL = "/v1/generations/" + task.ID + "/last-frame"
		}
	}
	media.Resources = resources
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

// Only the resolved adapter can turn an opaque authenticated reference into bytes.
// Client request URLs never enter this path and the ordinary HTTP policy stays intact.
func archiveProviderArtifact(ctx context.Context, destination ResultStorage, provider core.MediaProvider, id string, a core.ProviderArtifact) (core.StoredMedia, error) {
	if a.URL != "" {
		return destination.Archive(ctx, id, a.URL)
	}
	archiver, ok := destination.(interface {
		ArchiveSource(context.Context, string, core.ArtifactSource) (core.StoredMedia, error)
	})
	if !ok {
		return core.StoredMedia{}, errors.New("trusted artifact streams are not supported by result storage")
	}
	var source core.ArtifactSource
	if a.SourceReference != "" {
		opener, ok := provider.(core.ArtifactOpener)
		if !ok {
			return core.StoredMedia{}, errors.New("provider does not support authenticated artifacts")
		}
		var err error
		source, err = opener.OpenArtifact(ctx, a.SourceReference)
		if err != nil {
			if source.Reader != nil {
				_ = source.Reader.Close()
			}
			return core.StoredMedia{}, errors.New("provider artifact could not be opened")
		}
	} else {
		kind := "text/plain"
		if a.MediaType == "json" {
			kind = "application/json"
		}
		source = core.ArtifactSource{Reader: io.NopCloser(strings.NewReader(a.Text)), ContentType: kind, SizeBytes: int64(len(a.Text))}
	}
	return archiver.ArchiveSource(ctx, id, source)
}

func firstResources(values [][]core.ProviderResource) []core.ProviderResource {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}
