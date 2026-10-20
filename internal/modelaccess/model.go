// Package modelaccess defines installation-wide public model identities and grants.
package modelaccess

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

type Ref struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Name     string `json:"name"`
}

type Model struct {
	Ref
	ID               string `json:"id"`
	PublicID         string `json:"public_id"`
	EnabledByDefault bool   `json:"enabled_by_default"`
	Granted          bool   `json:"granted"`
	Version          int64  `json:"version"`
}

type Update struct {
	PublicID         string `json:"public_id"`
	EnabledByDefault *bool  `json:"enabled_by_default"`
	Version          int64  `json:"version"`
}

type Access struct {
	ModelIDs []string `json:"model_ids"`
	Version  int64    `json:"version"`
}

type Store interface {
	Ensure(context.Context, []Ref) error
	List(context.Context, string) ([]Model, error)
	Update(context.Context, string, string, Update) (Model, error)
	Organization(context.Context, string, string) (Access, error)
	SaveOrganization(context.Context, string, string, Access) (Access, error)
}

var publicID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}$`)

func (u Update) Validate() error {
	if !publicID.MatchString(u.PublicID) || strings.Contains(u.PublicID, "//") || strings.HasSuffix(u.PublicID, "/") {
		return errors.New("public_id must be 1–200 characters: letters, digits, dots, underscores, colons, slashes or hyphens")
	}
	if u.EnabledByDefault == nil || u.Version < 1 {
		return errors.New("enabled_by_default and the current version are required")
	}
	return nil
}
func Key(provider, model string) string { return provider + "\x00" + model }
