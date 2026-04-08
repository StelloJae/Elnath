package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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
	workDir        string
	defaultTimeout time.Duration
}

// NewBashTool creates a BashTool rooted at workDir with a 120s default timeout.
func NewBashTool(workDir string) *BashTool {
	return &BashTool{workDir: workDir, defaultTimeout: bashDefaultTimeout}
}

func (t *BashTool) Name() string        { return "bash" }
func (t *BashTool) Description() string { return "Execute a shell command in the working directory." }

func (t *BashTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"command":     String("The shell command to execute."),
		"timeout_ms":  Int("Timeout in milliseconds (default 120000, max 600000)."),
		"working_dir": String("Working directory for the command (default: tool's workDir)."),
	}, []string{"command"})
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

	if dangerous, reason := analyzeCommand(p.Command); dangerous {
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

	workDir := t.workDir
	if p.WorkingDir != "" {
		workDir = p.WorkingDir
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
func analyzeCommand(command string) (dangerous bool, reason string) {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "cmd")
	if err != nil {
		// Unparseable commands are allowed — bash will report the syntax error.
		return false, ""
	}

	syntax.Walk(f, func(node syntax.Node) bool {
		if dangerous {
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}

		cmdName := firstLit(call.Args[0])
		switch cmdName {
		case "sudo":
			dangerous, reason = true, "sudo invocation is not allowed"
			return false
		case "dd":
			dangerous, reason = true, "dd command is not allowed"
			return false
		case "rm":
			if hasRmRfRoot(call) {
				dangerous, reason = true, "rm -rf on root or home is not allowed"
				return false
			}
		case "chmod", "chown":
			if hasSystemPath(call) {
				dangerous, reason = true, fmt.Sprintf("%s on system paths is not allowed", cmdName)
				return false
			}
		case "git":
			if isGitForcePushMain(call) {
				dangerous, reason = true, "git push --force to main/master is not allowed"
				return false
			}
		}
		return true
	})

	return dangerous, reason
}

// firstLit returns the first literal value of a shell word, or empty string.
func firstLit(word *syntax.Word) string {
	if len(word.Parts) == 0 {
		return ""
	}
	lit, ok := word.Parts[0].(*syntax.Lit)
	if !ok {
		return ""
	}
	return lit.Value
}

// hasRmRfRoot detects `rm -rf /` or `rm -rf ~` patterns.
func hasRmRfRoot(call *syntax.CallExpr) bool {
	hasRF := false
	hasRootPath := false
	for _, arg := range call.Args[1:] {
		v := firstLit(arg)
		if v == "-rf" || v == "-fr" || v == "-r" {
			hasRF = true
		}
		if v == "/" || v == "~" || v == "$HOME" {
			hasRootPath = true
		}
	}
	return hasRF && hasRootPath
}

// hasSystemPath checks whether any argument looks like a system path.
func hasSystemPath(call *syntax.CallExpr) bool {
	systemPrefixes := []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/boot", "/sys", "/proc"}
	for _, arg := range call.Args[1:] {
		v := firstLit(arg)
		for _, prefix := range systemPrefixes {
			if strings.HasPrefix(v, prefix) {
				return true
			}
		}
	}
	return false
}

// isGitForcePushMain detects `git push --force [remote] main|master`.
func isGitForcePushMain(call *syntax.CallExpr) bool {
	hasForce := false
	hasPush := false
	hasMain := false
	for _, arg := range call.Args[1:] {
		v := firstLit(arg)
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
