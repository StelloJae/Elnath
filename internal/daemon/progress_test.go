package daemon

import "testing"

func TestProgressEventRoundTrip(t *testing.T) {
	raw := EncodeProgressEvent(ProgressEvent{
		Kind:     ProgressKindWorkflow,
		Message:  "question → single",
		Intent:   "question",
		Workflow: "single",
	})

	ev, ok := ParseProgressEvent(raw)
	if !ok {
		t.Fatalf("ParseProgressEvent(%q) = !ok", raw)
	}
	if ev.Kind != ProgressKindWorkflow {
		t.Fatalf("kind = %q, want %q", ev.Kind, ProgressKindWorkflow)
	}
	if ev.Message != "question → single" {
		t.Fatalf("message = %q, want %q", ev.Message, "question → single")
	}
	if ev.Intent != "question" || ev.Workflow != "single" {
		t.Fatalf("event = %+v, want question/single", ev)
	}
}

func TestRenderProgressFallsBackToLegacyText(t *testing.T) {
	raw := "first line\nsecond line\n"
	if got := RenderProgress(raw); got != "second line" {
		t.Fatalf("RenderProgress(%q) = %q, want %q", raw, got, "second line")
	}
}
