package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/tools"
)

const runtimeCommandToolName = "runtime_command"

type runtimeCommandTool struct {
	resolveRuntime func() *executionRuntime
}

func newRuntimeCommandTool(resolveRuntime func() *executionRuntime) *runtimeCommandTool {
	return &runtimeCommandTool{resolveRuntime: resolveRuntime}
}

func (t *runtimeCommandTool) Name() string { return runtimeCommandToolName }

func (t *runtimeCommandTool) Description() string {
	return "Execute read-only local runtime slash commands such as /status, /version, /commands, /skills, and status-only provider/model/effort/plan queries"
}

func (t *runtimeCommandTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"command": tools.StringEnum(
			"Read-only runtime slash command to execute.",
			"/version", "/status", "/commands", "/help", "/skills", "/provider", "/model", "/effort", "/plan",
		),
		"args": tools.Array("Optional command arguments. Mutating arguments are rejected.", "string"),
	}, []string{"command"})
}

func (t *runtimeCommandTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *runtimeCommandTool) Reversible() bool { return true }

func (t *runtimeCommandTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *runtimeCommandTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *runtimeCommandTool) DeferInitialToolSchema() bool { return true }

type runtimeCommandInput struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type runtimeCommandOutput struct {
	Command string                `json:"command"`
	Args    []string              `json:"args,omitempty"`
	Output  string                `json:"output"`
	Receipt runtimeCommandReceipt `json:"receipt"`
}

type runtimeCommandReceipt struct {
	Tool               string   `json:"tool"`
	Action             string   `json:"action"`
	ReadOnly           bool     `json:"read_only"`
	ExecutionAvailable bool     `json:"execution_available"`
	ExecutionPolicy    string   `json:"execution_policy"`
	Command            string   `json:"command"`
	Args               []string `json:"args,omitempty"`
	StateMutation      bool     `json:"state_mutation"`
}

func (t *runtimeCommandTool) Execute(_ context.Context, params json.RawMessage) (*tools.Result, error) {
	var input runtimeCommandInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	rt := t.runtime()
	if rt == nil {
		return tools.ErrorResult("runtime_command: runtime unavailable"), nil
	}
	command := normalizeRuntimeCommandName(input.Command)
	args := normalizeRuntimeCommandArgs(input.Args)
	output, err := rt.applyReadOnlyRuntimeCommand(command, args)
	if err != nil {
		return tools.ErrorResult(err.Error()), nil
	}
	raw, err := json.Marshal(runtimeCommandOutput{
		Command: command,
		Args:    args,
		Output:  output,
		Receipt: runtimeCommandReceipt{
			Tool:               runtimeCommandToolName,
			Action:             "execute",
			ReadOnly:           true,
			ExecutionAvailable: true,
			ExecutionPolicy:    "local_runtime_control_readonly",
			Command:            command,
			Args:               args,
			StateMutation:      false,
		},
	})
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("runtime_command: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

func (t *runtimeCommandTool) runtime() *executionRuntime {
	if t == nil || t.resolveRuntime == nil {
		return nil
	}
	return t.resolveRuntime()
}

func (rt *executionRuntime) applyReadOnlyRuntimeCommand(command string, args []string) (string, error) {
	if !runtimeCommandReadOnlyArgs(command, args) {
		return "", fmt.Errorf("runtime_command: %s %s is not read-only", command, strings.Join(args, " "))
	}
	switch command {
	case "/version":
		return applyVersionCommand(args), nil
	case "/status":
		return rt.applyStatusCommand(args), nil
	case "/commands", "/help":
		return rt.applyCommandsCommand(args), nil
	case "/skills":
		return rt.applySkillsCommand(args), nil
	case "/provider":
		return rt.applyProviderCommand(args), nil
	case "/model":
		return rt.applyModelCommand(args), nil
	case "/effort":
		return rt.applyEffortCommand(args), nil
	case "/plan":
		return rt.applyPlanCommand(args), nil
	default:
		return "", fmt.Errorf("runtime_command: unsupported command %q", command)
	}
}

func normalizeRuntimeCommandName(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if !strings.HasPrefix(command, "/") {
		command = "/" + command
	}
	return command
}

func normalizeRuntimeCommandArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			out = append(out, arg)
		}
	}
	return out
}

func runtimeCommandReadOnlyArgs(command string, args []string) bool {
	switch command {
	case "/version":
		return argsMatchAny(args, nil, []string{"--json"}, []string{"help"}, []string{"-h"}, []string{"--help"})
	case "/status":
		return argsMatchAny(args, nil, []string{"--json"}, []string{"help"}, []string{"-h"}, []string{"--help"})
	case "/commands", "/help", "/skills":
		return runtimeCatalogCommandReadOnlyArgs(args)
	case "/provider":
		return runtimeProviderCommandReadOnlyArgs(args)
	case "/model":
		return argsMatchAny(args, nil, []string{"status"}, []string{"current"}, []string{"help"}, []string{"-h"}, []string{"--help"})
	case "/effort":
		return argsMatchAny(args, nil, []string{"status"}, []string{"current"}, []string{"help"}, []string{"-h"}, []string{"--help"})
	case "/plan":
		return argsMatchAny(args, []string{"status"}, []string{"current"}, []string{"help"}, []string{"-h"}, []string{"--help"})
	default:
		return false
	}
}

func runtimeCatalogCommandReadOnlyArgs(args []string) bool {
	for _, arg := range args {
		switch strings.ToLower(arg) {
		case "help", "-h", "--help", "--json", "--all", "--hidden":
			continue
		default:
			return false
		}
	}
	return true
}

func runtimeProviderCommandReadOnlyArgs(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch strings.ToLower(args[0]) {
	case "help", "-h", "--help", "--json":
		return len(args) == 1
	case "current", "status", "route", "candidates":
		return len(args) == 1 || (len(args) == 2 && strings.EqualFold(args[1], "--json"))
	case "check":
		return len(args) == 2 || (len(args) == 3 && strings.EqualFold(args[2], "--json"))
	default:
		return false
	}
}

func argsMatchAny(args []string, allowed ...[]string) bool {
	for _, option := range allowed {
		if len(args) != len(option) {
			continue
		}
		matches := true
		for i := range args {
			if !strings.EqualFold(args[i], option[i]) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}
