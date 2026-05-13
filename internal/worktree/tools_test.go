package worktree

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/tools"
)

func TestEnterWorktreeCreatesRegistryAndReusesExisting(t *testing.T) {
	repo := initGitRepo(t)
	tool := NewEnterTool(NewManager(repo))

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"feature/smoke"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("enter returned error result: %s", result.Output)
	}
	var output EnterOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal enter output: %v", err)
	}
	if output.Name != "feature/smoke" || output.Slug != "feature+smoke" {
		t.Fatalf("output = %+v, want normalized worktree names", output)
	}
	if output.Existing {
		t.Fatalf("first enter Existing = true, want false")
	}
	if output.Receipt.Tool != EnterToolName || output.Receipt.Action != "enter" || output.Receipt.ReadOnly || !output.Receipt.Persistent || !output.Receipt.RegistryBacked || output.Receipt.Name != output.Name || output.Receipt.Branch != output.Branch {
		t.Fatalf("receipt = %+v, want enter_worktree registry mutation receipt", output.Receipt)
	}
	repoRoot := gitOutput(t, repo, "rev-parse", "--show-toplevel")
	if !strings.HasPrefix(output.Path, filepath.Join(repoRoot, ".elnath", "worktrees")+string(os.PathSeparator)) {
		t.Fatalf("worktree path = %q, want under .elnath/worktrees", output.Path)
	}
	if got := gitOutput(t, output.Path, "rev-parse", "--show-toplevel"); got != output.Path {
		t.Fatalf("worktree rev-parse = %q, want %q", got, output.Path)
	}

	registry, err := NewManager(repo).readRegistry(context.Background())
	if err != nil {
		t.Fatalf("readRegistry: %v", err)
	}
	if len(registry.Worktrees) != 1 || registry.Worktrees[0].Name != "feature/smoke" || registry.Worktrees[0].OriginalHead == "" {
		t.Fatalf("registry = %+v, want one recorded worktree with original head", registry)
	}

	second, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"feature/smoke"}`))
	if err != nil {
		t.Fatalf("second Execute error = %v", err)
	}
	if second.IsError {
		t.Fatalf("second enter returned error result: %s", second.Output)
	}
	var secondOutput EnterOutput
	if err := json.Unmarshal([]byte(second.Output), &secondOutput); err != nil {
		t.Fatalf("unmarshal second output: %v", err)
	}
	if !secondOutput.Existing || secondOutput.Path != output.Path {
		t.Fatalf("second output = %+v, want existing worktree reuse at %q", secondOutput, output.Path)
	}
}

func TestEnterWorktreeRejectsUnsafeName(t *testing.T) {
	result, err := NewEnterTool(NewManager(initGitRepo(t))).Execute(context.Background(), json.RawMessage(`{"name":"../escape"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "invalid name") {
		t.Fatalf("result = %+v, want invalid name error", result)
	}
}

func TestExitWorktreeRequiresCleanOrDiscard(t *testing.T) {
	repo := initGitRepo(t)
	enter := NewEnterTool(NewManager(repo))
	exit := NewExitTool(NewManager(repo))

	result, err := enter.Execute(context.Background(), json.RawMessage(`{"name":"fix"}`))
	if err != nil {
		t.Fatalf("enter Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("enter returned error result: %s", result.Output)
	}
	var enterOutput EnterOutput
	if err := json.Unmarshal([]byte(result.Output), &enterOutput); err != nil {
		t.Fatalf("unmarshal enter output: %v", err)
	}
	if err := os.WriteFile(filepath.Join(enterOutput.Path, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	blocked, err := exit.Execute(context.Background(), json.RawMessage(`{"name":"fix","action":"remove"}`))
	if err != nil {
		t.Fatalf("blocked Execute error = %v", err)
	}
	if !blocked.IsError || !strings.Contains(blocked.Output, "uncommitted changes") {
		t.Fatalf("blocked result = %+v, want dirty worktree guard", blocked)
	}

	removed, err := exit.Execute(context.Background(), json.RawMessage(`{"name":"fix","action":"remove","discard_changes":true}`))
	if err != nil {
		t.Fatalf("remove Execute error = %v", err)
	}
	if removed.IsError {
		t.Fatalf("remove returned error result: %s", removed.Output)
	}
	var exitOutput ExitOutput
	if err := json.Unmarshal([]byte(removed.Output), &exitOutput); err != nil {
		t.Fatalf("unmarshal exit output: %v", err)
	}
	if !exitOutput.Removed || exitOutput.DirtyFiles != 1 {
		t.Fatalf("exit output = %+v, want forced removal with dirty count", exitOutput)
	}
	if exitOutput.Receipt.Tool != ExitToolName || exitOutput.Receipt.Action != "exit" || exitOutput.Receipt.ReadOnly || !exitOutput.Receipt.Persistent || !exitOutput.Receipt.RegistryBacked || !exitOutput.Receipt.Removed {
		t.Fatalf("receipt = %+v, want exit_worktree registry mutation receipt", exitOutput.Receipt)
	}
	if _, err := os.Stat(enterOutput.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists or stat failed unexpectedly: %v", err)
	}
}

func TestWorktreeListToolShowsRegisteredWorktrees(t *testing.T) {
	repo := initGitRepo(t)
	manager := NewManager(repo)
	enter := NewEnterTool(manager)
	list := NewListTool(manager)

	result, err := enter.Execute(context.Background(), json.RawMessage(`{"name":"feature/list"}`))
	if err != nil {
		t.Fatalf("enter Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("enter returned error result: %s", result.Output)
	}

	listed, err := list.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("list Execute error = %v", err)
	}
	if listed.IsError {
		t.Fatalf("list returned error result: %s", listed.Output)
	}
	var output ListOutput
	if err := json.Unmarshal([]byte(listed.Output), &output); err != nil {
		t.Fatalf("unmarshal list output: %v", err)
	}
	if output.Total != 1 || len(output.Worktrees) != 1 {
		t.Fatalf("list output = %+v, want one registered worktree", output)
	}
	if output.Receipt.Tool != ListToolName || output.Receipt.Action != "list" || !output.Receipt.ReadOnly || output.Receipt.Persistent || !output.Receipt.RegistryBacked || output.Receipt.Total != 1 {
		t.Fatalf("receipt = %+v, want worktree_list read-only receipt", output.Receipt)
	}
	if got := output.Worktrees[0]; got.Name != "feature/list" || got.Slug != "feature+list" || got.Path == "" || got.Branch == "" {
		t.Fatalf("listed worktree = %+v, want registry record details", got)
	}
	if !strings.HasSuffix(output.RegistryPath, filepath.Join(".elnath", "worktrees", "registry.json")) {
		t.Fatalf("registry path = %q, want managed registry path", output.RegistryPath)
	}
}

func TestWorktreeListToolReportsStatusEvidence(t *testing.T) {
	repo := initGitRepo(t)
	manager := NewManager(repo)
	enter := NewEnterTool(manager)
	list := NewListTool(manager)

	result, err := enter.Execute(context.Background(), json.RawMessage(`{"name":"feature/status"}`))
	if err != nil {
		t.Fatalf("enter Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("enter returned error result: %s", result.Output)
	}
	var entered EnterOutput
	if err := json.Unmarshal([]byte(result.Output), &entered); err != nil {
		t.Fatalf("unmarshal enter output: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entered.Path, "status.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	listedDirty, err := list.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("dirty list Execute error = %v", err)
	}
	if listedDirty.IsError {
		t.Fatalf("dirty list returned error result: %s", listedDirty.Output)
	}
	var dirtyOutput ListOutput
	if err := json.Unmarshal([]byte(listedDirty.Output), &dirtyOutput); err != nil {
		t.Fatalf("unmarshal dirty list output: %v", err)
	}
	if len(dirtyOutput.Worktrees) != 1 {
		t.Fatalf("dirty worktrees = %d, want 1", len(dirtyOutput.Worktrees))
	}
	if got := dirtyOutput.Worktrees[0]; !got.Exists || got.DirtyFiles != 1 || got.AheadCommits != 0 || got.StatusError != "" {
		t.Fatalf("dirty listed status = %+v, want exists with one dirty file", got)
	}

	gitRun(t, entered.Path, "add", "status.txt")
	gitRun(t, entered.Path, "commit", "-m", "status")

	listed, err := list.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("list Execute error = %v", err)
	}
	if listed.IsError {
		t.Fatalf("list returned error result: %s", listed.Output)
	}
	var output ListOutput
	if err := json.Unmarshal([]byte(listed.Output), &output); err != nil {
		t.Fatalf("unmarshal list output: %v", err)
	}
	if len(output.Worktrees) != 1 {
		t.Fatalf("worktrees = %d, want 1", len(output.Worktrees))
	}
	got := output.Worktrees[0]
	if !got.Exists || got.DirtyFiles != 0 || got.AheadCommits != 1 || got.StatusError != "" {
		t.Fatalf("listed status = %+v, want exists clean and one ahead commit", got)
	}
}

func TestWorktreePruneToolDefaultsToDryRun(t *testing.T) {
	repo := initGitRepo(t)
	manager := NewManager(repo)
	enter := NewEnterTool(manager)
	prune := NewPruneTool(manager)

	result, err := enter.Execute(context.Background(), json.RawMessage(`{"name":"stale"}`))
	if err != nil {
		t.Fatalf("enter Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("enter returned error result: %s", result.Output)
	}
	var entered EnterOutput
	if err := json.Unmarshal([]byte(result.Output), &entered); err != nil {
		t.Fatalf("unmarshal enter output: %v", err)
	}
	if err := os.RemoveAll(entered.Path); err != nil {
		t.Fatalf("remove worktree path: %v", err)
	}

	dryRun, err := prune.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("prune Execute error = %v", err)
	}
	if dryRun.IsError {
		t.Fatalf("prune returned error result: %s", dryRun.Output)
	}
	var output PruneOutput
	if err := json.Unmarshal([]byte(dryRun.Output), &output); err != nil {
		t.Fatalf("unmarshal prune output: %v", err)
	}
	if !output.DryRun || output.StaleCount != 1 || output.RemovedCount != 0 || output.KeptCount != 1 {
		t.Fatalf("dry-run output = %+v, want one stale entry kept", output)
	}
	if output.Receipt.Tool != PruneToolName || output.Receipt.Action != "prune" || !output.Receipt.ReadOnly || output.Receipt.Persistent || !output.Receipt.RegistryBacked || !output.Receipt.DryRun {
		t.Fatalf("receipt = %+v, want dry-run prune read-only receipt", output.Receipt)
	}
	registry, err := manager.readRegistry(context.Background())
	if err != nil {
		t.Fatalf("readRegistry: %v", err)
	}
	if len(registry.Worktrees) != 1 {
		t.Fatalf("registry worktrees after dry-run = %d, want 1", len(registry.Worktrees))
	}
}

func TestWorktreePruneToolRemovesMissingRegistryEntries(t *testing.T) {
	repo := initGitRepo(t)
	manager := NewManager(repo)
	enter := NewEnterTool(manager)
	prune := NewPruneTool(manager)

	result, err := enter.Execute(context.Background(), json.RawMessage(`{"name":"stale"}`))
	if err != nil {
		t.Fatalf("enter Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("enter returned error result: %s", result.Output)
	}
	var entered EnterOutput
	if err := json.Unmarshal([]byte(result.Output), &entered); err != nil {
		t.Fatalf("unmarshal enter output: %v", err)
	}
	if err := os.RemoveAll(entered.Path); err != nil {
		t.Fatalf("remove worktree path: %v", err)
	}

	removed, err := prune.Execute(context.Background(), json.RawMessage(`{"dry_run":false}`))
	if err != nil {
		t.Fatalf("prune Execute error = %v", err)
	}
	if removed.IsError {
		t.Fatalf("prune returned error result: %s", removed.Output)
	}
	var output PruneOutput
	if err := json.Unmarshal([]byte(removed.Output), &output); err != nil {
		t.Fatalf("unmarshal prune output: %v", err)
	}
	if output.DryRun || output.StaleCount != 1 || output.RemovedCount != 1 || output.KeptCount != 0 {
		t.Fatalf("prune output = %+v, want one stale entry removed", output)
	}
	registry, err := manager.readRegistry(context.Background())
	if err != nil {
		t.Fatalf("readRegistry: %v", err)
	}
	if len(registry.Worktrees) != 0 {
		t.Fatalf("registry worktrees after prune = %d, want 0", len(registry.Worktrees))
	}
}

func TestWorktreeRunToolExecutesInsideManagedWorktree(t *testing.T) {
	repo := initGitRepo(t)
	manager := NewManager(repo)
	enter := NewEnterTool(manager)
	run := NewRunTool(manager, tools.NewDirectRunner())

	result, err := enter.Execute(context.Background(), json.RawMessage(`{"name":"feature/run"}`))
	if err != nil {
		t.Fatalf("enter Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("enter returned error result: %s", result.Output)
	}

	ran, err := run.Execute(context.Background(), json.RawMessage(`{"name":"feature/run","command":"pwd; test -f README.md; echo ran > run.txt"}`))
	if err != nil {
		t.Fatalf("run Execute error = %v", err)
	}
	if ran.IsError {
		t.Fatalf("run returned error result: %s", ran.Output)
	}
	var output RunOutput
	if err := json.Unmarshal([]byte(ran.Output), &output); err != nil {
		t.Fatalf("unmarshal run output: %v", err)
	}
	if output.Name != "feature/run" || output.Path == "" || output.Runner != "direct" || output.IsError {
		t.Fatalf("run output = %+v, want successful direct run in managed worktree", output)
	}
	if output.Receipt.Tool != RunToolName || output.Receipt.Action != "run" || output.Receipt.ReadOnly || !output.Receipt.Persistent || !output.Receipt.ExecutionAvailable || output.Receipt.Runner != "direct" || output.Receipt.IsError {
		t.Fatalf("receipt = %+v, want worktree_run command receipt", output.Receipt)
	}
	if _, err := os.Stat(filepath.Join(output.Path, "run.txt")); err != nil {
		t.Fatalf("run.txt not written inside worktree: %v", err)
	}
	if strings.Contains(output.CommandOutput, repo+string(os.PathSeparator)+"sessions") {
		t.Fatalf("command output used session workspace, got %q", output.CommandOutput)
	}
}

func TestWorktreeRunToolRejectsWorkingDirEscape(t *testing.T) {
	repo := initGitRepo(t)
	manager := NewManager(repo)
	enter := NewEnterTool(manager)
	run := NewRunTool(manager, tools.NewDirectRunner())

	result, err := enter.Execute(context.Background(), json.RawMessage(`{"name":"feature/escape"}`))
	if err != nil {
		t.Fatalf("enter Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("enter returned error result: %s", result.Output)
	}

	ran, err := run.Execute(context.Background(), json.RawMessage(`{"name":"feature/escape","command":"pwd","working_dir":".."}`))
	if err != nil {
		t.Fatalf("run Execute error = %v", err)
	}
	if !ran.IsError || !strings.Contains(ran.Output, "invalid working_dir") {
		t.Fatalf("run result = %+v, want working_dir escape error", ran)
	}
}

func TestWorktreeToolMetadata(t *testing.T) {
	for _, tool := range []tools.Tool{NewEnterTool(nil), NewPruneTool(nil), NewExitTool(nil)} {
		if tool.IsConcurrencySafe(nil) {
			t.Fatalf("%s should not be concurrency-safe", tool.Name())
		}
		if tool.Reversible() {
			t.Fatalf("%s should not be reversible", tool.Name())
		}
		if got := tool.Scope(nil); !got.Persistent || got.Network || len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 {
			t.Fatalf("%s Scope() = %+v, want persistent-only scope", tool.Name(), got)
		}
		if tool.ShouldCancelSiblingsOnError() {
			t.Fatalf("%s should not cancel siblings", tool.Name())
		}
	}
	run := NewRunTool(nil, nil)
	if run.IsConcurrencySafe(nil) {
		t.Fatal("worktree_run should not be concurrency-safe")
	}
	if run.Reversible() {
		t.Fatal("worktree_run should not be reversible")
	}
	if got := run.Scope(nil); !got.Persistent || !got.Network || len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 {
		t.Fatalf("worktree_run Scope() = %+v, want persistent network scope", got)
	}
	if run.ShouldCancelSiblingsOnError() {
		t.Fatal("worktree_run should not cancel siblings")
	}
	if !tools.ShouldDeferToolSchema(run) {
		t.Fatal("worktree_run should defer initial schema")
	}
}

func TestWorktreeListToolMetadata(t *testing.T) {
	tool := NewListTool(nil)
	if !tool.IsConcurrencySafe(nil) {
		t.Fatal("worktree_list should be concurrency-safe")
	}
	if !tool.Reversible() {
		t.Fatal("worktree_list should be reversible")
	}
	if got := tool.Scope(nil); got.Persistent || got.Network || len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 {
		t.Fatalf("worktree_list Scope() = %+v, want empty read-only scope", got)
	}
	if tool.ShouldCancelSiblingsOnError() {
		t.Fatal("worktree_list should not cancel siblings")
	}
	if !tools.ShouldDeferToolSchema(tool) {
		t.Fatal("worktree_list should defer initial schema")
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, "init")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	gitRun(t, repo, "add", "README.md")
	gitRun(t, repo, "commit", "-m", "initial")
	return repo
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}
