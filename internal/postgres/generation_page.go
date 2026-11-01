package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

func (r *TaskRepository) ListGenerationPage(ctx context.Context, q core.GenerationPageQuery) (core.GenerationPage, error) {
	page := core.GenerationPage{Tasks: []core.Task{}, Versions: []core.GenerationRecordVersion{}}
	if q.Limit < 1 || q.Limit > 100 || len(q.IDs) > 50 {
		return page, fmt.Errorf("invalid generation page limit")
	}
	// Count and data must describe the same filtered snapshot, including an
	// empty page. Do not use count(*) OVER(): that reads the entire task payload
	// set before LIMIT and loses the total when the requested page is empty.
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	if q.ProjectID != "" && q.ProjectID != core.NoProjectFilter {
		if _, err := uuid.Parse(q.ProjectID); err != nil {
			return page, core.ErrProjectInvalid
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM generation_projects WHERE `+projectScope+` AND id=$4)`, identity.OwnerID(ctx), identity.ActorID(ctx), projectCompany(ctx), q.ProjectID).Scan(&exists); err != nil {
			return page, err
		}
		if !exists {
			return page, core.ErrProjectInvalid
		}
	}
	where, args := []string{}, []any{}
	bind := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if _, ok := identity.UserFromContext(ctx); ok || q.ProjectID != "" {
		where = append(where, "owner_id="+bind(identity.OwnerID(ctx))+"::uuid")
	}
	if author := identity.ContentAuthorFilter(ctx); author != "" {
		where = append(where, "actor_id="+bind(author)+"::uuid")
	}
	switch q.ProjectID {
	case core.NoProjectFilter:
		where = append(where, "project_id IS NULL")
	case "":
	default:
		where = append(where, "project_id="+bind(q.ProjectID)+"::uuid")
	}
	clause := func() string {
		if len(where) == 0 {
			return ""
		}
		return " WHERE " + strings.Join(where, " AND ")
	}
	if !q.SkipTotal {
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tasks"+clause(), args...).Scan(&page.Total); err != nil {
			return page, err
		}
	}
	if len(q.IDs) > 0 {
		where = append(where, "id=ANY("+bind(q.IDs)+"::uuid[])")
	}
	if q.Before != nil {
		where = append(where, "(created_at,id)<("+bind(q.Before.CreatedAt)+"::timestamptz,"+bind(q.Before.ID)+"::uuid)")
	}
	if q.Through != nil {
		where = append(where, "(created_at,id)>=("+bind(q.Through.CreatedAt)+"::timestamptz,"+bind(q.Through.ID)+"::uuid)")
	}
	columns := taskColumns + ",journal_revision"
	if q.VersionsOnly {
		columns = "id,created_at,journal_revision"
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+columns+" FROM tasks"+clause()+" ORDER BY created_at DESC,id DESC LIMIT "+bind(q.Limit+1), args...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var task core.Task
		var version core.GenerationRecordVersion
		var err error
		if q.VersionsOnly {
			err = rows.Scan(&version.ID, &version.CreatedAt, &version.Revision)
		} else {
			err = scanTask(generationRevisionScanner{rows, &version.Revision}, &task)
			version.ID, version.CreatedAt = task.ID, task.CreatedAt
		}
		if err != nil {
			rows.Close()
			return page, err
		}
		page.Versions = append(page.Versions, version)
		if !q.VersionsOnly {
			page.Tasks = append(page.Tasks, task)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if len(page.Versions) > q.Limit {
		page.Versions = page.Versions[:q.Limit]
		if !q.VersionsOnly {
			page.Tasks = page.Tasks[:q.Limit]
		}
		page.HasMore = true
	}
	return page, tx.Commit()
}

// Extend the common task scanner without changing historical task queries or
// adding the journal's internal revision to ordinary generation responses.
type generationRevisionScanner struct {
	row      rowScanner
	revision *int64
}

func (s generationRevisionScanner) Scan(dest ...any) error {
	return s.row.Scan(append(dest, s.revision)...)
}
