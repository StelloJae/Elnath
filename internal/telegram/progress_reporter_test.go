package telegram

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type prMockBot struct {
	mu     sync.Mutex
	sends  []prMockSend
	edits  []prMockEdit
	nextID int64
}

type prMockSend struct {
	chatID string
	text   string
}

type prMockEdit struct {
	chatID    string
	messageID int64
	text      string
}

func newPRMockBot() *prMockBot {
	return &prMockBot{nextID: 200}
}

func (m *prMockBot) SendMessage(_ context.Context, chatID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sends = append(m.sends, prMockSend{chatID: chatID, text: text})
	return nil
}

func (m *prMockBot) SendMessageReturningID(_ context.Context, chatID, text string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := m.nextID
	m.sends = append(m.sends, prMockSend{chatID: chatID, text: text})
	return id, nil
}

func (m *prMockBot) EditMessage(_ context.Context, chatID string, messageID int64, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edits = append(m.edits, prMockEdit{chatID: chatID, messageID: messageID, text: text})
	return nil
}

func (m *prMockBot) SetReaction(_ context.Context, _ string, _ int64, _ string) error {
	return nil
}

func (m *prMockBot) GetUpdates(_ context.Context, _ int64, _ int) ([]Update, error) {
	return nil, nil
}

func (m *prMockBot) getSends() []prMockSend {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]prMockSend, len(m.sends))
	copy(cp, m.sends)
	return cp
}

func (m *prMockBot) getEdits() []prMockEdit {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]prMockEdit, len(m.edits))
	copy(cp, m.edits)
	return cp
}

func (m *prMockBot) lastText() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.edits) > 0 {
		return m.edits[len(m.edits)-1].text
	}
	if len(m.sends) > 0 {
		return m.sends[len(m.sends)-1].text
	}
	return ""
}

func TestProgressReporterBatchesTools(t *testing.T) {
	bot := newPRMockBot()
	pr := NewProgressReporter(bot, "chat-1", nil)
	pr.Run()

	pr.ReportTool("bash", "ls -la")
	pr.ReportTool("read_file", "main.go")
	pr.ReportTool("edit_file", "config.go")

	// Wait for the flush (first event triggers immediate flush since lastFlush is zero).
	time.Sleep(500 * time.Millisecond)

	pr.Finish()
	pr.Wait()

	sends := bot.getSends()
	if len(sends) == 0 {
		t.Fatal("expected at least one send")
	}

	text := sends[0].text
	if !strings.Contains(text, "bash") {
		t.Fatalf("expected 'bash' in output, got %q", text)
	}
	if !strings.Contains(text, "read_file") {
		t.Fatalf("expected 'read_file' in output, got %q", text)
	}
	if !strings.Contains(text, "edit_file") {
		t.Fatalf("expected 'edit_file' in output, got %q", text)
	}
	if !strings.Contains(text, "🔧") {
		t.Fatalf("expected bash emoji in output, got %q", text)
	}
}

func TestProgressReporterDedup(t *testing.T) {
	bot := newPRMockBot()
	pr := NewProgressReporter(bot, "chat-1", nil)
	pr.Run()

	pr.ReportTool("bash", "make test")
	pr.ReportTool("bash", "make test")
	pr.ReportTool("bash", "make test")

	time.Sleep(500 * time.Millisecond)

	pr.Finish()
	pr.Wait()

	last := bot.lastText()
	if !strings.Contains(last, "×3") {
		t.Fatalf("expected '×3' dedup counter in output, got %q", last)
	}

	// Should only have one logical line (the deduped one), not three separate lines.
	lines := strings.Split(last, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 deduped line, got %d lines: %q", len(lines), last)
	}
}

func TestProgressReporterStage(t *testing.T) {
	bot := newPRMockBot()
	pr := NewProgressReporter(bot, "chat-1", nil)
	pr.Run()

	pr.ReportStage("code")

	time.Sleep(500 * time.Millisecond)

	pr.Finish()
	pr.Wait()

	last := bot.lastText()
	if !strings.Contains(last, "<b>") {
		t.Fatalf("expected bold formatting in stage output, got %q", last)
	}
	if !strings.Contains(last, "💻") {
		t.Fatalf("expected code stage icon in output, got %q", last)
	}
	if !strings.Contains(last, "Coding") {
		t.Fatalf("expected 'Coding' stage name in output, got %q", last)
	}
}

func TestProgressReporterEditsExisting(t *testing.T) {
	bot := newPRMockBot()
	pr := NewProgressReporter(bot, "chat-1", nil)
	pr.Run()

	// First tool — triggers initial send.
	pr.ReportTool("bash", "echo hello")
	time.Sleep(500 * time.Millisecond)

	sends := bot.getSends()
	if len(sends) == 0 {
		t.Fatal("expected initial send")
	}

	// Second tool — should edit, not send a new message.
	// Need to wait past the throttle interval so the edit actually fires.
	time.Sleep(progressEditInterval)
	pr.ReportTool("read_file", "readme.md")
	time.Sleep(500 * time.Millisecond)

	pr.Finish()
	pr.Wait()

	edits := bot.getEdits()
	if len(edits) == 0 {
		t.Fatal("expected at least one edit after initial send")
	}

	// Should still be exactly 1 send (no second send).
	sends = bot.getSends()
	if len(sends) != 1 {
		t.Fatalf("expected exactly 1 send, got %d", len(sends))
	}

	// The edit text should contain both tools.
	lastEdit := edits[len(edits)-1].text
	if !strings.Contains(lastEdit, "bash") {
		t.Fatalf("edit should contain first tool 'bash', got %q", lastEdit)
	}
	if !strings.Contains(lastEdit, "read_file") {
		t.Fatalf("edit should contain second tool 'read_file', got %q", lastEdit)
	}
}

func TestProgressReporterFinishFlushes(t *testing.T) {
	bot := newPRMockBot()
	pr := NewProgressReporter(bot, "chat-1", nil)
	pr.Run()

	pr.ReportTool("bash", "go build")
	pr.ReportTool("write_file", "output.txt")

	// Finish immediately — should still flush pending tools.
	pr.Finish()
	pr.Wait()

	last := bot.lastText()
	if last == "" {
		t.Fatal("expected flush on Finish(), got no output")
	}
	if !strings.Contains(last, "bash") {
		t.Fatalf("expected 'bash' in flushed output, got %q", last)
	}
	if !strings.Contains(last, "write_file") {
		t.Fatalf("expected 'write_file' in flushed output, got %q", last)
	}
}
