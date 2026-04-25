package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// B3b-1.5 GitTool routing tests — verify git no longer talks to
// exec.CommandContext directly and instead inherits the BashRunner
// boundary (clean env, HOME pinning, shell-quoted argv, telemetry).

func TestGitTool_RoutesThroughBashRunner(t *testing.T) {
	fake := &fakeBashRunner{
		runResult: BashRunResult{
			Output:         "BASH RESULT\nstatus: success\nfake-git-marker\n",
			IsError:        false,
			Classification: "success",
		},
	}
	gt := NewGitToolWithRunner(NewPathGuard(t.TempDir(), nil), fake)

	ctx := WithSessionID(context.Background(), "sess-A")
	res, err := gt.Execute(ctx, mustMarshal(t, map[string]any{
		"subcommand": "status",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success: %s", res.Output)
	}
	if !strings.Contains(res.Output, "fake-git-marker") {
		t.Fatalf("expected runner-supplied output to surface; got %q", res.Output)
	}

	fake.mu.Lock()
	calls := fake.runCalls
	fake.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("runner.Run called %d times, want 1", len(calls))
	}
	cmd := calls[0].Command
	if !strings.HasPrefix(cmd, "git ") {
		t.Errorf("expected command to start with 'git ', got %q", cmd)
	}
	if !strings.Contains(cmd, "'status'") {
		t.Errorf("expected status arg to be shell-quoted, got %q", cmd)
	}
	if !strings.Contains(cmd, "'--short'") {
		t.Errorf("expected --short to be shell-quoted, got %q", cmd)
	}
}

func TestGitTool_DiffPropagatesArgsThroughRunner(t *testing.T) {
	fake := &fakeBashRunner{
		runResult: BashRunResult{Output: "ok", IsError: false, Classification: "success"},
	}
	gt := NewGitToolWithRunner(NewPathGuard(t.TempDir(), nil), fake)

	ctx := WithSessionID(context.Background(), "sess-A")
	if _, err := gt.Execute(ctx, mustMarshal(t, map[string]any{
		"subcommand": "diff",
		"args":       []string{"--stat", "HEAD~1"},
	})); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	fake.mu.Lock()
	calls := fake.runCalls
	fake.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("runner.Run called %d times, want 1", len(calls))
	}
	cmd := calls[0].Command
	for _, want := range []string{"'diff'", "'--stat'", "'HEAD~1'"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("expected %q in command, got %q", want, cmd)
		}
	}
}

func TestGitTool_ShellQuotingPreventsInjection(t *testing.T) {
	dir := setupGitRepo(t)

	// Sentinel sits OUTSIDE the repo. If shell injection succeeded the
	// hostile diff arg would create it; with proper quoting git just
	// receives a literal path and reports the path as unknown.
	sentinelDir := t.TempDir()
	sentinel := filepath.Join(sentinelDir, "injection-sentinel")

	gt := NewGitTool(NewPathGuard(dir, nil))
	hostile := fmt.Sprintf("--; touch %s", sentinel)
	if _, err := gt.Execute(context.Background(), mustMarshal(t, map[string]any{
		"subcommand": "diff",
		"args":       []string{hostile},
	})); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatalf("shell injection sentinel was triggered — argv quoting did not prevent injection")
	}
}

func TestGitTool_ShellQuotingPreservesPathsWithSpaces(t *testing.T) {
	dir := setupGitRepo(t)

	// Create a file with an embedded space and stage it.
	target := filepath.Join(dir, "name with spaces.txt")
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	gt := NewGitTool(NewPathGuard(dir, nil))
	res, err := gt.Execute(context.Background(), mustMarshal(t, map[string]any{
		"subcommand": "status",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("status should succeed: %s", res.Output)
	}
	// git status --short with the spaced file untracked must surface the
	// literal name (likely quoted by git itself, e.g. "?? \"name with spaces.txt\""),
	// proving the file reached the repo via the runner without truncation
	// at the space.
	if !strings.Contains(res.Output, "spaces") {
		t.Errorf("expected spaced filename in status output, got: %s", res.Output)
	}
}

func TestGitTool_DoesNotLoadHostHomeGitconfig(t *testing.T) {
	// Stage a fake HOME with a .gitconfig that runs a sentinel via
	// core.fsmonitor. cleanBashEnv pins HOME to the session workspace, so
	// git running through DirectRunner must not see this fsmonitor entry.
	fakeHome := t.TempDir()
	sentinelDir := t.TempDir()
	sentinel := filepath.Join(sentinelDir, "fsmonitor-sentinel")
	gitconfig := fmt.Sprintf("[core]\n\tfsmonitor = sh -c 'touch %s; echo {}'\n", sentinel)
	if err := os.WriteFile(filepath.Join(fakeHome, ".gitconfig"), []byte(gitconfig), 0o644); err != nil {
		t.Fatalf("write fake gitconfig: %v", err)
	}
	t.Setenv("HOME", fakeHome)

	dir := setupGitRepo(t)
	gt := NewGitTool(NewPathGuard(dir, nil))

	if _, err := gt.Execute(context.Background(), mustMarshal(t, map[string]any{
		"subcommand": "status",
	})); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatalf("host HOME .gitconfig fsmonitor was triggered — HOME leakage NOT prevented")
	}
}

func TestGitTool_EmitsTelemetryThroughRunner(t *testing.T) {
	buf := captureSlogOutput(t)

	dir := setupGitRepo(t)
	gt := NewGitTool(NewPathGuard(dir, nil))

	if _, err := gt.Execute(context.Background(), mustMarshal(t, map[string]any{
		"subcommand": "status",
	})); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"runner_name=direct",
		"execution_mode=direct_host_guarded",
		"sandbox_enforced=false",
		"command_len=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("git telemetry missing %q in output:\n%s", want, out)
		}
	}
	// Even via GitTool the full git command must NOT be logged verbatim.
	// Only command_len should ship; tests pass empty payload arg which
	// would otherwise be benign, so we instead assert the bytes matching
	// the well-known shell-quoted form do NOT appear.
	if strings.Contains(out, "'status'") {
		t.Errorf("telemetry leaked the shell-quoted git command:\n%s", out)
	}
}

func TestGitTool_DoesNotImportOsExec(t *testing.T) {
	// Structural assertion: GitTool's source must not link in os/exec
	// directly any more — that import was the route the partner verdict
	// closed. Reading the source via go/format would be overkill; a
	// substring grep on the module file is sufficient.
	bytes, err := os.ReadFile(filepath.FromSlash("git.go"))
	if err != nil {
		t.Skipf("git.go not readable from cwd %s: %v (test is best-effort)", mustGetwd(t), err)
		return
	}
	src := string(bytes)
	if strings.Contains(src, `"os/exec"`) {
		t.Errorf("git.go must not import os/exec directly — route through BashRunner instead")
	}
	if strings.Contains(src, "exec.Command") || strings.Contains(src, "exec.CommandContext") {
		t.Errorf("git.go must not call exec.Command/exec.CommandContext directly")
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return wd
}

// ---------------------------------------------------------------------------
// shellQuoteArg / shellQuoteArgs unit tests
// ---------------------------------------------------------------------------

func TestShellQuoteArg_Cases(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "''"},
		{"simple", "'simple'"},
		{"with space", "'with space'"},
		{"; touch sentinel", "'; touch sentinel'"},
		{"$(touch sentinel)", "'$(touch sentinel)'"},
		{"`touch sentinel`", "'`touch sentinel`'"},
		{"it's", `'it'\''s'`},
		{"--stat", "'--stat'"},
		{"$HOME", "'$HOME'"},
	}
	for _, tc := range cases {
		got := shellQuoteArg(tc.in)
		if got != tc.want {
			t.Errorf("shellQuoteArg(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShellQuoteArgs_RoundTripsThroughBash(t *testing.T) {
	// Verify the quoted output, when fed to bash -c, reproduces the original
	// argv verbatim. This is the practical guarantee that matters: paths
	// with metacharacters survive the shell hop without injection.
	args := []string{"plain", "with space", "; touch /tmp/should-not-exist", "$(touch /tmp/should-not-exist)", "it's"}
	quoted := shellQuoteArgs(args)
	cmd := exec.Command("/bin/bash", "-c", "for a in "+quoted+"; do printf '%s\\n' \"$a\"; done")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %s: %v", out, err)
	}
	got := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(got) != len(args) {
		t.Fatalf("got %d lines, want %d: %v", len(got), len(args), got)
	}
	for i := range args {
		if got[i] != args[i] {
			t.Errorf("arg[%d] = %q after bash round-trip, want %q", i, got[i], args[i])
		}
	}
}

// Compile-time assertion: gitParams remains the only Unmarshal target so
// hostile JSON cannot inject sandbox-bypass fields by piggybacking on git
// schema. Mirrors the bash schema bypass-field test.
func TestGitSchema_DoesNotExposeSandboxBypass(t *testing.T) {
	gt := NewGitTool(NewPathGuard(t.TempDir(), nil))
	schema := strings.ToLower(string(gt.Schema()))
	forbidden := []string{
		"dangerously_disable_sandbox",
		"disable_sandbox",
		"sandbox_mode",
		"allow_unsandboxed",
		"bypass_sandbox",
		"runner",
	}
	for _, f := range forbidden {
		if strings.Contains(schema, f) {
			t.Errorf("git schema must not expose %q (LLM bypass forbidden)", f)
		}
	}
}

// Sanity check that the json.Marshal payload constructed by GitTool.run
// does NOT include any sandbox-bypass key. This locks in the assumption
// that GitTool builds its bash payload from only command + timeout_ms.
func TestGitTool_PayloadHasNoBypassKeys(t *testing.T) {
	fake := &fakeBashRunner{
		runResult: BashRunResult{Output: "ok", IsError: false, Classification: "success"},
	}
	gt := NewGitToolWithRunner(NewPathGuard(t.TempDir(), nil), fake)

	if _, err := gt.Execute(context.Background(), mustMarshal(t, map[string]any{
		"subcommand": "status",
	})); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	fake.mu.Lock()
	calls := fake.runCalls
	fake.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	// The fake records the BashRunRequest the runner saw. Re-marshal it
	// and confirm no bypass keys are present.
	asJSON, _ := json.Marshal(calls[0])
	for _, f := range []string{
		"dangerously_disable_sandbox", "disable_sandbox",
		"sandbox_mode", "allow_unsandboxed", "bypass_sandbox",
	} {
		if strings.Contains(strings.ToLower(string(asJSON)), f) {
			t.Errorf("BashRunRequest leaked bypass key %q: %s", f, asJSON)
		}
	}
}
