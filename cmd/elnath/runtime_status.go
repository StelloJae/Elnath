package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

const statusCommandUsage = "Usage: /status [--json]"

type runtimeStatusView struct {
	Version        string `json:"version"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	EffortMode     string `json:"effort_mode"`
	Effort         string `json:"effort,omitempty"`
	PermissionMode string `json:"permission_mode"`
	ToolExposure   string `json:"tool_exposure"`
	WorkDir        string `json:"work_dir"`
	DaemonMode     bool   `json:"daemon_mode"`
}

func (rt *executionRuntime) tryStatusCommand(
	sess *agent.Session,
	messages []llm.Message,
	input string,
	bus *event.Bus,
) ([]llm.Message, string, bool, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 || fields[0] != "/status" {
		return nil, "", false, nil
	}

	summary := rt.applyStatusCommand(fields[1:])
	if bus != nil {
		bus.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: summary + "\n"})
	}

	delta := []llm.Message{
		llm.NewUserMessage(input),
		llm.NewAssistantMessage(summary),
	}
	updated := append(messages, delta...)
	if sess != nil {
		if err := sess.AppendMessages(delta); err != nil {
			rt.app.Logger.Warn("session persist failed", "error", err)
		}
		sess.Messages = updated
	}
	return updated, summary, true, nil
}

func (rt *executionRuntime) applyStatusCommand(args []string) string {
	if len(args) == 1 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "help", "-h", "--help":
			return statusCommandUsage
		case "--json":
			raw, err := json.MarshalIndent(rt.runtimeStatusView(), "", "  ")
			if err != nil {
				return fmt.Sprintf("status: marshal JSON: %v", err)
			}
			return string(raw)
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf("Invalid status argument: %s. %s", strings.Join(args, " "), statusCommandUsage)
	}

	view := rt.runtimeStatusView()
	return strings.Join([]string{
		"Elnath runtime status:",
		fmt.Sprintf("  version:        %s", view.Version),
		fmt.Sprintf("  provider:       %s", view.Provider),
		fmt.Sprintf("  model:          %s", view.Model),
		fmt.Sprintf("  effort:         %s", formatRuntimeStatusEffort(view)),
		fmt.Sprintf("  permission:     %s", view.PermissionMode),
		fmt.Sprintf("  tool_exposure:  %s", view.ToolExposure),
		fmt.Sprintf("  work_dir:       %s", view.WorkDir),
		fmt.Sprintf("  daemon_mode:    %t", view.DaemonMode),
	}, "\n")
}

func (rt *executionRuntime) runtimeStatusView() runtimeStatusView {
	view := runtimeStatusView{
		Version:      version,
		Model:        "provider default",
		EffortMode:   strings.TrimSpace(rt.wfCfg.ReasoningEffortMode),
		Effort:       strings.TrimSpace(rt.wfCfg.ReasoningEffort),
		ToolExposure: strings.TrimSpace(rt.wfCfg.ToolExposureMode),
		WorkDir:      rt.workDir,
		DaemonMode:   rt.daemonMode,
	}
	if rt.provider != nil {
		view.Provider = rt.provider.Name()
	}
	if view.Provider == "" {
		view.Provider = "unknown"
	}
	if model := strings.TrimSpace(rt.wfCfg.Model); model != "" {
		view.Model = model
	}
	if view.EffortMode == "" {
		view.EffortMode = "auto"
	}
	if view.ToolExposure == "" {
		view.ToolExposure = config.ToolExposureModeStandard
	}
	if rt.wfCfg.Permission != nil {
		view.PermissionMode = rt.wfCfg.Permission.Mode().String()
	}
	if view.PermissionMode == "" {
		view.PermissionMode = "default"
	}
	return view
}

func formatRuntimeStatusEffort(view runtimeStatusView) string {
	if view.Effort == "" {
		return view.EffortMode
	}
	return view.EffortMode + "/" + view.Effort
}
