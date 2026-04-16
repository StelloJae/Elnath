# Phase 5.0: Typed Event Bus — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `onText func(string)` with a typed `event.Sink` interface across the entire codebase, enabling multiple typed-event observers (terminal, telegram, session recorder) from a single event bus.

**Architecture:** New leaf package `internal/event/` with `Event` interface + concrete types, `Sink`/`Observer` interfaces, and `Bus` implementation. 2-phase migration: Phase A adds the event package + adapter bridge (compile-safe), Phase B replaces all `onText` call sites and removes the bridge.

**Tech Stack:** Go 1.25+, `sync.RWMutex`, generics for test helpers, table-driven tests with `-race`.

**Spec:** `docs/specs/PHASE-5.0-TYPED-EVENT-BUS.md`

---

## File Structure

### New Files (Create)

| File | Responsibility |
|------|---------------|
| `internal/event/event.go` | `Event` interface, `Base` struct, `Sink`/`Observer` interfaces |
| `internal/event/types.go` | All concrete event types (LLM stream, progress, research, skill, session, daemon) |
| `internal/event/bus.go` | `Bus` struct (fan-out to observers, copy-on-read, panic recovery) |
| `internal/event/sink.go` | `NopSink`, `RecorderSink`, `EventsOfType[T]` helper |
| `internal/event/adapter.go` | `OnTextToSink`, `SinkToOnText` bridge functions (Phase A temporary) |
| `internal/event/event_test.go` | Unit tests for Bus, NopSink, RecorderSink |
| `internal/event/adapter_test.go` | Unit tests for bridge adapters |
| `internal/event/bus_test.go` | Concurrency and panic-recovery tests |

### Modified Files

| File | Change |
|------|--------|
| `internal/agent/agent.go` | `onText func(string)` → `sink event.Sink` in Run, stream, executeTools, collectApprovedToolCalls |
| `internal/orchestrator/types.go` | `WorkflowInput.OnText` → `WorkflowInput.Sink` |
| `internal/orchestrator/single.go` | `input.OnText` → `input.Sink` |
| `internal/orchestrator/team.go` | Remove mutex-wrapped onText, use `input.Sink` directly |
| `internal/orchestrator/autopilot.go` | `input.OnText` → `input.Sink` with typed events |
| `internal/orchestrator/research.go` | `input.OnText` → `input.Sink` |
| `internal/research/loop.go` | `onText` field → `sink event.Sink`, `emitf()` → `sink.Emit()` |
| `internal/research/experiment.go` | `onText` field → `sink event.Sink` |
| `internal/research/runner.go` | TaskRunner.Run onText → sink |
| `internal/skill/registry.go` | `ExecuteParams.OnText` → `ExecuteParams.Sink` |
| `internal/daemon/runner.go` | `TaskRunner` interface: `onText func(string)` → `sink event.Sink` |
| `internal/daemon/daemon.go` | AgentTaskRunner, task execution callers |
| `cmd/elnath/runtime.go` | `orchestrationOutput` → `TerminalObserver`, Bus wiring |

---

## Phase A: Event Package + Adapter Bridge

### Task 1: Core Interfaces and Base Type

**Files:**
- Create: `internal/event/event.go`
- Test: `internal/event/event_test.go`

- [ ] **Step 1: Write failing test for Base type**

```go
// internal/event/event_test.go
package event

import (
	"testing"
	"time"
)

func TestNewBase(t *testing.T) {
	before := time.Now()
	b := NewBase()
	after := time.Now()

	if b.Timestamp().Before(before) || b.Timestamp().After(after) {
		t.Fatalf("timestamp %v not between %v and %v", b.Timestamp(), before, after)
	}
	if b.SessionID() != "" {
		t.Fatalf("default session ID should be empty, got %q", b.SessionID())
	}
}

func TestNewBaseWith(t *testing.T) {
	ts := time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	b := NewBaseWith(ts, "sess-123")

	if b.Timestamp() != ts {
		t.Fatalf("expected %v, got %v", ts, b.Timestamp())
	}
	if b.SessionID() != "sess-123" {
		t.Fatalf("expected sess-123, got %q", b.SessionID())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/event/ -run TestNewBase -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Implement core interfaces and Base**

```go
// internal/event/event.go
package event

import "time"

// Event is the common interface for all typed events in the system.
type Event interface {
	EventType() string
	Timestamp() time.Time
}

// Sink accepts events for publication. Replaces onText func(string).
type Sink interface {
	Emit(Event)
}

// Observer receives events from a Bus.
type Observer interface {
	OnEvent(Event)
}

// Base provides common fields for all concrete event types.
type Base struct {
	ts        time.Time
	sessionID string
}

func (b Base) Timestamp() time.Time { return b.ts }
func (b Base) SessionID() string    { return b.sessionID }

func NewBase() Base {
	return Base{ts: time.Now()}
}

func NewBaseWith(ts time.Time, sessionID string) Base {
	return Base{ts: ts, sessionID: sessionID}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/stello/elnath && go test ./internal/event/ -run TestNewBase -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/stello/elnath && git add internal/event/event.go internal/event/event_test.go
git commit -m "feat(event): add core Event/Sink/Observer interfaces and Base type"
```

---

### Task 2: Concrete Event Types

**Files:**
- Create: `internal/event/types.go`
- Modify: `internal/event/event_test.go`

- [ ] **Step 1: Write failing test for event types**

```go
// append to internal/event/event_test.go

func TestTextDeltaEvent(t *testing.T) {
	e := TextDeltaEvent{Base: NewBase(), Content: "hello"}
	if e.EventType() != "text_delta" {
		t.Fatalf("expected text_delta, got %q", e.EventType())
	}
	if e.Content != "hello" {
		t.Fatalf("expected hello, got %q", e.Content)
	}
}

func TestToolProgressEvent(t *testing.T) {
	e := ToolProgressEvent{Base: NewBase(), ToolName: "bash", Preview: "ls -la"}
	if e.EventType() != "tool_progress" {
		t.Fatalf("expected tool_progress, got %q", e.EventType())
	}
}

func TestWorkflowProgressEvent(t *testing.T) {
	e := WorkflowProgressEvent{Base: NewBase(), Intent: "research", Workflow: "deep"}
	if e.EventType() != "workflow_progress" {
		t.Fatalf("expected workflow_progress, got %q", e.EventType())
	}
}

func TestResearchProgressEvent(t *testing.T) {
	e := ResearchProgressEvent{Base: NewBase(), Phase: "hypothesis", Round: 1, Message: "testing"}
	if e.EventType() != "research_progress" {
		t.Fatalf("expected research_progress, got %q", e.EventType())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/event/ -run "TestTextDelta|TestToolProgress|TestWorkflow|TestResearch" -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement all concrete event types**

```go
// internal/event/types.go
package event

import "github.com/stello/elnath/internal/llm"

// --- LLM Stream events (replace llm.StreamEvent at the Sink layer) ---

type TextDeltaEvent struct {
	Base
	Content string
}

func (TextDeltaEvent) EventType() string { return "text_delta" }

type ToolUseStartEvent struct {
	Base
	ID   string
	Name string
}

func (ToolUseStartEvent) EventType() string { return "tool_use_start" }

type ToolUseDeltaEvent struct {
	Base
	ID    string
	Input string
}

func (ToolUseDeltaEvent) EventType() string { return "tool_use_delta" }

type ToolUseDoneEvent struct {
	Base
	ID    string
	Name  string
	Input string
}

func (ToolUseDoneEvent) EventType() string { return "tool_use_done" }

type StreamDoneEvent struct {
	Base
	Usage llm.UsageStats
}

func (StreamDoneEvent) EventType() string { return "stream_done" }

type StreamErrorEvent struct {
	Base
	Err error
}

func (StreamErrorEvent) EventType() string { return "stream_error" }

// --- Agent Lifecycle events ---

type IterationStartEvent struct {
	Base
	Iteration int
	Max       int
}

func (IterationStartEvent) EventType() string { return "iteration_start" }

type CompressionEvent struct {
	Base
	BeforeCount int
	AfterCount  int
}

func (CompressionEvent) EventType() string { return "compression" }

type ClassifiedErrorEvent struct {
	Base
	Classification string
	Err            error
}

func (ClassifiedErrorEvent) EventType() string { return "classified_error" }

type AgentFinishEvent struct {
	Base
	FinishReason string
	Usage        llm.UsageStats
}

func (AgentFinishEvent) EventType() string { return "agent_finish" }

// --- Progress events (replace daemon.ProgressEvent + orchestrationOutput) ---

type ToolProgressEvent struct {
	Base
	ToolName string
	Preview  string
}

func (ToolProgressEvent) EventType() string { return "tool_progress" }

type WorkflowProgressEvent struct {
	Base
	Intent   string
	Workflow string
}

func (WorkflowProgressEvent) EventType() string { return "workflow_progress" }

type UsageProgressEvent struct {
	Base
	Summary string
}

func (UsageProgressEvent) EventType() string { return "usage_progress" }

// --- Research events ---

type ResearchProgressEvent struct {
	Base
	Phase   string
	Round   int
	Message string
}

func (ResearchProgressEvent) EventType() string { return "research_progress" }

type HypothesisEvent struct {
	Base
	HypothesisID string
	Statement    string
	Status       string
}

func (HypothesisEvent) EventType() string { return "hypothesis" }

// --- Skill events ---

type SkillExecuteEvent struct {
	Base
	SkillName string
	Status    string
}

func (SkillExecuteEvent) EventType() string { return "skill_execute" }

// --- Session events ---

type SessionResumeEvent struct {
	Base
	SID     string
	Surface string
}

func (SessionResumeEvent) EventType() string { return "session_resume" }

// --- Daemon events ---

type DaemonTaskEvent struct {
	Base
	TaskID string
	Status string
}

func (DaemonTaskEvent) EventType() string { return "daemon_task" }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/stello/elnath && go test ./internal/event/ -run "TestTextDelta|TestToolProgress|TestWorkflow|TestResearch" -v`
Expected: PASS

- [ ] **Step 5: Run type check**

Run: `cd /Users/stello/elnath && go vet ./internal/event/`
Expected: No errors

- [ ] **Step 6: Commit**

```bash
cd /Users/stello/elnath && git add internal/event/types.go internal/event/event_test.go
git commit -m "feat(event): add all concrete event types (LLM, progress, research, skill, session, daemon)"
```

---

### Task 3: Bus Implementation

**Files:**
- Create: `internal/event/bus.go`
- Create: `internal/event/bus_test.go`

- [ ] **Step 1: Write failing tests for Bus**

```go
// internal/event/bus_test.go
package event

import (
	"sync"
	"testing"
)

type recordingObserver struct {
	events []Event
}

func (r *recordingObserver) OnEvent(e Event) {
	r.events = append(r.events, e)
}

func TestBusEmitToSingleObserver(t *testing.T) {
	bus := NewBus()
	obs := &recordingObserver{}
	bus.Subscribe(obs)

	e := TextDeltaEvent{Base: NewBase(), Content: "hello"}
	bus.Emit(e)

	if len(obs.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(obs.events))
	}
	got, ok := obs.events[0].(TextDeltaEvent)
	if !ok {
		t.Fatalf("expected TextDeltaEvent, got %T", obs.events[0])
	}
	if got.Content != "hello" {
		t.Fatalf("expected hello, got %q", got.Content)
	}
}

func TestBusEmitToMultipleObservers(t *testing.T) {
	bus := NewBus()
	obs1 := &recordingObserver{}
	obs2 := &recordingObserver{}
	bus.Subscribe(obs1)
	bus.Subscribe(obs2)

	bus.Emit(TextDeltaEvent{Base: NewBase(), Content: "world"})

	if len(obs1.events) != 1 || len(obs2.events) != 1 {
		t.Fatalf("expected 1 event each, got %d and %d", len(obs1.events), len(obs2.events))
	}
}

func TestBusObserverPanicDoesNotAffectOthers(t *testing.T) {
	bus := NewBus()

	panicObs := ObserverFunc(func(Event) { panic("boom") })
	goodObs := &recordingObserver{}

	bus.Subscribe(panicObs)
	bus.Subscribe(goodObs)

	bus.Emit(TextDeltaEvent{Base: NewBase(), Content: "safe"})

	if len(goodObs.events) != 1 {
		t.Fatalf("expected 1 event after panic observer, got %d", len(goodObs.events))
	}
}

func TestBusConcurrentEmitSubscribe(t *testing.T) {
	bus := NewBus()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			bus.Subscribe(&recordingObserver{})
		}()
		go func() {
			defer wg.Done()
			bus.Emit(TextDeltaEvent{Base: NewBase(), Content: "race"})
		}()
	}
	wg.Wait()
}

func TestBusSubscribeDuringEmit(t *testing.T) {
	bus := NewBus()
	inner := &recordingObserver{}

	outer := ObserverFunc(func(e Event) {
		bus.Subscribe(inner)
	})
	bus.Subscribe(outer)

	bus.Emit(TextDeltaEvent{Base: NewBase(), Content: "trigger"})

	bus.Emit(TextDeltaEvent{Base: NewBase(), Content: "second"})
	if len(inner.events) != 1 {
		t.Fatalf("expected inner to receive second event, got %d", len(inner.events))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/event/ -run "TestBus" -v`
Expected: FAIL — NewBus, ObserverFunc not defined

- [ ] **Step 3: Implement Bus**

```go
// internal/event/bus.go
package event

import "sync"

// ObserverFunc adapts a plain function to the Observer interface.
type ObserverFunc func(Event)

func (f ObserverFunc) OnEvent(e Event) { f(e) }

// Bus fans out events to registered observers. Safe for concurrent use.
type Bus struct {
	observers []Observer
	mu        sync.RWMutex
}

// NewBus creates an empty Bus.
func NewBus() *Bus {
	return &Bus{}
}

// Subscribe registers an observer. Thread-safe.
func (b *Bus) Subscribe(o Observer) {
	b.mu.Lock()
	b.observers = append(b.observers, o)
	b.mu.Unlock()
}

// Emit sends an event to all registered observers.
// Uses copy-on-read: copies the observer slice under RLock, then releases
// the lock before calling observers. This prevents deadlock if an observer
// calls Subscribe during OnEvent.
// Each observer call is wrapped in panic recovery so one failing observer
// does not prevent others from receiving the event.
func (b *Bus) Emit(e Event) {
	b.mu.RLock()
	snapshot := make([]Observer, len(b.observers))
	copy(snapshot, b.observers)
	b.mu.RUnlock()

	for _, o := range snapshot {
		func() {
			defer func() { recover() }()
			o.OnEvent(e)
		}()
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/stello/elnath && go test ./internal/event/ -run "TestBus" -v -race`
Expected: PASS (all 5 tests, no race detected)

- [ ] **Step 5: Commit**

```bash
cd /Users/stello/elnath && git add internal/event/bus.go internal/event/bus_test.go
git commit -m "feat(event): add Bus with copy-on-read, panic recovery, and concurrency safety"
```

---

### Task 4: NopSink, RecorderSink, and Helpers

**Files:**
- Create: `internal/event/sink.go`
- Modify: `internal/event/event_test.go`

- [ ] **Step 1: Write failing tests**

```go
// append to internal/event/event_test.go

func TestNopSinkDoesNotPanic(t *testing.T) {
	var s Sink = NopSink{}
	s.Emit(TextDeltaEvent{Base: NewBase(), Content: "ignored"})
}

func TestRecorderSink(t *testing.T) {
	r := &RecorderSink{}
	r.Emit(TextDeltaEvent{Base: NewBase(), Content: "one"})
	r.Emit(ToolProgressEvent{Base: NewBase(), ToolName: "bash"})
	r.Emit(TextDeltaEvent{Base: NewBase(), Content: "two"})

	if len(r.Events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(r.Events))
	}
}

func TestRecorderSinkConcurrent(t *testing.T) {
	r := &RecorderSink{}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Emit(TextDeltaEvent{Base: NewBase(), Content: "concurrent"})
		}()
	}
	wg.Wait()
	if len(r.Events) != 100 {
		t.Fatalf("expected 100 events, got %d", len(r.Events))
	}
}

func TestEventsOfType(t *testing.T) {
	r := &RecorderSink{}
	r.Emit(TextDeltaEvent{Base: NewBase(), Content: "one"})
	r.Emit(ToolProgressEvent{Base: NewBase(), ToolName: "bash"})
	r.Emit(TextDeltaEvent{Base: NewBase(), Content: "two"})

	texts := EventsOfType[TextDeltaEvent](r)
	if len(texts) != 2 {
		t.Fatalf("expected 2 TextDeltaEvents, got %d", len(texts))
	}
	if texts[0].Content != "one" || texts[1].Content != "two" {
		t.Fatalf("unexpected content: %q, %q", texts[0].Content, texts[1].Content)
	}

	tools := EventsOfType[ToolProgressEvent](r)
	if len(tools) != 1 {
		t.Fatalf("expected 1 ToolProgressEvent, got %d", len(tools))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/event/ -run "TestNop|TestRecorder|TestEventsOfType" -v`
Expected: FAIL — NopSink, RecorderSink, EventsOfType not defined

- [ ] **Step 3: Implement sinks and helpers**

```go
// internal/event/sink.go
package event

import "sync"

// NopSink discards all events. Replaces the nil onText pattern.
type NopSink struct{}

func (NopSink) Emit(Event) {}

// RecorderSink records events for test assertions. Thread-safe.
type RecorderSink struct {
	mu     sync.Mutex
	Events []Event
}

func (r *RecorderSink) Emit(e Event) {
	r.mu.Lock()
	r.Events = append(r.Events, e)
	r.mu.Unlock()
}

// EventsOfType filters recorded events by concrete type.
func EventsOfType[T Event](r *RecorderSink) []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []T
	for _, e := range r.Events {
		if typed, ok := e.(T); ok {
			out = append(out, typed)
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/stello/elnath && go test ./internal/event/ -run "TestNop|TestRecorder|TestEventsOfType" -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/stello/elnath && git add internal/event/sink.go internal/event/event_test.go
git commit -m "feat(event): add NopSink, RecorderSink (thread-safe), and EventsOfType helper"
```

---

### Task 5: Adapter Bridge Functions (Phase A temporary)

**Files:**
- Create: `internal/event/adapter.go`
- Create: `internal/event/adapter_test.go`

- [ ] **Step 1: Write failing tests for adapters**

```go
// internal/event/adapter_test.go
package event

import "testing"

func TestOnTextToSink(t *testing.T) {
	var received []string
	fn := func(s string) { received = append(received, s) }

	sink := OnTextToSink(fn)
	sink.Emit(TextDeltaEvent{Base: NewBase(), Content: "hello"})
	sink.Emit(TextDeltaEvent{Base: NewBase(), Content: " world"})

	if len(received) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(received))
	}
	if received[0] != "hello" || received[1] != " world" {
		t.Fatalf("unexpected: %v", received)
	}
}

func TestOnTextToSinkNonTextEventsIgnored(t *testing.T) {
	var received []string
	fn := func(s string) { received = append(received, s) }

	sink := OnTextToSink(fn)
	sink.Emit(IterationStartEvent{Base: NewBase(), Iteration: 1, Max: 10})

	if len(received) != 0 {
		t.Fatalf("expected 0 calls for non-text event, got %d", len(received))
	}
}

func TestOnTextToSinkToolProgressEncodesJSON(t *testing.T) {
	var received []string
	fn := func(s string) { received = append(received, s) }

	sink := OnTextToSink(fn)
	sink.Emit(ToolProgressEvent{Base: NewBase(), ToolName: "bash", Preview: "ls"})

	if len(received) != 1 {
		t.Fatalf("expected 1 call, got %d", len(received))
	}
	if received[0] == "" {
		t.Fatal("expected non-empty encoded progress event")
	}
}

func TestOnTextToSinkNilReturnsNopSink(t *testing.T) {
	sink := OnTextToSink(nil)
	sink.Emit(TextDeltaEvent{Base: NewBase(), Content: "should not panic"})
}

func TestSinkToOnText(t *testing.T) {
	r := &RecorderSink{}
	fn := SinkToOnText(r)

	fn("hello")
	fn(" world")

	texts := EventsOfType[TextDeltaEvent](r)
	if len(texts) != 2 {
		t.Fatalf("expected 2 TextDeltaEvents, got %d", len(texts))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/event/ -run "TestOnTextToSink|TestSinkToOnText" -v`
Expected: FAIL — functions not defined

- [ ] **Step 3: Implement adapter bridge**

```go
// internal/event/adapter.go
package event

import (
	"encoding/json"
	"strings"
)

// OnTextToSink wraps a legacy onText callback as a Sink.
// TextDeltaEvent content is forwarded directly.
// ToolProgressEvent is JSON-encoded (daemon.ProgressEvent compat).
// Other events are silently dropped.
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/stello/elnath && go test ./internal/event/ -run "TestOnTextToSink|TestSinkToOnText" -v`
Expected: PASS

- [ ] **Step 5: Run full event package tests + type check**

Run: `cd /Users/stello/elnath && go test ./internal/event/ -v -race && go vet ./internal/event/`
Expected: All tests PASS, no vet errors

- [ ] **Step 6: Commit**

```bash
cd /Users/stello/elnath && git add internal/event/adapter.go internal/event/adapter_test.go
git commit -m "feat(event): add OnTextToSink/SinkToOnText adapter bridge for incremental migration"
```

---

### Task 6: Phase A Verification Checkpoint

- [ ] **Step 1: Run full test suite to verify no regressions**

Run: `cd /Users/stello/elnath && make test`
Expected: All existing tests PASS. The event package is a leaf — no existing code depends on it yet.

- [ ] **Step 2: Run type check**

Run: `cd /Users/stello/elnath && go vet ./...`
Expected: No errors

- [ ] **Step 3: Verify event package is self-contained**

Run: `cd /Users/stello/elnath && go list -m -json ./internal/event/ | grep -A5 Imports`
Expected: Only imports `sync`, `time`, `encoding/json`, `strings`, and `github.com/stello/elnath/internal/llm` (for UsageStats in types.go)

---

## Phase B: Full Migration

### Task 7: Agent.Run — onText → Sink

**Files:**
- Modify: `internal/agent/agent.go:193` — Run signature
- Modify: `internal/agent/agent.go:330` — streamWithRetry
- Modify: `internal/agent/agent.go:351,354` — stream, chatFallback calls
- Modify: `internal/agent/agent.go:393` — chatFallback
- Modify: `internal/agent/agent.go:442-495` — stream (StreamEvent → event.* conversion)
- Modify: `internal/agent/agent.go:544-568` — executeTools, executeToolsWithStats, collectApprovedToolCalls

This is the largest single change. Read the entire agent.go before editing.

- [ ] **Step 1: Re-read agent.go to get current state**

Run: `cd /Users/stello/elnath && wc -l internal/agent/agent.go`
Read the full file in chunks before making any changes.

- [ ] **Step 2: Add event import and change Run signature**

In `internal/agent/agent.go`, change:

```go
// OLD (line 193):
func (a *Agent) Run(ctx context.Context, messages []llm.Message, onText func(string)) (*RunResult, error) {
```

to:

```go
// NEW:
func (a *Agent) Run(ctx context.Context, messages []llm.Message, sink event.Sink) (*RunResult, error) {
```

Add `"github.com/stello/elnath/internal/event"` to imports.

- [ ] **Step 3: Update streamWithRetry, stream, chatFallback, executeTools chain**

Replace all `onText func(string)` parameters with `sink event.Sink` through the internal call chain:

- `streamWithRetry(ctx, req, sink)` (line ~330)
- `stream(ctx, req, sink)` (line ~442)
- `chatFallback(ctx, req, sink)` (line ~393)
- `executeTools(ctx, msgs, calls, sink)` (line ~544)
- `executeToolsWithStats(ctx, msgs, calls, sink, ...)` (line ~548)
- `collectApprovedToolCalls(ctx, calls, sink)` (line ~559)

- [ ] **Step 4: Convert stream() internals**

In `stream()`, replace the `onText` calls with typed Sink events:

```go
// OLD (line ~457):
if onText != nil {
    onText(ev.Content)
}

// NEW:
sink.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: ev.Content})
```

The full stream() switch should emit:
- `EventTextDelta` → `sink.Emit(event.TextDeltaEvent{...})`
- `EventToolUseStart` → `sink.Emit(event.ToolUseStartEvent{...})`
- `EventToolUseDelta` → `sink.Emit(event.ToolUseDeltaEvent{...})`
- `EventToolUseDone` → `sink.Emit(event.ToolUseDoneEvent{...})`
- `EventDone` → `sink.Emit(event.StreamDoneEvent{...})`
- `EventError` → `sink.Emit(event.StreamErrorEvent{...})`

- [ ] **Step 5: Convert collectApprovedToolCalls ProgressEvent**

In `collectApprovedToolCalls()`, replace:

```go
// OLD (lines 565-568):
if onText != nil {
    preview := extractToolPreview(call.Name, string(call.Input))
    ev := daemon.ToolProgressEvent(call.Name, preview)
    onText(daemon.EncodeProgressEvent(ev))
}

// NEW:
preview := extractToolPreview(call.Name, string(call.Input))
sink.Emit(event.ToolProgressEvent{
    Base:     event.NewBase(),
    ToolName: call.Name,
    Preview:  preview,
})
```

Remove the `daemon` import if no longer used in agent.go.

- [ ] **Step 6: Convert chatFallback**

```go
// OLD (line ~399-400):
if onText != nil {
    onText(resp.Content)
}

// NEW:
if resp.Content != "" {
    sink.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: resp.Content})
}
```

- [ ] **Step 7: Verify agent package compiles**

Run: `cd /Users/stello/elnath && go build ./internal/agent/`
Expected: FAIL — callers of Agent.Run still pass `onText func(string)`. This is expected; we fix callers next.

- [ ] **Step 8: Commit agent changes (compile-broken is OK at this step)**

```bash
cd /Users/stello/elnath && git add internal/agent/agent.go
git commit -m "refactor(agent): replace onText func(string) with event.Sink in Run and internal chain"
```

---

### Task 8: Orchestrator Migration

**Files:**
- Modify: `internal/orchestrator/types.go:28`
- Modify: `internal/orchestrator/single.go:37`
- Modify: `internal/orchestrator/team.go:305-313` and all OnText refs
- Modify: `internal/orchestrator/autopilot.go` — all OnText refs
- Modify: `internal/orchestrator/research.go` — all OnText refs

- [ ] **Step 1: Re-read each file before editing**

Read `types.go`, `single.go`, `team.go`, `autopilot.go`, `research.go` to get current line numbers.

- [ ] **Step 2: Change WorkflowInput.OnText → Sink**

In `internal/orchestrator/types.go`:

```go
// OLD (line 28):
OnText   func(string) // streaming text callback (nil = silent)

// NEW:
Sink     event.Sink   // typed event sink (use event.NopSink{} for silent)
```

Add `"github.com/stello/elnath/internal/event"` to imports.

- [ ] **Step 3: Update single.go**

```go
// OLD (line 37):
result, err := a.Run(ctx, messages, input.OnText)

// NEW:
result, err := a.Run(ctx, messages, input.Sink)
```

- [ ] **Step 4: Update team.go — remove mutex wrapper, use Sink directly**

The mutex-wrapped onText at lines 305-313 is no longer needed because Bus is already thread-safe. Replace:

```go
// OLD:
var onTextMu sync.Mutex
safeInput := input
if input.OnText != nil {
    safeInput.OnText = func(text string) {
        onTextMu.Lock()
        defer onTextMu.Unlock()
        input.OnText(text)
    }
}

// NEW:
safeInput := input
// Sink is already thread-safe (Bus uses copy-on-read with RWMutex).
// No wrapper needed.
```

Replace all `safeInput.OnText(...)` calls with typed events:
- `safeInput.OnText(fmt.Sprintf("[team] completed subtask %d: %s\n", ...))` → `safeInput.Sink.Emit(event.TextDeltaEvent{...})`
- Similarly for all other OnText calls in team.go

Remove `onTextMu` and the `sync` import if no longer needed.

- [ ] **Step 5: Update autopilot.go**

Replace all `input.OnText(...)` calls with `input.Sink.Emit(event.TextDeltaEvent{...})` for text output, and `input.Sink.Emit(event.WorkflowProgressEvent{...})` for workflow status.

Read the full file first to identify all call sites (~lines 130-235).

- [ ] **Step 6: Update research.go**

```go
// OLD:
input.OnText → agent.Run(ctx, ..., input.OnText)

// NEW:
input.Sink → agent.Run(ctx, ..., input.Sink)
```

- [ ] **Step 7: Verify orchestrator package compiles**

Run: `cd /Users/stello/elnath && go build ./internal/orchestrator/`
Expected: May fail if callers in cmd/elnath haven't been updated yet. That's OK.

- [ ] **Step 8: Commit**

```bash
cd /Users/stello/elnath && git add internal/orchestrator/
git commit -m "refactor(orchestrator): replace OnText with event.Sink across all workflows"
```

---

### Task 9: Research Loop Migration

**Files:**
- Modify: `internal/research/loop.go:33,54-56,158-161`
- Modify: `internal/research/experiment.go:32,45-47,98`
- Modify: `internal/research/runner.go:110,131,146`

- [ ] **Step 1: Re-read all three files**

- [ ] **Step 2: Update Loop — onText → sink, remove emitf**

In `internal/research/loop.go`:

```go
// OLD (line 33):
onText       func(string)

// NEW:
sink         event.Sink
```

Replace `WithOnText` option:

```go
// OLD:
func WithOnText(cb func(string)) LoopOption {
    return func(l *Loop) { l.onText = cb }
}

// NEW:
func WithSink(s event.Sink) LoopOption {
    return func(l *Loop) { l.sink = s }
}
```

Replace `emitf()`:

```go
// OLD:
func (l *Loop) emitf(format string, args ...any) {
    if l.onText != nil {
        l.onText(fmt.Sprintf(format, args...))
    }
}

// NEW:
func (l *Loop) emitResearch(phase string, round int, format string, args ...any) {
    l.sink.Emit(event.ResearchProgressEvent{
        Base:    event.NewBase(),
        Phase:   phase,
        Round:   round,
        Message: fmt.Sprintf(format, args...),
    })
}
```

Update all `l.emitf(...)` calls in Run() to use `l.emitResearch(...)` with appropriate phase and round values.

Ensure `NewLoop` defaults sink to `event.NopSink{}` if no `WithSink` option provided.

- [ ] **Step 3: Update ExperimentRunner**

In `internal/research/experiment.go`:

```go
// OLD (line 32):
onText   func(string)

// OLD (lines 45-47):
func (r *ExperimentRunner) WithOnText(cb func(string)) *ExperimentRunner {
    r.onText = cb
    return r
}

// NEW:
sink     event.Sink

func (r *ExperimentRunner) WithSink(s event.Sink) *ExperimentRunner {
    r.sink = s
    return r
}
```

Default sink to `event.NopSink{}` in `NewExperimentRunner`.

Update line 98:

```go
// OLD:
result, err := a.Run(ctx, messages, r.onText)

// NEW:
result, err := a.Run(ctx, messages, r.sink)
```

- [ ] **Step 4: Update research/runner.go — TaskRunner implementation**

This file implements `daemon.TaskRunner`. The interface change happens in Task 10. For now, update the internal call to pass sink:

```go
// Read the full runner.go to identify where onText is forwarded to
// ExperimentRunner.WithOnText and Loop.WithOnText, and replace with WithSink.
```

- [ ] **Step 5: Verify research package compiles**

Run: `cd /Users/stello/elnath && go build ./internal/research/`
Expected: May fail due to daemon.TaskRunner interface mismatch. Will fix in Task 10.

- [ ] **Step 6: Commit**

```bash
cd /Users/stello/elnath && git add internal/research/
git commit -m "refactor(research): replace onText/emitf with event.Sink and typed events"
```

---

### Task 10: Skill Registry and Daemon TaskRunner Migration

**Files:**
- Modify: `internal/skill/registry.go:124,169`
- Modify: `internal/daemon/runner.go:14`
- Modify: `internal/daemon/daemon.go:42-43,520-532`

- [ ] **Step 1: Update Skill ExecuteParams**

In `internal/skill/registry.go`:

```go
// OLD (line 124):
OnText     func(string)

// NEW:
Sink       event.Sink
```

Update line 169:

```go
// OLD:
result, err := a.Run(ctx, messages, params.OnText)

// NEW:
result, err := a.Run(ctx, messages, params.Sink)
```

- [ ] **Step 2: Update daemon.TaskRunner interface**

In `internal/daemon/runner.go`:

```go
// OLD:
type TaskRunner interface {
    Run(ctx context.Context, payload TaskPayload, onText func(string)) (TaskRunnerResult, error)
}

// NEW:
type TaskRunner interface {
    Run(ctx context.Context, payload TaskPayload, sink event.Sink) (TaskRunnerResult, error)
}
```

Add `"github.com/stello/elnath/internal/event"` import.

- [ ] **Step 3: Update daemon.go callers**

Read `daemon.go` to find all TaskRunner.Run call sites (~lines 520-532) and update `onText` to `sink`. Also update `AgentTaskRunner` function type:

```go
// OLD (line 42-43):
type AgentTaskRunner func(ctx context.Context, payload TaskPayload, onText func(string)) (TaskRunnerResult, error)

// NEW:
type AgentTaskRunner func(ctx context.Context, payload TaskPayload, sink event.Sink) (TaskRunnerResult, error)
```

- [ ] **Step 4: Update research/runner.go to match new interface**

Now that `daemon.TaskRunner` uses `sink event.Sink`, update `research/runner.go:110`:

```go
// OLD:
func (r *ResearchTaskRunner) Run(ctx context.Context, payload daemon.TaskPayload, onText func(string)) (daemon.TaskRunnerResult, error) {

// NEW:
func (r *ResearchTaskRunner) Run(ctx context.Context, payload daemon.TaskPayload, sink event.Sink) (daemon.TaskRunnerResult, error) {
```

- [ ] **Step 5: Verify all three packages compile**

Run: `cd /Users/stello/elnath && go build ./internal/skill/ ./internal/daemon/ ./internal/research/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/stello/elnath && git add internal/skill/ internal/daemon/ internal/research/
git commit -m "refactor(skill,daemon): replace onText with event.Sink in ExecuteParams and TaskRunner"
```

---

### Task 11: Runtime — orchestrationOutput → TerminalObserver + Bus Wiring

**Files:**
- Modify: `cmd/elnath/runtime.go:33-88,585,647`

This is the top of the call chain where the Bus is created and observers are registered.

- [ ] **Step 1: Re-read runtime.go fully (it may be large)**

Read `cmd/elnath/runtime.go` in chunks to understand the full orchestration output wiring.

- [ ] **Step 2: Create TerminalObserver as a replacement for cliOrchestrationOutput**

Add to `cmd/elnath/runtime.go` (or a new file `cmd/elnath/observer.go`):

```go
type terminalObserver struct{}

func (terminalObserver) OnEvent(e event.Event) {
    switch ev := e.(type) {
    case event.TextDeltaEvent:
        fmt.Print(ev.Content)
    case event.WorkflowProgressEvent:
        fmt.Printf("[%s → %s]\n\n", ev.Intent, ev.Workflow)
    case event.UsageProgressEvent:
        fmt.Println()
        fmt.Println(ev.Summary)
    case event.ToolProgressEvent:
        // CLI doesn't display tool progress inline (daemon does)
    case event.ResearchProgressEvent:
        fmt.Print(ev.Message)
    }
}
```

- [ ] **Step 3: Wire Bus into the execution path**

Replace the `orchestrationOutput` wiring at line ~585:

```go
// OLD:
WorkflowInput{
    ...
    OnText: output.emitText,
}

// NEW:
bus := event.NewBus()
bus.Subscribe(terminalObserver{})
// If daemon mode, also subscribe daemon progress observer.

WorkflowInput{
    ...
    Sink: bus,
}
```

Similarly update the skill execution path at line ~647.

- [ ] **Step 4: Handle daemon mode**

For daemon mode, create a `progressObserver` that replaces `orchestrationOutput.OnProgress`:

```go
type progressObserver struct {
    onProgress func(daemon.ProgressEvent)
}

func (p progressObserver) OnEvent(e event.Event) {
    switch ev := e.(type) {
    case event.ToolProgressEvent:
        p.onProgress(daemon.ToolProgressEvent(ev.ToolName, ev.Preview))
    case event.WorkflowProgressEvent:
        p.onProgress(daemon.WorkflowProgressEvent(ev.Intent, ev.Workflow))
    case event.UsageProgressEvent:
        p.onProgress(daemon.UsageProgressEvent(ev.Summary))
    case event.TextDeltaEvent:
        if ev.Content != "" {
            p.onProgress(daemon.TextProgressEvent(ev.Content))
        }
    }
}
```

Subscribe this observer in daemon mode alongside (or instead of) the terminal observer.

- [ ] **Step 5: Remove orchestrationOutput.emitText dual-dispatch**

Once Bus + Observers handle all routing, the `emitText()` method and its `ParseProgressEvent` dual-dispatch logic can be removed. The `orchestrationOutput` struct may be reduced or eliminated entirely.

- [ ] **Step 6: Verify cmd/elnath compiles**

Run: `cd /Users/stello/elnath && go build ./cmd/elnath/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
cd /Users/stello/elnath && git add cmd/elnath/
git commit -m "refactor(runtime): replace orchestrationOutput with event.Bus + TerminalObserver"
```

---

### Task 12: Test Migration — NopSink for Existing Tests

**Files:**
- Modify: All test files that pass `nil` or a `func(string)` as onText to Agent.Run

- [ ] **Step 1: Find all test files that call Agent.Run**

Run: `cd /Users/stello/elnath && grep -rn 'a\.Run\|agent\.Run\|\.Run(ctx' --include='*_test.go' internal/ cmd/`

- [ ] **Step 2: Replace nil/func(string) with event.NopSink{} or event.RecorderSink{}**

For tests that pass `nil`:
```go
// OLD:
result, err := a.Run(ctx, messages, nil)

// NEW:
result, err := a.Run(ctx, messages, event.NopSink{})
```

For tests that pass a recording callback:
```go
// OLD:
var output strings.Builder
result, err := a.Run(ctx, messages, func(s string) { output.WriteString(s) })

// NEW:
rec := &event.RecorderSink{}
result, err := a.Run(ctx, messages, rec)
// Use event.EventsOfType[event.TextDeltaEvent](rec) to check output
```

- [ ] **Step 3: Run full test suite**

Run: `cd /Users/stello/elnath && make test`
Expected: All tests PASS

- [ ] **Step 4: Commit**

```bash
cd /Users/stello/elnath && git add -u
git commit -m "test: migrate all existing tests from onText to event.NopSink/RecorderSink"
```

---

### Task 13: Remove Adapter Bridge + Dead Code Cleanup

**Files:**
- Delete: `internal/event/adapter.go`
- Delete: `internal/event/adapter_test.go`
- Check: `internal/daemon/progress.go` — verify `EncodeProgressEvent`/`ParseProgressEvent` still needed

- [ ] **Step 1: Verify no code imports adapter functions**

Run: `cd /Users/stello/elnath && grep -rn 'OnTextToSink\|SinkToOnText' --include='*.go' .`
Expected: Only hits in `adapter.go` and `adapter_test.go`. If other files still use them, migrate those first.

- [ ] **Step 2: Delete adapter files**

```bash
cd /Users/stello/elnath && rm internal/event/adapter.go internal/event/adapter_test.go
```

- [ ] **Step 3: Check daemon.ProgressEvent usage**

Run: `cd /Users/stello/elnath && grep -rn 'EncodeProgressEvent\|ParseProgressEvent' --include='*.go' .`

If only used in `progress.go` itself and the daemon `progressObserver` (which translates event types back to `daemon.ProgressEvent` for external consumers), keep `progress.go`. If `EncodeProgressEvent` is no longer called anywhere, remove those functions.

- [ ] **Step 4: Run full test suite**

Run: `cd /Users/stello/elnath && make test`
Expected: All tests PASS

- [ ] **Step 5: Run lint**

Run: `cd /Users/stello/elnath && make lint`
Expected: No errors

- [ ] **Step 6: Commit**

```bash
cd /Users/stello/elnath && git add -u
git commit -m "chore: remove adapter bridge and dead ProgressEvent encode/decode code"
```

---

### Task 14: Integration Test — Full Event Flow

**Files:**
- Create: `internal/event/integration_test.go` (or add to `internal/agent/agent_test.go`)

- [ ] **Step 1: Write integration test**

```go
// internal/event/integration_test.go
package event_test

import (
	"testing"

	"github.com/stello/elnath/internal/event"
)

func TestBusFullEventFlow(t *testing.T) {
	bus := event.NewBus()
	rec := &event.RecorderSink{}
	bus.Subscribe(event.ObserverFunc(func(e event.Event) {
		rec.Emit(e)
	}))

	bus.Emit(event.WorkflowProgressEvent{Base: event.NewBase(), Intent: "research", Workflow: "deep"})
	bus.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: "Hello "})
	bus.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: "world"})
	bus.Emit(event.ToolProgressEvent{Base: event.NewBase(), ToolName: "bash", Preview: "ls"})
	bus.Emit(event.StreamDoneEvent{Base: event.NewBase()})

	if len(rec.Events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(rec.Events))
	}

	texts := event.EventsOfType[event.TextDeltaEvent](rec)
	if len(texts) != 2 {
		t.Fatalf("expected 2 text events, got %d", len(texts))
	}

	workflows := event.EventsOfType[event.WorkflowProgressEvent](rec)
	if len(workflows) != 1 || workflows[0].Intent != "research" {
		t.Fatalf("unexpected workflow event: %+v", workflows)
	}

	tools := event.EventsOfType[event.ToolProgressEvent](rec)
	if len(tools) != 1 || tools[0].ToolName != "bash" {
		t.Fatalf("unexpected tool event: %+v", tools)
	}
}

func TestMultipleObserversReceiveAllEvents(t *testing.T) {
	bus := event.NewBus()
	rec1 := &event.RecorderSink{}
	rec2 := &event.RecorderSink{}

	bus.Subscribe(event.ObserverFunc(func(e event.Event) { rec1.Emit(e) }))
	bus.Subscribe(event.ObserverFunc(func(e event.Event) { rec2.Emit(e) }))

	for i := 0; i < 10; i++ {
		bus.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: "x"})
	}

	if len(rec1.Events) != 10 || len(rec2.Events) != 10 {
		t.Fatalf("expected 10 events each, got %d and %d", len(rec1.Events), len(rec2.Events))
	}
}
```

- [ ] **Step 2: Run integration tests**

Run: `cd /Users/stello/elnath && go test ./internal/event/ -v -race`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
cd /Users/stello/elnath && git add internal/event/integration_test.go
git commit -m "test(event): add integration tests for full event flow and multi-observer"
```

---

### Task 15: Final Verification

- [ ] **Step 1: Run full test suite with race detection**

Run: `cd /Users/stello/elnath && go test -race ./...`
Expected: All tests PASS, no races detected

- [ ] **Step 2: Run type check**

Run: `cd /Users/stello/elnath && go vet ./...`
Expected: No errors

- [ ] **Step 3: Run lint**

Run: `cd /Users/stello/elnath && make lint`
Expected: No errors

- [ ] **Step 4: Verify no remaining onText references in production code**

Run: `cd /Users/stello/elnath && grep -rn 'onText\|OnText' --include='*.go' internal/ cmd/ | grep -v '_test.go' | grep -v 'adapter'`
Expected: Zero hits (or only in comments/docs)

- [ ] **Step 5: Verify binary builds and runs**

Run: `cd /Users/stello/elnath && make build && ./elnath version`
Expected: Binary builds successfully and prints version

- [ ] **Step 6: Final commit with version bump if applicable**

```bash
cd /Users/stello/elnath && git add -A
git commit -m "feat: Phase 5.0 Typed Event Bus complete — onText replaced with event.Sink system-wide"
```
