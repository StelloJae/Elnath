package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/skill"
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

func TestExecutionRuntimeRegistersRuntimeCommandTool(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{})

	tool, ok := rt.reg.Get("runtime_command")
	if !ok {
		t.Fatal("runtime_command tool missing")
	}
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() {
		t.Fatal("runtime_command should be read-only and reversible")
	}
	if deferred, ok := tool.(interface{ DeferInitialToolSchema() bool }); !ok || !deferred.DeferInitialToolSchema() {
		t.Fatal("runtime_command should be deferred from initial schema")
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

func TestExecutionRuntimeRegistersProcessTools(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{})

	for _, name := range []string{tools.ProcessStartToolName, tools.ProcessMonitorToolName, tools.ProcessWaitToolName, tools.ProcessStopToolName} {
		tool, ok := rt.reg.Get(name)
		if !ok {
			t.Fatalf("%s tool missing", name)
		}
		if deferred, ok := tool.(interface{ DeferInitialToolSchema() bool }); !ok || !deferred.DeferInitialToolSchema() {
			t.Fatalf("%s should be deferred from initial schema", name)
		}
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

func TestCommandCatalogToolIncludesDiscoveryReceipt(t *testing.T) {
	tool := newCommandCatalogTool()

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"recommend","query":"reasoning effort","max_results":2}`))
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
			Name string `json:"name"`
		} `json:"commands"`
		Receipt struct {
			Tool                  string `json:"tool"`
			Action                string `json:"action"`
			ReadOnly              bool   `json:"read_only"`
			RegistryAvailable     bool   `json:"registry_available"`
			ExecutionAvailable    bool   `json:"execution_available"`
			ExecutionPolicy       string `json:"execution_policy"`
			TotalCommands         int    `json:"total_commands"`
			ReturnedCommands      int    `json:"returned_commands"`
			ExecutableCommands    int    `json:"executable_commands"`
			ModelCallableCommands int    `json:"model_callable_commands"`
			MaxResults            int    `json:"max_results"`
			Query                 string `json:"query"`
			FollowupTool          string `json:"followup_tool"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Receipt.Tool != "command_catalog" || out.Receipt.Action != "recommend" {
		t.Fatalf("receipt identity = %+v", out.Receipt)
	}
	if !out.Receipt.ReadOnly || !out.Receipt.RegistryAvailable {
		t.Fatalf("receipt read-only/registry = %+v", out.Receipt)
	}
	if out.Receipt.ExecutionAvailable || out.Receipt.ExecutionPolicy != "metadata_only" {
		t.Fatalf("receipt execution boundary = %+v", out.Receipt)
	}
	if out.Receipt.TotalCommands == 0 || out.Receipt.ReturnedCommands != len(out.Commands) {
		t.Fatalf("receipt counts = %+v commands=%d", out.Receipt, len(out.Commands))
	}
	if out.Receipt.ExecutableCommands == 0 {
		t.Fatalf("receipt executable commands = 0: %+v", out.Receipt)
	}
	if out.Receipt.MaxResults != 2 || out.Receipt.Query != "reasoning effort" {
		t.Fatalf("receipt request bounds = %+v", out.Receipt)
	}
	if out.Receipt.FollowupTool != "runtime_command" {
		t.Fatalf("receipt followup = %q, want runtime_command for read-only runtime control", out.Receipt.FollowupTool)
	}
}

func TestCommandCatalogToolListsSkillBackedSlashCommands(t *testing.T) {
	reg := skill.NewRegistry()
	reg.Add(&skill.Skill{
		Name:        "review-pr",
		Description: "Review pull requests",
		Trigger:     "/review-pr <pr_number>",
		Source:      "claude-command-skill",
		Prompt:      "Review the PR.",
	})
	tool := newCommandCatalogTool(reg)

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
	var found commandCatalogEntry
	for _, entry := range out.Commands {
		if entry.Name == "/review-pr" {
			found = entry
			break
		}
	}
	if found.Name == "" {
		t.Fatalf("commands = %+v, want /review-pr skill-backed command", out.Commands)
	}
	if found.Category != "skill" || found.ArgumentHint != "<pr_number>" || found.Source != "claude-command-skill" {
		t.Fatalf("skill-backed command = %+v", found)
	}
}

func TestCommandCatalogToolAddsSkillFollowupForModelCallableRecommendation(t *testing.T) {
	reg := skill.NewRegistry()
	reg.Add(&skill.Skill{
		Name:        "review-pr",
		Description: "Review pull requests",
		Trigger:     "/review-pr <pr_number>",
		Source:      "claude-command-skill",
		Prompt:      "Review the PR.",
	})
	tool := newCommandCatalogTool(reg)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"recommend","query":"review pull request","max_results":1}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Commands []commandCatalogEntry `json:"commands"`
		Receipt  struct {
			Tool         string `json:"tool"`
			Action       string `json:"action"`
			FollowupTool string `json:"followup_tool"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if len(out.Commands) != 1 || out.Commands[0].Name != "/review-pr" || !out.Commands[0].ModelCallable {
		t.Fatalf("commands = %+v, want one model-callable /review-pr command", out.Commands)
	}
	if out.Receipt.Tool != "command_catalog" || out.Receipt.Action != "recommend" || out.Receipt.FollowupTool != "skill" {
		t.Fatalf("receipt = %+v, want skill followup", out.Receipt)
	}
}

func TestCommandCatalogToolAddsSkillFollowupForModelCallableShow(t *testing.T) {
	reg := skill.NewRegistry()
	reg.Add(&skill.Skill{
		Name:        "review-pr",
		Description: "Review pull requests",
		Trigger:     "/review-pr <pr_number>",
		Prompt:      "Review the PR.",
	})
	tool := newCommandCatalogTool(reg)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"show","command":"/review-pr"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Command *commandCatalogEntry `json:"command"`
		Receipt struct {
			FollowupTool string `json:"followup_tool"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Command == nil || out.Command.Name != "/review-pr" || !out.Command.ModelCallable {
		t.Fatalf("command = %+v, want model-callable /review-pr", out.Command)
	}
	if out.Receipt.FollowupTool != "skill" {
		t.Fatalf("receipt followup = %q, want skill", out.Receipt.FollowupTool)
	}
}

func TestCommandCatalogToolHidesNonUserInvocableSkillBackedCommandsByDefault(t *testing.T) {
	reg := skill.NewRegistry()
	reg.Add(&skill.Skill{
		Name:        "visible-review",
		Description: "Visible review",
		Trigger:     "/visible-review",
		Prompt:      "Review.",
	})
	reg.Add(&skill.Skill{
		Name:        "background-review",
		Description: "Background review",
		Trigger:     "/background-review <target>",
		Prompt:      "Review in background.",
		Hidden:      true,
	})
	tool := newCommandCatalogTool(reg)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}
	var out struct {
		Commands []commandCatalogEntry `json:"commands"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	seen := map[string]commandCatalogEntry{}
	for _, entry := range out.Commands {
		seen[entry.Name] = entry
	}
	if _, ok := seen["/visible-review"]; !ok {
		t.Fatalf("commands = %+v, want visible skill command", out.Commands)
	}
	if _, ok := seen["/background-review"]; ok {
		t.Fatalf("commands = %+v, hidden skill command should be omitted by default", out.Commands)
	}

	res, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"list","include_hidden":true}`))
	if err != nil {
		t.Fatalf("Execute include hidden error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute include hidden returned error result: %s", res.Output)
	}
	out.Commands = nil
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("include hidden output is not JSON: %v\n%s", err, res.Output)
	}
	seen = map[string]commandCatalogEntry{}
	for _, entry := range out.Commands {
		seen[entry.Name] = entry
	}
	if entry, ok := seen["/background-review"]; !ok || !entry.Hidden || entry.ArgumentHint != "<target>" {
		t.Fatalf("commands = %+v, want hidden background-review with argument hint", out.Commands)
	}
}

func TestCommandCatalogToolUsesSkillNameWhenTriggerIsNotSlashCommand(t *testing.T) {
	reg := skill.NewRegistry()
	reg.Add(&skill.Skill{
		Name:        "review-pr",
		Description: "Review pull requests",
		Trigger:     "Use when reviewing pull requests",
		Source:      "codex-skill",
		Prompt:      "Review the PR.",
	})
	tool := newCommandCatalogTool(reg)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Commands []commandCatalogEntry `json:"commands"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	seen := map[string]commandCatalogEntry{}
	for _, entry := range out.Commands {
		seen[entry.Name] = entry
	}
	if _, ok := seen["/Use"]; ok {
		t.Fatalf("commands = %+v, natural-language trigger should not become /Use", out.Commands)
	}
	if entry, ok := seen["/review-pr"]; !ok || entry.ArgumentHint != "" {
		t.Fatalf("commands = %+v, want /review-pr without argument hint", out.Commands)
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
	if !strings.Contains(out.Command.ArgumentHint, "max") {
		t.Fatalf("ArgumentHint = %q, want max effort alias", out.Command.ArgumentHint)
	}
}

func TestCommandCatalogToolShowsRuntimeControlArgumentHints(t *testing.T) {
	tool := newCommandCatalogTool()

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"show","command":"/provider"}`))
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
	if out.Command == nil {
		t.Fatalf("output = %+v, want /provider command metadata", out)
	}
	if out.Command.ArgumentHint != "status|candidates|check <provider>|use <provider> [--json]" {
		t.Fatalf("ArgumentHint = %q, want provider usage hint", out.Command.ArgumentHint)
	}
}

func TestCommandCatalogToolExposesExecutionPolicyMetadata(t *testing.T) {
	reg := skill.NewRegistry()
	reg.Add(&skill.Skill{
		Name:        "review-pr",
		Description: "Review pull requests",
		Trigger:     "/review-pr <pr_number>",
		Source:      "claude-command-skill",
		Prompt:      "Review the PR.",
	})
	tool := newCommandCatalogTool(reg)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list","include_hidden":true}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Commands []commandCatalogEntry `json:"commands"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	seen := map[string]commandCatalogEntry{}
	for _, entry := range out.Commands {
		seen[entry.Name] = entry
	}
	if got := seen["run"]; got.Surface != "cli" || got.ExecutionPolicy != "cli_dispatch" || !got.ExecutionAvailable || got.ModelCallable {
		t.Fatalf("run command metadata = %+v, want cli non-model execution policy", got)
	}
	if got := seen["/provider"]; got.Surface != "runtime_slash" || got.ExecutionPolicy != "local_runtime_control" || !got.ExecutionAvailable || !got.ModelCallable {
		t.Fatalf("/provider metadata = %+v, want runtime slash read-only model-callable execution policy", got)
	}
	if got := seen["/review-pr"]; got.Surface != "skill_slash" || got.ExecutionPolicy != "skill_prompt_invocation" || !got.ExecutionAvailable || !got.ModelCallable || got.Source != "claude-command-skill" {
		t.Fatalf("/review-pr metadata = %+v, want model-callable skill invocation policy", got)
	}
	if got := seen["netproxy"]; got.Surface != "internal" || got.ExecutionPolicy != "internal_exec" || !got.ExecutionAvailable || got.ModelCallable {
		t.Fatalf("hidden internal command metadata = %+v, want internal non-model execution policy", got)
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
		Receipt struct {
			FollowupTool string `json:"followup_tool"`
		} `json:"receipt"`
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
	if out.Receipt.FollowupTool != "runtime_command" {
		t.Fatalf("receipt followup = %q, want runtime_command", out.Receipt.FollowupTool)
	}
}

func TestRuntimeCommandToolExecutesStatusReadOnly(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{})
	tool, ok := rt.reg.Get("runtime_command")
	if !ok {
		t.Fatal("runtime_command tool missing")
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"/status","args":["--json"]}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Command string `json:"command"`
		Output  string `json:"output"`
		Receipt struct {
			Tool               string   `json:"tool"`
			Action             string   `json:"action"`
			ReadOnly           bool     `json:"read_only"`
			ExecutionAvailable bool     `json:"execution_available"`
			ExecutionPolicy    string   `json:"execution_policy"`
			Command            string   `json:"command"`
			Args               []string `json:"args"`
			StateMutation      bool     `json:"state_mutation"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Command != "/status" || !strings.Contains(out.Output, `"provider"`) {
		t.Fatalf("output = %+v, want status JSON", out)
	}
	if out.Receipt.Tool != "runtime_command" || out.Receipt.Action != "execute" || !out.Receipt.ReadOnly || !out.Receipt.ExecutionAvailable || out.Receipt.ExecutionPolicy != "local_runtime_control_readonly" {
		t.Fatalf("receipt identity = %+v", out.Receipt)
	}
	if out.Receipt.Command != "/status" || len(out.Receipt.Args) != 1 || out.Receipt.Args[0] != "--json" || out.Receipt.StateMutation {
		t.Fatalf("receipt command bounds = %+v", out.Receipt)
	}
}

func TestRuntimeCommandToolRejectsMutatingEffortCommand(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{})
	tool, ok := rt.reg.Get("runtime_command")
	if !ok {
		t.Fatal("runtime_command tool missing")
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"/effort","args":["high"]}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Output, "read-only") {
		t.Fatalf("result = %+v, want read-only rejection", res)
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
