package conversation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
)

// --- mock types shared across all test files in this package ---

type mockClassifier struct {
	intent Intent
	err    error
}

func (m *mockClassifier) Classify(_ context.Context, _ llm.Provider, _ string, _ []llm.Message) (Intent, error) {
	return m.intent, m.err
}

type mockContextWindow struct {
	fitFn      func(ctx context.Context, messages []llm.Message, maxTokens int) ([]llm.Message, error)
	compressFn func(ctx context.Context, provider llm.Provider, messages []llm.Message, maxTokens int) ([]llm.Message, error)
}

func (m *mockContextWindow) Fit(ctx context.Context, messages []llm.Message, maxTokens int) ([]llm.Message, error) {
	if m.fitFn != nil {
		return m.fitFn(ctx, messages, maxTokens)
	}
	return messages, nil
}

func (m *mockContextWindow) CompressMessages(ctx context.Context, provider llm.Provider, messages []llm.Message, maxTokens int) ([]llm.Message, error) {
	if m.compressFn != nil {
		return m.compressFn(ctx, provider, messages, maxTokens)
	}
	// Default: delegate to Fit.
	return m.Fit(ctx, messages, maxTokens)
}

type mockHistoryStore struct {
	sessions map[string][]llm.Message
	saveErr  error
	loadErr  error
	infos    []SessionInfo
	listErr  error
}

func (m *mockHistoryStore) Save(_ context.Context, sessionID string, messages []llm.Message) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	if m.sessions == nil {
		m.sessions = make(map[string][]llm.Message)
	}
	m.sessions[sessionID] = messages
	return nil
}

func (m *mockHistoryStore) Load(_ context.Context, sessionID string) ([]llm.Message, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.sessions[sessionID], nil
}

func (m *mockHistoryStore) ListSessions(_ context.Context) ([]SessionInfo, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	if m.infos != nil {
		return append([]SessionInfo(nil), m.infos...), nil
	}
	var result []SessionInfo
	for id, msgs := range m.sessions {
		result = append(result, SessionInfo{ID: id, MessageCount: len(msgs)})
	}
	return result, nil
}

type mockProvider struct {
	chatFn func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
}

func (m *mockProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.chatFn != nil {
		return m.chatFn(ctx, req)
	}
	return &llm.ChatResponse{Content: `{"intent":"unclear","confidence":0.5}`}, nil
}

func (m *mockProvider) Stream(_ context.Context, _ llm.ChatRequest, _ func(llm.StreamEvent)) error {
	return nil
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Models() []llm.ModelInfo { return nil }

// --- helpers ---

// newTestSession creates a real agent.Session in t.TempDir() and returns
// the session and the data directory so Manager can be pointed at the same dir.
func newTestSession(t *testing.T) (*agent.Session, string) {
	t.Helper()
	dir := t.TempDir()
	sess, err := agent.NewSession(dir)
	if err != nil {
		t.Fatalf("agent.NewSession: %v", err)
	}
	return sess, dir
}

// --- Manager tests ---

func TestManagerNewSession(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(nil, dir)

	sess, err := mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if sess == nil {
		t.Fatal("NewSession returned nil session")
	}
	if sess.ID == "" {
		t.Error("NewSession returned session with empty ID")
	}
}

func TestManagerLoadSession(t *testing.T) {
	sess, dir := newTestSession(t)
	mgr := NewManager(nil, dir)

	loaded, err := mgr.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if loaded.ID != sess.ID {
		t.Errorf("loaded ID = %q, want %q", loaded.ID, sess.ID)
	}
}

func TestManagerLoadSession_NotFound(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(nil, dir)

	_, err := mgr.LoadSession("does-not-exist")
	if err == nil {
		t.Fatal("expected error loading nonexistent session, got nil")
	}
}

func TestManagerSendMessage_NoOptionals(t *testing.T) {
	sess, dir := newTestSession(t)
	mgr := NewManager(nil, dir)

	msgs, intent, err := mgr.SendMessage(context.Background(), sess.ID, "hello")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if intent != IntentUnclear {
		t.Errorf("intent = %q, want %q", intent, IntentUnclear)
	}
	if len(msgs) != 1 {
		t.Errorf("message count = %d, want 1", len(msgs))
	}
	if msgs[0].Role != llm.RoleUser {
		t.Errorf("first message role = %q, want %q", msgs[0].Role, llm.RoleUser)
	}
}

func TestManagerSendMessage_PersistsUserMessageToSessionSnapshot(t *testing.T) {
	sess, dir := newTestSession(t)
	mgr := NewManager(nil, dir)

	if _, _, err := mgr.SendMessage(context.Background(), sess.ID, "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	reloaded, err := mgr.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(reloaded.Messages) != 1 {
		t.Fatalf("reloaded message count = %d, want 1", len(reloaded.Messages))
	}
	if got := reloaded.Messages[0].Text(); got != "hello" {
		t.Fatalf("reloaded first message = %q, want hello", got)
	}
}

func TestManagerSendMessage_WithClassifier(t *testing.T) {
	sess, dir := newTestSession(t)
	mgr := NewManager(nil, dir).
		WithClassifier(&mockClassifier{intent: IntentQuestion}).
		WithProvider(&mockProvider{})

	_, intent, err := mgr.SendMessage(context.Background(), sess.ID, "what is Go?")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if intent != IntentQuestion {
		t.Errorf("intent = %q, want %q", intent, IntentQuestion)
	}
}

func TestManagerSendMessage_ClassifierError(t *testing.T) {
	// Classifier fails → intent defaults to IntentUnclear, no error returned.
	sess, dir := newTestSession(t)
	mgr := NewManager(nil, dir).
		WithClassifier(&mockClassifier{err: errors.New("network error")}).
		WithProvider(&mockProvider{})

	_, intent, err := mgr.SendMessage(context.Background(), sess.ID, "test")
	if err != nil {
		t.Fatalf("SendMessage: unexpected error: %v", err)
	}
	if intent != IntentUnclear {
		t.Errorf("intent = %q, want %q after classifier error", intent, IntentUnclear)
	}
}

func TestManagerSendMessage_WithHistoryStore(t *testing.T) {
	sess, dir := newTestSession(t)
	store := &mockHistoryStore{}
	mgr := NewManager(nil, dir).WithHistoryStore(store)

	_, _, err := mgr.SendMessage(context.Background(), sess.ID, "save me")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	saved, ok := store.sessions[sess.ID]
	if !ok {
		t.Fatal("history.Save was not called")
	}
	if len(saved) != 1 {
		t.Errorf("saved message count = %d, want 1", len(saved))
	}
}

func TestManagerSendMessage_HistoryStoreSaveError(t *testing.T) {
	// Save failure is non-fatal; SendMessage must still succeed.
	sess, dir := newTestSession(t)
	store := &mockHistoryStore{saveErr: errors.New("disk full")}
	mgr := NewManager(nil, dir).WithHistoryStore(store)

	_, _, err := mgr.SendMessage(context.Background(), sess.ID, "hello")
	if err != nil {
		t.Fatalf("SendMessage should not fail on history save error: %v", err)
	}
}

func TestManagerSendMessage_WithContextWindow(t *testing.T) {
	sess, dir := newTestSession(t)
	called := false
	cw := &mockContextWindow{
		fitFn: func(_ context.Context, msgs []llm.Message, _ int) ([]llm.Message, error) {
			called = true
			return msgs, nil
		},
	}
	mgr := NewManager(nil, dir).WithContextWindow(cw)

	_, _, err := mgr.SendMessage(context.Background(), sess.ID, "hello")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if !called {
		t.Error("ContextWindow.Fit was not called")
	}
}

func TestManagerSendMessage_ContextWindowError(t *testing.T) {
	// Fit failure → original messages used, no error returned.
	sess, dir := newTestSession(t)
	cw := &mockContextWindow{
		fitFn: func(_ context.Context, msgs []llm.Message, _ int) ([]llm.Message, error) {
			return nil, errors.New("context window error")
		},
	}
	mgr := NewManager(nil, dir).WithContextWindow(cw)

	msgs, _, err := mgr.SendMessage(context.Background(), sess.ID, "hello")
	if err != nil {
		t.Fatalf("SendMessage: unexpected error: %v", err)
	}
	// Fallback: original messages (empty) + user message = 1 message.
	if len(msgs) != 1 {
		t.Errorf("message count = %d, want 1 after context window error", len(msgs))
	}
}

func TestManagerSendMessage_BadSessionID(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(nil, dir)

	_, _, err := mgr.SendMessage(context.Background(), "no-such-session", "hello")
	if err == nil {
		t.Fatal("expected error for missing session, got nil")
	}
}

func TestManagerGetHistory_FromStore(t *testing.T) {
	dir := t.TempDir()
	store := &mockHistoryStore{
		sessions: map[string][]llm.Message{
			"store-only": {llm.NewUserMessage("stored msg")},
		},
	}
	store.infos = []SessionInfo{{ID: "store-only", MessageCount: 1}}
	mgr := NewManager(nil, dir).WithHistoryStore(store)

	msgs, err := mgr.GetHistory(context.Background(), "store-only")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("message count = %d, want 1", len(msgs))
	}
	if got := msgs[0].Text(); got != "stored msg" {
		t.Fatalf("history text = %q, want stored msg", got)
	}
}

func TestManagerGetHistory_PrefersSessionFileOverStore(t *testing.T) {
	sess, dir := newTestSession(t)
	if err := sess.AppendMessage(llm.NewUserMessage("from-file")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	store := &mockHistoryStore{
		sessions: map[string][]llm.Message{
			sess.ID: {llm.NewUserMessage("from-store")},
		},
	}
	mgr := NewManager(nil, dir).WithHistoryStore(store)

	msgs, err := mgr.GetHistory(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(msgs))
	}
	if got := msgs[0].Text(); got != "from-file" {
		t.Fatalf("history text = %q, want from-file", got)
	}
	if got := msgs[0].Text(); got != "file-backed msg" {
		t.Fatalf("first message = %q, want canonical JSONL message", got)
	}
}

func TestManagerGetHistory_FallbackToSessionFile(t *testing.T) {
	// No history store → falls back to JSONL session file.
	sess, dir := newTestSession(t)
	mgr := NewManager(nil, dir)

	msgs, err := mgr.GetHistory(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	// Session has no messages appended yet.
	if len(msgs) != 0 {
		t.Errorf("message count = %d, want 0", len(msgs))
	}
}

func TestManagerGetHistory_StoreLoadError_FallsBackToFile(t *testing.T) {
	sess, dir := newTestSession(t)
	store := &mockHistoryStore{loadErr: errors.New("store unavailable")}
	mgr := NewManager(nil, dir).WithHistoryStore(store)

	msgs, err := mgr.GetHistory(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("fallback message count = %d, want 0", len(msgs))
	}
}

func TestManagerGetHistory_FallsBackToStoreWhenSessionFileMissing(t *testing.T) {
	dir := t.TempDir()
	store := &mockHistoryStore{
		sessions: map[string][]llm.Message{
			"store-only": {llm.NewUserMessage("stored msg")},
		},
	}
	mgr := NewManager(nil, dir).WithHistoryStore(store)

	msgs, err := mgr.GetHistory(context.Background(), "store-only")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(msgs))
	}
	if got := msgs[0].Text(); got != "stored msg" {
		t.Fatalf("first message = %q, want store fallback message", got)
	}
}

func TestManagerGetHistory_FallsBackToStoreWhenSessionFileIsCorrupt(t *testing.T) {
	sess, dir := newTestSession(t)
	path := filepath.Join(dir, "sessions", sess.ID+".jsonl")
	if err := os.WriteFile(path, []byte("not-json\n"), 0644); err != nil {
		t.Fatalf("WriteFile corrupt session: %v", err)
	}

	store := &mockHistoryStore{
		sessions: map[string][]llm.Message{
			sess.ID: {llm.NewUserMessage("stored msg")},
		},
	}
	mgr := NewManager(nil, dir).WithHistoryStore(store)

	msgs, err := mgr.GetHistory(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(msgs))
	}
	if got := msgs[0].Text(); got != "stored msg" {
		t.Fatalf("first message = %q, want store fallback message", got)
	}
}

func TestManagerGetHistory_BadSessionID(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(nil, dir)

	_, err := mgr.GetHistory(context.Background(), "no-such-id")
	if err == nil {
		t.Fatal("expected error for missing session, got nil")
	}
}

func TestManagerListSessions_WithStore(t *testing.T) {
	sess, dir := newTestSession(t)
	store := &mockHistoryStore{
		sessions: map[string][]llm.Message{
			"s1": {llm.NewUserMessage("a")},
			"s2": {llm.NewUserMessage("b"), llm.NewAssistantMessage("c")},
		},
	}
	mgr := NewManager(nil, dir).WithHistoryStore(store)

	sessions, err := mgr.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("session count = %d, want 3", len(sessions))
	}
	seen := map[string]bool{}
	for _, info := range sessions {
		seen[info.ID] = true
	}
	for _, id := range []string{"s1", "s2", sess.ID} {
		if !seen[id] {
			t.Fatalf("session %q missing from merged list", id)
		}
	}
}

func TestManagerListSessions_NoStore(t *testing.T) {
	sess, dir := newTestSession(t)
	mgr := NewManager(nil, dir)

	sessions, err := mgr.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 file-backed session, got %d", len(sessions))
	}
	if sessions[0].ID != sess.ID {
		t.Fatalf("session ID = %q, want %q", sessions[0].ID, sess.ID)
	}
}

func TestManagerListSessions_PrefersFileMetadataForTranscriptBackedSessions(t *testing.T) {
	sessA, dir := newTestSession(t)
	if err := sessA.AppendMessages([]llm.Message{
		llm.NewUserMessage("from-file-1"),
		llm.NewAssistantMessage("from-file-2"),
	}); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	fileInfos, err := agent.ListSessionFiles(dir)
	if err != nil {
		t.Fatalf("ListSessionFiles: %v", err)
	}
	if len(fileInfos) != 1 {
		t.Fatalf("file-backed session count = %d, want 1", len(fileInfos))
	}

	store := &mockHistoryStore{
		infos: []SessionInfo{
			{
				ID:           sessA.ID,
				CreatedAt:    fileInfos[0].CreatedAt.Add(-2 * time.Hour),
				UpdatedAt:    fileInfos[0].UpdatedAt.Add(2 * time.Hour),
				MessageCount: 99,
			},
		},
	}
	mgr := NewManager(nil, dir).WithHistoryStore(store)

	sessions, err := mgr.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(sessions))
	}
	if sessions[0].MessageCount != fileInfos[0].MessageCount {
		t.Fatalf("MessageCount = %d, want file-backed %d", sessions[0].MessageCount, fileInfos[0].MessageCount)
	}
	if !sessions[0].UpdatedAt.Equal(fileInfos[0].UpdatedAt) {
		t.Fatalf("UpdatedAt = %v, want file-backed %v", sessions[0].UpdatedAt, fileInfos[0].UpdatedAt)
	}
	if !sessions[0].CreatedAt.Equal(fileInfos[0].CreatedAt) {
		t.Fatalf("CreatedAt = %v, want file-backed %v", sessions[0].CreatedAt, fileInfos[0].CreatedAt)
	}
}

func TestManagerLoadLatestSession_PrefersMostRecentTranscript(t *testing.T) {
	sessA, dir := newTestSession(t)
	sessB, err := agent.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession B: %v", err)
	}
	if err := sessA.AppendMessage(llm.NewUserMessage("older session")); err != nil {
		t.Fatalf("AppendMessage A: %v", err)
	}
	if err := sessB.AppendMessage(llm.NewUserMessage("newer session")); err != nil {
		t.Fatalf("AppendMessage B: %v", err)
	}

	now := time.Now().UTC()
	if err := os.Chtimes(filepath.Join(dir, "sessions", sessA.ID+".jsonl"), now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("Chtimes A: %v", err)
	}
	if err := os.Chtimes(filepath.Join(dir, "sessions", sessB.ID+".jsonl"), now, now); err != nil {
		t.Fatalf("Chtimes B: %v", err)
	}

	store := &mockHistoryStore{
		infos: []SessionInfo{
			{ID: sessA.ID, UpdatedAt: now.Add(-time.Hour), MessageCount: 1},
		},
	}
	mgr := NewManager(nil, dir).WithHistoryStore(store)

	latest, err := mgr.LoadLatestSession()
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if latest.ID != sessB.ID {
		t.Fatalf("latest session = %q, want %q", latest.ID, sessB.ID)
	}
}

func TestManagerLoadLatestSession_IgnoresStoreOnlyNewerCandidates(t *testing.T) {
	sess, dir := newTestSession(t)
	if err := sess.AppendMessage(llm.NewUserMessage("resumable transcript")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	now := time.Now().UTC()
	if err := os.Chtimes(filepath.Join(dir, "sessions", sess.ID+".jsonl"), now, now); err != nil {
		t.Fatalf("Chtimes transcript: %v", err)
	}

	store := &mockHistoryStore{
		infos: []SessionInfo{
			{ID: "store-only-newer", UpdatedAt: now.Add(time.Hour), MessageCount: 5},
		},
	}
	mgr := NewManager(nil, dir).WithHistoryStore(store)

	for i := 0; i < 3; i++ {
		latest, err := mgr.LoadLatestSession()
		if err != nil {
			t.Fatalf("LoadLatestSession run %d: %v", i+1, err)
		}
		if latest.ID != sess.ID {
			t.Fatalf("LoadLatestSession run %d = %q, want %q", i+1, latest.ID, sess.ID)
		}
	}
}

func TestManagerWithMethods_ReturnsSelf(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(nil, dir)

	// Verify fluent API returns the same *Manager.
	p := &mockProvider{}
	c := &mockClassifier{}
	cw := &mockContextWindow{}
	hs := &mockHistoryStore{}

	result := mgr.
		WithProvider(p).
		WithClassifier(c).
		WithContextWindow(cw).
		WithHistoryStore(hs)

	if result != mgr {
		t.Error("With* methods should return the same *Manager")
	}
}
