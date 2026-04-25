package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const gitTimeout = 30 * time.Second

// GitTool dispatches git subcommands (status, diff, commit, log, branch).
// Execution is routed through a composed BashTool so git inherits the same
// B3a/B1/B3b-0.5 guardrails as bash: clean env (HOME pinned to the session
// workspace, host HOME / .gitconfig invisible), session-scoped cwd,
// bounded output, process group cleanup, structured BASH RESULT metadata,
// and per-invocation telemetry. Argv is shell-quoted before being handed
// to the runner so user-controlled paths cannot inject shell metacharacters.
type GitTool struct {
	guard    *PathGuard
	bashTool *BashTool
}

// NewGitTool constructs a GitTool whose underlying BashTool uses the
// default DirectRunner backend.
func NewGitTool(guard *PathGuard) *GitTool {
	return &GitTool{guard: guard, bashTool: NewBashTool(guard)}
}

// NewGitToolWithRunner constructs a GitTool whose underlying BashTool
// uses the supplied BashRunner. Used by tests to inject fakes and by
// future substrate composition.
func NewGitToolWithRunner(guard *PathGuard, runner BashRunner) *GitTool {
	return &GitTool{guard: guard, bashTool: NewBashToolWithRunner(guard, runner)}
}

func (t *GitTool) Name() string        { return "git" }
func (t *GitTool) Description() string { return "Run git commands in the repository." }

func (t *GitTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"subcommand": StringEnum("The git subcommand to run.", "status", "diff", "commit", "log", "branch"),
		"args":       Array("Additional arguments for the subcommand.", "string"),
		"message":    String("Commit message (required for 'commit')."),
	}, []string{"subcommand"})
}

func (t *GitTool) IsConcurrencySafe(params json.RawMessage) bool {
	var p gitParams
	if err := json.Unmarshal(params, &p); err != nil {
		return false
	}
	return isReadOnlyGitSubcommand(p.Subcommand)
}

func (t *GitTool) Reversible() bool { return false }

func (t *GitTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *GitTool) Scope(params json.RawMessage) ToolScope {
	var p gitParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ConservativeScope()
	}
	if isReadOnlyGitSubcommand(p.Subcommand) {
		return ToolScope{ReadPaths: []string{t.guard.WorkDir()}}
	}
	return ToolScope{WritePaths: []string{t.guard.WorkDir()}, Persistent: true}
}

type gitParams struct {
	Subcommand string   `json:"subcommand"`
	Args       []string `json:"args"`
	Message    string   `json:"message"`
}

func isReadOnlyGitSubcommand(subcommand string) bool {
	switch subcommand {
	case "status", "diff", "log":
		return true
	default:
		return false
	}
}

func (t *GitTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p gitParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}

	switch p.Subcommand {
	case "status":
		return t.run(ctx, "status", "--short")
	case "diff":
		args := append([]string{"diff"}, p.Args...)
		return t.run(ctx, args...)
	case "commit":
		if strings.TrimSpace(p.Message) == "" {
			return ErrorResult("git commit requires a message"), nil
		}
		args := append([]string{"commit", "-m", p.Message}, p.Args...)
		return t.run(ctx, args...)
	case "log":
		args := []string{"log", "--oneline", "--no-decorate"}
		args = append(args, p.Args...)
		return t.run(ctx, args...)
	case "branch":
		args := append([]string{"branch"}, p.Args...)
		return t.run(ctx, args...)
	default:
		return ErrorResult(fmt.Sprintf("unsupported git subcommand: %s", p.Subcommand)), nil
	}
}

// run hands the git invocation to the composed BashTool. Args are
// shell-quoted so user-supplied paths cannot inject metacharacters; the
// command is interpreted by the same runner as bash, so git inherits
// HOME pinning, env cleaning, output bounding, and telemetry.
func (t *GitTool) run(ctx context.Context, args ...string) (*Result, error) {
	cmdStr := "git " + shellQuoteArgs(args)

	payload, err := json.Marshal(map[string]any{
		"command":    cmdStr,
		"timeout_ms": int(gitTimeout / time.Millisecond),
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("git: marshal payload: %v", err)), nil
	}
	return t.bashTool.Execute(ctx, payload)
}

// shellQuoteArg wraps s in POSIX single quotes, escaping any embedded
// single quote via the standard '\'' sequence. Single-quoted strings in
// POSIX shells disable every form of expansion (variables, command
// substitution, glob, history) so this quoting is safe for any byte
// sequence except embedded NUL.
func shellQuoteArg(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellQuoteArgs joins args with spaces after individually quoting each
// element. The output is suitable to follow a literal command name in a
// bash -c invocation, e.g. "git " + shellQuoteArgs(args).
func shellQuoteArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuoteArg(a)
	}
	return strings.Join(parts, " ")
}
