package core

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

type admissionIntentKey struct{}

// WithAdmissionIntent accepts only a server-computed fingerprint, never a header.
func WithAdmissionIntent(ctx context.Context, intent string) context.Context {
	return context.WithValue(ctx, admissionIntentKey{}, intent)
}
func AdmissionIntent(ctx context.Context) string {
	v, _ := ctx.Value(admissionIntentKey{}).(string)
	return v
}
func FingerprintAdmissionIntent(ctx context.Context, operation string, value any) (string, error) {
	b, e := json.Marshal(value)
	if e != nil {
		return "", e
	}
	return "v1:" + requestHash(operation, identity.OwnerID(ctx), identity.ActorID(ctx), string(b)), nil
}

type AcceptedIntentRepository interface {
	RecoverAcceptedIntent(context.Context, string, string) (Task, bool, error)
}

func (s *TaskService) RecoverAcceptedIntent(ctx context.Context, key, intent string) (Task, bool, error) {
	if key == "" || intent == "" {
		return Task{}, false, nil
	}
	if len(key) > 200 {
		return Task{}, false, ErrIdempotencyKey
	}
	repo, ok := s.repo.(AcceptedIntentRepository)
	if !ok {
		return Task{}, false, nil
	}
	task, found, err := repo.RecoverAcceptedIntent(ctx, key, intent)
	if found && err == nil {
		s.enqueueAccepted(ctx, task)
	}
	return task, found, err
}
func (s *TaskService) enqueueAccepted(ctx context.Context, task Task) {
	if task.Status == TaskQueued || task.Status == TaskRunning {
		if e := s.queue.Enqueue(ctx, task.ID); e == nil {
			_ = s.repo.MarkEnqueued(ctx, task.ID)
		}
	}
}

// HasAcceptedIntent is only a bounded-body admission hint. It does not recover a
// task or authorize execution; exact intent/current authority are checked later.
func (s *TaskService) HasAcceptedIntent(ctx context.Context, key string) (bool, error) {
	if key == "" || len(key) > 200 {
		return false, nil
	}
	r, ok := s.repo.(interface {
		HasAcceptedIntent(context.Context, string) (bool, error)
	})
	if !ok {
		return false, nil
	}
	return r.HasAcceptedIntent(ctx, key)
}
