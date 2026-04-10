package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
)

const progressSchemaVersion = "elnath.progress.v1"

const (
	ProgressKindWorkflow = "workflow"
	ProgressKindText     = "text"
	ProgressKindUsage    = "usage"
	ProgressKindTool     = "tool"
)

// ProgressEvent is the shared, UI-safe progress envelope consumed by daemon
// status output today and future delivery bridges later.
type ProgressEvent struct {
	Version  string `json:"version,omitempty"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	Intent   string `json:"intent,omitempty"`
	Workflow string `json:"workflow,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
	Preview  string `json:"preview,omitempty"`
}

func WorkflowProgressEvent(intent, workflow string) ProgressEvent {
	message := strings.TrimSpace(fmt.Sprintf("%s → %s", intent, workflow))
	return ProgressEvent{
		Version:  progressSchemaVersion,
		Kind:     ProgressKindWorkflow,
		Message:  message,
		Intent:   strings.TrimSpace(intent),
		Workflow: strings.TrimSpace(workflow),
	}
}

func TextProgressEvent(text string) ProgressEvent {
	return ProgressEvent{
		Version: progressSchemaVersion,
		Kind:    ProgressKindText,
		Message: summarizeProgress(text),
	}
}

func ToolProgressEvent(toolName, preview string) ProgressEvent {
	msg := toolName
	if preview != "" {
		msg = fmt.Sprintf("%s: %s", toolName, preview)
	}
	return ProgressEvent{
		Version:  progressSchemaVersion,
		Kind:     ProgressKindTool,
		Message:  msg,
		ToolName: strings.TrimSpace(toolName),
		Preview:  strings.TrimSpace(preview),
	}
}

func UsageProgressEvent(summary string) ProgressEvent {
	return ProgressEvent{
		Version: progressSchemaVersion,
		Kind:    ProgressKindUsage,
		Message: strings.TrimSpace(summary),
	}
}

func EncodeProgressEvent(ev ProgressEvent) string {
	ev = normalizeProgressEvent(ev)
	if ev.Message == "" {
		return ""
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return ev.Message
	}
	return string(data)
}

func ParseProgressEvent(raw string) (ProgressEvent, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return ProgressEvent{}, false
	}

	var ev ProgressEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		return ProgressEvent{}, false
	}
	ev = normalizeProgressEvent(ev)
	if ev.Message == "" {
		return ProgressEvent{}, false
	}
	return ev, true
}

func RenderProgress(raw string) string {
	if ev, ok := ParseProgressEvent(raw); ok {
		return ev.Message
	}
	return summarizeProgress(raw)
}

func normalizeProgressEvent(ev ProgressEvent) ProgressEvent {
	ev.Version = progressSchemaVersion
	ev.Kind = strings.TrimSpace(ev.Kind)
	ev.Message = strings.TrimSpace(ev.Message)
	ev.Intent = strings.TrimSpace(ev.Intent)
	ev.Workflow = strings.TrimSpace(ev.Workflow)
	return ev
}
