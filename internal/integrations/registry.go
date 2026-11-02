package integrations

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds connector definitions.
type Registry struct {
	mu   sync.RWMutex
	byID map[string]*Definition
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byID: map[string]*Definition{}}
}

// Register adds definitions. Duplicate ids error.
func (r *Registry) Register(defs ...*Definition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range defs {
		if d == nil || d.ID == "" {
			return fmt.Errorf("integrations: empty definition")
		}
		if _, ok := r.byID[d.ID]; ok {
			return fmt.Errorf("integrations: duplicate connector %q", d.ID)
		}
		r.byID[d.ID] = d
	}
	return nil
}

// Get returns a definition by id.
func (r *Registry) Get(id string) (*Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.byID[id]
	return d, ok
}

// All returns definitions sorted by id.
func (r *Registry) All() []*Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Definition, 0, len(r.byID))
	for _, d := range r.byID {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// DefaultRegistry returns P8.1 references (zapier, rspamd) plus P8.2 and P8.3
// connectors (slack 1.1.0 replaces the P8.1 slack reference).
func DefaultRegistry() *Registry {
	r := NewRegistry()
	defs := []*Definition{ZapierDefinition(), RspamdDefinition()}
	defs = append(defs, P82Definitions()...)
	defs = append(defs, P83Definitions()...)
	if err := r.Register(defs...); err != nil {
		panic(err)
	}
	return r
}
