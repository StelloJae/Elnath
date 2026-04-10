package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type streamMockBot struct {
	mu    sync.Mutex
	sends []streamMockSend
	edits []streamMockEdit
	nextID int64
}

type streamMockSend struct {
	chatID string
	text   string
}

type streamMockEdit struct {
	chatID    string
	messageID int64
	text      string
}

func newStreamMockBot() *streamMockBot {
	return &streamMockBot{nextID: 100}
}

func (m *streamMockBot) SendMessage(_ context.Context, chatID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sends = append(m.sends, streamMockSend{chatID: chatID, text: text})
	return nil
}

func (m *streamMockBot) SendMessageReturningID(_ context.Context, chatID, text string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := m.nextID
	m.sends = append(m.sends, streamMockSend{chatID: chatID, text: text})
	return id, nil
}

func (m *streamMockBot) EditMessage(_ context.Context, chatID string, messageID int64, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edits = append(m.edits, streamMockEdit{chatID: chatID, messageID: messageID, text: text})
	return nil
}

func (m *streamMockBot) SetReaction(_ context.Context, _ string, _ int64, _ string) error {
	return nil
}

func (m *streamMockBot) GetUpdates(_ context.Context, _ int64, _ int) ([]Update, error) {
	return nil, nil
}

func (m *streamMockBot) getSends() []streamMockSend {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]streamMockSend, len(m.sends))
	copy(cp, m.sends)
	return cp
}

func (m *streamMockBot) getEdits() []streamMockEdit {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]streamMockEdit, len(m.edits))
	copy(cp, m.edits)
	return cp
}

func (m *streamMockBot) allTexts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, s := range m.sends {
		out = append(out, s.text)
	}
	for _, e := range m.edits {
		out = append(out, e.text)
	}
	return out
}

func TestStreamConsumerBasicSend(t *testing.T) {
	bot := newStreamMockBot()
	sc := NewStreamConsumer(bot, "chat-1", nil)

	if sc.AlreadySent() {
		t.Fatal("AlreadySent should be false before Run")
	}

	sc.Run()

	sc.Send("Hello ")
	sc.Send("world")
	time.Sleep(500 * time.Millisecond)
	sc.Finish()
	sc.Wait()

	if !sc.AlreadySent() {
		t.Fatal("AlreadySent should be true after sending")
	}

	sends := bot.getSends()
	if len(sends) == 0 {
		t.Fatal("expected at least one send")
	}

	// The final text (last send or edit) should be "Hello world" without cursor.
	allTexts := bot.allTexts()
	last := allTexts[len(allTexts)-1]
	if strings.Contains(last, streamCursor) {
		t.Fatalf("final text should not contain cursor, got %q", last)
	}
	if !strings.Contains(last, "Hello world") {
		t.Fatalf("final text should contain 'Hello world', got %q", last)
	}
}

func TestStreamConsumerCursor(t *testing.T) {
	bot := newStreamMockBot()
	sc := NewStreamConsumer(bot, "chat-1", nil)

	sc.Run()

	sc.Send("Streaming text")
	time.Sleep(500 * time.Millisecond)

	// Before finish, intermediate sends/edits should have cursor.
	sends := bot.getSends()
	if len(sends) == 0 {
		t.Fatal("expected at least one send during streaming")
	}

	hasCursor := false
	for _, s := range sends {
		if strings.HasSuffix(s.text, streamCursor) {
			hasCursor = true
			break
		}
	}
	if !hasCursor {
		t.Fatal("intermediate sends should have cursor appended")
	}

	sc.Finish()
	sc.Wait()

	// After finish, last text should NOT have cursor.
	allTexts := bot.allTexts()
	last := allTexts[len(allTexts)-1]
	if strings.Contains(last, streamCursor) {
		t.Fatalf("final text should not have cursor, got %q", last)
	}
}

func TestStreamConsumerDedup(t *testing.T) {
	bot := newStreamMockBot()
	sc := NewStreamConsumer(bot, "chat-1", nil)

	sc.Run()

	sc.Send("same")
	time.Sleep(500 * time.Millisecond)

	// Force another flush cycle with no new data — should dedup.
	time.Sleep(500 * time.Millisecond)

	sc.Finish()
	sc.Wait()

	sends := bot.getSends()
	edits := bot.getEdits()

	// Only 1 send expected (first message). The final flush (without cursor)
	// will produce one edit. No extra edits from dedup.
	totalOps := len(sends) + len(edits)
	if totalOps > 3 {
		t.Fatalf("expected at most 3 operations (send + edit for cursor removal + possible intermediate), got sends=%d edits=%d", len(sends), len(edits))
	}
}

func TestStreamConsumerNewSegment(t *testing.T) {
	bot := newStreamMockBot()
	sc := NewStreamConsumer(bot, "chat-1", nil)

	sc.Run()

	sc.Send("first segment")
	time.Sleep(500 * time.Millisecond)

	sc.NewSegment()
	time.Sleep(100 * time.Millisecond)

	sc.Send("second segment")
	time.Sleep(500 * time.Millisecond)

	sc.Finish()
	sc.Wait()

	sends := bot.getSends()
	if len(sends) < 2 {
		t.Fatalf("expected at least 2 sends (one per segment), got %d", len(sends))
	}

	// Verify they used different message IDs by checking that there are
	// at least 2 distinct send operations (each creates a new message).
	firstSendText := sends[0].text
	secondSendText := sends[1].text

	// First send should contain "first segment"
	if !strings.Contains(firstSendText, "first segment") {
		t.Fatalf("first send should contain 'first segment', got %q", firstSendText)
	}

	// Second send should contain "second segment"
	if !strings.Contains(secondSendText, "second segment") {
		t.Fatalf("second send should contain 'second segment', got %q", secondSendText)
	}
}

func TestStreamConsumerBufferThreshold(t *testing.T) {
	bot := newStreamMockBot()
	sc := NewStreamConsumer(bot, "chat-1", nil)

	sc.Run()

	// Send a large chunk that exceeds the 40-char threshold.
	// This should trigger a flush within one tick cycle (50ms),
	// not waiting for the full 300ms interval.
	longText := strings.Repeat("x", 50)
	sc.Send(longText)

	// Wait enough for the 50ms tick but much less than 300ms.
	time.Sleep(150 * time.Millisecond)

	sends := bot.getSends()
	if len(sends) == 0 {
		t.Fatal("expected flush triggered by buffer threshold before 300ms")
	}

	if !strings.Contains(sends[0].text, longText) {
		t.Fatalf("flushed text should contain the long input, got %q", sends[0].text)
	}

	sc.Finish()
	sc.Wait()
}

func TestStreamConsumerMaxLen(t *testing.T) {
	bot := newStreamMockBot()
	sc := NewStreamConsumer(bot, "chat-1", nil)

	sc.Run()

	// Send text exceeding streamMaxLen.
	huge := strings.Repeat("A", streamMaxLen+500)
	sc.Send(huge)
	time.Sleep(500 * time.Millisecond)

	sc.Finish()
	sc.Wait()

	allTexts := bot.allTexts()
	for _, text := range allTexts {
		plain := strings.TrimSuffix(text, streamCursor)
		if len(plain) > streamMaxLen {
			t.Fatalf("text length %d exceeds max %d", len(plain), streamMaxLen)
		}
	}
}

func TestStreamConsumerEditFailureLogsAndContinues(t *testing.T) {
	bot := &streamFailEditBot{streamMockBot: newStreamMockBot()}
	sc := NewStreamConsumer(bot, "chat-1", nil)

	sc.Run()

	sc.Send("hello")
	time.Sleep(500 * time.Millisecond)

	// Send more data so an edit is attempted (and fails).
	sc.Send(" more")
	time.Sleep(500 * time.Millisecond)

	// Should not panic or hang.
	sc.Finish()
	sc.Wait()

	if !sc.AlreadySent() {
		t.Fatal("AlreadySent should be true from initial send")
	}
}

type streamFailEditBot struct {
	*streamMockBot
}

func (b *streamFailEditBot) EditMessage(_ context.Context, chatID string, messageID int64, text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.edits = append(b.edits, streamMockEdit{chatID: chatID, messageID: messageID, text: text})
	return fmt.Errorf("network error")
}

func TestStreamConsumerAlreadySentFalseWhenNoData(t *testing.T) {
	bot := newStreamMockBot()
	sc := NewStreamConsumer(bot, "chat-1", nil)

	sc.Run()

	// Finish immediately with no data sent.
	sc.Finish()
	sc.Wait()

	if sc.AlreadySent() {
		t.Fatal("AlreadySent should be false when no data was sent")
	}
}
