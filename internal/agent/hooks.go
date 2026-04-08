package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stello/elnath/internal/tools"
)

const hookTimeout = 30 * time.Second

// HookAction indicates what should happen after a PreToolUse hook runs.
type HookAction int

const (
	HookAllow HookAction = iota
	HookDeny
)

// HookResult carries the outcome of a PreToolUse hook.
type HookResult struct {
	Action  HookAction
	Message string
}

// Hook is the interface for tool execution lifecycle hooks.
type Hook interface {
	PreToolUse(ctx context.Context, toolName string, params json.RawMessage) (HookResult, error)
	PostToolUse(ctx context.Context, toolName string, params json.RawMessage, result *tools.Result) error
}

// HookRegistry holds ordered hooks and runs them sequentially.
type HookRegistry struct {
	hooks  []Hook
	onStop []func(ctx context.Context) error
}

// NewHookRegistry creates an empty HookRegistry.
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{}
}

// Add appends a hook to the registry.
func (r *HookRegistry) Add(h Hook) {
	r.hooks = append(r.hooks, h)
}

// AddOnStop registers a function to be called when the agent loop stops.
func (r *HookRegistry) AddOnStop(fn func(ctx context.Context) error) {
	r.onStop = append(r.onStop, fn)
}

// RunPreToolUse runs all pre-tool-use hooks. Stops on first deny or error.
func (r *HookRegistry) RunPreToolUse(ctx context.Context, toolName string, params json.RawMessage) (HookResult, error) {
	for _, h := range r.hooks {
		result, err := h.PreToolUse(ctx, toolName, params)
		if err != nil {
			return HookResult{Action: HookDeny, Message: err.Error()}, err
		}
		if result.Action == HookDeny {
			return result, nil
		}
	}
	return HookResult{Action: HookAllow}, nil
}

// RunPostToolUse runs all post-tool-use hooks. Stops on first error.
func (r *HookRegistry) RunPostToolUse(ctx context.Context, toolName string, params json.RawMessage, result *tools.Result) error {
	for _, h := range r.hooks {
		if err := h.PostToolUse(ctx, toolName, params, result); err != nil {
			return err
		}
	}
	return nil
}

// RunOnStop runs all registered stop hooks, collecting errors.
func (r *HookRegistry) RunOnStop(ctx context.Context) error {
	var errs []string
	for _, fn := range r.onStop {
		if err := fn(ctx); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("hook errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// CommandHook runs shell commands as pre/post tool-use hooks.
type CommandHook struct {
	Matcher string // glob pattern for tool name matching (e.g., "*", "bash", "write_*")
	PreCmd  string // shell command for PreToolUse (empty = skip)
	PostCmd string // shell command for PostToolUse (empty = skip)
}

func (h *CommandHook) matches(toolName string) bool {
	if h.Matcher == "" || h.Matcher == "*" {
		return true
	}
	matched, _ := filepath.Match(h.Matcher, toolName)
	return matched
}

// PreToolUse runs PreCmd if the tool name matches. Exit 0 = allow, non-zero = deny.
func (h *CommandHook) PreToolUse(ctx context.Context, toolName string, params json.RawMessage) (HookResult, error) {
	if h.PreCmd == "" || !h.matches(toolName) {
		return HookResult{Action: HookAllow}, nil
	}

	input, _ := json.Marshal(map[string]any{
		"tool_name": toolName,
		"params":    json.RawMessage(params),
	})

	hookCtx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()

	cmd := exec.CommandContext(hookCtx, "sh", "-c", h.PreCmd)
	cmd.Stdin = strings.NewReader(string(input))
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = fmt.Sprintf("pre-hook denied: %s", err)
		}
		return HookResult{Action: HookDeny, Message: msg}, nil
	}
	return HookResult{Action: HookAllow}, nil
}

// PostToolUse runs PostCmd if the tool name matches.
func (h *CommandHook) PostToolUse(ctx context.Context, toolName string, params json.RawMessage, result *tools.Result) error {
	if h.PostCmd == "" || !h.matches(toolName) {
		return nil
	}

	input, _ := json.Marshal(map[string]any{
		"tool_name": toolName,
		"params":    json.RawMessage(params),
		"result": map[string]any{
			"output":   result.Output,
			"is_error": result.IsError,
		},
	})

	hookCtx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()

	cmd := exec.CommandContext(hookCtx, "sh", "-c", h.PostCmd)
	cmd.Stdin = strings.NewReader(string(input))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("post-hook error: %s: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
