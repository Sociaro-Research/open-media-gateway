// Package providerassets composes the provider modules shipped in this build.
package providerassets

import (
	"fmt"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
	"github.com/Sociaro-Research/media-gateway-dev/plugins/provider/agnes"
	"github.com/Sociaro-Research/media-gateway-dev/plugins/provider/alibaba"
	"github.com/Sociaro-Research/media-gateway-dev/plugins/provider/bytedance"
	"github.com/Sociaro-Research/media-gateway-dev/plugins/provider/mock"
	"github.com/Sociaro-Research/media-gateway-dev/plugins/provider/openai"
	"github.com/Sociaro-Research/media-gateway-dev/plugins/provider/sonilo"
	"regexp"
)

// This is the only host file edited when adding or removing a module.
var builtins = MustRegistry(openai.Definition(), mock.Definition(), bytedance.Definition(), alibaba.Definition(), agnes.Definition(), sonilo.Definition())

type Registry struct {
	definitions []providerkit.Definition
	byID        map[string]providerkit.Definition
	aliases     map[string]string
}

func NewRegistry(ds ...providerkit.Definition) (*Registry, error) {
	r := &Registry{byID: map[string]providerkit.Definition{}, aliases: map[string]string{}}
	valid := regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	for _, d := range ds {
		if !valid.MatchString(d.ID) || d.Name == "" || d.Factory == nil {
			return nil, fmt.Errorf("invalid provider definition %q", d.ID)
		}
		if _, ok := r.byID[d.ID]; ok {
			return nil, fmt.Errorf("duplicate provider %q", d.ID)
		}
		fields := map[string]bool{}
		for _, f := range d.Fields {
			if !valid.MatchString(f.Key) || fields[f.Key] || f.Key == "enabled" {
				return nil, fmt.Errorf("invalid field in %s", d.ID)
			}
			fields[f.Key] = true
		}
		if (d.Bills == nil) != (d.NormalizeBill == nil) {
			return nil, fmt.Errorf("incomplete billing contract for %s", d.ID)
		}
		r.byID[d.ID] = d
		r.definitions = append(r.definitions, d)
	}
	for _, d := range ds {
		for _, a := range d.Aliases {
			if _, ok := r.byID[a]; ok || r.aliases[a] != "" || !valid.MatchString(a) {
				return nil, fmt.Errorf("duplicate or invalid provider alias %q", a)
			}
			r.aliases[a] = d.ID
		}
	}
	return r, nil
}
func MustRegistry(ds ...providerkit.Definition) *Registry {
	r, e := NewRegistry(ds...)
	if e != nil {
		panic(e)
	}
	return r
}
func (r *Registry) All() []providerkit.Definition {
	return append([]providerkit.Definition(nil), r.definitions...)
}
func (r *Registry) Find(id string) (providerkit.Definition, bool) {
	if a := r.aliases[id]; a != "" {
		id = a
	}
	d, ok := r.byID[id]
	return d, ok
}
func All() []providerkit.Definition                 { return builtins.All() }
func Find(id string) (providerkit.Definition, bool) { return builtins.Find(id) }
func IDs() []string {
	ids := []string{}
	for _, d := range All() {
		ids = append(ids, d.ID)
	}
	return ids
}
func Canonical(id string) string {
	if d, ok := Find(id); ok {
		return d.ID
	}
	return id
}
func DefaultID() string {
	for _, d := range All() {
		if d.Default {
			return d.ID
		}
	}
	for _, d := range All() {
		return d.ID
	}
	return ""
}

func Builtins() *Registry { return builtins }
