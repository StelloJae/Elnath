package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
)

type mockOutcomeAppender struct {
	mu      sync.Mutex
	records []learning.OutcomeRecord
	err     error
}

func (m *mockOutcomeAppender) Append(r learning.OutcomeRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.records = append(m.records, r)
	return nil
}

func (m *mockOutcomeAppender) snapshot() []learning.OutcomeRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]learning.OutcomeRecord, len(m.records))
	copy(out, m.records)
	return out
}

type chatMockBot struct {
	mu     sync.Mutex
	sends  []chatMockSend
	edits  []chatMockEdit
	nextID int64
}

type chatMockSend struct {
	chatID string
	text   string
}

type chatMockEdit struct {
	chatID    string
	messageID int64
	text      string
}

func newChatMockBot() *chatMockBot {
	return &chatMockBot{nextID: 200}
}

func (m *chatMockBot) SendMessage(_ context.Context, chatID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sends = append(m.sends, chatMockSend{chatID: chatID, text: text})
	return nil
}

func (m *chatMockBot) SendMessageReturningID(_ context.Context, chatID, text string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	m.sends = append(m.sends, chatMockSend{chatID: chatID, text: text})
	return m.nextID, nil
}

func (m *chatMockBot) EditMessage(_ context.Context, chatID string, messageID int64, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edits = append(m.edits, chatMockEdit{chatID: chatID, messageID: messageID, text: text})
	return nil
}

func (m *chatMockBot) SetReaction(_ context.Context, _ string, _ int64, _ string) error {
	return nil
}

func (m *chatMockBot) GetUpdates(_ context.Context, _ int64, _ int) ([]Update, error) {
	return nil, nil
}

func (m *chatMockBot) lastText() string {
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

func (m *chatMockBot) allSendTexts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.sends))
	for i, s := range m.sends {
		out[i] = s.text
	}
	return out
}

type chatMockProvider struct {
	response  string
	streamErr error
}

func (p *chatMockProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	if p.streamErr != nil {
		return p.streamErr
	}
	for _, r := range p.response {
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: string(r)})
	}
	cb(llm.StreamEvent{Type: llm.EventDone})
	return nil
}

func (p *chatMockProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: p.response}, nil
}

func (p *chatMockProvider) Name() string            { return "mock" }
func (p *chatMockProvider) Models() []llm.ModelInfo { return nil }

func TestChatResponderStreamsResponse(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "Hello!"}
	cr := NewChatResponder(provider, bot, "chat-42", nil)

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "Hi there", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	last := bot.lastText()
	if last == "" {
		t.Fatal("expected at least one message sent to Telegram")
	}
	if !strings.Contains(last, "Hello!") {
		t.Fatalf("expected final text to contain 'Hello!', got %q", last)
	}
	if strings.Contains(last, streamCursor) {
		t.Fatalf("final text should not contain cursor, got %q", last)
	}
}

func TestChatResponderHandlesStreamError(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{streamErr: fmt.Errorf("provider unavailable")}
	cr := NewChatResponder(provider, bot, "chat-42", nil)

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "Hi", 1)
	if err == nil {
		t.Fatal("expected error from Respond")
	}
	if !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("expected error to contain 'provider unavailable', got %q", err.Error())
	}

	texts := bot.allSendTexts()
	found := false
	for _, text := range texts {
		if strings.Contains(text, "Error") && strings.Contains(text, "provider unavailable") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected error message sent to user, got texts: %v", texts)
	}
}

func TestChatResponderEmptyResponse(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: ""}
	cr := NewChatResponder(provider, bot, "chat-42", nil)

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "Hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestChatResponderRecordsSuccessOutcome(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "Hi back"}
	store := &mockOutcomeAppender{}
	cr := NewChatResponder(provider, bot, "chat-42", nil, WithOutcomeStore(store))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj-ok", Surface: "telegram"}, "hello", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	records := store.snapshot()
	if len(records) != 1 {
		t.Fatalf("expected 1 outcome record, got %d", len(records))
	}
	r := records[0]
	if r.ProjectID != "proj-ok" {
		t.Errorf("ProjectID: got %q, want %q", r.ProjectID, "proj-ok")
	}
	if r.Intent != "chat" {
		t.Errorf("Intent: got %q, want %q", r.Intent, "chat")
	}
	if r.Workflow != "chat_direct" {
		t.Errorf("Workflow: got %q, want %q", r.Workflow, "chat_direct")
	}
	if !r.Success {
		t.Errorf("Success: got false, want true")
	}
	if r.FinishReason != "stop" {
		t.Errorf("FinishReason: got %q, want %q", r.FinishReason, "stop")
	}
	if r.PreferenceUsed {
		t.Errorf("PreferenceUsed: got true, want false (chat bypasses routing)")
	}
	if r.InputSnippet == "" {
		t.Errorf("InputSnippet: expected non-empty")
	}
}

func TestChatResponderRecordsErrorOutcome(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{streamErr: fmt.Errorf("provider unavailable")}
	store := &mockOutcomeAppender{}
	cr := NewChatResponder(provider, bot, "chat-42", nil, WithOutcomeStore(store))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj-err", Surface: "telegram"}, "hi", 1)
	if err == nil {
		t.Fatalf("expected error")
	}

	records := store.snapshot()
	if len(records) != 1 {
		t.Fatalf("expected 1 outcome record, got %d", len(records))
	}
	r := records[0]
	if r.ProjectID != "proj-err" {
		t.Errorf("ProjectID: got %q, want %q", r.ProjectID, "proj-err")
	}
	if r.Intent != "chat" {
		t.Errorf("Intent: got %q, want %q", r.Intent, "chat")
	}
	if r.Workflow != "chat_direct" {
		t.Errorf("Workflow: got %q, want %q", r.Workflow, "chat_direct")
	}
	if r.Success {
		t.Errorf("Success: got true, want false")
	}
	if r.FinishReason != "error" {
		t.Errorf("FinishReason: got %q, want %q", r.FinishReason, "error")
	}
}

func TestChatResponderSkipsOutcomeWhenProjectIDEmpty(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "hello"}
	store := &mockOutcomeAppender{}
	cr := NewChatResponder(provider, bot, "chat-42", nil, WithOutcomeStore(store))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := len(store.snapshot()); n != 0 {
		t.Errorf("expected 0 outcome records when ProjectID empty, got %d", n)
	}
}
