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
	if !strings.Contains(req.System, "personal AI assistant") {
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
	user      llm.Message
	assistant llm.Message
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

func (p *stubChatPersister) AppendChatTurn(_ context.Context, sid string, user, assistant llm.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.appendErr != nil {
		return p.appendErr
	}
	p.appends = append(p.appends, chatAppend{sessionID: sid, user: user, assistant: assistant})
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
	if a.user.Role != llm.RoleUser || a.user.Text() != "hi there" {
		t.Errorf("user msg = {%q, %q}, want {user, hi there}", a.user.Role, a.user.Text())
	}
	if a.assistant.Role != llm.RoleAssistant || a.assistant.Text() != "hello back" {
		t.Errorf("assistant msg = {%q, %q}, want {assistant, hello back}", a.assistant.Role, a.assistant.Text())
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

func TestChatResponder_LegacyPathPreservedWhenNoPipeline(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}

	cr := NewChatResponder(provider, bot, "chat-42", nil)

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if !strings.Contains(req.System, "personal AI assistant") {
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

	rs := bot.allReactions()
	if len(rs) != 1 {
		t.Fatalf("reactions = %d, want 1 (chat success reaction)", len(rs))
	}
	if rs[0].messageID != 77 {
		t.Errorf("reaction messageID = %d, want 77 (replyToMsgID)", rs[0].messageID)
	}
	if rs[0].emoji != "👍" {
		t.Errorf("reaction emoji = %q, want %q", rs[0].emoji, "👍")
	}
	if rs[0].chatID != "chat-42" {
		t.Errorf("reaction chatID = %q, want %q", rs[0].chatID, "chat-42")
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

	rs := bot.allReactions()
	if len(rs) != 1 {
		t.Fatalf("reactions = %d, want 1 (chat failure reaction)", len(rs))
	}
	if rs[0].messageID != 99 {
		t.Errorf("reaction messageID = %d, want 99 (replyToMsgID)", rs[0].messageID)
	}
	if rs[0].emoji != "😢" {
		t.Errorf("reaction emoji = %q, want %q", rs[0].emoji, "😢")
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
