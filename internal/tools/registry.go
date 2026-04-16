package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/stello/elnath/internal/llm"
)

// Registry holds named tools and dispatches execution by tool name.
// All methods are safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

type readTrackerProvider interface {
	ReadTracker() *ReadTracker
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool. Panics on duplicate names.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		panic(fmt.Sprintf("tools: duplicate tool name %q", t.Name()))
	}
	r.tools[t.Name()] = t
}

// Get returns the tool by name and whether it was found.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Execute runs the named tool with the given JSON params.
func (r *Registry) Execute(ctx context.Context, name string, params json.RawMessage) (*Result, error) {
	t, ok := r.Get(name)
	if !ok {
		return ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
	if targetProvider, ok := t.(ArgTargetProvider); ok {
		params = CoerceToolArgs(params, targetProvider.ArgsTarget())
	}
	return t.Execute(ctx, params)
}

// List returns all registered tools in an unspecified order.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for name := range r.tools {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) ReadTracker() *ReadTracker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, tool := range r.tools {
		provider, ok := tool.(readTrackerProvider)
		if !ok {
			continue
		}
		if tracker := provider.ReadTracker(); tracker != nil {
			return tracker
		}
	}
	return nil
}

// ToolDefs returns the llm.ToolDef definitions for all registered tools,
// suitable for inclusion in a ChatRequest.
func (r *Registry) ToolDefs() []llm.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]llm.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, llm.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
		})
	}
	return defs
}
