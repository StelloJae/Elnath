package telegram

// ProgressRenderer is the surface Task sink and Chat responder share
// when they need to show per-step progress to the partner. Task path
// already owns the concrete *ProgressReporter (batched Telegram edit
// bubble, dedup, throttle); Chat path joins via L2.2 by accepting any
// ProgressRenderer factory.
//
// The interface is deliberately narrow so the noop implementation
// below can stand in whenever a surface (CLI, tests, factory-less
// wiring) has no live progress channel. Methods are fire-and-forget —
// the Task / Chat call site must be able to invoke them without
// reasoning about whether a real renderer is attached.
type ProgressRenderer interface {
	// ReportTool records a tool invocation (e.g. "web_fetch" with a
	// URL preview). Implementations batch and throttle; callers do
	// not need to rate-limit.
	ReportTool(name, preview string)
	// ReportStage records a high-level pipeline stage transition
	// (plan / code / test / review / etc.). Surfaced as a bolded
	// header in the Telegram bubble.
	ReportStage(stage string)
	// Finish signals "no more events will arrive". Renderers flush
	// any pending edits and release resources.
	Finish()
	// Wait blocks until the renderer's internal goroutine (if any)
	// has drained. Must be called after Finish so the chat/task
	// handler can return cleanly without leaking the emitter.
	Wait()
}

// Compile-time assertion: the existing concrete *ProgressReporter
// must stay interface-compatible. If a signature drifts, the build
// breaks here instead of at a subtle runtime call site.
var _ ProgressRenderer = (*ProgressReporter)(nil)

// noopProgressRenderer is the silent stand-in used when a surface has
// no real progress channel wired. Every method is a pure no-op —
// including Wait, which returns immediately because there is no
// background goroutine to drain. This is the default the ChatResponder
// falls back to when ChatPipelineDeps.ProgressFactory is nil, so the
// chat tool loop can call progress methods unconditionally without
// nil-checking at every site.
type noopProgressRenderer struct{}

func (noopProgressRenderer) ReportTool(_, _ string) {}
func (noopProgressRenderer) ReportStage(_ string)   {}
func (noopProgressRenderer) Finish()                {}
func (noopProgressRenderer) Wait()                  {}
