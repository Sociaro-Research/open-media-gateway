package core

import (
	"context"
	"errors"
	"time"
)

// GenerationPosition uses the database's complete ordering precision. It is a
// position, not a task lookup, so deletion or reassignment cannot break a walk.
type GenerationPosition struct {
	CreatedAt time.Time
	ID        string
}

type GenerationPageQuery struct {
	ProjectID    string
	Before       *GenerationPosition
	Through      *GenerationPosition
	Limit        int
	IDs          []string
	VersionsOnly bool
	SkipTotal    bool
}

type GenerationRecordVersion struct {
	GenerationPosition
	Revision int64
}

type GenerationPage struct {
	Tasks    []Task
	Versions []GenerationRecordVersion
	Total    int64
	HasMore  bool
}

type GenerationPageReader interface {
	ListGenerationPage(context.Context, GenerationPageQuery) (GenerationPage, error)
}

func (s *TaskService) ListGenerationPage(ctx context.Context, q GenerationPageQuery) (GenerationPage, error) {
	if r, ok := s.repo.(GenerationPageReader); ok {
		return r.ListGenerationPage(ctx, q)
	}
	return GenerationPage{}, errors.New("generation cursor pagination is unavailable")
}
