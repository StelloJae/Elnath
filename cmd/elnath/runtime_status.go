package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	basetools "github.com/stello/elnath/internal/tools"
)

const statusCommandUsage = "Usage: /status [--json]"

type runtimeStatusView struct {
	Version              string `json:"version"`
	Provider             string `json:"provider"`
	Model                string `json:"model"`
	EffortMode           string `json:"effort_mode"`
	Effort               string `json:"effort,omitempty"`
	ProviderEffort       string `json:"provider_effort"`
	ProviderEffortNote   string `json:"provider_effort_note,omitempty"`
	AutoEffortCompatible bool   `json:"auto_effort_compatible"`
	PermissionMode       string `json:"permission_mode"`
	ToolExposure         string `json:"tool_exposure"`
	ToolCount            int    `json:"tool_count"`
	DeferredToolCount    int    `json:"deferred_tool_count"`
	ControlSurface       struct {
		ToolCount int      `json:"tool_count"`
		Missing   []string `json:"missing"`
	} `json:"control_surface"`
	WorkDir    string `json:"work_dir"`
	DaemonMode bool   `json:"daemon_mode"`
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
		fmt.Sprintf("  tools:          %d registered (%d deferred)", view.ToolCount, view.DeferredToolCount),
		fmt.Sprintf("  control_surface: %s", formatRuntimeStatusControlSurface(view)),
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
		caps := llm.CapabilitiesOf(rt.provider)
		view.ProviderEffort = caps.ReasoningEffort
		view.ProviderEffortNote = caps.ReasoningEffortFallback
		view.AutoEffortCompatible = autoEffortCompatible(caps.ReasoningEffort)
	}
	if view.Provider == "" {
		view.Provider = "unknown"
	}
	if view.ProviderEffort == "" {
		view.ProviderEffort = llm.ReasoningEffortUnknown
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
	if rt.reg != nil {
		registeredTools := rt.reg.List()
		view.ToolCount = len(registeredTools)
		for _, tool := range registeredTools {
			if basetools.ShouldDeferToolSchema(tool) {
				view.DeferredToolCount++
			}
		}
	}
	controlSurface := runtimeControlSurfaceStatus(rt.reg)
	view.ControlSurface.ToolCount = controlSurface.ToolCount
	view.ControlSurface.Missing = controlSurface.Missing
	if rt.wfCfg.Permission != nil {
		view.PermissionMode = rt.wfCfg.Permission.Mode().String()
	}
	if view.PermissionMode == "" {
		view.PermissionMode = "default"
	}
	return view
}

type runtimeControlSurfaceStatusView struct {
	ToolCount int
	Missing   []string
}

func runtimeControlSurfaceStatus(reg *basetools.Registry) runtimeControlSurfaceStatusView {
	seen := map[string]struct{}{}
	out := runtimeControlSurfaceStatusView{}
	for _, surface := range controlSurfaceManifest() {
		for _, name := range surface.Tools {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out.ToolCount++
			if reg == nil {
				out.Missing = append(out.Missing, name)
				continue
			}
			if _, ok := reg.Get(name); !ok {
				out.Missing = append(out.Missing, name)
			}
		}
	}
	sort.Strings(out.Missing)
	return out
}

func formatRuntimeStatusEffort(view runtimeStatusView) string {
	if view.Effort == "" {
		return view.EffortMode
	}
	return view.EffortMode + "/" + view.Effort
}

func formatRuntimeStatusControlSurface(view runtimeStatusView) string {
	if len(view.ControlSurface.Missing) == 0 {
		return "ok"
	}
	return fmt.Sprintf("missing %d/%d: %s",
		len(view.ControlSurface.Missing),
		view.ControlSurface.ToolCount,
		strings.Join(view.ControlSurface.Missing, ","),
	)
}
