package event

import (
	"strings"
	"testing"
)

func TestOnTextToSink(t *testing.T) {
	var got []string
	sink := OnTextToSink(func(s string) { got = append(got, s) })

	sink.Emit(TextDeltaEvent{Base: NewBase(), Content: "hello"})
	sink.Emit(TextDeltaEvent{Base: NewBase(), Content: "world"})

	if len(got) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(got))
	}
	if got[0] != "hello" || got[1] != "world" {
		t.Errorf("unexpected content: %v", got)
	}
}

func TestOnTextToSinkNonTextEventsIgnored(t *testing.T) {
	called := false
	sink := OnTextToSink(func(s string) { called = true })

	sink.Emit(IterationStartEvent{Base: NewBase(), Iteration: 1, Max: 5})

	if called {
		t.Error("expected callback not to be called for IterationStartEvent")
	}
}

func TestOnTextToSinkToolProgressEncodesJSON(t *testing.T) {
	var got string
	sink := OnTextToSink(func(s string) { got = s })

	sink.Emit(ToolProgressEvent{Base: NewBase(), ToolName: "bash", Preview: "ls -la"})

	if got == "" {
		t.Fatal("expected non-empty JSON string")
	}
	if !strings.Contains(got, "tool") {
		t.Errorf("expected JSON to contain %q, got: %s", "tool", got)
	}
}

func TestOnTextToSinkNilReturnsNopSink(t *testing.T) {
	sink := OnTextToSink(nil)
	if _, ok := sink.(NopSink); !ok {
		t.Errorf("expected NopSink, got %T", sink)
	}
	// Must not panic.
	sink.Emit(TextDeltaEvent{Base: NewBase(), Content: "ignored"})
}

func TestSinkToOnText(t *testing.T) {
	rec := &RecorderSink{}
	fn := SinkToOnText(rec)

	fn("first")
	fn("second")

	events := EventsOfType[TextDeltaEvent](rec)
	if len(events) != 2 {
		t.Fatalf("expected 2 TextDeltaEvents, got %d", len(events))
	}
	if events[0].Content != "first" || events[1].Content != "second" {
		t.Errorf("unexpected content: %v, %v", events[0].Content, events[1].Content)
	}
}
