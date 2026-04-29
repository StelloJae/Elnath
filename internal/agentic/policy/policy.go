package policy

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/tools"
)

const Version = "agentic-policy-v1"

type Request struct {
	TaskID     int64
	ActorID    int64
	ActionKind string
	ToolName   string
	Input      json.RawMessage
}

type Result struct {
	TaskID        int64
	ActorID       int64
	ActionKind    string
	ToolName      string
	RiskLevel     string
	Decision      string
	Reason        string
	PolicyVersion string
}

type Evaluator struct{}

func NewEvaluator() *Evaluator {
	return &Evaluator{}
}

func (e *Evaluator) Evaluate(req Request) (Result, error) {
	actionKind := strings.TrimSpace(req.ActionKind)
	toolName := strings.TrimSpace(req.ToolName)
	result := Result{
		TaskID:        req.TaskID,
		ActorID:       req.ActorID,
		ActionKind:    actionKind,
		ToolName:      toolName,
		PolicyVersion: Version,
	}

	if isDangerousToolCall(toolName, req.Input) {
		result.RiskLevel = agentic.RiskLevelCritical
		result.Decision = agentic.PolicyDecisionDenied
		result.Reason = dangerousReason(toolName, req.Input)
		return result, nil
	}

	// observe_only records pure observation context. Tool-bearing actions are
	// classified by their tool semantics so callers cannot downgrade risk.
	if actionKind == "observe_only" && toolName == "" {
		result.RiskLevel = agentic.RiskLevelLow
		result.Decision = agentic.PolicyDecisionObserveOnly
		result.Reason = "observe-only action is recorded without execution authority"
		return result, nil
	}

	if gitDecision, ok := classifyGit(toolName, req.Input); ok {
		result.RiskLevel = gitDecision.RiskLevel
		result.Decision = gitDecision.Decision
		result.Reason = gitDecision.Reason
		return result, nil
	}

	if actionKind == "observe" || isReadOnlyTool(toolName) {
		result.RiskLevel = agentic.RiskLevelLow
		result.Decision = agentic.PolicyDecisionAuto
		result.Reason = "read-only or observe action may proceed under policy"
		return result, nil
	}

	if toolName == "bash" {
		result.RiskLevel = agentic.RiskLevelHigh
		result.Decision = agentic.PolicyDecisionApprovalRequired
		result.Reason = "shell command requires approval before enforcement exists"
		return result, nil
	}

	result.RiskLevel = agentic.RiskLevelMedium
	result.Decision = agentic.PolicyDecisionApprovalRequired
	result.Reason = "mutating or unknown action requires approval before enforcement exists"
	return result, nil
}

func (e *Evaluator) EvaluateAndRecord(ctx context.Context, store *agentic.Store, req Request) (*agentic.PolicyDecisionRecord, error) {
	if store == nil {
		return nil, errors.New("policy: store is nil")
	}
	if req.TaskID == 0 {
		return nil, errors.New("policy: task_id is required")
	}
	result, err := e.Evaluate(req)
	if err != nil {
		return nil, err
	}
	return store.CreatePolicyDecision(ctx, agentic.PolicyDecisionRecord{
		TaskID:        result.TaskID,
		ActorID:       result.ActorID,
		ActionKind:    result.ActionKind,
		ToolName:      result.ToolName,
		RiskLevel:     result.RiskLevel,
		Decision:      result.Decision,
		Reason:        result.Reason,
		PolicyVersion: result.PolicyVersion,
	})
}

func isReadOnlyTool(name string) bool {
	switch name {
	case "read_file", "glob", "grep", "web_fetch", "web_search",
		"wiki_search", "wiki_read",
		"conversation_search", "cross_project_search", "cross_project_conversation_search":
		return true
	}
	return false
}

func isDangerousToolCall(toolName string, input json.RawMessage) bool {
	switch toolName {
	case "bash":
		dangerous, _ := analyzeBashInput(input)
		return dangerous
	case "git":
		return isDangerousGitInput(input)
	case "write_file", "edit_file", "wiki_write":
		return hasSystemPathInput(input)
	default:
		return false
	}
}

func dangerousReason(toolName string, input json.RawMessage) string {
	switch toolName {
	case "bash":
		dangerous, reason := analyzeBashInput(input)
		if dangerous && reason != "" {
			return reason
		}
		return "shell command is denied by policy"
	case "git":
		return "hardline git action is denied by policy"
	case "write_file", "edit_file", "wiki_write":
		return "filesystem write to system path is denied by policy"
	default:
		return "action is denied by policy"
	}
}

func analyzeBashInput(input json.RawMessage) (bool, string) {
	var payload struct {
		Command string `json:"command"`
		Cmd     string `json:"cmd"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return false, ""
	}
	command := payload.Command
	if command == "" {
		command = payload.Cmd
	}
	if strings.TrimSpace(command) == "" {
		return false, ""
	}
	return tools.AnalyzeCommandSafety(command)
}

type gitPolicyDecision struct {
	RiskLevel string
	Decision  string
	Reason    string
}

func classifyGit(toolName string, input json.RawMessage) (gitPolicyDecision, bool) {
	if toolName != "git" {
		return gitPolicyDecision{}, false
	}
	params := parseGitParams(input)
	if isReadOnlyGitSubcommand(params.Subcommand) {
		return gitPolicyDecision{
			RiskLevel: agentic.RiskLevelLow,
			Decision:  agentic.PolicyDecisionAuto,
			Reason:    "read-only git action may proceed under policy",
		}, true
	}
	if isDangerousGitParams(params) {
		return gitPolicyDecision{
			RiskLevel: agentic.RiskLevelCritical,
			Decision:  agentic.PolicyDecisionDenied,
			Reason:    "hardline git action is denied by policy",
		}, true
	}
	return gitPolicyDecision{
		RiskLevel: agentic.RiskLevelMedium,
		Decision:  agentic.PolicyDecisionApprovalRequired,
		Reason:    "mutating git action requires approval before enforcement exists",
	}, true
}

type gitParams struct {
	Subcommand string   `json:"subcommand"`
	Args       []string `json:"args"`
}

func parseGitParams(input json.RawMessage) gitParams {
	var params gitParams
	_ = json.Unmarshal(input, &params)
	params.Subcommand = normalizeToken(params.Subcommand)
	for i, arg := range params.Args {
		params.Args[i] = normalizeToken(arg)
	}
	return params
}

func isReadOnlyGitSubcommand(subcommand string) bool {
	switch subcommand {
	case "status", "diff", "log":
		return true
	default:
		return false
	}
}

func isDangerousGitInput(input json.RawMessage) bool {
	return isDangerousGitParams(parseGitParams(input))
}

func isDangerousGitParams(params gitParams) bool {
	switch params.Subcommand {
	case "push":
		return hasAnyArg(params.Args, "--force", "-f", "--mirror")
	case "reset", "clean":
		return true
	case "branch":
		return hasAnyArg(params.Args, "-d", "-D", "--delete")
	default:
		return false
	}
}

func hasAnyArg(args []string, needles ...string) bool {
	for _, arg := range args {
		for _, needle := range needles {
			if arg == needle {
				return true
			}
		}
	}
	return false
}

func hasSystemPathInput(input json.RawMessage) bool {
	var payload map[string]any
	if err := json.Unmarshal(input, &payload); err != nil {
		return false
	}
	for _, key := range []string{"file_path", "path", "target"} {
		value, ok := payload[key].(string)
		if ok && isSystemPath(value) {
			return true
		}
	}
	return false
}

func isSystemPath(path string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	switch cleaned {
	case "/bin", "/boot", "/dev", "/etc", "/lib", "/opt", "/private/etc", "/root", "/sbin", "/System", "/usr", "/var":
		return true
	}
	for _, prefix := range []string{
		"/bin/", "/boot/", "/dev/", "/etc/", "/lib/", "/opt/", "/private/etc/", "/root/", "/sbin/", "/System/", "/usr/", "/var/",
	} {
		if strings.HasPrefix(cleaned, prefix) {
			return true
		}
	}
	return false
}

func normalizeToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
