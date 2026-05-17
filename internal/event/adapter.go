package event

import (
	"encoding/json"
	"strings"
)

// OnTextToSink wraps a legacy onText callback as a Sink.
// TextDeltaEvent → forwards content string directly
// ToolProgressEvent → JSON-encodes as daemon.ProgressEvent compat
// WorkflowProgressEvent → JSON-encodes
// RuntimeProgressEvent → JSON-encodes
// UsageProgressEvent → JSON-encodes
// ResearchProgressEvent → forwards Message string
// Other events → silently dropped
// If fn is nil, returns NopSink.
func OnTextToSink(fn func(string)) Sink {
	if fn == nil {
		return NopSink{}
	}
	return &onTextSinkAdapter{fn: fn}
}

type onTextSinkAdapter struct {
	fn func(string)
}

func (a *onTextSinkAdapter) Emit(e Event) {
	switch ev := e.(type) {
	case TextDeltaEvent:
		a.fn(ev.Content)
	case ToolProgressEvent:
		a.fn(encodeToolProgressCompat(ev))
	case WorkflowProgressEvent:
		msg := strings.TrimSpace(ev.Intent + " → " + ev.Workflow)
		a.fn(encodeProgressCompat("workflow", msg, ""))
	case RuntimeProgressEvent:
		a.fn(encodeRuntimeProgressCompat(ev))
	case UsageProgressEvent:
		a.fn(encodeProgressCompat("usage", ev.Summary, ""))
	case ResearchProgressEvent:
		a.fn(ev.Message)
	}
}

func encodeProgressCompat(kind, message, preview string) string {
	m := map[string]string{
		"version": "elnath.progress.v1",
		"kind":    kind,
		"message": message,
	}
	if preview != "" {
		m["preview"] = preview
	}
	data, err := json.Marshal(m)
	if err != nil {
		return message
	}
	return string(data)
}

func encodeToolProgressCompat(ev ToolProgressEvent) string {
	msg := strings.TrimSpace(ev.ToolName)
	if strings.TrimSpace(ev.Preview) != "" {
		msg = strings.TrimSpace(msg + ": " + ev.Preview)
	}
	m := map[string]any{
		"version": "elnath.progress.v1",
		"kind":    "tool",
		"message": msg,
	}
	if strings.TrimSpace(ev.ToolName) != "" {
		m["tool_name"] = strings.TrimSpace(ev.ToolName)
	}
	if strings.TrimSpace(ev.Preview) != "" {
		m["preview"] = strings.TrimSpace(ev.Preview)
	}
	if strings.TrimSpace(ev.Phase) != "" {
		m["phase"] = strings.TrimSpace(ev.Phase)
	}
	if ev.DurationMS > 0 {
		m["duration_ms"] = ev.DurationMS
	}
	if ev.IsError {
		m["is_error"] = true
	}
	data, err := json.Marshal(m)
	if err != nil {
		return msg
	}
	return string(data)
}

func encodeRuntimeProgressCompat(ev RuntimeProgressEvent) string {
	msg := strings.TrimSpace(ev.Message)
	if msg == "" {
		msg = strings.TrimSpace(ev.Phase)
	}
	m := map[string]string{
		"version": "elnath.progress.v1",
		"kind":    "runtime",
		"message": msg,
	}
	if phase := strings.TrimSpace(ev.Phase); phase != "" {
		m["phase"] = phase
	}
	data, err := json.Marshal(m)
	if err != nil {
		return msg
	}
	return string(data)
}

// SinkToOnText wraps a Sink as a legacy onText callback.
// Each string is emitted as a TextDeltaEvent.
func SinkToOnText(sink Sink) func(string) {
	return func(text string) {
		sink.Emit(TextDeltaEvent{Base: NewBase(), Content: text})
	}
}
