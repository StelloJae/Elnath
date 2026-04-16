# Magic Docs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an Event Bus Observer that automatically extracts wiki-worthy knowledge from agent activity events via LLM and writes it to the wiki.

**Architecture:** AccumulatorObserver on the event Bus accumulates events in memory. On completion triggers (AgentFinish, research conclusion, skill done), it sends an ExtractionRequest to a buffered channel. A single Extractor goroutine applies a rule filter, calls LLM extract-or-skip, and writes pages to wiki via WikiWriter with ownership-based permissions.

**Tech Stack:** Go 1.25+, `internal/event` (Event Bus), `internal/wiki` (Store, Schema), `internal/llm` (Provider), `internal/core` (App lifecycle), `log/slog`

**Spec:** `docs/specs/PHASE-5.1-MAGIC-DOCS.md`

---

## File Structure

| File | Responsibility |
|------|---------------|
| Create: `internal/magicdocs/types.go` | ExtractionRequest, ExtractionResult, PageAction, classification enum |
| Create: `internal/magicdocs/filter.go` | classify(), Filter() — rule-based event classification |
| Create: `internal/magicdocs/filter_test.go` | Table-driven tests for classify + Filter |
| Create: `internal/magicdocs/observer.go` | AccumulatorObserver — event.Observer impl, trigger detection |
| Create: `internal/magicdocs/observer_test.go` | OnEvent accumulation, trigger dispatch, backpressure |
| Create: `internal/magicdocs/writer.go` | WikiWriter — ownership checks, create/update/linked pages |
| Create: `internal/magicdocs/writer_test.go` | Ownership verification, create/update/linked page scenarios |
| Create: `internal/magicdocs/prompt.go` | buildPrompt() — LLM prompt construction |
| Create: `internal/magicdocs/extractor.go` | Extractor goroutine, LLM call, response parsing |
| Create: `internal/magicdocs/extractor_test.go` | Extract-or-skip, parse response, filter→extract pipeline |
| Create: `internal/magicdocs/magicdocs.go` | MagicDocs orchestrator — New, Start, Close, Observer |
| Create: `internal/magicdocs/magicdocs_test.go` | Lifecycle, graceful shutdown |
| Modify: `internal/config/config.go:13-36` | Add MagicDocsConfig to Config struct |
| Modify: `cmd/elnath/runtime.go:551-558` | Wire MagicDocs into runTask Bus creation |

---

### Task 1: Types

**Files:**
- Create: `internal/magicdocs/types.go`

- [ ] **Step 1: Create types.go with all data types**

```go
package magicdocs

import (
	"time"

	"github.com/stello/elnath/internal/event"
)

// ExtractionRequest is sent from AccumulatorObserver to Extractor on trigger.
type ExtractionRequest struct {
	Events    []event.Event
	SessionID string
	Trigger   string
	Timestamp time.Time
}

// ExtractionResult is the parsed LLM response.
type ExtractionResult struct {
	Pages []PageAction `json:"pages"`
}

// PageAction describes a single wiki page to create or update.
type PageAction struct {
	Action     string   `json:"action"`
	Path       string   `json:"path"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Content    string   `json:"content"`
	Confidence string   `json:"confidence"`
	Tags       []string `json:"tags"`
}

// classification is the filter verdict for an event.
type classification int

const (
	drop    classification = iota
	pass
	context_
)
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/stello/elnath && go build ./internal/magicdocs/`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/magicdocs/types.go
git commit -m "feat(magicdocs): add data types for extraction pipeline"
```

---

### Task 2: Rule Filter

**Files:**
- Create: `internal/magicdocs/filter.go`
- Create: `internal/magicdocs/filter_test.go`

- [ ] **Step 1: Write the failing test for classify**

```go
package magicdocs

import (
	"testing"
	"time"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

func TestClassify(t *testing.T) {
	base := event.NewBaseWith(time.Now(), "test-session")

	tests := []struct {
		name string
		ev   event.Event
		want classification
	}{
		// DROP
		{"text_delta", event.TextDeltaEvent{Base: base, Content: "hello"}, drop},
		{"tool_use_start", event.ToolUseStartEvent{Base: base, ID: "1", Name: "read"}, drop},
		{"tool_use_delta", event.ToolUseDeltaEvent{Base: base, ID: "1", Input: "{}"}, drop},
		{"stream_done", event.StreamDoneEvent{Base: base, Usage: llm.UsageStats{}}, drop},
		{"stream_error", event.StreamErrorEvent{Base: base}, drop},
		{"iteration_start", event.IterationStartEvent{Base: base, Iteration: 1, Max: 10}, drop},

		// PASS
		{"research_conclusion", event.ResearchProgressEvent{Base: base, Phase: "conclusion", Round: 3, Message: "done"}, pass},
		{"research_synthesis", event.ResearchProgressEvent{Base: base, Phase: "synthesis", Round: 1, Message: "sum"}, pass},
		{"hypothesis", event.HypothesisEvent{Base: base, HypothesisID: "h1", Statement: "x", Status: "validated"}, pass},
		{"agent_finish", event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}}, pass},
		{"skill_done", event.SkillExecuteEvent{Base: base, SkillName: "tdd", Status: "done"}, pass},
		{"daemon_task_done", event.DaemonTaskEvent{Base: base, TaskID: "t1", Status: "done"}, pass},

		// CONTEXT
		{"research_exploring", event.ResearchProgressEvent{Base: base, Phase: "exploring", Round: 1, Message: "..."}, context_},
		{"skill_started", event.SkillExecuteEvent{Base: base, SkillName: "tdd", Status: "started"}, context_},
		{"daemon_task_started", event.DaemonTaskEvent{Base: base, TaskID: "t1", Status: "started"}, context_},
		{"tool_use_done", event.ToolUseDoneEvent{Base: base, ID: "1", Name: "read", Input: "{}"}, context_},
		{"tool_progress", event.ToolProgressEvent{Base: base, ToolName: "bash", Preview: "ls"}, context_},
		{"compression", event.CompressionEvent{Base: base, BeforeCount: 20, AfterCount: 10}, context_},
		{"workflow_progress", event.WorkflowProgressEvent{Base: base, Intent: "research", Workflow: "deep"}, context_},
		{"usage_progress", event.UsageProgressEvent{Base: base, Summary: "$0.05"}, context_},
		{"session_resume", event.SessionResumeEvent{Base: base, ResumedSessionID: "s1", Surface: "cli"}, context_},
		{"classified_error", event.ClassifiedErrorEvent{Base: base, Classification: "rate_limit"}, context_},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.ev)
			if got != tt.want {
				t.Errorf("classify(%s) = %d, want %d", tt.name, got, tt.want)
			}
		})
	}
}

func TestFilter(t *testing.T) {
	base := event.NewBaseWith(time.Now(), "test-session")

	events := []event.Event{
		event.TextDeltaEvent{Base: base, Content: "hello"},
		event.ResearchProgressEvent{Base: base, Phase: "conclusion", Message: "found it"},
		event.ToolUseDoneEvent{Base: base, ID: "1", Name: "read", Input: "{}"},
		event.IterationStartEvent{Base: base, Iteration: 1, Max: 5},
		event.HypothesisEvent{Base: base, HypothesisID: "h1", Statement: "x", Status: "ok"},
	}

	result := Filter(events)
	if len(result.Signal) != 2 {
		t.Errorf("Signal count = %d, want 2", len(result.Signal))
	}
	if len(result.Context) != 1 {
		t.Errorf("Context count = %d, want 1", len(result.Context))
	}
}

func TestFilter_NoSignal_ReturnsEmpty(t *testing.T) {
	base := event.NewBaseWith(time.Now(), "test-session")
	events := []event.Event{
		event.TextDeltaEvent{Base: base, Content: "hello"},
		event.ToolUseDeltaEvent{Base: base, ID: "1", Input: "{}"},
	}
	result := Filter(events)
	if len(result.Signal) != 0 {
		t.Errorf("Signal count = %d, want 0", len(result.Signal))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/magicdocs/ -run TestClassify -v`
Expected: FAIL — `classify` undefined

- [ ] **Step 3: Implement filter.go**

```go
package magicdocs

import "github.com/stello/elnath/internal/event"

// FilterResult holds the classified events after rule-based filtering.
type FilterResult struct {
	Signal  []event.Event
	Context []event.Event
}

// Filter applies rule-based classification to a batch of events.
// Signal events are candidates for LLM extraction. Context events
// provide background for the LLM prompt. DROP events are discarded.
func Filter(events []event.Event) FilterResult {
	var result FilterResult
	for _, e := range events {
		switch classify(e) {
		case pass:
			result.Signal = append(result.Signal, e)
		case context_:
			result.Context = append(result.Context, e)
		}
	}
	return result
}

func classify(e event.Event) classification {
	switch ev := e.(type) {
	case event.TextDeltaEvent:
		return drop
	case event.ToolUseStartEvent:
		return drop
	case event.ToolUseDeltaEvent:
		return drop
	case event.StreamDoneEvent:
		return drop
	case event.StreamErrorEvent:
		return drop
	case event.IterationStartEvent:
		return drop

	case event.ResearchProgressEvent:
		if ev.Phase == "conclusion" || ev.Phase == "synthesis" {
			return pass
		}
		return context_
	case event.HypothesisEvent:
		return pass
	case event.AgentFinishEvent:
		return pass
	case event.SkillExecuteEvent:
		if ev.Status == "done" {
			return pass
		}
		return context_
	case event.DaemonTaskEvent:
		if ev.Status == "done" {
			return pass
		}
		return context_

	case event.ToolUseDoneEvent:
		return context_
	case event.ToolProgressEvent:
		return context_
	case event.CompressionEvent:
		return context_
	case event.WorkflowProgressEvent:
		return context_
	case event.UsageProgressEvent:
		return context_
	case event.SessionResumeEvent:
		return context_
	case event.ClassifiedErrorEvent:
		return context_

	default:
		return drop
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/stello/elnath && go test ./internal/magicdocs/ -run "TestClassify|TestFilter" -v`
Expected: PASS (all cases)

- [ ] **Step 5: Run race detector**

Run: `cd /Users/stello/elnath && go test -race ./internal/magicdocs/`
Expected: PASS, no race conditions

- [ ] **Step 6: Commit**

```bash
git add internal/magicdocs/filter.go internal/magicdocs/filter_test.go
git commit -m "feat(magicdocs): add rule-based event filter with table-driven tests"
```

---

### Task 3: AccumulatorObserver

**Files:**
- Create: `internal/magicdocs/observer.go`
- Create: `internal/magicdocs/observer_test.go`

- [ ] **Step 1: Write the failing test for AccumulatorObserver**

```go
package magicdocs

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

func TestAccumulatorObserver_AccumulatesAndTriggers(t *testing.T) {
	ch := make(chan ExtractionRequest, 16)
	obs := NewAccumulatorObserver(ch, "test-session", slog.Default())
	base := event.NewBaseWith(time.Now(), "test-session")

	obs.OnEvent(event.TextDeltaEvent{Base: base, Content: "hello"})
	obs.OnEvent(event.ToolUseDoneEvent{Base: base, ID: "1", Name: "read", Input: "{}"})
	obs.OnEvent(event.ResearchProgressEvent{Base: base, Phase: "exploring", Round: 1, Message: "..."})

	select {
	case <-ch:
		t.Fatal("should not trigger yet")
	default:
	}

	obs.OnEvent(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})

	select {
	case req := <-ch:
		if len(req.Events) != 4 {
			t.Errorf("Events count = %d, want 4", len(req.Events))
		}
		if req.SessionID != "test-session" {
			t.Errorf("SessionID = %q, want %q", req.SessionID, "test-session")
		}
		if req.Trigger != "agent_finish" {
			t.Errorf("Trigger = %q, want %q", req.Trigger, "agent_finish")
		}
	default:
		t.Fatal("expected extraction request on channel")
	}
}

func TestAccumulatorObserver_BufferResetsAfterTrigger(t *testing.T) {
	ch := make(chan ExtractionRequest, 16)
	obs := NewAccumulatorObserver(ch, "test-session", slog.Default())
	base := event.NewBaseWith(time.Now(), "test-session")

	obs.OnEvent(event.TextDeltaEvent{Base: base, Content: "first"})
	obs.OnEvent(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})
	<-ch

	obs.OnEvent(event.TextDeltaEvent{Base: base, Content: "second"})
	obs.OnEvent(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})

	req := <-ch
	if len(req.Events) != 2 {
		t.Errorf("second batch Events count = %d, want 2 (not 4)", len(req.Events))
	}
}

func TestAccumulatorObserver_BackpressureDrop(t *testing.T) {
	ch := make(chan ExtractionRequest, 1)
	obs := NewAccumulatorObserver(ch, "test-session", slog.Default())
	base := event.NewBaseWith(time.Now(), "test-session")

	obs.OnEvent(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})
	obs.OnEvent(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})

	select {
	case <-ch:
	default:
		t.Fatal("first request should be in channel")
	}
}

func TestIsTrigger(t *testing.T) {
	base := event.NewBaseWith(time.Now(), "test-session")
	tests := []struct {
		name string
		ev   event.Event
		want bool
	}{
		{"agent_finish", event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}}, true},
		{"research_conclusion", event.ResearchProgressEvent{Base: base, Phase: "conclusion"}, true},
		{"research_synthesis", event.ResearchProgressEvent{Base: base, Phase: "synthesis"}, true},
		{"research_exploring", event.ResearchProgressEvent{Base: base, Phase: "exploring"}, false},
		{"skill_done", event.SkillExecuteEvent{Base: base, SkillName: "s", Status: "done"}, true},
		{"skill_started", event.SkillExecuteEvent{Base: base, SkillName: "s", Status: "started"}, false},
		{"daemon_done", event.DaemonTaskEvent{Base: base, TaskID: "t", Status: "done"}, true},
		{"daemon_queued", event.DaemonTaskEvent{Base: base, TaskID: "t", Status: "queued"}, false},
		{"text_delta", event.TextDeltaEvent{Base: base, Content: "hi"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTrigger(tt.ev); got != tt.want {
				t.Errorf("isTrigger(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/magicdocs/ -run "TestAccumulator|TestIsTrigger" -v`
Expected: FAIL — `NewAccumulatorObserver` undefined

- [ ] **Step 3: Implement observer.go**

```go
package magicdocs

import (
	"log/slog"
	"sync"
	"time"

	"github.com/stello/elnath/internal/event"
)

// AccumulatorObserver implements event.Observer. It accumulates events in
// memory and sends an ExtractionRequest to extractCh when a trigger fires.
type AccumulatorObserver struct {
	mu        sync.Mutex
	buffer    []event.Event
	extractCh chan<- ExtractionRequest
	sessionID string
	logger    *slog.Logger
}

// NewAccumulatorObserver creates an observer that sends extraction requests
// to ch on trigger events.
func NewAccumulatorObserver(ch chan<- ExtractionRequest, sessionID string, logger *slog.Logger) *AccumulatorObserver {
	return &AccumulatorObserver{
		extractCh: ch,
		sessionID: sessionID,
		logger:    logger,
	}
}

// OnEvent implements event.Observer. Must be non-blocking.
func (a *AccumulatorObserver) OnEvent(e event.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.buffer = append(a.buffer, e)

	if isTrigger(e) {
		snapshot := make([]event.Event, len(a.buffer))
		copy(snapshot, a.buffer)
		a.buffer = a.buffer[:0]

		select {
		case a.extractCh <- ExtractionRequest{
			Events:    snapshot,
			SessionID: a.sessionID,
			Trigger:   e.EventType(),
			Timestamp: time.Now(),
		}:
		default:
			a.logger.Warn("magic-docs extraction channel full, dropping request",
				"trigger", e.EventType(),
				"buffered_events", len(snapshot),
			)
		}
	}
}

func isTrigger(e event.Event) bool {
	switch ev := e.(type) {
	case event.AgentFinishEvent:
		return true
	case event.ResearchProgressEvent:
		return ev.Phase == "conclusion" || ev.Phase == "synthesis"
	case event.SkillExecuteEvent:
		return ev.Status == "done"
	case event.DaemonTaskEvent:
		return ev.Status == "done"
	default:
		return false
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/stello/elnath && go test ./internal/magicdocs/ -run "TestAccumulator|TestIsTrigger" -v`
Expected: PASS

- [ ] **Step 5: Run race detector**

Run: `cd /Users/stello/elnath && go test -race ./internal/magicdocs/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/magicdocs/observer.go internal/magicdocs/observer_test.go
git commit -m "feat(magicdocs): add AccumulatorObserver with trigger detection"
```

---

### Task 4: WikiWriter

**Files:**
- Create: `internal/magicdocs/writer.go`
- Create: `internal/magicdocs/writer_test.go`

- [ ] **Step 1: Write the failing test for WikiWriter**

```go
package magicdocs

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stello/elnath/internal/wiki"
)

func testStore(t *testing.T) *wiki.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := wiki.NewStore(filepath.Join(dir, "wiki"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func TestWikiWriter_CreatePage(t *testing.T) {
	store := testStore(t)
	w := NewWikiWriter(store, slog.Default())

	actions := []PageAction{{
		Action:     "create",
		Path:       "analyses/test-finding.md",
		Title:      "Test Finding",
		Type:       "analysis",
		Content:    "Some finding content",
		Confidence: "medium",
		Tags:       []string{"test"},
	}}

	created, updated := w.Apply(actions, "sess-1", "agent_finish")
	if created != 1 || updated != 0 {
		t.Errorf("created=%d updated=%d, want 1,0", created, updated)
	}

	page, err := store.Read("analyses/test-finding.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if page.Title != "Test Finding" {
		t.Errorf("Title = %q, want %q", page.Title, "Test Finding")
	}
	source, _ := page.Extra["source"].(string)
	if source != "magic-docs" {
		t.Errorf("Extra[source] = %q, want %q", source, "magic-docs")
	}
	sess, _ := page.Extra["source_session"].(string)
	if sess != "sess-1" {
		t.Errorf("Extra[source_session] = %q, want %q", sess, "sess-1")
	}
}

func TestWikiWriter_UpdateOwnedPage(t *testing.T) {
	store := testStore(t)
	w := NewWikiWriter(store, slog.Default())

	err := store.Create(&wiki.Page{
		Path:    "analyses/owned.md",
		Title:   "Owned",
		Type:    wiki.PageTypeAnalysis,
		Content: "original",
		Extra: map[string]any{
			"source":         "magic-docs",
			"source_session": "old-sess",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	actions := []PageAction{{
		Action:     "update",
		Path:       "analyses/owned.md",
		Title:      "Owned Updated",
		Type:       "analysis",
		Content:    "updated content",
		Confidence: "high",
		Tags:       []string{"updated"},
	}}

	created, updated := w.Apply(actions, "sess-2", "research_progress")
	if created != 0 || updated != 1 {
		t.Errorf("created=%d updated=%d, want 0,1", created, updated)
	}

	page, _ := store.Read("analyses/owned.md")
	if page.Content != "updated content\n" {
		t.Errorf("Content = %q, want %q", page.Content, "updated content\n")
	}
}

func TestWikiWriter_UpdateHumanPage_CreatesLinkedPage(t *testing.T) {
	store := testStore(t)
	w := NewWikiWriter(store, slog.Default())

	err := store.Create(&wiki.Page{
		Path:    "concepts/go-errors.md",
		Title:   "Go Errors",
		Type:    wiki.PageTypeConcept,
		Content: "Human written content",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	actions := []PageAction{{
		Action:     "update",
		Path:       "concepts/go-errors.md",
		Title:      "Go Error Wrapping Discovery",
		Type:       "concept",
		Content:    "Auto-discovered pattern",
		Confidence: "medium",
		Tags:       []string{"go"},
	}}

	created, updated := w.Apply(actions, "sess-3", "agent_finish")
	if created != 1 || updated != 0 {
		t.Errorf("created=%d updated=%d, want 1,0 (should create linked, not update)", created, updated)
	}

	original, _ := store.Read("concepts/go-errors.md")
	if original.Content != "Human written content\n" {
		t.Error("human page should not be modified")
	}
}

func TestWikiWriter_UpdateNonexistent_FallsBackToCreate(t *testing.T) {
	store := testStore(t)
	w := NewWikiWriter(store, slog.Default())

	actions := []PageAction{{
		Action:     "update",
		Path:       "analyses/nonexistent.md",
		Title:      "New Finding",
		Type:       "analysis",
		Content:    "Content",
		Confidence: "low",
	}}

	created, updated := w.Apply(actions, "sess-4", "agent_finish")
	if created != 1 || updated != 0 {
		t.Errorf("created=%d updated=%d, want 1,0 (fallback to create)", created, updated)
	}
}

func TestIsOwnedByMagicDocs(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		want  bool
	}{
		{"both present", map[string]any{"source": "magic-docs", "source_session": "s1"}, true},
		{"source only", map[string]any{"source": "magic-docs"}, false},
		{"wrong source", map[string]any{"source": "human", "source_session": "s1"}, false},
		{"nil extra", nil, false},
		{"empty extra", map[string]any{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := &wiki.Page{Extra: tt.extra}
			if got := isOwnedByMagicDocs(page); got != tt.want {
				t.Errorf("isOwnedByMagicDocs() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/magicdocs/ -run "TestWikiWriter|TestIsOwned" -v`
Expected: FAIL — `NewWikiWriter` undefined

- [ ] **Step 3: Implement writer.go**

```go
package magicdocs

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/stello/elnath/internal/wiki"
)

// WikiWriter writes extraction results to the wiki with ownership-based
// permissions. Only pages created by Magic Docs (verified via Extra metadata)
// can be updated. Human pages are never modified.
type WikiWriter struct {
	store  *wiki.Store
	logger *slog.Logger
}

// NewWikiWriter creates a WikiWriter.
func NewWikiWriter(store *wiki.Store, logger *slog.Logger) *WikiWriter {
	return &WikiWriter{store: store, logger: logger}
}

// Apply writes page actions to the wiki. Returns counts of created and updated pages.
func (w *WikiWriter) Apply(actions []PageAction, sessionID, trigger string) (created, updated int) {
	for _, a := range actions {
		var err error
		switch a.Action {
		case "create":
			err = w.createPage(a, sessionID, trigger)
			if err == nil {
				created++
			}
		case "update":
			wasUpdate, e := w.updateOwnedPage(a, sessionID, trigger)
			err = e
			if err == nil {
				if wasUpdate {
					updated++
				} else {
					created++
				}
			}
		default:
			w.logger.Warn("magic-docs unknown action", "action", a.Action, "path", a.Path)
			continue
		}
		if err != nil {
			w.logger.Error("magic-docs wiki write failed",
				"action", a.Action,
				"path", a.Path,
				"error", err,
			)
		}
	}
	return
}

func (w *WikiWriter) createPage(a PageAction, sessionID, trigger string) error {
	page := &wiki.Page{
		Path:       a.Path,
		Title:      a.Title,
		Type:       wiki.PageType(a.Type),
		Content:    a.Content,
		Confidence: a.Confidence,
		Tags:       a.Tags,
		Extra: map[string]any{
			"source":         "magic-docs",
			"source_session": sessionID,
			"source_event":   trigger,
		},
	}
	return w.store.Create(page)
}

func (w *WikiWriter) updateOwnedPage(a PageAction, sessionID, trigger string) (wasUpdate bool, err error) {
	existing, err := w.store.Read(a.Path)
	if err != nil {
		return false, w.createPage(a, sessionID, trigger)
	}

	if !isOwnedByMagicDocs(existing) {
		return false, w.createLinkedPage(a, existing, sessionID, trigger)
	}

	existing.Content = a.Content
	existing.Confidence = a.Confidence
	existing.Tags = a.Tags
	existing.Extra["source_session"] = sessionID
	existing.Extra["source_event"] = trigger
	return true, w.store.Upsert(existing)
}

func (w *WikiWriter) createLinkedPage(a PageAction, target *wiki.Page, sessionID, trigger string) error {
	hash := shortHash(sessionID + a.Path)
	dir := filepath.Dir(a.Path)
	base := strings.TrimSuffix(filepath.Base(a.Path), ".md")
	linkedPath := filepath.Join(dir, base+"-auto-"+hash+".md")

	page := &wiki.Page{
		Path:       linkedPath,
		Title:      a.Title,
		Type:       wiki.PageType(a.Type),
		Content:    fmt.Sprintf("Related: [%s](%s)\n\n%s", target.Title, target.Path, a.Content),
		Confidence: a.Confidence,
		Tags:       a.Tags,
		Extra: map[string]any{
			"source":         "magic-docs",
			"source_session": sessionID,
			"source_event":   trigger,
			"related_to":     target.Path,
		},
	}
	return w.store.Create(page)
}

func isOwnedByMagicDocs(page *wiki.Page) bool {
	if page.Extra == nil {
		return false
	}
	source, _ := page.Extra["source"].(string)
	_, hasSession := page.Extra["source_session"]
	return source == "magic-docs" && hasSession
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:4])
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/stello/elnath && go test ./internal/magicdocs/ -run "TestWikiWriter|TestIsOwned" -v`
Expected: PASS

- [ ] **Step 5: Run race detector**

Run: `cd /Users/stello/elnath && go test -race ./internal/magicdocs/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/magicdocs/writer.go internal/magicdocs/writer_test.go
git commit -m "feat(magicdocs): add WikiWriter with ownership-based permissions"
```

---

### Task 5: Prompt Builder

**Files:**
- Create: `internal/magicdocs/prompt.go`

- [ ] **Step 1: Implement prompt.go**

```go
package magicdocs

import (
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

const systemPrompt = `You are a knowledge extraction agent for Elnath's wiki. Given a batch of agent activity events, extract wiki-worthy knowledge.

Return JSON (no markdown fences): {"pages": [...]} or {"pages": []} if nothing worth keeping.

Each page object:
{
  "action": "create" or "update",
  "path": "<type>/<slug>.md",
  "title": "Page Title",
  "type": "entity" | "concept" | "source" | "analysis" | "map",
  "content": "Markdown body (no frontmatter)",
  "confidence": "high" | "medium" | "low",
  "tags": ["tag1", "tag2"]
}

Rules:
- Only extract NOVEL knowledge: facts, insights, patterns, conclusions
- Do NOT extract: raw tool output, mechanical progress, debugging noise, trivial observations
- For "update": path must point to an existing auto-generated page
- Prefer "analysis" type for research findings, "concept" for discovered patterns
- Write content in Korean (matching the wiki's language)
- Be concise: 100-500 words per page
- Use lowercase slugs with hyphens for paths (e.g. "analyses/go-error-wrapping.md")`

func buildPrompt(req ExtractionRequest, f FilterResult, model string) llm.ChatRequest {
	var sb strings.Builder
	sb.WriteString("## Signal Events (핵심)\n")
	for i, e := range f.Signal {
		sb.WriteString(fmt.Sprintf("[%d] %s: %s\n", i+1, e.EventType(), summarizeEvent(e)))
	}
	if len(f.Context) > 0 {
		sb.WriteString("\n## Context Events (맥락)\n")
		for i, e := range f.Context {
			sb.WriteString(fmt.Sprintf("[%d] %s: %s\n", i+1, e.EventType(), summarizeEvent(e)))
		}
	}

	return llm.ChatRequest{
		Model:     model,
		System:    systemPrompt,
		MaxTokens: 4096,
		Messages: []llm.Message{
			llm.NewUserMessage(sb.String()),
		},
	}
}

func summarizeEvent(e event.Event) string {
	switch ev := e.(type) {
	case event.ResearchProgressEvent:
		return fmt.Sprintf("phase=%s round=%d %s", ev.Phase, ev.Round, ev.Message)
	case event.HypothesisEvent:
		return fmt.Sprintf("id=%s status=%s %q", ev.HypothesisID, ev.Status, ev.Statement)
	case event.AgentFinishEvent:
		return fmt.Sprintf("reason=%s", ev.FinishReason)
	case event.SkillExecuteEvent:
		return fmt.Sprintf("skill=%s status=%s", ev.SkillName, ev.Status)
	case event.DaemonTaskEvent:
		return fmt.Sprintf("task=%s status=%s", ev.TaskID, ev.Status)
	case event.ToolUseDoneEvent:
		return fmt.Sprintf("tool=%s id=%s", ev.Name, ev.ID)
	case event.ToolProgressEvent:
		return fmt.Sprintf("tool=%s %s", ev.ToolName, ev.Preview)
	case event.CompressionEvent:
		return fmt.Sprintf("before=%d after=%d", ev.BeforeCount, ev.AfterCount)
	case event.WorkflowProgressEvent:
		return fmt.Sprintf("intent=%s workflow=%s", ev.Intent, ev.Workflow)
	case event.UsageProgressEvent:
		return ev.Summary
	case event.SessionResumeEvent:
		return fmt.Sprintf("resumed=%s surface=%s", ev.ResumedSessionID, ev.Surface)
	case event.ClassifiedErrorEvent:
		return fmt.Sprintf("class=%s err=%v", ev.Classification, ev.Err)
	default:
		return e.EventType()
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/stello/elnath && go build ./internal/magicdocs/`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/magicdocs/prompt.go
git commit -m "feat(magicdocs): add LLM prompt builder for knowledge extraction"
```

---

### Task 6: Extractor

**Files:**
- Create: `internal/magicdocs/extractor.go`
- Create: `internal/magicdocs/extractor_test.go`

- [ ] **Step 1: Write the failing test for parseExtractionResult and Extractor**

```go
package magicdocs

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

func TestParseExtractionResult_ValidJSON(t *testing.T) {
	raw := `{"pages": [{"action": "create", "path": "analyses/test.md", "title": "Test", "type": "analysis", "content": "Body", "confidence": "medium", "tags": ["go"]}]}`
	result, err := parseExtractionResult(raw)
	if err != nil {
		t.Fatalf("parseExtractionResult: %v", err)
	}
	if len(result.Pages) != 1 {
		t.Fatalf("Pages count = %d, want 1", len(result.Pages))
	}
	if result.Pages[0].Title != "Test" {
		t.Errorf("Title = %q, want %q", result.Pages[0].Title, "Test")
	}
}

func TestParseExtractionResult_MarkdownFenced(t *testing.T) {
	raw := "```json\n{\"pages\": [{\"action\": \"create\", \"path\": \"a/b.md\", \"title\": \"T\", \"type\": \"analysis\", \"content\": \"C\", \"confidence\": \"low\", \"tags\": []}]}\n```"
	result, err := parseExtractionResult(raw)
	if err != nil {
		t.Fatalf("parseExtractionResult: %v", err)
	}
	if len(result.Pages) != 1 {
		t.Fatalf("Pages count = %d, want 1", len(result.Pages))
	}
}

func TestParseExtractionResult_EmptyPages(t *testing.T) {
	raw := `{"pages": []}`
	result, err := parseExtractionResult(raw)
	if err != nil {
		t.Fatalf("parseExtractionResult: %v", err)
	}
	if len(result.Pages) != 0 {
		t.Errorf("Pages count = %d, want 0", len(result.Pages))
	}
}

func TestParseExtractionResult_InvalidJSON(t *testing.T) {
	_, err := parseExtractionResult("not json at all")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestValidatePageAction(t *testing.T) {
	tests := []struct {
		name    string
		action  PageAction
		wantErr bool
	}{
		{"valid create", PageAction{Action: "create", Path: "analyses/x.md", Type: "analysis", Confidence: "medium"}, false},
		{"valid update", PageAction{Action: "update", Path: "concepts/y.md", Type: "concept", Confidence: "high"}, false},
		{"bad action", PageAction{Action: "delete", Path: "a/b.md", Type: "analysis", Confidence: "low"}, true},
		{"bad type", PageAction{Action: "create", Path: "a/b.md", Type: "unknown", Confidence: "low"}, true},
		{"bad confidence", PageAction{Action: "create", Path: "a/b.md", Type: "analysis", Confidence: "ultra"}, true},
		{"path traversal", PageAction{Action: "create", Path: "../../etc/passwd", Type: "analysis", Confidence: "low"}, true},
		{"empty path", PageAction{Action: "create", Path: "", Type: "analysis", Confidence: "low"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePageAction(tt.action)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePageAction() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// mockProvider implements llm.Provider for testing.
type mockProvider struct {
	response string
	err      error
}

func (m *mockProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.ChatResponse{Content: m.response}, nil
}
func (m *mockProvider) Stream(_ context.Context, _ llm.ChatRequest, _ func(llm.StreamEvent)) error {
	return nil
}
func (m *mockProvider) Name() string            { return "mock" }
func (m *mockProvider) Models() []llm.ModelInfo  { return nil }

func TestExtractor_ExtractAndWrite(t *testing.T) {
	store, err := wiki.NewStore(filepath.Join(t.TempDir(), "wiki"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	provider := &mockProvider{
		response: `{"pages": [{"action": "create", "path": "analyses/discovery.md", "title": "Discovery", "type": "analysis", "content": "Found something", "confidence": "medium", "tags": ["test"]}]}`,
	}

	logger := slog.Default()
	writer := NewWikiWriter(store, logger)
	ext := NewExtractor(provider, "test-model", writer, logger)

	ch := make(chan ExtractionRequest, 1)
	base := event.NewBaseWith(time.Now(), "test-session")
	ch <- ExtractionRequest{
		Events: []event.Event{
			event.ResearchProgressEvent{Base: base, Phase: "conclusion", Round: 3, Message: "found it"},
			event.AgentFinishEvent{Base: base, FinishReason: "end_turn"},
		},
		SessionID: "test-session",
		Trigger:   "agent_finish",
		Timestamp: time.Now(),
	}
	close(ch)

	ctx := context.Background()
	ext.Run(ctx, ch)

	page, err := store.Read("analyses/discovery.md")
	if err != nil {
		t.Fatalf("wiki page not created: %v", err)
	}
	if page.Title != "Discovery" {
		t.Errorf("Title = %q, want %q", page.Title, "Discovery")
	}
}

func TestExtractor_SkipsWhenNoSignal(t *testing.T) {
	store, _ := wiki.NewStore(filepath.Join(t.TempDir(), "wiki"))
	provider := &mockProvider{response: `should not be called`}
	logger := slog.Default()
	writer := NewWikiWriter(store, logger)
	ext := NewExtractor(provider, "test-model", writer, logger)

	ch := make(chan ExtractionRequest, 1)
	base := event.NewBaseWith(time.Now(), "test-session")
	ch <- ExtractionRequest{
		Events: []event.Event{
			event.TextDeltaEvent{Base: base, Content: "just text"},
		},
		SessionID: "test-session",
		Trigger:   "agent_finish",
		Timestamp: time.Now(),
	}
	close(ch)

	ext.Run(context.Background(), ch)
	// No pages should be created
	pages, _ := store.List()
	if len(pages) != 0 {
		t.Errorf("expected no pages, got %d", len(pages))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/magicdocs/ -run "TestParseExtraction|TestValidate|TestExtractor" -v`
Expected: FAIL — `parseExtractionResult` undefined

- [ ] **Step 3: Implement extractor.go**

```go
package magicdocs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/stello/elnath/internal/llm"
)

// Extractor processes ExtractionRequests from a channel: filters events,
// calls the LLM for knowledge extraction, and writes results to wiki.
type Extractor struct {
	provider llm.Provider
	model    string
	writer   *WikiWriter
	logger   *slog.Logger
}

// NewExtractor creates an Extractor.
func NewExtractor(provider llm.Provider, model string, writer *WikiWriter, logger *slog.Logger) *Extractor {
	return &Extractor{
		provider: provider,
		model:    model,
		writer:   writer,
		logger:   logger,
	}
}

// Run processes extraction requests until ch is closed or ctx is cancelled.
func (x *Extractor) Run(ctx context.Context, ch <-chan ExtractionRequest) {
	for req := range ch {
		select {
		case <-ctx.Done():
			return
		default:
		}
		x.processRequest(ctx, req)
	}
}

func (x *Extractor) processRequest(ctx context.Context, req ExtractionRequest) {
	defer func() {
		if r := recover(); r != nil {
			x.logger.Error("magic-docs extractor panic", "recover", r, "trigger", req.Trigger)
		}
	}()

	filtered := Filter(req.Events)
	if len(filtered.Signal) == 0 {
		x.logger.Debug("magic-docs skip: no signal events",
			"trigger", req.Trigger,
			"total_events", len(req.Events),
		)
		return
	}

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	prompt := buildPrompt(req, filtered, x.model)
	resp, err := x.provider.Chat(callCtx, prompt)
	if err != nil {
		x.logger.Error("magic-docs LLM call failed",
			"trigger", req.Trigger,
			"error", err,
		)
		return
	}

	result, err := parseExtractionResult(resp.Content)
	if err != nil {
		x.logger.Error("magic-docs parse failed",
			"trigger", req.Trigger,
			"error", err,
		)
		return
	}

	if len(result.Pages) == 0 {
		x.logger.Debug("magic-docs: nothing worth keeping",
			"trigger", req.Trigger,
		)
		return
	}

	var valid []PageAction
	for _, a := range result.Pages {
		if err := validatePageAction(a); err != nil {
			x.logger.Warn("magic-docs invalid page action",
				"path", a.Path,
				"error", err,
			)
			continue
		}
		valid = append(valid, a)
	}

	if len(valid) == 0 {
		return
	}

	created, updated := x.writer.Apply(valid, req.SessionID, req.Trigger)
	x.logger.Info("magic-docs extraction complete",
		"trigger", req.Trigger,
		"signal_events", len(filtered.Signal),
		"pages_created", created,
		"pages_updated", updated,
	)
}

// parseExtractionResult parses the LLM JSON response. Handles markdown
// fences and extracts the first JSON object.
func parseExtractionResult(raw string) (*ExtractionResult, error) {
	cleaned := strings.TrimSpace(raw)
	if strings.HasPrefix(cleaned, "```") {
		lines := strings.Split(cleaned, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
		}
		if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			lines = lines[:len(lines)-1]
		}
		cleaned = strings.Join(lines, "\n")
	}

	cleaned = extractFirstJSONObject(cleaned)

	var result ExtractionResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("parse extraction json: %w", err)
	}
	return &result, nil
}

// extractFirstJSONObject finds the first balanced {...} in the input.
func extractFirstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start == -1 {
		return s
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		c := s[i]
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}

var validActions = map[string]bool{"create": true, "update": true}
var validTypes = map[string]bool{
	"entity": true, "concept": true, "source": true,
	"analysis": true, "map": true,
}
var validConfidence = map[string]bool{"high": true, "medium": true, "low": true}

func validatePageAction(a PageAction) error {
	if !validActions[a.Action] {
		return fmt.Errorf("invalid action %q", a.Action)
	}
	if a.Path == "" {
		return fmt.Errorf("empty path")
	}
	if strings.Contains(a.Path, "..") {
		return fmt.Errorf("path traversal detected: %q", a.Path)
	}
	if !validTypes[a.Type] {
		return fmt.Errorf("invalid type %q", a.Type)
	}
	if !validConfidence[a.Confidence] {
		return fmt.Errorf("invalid confidence %q", a.Confidence)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/stello/elnath && go test ./internal/magicdocs/ -run "TestParseExtraction|TestValidate|TestExtractor" -v`
Expected: PASS

- [ ] **Step 5: Run all package tests + race detector**

Run: `cd /Users/stello/elnath && go test -race ./internal/magicdocs/ -v`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add internal/magicdocs/extractor.go internal/magicdocs/extractor_test.go
git commit -m "feat(magicdocs): add Extractor with LLM extract-or-skip and response parsing"
```

---

### Task 7: MagicDocs Orchestrator

**Files:**
- Create: `internal/magicdocs/magicdocs.go`
- Create: `internal/magicdocs/magicdocs_test.go`

- [ ] **Step 1: Write the failing test for lifecycle**

```go
package magicdocs

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

func TestMagicDocs_Lifecycle(t *testing.T) {
	store, err := wiki.NewStore(filepath.Join(t.TempDir(), "wiki"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	provider := &mockProvider{
		response: `{"pages": [{"action": "create", "path": "analyses/lifecycle.md", "title": "Lifecycle Test", "type": "analysis", "content": "Works", "confidence": "medium", "tags": []}]}`,
	}

	md := New(Config{
		Enabled:   true,
		Store:     store,
		Provider:  provider,
		Model:     "test-model",
		Logger:    slog.Default(),
		SessionID: "test-session",
	})

	ctx := context.Background()
	md.Start(ctx)

	bus := event.NewBus()
	bus.Subscribe(md.Observer())

	base := event.NewBaseWith(time.Now(), "test-session")
	bus.Emit(event.ResearchProgressEvent{Base: base, Phase: "conclusion", Round: 3, Message: "found it"})
	bus.Emit(event.AgentFinishEvent{Base: base, FinishReason: "end_turn"})

	closeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := md.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	page, err := store.Read("analyses/lifecycle.md")
	if err != nil {
		t.Fatalf("wiki page not created: %v", err)
	}
	if page.Title != "Lifecycle Test" {
		t.Errorf("Title = %q, want %q", page.Title, "Lifecycle Test")
	}
}

func TestMagicDocs_Disabled(t *testing.T) {
	md := New(Config{Enabled: false})
	ctx := context.Background()
	md.Start(ctx)

	obs := md.Observer()
	base := event.NewBaseWith(time.Now(), "test")
	obs.OnEvent(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})

	if err := md.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestMagicDocs_GracefulShutdown_Timeout(t *testing.T) {
	provider := &mockProvider{
		response: `{"pages": []}`,
	}
	store, _ := wiki.NewStore(filepath.Join(t.TempDir(), "wiki"))

	md := New(Config{
		Enabled:   true,
		Store:     store,
		Provider:  provider,
		Model:     "test",
		Logger:    slog.Default(),
		SessionID: "test",
	})

	ctx := context.Background()
	md.Start(ctx)

	closeCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	err := md.Close(closeCtx)
	if err != nil {
		t.Logf("Close with timeout: %v (acceptable)", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/magicdocs/ -run "TestMagicDocs" -v`
Expected: FAIL — `New` undefined

- [ ] **Step 3: Implement magicdocs.go**

```go
package magicdocs

import (
	"context"
	"log/slog"
	"sync"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

const extractChSize = 16

// Config holds the MagicDocs configuration.
type Config struct {
	Enabled   bool
	Store     *wiki.Store
	Provider  llm.Provider
	Model     string
	Logger    *slog.Logger
	SessionID string
}

// MagicDocs orchestrates automatic wiki knowledge extraction from agent events.
type MagicDocs struct {
	observer  *AccumulatorObserver
	extractor *Extractor
	extractCh chan ExtractionRequest
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	enabled   bool
	logger    *slog.Logger
}

// New creates a MagicDocs instance. If cfg.Enabled is false, all operations
// are no-ops.
func New(cfg Config) *MagicDocs {
	if !cfg.Enabled {
		return &MagicDocs{enabled: false}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	ch := make(chan ExtractionRequest, extractChSize)
	writer := NewWikiWriter(cfg.Store, logger)

	return &MagicDocs{
		observer:  NewAccumulatorObserver(ch, cfg.SessionID, logger),
		extractor: NewExtractor(cfg.Provider, cfg.Model, writer, logger),
		extractCh: ch,
		enabled:   true,
		logger:    logger,
	}
}

// Observer returns an event.Observer to subscribe to the Bus.
// If disabled, returns a no-op observer.
func (m *MagicDocs) Observer() event.Observer {
	if !m.enabled {
		return event.ObserverFunc(func(event.Event) {})
	}
	return m.observer
}

// Start begins the Extractor goroutine. Must be called after New.
func (m *MagicDocs) Start(ctx context.Context) {
	if !m.enabled {
		return
	}
	extractCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.extractor.Run(extractCtx, m.extractCh)
	}()
	m.logger.Info("magic-docs started", "session_id", m.observer.sessionID)
}

// Close gracefully shuts down MagicDocs. Waits for in-flight extraction
// to complete or ctx to expire.
func (m *MagicDocs) Close(ctx context.Context) error {
	if !m.enabled {
		return nil
	}
	close(m.extractCh)

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		m.logger.Info("magic-docs stopped gracefully")
		return nil
	case <-ctx.Done():
		if m.cancel != nil {
			m.cancel()
		}
		return ctx.Err()
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/stello/elnath && go test ./internal/magicdocs/ -run "TestMagicDocs" -v -timeout 30s`
Expected: PASS

- [ ] **Step 5: Run full package tests + race detector**

Run: `cd /Users/stello/elnath && go test -race ./internal/magicdocs/ -v -timeout 30s`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add internal/magicdocs/magicdocs.go internal/magicdocs/magicdocs_test.go
git commit -m "feat(magicdocs): add MagicDocs orchestrator with lifecycle management"
```

---

### Task 8: Configuration

**Files:**
- Modify: `internal/config/config.go:13-36`

- [ ] **Step 1: Add MagicDocsConfig to config.go**

Add the MagicDocsConfig struct after `LLMExtractionConfig` (around line 125):

```go
type MagicDocsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Model   string `yaml:"model"`
}
```

Add the field to the `Config` struct (after `LLMExtraction` field, around line 32):

```go
MagicDocs     MagicDocsConfig     `yaml:"magic_docs"`
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/stello/elnath && go build ./internal/config/`
Expected: no errors

- [ ] **Step 3: Run existing config tests**

Run: `cd /Users/stello/elnath && go test ./internal/config/ -v`
Expected: PASS (no regressions)

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add MagicDocsConfig (enabled, model)"
```

---

### Task 9: Runtime Wiring

**Files:**
- Modify: `cmd/elnath/runtime.go:551-558`

- [ ] **Step 1: Add import for magicdocs**

Add to the import block in `runtime.go`:

```go
"github.com/stello/elnath/internal/magicdocs"
```

- [ ] **Step 2: Wire MagicDocs into runTask**

In `runTask` (at `runtime.go:558`), immediately after `bus := newBus(output, !rt.daemonMode)`, add:

```go
	if rt.app.Config.MagicDocs.Enabled && rt.wikiStore != nil {
		md := magicdocs.New(magicdocs.Config{
			Enabled:   true,
			Store:     rt.wikiStore,
			Provider:  rt.provider,
			Model:     rt.app.Config.MagicDocs.Model,
			Logger:    rt.app.Logger.With("component", "magic-docs"),
			SessionID: sess.ID,
		})
		bus.Subscribe(md.Observer())
		md.Start(ctx)
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := md.Close(closeCtx); err != nil {
				rt.app.Logger.Warn("magic-docs close error", "error", err)
			}
		}()
	}
```

- [ ] **Step 3: Verify it compiles**

Run: `cd /Users/stello/elnath && go build ./cmd/elnath/`
Expected: no errors

- [ ] **Step 4: Run existing runtime tests**

Run: `cd /Users/stello/elnath && go test ./cmd/elnath/ -run TestRunTask -v -timeout 60s`
Expected: PASS (no regressions — MagicDocs disabled by default)

- [ ] **Step 5: Commit**

```bash
git add cmd/elnath/runtime.go
git commit -m "feat(runtime): wire MagicDocs into runTask event bus"
```

---

### Task 10: Integration Test

**Files:**
- Modify: `internal/magicdocs/magicdocs_test.go`

- [ ] **Step 1: Add end-to-end integration test**

Append to `magicdocs_test.go`:

```go
func TestIntegration_FullPipeline(t *testing.T) {
	store, err := wiki.NewStore(filepath.Join(t.TempDir(), "wiki"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	provider := &mockProvider{
		response: `{
			"pages": [
				{"action": "create", "path": "analyses/research-finding.md", "title": "Research Finding", "type": "analysis", "content": "## Go Error Patterns\n\nSentinel errors should be preferred.", "confidence": "high", "tags": ["go", "errors"]},
				{"action": "create", "path": "concepts/sentinel-errors.md", "title": "Sentinel Errors", "type": "concept", "content": "A pattern in Go.", "confidence": "medium", "tags": ["go"]}
			]
		}`,
	}

	md := New(Config{
		Enabled:   true,
		Store:     store,
		Provider:  provider,
		Model:     "test-model",
		Logger:    slog.Default(),
		SessionID: "integration-test",
	})

	ctx := context.Background()
	md.Start(ctx)

	bus := event.NewBus()
	bus.Subscribe(md.Observer())

	base := event.NewBaseWith(time.Now(), "integration-test")

	bus.Emit(event.WorkflowProgressEvent{Base: base, Intent: "research", Workflow: "deep_research"})
	bus.Emit(event.ToolUseDoneEvent{Base: base, ID: "1", Name: "read_file", Input: `{"path":"agent.go"}`})
	bus.Emit(event.ResearchProgressEvent{Base: base, Phase: "exploring", Round: 1, Message: "looking at errors"})
	bus.Emit(event.HypothesisEvent{Base: base, HypothesisID: "h1", Statement: "sentinels are better", Status: "validated"})
	bus.Emit(event.ResearchProgressEvent{Base: base, Phase: "conclusion", Round: 3, Message: "sentinel errors preferred"})
	bus.Emit(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})

	closeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := md.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify pages were created
	p1, err := store.Read("analyses/research-finding.md")
	if err != nil {
		t.Fatalf("analyses page not created: %v", err)
	}
	if p1.Type != wiki.PageTypeAnalysis {
		t.Errorf("p1.Type = %q, want %q", p1.Type, wiki.PageTypeAnalysis)
	}
	source, _ := p1.Extra["source"].(string)
	if source != "magic-docs" {
		t.Errorf("p1 source = %q, want magic-docs", source)
	}

	p2, err := store.Read("concepts/sentinel-errors.md")
	if err != nil {
		t.Fatalf("concepts page not created: %v", err)
	}
	if p2.Type != wiki.PageTypeConcept {
		t.Errorf("p2.Type = %q, want %q", p2.Type, wiki.PageTypeConcept)
	}

	// Verify ownership metadata
	sess, _ := p2.Extra["source_session"].(string)
	if sess != "integration-test" {
		t.Errorf("p2 source_session = %q, want integration-test", sess)
	}
	evt, _ := p2.Extra["source_event"].(string)
	if evt != "agent_finish" {
		t.Errorf("p2 source_event = %q, want agent_finish", evt)
	}
}
```

- [ ] **Step 2: Run integration test**

Run: `cd /Users/stello/elnath && go test ./internal/magicdocs/ -run TestIntegration -v -timeout 30s`
Expected: PASS

- [ ] **Step 3: Run FULL test suite with race detector**

Run: `cd /Users/stello/elnath && go test -race ./internal/magicdocs/ -v -timeout 60s`
Expected: ALL PASS

- [ ] **Step 4: Run project-wide build + vet**

Run: `cd /Users/stello/elnath && make lint && make test`
Expected: PASS (no regressions across entire project)

- [ ] **Step 5: Commit**

```bash
git add internal/magicdocs/magicdocs_test.go
git commit -m "test(magicdocs): add end-to-end integration test for full pipeline"
```

---

### Task 11: Final Verification

- [ ] **Step 1: Run full project test suite**

Run: `cd /Users/stello/elnath && make test`
Expected: ALL PASS

- [ ] **Step 2: Run full project lint**

Run: `cd /Users/stello/elnath && make lint`
Expected: no issues

- [ ] **Step 3: Verify file count and structure**

Run: `find /Users/stello/elnath/internal/magicdocs -name "*.go" | sort`
Expected:
```
internal/magicdocs/extractor.go
internal/magicdocs/extractor_test.go
internal/magicdocs/filter.go
internal/magicdocs/filter_test.go
internal/magicdocs/magicdocs.go
internal/magicdocs/magicdocs_test.go
internal/magicdocs/observer.go
internal/magicdocs/observer_test.go
internal/magicdocs/prompt.go
internal/magicdocs/types.go
internal/magicdocs/writer.go
internal/magicdocs/writer_test.go
```

12 files total (6 implementation + 5 test + 1 types).

- [ ] **Step 4: Review test coverage**

Run: `cd /Users/stello/elnath && go test -cover ./internal/magicdocs/`
Expected: ≥80% coverage

- [ ] **Step 5: Final commit (if any remaining changes)**

```bash
git commit -m "feat: Phase 5.1 Magic Docs — automatic wiki knowledge extraction from agent events"
```
