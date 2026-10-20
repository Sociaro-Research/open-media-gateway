// Package providerassets manages owner-scoped registrations in provider libraries.
package providerassets

import (
	"context"
	"errors"
	"time"
)

var (
	ErrUnavailable = errors.New("asset library is not configured for this provider")
	ErrNotFound    = errors.New("provider asset not found")
	ErrConflict    = errors.New("asset is in use, shared with another owner, or registration is already in progress")
	ErrRejected    = errors.New("provider rejected asset registration")
	ErrInvalid     = errors.New("invalid provider asset request")
)

type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Asset struct {
	Failure          string `json:"error,omitempty"`
	RegistrationName string `json:"registration_name,omitempty"`
	ID               string `json:"asset_id"`
	GroupID          string `json:"group_id"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	Status           string `json:"status"`
	PreviewURL       string `json:"preview_url,omitempty"`
	URI              string `json:"uri,omitempty"`
}
type Page struct {
	Items []Asset `json:"data"`
	Next  string  `json:"next,omitempty"`
}
type Client interface {
	Scope() string
	Groups(context.Context) ([]Group, error)
	CreateGroup(context.Context, string) (Group, error)
	List(context.Context, string, string, string) (Page, error)
	Get(context.Context, string) (Asset, error)
	Create(context.Context, string, string, string, string) (Asset, error)
	Delete(context.Context, string) error
}
type Directory interface {
	AssetLibrary(context.Context, string) (Client, error)
}
type Record struct {
	ID            string `json:"id"`
	Owner         string `json:"-"`
	Actor         string `json:"-"`
	ProviderOwner string `json:"-"`
	Provider      string `json:"provider"`
	Scope         string `json:"-"`
	SourceID      string `json:"source_id,omitempty"`
	Asset
	UpdatedAt time.Time `json:"updated_at"`
}

// Record.ID is the Gateway binding ID; Asset.ID is the provider ID.
type Repository interface {
	Groups(context.Context, string, string) ([]Group, error)
	SaveGroup(context.Context, string, string, Group) error
	List(context.Context, string, string, int) ([]Record, error)
	Get(context.Context, string) (Record, error)
	Reserve(context.Context, Record) (Record, bool, error)
	Save(context.Context, Record) error
	Pending(context.Context, int) ([]Record, error)
	BeginDelete(context.Context, string) (Record, error)
	Detach(context.Context, string) error
}
