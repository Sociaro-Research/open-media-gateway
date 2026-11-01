package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

type acceptedReceiptContextKey struct{}

func hasImmediateResult(result core.ProviderTask) bool {
	return len(result.Artifacts) > 0 || result.ResultURL != "" || len(result.Resources) > 0
}

// A cancellation can end archival, but must not discard an already accepted
// provider response. This bounded write keeps the original live claim and all
// owner/connection context; it never renews or bypasses a lost lease.
func (w *Worker) acceptImmediateResult(ctx context.Context, provider core.MediaProvider, task core.Task, result core.ProviderTask) error {
	if repo, ok := w.repo.(core.AcceptedReceiptRepository); ok {
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := repo.SaveAcceptedReceipt(persist, task, result)
		cancel()
		if err != nil {
			return err
		}
		ctx = context.WithValue(ctx, acceptedReceiptContextKey{}, true)
	} else {
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := w.repo.SetProviderTaskID(persist, task.ID, result.ID)
		cancel()
		if err != nil {
			return err
		}
	}
	return w.resumeAcceptedResult(ctx, provider, task, result)
}

func (w *Worker) resumeAcceptedResult(ctx context.Context, provider core.MediaProvider, task core.Task, result core.ProviderTask) error {
	if validator, ok := provider.(core.AcceptedReceiptValidator); ok {
		if err := validator.ValidateAcceptedReceipt(ctx, task); err != nil {
			return err
		}
	}
	if _, ok := w.repo.(core.AcceptedReceiptRepository); ok {
		ctx = context.WithValue(ctx, acceptedReceiptContextKey{}, true)
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	err := w.storeUsage(persist, provider, task, result.Usage)
	cancel()
	if err != nil {
		return err
	}
	if len(result.Artifacts) > 0 {
		return w.archiveArtifacts(ctx, ctx, provider, task, result.ResultURL, result.LastFrameURL, result.Artifacts, result.Resources)
	}
	if result.ResultURL != "" {
		return w.archiveResult(ctx, ctx, task, result.ResultURL, result.LastFrameURL, result.Resources)
	}
	if len(result.Resources) > 0 {
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return w.repo.MarkSucceeded(finish, task.ID, core.StoredMedia{Resources: result.Resources}, "")
	}
	return errors.New("provider completed without a media result")
}

type receiptArchiveProgress struct {
	repo      core.AcceptedReceiptProgressRepository
	task      core.Task
	artifacts []core.StoredArtifact
	frame     *core.StoredArtifact
}

func (w *Worker) loadArchiveProgress(ctx context.Context, task core.Task) (*receiptArchiveProgress, error) {
	enabled, _ := ctx.Value(acceptedReceiptContextKey{}).(bool)
	if !enabled {
		return nil, nil
	}
	repo, ok := w.repo.(core.AcceptedReceiptProgressRepository)
	if !ok {
		return nil, nil
	}
	artifacts, frame, err := repo.LoadAcceptedReceiptProgress(ctx, task)
	if err != nil {
		return nil, err
	}
	return &receiptArchiveProgress{repo: repo, task: task, artifacts: artifacts, frame: frame}, nil
}

func (p *receiptArchiveProgress) restored(ctx context.Context, destination ResultStorage, index int, frame bool) (core.StoredMedia, bool, error) {
	if p == nil {
		return core.StoredMedia{}, false, nil
	}
	var a core.StoredArtifact
	if frame {
		if p.frame == nil {
			return core.StoredMedia{}, false, nil
		}
		a = *p.frame
	} else {
		if index >= len(p.artifacts) {
			return core.StoredMedia{}, false, nil
		}
		a = p.artifacts[index]
	}
	store, ok := destination.(storage.Store)
	if !ok {
		return core.StoredMedia{}, false, errors.New("receipt recovery requires readable archived storage")
	}
	if err := ctx.Err(); err != nil {
		return core.StoredMedia{}, false, err
	}
	object, err := store.Open(ctx, a.StorageKey)
	if err != nil {
		return core.StoredMedia{}, false, errors.New("accepted archive checkpoint is unavailable")
	}
	var closeOnce sync.Once
	closeReader := func() { closeOnce.Do(func() { _ = object.Reader.Close() }) }
	stopClose := context.AfterFunc(ctx, closeReader)
	defer func() { stopClose(); closeReader() }()
	if object.ContentType != a.ContentType || object.SizeBytes != a.SizeBytes {
		return core.StoredMedia{}, false, errors.New("accepted archive checkpoint metadata differs")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(object.Reader, a.SizeBytes+1))
	if ctx.Err() != nil {
		return core.StoredMedia{}, false, ctx.Err()
	}
	if err != nil || n != a.SizeBytes || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return core.StoredMedia{}, false, errors.New("accepted archive checkpoint content differs")
	}
	return core.StoredMedia{StorageKey: a.StorageKey, ContentType: a.ContentType, SizeBytes: a.SizeBytes, SHA256: a.SHA256}, true, nil
}

func (p *receiptArchiveProgress) record(ctx context.Context, a core.StoredArtifact, index int, frame bool) error {
	if p == nil {
		return nil
	}
	if frame {
		if p.frame != nil {
			return nil
		}
		p.frame = &a
	} else {
		if index < len(p.artifacts) {
			return nil
		}
		if index != len(p.artifacts) {
			return errors.New("accepted archive checkpoint is not ordered")
		}
		p.artifacts = append(p.artifacts, a)
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return p.repo.SaveAcceptedReceiptProgress(persist, p.task, p.artifacts, p.frame)
}
