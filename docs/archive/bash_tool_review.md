# Bash Tool external review source bundle

Historical archive for v40 Bash Tool hardening work.

This document preserves the source/review bundle used for external GPT audit.
It is not canonical implementation documentation.
Current implementation should be read from the repository source files.

Primary use:
- provenance for Bash Tool v2 roadmap
- reference for P0 host-process hardening findings
- future audit context

---

# Elnath `bash` Tool — Full Source for Review / Rewrite

## Context

**Project:** Elnath — autonomous AI-assistant platform in Go (Go 1.25+, no CGo).
An agent loop calls tools; `BashTool` is one of those tools. The agent hands
the tool a JSON blob with a shell command and expects back a `*Result`
(`{Output, IsError}`) that it will feed back to the LLM as `tool_result`.

**Goal of this review:** make the bash tool *very strong*. Re-write is welcome.
Specifically interested in:

- Robustness of the AST-based safety analysis (false negatives / false positives, bypasses).
- Process lifecycle: timeout, cancellation, zombie / orphan processes, stderr interleaving.
- Output handling: truncation fairness (stdout vs stderr), byte-cap vs line-cap, ANSI, binary.
- Working-directory isolation: session sandbox semantics, symlink escape.
- Concurrency: `IsConcurrencySafe=false` and `Scope.WritePaths = [workDir]` — are we over-conservative? Under-conservative?
- Error reporting format the LLM consumes: combined stdout+stderr + exit status clarity.
- Defaults: 120s timeout, 600s max, 64 KiB output cap — sane for agent use?
- Missing features a "strong" bash tool should have (e.g. stdin input, env allowlist, background mode, streaming, PID group kill, resource limits via `setrlimit` / cgroups).

**External dependency:** `mvdan.cc/sh/v3/syntax` (Go shell parser / AST).

**Platform:** runs on macOS + Linux. The binary is `bash` (POSIX-ish; we rely on `bash -c`).

**Security model (current):** pre-execution AST walk rejects a hand-picked
blocklist (sudo, dd, `rm -rf /`, system-path writes via cp/mv/touch/mkdir/chmod/chown,
`git push --force main`, output redirection to system paths). Unparseable
commands are allowed through (bash itself reports syntax errors). Everything
else runs with the caller's privileges in the session workspace.

**Workspace model:** `PathGuard` owns a root `workDir`. Each agent session
gets a subdir `workDir/sessions/<sanitized-id>/`; bash is pinned to that
subdir unless the caller passes `working_dir` (resolved relative to the
session subdir). There is **no** chroot / mount namespace — path isolation
is advisory.

---

## 1. `internal/tools/bash.go` — the tool itself

```go
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

const (
	bashDefaultTimeout = 120 * time.Second
	bashMaxTimeout     = 600 * time.Second
)

// BashTool executes shell commands with a timeout and AST-based safety analysis.
type BashTool struct {
	guard          *PathGuard
	defaultTimeout time.Duration
}

// NewBashTool creates a BashTool rooted at workDir with a 120s default timeout.
func NewBashTool(guard *PathGuard) *BashTool {
	return &BashTool{guard: guard, defaultTimeout: bashDefaultTimeout}
}

func (t *BashTool) Name() string { return "bash" }
func (t *BashTool) Description() string {
	return "Execute a shell command in the working directory.\n\nIMPORTANT: Do NOT use bash for tasks that have a dedicated tool:\n- File search: use glob (not find or ls)\n- Content search: use grep (not grep or rg)\n- Read files: use read_file (not cat/head/tail)\n- Edit files: use edit_file (not sed/awk)\n- Write files: use write_file (not echo/cat heredoc)\n\nUsing dedicated tools is faster and lets the user review your work more easily."
}

func (t *BashTool) ArgsTarget() any { return &bashParams{} }

func (t *BashTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"command":     String("The shell command to execute."),
		"timeout_ms":  Int("Timeout in milliseconds (default 120000, max 600000)."),
		"working_dir": String("Working directory for the command (default: tool's workDir)."),
	}, []string{"command"})
}

func (t *BashTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *BashTool) Reversible() bool { return false }

// ShouldCancelSiblingsOnError returns false so non-zero bash exits
// surface to the LLM as tool_result(IsError=true), letting the agent
// recover on the next turn instead of aborting the workflow.
func (t *BashTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *BashTool) Scope(params json.RawMessage) ToolScope {
	var p bashParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ConservativeScope()
	}
	return ToolScope{
		Network:    true,
		Persistent: true,
		WritePaths: []string{t.guard.WorkDir()},
	}
}

type bashParams struct {
	Command    string `json:"command"`
	TimeoutMs  int    `json:"timeout_ms"`
	WorkingDir string `json:"working_dir"`
}

func (t *BashTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p bashParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	if strings.TrimSpace(p.Command) == "" {
		return ErrorResult("command must not be empty"), nil
	}

	if dangerous, reason := AnalyzeCommandSafety(p.Command); dangerous {
		return ErrorResult(fmt.Sprintf("command blocked: %s", reason)), nil
	}

	timeout := t.defaultTimeout
	if p.TimeoutMs > 0 {
		d := time.Duration(p.TimeoutMs) * time.Millisecond
		if d > bashMaxTimeout {
			d = bashMaxTimeout
		}
		timeout = d
	}

	sessionDir, sessErr := t.guard.EnsureSessionWorkDir(SessionIDFrom(ctx))
	if sessErr != nil {
		return ErrorResult(fmt.Sprintf("session workspace: %v", sessErr)), nil
	}
	workDir := sessionDir
	if p.WorkingDir != "" {
		resolved, err := t.guard.ResolveIn(sessionDir, p.WorkingDir)
		if err != nil {
			return ErrorResult(fmt.Sprintf("invalid working_dir: %v", err)), nil
		}
		workDir = resolved
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "bash", "-c", p.Command)
	cmd.Dir = workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	combined := stdout.String()
	if stderr.Len() > 0 {
		if combined != "" {
			combined += "\n"
		}
		combined += stderr.String()
	}
	combined = truncateOutput(combined, toolMaxOutputBytes)

	if execCtx.Err() == context.DeadlineExceeded {
		return ErrorResult(fmt.Sprintf("command timed out after %s", timeout)), nil
	}
	if err != nil {
		msg := combined
		if msg == "" {
			msg = err.Error()
		}
		return &Result{Output: msg, IsError: true}, nil
	}

	return SuccessResult(combined), nil
}

// analyzeCommand parses the shell command AST and checks for dangerous patterns.
// Returns dangerous=true with a reason if a blocked pattern is found.
func AnalyzeCommandSafety(command string) (dangerous bool, reason string) {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "cmd")
	if err != nil {
		// Unparseable commands are allowed — bash will report the syntax error.
		return false, ""
	}

	syntax.Walk(f, func(node syntax.Node) bool {
		if dangerous {
			return false
		}
		if stmt, ok := node.(*syntax.Stmt); ok {
			if hasDangerousOutputRedirect(stmt.Redirs) {
				dangerous, reason = true, "writing to system paths via shell redirection is not allowed"
				return false
			}
			return true
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}

		cmdName, args := unwrapCommand(call)
		switch cmdName {
		case "sudo":
			dangerous, reason = true, "sudo invocation is not allowed"
			return false
		case "dd":
			dangerous, reason = true, "dd command is not allowed"
			return false
		case "rm":
			if hasRmRfRoot(args) {
				dangerous, reason = true, "rm -rf on root or home is not allowed"
				return false
			}
		case "cp", "mv":
			if target := writeDestination(cmdName, args); target != "" && isSystemPath(target) {
				dangerous, reason = true, fmt.Sprintf("%s to system paths is not allowed", cmdName)
				return false
			}
		case "touch", "mkdir":
			if hasSystemPath(writeTargets(cmdName, args)) {
				dangerous, reason = true, fmt.Sprintf("%s on system paths is not allowed", cmdName)
				return false
			}
		case "chmod", "chown":
			if hasSystemPath(args) {
				dangerous, reason = true, fmt.Sprintf("%s on system paths is not allowed", cmdName)
				return false
			}
		case "git":
			if isGitForcePushMain(args) {
				dangerous, reason = true, "git push --force to main/master is not allowed"
				return false
			}
		}
		return true
	})

	return dangerous, reason
}

func analyzeCommand(command string) (dangerous bool, reason string) {
	return AnalyzeCommandSafety(command)
}

// wordText returns a conservative string representation of a shell word.
// Unknown dynamic expansions return an empty string so callers fail closed.
func wordText(word *syntax.Word) string {
	if len(word.Parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, part := range word.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				switch q := inner.(type) {
				case *syntax.Lit:
					b.WriteString(q.Value)
				case *syntax.ParamExp:
					if q.Param.Value == "" {
						return ""
					}
					b.WriteString("$")
					b.WriteString(q.Param.Value)
				default:
					return ""
				}
			}
		case *syntax.ParamExp:
			if p.Param.Value == "" {
				return ""
			}
			b.WriteString("$")
			b.WriteString(p.Param.Value)
		default:
			return ""
		}
	}
	return b.String()
}

func unwrapCommand(call *syntax.CallExpr) (string, []string) {
	args := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		args = append(args, wordText(arg))
	}
	return unwrapArgs(args)
}

func unwrapArgs(args []string) (string, []string) {
	current := args
	for len(current) > 0 {
		switch current[0] {
		case "command", "time", "nohup":
			current = stripOptionalDoubleDash(current[1:])
		case "nice":
			current = stripOptionalDoubleDash(skipNiceArgs(current))
		case "timeout":
			current = stripOptionalDoubleDash(skipTimeoutArgs(current))
		case "env":
			current = stripEnvArgs(current)
		default:
			return current[0], current[1:]
		}
	}
	return "", nil
}

func stripOptionalDoubleDash(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}

func skipNiceArgs(args []string) []string {
	if len(args) < 2 {
		return nil
	}
	if args[1] == "-n" && len(args) >= 4 {
		return args[3:]
	}
	if strings.HasPrefix(args[1], "-") {
		if _, err := strconv.Atoi(strings.TrimPrefix(args[1], "-")); err == nil {
			return args[2:]
		}
	}
	return args[1:]
}

func skipTimeoutArgs(args []string) []string {
	i := 1
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "--":
			i++
			goto duration
		case arg == "--foreground" || arg == "--preserve-status" || arg == "--verbose" || arg == "-v":
			i++
		case strings.HasPrefix(arg, "--kill-after=") || strings.HasPrefix(arg, "--signal="):
			i++
		case arg == "--kill-after" || arg == "--signal" || arg == "-k" || arg == "-s":
			i += 2
		case strings.HasPrefix(arg, "-k") || strings.HasPrefix(arg, "-s"):
			i++
		case strings.HasPrefix(arg, "-"):
			return args
		default:
			goto duration
		}
	}

duration:
	if i >= len(args) {
		return nil
	}
	duration := args[i]
	if _, err := time.ParseDuration(duration); err != nil {
		if _, err := strconv.Atoi(duration); err != nil {
			return args
		}
	}
	return args[i+1:]
}

func stripEnvArgs(args []string) []string {
	i := 1
	for i < len(args) {
		arg := args[i]
		if arg == "--" {
			i++
			break
		}
		if strings.Contains(arg, "=") && !strings.HasPrefix(arg, "-") {
			i++
			continue
		}
		if arg == "-i" || arg == "--ignore-environment" {
			i++
			continue
		}
		if arg == "-u" || arg == "--unset" {
			i += 2
			continue
		}
		if strings.HasPrefix(arg, "--unset=") {
			i++
			continue
		}
		break
	}
	return args[i:]
}

func positionalArgs(args []string, flagsWithValue map[string]struct{}) []string {
	var positional []string
	afterDoubleDash := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if afterDoubleDash {
			positional = append(positional, arg)
			continue
		}
		if arg == "--" {
			afterDoubleDash = true
			continue
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}

		name := arg
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			name = arg[:eq]
		}
		if _, ok := flagsWithValue[name]; ok && !strings.Contains(arg, "=") && i+1 < len(args) {
			i++
		}
	}

	return positional
}

func writeDestination(cmdName string, args []string) string {
	switch cmdName {
	case "cp", "mv":
		for i := 0; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "-t" || arg == "--target-directory":
				if i+1 < len(args) {
					return args[i+1]
				}
				return ""
			case strings.HasPrefix(arg, "--target-directory="):
				return strings.TrimPrefix(arg, "--target-directory=")
			}
		}

		positional := positionalArgs(args, map[string]struct{}{
			"-t":                 {},
			"--target-directory": {},
			"-S":                 {},
			"--suffix":           {},
			"--backup":           {},
		})
		if len(positional) < 2 {
			return ""
		}
		return positional[len(positional)-1]
	default:
		return ""
	}
}

func writeTargets(cmdName string, args []string) []string {
	switch cmdName {
	case "touch":
		return positionalArgs(args, map[string]struct{}{
			"-r":          {},
			"--reference": {},
			"-t":          {},
			"-d":          {},
			"--date":      {},
		})
	case "mkdir":
		return positionalArgs(args, map[string]struct{}{
			"-m":        {},
			"--mode":    {},
			"-Z":        {},
			"--context": {},
		})
	default:
		return nil
	}
}

func hasDangerousOutputRedirect(redirs []*syntax.Redirect) bool {
	for _, redir := range redirs {
		if redir == nil || redir.Word == nil || !isOutputRedirection(redir.Op) {
			continue
		}
		if isSystemPath(wordText(redir.Word)) {
			return true
		}
	}
	return false
}

func isOutputRedirection(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrInOut, syntax.DplOut, syntax.RdrClob, syntax.AppClob, syntax.RdrAll, syntax.RdrAllClob, syntax.AppAll, syntax.AppAllClob:
		return true
	default:
		return false
	}
}

// hasRmRfRoot detects destructive recursive removals against critical paths.
func hasRmRfRoot(args []string) bool {
	hasRF := false
	hasRootPath := false
	for _, v := range args {
		if v == "-rf" || v == "-fr" || v == "-r" {
			hasRF = true
		}
		if isDangerousRemovalTarget(v) {
			hasRootPath = true
		}
	}
	return hasRF && hasRootPath
}

func isDangerousRemovalTarget(v string) bool {
	if v == "/" || v == "~" || v == "$HOME" {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && v == home {
		return true
	}
	return isSystemPath(v)
}

// hasSystemPath checks whether any argument looks like a system path.
func hasSystemPath(args []string) bool {
	for _, v := range args {
		if isSystemPath(v) {
			return true
		}
	}
	return false
}

func isSystemPath(v string) bool {
	systemPrefixes := []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/boot", "/sys", "/proc"}
	for _, prefix := range systemPrefixes {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return false
}

// isGitForcePushMain detects `git push --force [remote] main|master`.
func isGitForcePushMain(args []string) bool {
	hasForce := false
	hasPush := false
	hasMain := false
	for _, v := range args {
		switch v {
		case "push":
			hasPush = true
		case "--force", "-f":
			hasForce = true
		case "main", "master":
			hasMain = true
		}
	}
	return hasPush && hasForce && hasMain
}
```

---

## 2. `internal/tools/bash_test.go` — current test coverage

```go
package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func makeBashParams(t *testing.T, command string, extraFields map[string]any) json.RawMessage {
	t.Helper()
	m := map[string]any{"command": command}
	for k, v := range extraFields {
		m[k] = v
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal bash params: %v", err)
	}
	return raw
}

func TestBashExecute(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "echo hello", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if !strings.Contains(res.Output, "hello") {
		t.Errorf("output %q does not contain %q", res.Output, "hello")
	}
}

func TestBashTimeout(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	// 1000 ms timeout, but sleep 10 s — must time out.
	res, err := tool.Execute(context.Background(), makeBashParams(t, "sleep 10", map[string]any{
		"timeout_ms": 1000,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected timeout error result, got success: %s", res.Output)
	}
	if !strings.Contains(res.Output, "timed out") {
		t.Errorf("output %q does not mention timeout", res.Output)
	}
}

func TestBashOutputTruncatesStdout(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "head -c 70000 /dev/zero | tr '\\000' 'a'", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if len(res.Output) > toolMaxOutputBytes {
		t.Fatalf("output len = %d, want <= %d", len(res.Output), toolMaxOutputBytes)
	}
	if !strings.Contains(res.Output, "output truncated") {
		t.Fatalf("expected truncation marker, got %q", res.Output[len(res.Output)-80:])
	}
}

func TestBashOutputTruncatesCombinedStreams(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "head -c 70000 /dev/zero | tr '\\000' 'b' 1>&2", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if len(res.Output) > toolMaxOutputBytes {
		t.Fatalf("output len = %d, want <= %d", len(res.Output), toolMaxOutputBytes)
	}
	if !strings.Contains(res.Output, "output truncated") {
		t.Fatalf("expected truncation marker, got %q", res.Output[len(res.Output)-80:])
	}
}

func TestBashWorkingDir(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil)) // tool's own default workDir is irrelevant here

	// Resolve the real path because t.TempDir() may return a symlink on macOS.
	realDir, err := os.Lstat(dir)
	_ = realDir
	if err != nil {
		t.Fatalf("stat temp dir: %v", err)
	}

	res, err := tool.Execute(context.Background(), makeBashParams(t, "pwd", map[string]any{
		"working_dir": dir,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}

	// pwd may resolve symlinks differently on macOS (/private/var vs /var);
	// compare the base name to keep the test portable.
	gotTrimmed := strings.TrimSpace(res.Output)
	if !strings.HasSuffix(gotTrimmed, strings.TrimRight(dir, "/")) &&
		!strings.Contains(gotTrimmed, strings.TrimLeft(dir, "/")) {
		// Fallback: just check the last path component matches.
		wantBase := dir[strings.LastIndex(dir, "/")+1:]
		if !strings.HasSuffix(gotTrimmed, wantBase) {
			t.Errorf("pwd output %q does not match working_dir %q", gotTrimmed, dir)
		}
	}
}

func TestBashEmptyCommand(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "   ", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for empty command, got success: %s", res.Output)
	}
}

func TestBashInvalidParams(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), json.RawMessage(`not-valid-json`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for invalid JSON params, got success: %s", res.Output)
	}
}

func TestBashAccessors(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	if tool.Name() != "bash" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "bash")
	}
	if tool.Description() == "" {
		t.Error("Description() returned empty string")
	}
	schema := tool.Schema()
	if len(schema) == 0 {
		t.Error("Schema() returned empty")
	}
}

func TestAnalyzeCommand(t *testing.T) {
	cases := []struct {
		command   string
		dangerous bool
		reason    string
	}{
		{command: "echo hello", dangerous: false},
		{command: "sudo rm -rf /", dangerous: true},
		{command: "dd if=/dev/zero of=/dev/sda", dangerous: true},
		{command: "rm -rf /", dangerous: true},
		{command: "rm -rf ~", dangerous: true},
		{command: "rm -rf \"$HOME\"", dangerous: true},
		{command: "rm -fr /", dangerous: true},
		{command: "timeout 5 rm -rf /", dangerous: true},
		{command: "rm file.txt", dangerous: false},
		{command: "cp ./file /etc/passwd", dangerous: true},
		{command: "timeout 5 cp ./file /etc/passwd", dangerous: true},
		{command: "cp /etc/hosts ./hosts.copy", dangerous: false},
		{command: "mv ./tool /usr/local/bin/tool", dangerous: true},
		{command: "touch /etc/passwd", dangerous: true},
		{command: "touch -r /etc/hosts ./local-copy", dangerous: false},
		{command: "mkdir -p /usr/local/share/test", dangerous: true},
		{command: "chmod 777 /etc/passwd", dangerous: true},
		{command: "chmod 777 \"/etc/passwd\"", dangerous: true},
		{command: "chown root /usr/bin/test", dangerous: true},
		{command: "chmod 644 myfile.txt", dangerous: false},
		{command: "echo hi > /etc/passwd", dangerous: true},
		{command: "cat < /etc/passwd", dangerous: false},
		{command: "git push --force origin main", dangerous: true},
		{command: "git push --force origin feature", dangerous: false},
		{command: "git push origin main", dangerous: false},
		{command: "git push -f origin master", dangerous: true},
		{command: "(((", dangerous: false}, // unparseable — bash will report the error
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.command, func(t *testing.T) {
			dangerous, reason := analyzeCommand(tc.command)
			if dangerous != tc.dangerous {
				t.Errorf("analyzeCommand(%q) dangerous=%v, want %v (reason=%q)",
					tc.command, dangerous, tc.dangerous, reason)
			}
			if tc.dangerous && reason == "" {
				t.Errorf("analyzeCommand(%q) returned dangerous=true but empty reason", tc.command)
			}
		})
	}
}
```

---

## 3. Supporting types & helpers (same `tools` package)

These are referenced by `bash.go` and you'll need them to reason about
contracts and possible re-writes.

### 3.1 `tool.go` — `Tool` interface + `Result`

```go
package tools

import (
	"context"
	"encoding/json"
)

// Tool is the interface all executable tools must implement.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, params json.RawMessage) (*Result, error)

	// IsConcurrencySafe reports whether this invocation can run in parallel
	// with OTHER concurrency-safe invocations without any ordering guarantees.
	// The predicate is self-referential: a true return means "I have no
	// observable side-effects that need ordering vs other safe calls".
	// Cross-call collision (same write path, same DB row) is the partitioner's
	// job — it uses Scope() for that, not this method.
	// Params may be nil or malformed; implementations MUST tolerate that and
	// fall back to a conservative (false) default.
	IsConcurrencySafe(params json.RawMessage) bool

	// Reversible reports whether the tool's effect can be undone by a
	// subsequent call (a read has nothing to undo → true; a file overwrite
	// loses prior content → false). This is a static property of the tool,
	// not of the invocation.
	Reversible() bool

	// Scope returns the read/write/network/persistent footprint of this
	// specific invocation. File paths MUST be absolute (resolved via
	// PathGuard when applicable) so partitioners can compare them byte-wise.
	// Params may be nil or malformed; implementations MUST return
	// ConservativeScope() in that case so callers fail closed.
	Scope(params json.RawMessage) ToolScope

	// ShouldCancelSiblingsOnError reports whether an execution error from this
	// tool should cancel other goroutines sharing the same batch.
	ShouldCancelSiblingsOnError() bool
}

// Executor is the narrow interface the agent uses to execute tools.
type Executor interface {
	Execute(ctx context.Context, name string, params json.RawMessage) (*Result, error)
}

// Result holds the output of a tool execution.
type Result struct {
	Output  string
	IsError bool
}

// ErrorResult returns a Result that signals a tool execution failure.
func ErrorResult(msg string) *Result {
	return &Result{Output: msg, IsError: true}
}

// SuccessResult returns a Result that signals successful tool execution.
func SuccessResult(output string) *Result {
	return &Result{Output: output, IsError: false}
}
```

### 3.2 `scope.go` — `ToolScope` + conservative default

```go
package tools

// ToolScope describes the read / write / network / persistence footprint of a
// single tool invocation. All slices are treated as immutable after return —
// callers MUST NOT mutate them.
//
// Semantics:
//   - ReadPaths: absolute paths this call may read. Empty slice means "no
//     file reads". An entry equal to the guard's workDir means "any file under
//     workDir".
//   - WritePaths: absolute paths this call may write. Used by the LBB3
//     partitioner for file-level lock and by PathGuard for CheckScope.
//   - Network: true if the call touches the network (HTTP, DNS, etc).
//   - Persistent: true if the call mutates external state that survives the
//     process (DB writes, file writes, git commits, remote RPC). Reads from
//     persistent stores are NOT persistent=true.
type ToolScope struct {
	ReadPaths  []string
	WritePaths []string
	Network    bool
	Persistent bool
}

// ConservativeScope is the fail-closed default used when params cannot be
// parsed. Treat as "I touch everything".
func ConservativeScope() ToolScope {
	return ToolScope{Network: true, Persistent: true}
}
```

### 3.3 `sessionctx.go` — session id propagation

```go
package tools

import "context"

// sessionIDContextKey keys the active session id on a context. Workspace-aware
// tools (bash, file, git) read this to derive an isolated per-session working
// directory via PathGuard.EnsureSessionWorkDir, preventing cross-session
// artifact contamination observed in dogfood session 4 (FU-WorkspaceScope).
type sessionIDContextKey struct{}

// WithSessionID returns a derived context tagged with the given session id.
// An empty id is preserved as-is so downstream callers can detect "no
// session" and fall back to the root WorkDir.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDContextKey{}, sessionID)
}

// SessionIDFrom returns the session id stored on ctx, or "" when no session
// is bound. The empty value intentionally maps to root-WorkDir behavior in
// EnsureSessionWorkDir, preserving legacy callers that never set the key.
func SessionIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(sessionIDContextKey{}).(string); ok {
		return v
	}
	return ""
}
```

### 3.4 `pathguard.go` — workspace sandbox & write-deny enforcement

```go
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stello/elnath/internal/userfacingerr"
)

// PathGuard resolves tool paths and enforces write-deny rules.
// Read operations are unrestricted. Write operations are blocked
// for paths under any protected directory.
type PathGuard struct {
	workDir        string
	protectedPaths []string
	homeDir        string
}

// NewPathGuard creates a PathGuard with the given working directory
// and write-protected paths. Protected paths are expanded and cleaned.
func NewPathGuard(workDir string, protectedPaths []string) *PathGuard {
	home, _ := os.UserHomeDir()
	cleaned := make([]string, 0, len(protectedPaths))
	for _, p := range protectedPaths {
		p = expandHome(home, p)
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		cleaned = append(cleaned, filepath.Clean(p))
	}
	return &PathGuard{
		workDir:        workDir,
		protectedPaths: cleaned,
		homeDir:        home,
	}
}

// WorkDir returns the guard's default working directory.
func (g *PathGuard) WorkDir() string { return g.workDir }

// sessionWorkDirSubdir is the subdirectory under the root WorkDir that holds
// per-session workspaces. Keeping sessions under a dedicated subdir keeps the
// root cleanly separable from legacy files and per-project artifacts.
const sessionWorkDirSubdir = "sessions"

// SessionWorkDir returns the workspace path for a given session. An empty
// sessionID falls back to the root WorkDir, preserving legacy callers. The
// session id is sanitized so a malicious id cannot escape the root.
//
// This is a pure path computation; use EnsureSessionWorkDir when the directory
// must exist on disk.
func (g *PathGuard) SessionWorkDir(sessionID string) string {
	if sessionID == "" {
		return g.workDir
	}
	return filepath.Join(g.workDir, sessionWorkDirSubdir, sanitizeSessionID(sessionID))
}

// EnsureSessionWorkDir returns the session workspace path and creates the
// directory (with parents) if it does not yet exist. An empty sessionID
// returns the root WorkDir without touching the filesystem.
func (g *PathGuard) EnsureSessionWorkDir(sessionID string) (string, error) {
	dir := g.SessionWorkDir(sessionID)
	if sessionID == "" {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create session workspace %q: %w", dir, err)
	}
	return dir, nil
}

// PurgeSessionWorkDir removes the per-session subdir and its contents. The
// call is idempotent: empty session ids and missing directories are no-ops.
// As a safety net the resolved path must live under <root>/sessions/; any
// resolved path that escapes (e.g. via a malformed sanitize result) is
// refused so a stray sid can never wipe the project root.
func (g *PathGuard) PurgeSessionWorkDir(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	dir := g.SessionWorkDir(sessionID)
	sessionsRoot := filepath.Join(g.workDir, sessionWorkDirSubdir)
	rel, err := filepath.Rel(sessionsRoot, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("refusing to purge %q: outside session root %q", dir, sessionsRoot)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("purge session workspace %q: %w", dir, err)
	}
	return nil
}

// sanitizeSessionID strips path separators and traversal segments so the
// returned id is always a single, safe directory name.
func sanitizeSessionID(sessionID string) string {
	cleaned := filepath.Base(sessionID)
	cleaned = strings.ReplaceAll(cleaned, "..", "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" || cleaned == "." {
		return "_invalid"
	}
	return cleaned
}

// Resolve expands ~ and converts rawPath to an absolute, cleaned path.
// Relative paths are resolved against the guard's working directory.
func (g *PathGuard) Resolve(rawPath string) (string, error) {
	return g.ResolveIn(g.workDir, rawPath)
}

// ResolveIn resolves rawPath against an explicit base directory.
func (g *PathGuard) ResolveIn(cwd, rawPath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("empty path")
	}
	p := expandHome(g.homeDir, rawPath)
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p), nil
}

// CheckWrite returns an error if absPath falls under a protected directory.
func (g *PathGuard) CheckWrite(absPath string) error {
	cleaned := filepath.Clean(absPath)
	for _, pp := range g.protectedPaths {
		if cleaned == pp || strings.HasPrefix(cleaned, pp+string(filepath.Separator)) {
			inner := fmt.Errorf("write denied: %q is under protected path %q", absPath, pp)
			return userfacingerr.Wrap(userfacingerr.ELN020, inner, "path guard")
		}
	}
	return nil
}

// CheckScope validates that every write path in scope is allowed under the
// guard's protected-path rules. Read paths are not checked.
func (g *PathGuard) CheckScope(scope ToolScope) error {
	for _, p := range scope.WritePaths {
		if err := g.CheckWrite(p); err != nil {
			return err
		}
	}
	return nil
}

func expandHome(home, p string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
```

### 3.5 `output.go` — truncation

```go
package tools

import "fmt"

const toolMaxOutputBytes = 64 * 1024

func truncateOutput(output string, limit int) string {
	if limit <= 0 || len(output) <= limit {
		return output
	}

	suffix := fmt.Sprintf("\n... [output truncated to %d bytes]\n", limit)
	keep := limit - len(suffix)
	if keep < 0 {
		keep = 0
	}
	return output[:keep] + suffix
}
```

### 3.6 `schema.go` — schema builder helpers

```go
package tools

import "encoding/json"

// Property describes a single JSON Schema property.
type Property struct {
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	Default     interface{} `json:"default,omitempty"`
	Items       *itemsDef   `json:"items,omitempty"`
}

type itemsDef struct {
	Type string `json:"type"`
}

// String creates a string property.
func String(desc string) Property {
	return Property{Type: "string", Description: desc}
}

// StringEnum creates a string property restricted to the given values.
func StringEnum(desc string, values ...string) Property {
	return Property{Type: "string", Description: desc, Enum: values}
}

// Int creates an integer property.
func Int(desc string) Property {
	return Property{Type: "integer", Description: desc}
}

// Bool creates a boolean property.
func Bool(desc string) Property {
	return Property{Type: "boolean", Description: desc}
}

// Array creates an array property whose items have the given type.
func Array(desc string, itemType string) Property {
	return Property{
		Type:        "array",
		Description: desc,
		Items:       &itemsDef{Type: itemType},
	}
}

// Object builds a JSON Schema "object" type from the given properties and
// required field list. Returns a json.RawMessage ready for Tool.Schema().
func Object(properties map[string]Property, required []string) json.RawMessage {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	raw, _ := json.Marshal(schema)
	return raw
}
```

---

## Prompt to GPT (suggested)

> You are reviewing / potentially rewriting Elnath's `BashTool` in Go. The
> goal is a **very strong** agentic bash tool — something you would trust in
> an autonomous loop that may run thousands of commands unattended.
>
> Constraints: Go 1.25+, pure Go (no CGo), macOS + Linux targets, external
> dep `mvdan.cc/sh/v3/syntax` already available. Keep the `Tool` interface
> contract (`Name / Description / Schema / Execute / IsConcurrencySafe /
> Reversible / Scope / ShouldCancelSiblingsOnError`). The `Result` shape
> `{Output, IsError}` is the canonical return to the LLM.
>
> Please produce:
>
> 1. A prioritized list of concrete issues (bugs, bypasses, UX gaps) in the
>    current implementation — cite line numbers / function names.
> 2. A proposed feature set for a "strong" bash tool (stdin, env allowlist,
>    streaming, background / long-running mode, per-command resource limits,
>    PID-group cleanup on timeout, structured exit info, ANSI stripping,
>    binary-safe truncation, rate limiting, etc.).
> 3. A full Go re-write of `bash.go` (and only `bash.go`; keep the
>    supporting types) that implements your recommendations. Include unit
>    tests or at minimum a list of test cases you'd add.
> 4. Explicit call-outs of anything you intentionally leave insecure
>    (e.g. "we don't chroot — out of scope").
