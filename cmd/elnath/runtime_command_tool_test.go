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

func TestExecutionRuntimeRegistersAskUserQuestionTool(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{})

	tool, ok := rt.reg.Get("ask_user_question")
	if !ok {
		t.Fatal("ask_user_question tool missing")
	}
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() {
		t.Fatal("ask_user_question should be read-only and reversible")
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
	for _, name := range []string{"commands", "run", "skill", "/effort", "/model", "/provider", "/skills"} {
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

func TestCommandCatalogToolShowsRuntimeControlWithoutExecuting(t *testing.T) {
	tool := newCommandCatalogTool()

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"show","command":"/effort"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Action  string               `json:"action"`
		Command *commandCatalogEntry `json:"command"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "show" || out.Command == nil || out.Command.Name != "/effort" || out.Command.Category != "runtime-control" {
		t.Fatalf("output = %+v, want /effort runtime-control metadata", out)
	}
}

func TestCommandCatalogToolRecommendsCommandsByQuery(t *testing.T) {
	tool := newCommandCatalogTool()

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"recommend","query":"provider model status","max_results":1}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Action   string `json:"action"`
		Query    string `json:"query"`
		Commands []struct {
			Name          string   `json:"name"`
			Score         int      `json:"score"`
			MatchedFields []string `json:"matched_fields"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "recommend" || out.Query != "provider model status" || len(out.Commands) != 1 {
		t.Fatalf("output = %+v, want one recommendation", out)
	}
	if got := out.Commands[0]; got.Name != "provider" || got.Score == 0 || len(got.MatchedFields) == 0 {
		t.Fatalf("recommendation = %+v, want scored provider command", got)
	}
}

func TestCommandCatalogToolRecommendsRuntimeControlByQuery(t *testing.T) {
	tool := newCommandCatalogTool()

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"recommend","query":"reasoning effort status","max_results":1}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Action   string `json:"action"`
		Commands []struct {
			Name     string `json:"name"`
			Category string `json:"category"`
			Score    int    `json:"score"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if len(out.Commands) != 1 {
		t.Fatalf("commands = %+v, want one recommendation", out.Commands)
	}
	if got := out.Commands[0]; got.Name != "/effort" || got.Category != "runtime-control" || got.Score == 0 {
		t.Fatalf("recommendation = %+v, want scored /effort runtime-control", got)
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
