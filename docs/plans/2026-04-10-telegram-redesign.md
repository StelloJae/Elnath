# Telegram Messaging Architecture Redesign

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign Telegram integration so chat messages get instant LLM responses (no queue), task messages get clean progress + response streaming (Hermes patterns), and the daemon can't modify its own source code.

**Architecture:** Two-path message routing: Shell classifies intent before dispatch — chat/question/wiki_query go direct to LLM with streaming response, task intents go to queue as before. TelegramSink is rewritten from scratch with two independent message streams (ProgressReporter at 1.5s interval for tool status, StreamConsumer at 0.3s interval for LLM response text). Daemon workspace is sandboxed to `~/.elnath/workspace/`.

**Tech Stack:** Go 1.25+, modernc.org/sqlite, Telegram Bot API (HTTP), existing `llm.Provider.Stream()` callback pattern.

---

## File Structure

### New Files
| File | Responsibility |
|------|---------------|
| `internal/telegram/stream.go` | StreamConsumer — Go channel → Telegram message editing (0.3s interval, cursor ▉, buffer threshold) |
| `internal/telegram/stream_test.go` | StreamConsumer tests |
| `internal/telegram/progress_reporter.go` | ProgressReporter — tool progress → separate Telegram message (1.5s interval, dedup, batching) |
| `internal/telegram/progress_reporter_test.go` | ProgressReporter tests |
| `internal/telegram/chat.go` | ChatResponder — direct LLM streaming for chat/question intents |
| `internal/telegram/chat_test.go` | ChatResponder tests |

### Modified Files
| File | Changes |
|------|---------|
| `internal/telegram/sink.go` | **Full rewrite** — new TelegramSink orchestrating StreamConsumer + ProgressReporter |
| `internal/telegram/sink_test.go` | Rewrite to match new sink |
| `internal/telegram/shell.go` | Add intent classification + ChatResponder dispatch before queue routing |
| `internal/telegram/shell_test.go` | Add tests for chat vs task routing |
| `internal/telegram/http_client.go` | Add flood control retry (inline 3-attempt) |
| `internal/daemon/daemon.go` | Add `StreamObserver` interface + wiring alongside `ProgressObserver` |
| `internal/daemon/daemon_test.go` | Tests for stream observer |
| `internal/config/config.go` | Add `WorkDir` to `DaemonConfig` |
| `cmd/elnath/cmd_daemon.go` | Wire ChatResponder + sandbox workspace |
| `cmd/elnath/cmd_telegram.go` | No change needed (standalone shell doesn't run daemon) |
| `cmd/elnath/runtime.go` | Pass stream callback through orchestration output |

### Deleted Code
| What | Why |
|------|-----|
| `sink.go` lines 56-72 (`trackedMessage` struct) | Replaced by per-task `activeTask` with StreamConsumer + ProgressReporter |
| `sink.go` heartbeat system (lines 325-368) | Replaced by ProgressReporter's edit interval |
| `sink.go` deferred edit (lines 370-386) | Replaced by StreamConsumer's channel-based batching |
| `sink.go` stage bar rendering (lines 416-454) | Moved to ProgressReporter |

### Preserved Code (유지)
| What | Where |
|------|-------|
| `synthesizeAssistantSummary` | `orchestrator/autopilot.go:159` — kept as-is |
| `dedupSummary` | `orchestrator/autopilot.go:214` — kept as-is |
| `condenseSummary` | Moved to new sink.go utility section |
| `escapeHTML` | Moved to new sink.go utility section |
| `isMessageNotModifiedError` | `http_client.go:40` — kept, used by StreamConsumer |
| `isHTMLParseError` + fallback | `http_client.go:34` — kept |
| All of `http_client.go` | Kept with minor flood control addition |
| `BotClient` interface | `shell.go:27` — kept as-is |

---

## Phase 1: StreamConsumer (Foundation)

The StreamConsumer is the core building block used by both chat responses and task completions. Build and test it first in isolation.

### Task 1.1: StreamConsumer Type + Core Loop

**Files:**
- Create: `internal/telegram/stream.go`
- Test: `internal/telegram/stream_test.go`

- [ ] **Step 1: Write failing test — basic send flow**

```go
// stream_test.go
package telegram

import (
	"sync"
	"testing"
	"time"
)

type mockBot struct {
	mu       sync.Mutex
	sent     []string
	edited   map[int64][]string
	nextID   int64
}

func newMockBot() *mockBot {
	return &mockBot{edited: make(map[int64][]string), nextID: 1}
}

func (m *mockBot) SendMessageReturningID(_ context.Context, _, text string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID
	m.nextID++
	m.sent = append(m.sent, text)
	return id, nil
}

func (m *mockBot) EditMessage(_ context.Context, _ string, msgID int64, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edited[msgID] = append(m.edited[msgID], text)
	return nil
}

// implement remaining BotClient methods as no-ops...

func TestStreamConsumerBasicSend(t *testing.T) {
	bot := newMockBot()
	sc := NewStreamConsumer(bot, "123", nil)
	go sc.Run()

	sc.Send("Hello ")
	sc.Send("world!")
	sc.Finish()
	sc.Wait()

	if !sc.AlreadySent() {
		t.Fatal("expected AlreadySent=true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -run TestStreamConsumerBasicSend -v`
Expected: FAIL — `NewStreamConsumer` undefined

- [ ] **Step 3: Implement StreamConsumer**

```go
// stream.go
package telegram

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	streamEditInterval    = 300 * time.Millisecond
	streamBufferThreshold = 40
	streamCursor          = " ▉"
	streamMaxLen          = 3800
)

type streamDelta struct {
	text    string
	segment bool // true = tool boundary, start new message
	done    bool
}

type StreamConsumer struct {
	bot    BotClient
	chatID string
	logger *slog.Logger

	ch   chan streamDelta
	done chan struct{}

	mu          sync.Mutex
	msgID       int64
	buffer      strings.Builder
	lastSent    string
	lastEditAt  time.Time
	alreadySent bool
}

func NewStreamConsumer(bot BotClient, chatID string, logger *slog.Logger) *StreamConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &StreamConsumer{
		bot:    bot,
		chatID: chatID,
		logger: logger,
		ch:     make(chan streamDelta, 64),
		done:   make(chan struct{}),
	}
}

func (sc *StreamConsumer) Send(text string) {
	sc.ch <- streamDelta{text: text}
}

func (sc *StreamConsumer) NewSegment() {
	sc.ch <- streamDelta{segment: true}
}

func (sc *StreamConsumer) Finish() {
	sc.ch <- streamDelta{done: true}
}

func (sc *StreamConsumer) Wait() {
	<-sc.done
}

func (sc *StreamConsumer) AlreadySent() bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.alreadySent
}

func (sc *StreamConsumer) Run() {
	defer close(sc.done)

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		gotDone := false
		gotSegment := false

		// Drain all queued deltas.
	drain:
		for {
			select {
			case delta := <-sc.ch:
				if delta.done {
					gotDone = true
					break drain
				}
				if delta.segment {
					gotSegment = true
					break drain
				}
				sc.buffer.WriteString(delta.text)
			default:
				break drain
			}
		}

		elapsed := time.Since(sc.lastEditAt)
		bufLen := sc.buffer.Len()
		shouldFlush := gotDone || gotSegment ||
			(elapsed >= streamEditInterval && bufLen > 0) ||
			bufLen >= streamBufferThreshold

		if shouldFlush && bufLen > 0 {
			text := sc.buffer.String()
			displayText := text
			if !gotDone && !gotSegment {
				displayText += streamCursor
			}
			sc.flush(displayText)
		}

		if gotSegment {
			sc.mu.Lock()
			sc.msgID = 0
			sc.mu.Unlock()
			sc.buffer.Reset()
		}

		if gotDone {
			return
		}

		// Wait for next tick or new delta.
		select {
		case delta := <-sc.ch:
			if delta.done {
				if sc.buffer.Len() > 0 {
					sc.flush(sc.buffer.String())
				}
				return
			}
			if delta.segment {
				if sc.buffer.Len() > 0 {
					sc.flush(sc.buffer.String())
				}
				sc.mu.Lock()
				sc.msgID = 0
				sc.mu.Unlock()
				sc.buffer.Reset()
				continue
			}
			sc.buffer.WriteString(delta.text)
		case <-ticker.C:
		}
	}
}

func (sc *StreamConsumer) flush(text string) {
	sc.mu.Lock()
	if text == sc.lastSent {
		sc.mu.Unlock()
		return
	}
	sc.lastSent = text
	sc.lastEditAt = time.Now()
	msgID := sc.msgID
	sc.alreadySent = true
	sc.mu.Unlock()

	ctx := context.Background()
	if msgID > 0 {
		if err := sc.bot.EditMessage(ctx, sc.chatID, msgID, text); err != nil {
			if !isMessageNotModifiedError(err) {
				sc.logger.Warn("stream: edit failed", "msg_id", msgID, "error", err)
			}
		}
		return
	}

	newID, err := sc.bot.SendMessageReturningID(ctx, sc.chatID, text)
	if err != nil {
		sc.logger.Warn("stream: send failed", "error", err)
		return
	}
	sc.mu.Lock()
	sc.msgID = newID
	sc.mu.Unlock()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -run TestStreamConsumerBasicSend -v`
Expected: PASS

- [ ] **Step 5: Add tests for cursor, dedup, segment boundary**

```go
func TestStreamConsumerCursor(t *testing.T) {
	bot := newMockBot()
	sc := NewStreamConsumer(bot, "123", nil)
	go sc.Run()

	// Send enough to trigger buffer threshold.
	sc.Send(strings.Repeat("x", streamBufferThreshold+1))
	time.Sleep(100 * time.Millisecond)

	bot.mu.Lock()
	lastSent := bot.sent[len(bot.sent)-1]
	bot.mu.Unlock()

	if !strings.HasSuffix(lastSent, streamCursor) {
		t.Errorf("expected cursor suffix, got %q", lastSent)
	}

	sc.Finish()
	sc.Wait()
}

func TestStreamConsumerNewSegment(t *testing.T) {
	bot := newMockBot()
	sc := NewStreamConsumer(bot, "123", nil)
	go sc.Run()

	sc.Send("first message")
	sc.NewSegment()
	sc.Send("second message")
	sc.Finish()
	sc.Wait()

	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.sent) < 2 {
		t.Fatalf("expected >=2 messages, got %d", len(bot.sent))
	}
}
```

- [ ] **Step 6: Run all stream tests**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -run TestStreamConsumer -v -race`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
cd /Users/stello/elnath && git add internal/telegram/stream.go internal/telegram/stream_test.go
git commit -m "feat(telegram): add StreamConsumer — Hermes-style 0.3s streaming with cursor and segment support"
```

---

### Task 1.2: ProgressReporter Type

**Files:**
- Create: `internal/telegram/progress_reporter.go`
- Test: `internal/telegram/progress_reporter_test.go`

- [ ] **Step 1: Write failing test — tool progress batching**

```go
// progress_reporter_test.go
package telegram

import (
	"testing"
	"time"
)

func TestProgressReporterBatchesTools(t *testing.T) {
	bot := newMockBot()
	pr := NewProgressReporter(bot, "123", nil)
	go pr.Run()

	pr.ReportTool("bash", "ls -la")
	pr.ReportTool("file_write", "main.go")
	pr.ReportTool("bash", "go test")
	time.Sleep(1600 * time.Millisecond) // Wait for 1.5s flush interval.

	pr.Finish()
	pr.Wait()

	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.sent) == 0 {
		t.Fatal("expected at least one progress message")
	}
}

func TestProgressReporterDedup(t *testing.T) {
	bot := newMockBot()
	pr := NewProgressReporter(bot, "123", nil)
	go pr.Run()

	pr.ReportTool("bash", "npm test")
	pr.ReportTool("bash", "npm test")
	pr.ReportTool("bash", "npm test")
	time.Sleep(1600 * time.Millisecond)

	pr.Finish()
	pr.Wait()

	bot.mu.Lock()
	defer bot.mu.Unlock()
	// Should show "bash: npm test (×3)" not three separate lines.
	if len(bot.sent) == 0 {
		t.Fatal("expected progress message")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -run TestProgressReporter -v`
Expected: FAIL — `NewProgressReporter` undefined

- [ ] **Step 3: Implement ProgressReporter**

```go
// progress_reporter.go
package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	progressEditInterval = 1500 * time.Millisecond
)

type toolEvent struct {
	name    string
	preview string
	done    bool
}

var toolEmojis = map[string]string{
	"bash":       "🔧",
	"file_write": "📝",
	"file_read":  "📖",
	"file_edit":  "✏️",
	"git":        "📦",
	"web_search": "🔍",
	"wiki":       "📚",
}

type ProgressReporter struct {
	bot    BotClient
	chatID string
	logger *slog.Logger

	ch   chan toolEvent
	done chan struct{}

	mu    sync.Mutex
	msgID int64
	lines []string
}

func NewProgressReporter(bot BotClient, chatID string, logger *slog.Logger) *ProgressReporter {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProgressReporter{
		bot:    bot,
		chatID: chatID,
		logger: logger,
		ch:     make(chan toolEvent, 64),
		done:   make(chan struct{}),
	}
}

func (pr *ProgressReporter) ReportTool(name, preview string) {
	pr.ch <- toolEvent{name: name, preview: preview}
}

func (pr *ProgressReporter) Finish() {
	pr.ch <- toolEvent{done: true}
}

func (pr *ProgressReporter) Wait() {
	<-pr.done
}

func (pr *ProgressReporter) MessageID() int64 {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	return pr.msgID
}

func (pr *ProgressReporter) Run() {
	defer close(pr.done)

	var lastFlush time.Time

	for {
		// Wait for first event or done signal.
		ev, ok := <-pr.ch
		if !ok || ev.done {
			return
		}
		pr.appendToolLine(ev)

		// Batch: drain any additional events that arrived.
	drain:
		for {
			select {
			case ev := <-pr.ch:
				if ev.done {
					pr.flushProgress()
					return
				}
				pr.appendToolLine(ev)
			default:
				break drain
			}
		}

		// Throttle: wait for interval if needed.
		remaining := progressEditInterval - time.Since(lastFlush)
		if remaining > 0 {
			timer := time.NewTimer(remaining)
			select {
			case ev := <-pr.ch:
				timer.Stop()
				if ev.done {
					pr.flushProgress()
					return
				}
				pr.appendToolLine(ev)
			case <-timer.C:
			}
		}

		pr.flushProgress()
		lastFlush = time.Now()
	}
}

func (pr *ProgressReporter) appendToolLine(ev toolEvent) {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	emoji := toolEmojis[ev.name]
	if emoji == "" {
		emoji = "⚙️"
	}

	preview := ev.preview
	if len(preview) > 40 {
		preview = preview[:37] + "..."
	}

	var line string
	if preview != "" {
		line = fmt.Sprintf("%s %s: \"%s\"", emoji, ev.name, escapeHTML(preview))
	} else {
		line = fmt.Sprintf("%s %s", emoji, ev.name)
	}

	// Dedup: if last line has same tool+preview, increment counter.
	if len(pr.lines) > 0 {
		last := pr.lines[len(pr.lines)-1]
		if last == line {
			pr.lines[len(pr.lines)-1] = line + " (×2)"
			return
		}
		if base, count := parseRepeatSuffix(last); base == line {
			pr.lines[len(pr.lines)-1] = fmt.Sprintf("%s (×%d)", line, count+1)
			return
		}
	}

	pr.lines = append(pr.lines, line)
}

func (pr *ProgressReporter) flushProgress() {
	pr.mu.Lock()
	if len(pr.lines) == 0 {
		pr.mu.Unlock()
		return
	}
	text := strings.Join(pr.lines, "\n")
	msgID := pr.msgID
	pr.mu.Unlock()

	ctx := context.Background()
	if msgID > 0 {
		if err := pr.bot.EditMessage(ctx, pr.chatID, msgID, text); err != nil {
			if !isMessageNotModifiedError(err) {
				pr.logger.Warn("progress: edit failed", "error", err)
			}
		}
		return
	}

	newID, err := pr.bot.SendMessageReturningID(ctx, pr.chatID, text)
	if err != nil {
		pr.logger.Warn("progress: send failed", "error", err)
		return
	}
	pr.mu.Lock()
	pr.msgID = newID
	pr.mu.Unlock()
}

// parseRepeatSuffix extracts "base (×N)" → base, N. Returns "", 0 on mismatch.
func parseRepeatSuffix(s string) (string, int) {
	idx := strings.LastIndex(s, " (×")
	if idx == -1 || !strings.HasSuffix(s, ")") {
		return "", 0
	}
	base := s[:idx]
	var count int
	fmt.Sscanf(s[idx:], " (×%d)", &count)
	return base, count
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -run TestProgressReporter -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/stello/elnath && git add internal/telegram/progress_reporter.go internal/telegram/progress_reporter_test.go
git commit -m "feat(telegram): add ProgressReporter — 1.5s batched tool progress with dedup"
```

---

## Phase 2: TelegramSink Rewrite

### Task 2.1: New TelegramSink (Replaces Old)

**Files:**
- Rewrite: `internal/telegram/sink.go`
- Rewrite: `internal/telegram/sink_test.go`

- [ ] **Step 1: Write failing test — new sink progress + completion flow**

```go
// sink_test.go
package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/stello/elnath/internal/daemon"
)

func TestNewSinkProgressAndCompletion(t *testing.T) {
	bot := newMockBot()
	sink := NewTelegramSink(bot, "123", nil)

	sink.TrackUserMessage(1, 100)

	// Simulate progress events.
	sink.OnToolProgress(1, "bash", "ls -la")
	sink.OnToolProgress(1, "file_write", "main.go")

	// Wait for progress to flush.
	time.Sleep(1700 * time.Millisecond)

	// Simulate completion with summary streaming.
	sink.OnStreamDelta(1, "작업을 ")
	sink.OnStreamDelta(1, "완료했습니다!")
	sink.OnStreamDone(1)

	sink.NotifyCompletion(context.Background(), daemon.TaskCompletion{
		TaskID:      1,
		Status:      daemon.StatusDone,
		Summary:     "작업을 완료했습니다!",
		StartedAt:   time.Now().Add(-2 * time.Minute),
		CompletedAt: time.Now(),
	})

	// Verify: user message got reaction, progress message exists, response message exists.
	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.sent) < 2 {
		t.Errorf("expected >=2 messages (progress + response), got %d", len(bot.sent))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -run TestNewSinkProgressAndCompletion -v`
Expected: FAIL — method signatures don't match

- [ ] **Step 3: Rewrite sink.go**

```go
// sink.go
package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/stello/elnath/internal/daemon"
)

const maxMessageLen = 4000

type activeTask struct {
	userMsgID int64
	progress  *ProgressReporter
	stream    *StreamConsumer
	cancel    context.CancelFunc
}

type TelegramSink struct {
	bot    BotClient
	chatID string
	logger *slog.Logger

	mu     sync.Mutex
	active map[int64]*activeTask
}

func NewTelegramSink(bot BotClient, chatID string, logger *slog.Logger) *TelegramSink {
	if logger == nil {
		logger = slog.Default()
	}
	return &TelegramSink{
		bot:    bot,
		chatID: chatID,
		logger: logger,
		active: make(map[int64]*activeTask),
	}
}

func (s *TelegramSink) TrackUserMessage(taskID, userMsgID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.ensureTask(taskID)
	task.userMsgID = userMsgID
}

func (s *TelegramSink) OnToolProgress(taskID int64, toolName, preview string) {
	s.mu.Lock()
	task := s.ensureTask(taskID)
	pr := task.progress
	s.mu.Unlock()

	pr.ReportTool(toolName, preview)
}

func (s *TelegramSink) OnStreamDelta(taskID int64, text string) {
	s.mu.Lock()
	task := s.ensureTask(taskID)
	sc := task.stream
	s.mu.Unlock()

	sc.Send(text)
}

func (s *TelegramSink) OnStreamDone(taskID int64) {
	s.mu.Lock()
	task := s.active[taskID]
	s.mu.Unlock()

	if task != nil && task.stream != nil {
		task.stream.Finish()
		task.stream.Wait()
	}
}

// OnProgress implements daemon.ProgressObserver. Parses the legacy progress
// format and routes to either ProgressReporter or StreamConsumer.
func (s *TelegramSink) OnProgress(taskID int64, progress string) {
	rendered := daemon.RenderProgress(progress)
	if rendered == "" {
		return
	}

	// Summary stream → route to StreamConsumer.
	if text, ok := parseSummaryStream(rendered); ok {
		s.OnStreamDelta(taskID, text)
		return
	}

	// Stage marker → route to ProgressReporter as stage info.
	if stage, ok := parseStageMarker(rendered); ok {
		s.OnToolProgress(taskID, stage, "")
		return
	}

	// Default: tool progress text.
	name, preview := parseToolProgress(rendered)
	s.OnToolProgress(taskID, name, preview)
}

func (s *TelegramSink) NotifyCompletion(_ context.Context, c daemon.TaskCompletion) error {
	s.mu.Lock()
	task := s.active[c.TaskID]
	delete(s.active, c.TaskID)
	s.mu.Unlock()

	if task == nil {
		return nil
	}

	// Stop progress reporter.
	task.progress.Finish()
	task.progress.Wait()

	// Set reaction on user's original message.
	ctx := context.Background()
	if task.userMsgID > 0 {
		emoji := "✅"
		if c.Status == daemon.StatusFailed {
			emoji = "❌"
		}
		_ = s.bot.SetReaction(ctx, s.chatID, task.userMsgID, emoji)
	}

	// Edit progress message with final status.
	icon := "✅"
	label := "Complete"
	if c.Status == daemon.StatusFailed {
		icon = "❌"
		label = "Failed"
	}

	elapsed := formatElapsed(c.StartedAt, c.CompletedAt)
	header := fmt.Sprintf("%s <b>%s</b>%s <code>#%d</code>", icon, label, elapsed, c.TaskID)
	if prMsgID := task.progress.MessageID(); prMsgID > 0 {
		_ = s.bot.EditMessage(ctx, s.chatID, prMsgID, header)
	}

	// If stream consumer hasn't sent the summary yet, type it out now.
	if !task.stream.AlreadySent() {
		summary := condenseSummary(emptyFallback(c.Summary, "-"))
		if summary != "" {
			_ = s.bot.SendMessage(ctx, s.chatID, summary)
		}
	} else {
		task.stream.Finish()
		task.stream.Wait()
	}

	return nil
}

func (s *TelegramSink) String() string {
	return "TelegramSink"
}

func (s *TelegramSink) ensureTask(taskID int64) *activeTask {
	task := s.active[taskID]
	if task != nil {
		return task
	}

	task = &activeTask{
		progress: NewProgressReporter(s.bot, s.chatID, s.logger),
		stream:   NewStreamConsumer(s.bot, s.chatID, s.logger),
	}
	go task.progress.Run()
	go task.stream.Run()
	s.active[taskID] = task
	return task
}

// --- Utilities (preserved from old sink) ---

func parseSummaryStream(s string) (string, bool) {
	const prefix = "[summary] "
	if strings.HasPrefix(s, prefix) {
		return strings.TrimPrefix(s, prefix), true
	}
	return "", false
}

func parseStageMarker(s string) (string, bool) {
	for _, prefix := range []string{"[autopilot] stage: ", "[team] ", "[research] "} {
		if strings.HasPrefix(s, prefix) {
			stage := strings.TrimSpace(strings.TrimPrefix(s, prefix))
			stage = strings.TrimSuffix(stage, "\n")
			if stage != "" {
				return stage, true
			}
		}
	}
	return "", false
}

func parseToolProgress(s string) (name, preview string) {
	s = strings.TrimSpace(s)
	if len(s) > 50 {
		s = s[:50]
	}
	return "tool", s
}

func formatElapsed(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	d := end.Sub(start)
	if d >= time.Minute {
		return fmt.Sprintf(" (%dm%ds)", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf(" (%ds)", int(d.Seconds()))
}

func condenseSummary(raw string) string {
	raw = strings.TrimSpace(raw)
	lines := strings.Split(raw, "\n")

	var result []string
	totalLen := 0
	const maxSummaryLen = 500

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "##") || strings.HasPrefix(line, "---") {
			continue
		}
		escaped := escapeHTML(line)
		if totalLen+len(escaped) > maxSummaryLen {
			result = append(result, escaped[:min(len(escaped), maxSummaryLen-totalLen)]+"…")
			break
		}
		result = append(result, escaped)
		totalLen += len(escaped) + 1
	}

	if len(result) == 0 {
		return escapeHTML(raw[:min(len(raw), maxSummaryLen)])
	}
	return strings.Join(result, "\n")
}

func emptyFallback(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

var htmlReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

func escapeHTML(s string) string {
	return htmlReplacer.Replace(s)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 4: Run all telegram tests**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -v -race`
Expected: PASS (some old tests may need updating — fix them)

- [ ] **Step 5: Commit**

```bash
cd /Users/stello/elnath && git add internal/telegram/sink.go internal/telegram/sink_test.go
git commit -m "feat(telegram): rewrite TelegramSink — separate ProgressReporter + StreamConsumer per task"
```

---

## Phase 3: Chat Router

### Task 3.1: ChatResponder — Direct LLM Streaming for Chat Intents

**Files:**
- Create: `internal/telegram/chat.go`
- Test: `internal/telegram/chat_test.go`

- [ ] **Step 1: Write failing test**

```go
// chat_test.go
package telegram

import (
	"context"
	"testing"

	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/llm"
)

type mockProvider struct {
	response string
}

func (m *mockProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: m.response}, nil
}

func (m *mockProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	for _, r := range m.response {
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: string(r)})
	}
	cb(llm.StreamEvent{Type: llm.EventDone})
	return nil
}

func (m *mockProvider) Name() string         { return "mock" }
func (m *mockProvider) Models() []llm.ModelInfo { return nil }

func TestChatResponderStreamsResponse(t *testing.T) {
	bot := newMockBot()
	provider := &mockProvider{response: "안녕하세요! 무엇을 도와드릴까요?"}
	responder := NewChatResponder(provider, bot, "123", nil)

	err := responder.Respond(context.Background(), "안녕", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.sent) == 0 {
		t.Fatal("expected at least one message sent")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -run TestChatResponder -v`
Expected: FAIL — `NewChatResponder` undefined

- [ ] **Step 3: Implement ChatResponder**

```go
// chat.go
package telegram

import (
	"context"
	"log/slog"

	"github.com/stello/elnath/internal/llm"
)

type ChatResponder struct {
	provider llm.Provider
	bot      BotClient
	chatID   string
	logger   *slog.Logger
}

func NewChatResponder(provider llm.Provider, bot BotClient, chatID string, logger *slog.Logger) *ChatResponder {
	if logger == nil {
		logger = slog.Default()
	}
	return &ChatResponder{
		provider: provider,
		bot:      bot,
		chatID:   chatID,
		logger:   logger,
	}
}

// Respond streams an LLM response directly to Telegram, bypassing the task queue.
// Used for chat, question, and wiki_query intents.
func (c *ChatResponder) Respond(ctx context.Context, userMessage string, replyToMsgID int64) error {
	sc := NewStreamConsumer(c.bot, c.chatID, c.logger)
	go sc.Run()

	systemPrompt := `You are a personal AI assistant. Respond naturally in the user's language.
Be concise, helpful, and conversational. Use 한국어 when the user speaks Korean.`

	req := llm.ChatRequest{
		Messages:    []llm.Message{llm.NewUserMessage(userMessage)},
		System:      systemPrompt,
		MaxTokens:   1024,
		Temperature: 0.7,
	}

	err := c.provider.Stream(ctx, req, func(ev llm.StreamEvent) {
		switch ev.Type {
		case llm.EventTextDelta:
			sc.Send(ev.Content)
		case llm.EventDone:
			sc.Finish()
		}
	})
	if err != nil {
		sc.Finish()
		sc.Wait()
		return c.bot.SendMessage(ctx, c.chatID, "⚠️ 응답 생성 중 오류가 발생했습니다.")
	}

	sc.Wait()
	return nil
}
```

- [ ] **Step 4: Run test**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -run TestChatResponder -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/stello/elnath && git add internal/telegram/chat.go internal/telegram/chat_test.go
git commit -m "feat(telegram): add ChatResponder — direct LLM streaming for chat intents"
```

---

### Task 3.2: Shell Intent Router — Chat vs Task Dispatch

**Files:**
- Modify: `internal/telegram/shell.go`
- Modify: `internal/telegram/shell_test.go`

- [ ] **Step 1: Write failing test — chat message bypasses queue**

```go
// Add to shell_test.go

func TestShellChatBypassesQueue(t *testing.T) {
	bot := newMockBot()
	queue := newTestQueue(t) // existing helper
	approvals := newTestApprovalStore(t) // existing helper
	provider := &mockProvider{response: "안녕하세요!"}
	classifier := &mockClassifier{intent: conversation.IntentChat}

	shell, err := NewShell(queue, approvals, bot, "123", t.TempDir()+"/state.json",
		WithChatResponder(NewChatResponder(provider, bot, "123", nil)),
		WithClassifier(classifier, provider),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = shell.HandleUpdate(context.Background(), Update{
		ID: 1,
		Message: Message{ChatID: "123", MessageID: 10, Text: "안녕!"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Chat message should NOT be in the queue.
	tasks, _ := queue.List(context.Background())
	for _, task := range tasks {
		if strings.Contains(task.Payload, "안녕") {
			t.Fatal("chat message should not be queued")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -run TestShellChatBypassesQueue -v`
Expected: FAIL — `WithChatResponder` undefined

- [ ] **Step 3: Add functional options and intent routing to Shell**

Modify `internal/telegram/shell.go`:

Add these types and modify the Shell struct:

```go
// IntentClassifier classifies user messages. Subset of conversation.LLMClassifier.
type IntentClassifier interface {
	Classify(ctx context.Context, provider llm.Provider, message string, history []llm.Message) (conversation.Intent, error)
}

// ShellOption configures optional Shell capabilities.
type ShellOption func(*Shell)

// WithChatResponder enables direct chat response for non-task intents.
func WithChatResponder(responder *ChatResponder) ShellOption {
	return func(s *Shell) { s.chatResponder = responder }
}

// WithClassifier enables intent classification before dispatch.
func WithClassifier(classifier IntentClassifier, provider llm.Provider) ShellOption {
	return func(s *Shell) {
		s.classifier = classifier
		s.classifyProvider = provider
	}
}
```

Add fields to Shell struct:

```go
type Shell struct {
	queue              *daemon.Queue
	approvals          *daemon.ApprovalStore
	bot                BotClient
	chatID             string
	statePath          string
	skipNotifyComplete bool
	chatResponder      *ChatResponder      // nil = all messages go to queue
	classifier         IntentClassifier     // nil = skip classification
	classifyProvider   llm.Provider         // provider for classifier
}
```

Modify `NewShell` to accept options:

```go
func NewShell(queue *daemon.Queue, approvals *daemon.ApprovalStore, bot BotClient, chatID, statePath string, opts ...ShellOption) (*Shell, error) {
	// ... existing validation ...
	s := &Shell{
		queue:     queue,
		approvals: approvals,
		bot:       bot,
		chatID:    strings.TrimSpace(chatID),
		statePath: statePath,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}
```

Modify `HandleUpdate` to classify intent before dispatch:

```go
func (s *Shell) HandleUpdate(ctx context.Context, update Update) error {
	if strings.TrimSpace(update.Message.Text) == "" {
		return nil
	}
	if update.Message.ChatID != "" && update.Message.ChatID != s.chatID {
		return nil
	}

	text := strings.TrimSpace(update.Message.Text)
	fields := strings.Fields(text)

	// Explicit commands always go to command handler.
	if len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
		reply, err := s.handleCommand(ctx, text)
		if err != nil {
			reply = "⚠️ " + err.Error()
		}
		return s.bot.SendMessage(ctx, s.chatID, reply)
	}

	// Non-command messages: classify intent if classifier is available.
	if s.classifier != nil && s.chatResponder != nil {
		intent, err := s.classifier.Classify(ctx, s.classifyProvider, text, nil)
		if err != nil {
			s.logger.Warn("intent classification failed, falling back to queue", "error", err)
		} else if isChatIntent(intent) {
			// Direct response — no queue.
			_ = s.bot.SetReaction(ctx, s.chatID, update.Message.MessageID, "💬")
			return s.chatResponder.Respond(ctx, text, update.Message.MessageID)
		}
	}

	// Task intent — enqueue.
	if update.Message.MessageID > 0 {
		_ = s.bot.SetReaction(ctx, s.chatID, update.Message.MessageID, "👀")
	}
	reply, err := s.enqueueNewTask(ctx, text)
	if err != nil {
		reply = "⚠️ " + err.Error()
	}
	return s.bot.SendMessage(ctx, s.chatID, reply)
}

func isChatIntent(intent conversation.Intent) bool {
	switch intent {
	case conversation.IntentChat, conversation.IntentQuestion, conversation.IntentWikiQuery:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4: Fix existing tests for new NewShell signature**

All existing `NewShell` calls need `...ShellOption` added (no options = backward compatible since it's variadic).

- [ ] **Step 5: Run all shell tests**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -run TestShell -v -race`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/stello/elnath && git add internal/telegram/shell.go internal/telegram/shell_test.go
git commit -m "feat(telegram): chat vs task routing — classify intent before dispatch"
```

---

### Task 3.3: Wire Chat Router in cmd_daemon.go

**Files:**
- Modify: `cmd/elnath/cmd_daemon.go:127-149`

- [ ] **Step 1: Update daemon start to pass classifier + chat responder to Shell**

In `cmdDaemonStart`, after creating `bot` and before creating `shell`:

```go
chatResponder := telegram.NewChatResponder(provider, bot, cfg.Telegram.ChatID, app.Logger)
classifier := conversation.NewLLMClassifier()

shell, shellErr := telegram.NewShell(queue, approvalStore, bot, cfg.Telegram.ChatID, statePath,
	telegram.WithChatResponder(chatResponder),
	telegram.WithClassifier(classifier, provider),
)
```

- [ ] **Step 2: Add `llm` import and verify build**

Run: `cd /Users/stello/elnath && go build ./cmd/elnath/`
Expected: Build succeeds

- [ ] **Step 3: Run full test suite**

Run: `cd /Users/stello/elnath && go test ./... -race -count=1 2>&1 | tail -20`
Expected: All tests pass

- [ ] **Step 4: Commit**

```bash
cd /Users/stello/elnath && git add cmd/elnath/cmd_daemon.go
git commit -m "feat(telegram): wire chat router — classifier + ChatResponder in daemon startup"
```

---

## Phase 4: Daemon Workspace Sandbox

### Task 4.1: Add WorkDir Config + Enforce Sandbox

**Files:**
- Modify: `internal/config/config.go:71-77`
- Modify: `cmd/elnath/cmd_daemon.go`
- Modify: `cmd/elnath/runtime.go:137`

- [ ] **Step 1: Add WorkDir to DaemonConfig**

```go
type DaemonConfig struct {
	SocketPath        string `yaml:"socket_path"`
	MaxWorkers        int    `yaml:"max_workers"`
	MaxRecoveries     int    `yaml:"max_recoveries"`
	InactivityTimeout int    `yaml:"inactivity_timeout_seconds"`
	WallClockTimeout  int    `yaml:"wall_clock_timeout_seconds"`
	WorkDir           string `yaml:"work_dir"`
}
```

- [ ] **Step 2: Set default WorkDir in DefaultConfig**

Find the `DefaultConfig()` function and add:

```go
cfg.Daemon.WorkDir = filepath.Join(homeDir, ".elnath", "workspace")
```

- [ ] **Step 3: Create workspace dir and use it for tool registry cwd**

In `cmdDaemonStart`, before `buildExecutionRuntime`:

```go
workDir := cfg.Daemon.WorkDir
if workDir == "" {
	home, _ := os.UserHomeDir()
	workDir = filepath.Join(home, ".elnath", "workspace")
}
if err := os.MkdirAll(workDir, 0o755); err != nil {
	return fmt.Errorf("create workspace dir: %w", err)
}
app.Logger.Info("daemon workspace", "dir", workDir)
```

- [ ] **Step 4: Pass workDir to buildExecutionRuntime**

Modify `buildExecutionRuntime` to accept a `workDir` parameter and use it instead of `os.Getwd()`:

In `runtime.go:137`, change:

```go
cwd, _ := os.Getwd()
reg := buildToolRegistry(cwd)
```

To use the passed workDir parameter. Add `workDir string` parameter to `buildExecutionRuntime`.

In daemon mode, pass `workDir`. In interactive mode (`cmd_run.go`), keep `os.Getwd()`.

- [ ] **Step 5: Verify build**

Run: `cd /Users/stello/elnath && go build ./cmd/elnath/`
Expected: Build succeeds

- [ ] **Step 6: Run tests**

Run: `cd /Users/stello/elnath && go test ./... -race -count=1 2>&1 | tail -20`
Expected: All tests pass

- [ ] **Step 7: Commit**

```bash
cd /Users/stello/elnath && git add internal/config/config.go cmd/elnath/cmd_daemon.go cmd/elnath/runtime.go
git commit -m "feat(daemon): workspace sandbox — isolate daemon execution from source directory"
```

---

## Phase 5: HTTP Client Hardening

### Task 5.1: Flood Control Inline Retry

**Files:**
- Modify: `internal/telegram/http_client.go`
- Modify: `internal/telegram/http_client_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestHTTPClientRetryOnFloodControl(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(429)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":          false,
				"description": "Too Many Requests: retry after 1",
				"parameters":  map[string]int{"retry_after": 1},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":     true,
			"result": map[string]int64{"message_id": 1},
		})
	}))
	defer srv.Close()

	client := NewHTTPClient("test-token", srv.URL)
	_, err := client.SendMessageReturningID(context.Background(), "123", "hello")
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -run TestHTTPClientRetryOnFloodControl -v`
Expected: FAIL — no retry logic

- [ ] **Step 3: Add flood control retry to postSendMessage and postEditMessage**

Add a `isFloodControl` check and retry loop:

```go
func isFloodControl(err error) (retryAfter int, ok bool) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 {
		return 0, false
	}
	// Parse "retry after N" from description.
	var after int
	if _, scanErr := fmt.Sscanf(apiErr.Description, "Too Many Requests: retry after %d", &after); scanErr == nil && after > 0 {
		return after, true
	}
	return 1, true // default 1s
}
```

Wrap `postSendMessage` in a 3-attempt retry loop in `doSendMessage`.

- [ ] **Step 4: Run test**

Run: `cd /Users/stello/elnath && go test ./internal/telegram/ -run TestHTTPClient -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/stello/elnath && git add internal/telegram/http_client.go internal/telegram/http_client_test.go
git commit -m "fix(telegram): add flood control retry — 3 attempts with Telegram-provided backoff"
```

---

## Phase 6: Integration Wiring

### Task 6.1: Connect New Sink to Daemon Progress Pipeline

**Files:**
- Modify: `cmd/elnath/cmd_daemon.go:139-142`
- Modify: `cmd/elnath/runtime.go:283-331`

- [ ] **Step 1: Update daemon wiring**

The new `TelegramSink` still implements `daemon.ProgressObserver` via its `OnProgress` method, so existing wiring works:

```go
d.WithProgressObserver(tgSink) // unchanged — OnProgress dispatches to progress/stream internally
```

Verify this compiles and runs correctly.

- [ ] **Step 2: Update orchestration output to emit summary via stream**

In `runtime.go`, the `synthesizeAssistantSummary` function currently emits `[summary] text` via `onText`. The new sink's `OnProgress` method already parses `[summary]` prefix and routes to `OnStreamDelta`. So this works without changes.

However, the current `synthesizeAssistantSummary` does a single `provider.Chat()` call and returns the full text — it doesn't stream. To get real streaming, modify it to use `provider.Stream()`:

In `autopilot.go:188`, change:

```go
// Before: single Chat call
resp, err := provider.Chat(ctx, llm.ChatRequest{...})

// After: stream deltas through onText
var result strings.Builder
err := provider.Stream(ctx, llm.ChatRequest{
	Messages:  []llm.Message{llm.NewUserMessage(prompt)},
	MaxTokens: 200,
}, func(ev llm.StreamEvent) {
	if ev.Type == llm.EventTextDelta {
		result.WriteString(ev.Content)
		if onText != nil {
			onText("[summary] " + result.String())
		}
	}
})
```

This streams the summary token-by-token through the `[summary]` channel, which the sink routes to StreamConsumer for real-time Telegram editing.

- [ ] **Step 3: Run full test suite**

Run: `cd /Users/stello/elnath && go test ./... -race -count=1 2>&1 | tail -20`
Expected: All tests pass

- [ ] **Step 4: Build and verify**

Run: `cd /Users/stello/elnath && go build ./cmd/elnath/ && go vet ./...`
Expected: Clean build, no vet issues

- [ ] **Step 5: Commit**

```bash
cd /Users/stello/elnath && git add internal/orchestrator/autopilot.go cmd/elnath/cmd_daemon.go cmd/elnath/runtime.go
git commit -m "feat(telegram): wire streaming summary — synthesizeAssistantSummary now streams via provider.Stream"
```

---

## Verification Checklist

After all phases are complete, verify:

- [ ] `go build ./cmd/elnath/` — clean build
- [ ] `go vet ./...` — no issues
- [ ] `go test ./internal/telegram/ -v -race` — all pass
- [ ] `go test ./internal/daemon/ -v -race` — all pass
- [ ] `go test ./... -race -count=1` — full suite passes
- [ ] Manual test: send "안녕" via Telegram → instant streaming response (no queue)
- [ ] Manual test: send "elnath 소스코드에서 버그 찾아줘" → queued task with progress + summary
- [ ] Manual test: daemon workspace is `~/.elnath/workspace/`, not elnath source dir
- [ ] Manual test: rapid messages don't cause Telegram API throttling

---

## Dependency Graph

```
Phase 1 (StreamConsumer) ──┐
                           ├──→ Phase 2 (Sink Rewrite) ──→ Phase 6 (Integration)
Phase 1 (ProgressReporter)─┘                                    ↑
                                                                 │
Phase 3 (Chat Router) ──────────────────────────────────────────┘
                                                                 
Phase 4 (Sandbox) ──────────────────────────────────────────────→ Phase 6

Phase 5 (Flood Control) ───────────────────────────────────────→ Phase 6
```

Phases 1, 3, 4, 5 are independent and can be executed in parallel.
Phase 2 depends on Phase 1.
Phase 6 depends on all others.
