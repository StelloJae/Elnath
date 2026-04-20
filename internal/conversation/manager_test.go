package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/identity"
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

func TestManagerGetHistory_PrefersCanonicalSessionFileOverStore(t *testing.T) {
	sess, dir := newTestSession(t)
	if err := sess.AppendMessage(llm.NewUserMessage("file-backed msg")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	store := &mockHistoryStore{
		sessions: map[string][]llm.Message{
			sess.ID: {llm.NewUserMessage("stale store msg")},
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
	if got := msgs[0].Text(); got != "file-backed msg" {
		t.Fatalf("history text = %q, want file-backed msg", got)
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

func TestManagerSessionIndex(t *testing.T) {
	dir := t.TempDir()
	olderPrincipal := identity.Principal{UserID: "user-1", ProjectID: "proj-1", Surface: "telegram"}
	newerPrincipal := identity.Principal{UserID: "user-2", ProjectID: "proj-1", Surface: "cli"}

	older, err := agent.NewSession(dir, olderPrincipal)
	if err != nil {
		t.Fatalf("NewSession older: %v", err)
	}
	if err := older.AppendMessage(llm.NewUserMessage("older")); err != nil {
		t.Fatalf("AppendMessage older: %v", err)
	}

	newer, err := agent.NewSession(dir, newerPrincipal)
	if err != nil {
		t.Fatalf("NewSession newer: %v", err)
	}
	if err := newer.AppendMessages([]llm.Message{
		llm.NewUserMessage("newer-1"),
		llm.NewAssistantMessage("newer-2"),
	}); err != nil {
		t.Fatalf("AppendMessages newer: %v", err)
	}

	now := time.Now().UTC()
	if err := os.Chtimes(filepath.Join(dir, "sessions", older.ID+".jsonl"), now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("Chtimes older: %v", err)
	}
	if err := os.Chtimes(filepath.Join(dir, "sessions", newer.ID+".jsonl"), now, now); err != nil {
		t.Fatalf("Chtimes newer: %v", err)
	}

	mgr := NewManager(nil, dir)
	index, err := mgr.SessionIndex()
	if err != nil {
		t.Fatalf("SessionIndex: %v", err)
	}
	if len(index) != 2 {
		t.Fatalf("session index count = %d, want 2", len(index))
	}
	if index[0].ID != newer.ID {
		t.Fatalf("latest index entry = %q, want %q", index[0].ID, newer.ID)
	}
	if index[0].Principal != newerPrincipal {
		t.Fatalf("latest principal = %+v, want %+v", index[0].Principal, newerPrincipal)
	}
	if index[0].MsgCount != 2 {
		t.Fatalf("latest MsgCount = %d, want 2", index[0].MsgCount)
	}
	if index[1].ID != older.ID {
		t.Fatalf("older index entry = %q, want %q", index[1].ID, older.ID)
	}
	if index[1].Principal != olderPrincipal {
		t.Fatalf("older principal = %+v, want %+v", index[1].Principal, olderPrincipal)
	}
}

func TestManagerLoadLatestSession_PrincipalFilter(t *testing.T) {
	dir := t.TempDir()
	matching := identity.Principal{UserID: "user-1", ProjectID: "proj-1", Surface: "telegram"}
	other := identity.Principal{UserID: "user-2", ProjectID: "proj-1", Surface: "cli"}

	wantSession, err := agent.NewSession(dir, matching)
	if err != nil {
		t.Fatalf("NewSession matching: %v", err)
	}
	if err := wantSession.AppendMessage(llm.NewUserMessage("telegram work")); err != nil {
		t.Fatalf("AppendMessage matching: %v", err)
	}

	otherSession, err := agent.NewSession(dir, other)
	if err != nil {
		t.Fatalf("NewSession other: %v", err)
	}
	if err := otherSession.AppendMessage(llm.NewUserMessage("someone else's work")); err != nil {
		t.Fatalf("AppendMessage other: %v", err)
	}

	now := time.Now().UTC()
	if err := os.Chtimes(filepath.Join(dir, "sessions", wantSession.ID+".jsonl"), now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("Chtimes matching: %v", err)
	}
	if err := os.Chtimes(filepath.Join(dir, "sessions", otherSession.ID+".jsonl"), now, now); err != nil {
		t.Fatalf("Chtimes other: %v", err)
	}

	mgr := NewManager(nil, dir)
	latest, err := mgr.LoadLatestSession(identity.Principal{UserID: "user-1", ProjectID: "proj-1", Surface: "cli"})
	if err != nil {
		t.Fatalf("LoadLatestSession(filtered): %v", err)
	}
	if latest.ID != wantSession.ID {
		t.Fatalf("filtered latest session = %q, want %q", latest.ID, wantSession.ID)
	}
}

func TestManagerLoadLatestSession_CrossSurface(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USER", "stello")
	wantPrincipal := identity.ResolveTelegramPrincipal(12345, dir)

	sess, err := agent.NewSession(dir, wantPrincipal)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := sess.AppendMessage(llm.NewUserMessage("resume me from CLI")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	mgr := NewManager(nil, dir)
	latest, err := mgr.LoadLatestSession(identity.ResolveCLIPrincipal(nil, "", dir))
	if err != nil {
		t.Fatalf("LoadLatestSession(cross-surface): %v", err)
	}
	if latest.ID != sess.ID {
		t.Fatalf("cross-surface latest session = %q, want %q", latest.ID, sess.ID)
	}
}

func TestManagerLoadLatestSession_DifferentCrossSurfaceUserRejected(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USER", "stello")
	first, err := agent.NewSession(dir, identity.Principal{UserID: "111", CanonicalUserID: "other@host", ProjectID: "proj-1", Surface: "telegram"})
	if err != nil {
		t.Fatalf("NewSession first: %v", err)
	}
	if err := first.AppendMessage(llm.NewUserMessage("first telegram transcript")); err != nil {
		t.Fatalf("AppendMessage first: %v", err)
	}

	mgr := NewManager(nil, dir)
	_, err = mgr.LoadLatestSession(identity.ResolveCLIPrincipal(nil, "", dir))
	if err == nil {
		t.Fatal("LoadLatestSession(different cross-surface user) error = nil, want error")
	}
}

func TestManagerLoadLatestSession_DifferentUserReturnsNoMatch(t *testing.T) {
	dir := t.TempDir()
	sess, err := agent.NewSession(dir, identity.Principal{UserID: "user-1", ProjectID: "proj-1", Surface: "cli"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := sess.AppendMessage(llm.NewUserMessage("private transcript")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	mgr := NewManager(nil, dir)
	_, err = mgr.LoadLatestSession(identity.Principal{UserID: "user-2", ProjectID: "proj-1", Surface: "cli"})
	if err == nil {
		t.Fatal("LoadLatestSession(different user) error = nil, want error")
	}
}

func TestManagerLoadSessionForPrincipal_DifferentUserRejected(t *testing.T) {
	dir := t.TempDir()
	sess, err := agent.NewSession(dir, identity.Principal{UserID: "user-1", ProjectID: "proj-1", Surface: "telegram"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	mgr := NewManager(nil, dir)
	_, err = mgr.LoadSessionForPrincipal(sess.ID, identity.Principal{UserID: "user-2", ProjectID: "proj-1", Surface: "cli"})
	if err == nil {
		t.Fatal("LoadSessionForPrincipal(different user) error = nil, want error")
	}
}

// TestManagerLoadSessionForPrincipal_CanonicalDriftAllowed guards the F3 root-
// cause fix. Canonical user IDs are derived from USER@HOSTNAME and can drift
// across network/VPN/daemon-restart boundaries even for the same user on the
// same surface. Session ownership must still succeed when the stable triple
// (UserID, ProjectID, Surface) matches, even if canonical has drifted.
func TestManagerLoadSessionForPrincipal_CanonicalDriftAllowed(t *testing.T) {
	dir := t.TempDir()
	created := identity.Principal{
		UserID:          "8568468639",
		CanonicalUserID: "stello@192.168.nate.com",
		ProjectID:       "proj-1",
		Surface:         "telegram",
	}
	sess, err := agent.NewSession(dir, created)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	mgr := NewManager(nil, dir)
	resuming := identity.Principal{
		UserID:          "8568468639",
		CanonicalUserID: "stello@Stellos-MacBook-Pro.local",
		ProjectID:       "proj-1",
		Surface:         "telegram",
	}
	loaded, err := mgr.LoadSessionForPrincipal(sess.ID, resuming)
	if err != nil {
		t.Fatalf("LoadSessionForPrincipal(canonical-drift) = %v, want success", err)
	}
	if loaded.ID != sess.ID {
		t.Fatalf("loaded session = %q, want %q", loaded.ID, sess.ID)
	}
}

func TestManagerLoadSessionForPrincipal_LegacySessionAllowed(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	hdr, err := json.Marshal(struct {
		ID        string    `json:"id"`
		CreatedAt time.Time `json:"created_at"`
		Version   int       `json:"version"`
	}{
		ID:        "legacy-sess",
		CreatedAt: time.Now().UTC(),
		Version:   1,
	})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	msg, err := json.Marshal(llm.NewUserMessage("legacy transcript"))
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	path := filepath.Join(sessionsDir, "legacy-sess.jsonl")
	data := append(hdr, '\n')
	data = append(data, msg...)
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mgr := NewManager(nil, dir)
	loaded, err := mgr.LoadSessionForPrincipal("legacy-sess", identity.Principal{UserID: "stello@host", ProjectID: "proj-1", Surface: "cli"})
	if err != nil {
		t.Fatalf("LoadSessionForPrincipal(legacy): %v", err)
	}
	if loaded.ID != "legacy-sess" {
		t.Fatalf("loaded legacy session = %q, want legacy-sess", loaded.ID)
	}
}

func TestManagerLoadLatestSession_LegacySessionFallback(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	hdr, err := json.Marshal(struct {
		ID        string    `json:"id"`
		CreatedAt time.Time `json:"created_at"`
		Version   int       `json:"version"`
	}{
		ID:        "legacy-sess",
		CreatedAt: time.Now().UTC(),
		Version:   1,
	})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	msg, err := json.Marshal(llm.NewUserMessage("legacy transcript"))
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	path := filepath.Join(sessionsDir, "legacy-sess.jsonl")
	data := append(hdr, '\n')
	data = append(data, msg...)
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mgr := NewManager(nil, dir)
	latest, err := mgr.LoadLatestSession(identity.Principal{UserID: "stello@host", ProjectID: "proj-1", Surface: "cli"})
	if err != nil {
		t.Fatalf("LoadLatestSession(legacy): %v", err)
	}
	if latest.ID != "legacy-sess" {
		t.Fatalf("legacy fallback session = %q, want legacy-sess", latest.ID)
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

func TestManagerSendMessage_ReconstructsLocaleOnResume(t *testing.T) {
	sess, dir := newTestSession(t)
	mgr := NewManager(nil, dir)

	if _, _, err := mgr.SendMessage(context.Background(), sess.ID, "안녕하세요"); err != nil {
		t.Fatalf("first SendMessage: %v", err)
	}

	resumed := NewManager(nil, dir)
	if _, _, err := resumed.SendMessage(context.Background(), sess.ID, "네"); err != nil {
		t.Fatalf("resumed SendMessage: %v", err)
	}
	if got := resumed.LastLocale(sess.ID); got != "ko" {
		t.Fatalf("LastLocale(resumed) = %q, want ko", got)
	}
}

func TestManagerSendMessage_IsolatesLocalePerSession(t *testing.T) {
	first, dir := newTestSession(t)
	second, err := agent.NewSession(dir)
	if err != nil {
		t.Fatalf("agent.NewSession: %v", err)
	}

	mgr := NewManager(nil, dir)
	if _, _, err := mgr.SendMessage(context.Background(), first.ID, "안녕하세요"); err != nil {
		t.Fatalf("first session SendMessage: %v", err)
	}
	if _, _, err := mgr.SendMessage(context.Background(), second.ID, "ok"); err != nil {
		t.Fatalf("second session SendMessage: %v", err)
	}
	if got := mgr.LastLocale(first.ID); got != "ko" {
		t.Fatalf("LastLocale(first) = %q, want ko", got)
	}
	if got := mgr.LastLocale(second.ID); got != "en" {
		t.Fatalf("LastLocale(second) = %q, want en", got)
	}
}

// --- FU-SessionCompaction P1: dynamic provider context window ---

func buildStructuredSummaryFixture() string {
	return `# Session Summary

## 1. User goal
Testing compression behavior.

## 2. Completed steps
- Seeded fixture messages for compression tests.

## 3. Current focus
Validating the dynamic threshold path.

## 4. Files touched
(none)

## 5. Outstanding TODOs
(none)

## 6. Blockers / unresolved
(none)

## 7. Key decisions
- Use a 9-section fixture so parseStructuredSummary succeeds.

## 8. Open questions
(none)

## 9. Next action
Run the compression path end-to-end.`
}

func seedCompressionMessages(t *testing.T, sess *agent.Session, count int) {
	t.Helper()
	msgs := make([]llm.Message, 0, count)
	filler := strings.Repeat("abc ", 24) // ~96 chars + index suffix keeps each body unique
	for i := 0; i < count; i++ {
		// Session.AppendMessage dedups user-role messages by content hash, so each
		// body must be distinct to survive persistence. See internal/agent/session.go.
		body := fmt.Sprintf("%s[msg-%03d]", filler, i)
		if i%2 == 0 {
			msgs = append(msgs, llm.NewUserMessage(body))
		} else {
			msgs = append(msgs, llm.NewAssistantMessage(body))
		}
	}
	if err := sess.AppendMessages(msgs); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}
}

// TestManagerSendMessage_CompressesWhenOverDynamicThreshold verifies that when
// a provider context window is wired on the Manager, the compression threshold
// derives from that window instead of the static 100K default. Seed 50 messages
// (~1450 estimated tokens) with providerContextWindow=1000 and assert the
// returned slice was compressed and starts with a structured summary.
func TestManagerSendMessage_CompressesWhenOverDynamicThreshold(t *testing.T) {
	sess, dir := newTestSession(t)
	seedCompressionMessages(t, sess, 50)

	fixture := buildStructuredSummaryFixture()
	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{Content: fixture}, nil
		},
	}

	cw := NewContextWindow()
	mgr := NewManager(nil, dir).
		WithContextWindow(cw).
		WithProvider(provider).
		WithProviderContextWindow(1000)

	result, _, err := mgr.SendMessage(context.Background(), sess.ID, "ping")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// 50 seeded + 1 new user message = 51 before compression. Expect shrink.
	if len(result) >= 50 {
		t.Errorf("compressed message count = %d, want < 50", len(result))
	}
	if len(result) == 0 {
		t.Fatal("result is empty")
	}
	if !isStructuredSummaryMessage(result[0]) {
		t.Errorf("first message is not a structured summary: role=%q text=%q",
			result[0].Role, result[0].Text())
	}
}

// TestManagerSendMessage_SkipsCompressionWhenUnderThreshold verifies that when
// the provider context window is comfortably above the message total, no LLM
// summary call is made and the history flows through intact.
func TestManagerSendMessage_SkipsCompressionWhenUnderThreshold(t *testing.T) {
	sess, dir := newTestSession(t)
	seedCompressionMessages(t, sess, 50)

	chatCalled := 0
	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			chatCalled++
			return &llm.ChatResponse{Content: buildStructuredSummaryFixture()}, nil
		},
	}

	cw := NewContextWindow()
	mgr := NewManager(nil, dir).
		WithContextWindow(cw).
		WithProvider(provider).
		WithProviderContextWindow(100_000)

	result, _, err := mgr.SendMessage(context.Background(), sess.ID, "ping")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if len(result) != 51 {
		t.Errorf("message count = %d, want 51 (50 seeded + 1 new user)", len(result))
	}
	if chatCalled != 0 {
		t.Errorf("provider.Chat called %d times, want 0 (no compression expected)", chatCalled)
	}
}

// TestManagerSendMessage_FallsBackToConfigMaxWhenProviderWindowUnknown covers
// the fallback branch: providerContextWindow=0 (unknown) means the manager
// should use the configured MaxContextTokens to size the threshold.
func TestManagerSendMessage_FallsBackToConfigMaxWhenProviderWindowUnknown(t *testing.T) {
	sess, dir := newTestSession(t)
	seedCompressionMessages(t, sess, 50)

	fixture := buildStructuredSummaryFixture()
	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{Content: fixture}, nil
		},
	}

	cw := NewContextWindow()
	mgr := NewManager(nil, dir).
		WithContextWindow(cw).
		WithProvider(provider).
		WithMaxContextTokens(500).
		WithProviderContextWindow(0)

	result, _, err := mgr.SendMessage(context.Background(), sess.ID, "ping")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// With maxContextTokens=500 the auto-compress threshold is 400. 50 seeded
	// messages (~1450 estimated tokens) comfortably exceed that → compress.
	if len(result) >= 50 {
		t.Errorf("compressed message count = %d, want < 50 (config fallback threshold)", len(result))
	}
	if len(result) == 0 {
		t.Fatal("result is empty")
	}
	if !isStructuredSummaryMessage(result[0]) {
		t.Errorf("first message is not a structured summary: role=%q text=%q",
			result[0].Role, result[0].Text())
	}
}

// --- Chat-path persistence (FU-ChatSessionPersist) ---

func TestManagerEnsureChatSession_CreatesSessionWithPrincipal(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(nil, dir)

	principal := identity.Principal{UserID: "42", ProjectID: "proj-chat", Surface: "telegram"}
	sid, err := mgr.EnsureChatSession(context.Background(), principal)
	if err != nil {
		t.Fatalf("EnsureChatSession: %v", err)
	}
	if sid == "" {
		t.Fatal("EnsureChatSession returned empty session ID")
	}

	loaded, err := mgr.LoadSession(sid)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if loaded.Principal.UserID != "42" {
		t.Errorf("Principal.UserID = %q, want %q", loaded.Principal.UserID, "42")
	}
	if loaded.Principal.ProjectID != "proj-chat" {
		t.Errorf("Principal.ProjectID = %q, want %q", loaded.Principal.ProjectID, "proj-chat")
	}
	if loaded.Principal.Surface != "telegram" {
		t.Errorf("Principal.Surface = %q, want %q", loaded.Principal.Surface, "telegram")
	}
}

func TestManagerAppendChatTurn_PersistsUserAndAssistant(t *testing.T) {
	sess, dir := newTestSession(t)
	mgr := NewManager(nil, dir)

	userMsg := llm.NewUserMessage("what time is it?")
	asstMsg := llm.NewAssistantMessage("it's 9 pm")

	if err := mgr.AppendChatTurn(context.Background(), sess.ID, userMsg, asstMsg); err != nil {
		t.Fatalf("AppendChatTurn: %v", err)
	}

	reloaded, err := mgr.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(reloaded.Messages) != 2 {
		t.Fatalf("reloaded message count = %d, want 2", len(reloaded.Messages))
	}
	if reloaded.Messages[0].Role != llm.RoleUser {
		t.Errorf("first message role = %q, want user", reloaded.Messages[0].Role)
	}
	if got := reloaded.Messages[0].Text(); got != "what time is it?" {
		t.Errorf("first message text = %q, want %q", got, "what time is it?")
	}
	if reloaded.Messages[1].Role != llm.RoleAssistant {
		t.Errorf("second message role = %q, want assistant", reloaded.Messages[1].Role)
	}
	if got := reloaded.Messages[1].Text(); got != "it's 9 pm" {
		t.Errorf("second message text = %q, want %q", got, "it's 9 pm")
	}
}

func TestManagerAppendChatTurn_AccumulatesAcrossTurns(t *testing.T) {
	sess, dir := newTestSession(t)
	mgr := NewManager(nil, dir)
	ctx := context.Background()

	if err := mgr.AppendChatTurn(ctx,
		sess.ID,
		llm.NewUserMessage("hi"),
		llm.NewAssistantMessage("hello"),
	); err != nil {
		t.Fatalf("first AppendChatTurn: %v", err)
	}
	if err := mgr.AppendChatTurn(ctx,
		sess.ID,
		llm.NewUserMessage("what did I just say?"),
		llm.NewAssistantMessage("you said hi"),
	); err != nil {
		t.Fatalf("second AppendChatTurn: %v", err)
	}

	history, err := mgr.GetHistory(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) != 4 {
		t.Fatalf("history len = %d, want 4 (2 turns × 2 msgs)", len(history))
	}
	if got := history[0].Text(); got != "hi" {
		t.Errorf("history[0].Text = %q, want hi", got)
	}
	if got := history[3].Text(); got != "you said hi" {
		t.Errorf("history[3].Text = %q, want 'you said hi'", got)
	}
}
