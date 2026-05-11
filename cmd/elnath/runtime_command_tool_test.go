package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

func TestExecutionRuntimeRegistersCommandCatalogTool(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{})

	tool, ok := rt.reg.Get("command_catalog")
	if !ok {
		t.Fatal("command_catalog tool missing")
	}
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() {
		t.Fatal("command_catalog should be read-only and reversible")
	}
	if got := tool.Scope(nil); len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 || got.Network || got.Persistent {
		t.Fatalf("Scope(nil) = %+v, want empty read-only scope", got)
	}
}

func TestExecutionRuntimeRegistersSkillCatalogTool(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{})

	tool, ok := rt.reg.Get("skill_catalog")
	if !ok {
		t.Fatal("skill_catalog tool missing")
	}
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() {
		t.Fatal("skill_catalog should be read-only and reversible")
	}
}

func TestCommandCatalogToolListsCommandsAsJSON(t *testing.T) {
	tool := newCommandCatalogTool()

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Action   string                `json:"action"`
		Commands []commandCatalogEntry `json:"commands"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "list" {
		t.Fatalf("Action = %q, want list", out.Action)
	}
	seen := map[string]bool{}
	for _, entry := range out.Commands {
		seen[entry.Name] = true
		if entry.Hidden {
			t.Fatalf("list exposed hidden command %q", entry.Name)
		}
	}
	for _, name := range []string{"commands", "run", "skill"} {
		if !seen[name] {
			t.Fatalf("command list missing %q", name)
		}
	}
}

func TestCommandCatalogToolShowsCommandWithoutExecuting(t *testing.T) {
	tool := newCommandCatalogTool()

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"show","command":"run"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}
	if strings.Contains(res.Output, "runtime answer") {
		t.Fatalf("command_catalog should not execute commands: %s", res.Output)
	}

	var out struct {
		Action  string               `json:"action"`
		Command *commandCatalogEntry `json:"command"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "show" || out.Command == nil || out.Command.Name != "run" {
		t.Fatalf("output = %+v, want run command metadata", out)
	}
}

func TestCommandCatalogToolRejectsExecuteAction(t *testing.T) {
	tool := newCommandCatalogTool()

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"execute","command":"run"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Output, "unsupported action") {
		t.Fatalf("result = %+v, want unsupported action error", res)
	}
}

var _ tools.Tool = (*commandCatalogTool)(nil)
var _ llm.Provider = (*countingProvider)(nil)
