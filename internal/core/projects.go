package core

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// NoProjectFilter is a read-only list filter, outside the UUID project namespace.
// It must never be used as a generation assignment or project identity.
const NoProjectFilter = "none"

var ErrProjectForbidden = errors.New("project changes are not allowed for this role")

var ErrProjectInvalid = errors.New("project is unavailable or invalid")
var ErrProjectConflict = errors.New("project changed; review the current value before saving")
var ErrProjectNameTaken = errors.New("a project with this name already exists")
var ErrProjectName = errors.New("project name must contain 1 to 80 characters without control characters")

type Project struct {
	Version   int64     `json:"version"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func ProjectName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		return "", ErrProjectName
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return "", ErrProjectName
		}
	}
	return name, nil
}

type ProjectRepository interface {
	ListProjects(context.Context) ([]Project, error)
	CreateProject(context.Context, string) (Project, error)
	ListProjectTasks(context.Context, string, int, int) ([]Task, error)
}

func (s *TaskService) ListProjects(ctx context.Context) ([]Project, error) {
	if r, ok := s.repo.(ProjectRepository); ok {
		return r.ListProjects(ctx)
	}
	return nil, ErrProjectInvalid
}
func (s *TaskService) CreateProject(ctx context.Context, name string) (Project, error) {
	name, err := ProjectName(name)
	if err != nil {
		return Project{}, err
	}
	if r, ok := s.repo.(ProjectRepository); ok {
		return r.CreateProject(ctx, name)
	}
	return Project{}, ErrProjectInvalid
}
func (s *TaskService) ListProjectTasks(ctx context.Context, id string, limit, offset int) ([]Task, error) {
	if r, ok := s.repo.(ProjectRepository); ok {
		return r.ListProjectTasks(ctx, id, limit, offset)
	}
	return nil, ErrProjectInvalid
}

// Project changes never alter generation content or retention settings.
type ProjectAssignment struct {
	ProjectID string `json:"project_id"`
	Version   int64  `json:"version"`
}
type ProjectManagement interface {
	RenameProject(context.Context, string, string, int64) (Project, error)
	DeleteProject(context.Context, string, int64) error
	AssignTaskProject(context.Context, string, string, int64) (ProjectAssignment, error)
}

func (s *TaskService) RenameProject(ctx context.Context, id, name string, version int64) (Project, error) {
	if store, ok := s.repo.(ProjectManagement); ok {
		return store.RenameProject(ctx, id, name, version)
	}
	return Project{}, ErrProjectInvalid
}
func (s *TaskService) DeleteProject(ctx context.Context, id string, version int64) error {
	if store, ok := s.repo.(ProjectManagement); ok {
		return store.DeleteProject(ctx, id, version)
	}
	return ErrProjectInvalid
}
func (s *TaskService) AssignTaskProject(ctx context.Context, id, project string, version int64) (ProjectAssignment, error) {
	if store, ok := s.repo.(ProjectManagement); ok {
		return store.AssignTaskProject(ctx, id, project, version)
	}
	return ProjectAssignment{}, ErrProjectInvalid
}
