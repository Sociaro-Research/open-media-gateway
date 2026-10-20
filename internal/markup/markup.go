// Package markup resolves customer pricing rules without changing provider costs.
package markup

import (
	"context"
	"errors"
	"strings"

	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
)

type Rule struct {
	Provider string         `json:"provider"`
	Model    string         `json:"model,omitempty"`
	Percent  *money.Percent `json:"markup_percent"`
}
type Config struct {
	Default    *money.Percent `json:"default_percent"`
	Exceptions []Rule         `json:"exceptions"`
	Version    int64          `json:"version"`
}
type Policy struct {
	System  Config `json:"system"`
	Company Config `json:"company"`
}
type Resolution struct {
	Percent money.Percent `json:"percent"`
	Source  string        `json:"source"`
}
type Reader interface {
	MarkupPolicy(context.Context, string) (Policy, error)
}
type Repository interface {
	Markups(context.Context, string, string) (Policy, error)
	SaveMarkups(context.Context, string, string, Config) (Policy, error)
}

func (c Config) Validate(system bool) error {
	if system && c.Default == nil {
		return errors.New("system default markup is required")
	}
	if c.Default != nil {
		if err := c.Default.Validate(); err != nil {
			return err
		}
	}
	if c.Version < 1 {
		return errors.New("version is required")
	}
	if len(c.Exceptions) > 500 {
		return errors.New("at most 500 markup exceptions are allowed")
	}
	seen := map[string]bool{}
	for _, r := range c.Exceptions {
		if r.Provider == "" || len(r.Provider) > 100 || strings.TrimSpace(r.Provider) != r.Provider || len(r.Model) > 256 || strings.TrimSpace(r.Model) != r.Model || strings.ContainsAny(r.Provider+r.Model, "\x00\r\n") {
			return errors.New("invalid provider or model identifier")
		}
		if r.Percent == nil {
			return errors.New("each exception requires a markup percentage; remove it to inherit")
		}
		if err := r.Percent.Validate(); err != nil {
			return err
		}
		key := r.Provider + "\x00" + r.Model
		if seen[key] {
			return errors.New("only one exception per provider or provider/model is allowed")
		}
		seen[key] = true
	}
	return nil
}
func (p Policy) Resolve(provider, model string) Resolution {
	for _, scope := range []struct {
		config Config
		name   string
	}{{p.Company, "company"}, {p.System, "system"}} {
		for _, r := range scope.config.Exceptions {
			if r.Percent != nil && r.Provider == provider && r.Model != "" && r.Model == model {
				return Resolution{*r.Percent, scope.name + "_model"}
			}
		}
		for _, r := range scope.config.Exceptions {
			if r.Percent != nil && r.Provider == provider && r.Model == "" {
				return Resolution{*r.Percent, scope.name + "_provider"}
			}
		}
		if scope.config.Default != nil {
			return Resolution{*scope.config.Default, scope.name + "_default"}
		}
	}
	return Resolution{0, "system_default"}
}
