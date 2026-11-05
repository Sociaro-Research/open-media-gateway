// Package diagnostics projects stored failure text through trusted, closed vocabularies.
package diagnostics

import (
	"sort"
	"sync"
)

const MaxStoredMessageBytes = 4096
const UnknownGeneration = "Generation failed. Stored diagnostic details could not be safely verified. Check the original generation and provider history; ask an operator to inspect the provider connection and result storage before creating another generation."

// Projector must consume the whole input and reconstruct output exclusively from
// constants and validated bounded values. Never return arbitrary stored text.
type Projector func(string) (string, bool)
type Registry struct {
	mu      sync.RWMutex
	entries map[string]Projector
	ordered []Projector
}

func (r *Registry) Register(name string, fn Projector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[string]Projector{}
	}
	r.entries[name] = fn
	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	r.ordered = make([]Projector, 0, len(names))
	for _, name := range names {
		r.ordered = append(r.ordered, r.entries[name])
	}
}
func (r *Registry) Project(raw string) (string, bool) {
	if raw == "" || len(raw) > MaxStoredMessageBytes {
		return "", false
	}
	r.mu.RLock()
	entries := r.ordered
	r.mu.RUnlock()
	for _, fn := range entries {
		if safe, ok := fn(raw); ok {
			return safe, true
		}
	}
	return "", false
}

var shared Registry

func Register(name string, fn Projector) { shared.Register(name, fn) }
func Project(raw string) (string, bool)  { return shared.Project(raw) }

// RegisterExact clones adapter-owned constant vocabularies. Call only from
// trusted adapter initialization, never with request/configuration/response data.
func RegisterExact(name string, messages map[string]string) {
	table := make(map[string]string, len(messages)*2)
	for raw, safe := range messages {
		table[raw] = safe
		table[safe] = safe
	}
	Register(name, func(raw string) (string, bool) { safe, ok := table[raw]; return safe, ok })
}
