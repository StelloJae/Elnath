package llm

import (
	"fmt"
	"sync"

	"github.com/stello/elnath/internal/core"
)

// Registry holds named Provider instances and is safe for concurrent use.
type Registry struct {
	mu          sync.RWMutex
	providers   map[string]Provider
	defaultName string
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds or replaces the provider under the given name.
// The first registered provider becomes the default unless SetDefault is
// called explicitly.
func (r *Registry) Register(name string, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = p
	if r.defaultName == "" {
		r.defaultName = name
	}
}

// SetDefault designates which provider is returned by Default().
// Returns an error if the name has not been registered.
func (r *Registry) SetDefault(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[name]; !ok {
		return fmt.Errorf("llm registry: set default %q: %w", name, core.ErrNotFound)
	}
	r.defaultName = name
	return nil
}

// Get returns the provider with the given name, or core.ErrNotFound if absent.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("llm registry: provider %q: %w", name, core.ErrNotFound)
	}
	return p, nil
}

// Default returns the default provider, or an error if none is set.
func (r *Registry) Default() (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.defaultName == "" {
		return nil, fmt.Errorf("llm registry: no default provider: %w", core.ErrNotFound)
	}
	return r.providers[r.defaultName], nil
}

// List returns all registered provider names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for n := range r.providers {
		names = append(names, n)
	}
	return names
}

// Names is an alias for List, kept for backwards compatibility.
func (r *Registry) Names() []string { return r.List() }
