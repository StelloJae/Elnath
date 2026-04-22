package telegram

import (
	"log/slog"
	"testing"
)

// TestNoopProgressRenderer_MethodsAreSafe pins the L2.1 baseline: when
// no ProgressFactory is wired, the chat path must still be able to call
// the four interface methods without panicking. This is the regression
// guard that lets Respond treat progress as optional (the whole point
// of the factory-based injection — legacy wire without a ProgressFactory
// stays silent instead of crashing).
func TestNoopProgressRenderer_MethodsAreSafe(t *testing.T) {
	t.Parallel()

	var r ProgressRenderer = noopProgressRenderer{}

	// None of these should panic or block — noop is meant to be a
	// drop-in silent stand-in.
	r.ReportTool("web_fetch", "https://example.com")
	r.ReportStage("code")
	r.Finish()
	r.Wait()
}

// TestProgressReporterImplementsProgressRenderer runtime-asserts the
// interface satisfaction. The compile-time assertion in progress.go
// already guarantees this at build time — this test provides an
// additional failure mode (test output) if someone ever comments the
// assertion out or renames methods in a way that slips past the type
// check.
func TestProgressReporterImplementsProgressRenderer(t *testing.T) {
	t.Parallel()

	var r ProgressRenderer = NewProgressReporter(nil, "chat-42", slog.Default())
	if r == nil {
		t.Fatal("NewProgressReporter returned nil when typed as ProgressRenderer")
	}
}

// TestChatResponder_ProgressRendererReturnsNoopWhenFactoryNil pins the
// nil-safety contract: a ChatResponder with no pipeline (or a pipeline
// without a ProgressFactory) returns a usable noop renderer, not nil.
// L2.2 adds the consumption site in runStreamWithTools, which relies
// on this contract to avoid nil-check noise at every ReportTool call.
func TestChatResponder_ProgressRendererReturnsNoopWhenFactoryNil(t *testing.T) {
	t.Parallel()

	// No pipeline at all.
	cr := NewChatResponder(nil, nil, "chat-42", nil)
	r := cr.progressRenderer()
	if r == nil {
		t.Fatal("progressRenderer() returned nil; want noop fallback")
	}
	// Pipeline present but no factory.
	cr2 := NewChatResponder(nil, nil, "chat-42", nil,
		WithChatPipeline(ChatPipelineDeps{Builder: &stubChatBuilder{result: "SYS"}}),
	)
	r2 := cr2.progressRenderer()
	if r2 == nil {
		t.Fatal("progressRenderer() returned nil when ProgressFactory nil; want noop fallback")
	}
}

// TestChatResponder_ProgressRendererUsesFactoryWhenWired pins that a
// wired factory is called with the chat-instance chatID so the
// returned renderer can address the right chat. This is the per-turn
// instance-creation hook that L2.2 activates.
func TestChatResponder_ProgressRendererUsesFactoryWhenWired(t *testing.T) {
	t.Parallel()

	var gotChatID string
	factory := func(chatID string) ProgressRenderer {
		gotChatID = chatID
		return noopProgressRenderer{}
	}

	cr := NewChatResponder(nil, nil, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		Builder:         &stubChatBuilder{result: "SYS"},
		ProgressFactory: factory,
	}))

	r := cr.progressRenderer()
	if r == nil {
		t.Fatal("progressRenderer() returned nil with factory wired")
	}
	if gotChatID != "chat-42" {
		t.Errorf("factory called with chatID = %q, want %q", gotChatID, "chat-42")
	}
}
