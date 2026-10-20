package core

import (
	"context"
	"errors"
)

var ErrTaskActive = errors.New("result files cannot be deleted while generation is running")

func (s *TaskService) DeleteResult(ctx context.Context, id string, remove func(context.Context, string, string) error) error {
	task, e := s.repo.Get(ctx, id)
	if e != nil {
		return e
	}
	if task.Status != TaskSucceeded && task.Status != TaskFailed {
		return ErrTaskActive
	}
	r, ok := s.repo.(interface {
		StoredResultKeys(context.Context, string) ([]string, error)
		MarkResultDeleted(context.Context, string) error
	})
	if !ok {
		if task.ResultDeletedAt != nil {
			return nil
		}
		return errors.New("result deletion unavailable")
	}
	keys, e := r.StoredResultKeys(ctx, id)
	if e != nil {
		return e
	}
	// A losing archive can finish after an earlier successful deletion. Revisit
	// live indexed objects, while the repository omits already-deleted task keys.
	if task.ResultDeletedAt != nil && len(keys) == 0 {
		return nil
	}
	for _, key := range keys {
		if e = remove(ctx, id, key); e != nil {
			return e
		}
	}
	return r.MarkResultDeleted(ctx, id)
}
