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
