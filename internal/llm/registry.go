package llm

import (
	"fmt"
	"strings"
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

// ResolveModel expands short aliases to canonical model IDs.
func ResolveModel(model string) string {
	aliases := map[string]string{
		"opus":   "claude-opus-4-6",
		"sonnet": "claude-sonnet-4-6",
		"haiku":  "claude-haiku-4-5-20251213",
	}
	if canonical, ok := aliases[model]; ok {
		return canonical
	}
	return model
}

// DetectProvider returns the provider name for a given model string.
// Model prefix always wins over env-var/config-based detection.
// Returns an empty string when the prefix is unrecognised.
func DetectProvider(model string) string {
	canonical := ResolveModel(model)
	switch {
	case strings.HasPrefix(canonical, "claude"):
		return "anthropic"
	case strings.HasPrefix(canonical, "gpt-"),
		strings.HasPrefix(canonical, "openai/"),
		strings.HasPrefix(canonical, "o1-"),
		strings.HasPrefix(canonical, "o3-"),
		strings.HasPrefix(canonical, "o4-"):
		return "openai"
	case strings.HasPrefix(canonical, "grok"):
		return "xai"
	}
	return ""
}

// ForModel returns the provider that should handle the given model together
// with the resolved canonical model ID. It tries prefix-based detection first
// and falls back to the default provider when no match is found.
func (r *Registry) ForModel(model string) (Provider, string, error) {
	canonical := ResolveModel(model)
	providerName := DetectProvider(canonical)

	if providerName != "" {
		r.mu.RLock()
		p, ok := r.providers[providerName]
		r.mu.RUnlock()
		if ok {
			return p, canonical, nil
		}
	}

	// Fall back to default provider.
	p, err := r.Default()
	if err != nil {
		return nil, "", err
	}
	return p, canonical, nil
}
