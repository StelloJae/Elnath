package tools

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
)

// mockTool is a minimal Tool implementation for registry tests.
type mockTool struct {
	name   string
	result *Result
}

func (m *mockTool) Name() string                           { return m.name }
func (m *mockTool) Description() string                    { return "mock tool: " + m.name }
func (m *mockTool) Schema() json.RawMessage                { return json.RawMessage(`{"type":"object"}`) }
func (m *mockTool) IsConcurrencySafe(json.RawMessage) bool { return false }
func (m *mockTool) Reversible() bool                       { return false }
func (m *mockTool) Scope(json.RawMessage) ToolScope        { return ConservativeScope() }
func (m *mockTool) ShouldCancelSiblingsOnError() bool      { return false }
func (m *mockTool) Execute(_ context.Context, _ json.RawMessage) (*Result, error) {
	return m.result, nil
}

func TestRegisterAndExecute(t *testing.T) {
	reg := NewRegistry()
	want := SuccessResult("mock output")
	reg.Register(&mockTool{name: "echo", result: want})

	got, err := reg.Execute(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	if got.Output != want.Output || got.IsError != want.IsError {
		t.Errorf("Execute result = %+v, want %+v", got, want)
	}
}

func TestExecuteUnknown(t *testing.T) {
	reg := NewRegistry()

	got, err := reg.Execute(context.Background(), "no_such_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	if !got.IsError {
		t.Errorf("expected IsError=true for unknown tool, got false")
	}
}

func TestList(t *testing.T) {
	reg := NewRegistry()
	names := []string{"alpha", "beta", "gamma"}
	for _, n := range names {
		reg.Register(&mockTool{name: n, result: SuccessResult("")})
	}

	tools := reg.List()
	if len(tools) != len(names) {
		t.Fatalf("List returned %d tools, want %d", len(tools), len(names))
	}

	got := make([]string, len(tools))
	for i, t := range tools {
		got[i] = t.Name()
	}
	sort.Strings(got)
	sort.Strings(names)

	for i := range names {
		if got[i] != names[i] {
			t.Errorf("List()[%d] = %q, want %q", i, got[i], names[i])
		}
	}
}

func TestToolDefs(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "alpha", result: SuccessResult("")})
	reg.Register(&mockTool{name: "beta", result: SuccessResult("")})

	defs := reg.ToolDefs()
	if len(defs) != 2 {
		t.Fatalf("ToolDefs returned %d defs, want 2", len(defs))
	}

	// Build a name→def map for order-independent assertion.
	byName := make(map[string]struct {
		desc   string
		schema string
	})
	for _, d := range defs {
		byName[d.Name] = struct {
			desc   string
			schema string
		}{d.Description, string(d.InputSchema)}
	}

	for _, name := range []string{"alpha", "beta"} {
		d, ok := byName[name]
		if !ok {
			t.Errorf("ToolDefs missing entry for %q", name)
			continue
		}
		if d.desc == "" {
			t.Errorf("ToolDef[%q].Description is empty", name)
		}
		if d.schema == "" {
			t.Errorf("ToolDef[%q].InputSchema is empty", name)
		}
	}
}

func TestRegisterDuplicate(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "dup", result: SuccessResult("")})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	reg.Register(&mockTool{name: "dup", result: SuccessResult("")})
}
