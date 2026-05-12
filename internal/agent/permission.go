package agent

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/stello/elnath/internal/tools"
)

// PermissionMode controls how the permission engine makes decisions.
type PermissionMode int

const (
	// ModeDefault asks the prompter for non-read-only tools not in allow/deny lists.
	ModeDefault PermissionMode = iota
	// ModeAcceptEdits auto-approves read-only and file-edit tools; asks for others.
	ModeAcceptEdits
	// ModePlan approves only read-only tools; denies everything else.
	ModePlan
	// ModeBypass approves everything without prompting.
	ModeBypass
)

// Prompter is called when interactive confirmation is needed.
type Prompter interface {
	Prompt(ctx context.Context, toolName string, input json.RawMessage) (bool, error)
}

// Permission is the permission engine for tool execution.
// Resolution order (6 steps):
//  1. Deny list — always deny
//  2. Allow list — always allow
//  3. Mode: Bypass → allow; Plan + non-read-only → deny
//  4. Mode: AcceptEdits + (read-only || edit tool) → allow
//  5. No prompter → allow (non-interactive)
//  6. Ask the prompter
type Permission struct {
	mu        sync.RWMutex
	allowList []string
	denyList  []string
	prompter  Prompter
	mode      PermissionMode
}

// PermissionOption configures a Permission engine.
type PermissionOption func(*Permission)

// WithAllowList pre-approves the listed tool names.
func WithAllowList(names ...string) PermissionOption {
	return func(p *Permission) { p.allowList = append(p.allowList, names...) }
}

// WithDenyList blocks the listed tool names regardless of mode.
func WithDenyList(names ...string) PermissionOption {
	return func(p *Permission) { p.denyList = append(p.denyList, names...) }
}

// WithPrompter sets the interactive prompter used in Default and AcceptEdits modes.
func WithPrompter(pr Prompter) PermissionOption {
	return func(p *Permission) { p.prompter = pr }
}

// WithMode sets the permission mode.
func WithMode(m PermissionMode) PermissionOption {
	return func(p *Permission) { p.mode = m }
}

// NewPermission constructs a Permission engine.
func NewPermission(opts ...PermissionOption) *Permission {
	p := &Permission{mode: ModeDefault}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (m PermissionMode) String() string {
	switch m {
	case ModeAcceptEdits:
		return "accept_edits"
	case ModePlan:
		return "plan"
	case ModeBypass:
		return "bypass"
	default:
		return "default"
	}
}

func (p *Permission) Mode() PermissionMode {
	if p == nil {
		return ModeDefault
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

func (p *Permission) SetMode(mode PermissionMode) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = mode
}

// Check returns true if the named tool call should proceed.
func (p *Permission) Check(ctx context.Context, toolName string, input json.RawMessage) (bool, error) {
	p.mu.RLock()
	allowList := append([]string(nil), p.allowList...)
	denyList := append([]string(nil), p.denyList...)
	prompter := p.prompter
	mode := p.mode
	p.mu.RUnlock()

	// Step 1: deny list — always blocks.
	for _, d := range denyList {
		if d == toolName {
			return false, nil
		}
	}

	// Explicit bypass mode still means bypass.
	if mode == ModeBypass {
		return true, nil
	}

	// Dangerous bash commands must not be auto-approved purely by tool name.
	if toolName == "bash" && isDangerousBashInput(input) {
		if prompter == nil {
			return false, nil
		}
		return prompter.Prompt(ctx, toolName, input)
	}

	// Step 2: allow list — always permits.
	for _, a := range allowList {
		if a == toolName {
			return true, nil
		}
	}

	// Step 3: mode shortcuts.
	switch mode {
	case ModePlan:
		// Only read-only tools are permitted in plan mode.
		return isReadOnly(toolName), nil
	}

	// Step 4: AcceptEdits auto-approves reads and edits.
	if mode == ModeAcceptEdits && (isReadOnly(toolName) || isEditTool(toolName)) {
		return true, nil
	}

	// Step 5: no prompter — allow (non-interactive / scripted usage).
	if prompter == nil {
		return true, nil
	}

	// Step 6: ask the prompter.
	return prompter.Prompt(ctx, toolName, input)
}

func isDangerousBashInput(input json.RawMessage) bool {
	var payload struct {
		Command string `json:"command"`
		Cmd     string `json:"cmd"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return false
	}
	command := payload.Command
	if command == "" {
		command = payload.Cmd
	}
	if command == "" {
		return false
	}
	dangerous, _ := tools.AnalyzeCommandSafety(command)
	return dangerous
}

// isReadOnly returns true for tools that only read state and never modify it.
func isReadOnly(name string) bool {
	switch name {
	case "read_file", "glob", "grep", "web_fetch", "web_search",
		"wiki_search", "wiki_read",
		"conversation_search", "cross_project_search", "cross_project_conversation_search",
		"tool_search", "command_catalog", "todo_write", "task_list", "task_get", "task_output", "task_monitor", "ask_user_question", "schedule_list", "enter_plan_mode", "exit_plan_mode":
		return true
	}
	return false
}

// isEditTool returns true for tools that modify file content but do not
// execute arbitrary commands.
func isEditTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "wiki_write":
		return true
	}
	return false
}
