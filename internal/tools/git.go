package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const gitTimeout = 30 * time.Second

// GitTool dispatches git subcommands (status, diff, commit, log, branch).
type GitTool struct{ guard *PathGuard }

func NewGitTool(guard *PathGuard) *GitTool { return &GitTool{guard: guard} }

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

// run executes a git command with the given arguments.
func (t *GitTool) run(ctx context.Context, args ...string) (*Result, error) {
	execCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "git", args...)
	cmd.Dir = t.guard.WorkDir()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	if execCtx.Err() == context.DeadlineExceeded {
		return ErrorResult("git command timed out"), nil
	}
	if err != nil {
		msg := output
		if msg == "" {
			msg = err.Error()
		}
		return ErrorResult(msg), nil
	}
	if output == "" {
		output = "(no output)"
	}
	return &Result{Output: output}, nil
}
