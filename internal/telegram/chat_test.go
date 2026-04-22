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
	"github.com/stello/elnath/internal/prompt"
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
	mu        sync.Mutex
	sends     []chatMockSend
	edits     []chatMockEdit
	reactions []chatMockReaction
	nextID    int64
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

type chatMockReaction struct {
	chatID    string
	messageID int64
	emoji     string
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

func (m *chatMockBot) SetReaction(_ context.Context, chatID string, messageID int64, emoji string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reactions = append(m.reactions, chatMockReaction{chatID: chatID, messageID: messageID, emoji: emoji})
	return nil
}

func (m *chatMockBot) allReactions() []chatMockReaction {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]chatMockReaction, len(m.reactions))
	copy(out, m.reactions)
	return out
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

type chatProviderToolUse struct {
	id    string
	name  string
	input string // JSON
}

type chatProviderStep struct {
	text     string
	toolUses []chatProviderToolUse
	err      error
}

type chatMockProvider struct {
	response  string
	streamErr error

	steps []chatProviderStep

	mu           sync.Mutex
	callCount    int
	lastReq      *llm.ChatRequest
	capturedReqs []llm.ChatRequest
}

func (p *chatMockProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.mu.Lock()
	copied := req
	p.lastReq = &copied
	p.capturedReqs = append(p.capturedReqs, copied)
	if p.streamErr != nil {
		p.mu.Unlock()
		return p.streamErr
	}
	if len(p.steps) > 0 {
		idx := p.callCount
		p.callCount++
		p.mu.Unlock()
		if idx >= len(p.steps) {
			return fmt.Errorf("chatMockProvider: no scripted step for call %d (have %d)", idx, len(p.steps))
		}
		step := p.steps[idx]
		if step.err != nil {
			return step.err
		}
		for _, r := range step.text {
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: string(r)})
		}
		for _, tu := range step.toolUses {
			cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: tu.id, Name: tu.name}})
			cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: tu.id, Name: tu.name, Input: tu.input}})
		}
		cb(llm.StreamEvent{Type: llm.EventDone})
		return nil
	}
	p.mu.Unlock()
	for _, r := range p.response {
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: string(r)})
	}
	cb(llm.StreamEvent{Type: llm.EventDone})
	return nil
}

func (p *chatMockProvider) capturedRequest(t *testing.T) llm.ChatRequest {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastReq == nil {
		t.Fatal("expected provider to capture a ChatRequest")
	}
	return *p.lastReq
}

func (p *chatMockProvider) capturedRequests() []llm.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]llm.ChatRequest, len(p.capturedReqs))
	copy(out, p.capturedReqs)
	return out
}

func (p *chatMockProvider) callCountSnapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
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
	// Post-FU-ChatFriendlyError (P4): partner-facing message is a mapped Korean
	// string marked with ⚠️; raw provider text ("provider unavailable") no
	// longer leaks. We only verify that an error message was actually delivered.
	found := false
	for _, text := range texts {
		if strings.Contains(text, "⚠️") {
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

type stubChatBuilder struct {
	result string
	err    error

	mu       sync.Mutex
	received *prompt.RenderState
}

func (b *stubChatBuilder) Build(_ context.Context, state *prompt.RenderState) (string, error) {
	b.mu.Lock()
	b.received = state
	b.mu.Unlock()
	if b.err != nil {
		return "", b.err
	}
	return b.result, nil
}

func (b *stubChatBuilder) capturedState(t *testing.T) *prompt.RenderState {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.received == nil {
		t.Fatal("expected Builder to receive a RenderState")
	}
	return b.received
}

type stubHistoryLoader struct {
	messages []llm.Message
	err      error
}

func (h *stubHistoryLoader) GetHistory(_ context.Context, _ string) ([]llm.Message, error) {
	if h.err != nil {
		return nil, h.err
	}
	return h.messages, nil
}

type stubSessionLookup struct {
	session string
	ok      bool
}

func (l *stubSessionLookup) Lookup(_, _ string) (string, bool) {
	return l.session, l.ok
}

func TestChatResponder_UsesPromptPipelineWhenWired(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	builder := &stubChatBuilder{result: "CUSTOM-SYSTEM"}
	lookup := &stubSessionLookup{session: "sess-1", ok: true}
	history := &stubHistoryLoader{messages: []llm.Message{
		llm.NewUserMessage("prior user"),
		llm.NewAssistantMessage("prior reply"),
	}}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		Builder:    builder,
		Lookup:     lookup,
		History:    history,
		MaxHistory: 10,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "current msg", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if !strings.Contains(req.System, "CUSTOM-SYSTEM") {
		t.Errorf("System prompt should contain builder result: got %q", req.System)
	}
	if len(req.Messages) != 3 {
		t.Errorf("Messages len: got %d, want 3 (2 history + 1 current)", len(req.Messages))
	}

	state := builder.capturedState(t)
	if state.Principal.UserID != "42" {
		t.Errorf("Principal.UserID: got %q, want %q", state.Principal.UserID, "42")
	}
	if state.Principal.Surface != "telegram" {
		t.Errorf("Principal.Surface: got %q, want %q", state.Principal.Surface, "telegram")
	}
	if state.SessionID != "sess-1" {
		t.Errorf("SessionID: got %q, want %q", state.SessionID, "sess-1")
	}
	if state.ProjectID != "proj" {
		t.Errorf("ProjectID: got %q, want %q", state.ProjectID, "proj")
	}
	if len(state.Messages) != 2 {
		t.Errorf("RenderState.Messages len: got %d, want 2", len(state.Messages))
	}
}

func TestChatResponder_FallsBackToLegacySystemWhenBuildFails(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	builder := &stubChatBuilder{err: fmt.Errorf("build failed intentionally")}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		Builder: builder,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if !strings.Contains(req.System, "Elnath") {
		t.Errorf("expected legacy fallback system prompt, got %q", req.System)
	}
}

func TestChatResponder_SkipsHistoryWhenSessionNotBound(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	builder := &stubChatBuilder{result: "SYS"}
	lookup := &stubSessionLookup{ok: false}
	history := &stubHistoryLoader{messages: []llm.Message{llm.NewUserMessage("should not appear")}}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		Builder: builder,
		Lookup:  lookup,
		History: history,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if len(req.Messages) != 1 {
		t.Errorf("Messages len: got %d, want 1 (current only, session unbound)", len(req.Messages))
	}
}

func TestChatResponder_TrimsHistoryAtMax(t *testing.T) {
	var msgs []llm.Message
	for i := 0; i < 50; i++ {
		msgs = append(msgs, llm.NewUserMessage(fmt.Sprintf("m%d", i)))
	}

	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	builder := &stubChatBuilder{result: "SYS"}
	lookup := &stubSessionLookup{session: "s", ok: true}
	history := &stubHistoryLoader{messages: msgs}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		Builder:    builder,
		Lookup:     lookup,
		History:    history,
		MaxHistory: 5,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "current", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if len(req.Messages) != 6 {
		t.Errorf("Messages len: got %d, want 6 (5 trimmed + 1 current)", len(req.Messages))
	}
}

// --- Chat-path session persistence (FU-ChatSessionPersist) ---

type chatAppend struct {
	sessionID string
	messages  []llm.Message
}

// firstMessageByRole returns the first message in the append whose role
// matches. Tests used the pre-L1.2 two-argument persister shape and
// looked at `user` / `assistant` directly; the slice variant keeps the
// same assertions readable by pulling the paired messages back out.
func (a chatAppend) firstMessageByRole(role string) (llm.Message, bool) {
	for _, m := range a.messages {
		if m.Role == role {
			return m, true
		}
	}
	return llm.Message{}, false
}

type stubChatPersister struct {
	ensuredSession string
	ensureErr      error
	appendErr      error

	mu               sync.Mutex
	appends          []chatAppend
	ensureCalls      int
	ensureCalledWith identity.Principal
}

func (p *stubChatPersister) EnsureChatSession(_ context.Context, principal identity.Principal) (string, error) {
	p.mu.Lock()
	p.ensureCalls++
	p.ensureCalledWith = principal
	p.mu.Unlock()
	if p.ensureErr != nil {
		return "", p.ensureErr
	}
	return p.ensuredSession, nil
}

func (p *stubChatPersister) AppendChatTurn(_ context.Context, sid string, messages []llm.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.appendErr != nil {
		return p.appendErr
	}
	copied := make([]llm.Message, len(messages))
	copy(copied, messages)
	p.appends = append(p.appends, chatAppend{sessionID: sid, messages: copied})
	return nil
}

func (p *stubChatPersister) snapshot() []chatAppend {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]chatAppend, len(p.appends))
	copy(out, p.appends)
	return out
}

type chatBind struct {
	chatID    string
	userID    string
	sessionID string
}

type stubChatBinder struct {
	err error

	mu         sync.Mutex
	remembered []chatBind
}

func (b *stubChatBinder) Remember(chatID, userID, sessionID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	b.remembered = append(b.remembered, chatBind{chatID: chatID, userID: userID, sessionID: sessionID})
	return nil
}

func (b *stubChatBinder) snapshot() []chatBind {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]chatBind, len(b.remembered))
	copy(out, b.remembered)
	return out
}

func TestChatResponder_PersistsTurnToBoundSession(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "hello back"}
	builder := &stubChatBuilder{result: "SYS"}
	lookup := &stubSessionLookup{session: "sess-bound", ok: true}
	persister := &stubChatPersister{}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		Builder:   builder,
		Lookup:    lookup,
		Persister: persister,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi there", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	appends := persister.snapshot()
	if len(appends) != 1 {
		t.Fatalf("expected 1 AppendChatTurn call, got %d", len(appends))
	}
	a := appends[0]
	if a.sessionID != "sess-bound" {
		t.Errorf("sessionID = %q, want sess-bound", a.sessionID)
	}
	userMsg, ok := a.firstMessageByRole(llm.RoleUser)
	if !ok || userMsg.Text() != "hi there" {
		t.Errorf("user msg = {%q, %q}, want {user, hi there}", userMsg.Role, userMsg.Text())
	}
	asstMsg, ok := a.firstMessageByRole(llm.RoleAssistant)
	if !ok || asstMsg.Text() != "hello back" {
		t.Errorf("assistant msg = {%q, %q}, want {assistant, hello back}", asstMsg.Role, asstMsg.Text())
	}
}

func TestChatResponder_CreatesAndBindsNewSessionWhenUnbound(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "response"}
	builder := &stubChatBuilder{result: "SYS"}
	lookup := &stubSessionLookup{ok: false}
	persister := &stubChatPersister{ensuredSession: "sess-new"}
	binder := &stubChatBinder{}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		Builder:      builder,
		Lookup:       lookup,
		Persister:    persister,
		BindRecorder: binder,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj-x", Surface: "telegram"}, "first hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	persister.mu.Lock()
	ensureCalls := persister.ensureCalls
	ensurePrincipal := persister.ensureCalledWith
	persister.mu.Unlock()
	if ensureCalls != 1 {
		t.Fatalf("EnsureChatSession called %d times, want 1", ensureCalls)
	}
	if ensurePrincipal.UserID != "42" || ensurePrincipal.ProjectID != "proj-x" || ensurePrincipal.Surface != "telegram" {
		t.Errorf("EnsureChatSession principal = %+v, want {42, proj-x, telegram}", ensurePrincipal)
	}

	appends := persister.snapshot()
	if len(appends) != 1 {
		t.Fatalf("expected 1 append, got %d", len(appends))
	}
	if appends[0].sessionID != "sess-new" {
		t.Errorf("append sessionID = %q, want sess-new", appends[0].sessionID)
	}

	remembered := binder.snapshot()
	if len(remembered) != 1 {
		t.Fatalf("expected 1 Remember call, got %d", len(remembered))
	}
	r := remembered[0]
	if r.chatID != "chat-42" || r.userID != "42" || r.sessionID != "sess-new" {
		t.Errorf("Remember call = %+v, want {chat-42, 42, sess-new}", r)
	}
}

func TestChatResponder_SkipsPersistWhenStreamFails(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{streamErr: fmt.Errorf("provider down")}
	persister := &stubChatPersister{}
	binder := &stubChatBinder{}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		Builder:      &stubChatBuilder{result: "SYS"},
		Lookup:       &stubSessionLookup{session: "sess-1", ok: true},
		Persister:    persister,
		BindRecorder: binder,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err == nil {
		t.Fatal("expected error from Respond when stream fails")
	}

	if appends := persister.snapshot(); len(appends) != 0 {
		t.Errorf("expected 0 AppendChatTurn calls on stream error, got %d", len(appends))
	}
	if remembered := binder.snapshot(); len(remembered) != 0 {
		t.Errorf("expected 0 Remember calls on stream error, got %d", len(remembered))
	}
}

// TestChatResponder_PersistsMessagesWithSourceChat (L1.2 R1) pins the
// provenance marker the chat-path must stamp on every persisted message.
// Source="chat" is the contract the L1.3 load-side sanitiser will read
// to decide which tool blocks to keep (chat-owned) vs strip (task-origin
// bleed from the shared session JSONL). This test walks the legacy /
// no-tool-loop path; the tool-loop variant is covered further below.
func TestChatResponder_PersistsMessagesWithSourceChat(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "hello back"}
	lookup := &stubSessionLookup{session: "sess-bound", ok: true}
	persister := &stubChatPersister{}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		Builder:   &stubChatBuilder{result: "SYS"},
		Lookup:    lookup,
		Persister: persister,
	}))

	if err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi there", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	appends := persister.snapshot()
	if len(appends) != 1 {
		t.Fatalf("expected 1 AppendChatTurn call, got %d", len(appends))
	}
	if got := len(appends[0].messages); got != 2 {
		t.Fatalf("persisted messages = %d, want 2 (user + assistant)", got)
	}
	for i, msg := range appends[0].messages {
		if msg.Source != llm.SourceChat {
			t.Errorf("messages[%d] Source = %q, want %q", i, msg.Source, llm.SourceChat)
		}
	}
}

func TestChatResponder_LegacyPathPreservedWhenNoPipeline(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}

	cr := NewChatResponder(provider, bot, "chat-42", nil)

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if !strings.Contains(req.System, "Elnath") {
		t.Errorf("expected legacy system prompt, got %q", req.System)
	}
	if len(req.Messages) != 1 {
		t.Errorf("Messages len: got %d, want 1 (legacy single-message path)", len(req.Messages))
	}
}

// --- FU-CR2a: ChatPipelineDeps.ToolDefs plumbing ---

func TestChatResponder_ForwardsToolDefsToProvider(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	wantTools := []llm.ToolDef{
		{Name: "read_file", Description: "read"},
		{Name: "web_fetch", Description: "fetch"},
	}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolDefs: wantTools,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if len(req.Tools) != len(wantTools) {
		t.Fatalf("Tools len: got %d, want %d", len(req.Tools), len(wantTools))
	}
	for i, want := range wantTools {
		if req.Tools[i].Name != want.Name {
			t.Errorf("Tools[%d].Name: got %q, want %q", i, req.Tools[i].Name, want.Name)
		}
	}
}

func TestChatResponder_NoToolsWhenPipelineAbsent(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}

	cr := NewChatResponder(provider, bot, "chat-42", nil)

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if len(req.Tools) != 0 {
		t.Errorf("Tools: got %d entries, want 0 (legacy path, no pipeline)", len(req.Tools))
	}
}

func TestChatResponder_NoToolsWhenToolDefsEmpty(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		Builder: &stubChatBuilder{result: "SYS"},
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if len(req.Tools) != 0 {
		t.Errorf("Tools: got %d entries, want 0 (pipeline present, ToolDefs empty)", len(req.Tools))
	}
}

// --- FU-TgReactions: chat completion reactions on user message ---

func TestChatResponder_SetsSuccessReactionOnCompletion(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "Hello!"}
	cr := NewChatResponder(provider, bot, "chat-42", nil)

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 77)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// FU-ChatEntryWorking (P1): ✍ is now set at chat-path entry, so every
	// turn ends with [✍, 👍] — entry reaction + terminal success reaction.
	rs := bot.allReactions()
	if len(rs) != 2 {
		t.Fatalf("reactions = %d, want 2 (entry ✍ + success 👍)", len(rs))
	}
	if rs[0].emoji != "✍" {
		t.Errorf("reaction[0].emoji = %q, want %q (entry-side ✍)", rs[0].emoji, "✍")
	}
	if rs[1].emoji != "👍" {
		t.Errorf("reaction[1].emoji = %q, want %q (terminal success)", rs[1].emoji, "👍")
	}
	for i, r := range rs {
		if r.messageID != 77 {
			t.Errorf("reaction[%d].messageID = %d, want 77 (replyToMsgID)", i, r.messageID)
		}
		if r.chatID != "chat-42" {
			t.Errorf("reaction[%d].chatID = %q, want %q", i, r.chatID, "chat-42")
		}
	}
}

func TestChatResponder_SetsFailureReactionOnStreamError(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{streamErr: fmt.Errorf("provider unavailable")}
	cr := NewChatResponder(provider, bot, "chat-42", nil)

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 99)
	if err == nil {
		t.Fatal("expected error from Respond")
	}

	// FU-ChatEntryWorking (P1): entry ✍ fires before the stream fails, so
	// the sequence on failure is [✍, 😢] — entry reaction then 😢 overwrite.
	rs := bot.allReactions()
	if len(rs) != 2 {
		t.Fatalf("reactions = %d, want 2 (entry ✍ + failure 😢)", len(rs))
	}
	if rs[0].emoji != "✍" || rs[1].emoji != "😢" {
		t.Errorf("reactions = [%q, %q], want [✍, 😢]", rs[0].emoji, rs[1].emoji)
	}
	for i, r := range rs {
		if r.messageID != 99 {
			t.Errorf("reaction[%d].messageID = %d, want 99", i, r.messageID)
		}
	}
}

func TestChatResponder_SkipsReactionWhenReplyToMsgIDZero(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	cr := NewChatResponder(provider, bot, "chat-42", nil)

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rs := bot.allReactions()
	if len(rs) != 0 {
		t.Errorf("reactions = %d, want 0 (no user message to react to)", len(rs))
	}
}

// --- FU-ChatFallbackVoice (P5): fallback system prompt voice + no "concise" ---

func TestChatSystemPromptFallback_HasPartnerVoiceAndNoConciseDirective(t *testing.T) {
	// Guards the fallback system prompt used when prompt.Builder is not
	// wired. Audit 2026-04-21 cell F1: old fallback forced short answers
	// and dropped identity. The partner prefers detailed, substantiated
	// replies in Korean, and chat tool loop is meaningful only if the
	// fallback also reminds the model to actually call tools.
	wantPresent := []string{
		"Elnath",      // identity anchored even without prompt.Builder
		"한국어",         // Korean default
		"상세",          // substantive / detailed bias
		"도구",          // tool-use cue preserved even without strong-guide header
	}
	for _, want := range wantPresent {
		if !strings.Contains(chatSystemPrompt, want) {
			t.Errorf("chatSystemPrompt missing %q; got:\n%s", want, chatSystemPrompt)
		}
	}

	wantAbsent := []string{
		"concise", // the 2-line fallback directive that caused output_tokens 7.6x gap
		"Concise",
	}
	for _, avoid := range wantAbsent {
		if strings.Contains(chatSystemPrompt, avoid) {
			t.Errorf("chatSystemPrompt still contains %q (post-FU-ChatFallbackVoice should have removed terse directive)", avoid)
		}
	}
}

// --- FU-ChatFriendlyError (P4): raw provider JSON not leaked to partner ---

func TestFriendlyChatError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "알 수 없는 문제"},
		{"context canceled", context.Canceled, "취소"},
		{"deadline exceeded", context.DeadlineExceeded, "오래"},
		{"max iterations", fmt.Errorf("chat tool loop exceeded max iterations (5)"), "간단히"},
		{"rate limit", fmt.Errorf("codex: status 429: rate limited"), "몰렸"},
		{"auth 401", fmt.Errorf("codex: status 401: unauthorized"), "인증"},
		{"auth 403", fmt.Errorf("anthropic: status 403: forbidden"), "인증"},
		{"bad request", fmt.Errorf("codex: status 400: no tool call found"), "다시 시도"},
		{"server error", fmt.Errorf("openai: status 503: overloaded"), "모델 서버"},
		{"generic codex", fmt.Errorf("codex: unexpected stream end"), "모델 쪽"},
		{"unknown", fmt.Errorf("something broke"), "내부에서"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := friendlyChatError(tc.err)
			if got == "" {
				t.Fatalf("friendlyChatError returned empty string")
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("friendlyChatError(%v) = %q; missing %q", tc.err, got, tc.want)
			}
			// Never leak raw JSON or HTTP status noise
			if strings.Contains(got, "{") || strings.Contains(got, "status 4") || strings.Contains(got, "status 5") {
				t.Errorf("friendlyChatError leaked provider detail: %q", got)
			}
		})
	}
}

func TestChatResponder_SendsFriendlyErrorMessageNotRawProviderJSON(t *testing.T) {
	bot := newChatMockBot()
	// Simulate the exact Codex 400 shape dogfood hit 2026-04-21 15:16 —
	// raw JSON payload that must never reach the partner.
	rawCodex := `codex: status 400: {
  "error": {
    "message": "No tool call found for function call output with call_id call_wH9JVxyuUUHiADnStfgOacFM.",
    "type": "invalid_request_error"
  }
}`
	provider := &chatMockProvider{streamErr: fmt.Errorf("%s", rawCodex)}
	cr := NewChatResponder(provider, bot, "chat-42", nil)

	if err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 99); err == nil {
		t.Fatal("expected error from Respond")
	}

	sends := bot.allSendTexts()
	if len(sends) == 0 {
		t.Fatal("no message sent to partner")
	}
	for _, s := range sends {
		if strings.Contains(s, "{") || strings.Contains(s, "call_id") || strings.Contains(s, "invalid_request_error") {
			t.Errorf("raw provider JSON leaked into user-facing message: %q", s)
		}
		if !strings.Contains(s, "⚠️") {
			t.Errorf("user-facing message missing ⚠️ marker: %q", s)
		}
	}
}

// --- FU-ChatOutcomeSessionID (P3): chat outcomes cross-ref session JSONL ---

func TestChatResponder_OutcomeCarriesSessionIDWhenBound(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	store := &mockOutcomeAppender{}

	cr := NewChatResponder(provider, bot, "chat-42", nil,
		WithOutcomeStore(store),
		WithChatPipeline(ChatPipelineDeps{
			Builder: &stubChatBuilder{result: "SYS"},
			Lookup:  &stubSessionLookup{session: "sess-deadbeef", ok: true},
		}),
	)

	if err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	records := store.snapshot()
	if len(records) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(records))
	}
	if records[0].SessionID != "sess-deadbeef" {
		t.Errorf("SessionID = %q, want %q (bound chat session propagated to outcome)", records[0].SessionID, "sess-deadbeef")
	}
}

func TestChatResponder_OutcomeSessionIDEmptyWhenUnbound(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	store := &mockOutcomeAppender{}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithOutcomeStore(store))

	if err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	records := store.snapshot()
	if len(records) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(records))
	}
	if records[0].SessionID != "" {
		t.Errorf("SessionID = %q, want empty (no binder wired → no session lookup)", records[0].SessionID)
	}
}

// --- FU-ChatHistorySanitize: strip agent.Loop tool blocks from chat history ---

func TestSanitizeChatHistory_StripsToolBlocksFromSharedSession(t *testing.T) {
	msgs := []llm.Message{
		llm.NewUserMessage("안녕"),
		llm.NewAssistantMessage("안녕하세요"),
		// agent.Loop artefact 1: assistant message carrying only a tool_use
		{Role: "assistant", Content: []llm.ContentBlock{
			llm.ToolUseBlock{ID: "call_old_1", Name: "web_fetch", Input: []byte(`{"url":"x"}`)},
		}},
		// Paired tool_result on user role
		{Role: "user", Content: []llm.ContentBlock{
			llm.ToolResultBlock{ToolUseID: "call_old_1", Content: "fetched body"},
		}},
		// Mixed assistant turn: text + stray tool_use — text must survive
		{Role: "assistant", Content: []llm.ContentBlock{
			llm.TextBlock{Text: "잠깐만요"},
			llm.ToolUseBlock{ID: "call_old_2", Name: "web_fetch", Input: []byte(`{}`)},
		}},
		llm.NewUserMessage("다른 질문"),
	}

	got := sanitizeChatHistory(msgs)

	if len(got) != 4 {
		t.Fatalf("sanitized len = %d, want 4 (2 orphan turns dropped). got=%+v", len(got), got)
	}

	for i, m := range got {
		for _, b := range m.Content {
			switch b.(type) {
			case llm.ToolUseBlock, llm.ToolResultBlock:
				t.Errorf("message[%d] (role=%s) still carries %T after sanitize", i, m.Role, b)
			}
		}
	}

	// Mixed turn (was index 4; now index 2 after two drops) keeps its text.
	mixed := got[2]
	if mixed.Role != "assistant" || len(mixed.Content) != 1 {
		t.Fatalf("mixed turn not stripped to text-only: %+v", mixed)
	}
	tb, ok := mixed.Content[0].(llm.TextBlock)
	if !ok || tb.Text != "잠깐만요" {
		t.Errorf("mixed turn text = %+v, want TextBlock{잠깐만요}", mixed.Content[0])
	}

	// Final message preserves the fresh user question.
	last := got[3]
	if last.Role != "user" || last.Text() != "다른 질문" {
		t.Errorf("last sanitized message = %+v, want user:다른 질문", last)
	}
}

func TestSanitizeChatHistory_EmptyInputs(t *testing.T) {
	if got := sanitizeChatHistory(nil); got != nil {
		t.Errorf("sanitize(nil) = %+v, want nil", got)
	}
	empty := []llm.Message{}
	if got := sanitizeChatHistory(empty); len(got) != 0 {
		t.Errorf("sanitize(empty) = %+v, want empty", got)
	}
}

// --- Phase L1.3 source-aware sanitize (FU-ChatHistorySourceAware) ---

// TestSanitizeChatHistory_PreservesChatOwnToolBlocks (L1.3 R1) pins the
// core behaviour change introduced by the universal-message-schema:
// chat-origin tool_use / tool_result blocks survive sanitize so the
// partner's own tool-loop history is visible on the next turn, while
// task-origin tool blocks bleeding in via the shared session JSONL are
// still stripped to avoid Codex HTTP 400 "No tool call found for
// function call output with call_id ..." on the reload.
//
// The mixed session below mirrors what load actually sees: a chat turn
// that ran the tool loop (user question → assistant text+tool_use →
// user tool_result → assistant final text) followed by a task turn that
// persisted its own tool_use / tool_result onto the same session.
func TestSanitizeChatHistory_PreservesChatOwnToolBlocks(t *testing.T) {
	chatAssistantToolUse := llm.Message{Role: "assistant", Source: llm.SourceChat, Content: []llm.ContentBlock{
		llm.TextBlock{Text: "잠깐 시세 확인할게요"},
		llm.ToolUseBlock{ID: "chat_call_1", Name: "web_fetch", Input: []byte(`{"url":"x"}`)},
	}}
	chatUserToolResult := llm.Message{Role: "user", Source: llm.SourceChat, Content: []llm.ContentBlock{
		llm.ToolResultBlock{ToolUseID: "chat_call_1", Content: "rows..."},
	}}
	msgs := []llm.Message{
		{Role: "user", Source: llm.SourceChat, Content: []llm.ContentBlock{llm.TextBlock{Text: "주식 알려줘"}}},
		chatAssistantToolUse,
		chatUserToolResult,
		{Role: "assistant", Source: llm.SourceChat, Content: []llm.ContentBlock{llm.TextBlock{Text: "정리된 답"}}},
		// Task-origin turn persisted into the same session JSONL.
		{Role: "user", Source: llm.SourceTask, Content: []llm.ContentBlock{llm.TextBlock{Text: "요약 태스크"}}},
		{Role: "assistant", Source: llm.SourceTask, Content: []llm.ContentBlock{
			llm.ToolUseBlock{ID: "task_call_1", Name: "wiki_search", Input: []byte(`{}`)},
		}},
		{Role: "user", Source: llm.SourceTask, Content: []llm.ContentBlock{
			llm.ToolResultBlock{ToolUseID: "task_call_1", Content: "wiki hit"},
		}},
	}

	got := sanitizeChatHistory(msgs)

	// 4 chat messages preserved verbatim + 1 task user text survives
	// (its tool_use/tool_result companion turns collapse to empty).
	if len(got) != 5 {
		t.Fatalf("sanitized len = %d, want 5; got=%+v", len(got), got)
	}

	// Chat assistant turn retains BOTH TextBlock and ToolUseBlock.
	ca := got[1]
	if ca.Role != "assistant" || ca.Source != llm.SourceChat || len(ca.Content) != 2 {
		t.Fatalf("chat assistant turn not preserved: %+v", ca)
	}
	var haveToolUse bool
	for _, b := range ca.Content {
		if tu, ok := b.(llm.ToolUseBlock); ok && tu.ID == "chat_call_1" {
			haveToolUse = true
		}
	}
	if !haveToolUse {
		t.Errorf("chat own tool_use was stripped, should be preserved; got=%+v", ca.Content)
	}

	// Chat tool_result block must survive on the user-role message.
	cr := got[2]
	if cr.Role != "user" || cr.Source != llm.SourceChat || len(cr.Content) != 1 {
		t.Fatalf("chat tool_result turn not preserved: %+v", cr)
	}
	if _, ok := cr.Content[0].(llm.ToolResultBlock); !ok {
		t.Errorf("chat own tool_result was stripped, want preserved; got=%T", cr.Content[0])
	}

	// Task-origin assistant tool_use + user tool_result must be absent
	// — both turns collapse to empty content and should be dropped.
	for i, m := range got {
		if m.Source != llm.SourceTask {
			continue
		}
		for _, b := range m.Content {
			switch b.(type) {
			case llm.ToolUseBlock, llm.ToolResultBlock:
				t.Errorf("got[%d] task tool block leaked after sanitize: %T", i, b)
			}
		}
	}
}

// TestSanitizeChatHistory_StripsLegacyOriginToolBlocks (L1.3 R2) pins
// the backwards-compat contract: session records written before L1.1
// have Source == "" on disk. resolveSource must treat those as
// task-origin (the only writer that produced tool blocks pre-L1) so the
// existing Codex HTTP 400 protection keeps holding on legacy history.
func TestSanitizeChatHistory_StripsLegacyOriginToolBlocks(t *testing.T) {
	msgs := []llm.Message{
		// Empty Source == pre-L1 JSONL record.
		{Role: "assistant", Source: "", Content: []llm.ContentBlock{
			llm.TextBlock{Text: "구형 턴"},
			llm.ToolUseBlock{ID: "legacy_1", Name: "web_fetch", Input: []byte(`{}`)},
		}},
		{Role: "user", Source: "", Content: []llm.ContentBlock{
			llm.ToolResultBlock{ToolUseID: "legacy_1", Content: "old body"},
		}},
	}

	got := sanitizeChatHistory(msgs)

	if len(got) != 1 {
		t.Fatalf("sanitized len = %d, want 1 (tool_result orphan turn dropped); got=%+v", len(got), got)
	}
	if got[0].Role != "assistant" || len(got[0].Content) != 1 {
		t.Fatalf("legacy assistant turn shape wrong: %+v", got[0])
	}
	if tb, ok := got[0].Content[0].(llm.TextBlock); !ok || tb.Text != "구형 턴" {
		t.Errorf("legacy text lost: %+v", got[0].Content[0])
	}
}

// TestSanitizeChatHistory_StripsTeamOriginToolBlocks (L1.3 R3) pins the
// Q3 B decision from the L1 plan: chat load strips team-origin tool
// blocks as well — a chat turn can't meaningfully narrate over tool
// calls the team orchestrator issued in a different scope, and the
// Codex-side call-id is still unknown to the chat conversation.
func TestSanitizeChatHistory_StripsTeamOriginToolBlocks(t *testing.T) {
	msgs := []llm.Message{
		{Role: "assistant", Source: llm.SourceTeam, Content: []llm.ContentBlock{
			llm.TextBlock{Text: "팀 요약"},
			llm.ToolUseBlock{ID: "team_call_1", Name: "wiki_search", Input: []byte(`{}`)},
		}},
		{Role: "user", Source: llm.SourceTeam, Content: []llm.ContentBlock{
			llm.ToolResultBlock{ToolUseID: "team_call_1", Content: "team result"},
		}},
	}

	got := sanitizeChatHistory(msgs)

	if len(got) != 1 {
		t.Fatalf("sanitized len = %d, want 1 (team tool_result orphan turn dropped); got=%+v", len(got), got)
	}
	for _, b := range got[0].Content {
		switch b.(type) {
		case llm.ToolUseBlock, llm.ToolResultBlock:
			t.Errorf("team tool block leaked after sanitize: %T", b)
		}
	}
}

// TestSanitizeChatHistory_NoOrphanToolResultAfterMixedLoad (L1.3 R4) is
// the Codex HTTP 400 regression guard. After sanitize every surviving
// tool_result must have a matching tool_use earlier in the filtered
// history — otherwise the Responses API rejects the turn with
// "No tool call found for function call output with call_id ...".
// This invariant holds trivially today because every tool block is
// stripped, but L1.3 preserves chat-origin blocks so the guard starts
// doing real work.
func TestSanitizeChatHistory_NoOrphanToolResultAfterMixedLoad(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Source: llm.SourceChat, Content: []llm.ContentBlock{llm.TextBlock{Text: "fetch X"}}},
		{Role: "assistant", Source: llm.SourceChat, Content: []llm.ContentBlock{
			llm.ToolUseBlock{ID: "chat_call_1", Name: "web_fetch", Input: []byte(`{}`)},
		}},
		{Role: "user", Source: llm.SourceChat, Content: []llm.ContentBlock{
			llm.ToolResultBlock{ToolUseID: "chat_call_1", Content: "fetched"},
		}},
		// Task-origin orphan: tool_result whose tool_use we must strip.
		{Role: "assistant", Source: llm.SourceTask, Content: []llm.ContentBlock{
			llm.ToolUseBlock{ID: "task_call_1", Name: "wiki_search", Input: []byte(`{}`)},
		}},
		{Role: "user", Source: llm.SourceTask, Content: []llm.ContentBlock{
			llm.ToolResultBlock{ToolUseID: "task_call_1", Content: "wiki"},
		}},
	}

	got := sanitizeChatHistory(msgs)

	seenToolUse := make(map[string]struct{})
	for i, m := range got {
		for _, b := range m.Content {
			switch blk := b.(type) {
			case llm.ToolUseBlock:
				seenToolUse[blk.ID] = struct{}{}
			case llm.ToolResultBlock:
				if _, ok := seenToolUse[blk.ToolUseID]; !ok {
					t.Errorf("got[%d] orphan tool_result: tool_use_id=%q has no earlier matching tool_use (would trigger Codex HTTP 400)", i, blk.ToolUseID)
				}
			}
		}
	}

	// Chat call_id survives (R1 overlap — tighter local assertion).
	if _, ok := seenToolUse["chat_call_1"]; !ok {
		t.Errorf("chat tool_use id=chat_call_1 should be preserved")
	}
	// Task call_id is stripped.
	if _, ok := seenToolUse["task_call_1"]; ok {
		t.Errorf("task tool_use id=task_call_1 should be stripped")
	}
}

// TestChatResponder_BuildPromptSetsIsChatAndAvailableTools pins L3.1 R4:
// the chat path's buildPrompt must stamp RenderState.IsChat=true and
// populate AvailableTools from the wired ToolDefs when the tool loop is
// active, so the chat-only prompt nodes (ChatSystemPromptNode /
// ChatToolGuideNode) can gate their content without relying on the
// hardcoded chatSystemPrompt / chatToolGuideHeader fallbacks.
// L3.2 removes the fallbacks; L3.1 only wires the state.
func TestChatResponder_BuildPromptSetsIsChatAndAvailableTools(t *testing.T) {
	builder := &stubChatBuilder{result: "SYS"}
	exec := newChatMockExecutor()

	cr := NewChatResponder(nil, nil, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		Builder:      builder,
		ToolDefs:     []llm.ToolDef{{Name: "web_search"}, {Name: "web_fetch"}},
		ToolExecutor: exec,
	}))

	principal := identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}
	_, _, _ = cr.buildPrompt(context.Background(), principal, "hi", cr.logger)

	state := builder.capturedState(t)
	if !state.IsChat {
		t.Errorf("RenderState.IsChat = false, want true for chat path")
	}
	if len(state.AvailableTools) != 2 {
		t.Fatalf("RenderState.AvailableTools len = %d, want 2 (web_search + web_fetch)", len(state.AvailableTools))
	}
	wantSet := map[string]bool{"web_search": true, "web_fetch": true}
	for _, name := range state.AvailableTools {
		if !wantSet[name] {
			t.Errorf("RenderState.AvailableTools contains unexpected %q", name)
		}
	}
}

// TestChatResponder_BuildPromptLeavesAvailableToolsEmptyWhenNoExecutor pins
// the "legacy stream, no tool loop" semantic: when ToolExecutor is nil the
// tool loop never fires, so AvailableTools must stay empty so
// ChatToolGuideNode doesn't render a guide the model cannot act on.
func TestChatResponder_BuildPromptLeavesAvailableToolsEmptyWhenNoExecutor(t *testing.T) {
	builder := &stubChatBuilder{result: "SYS"}

	cr := NewChatResponder(nil, nil, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		Builder:  builder,
		ToolDefs: []llm.ToolDef{{Name: "web_search"}},
		// ToolExecutor intentionally nil — legacy stream path.
	}))

	principal := identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}
	_, _, _ = cr.buildPrompt(context.Background(), principal, "hi", cr.logger)

	state := builder.capturedState(t)
	if !state.IsChat {
		t.Errorf("RenderState.IsChat = false, want true for chat path even without executor")
	}
	if len(state.AvailableTools) != 0 {
		t.Errorf("RenderState.AvailableTools = %v, want empty when tool loop inactive", state.AvailableTools)
	}
}
