package event

import (
	"encoding/json"
	"strings"
)

// OnTextToSink wraps a legacy onText callback as a Sink.
// TextDeltaEvent → forwards content string directly
// ToolProgressEvent → JSON-encodes as daemon.ProgressEvent compat
// WorkflowProgressEvent → JSON-encodes
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
		a.fn(encodeProgressCompat("tool", ev.ToolName, ev.Preview))
	case WorkflowProgressEvent:
		msg := strings.TrimSpace(ev.Intent + " → " + ev.Workflow)
		a.fn(encodeProgressCompat("workflow", msg, ""))
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

// SinkToOnText wraps a Sink as a legacy onText callback.
// Each string is emitted as a TextDeltaEvent.
func SinkToOnText(sink Sink) func(string) {
	return func(text string) {
		sink.Emit(TextDeltaEvent{Base: NewBase(), Content: text})
	}
}
