package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Personal namespaces retain author isolation. Company scope is accepted only
// for an actual organization owner; actor_id remains creation provenance.
const projectScope = `owner_id=$1 AND (actor_id::text=$2 OR ($3 AND EXISTS(SELECT 1 FROM organizations WHERE id=$1)))`

func projectCompany(ctx context.Context) bool { return identity.OrganizationFromContext(ctx).ID != "" }
func projectWrite(ctx context.Context) error {
	if !org.WriteProjects(identity.OrganizationFromContext(ctx).Role) {
		return core.ErrProjectForbidden
	}
	return nil
}
func lockProjectName(ctx context.Context, tx *sql.Tx, name string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1 || '/projects/' || lower($2),0))`, identity.OwnerID(ctx), name)
	return err
}
func (r *TaskRepository) ListProjects(ctx context.Context) ([]core.Project, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name,created_at,version FROM generation_projects WHERE `+projectScope+` ORDER BY lower(name),created_at,id`, identity.OwnerID(ctx), identity.ActorID(ctx), projectCompany(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []core.Project{}
	for rows.Next() {
		var p core.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.Version); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// A same-name retry returns its existing grouping after an uncertain response.
func (r *TaskRepository) CreateProject(ctx context.Context, name string) (core.Project, error) {
	name, err := core.ProjectName(name)
	if err != nil {
		return core.Project{}, err
	}
	if err = projectWrite(ctx); err != nil {
		return core.Project{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Project{}, err
	}
	defer tx.Rollback()
	if err = lockProjectName(ctx, tx, name); err != nil {
		return core.Project{}, err
	}
	var p core.Project
	// Preserve legacy duplicate IDs/names; name retries choose the oldest one.
	err = tx.QueryRowContext(ctx, `SELECT id,name,created_at,version FROM generation_projects WHERE `+projectScope+` AND lower(name)=lower($4) ORDER BY created_at,id LIMIT 1`, identity.OwnerID(ctx), identity.ActorID(ctx), projectCompany(ctx), name).Scan(&p.ID, &p.Name, &p.CreatedAt, &p.Version)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `INSERT INTO generation_projects(id,owner_id,actor_id,name) VALUES($1,$2,$3,$4) RETURNING id,name,created_at,version`, uuid.NewString(), identity.OwnerID(ctx), identity.ActorID(ctx), name).Scan(&p.ID, &p.Name, &p.CreatedAt, &p.Version)
	}
	if err != nil {
		return p, projectWriteError(err)
	}
	return p, tx.Commit()
}

func projectAccess(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return core.ErrProjectInvalid
	}
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT true FROM generation_projects WHERE `+projectScope+` AND id=$4 FOR KEY SHARE`, identity.OwnerID(ctx), identity.ActorID(ctx), projectCompany(ctx), id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return core.ErrProjectInvalid
	} else {
		return err
	}
}

func (r *TaskRepository) ListProjectTasks(ctx context.Context, id string, limit, offset int) ([]core.Task, error) {
	if id != core.NoProjectFilter {
		if err := projectAccess(ctx, r.db, id); err != nil {
			return nil, err
		}
	}
	// Company grouping does not widen existing role-filtered content reads.
	rows, err := r.db.QueryContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE owner_id=$1 AND (project_id=NULLIF($2,'none')::uuid OR ($2='none' AND project_id IS NULL)) AND ($3='' OR actor_id::text=$3) ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5`, identity.OwnerID(ctx), id, identity.ContentAuthorFilter(ctx), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []core.Task{}
	for rows.Next() {
		var task core.Task
		if err := scanTask(rows, &task); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func projectWriteError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return core.ErrProjectNameTaken
	}
	return err
}
func lockProject(ctx context.Context, tx *sql.Tx, id string, version int64) error {
	if err := projectWrite(ctx); err != nil {
		return err
	}
	if _, err := uuid.Parse(id); err != nil || version < 1 {
		return core.ErrProjectInvalid
	}
	var current int64
	err := tx.QueryRowContext(ctx, `SELECT version FROM generation_projects WHERE `+projectScope+` AND id=$4 FOR UPDATE`, identity.OwnerID(ctx), identity.ActorID(ctx), projectCompany(ctx), id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrProjectInvalid
	}
	if err != nil {
		return err
	}
	if current != version {
		return core.ErrProjectConflict
	}
	return nil
}
func (r *TaskRepository) RenameProject(ctx context.Context, id, name string, version int64) (core.Project, error) {
	name, err := core.ProjectName(name)
	if err != nil {
		return core.Project{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Project{}, err
	}
	defer tx.Rollback()
	if err = lockProject(ctx, tx, id, version); err != nil {
		return core.Project{}, err
	}
	if err = lockProjectName(ctx, tx, name); err != nil {
		return core.Project{}, err
	}
	var duplicate bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM generation_projects WHERE `+projectScope+` AND lower(name)=lower($4) AND id<>$5) AND lower((SELECT name FROM generation_projects WHERE id=$5))<>lower($4)`, identity.OwnerID(ctx), identity.ActorID(ctx), projectCompany(ctx), name, id).Scan(&duplicate)
	if err != nil {
		return core.Project{}, err
	}
	if duplicate {
		return core.Project{}, core.ErrProjectNameTaken
	}
	var p core.Project
	err = tx.QueryRowContext(ctx, `UPDATE generation_projects SET name=$2,version=version+1 WHERE id=$1 RETURNING id,name,created_at,version`, id, name).Scan(&p.ID, &p.Name, &p.CreatedAt, &p.Version)
	if err != nil {
		return core.Project{}, projectWriteError(err)
	}
	if err = tx.Commit(); err != nil {
		return core.Project{}, err
	}
	return p, nil
}
func (r *TaskRepository) DeleteProject(ctx context.Context, id string, version int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockProject(ctx, tx, id, version); err != nil {
		return err
	}
	// Lock order is project, then tasks, matching assignment and admission. This
	// covers both queued and completed generations without touching their payloads.
	if _, err = tx.ExecContext(ctx, `UPDATE tasks SET project_id=NULL,project_version=project_version+1 WHERE project_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM generation_projects WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *TaskRepository) AssignTaskProject(ctx context.Context, id, project string, version int64) (core.ProjectAssignment, error) {
	if err := projectWrite(ctx); err != nil {
		return core.ProjectAssignment{}, err
	}
	if _, err := uuid.Parse(id); err != nil || version < 0 {
		return core.ProjectAssignment{}, core.ErrTaskNotFound
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return core.ProjectAssignment{}, err
	}
	defer tx.Rollback()
	// Never lock a task and then its destination project: deletion takes the
	// project lock first. Hold the project lock until the assignment commits.
	if project != "" {
		if err = projectAccess(ctx, tx, project); err != nil {
			return core.ProjectAssignment{}, err
		}
	}
	var current int64
	err = tx.QueryRowContext(ctx, `SELECT project_version FROM tasks WHERE `+projectScope+` AND id=$4 FOR UPDATE`, identity.OwnerID(ctx), identity.ActorID(ctx), projectCompany(ctx), id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ProjectAssignment{}, core.ErrTaskNotFound
	}
	if err != nil {
		return core.ProjectAssignment{}, err
	}
	if current != version {
		return core.ProjectAssignment{}, core.ErrProjectConflict
	}
	var result core.ProjectAssignment
	err = tx.QueryRowContext(ctx, `UPDATE tasks SET project_id=$2,project_version=project_version+1 WHERE id=$1 RETURNING COALESCE(project_id::text,''),project_version`, id, nullableString(project)).Scan(&result.ProjectID, &result.Version)
	if err != nil {
		return core.ProjectAssignment{}, err
	}
	if err = tx.Commit(); err != nil {
		return core.ProjectAssignment{}, err
	}
	return result, nil
}
