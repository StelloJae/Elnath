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

func (m *mockTool) Name() string             { return m.name }
func (m *mockTool) Description() string      { return "mock tool: " + m.name }
func (m *mockTool) Schema() json.RawMessage  { return json.RawMessage(`{"type":"object"}`) }
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
