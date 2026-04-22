package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/userfacingerr"
)

type legacySessionHeader struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Version   int       `json:"version"`
}

func sessionMessageLineCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return strings.Count(string(data), "\n") - 1
}

func TestSessionNewAndAppend(t *testing.T) {
	dir := t.TempDir()

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if s.ID == "" {
		t.Error("session ID must not be empty")
	}
	if len(s.Messages) != 0 {
		t.Errorf("new session should have no messages, got %d", len(s.Messages))
	}

	msgs := []llm.Message{
		llm.NewUserMessage("hello"),
		llm.NewAssistantMessage("world"),
	}
	for _, m := range msgs {
		if err := s.AppendMessage(m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	if len(s.Messages) != 2 {
		t.Fatalf("in-memory messages = %d, want 2", len(s.Messages))
	}

	// Reload from disk and verify roundtrip.
	loaded, err := LoadSession(dir, s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	if loaded.ID != s.ID {
		t.Errorf("loaded ID = %q, want %q", loaded.ID, s.ID)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("loaded messages = %d, want 2", len(loaded.Messages))
	}

	for i, want := range msgs {
		got := loaded.Messages[i]
		if got.Role != want.Role {
			t.Errorf("messages[%d].Role = %q, want %q", i, got.Role, want.Role)
		}
		if got.Text() != want.Text() {
			t.Errorf("messages[%d].Text() = %q, want %q", i, got.Text(), want.Text())
		}
	}
}

func TestSessionAppendMessages(t *testing.T) {
	dir := t.TempDir()

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	batch := []llm.Message{
		llm.NewUserMessage("first"),
		llm.NewAssistantMessage("second"),
		llm.NewUserMessage("third"),
	}
	if err := s.AppendMessages(batch); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	loaded, err := LoadSession(dir, s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(loaded.Messages) != 3 {
		t.Errorf("loaded messages = %d, want 3", len(loaded.Messages))
	}
}

func TestAppendMessageDedupesIdenticalMessage(t *testing.T) {
	dir := t.TempDir()

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	msg := llm.NewUserMessage("hello")

	if err := s.AppendMessage(msg); err != nil {
		t.Fatalf("AppendMessage(first): %v", err)
	}
	if err := s.AppendMessage(msg); err != nil {
		t.Fatalf("AppendMessage(second): %v", err)
	}

	if len(s.Messages) != 1 {
		t.Fatalf("in-memory messages = %d, want 1", len(s.Messages))
	}
	if got := sessionMessageLineCount(t, filepath.Join(dir, "sessions", s.ID+".jsonl")); got != 1 {
		t.Fatalf("session file messages = %d, want 1", got)
	}
}

func TestLoadSessionPopulatesAppliedHashes(t *testing.T) {
	dir := t.TempDir()

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	msgs := []llm.Message{
		llm.NewUserMessage("one"),
		llm.NewAssistantMessage("two"),
		llm.NewUserMessage("three"),
	}
	if err := s.AppendMessages(msgs); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	loaded, err := LoadSession(dir, s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if err := loaded.AppendMessage(msgs[2]); err != nil {
		t.Fatalf("AppendMessage duplicate: %v", err)
	}

	if len(loaded.Messages) != 3 {
		t.Fatalf("loaded messages = %d, want 3", len(loaded.Messages))
	}
	if got := sessionMessageLineCount(t, filepath.Join(dir, "sessions", loaded.ID+".jsonl")); got != 3 {
		t.Fatalf("session file messages = %d, want 3", got)
	}
}

func TestAppendMessageDoesNotDedupeAssistantMessages(t *testing.T) {
	dir := t.TempDir()

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	msg := llm.NewAssistantMessage("identical response")

	if err := s.AppendMessage(msg); err != nil {
		t.Fatalf("AppendMessage(first): %v", err)
	}
	if err := s.AppendMessage(msg); err != nil {
		t.Fatalf("AppendMessage(second): %v", err)
	}

	if len(s.Messages) != 2 {
		t.Fatalf("assistant messages = %d, want 2", len(s.Messages))
	}
	if got := sessionMessageLineCount(t, filepath.Join(dir, "sessions", s.ID+".jsonl")); got != 2 {
		t.Fatalf("assistant message lines = %d, want 2", got)
	}

	reloaded, err := LoadSession(dir, s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(reloaded.Messages) != 2 {
		t.Fatalf("reloaded assistant messages = %d, want 2", len(reloaded.Messages))
	}
}

func TestAppendMessageConcurrentSafe(t *testing.T) {
	dir := t.TempDir()

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	msg := llm.NewUserMessage("same prompt")

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if err := s.AppendMessage(msg); err != nil {
				t.Errorf("AppendMessage: %v", err)
			}
		}()
	}
	wg.Wait()

	if len(s.Messages) != 1 {
		t.Fatalf("concurrent messages = %d, want 1", len(s.Messages))
	}
	if got := sessionMessageLineCount(t, filepath.Join(dir, "sessions", s.ID+".jsonl")); got != 1 {
		t.Fatalf("concurrent message lines = %d, want 1", got)
	}
}

func TestSessionFork(t *testing.T) {
	dir := t.TempDir()

	parent, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	msgs := []llm.Message{
		llm.NewUserMessage("parent msg 1"),
		llm.NewAssistantMessage("parent reply"),
	}
	if err := parent.AppendMessages(msgs); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	child, err := parent.Fork(dir)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	// Fork must produce a distinct session.
	if child.ID == parent.ID {
		t.Error("forked session must have a different ID")
	}

	// Child must contain all parent messages.
	if len(child.Messages) != len(parent.Messages) {
		t.Errorf("child messages = %d, want %d", len(child.Messages), len(parent.Messages))
	}
	for i := range msgs {
		if child.Messages[i].Text() != parent.Messages[i].Text() {
			t.Errorf("messages[%d] mismatch: child=%q parent=%q",
				i, child.Messages[i].Text(), parent.Messages[i].Text())
		}
	}

	// Verify the child file exists and is loadable independently.
	loaded, err := LoadSession(dir, child.ID)
	if err != nil {
		t.Fatalf("LoadSession(child): %v", err)
	}
	if len(loaded.Messages) != 2 {
		t.Errorf("loaded child messages = %d, want 2", len(loaded.Messages))
	}

	// Adding a message to the child must not affect the parent.
	if err := child.AppendMessage(llm.NewUserMessage("child only")); err != nil {
		t.Fatalf("child AppendMessage: %v", err)
	}

	reloadedParent, err := LoadSession(dir, parent.ID)
	if err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if len(reloadedParent.Messages) != 2 {
		t.Errorf("parent messages after child append = %d, want 2 (unchanged)", len(reloadedParent.Messages))
	}
}

func TestLoadSessionNotFound(t *testing.T) {
	dir := t.TempDir()

	_, err := LoadSession(dir, "nonexistent-id")
	if err == nil {
		t.Error("LoadSession with nonexistent ID should return an error")
	}
}

func TestListSessionFiles(t *testing.T) {
	dir := t.TempDir()

	first, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession first: %v", err)
	}
	if err := first.AppendMessage(llm.NewUserMessage("first")); err != nil {
		t.Fatalf("AppendMessage first: %v", err)
	}

	second, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession second: %v", err)
	}
	if err := second.AppendMessages([]llm.Message{
		llm.NewUserMessage("second-1"),
		llm.NewAssistantMessage("second-2"),
	}); err != nil {
		t.Fatalf("AppendMessages second: %v", err)
	}

	now := time.Now().UTC()
	firstPath := filepath.Join(dir, "sessions", first.ID+".jsonl")
	secondPath := filepath.Join(dir, "sessions", second.ID+".jsonl")
	if err := os.Chtimes(firstPath, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("Chtimes first: %v", err)
	}
	if err := os.Chtimes(secondPath, now, now); err != nil {
		t.Fatalf("Chtimes second: %v", err)
	}

	infos, err := ListSessionFiles(dir)
	if err != nil {
		t.Fatalf("ListSessionFiles: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("session count = %d, want 2", len(infos))
	}
	if infos[0].ID != second.ID {
		t.Fatalf("latest file-backed session = %q, want %q", infos[0].ID, second.ID)
	}
	if infos[0].MessageCount != 2 {
		t.Fatalf("second MessageCount = %d, want 2", infos[0].MessageCount)
	}
	if infos[1].ID != first.ID {
		t.Fatalf("older file-backed session = %q, want %q", infos[1].ID, first.ID)
	}
	if infos[1].MessageCount != 1 {
		t.Fatalf("first MessageCount = %d, want 1", infos[1].MessageCount)
	}
}

func TestReadSessionHeader(t *testing.T) {
	dir := t.TempDir()
	want := identity.Principal{UserID: "stello", ProjectID: "elnath", Surface: "cli"}

	s, err := NewSession(dir, want)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	hdr, err := ReadSessionHeader(sessionPath(dir, s.ID))
	if err != nil {
		t.Fatalf("ReadSessionHeader: %v", err)
	}
	if hdr.ID != s.ID {
		t.Fatalf("header ID = %q, want %q", hdr.ID, s.ID)
	}
	if hdr.Principal != want {
		t.Fatalf("header principal = %+v, want %+v", hdr.Principal, want)
	}
}

func TestReadSessionHeader_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := ReadSessionHeader(path)
	if err == nil {
		t.Fatal("ReadSessionHeader() error = nil, want error for empty file")
	}
}

func TestRecordResume(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSession(dir, identity.Principal{UserID: "12345", ProjectID: "elnath", Surface: "telegram"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	want := identity.Principal{UserID: "stello@host", ProjectID: "elnath", Surface: "cli"}

	if err := s.RecordResume(want); err != nil {
		t.Fatalf("RecordResume: %v", err)
	}

	resumes, err := LoadSessionResumeEvents(dir, s.ID)
	if err != nil {
		t.Fatalf("LoadSessionResumeEvents: %v", err)
	}
	if len(resumes) != 1 {
		t.Fatalf("resume count = %d, want 1", len(resumes))
	}
	if resumes[0].Type != "resume" {
		t.Fatalf("resume type = %q, want resume", resumes[0].Type)
	}
	if resumes[0].Surface != want.Surface {
		t.Fatalf("resume surface = %q, want %q", resumes[0].Surface, want.Surface)
	}
	if resumes[0].Principal != want {
		t.Fatalf("resume principal = %+v, want %+v", resumes[0].Principal, want)
	}
}

func TestLoadSessionSkipsResumeLines(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSession(dir, identity.Principal{UserID: "12345", ProjectID: "elnath", Surface: "telegram"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := s.AppendMessage(llm.NewUserMessage("hello before resume")); err != nil {
		t.Fatalf("AppendMessage user: %v", err)
	}
	if err := s.RecordResume(identity.Principal{UserID: "stello@host", ProjectID: "elnath", Surface: "cli"}); err != nil {
		t.Fatalf("RecordResume: %v", err)
	}
	if err := s.AppendMessage(llm.NewAssistantMessage("hello after resume")); err != nil {
		t.Fatalf("AppendMessage assistant: %v", err)
	}

	loaded, err := LoadSession(dir, s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("loaded messages = %d, want 2", len(loaded.Messages))
	}
	if got := loaded.Messages[0].Text(); got != "hello before resume" {
		t.Fatalf("first message = %q, want hello before resume", got)
	}
	if got := loaded.Messages[1].Text(); got != "hello after resume" {
		t.Fatalf("second message = %q, want hello after resume", got)
	}
	if got := sessionMessageLineCount(t, filepath.Join(dir, "sessions", s.ID+".jsonl")); got != 3 {
		t.Fatalf("session file non-header lines = %d, want 3 including resume metadata", got)
	}
}

func TestSessionNewPersistsPrincipal(t *testing.T) {
	dir := t.TempDir()
	want := identity.Principal{UserID: "stello", ProjectID: "elnath", Surface: "cli"}

	s, err := NewSession(dir, want)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if s.Principal != want {
		t.Fatalf("session principal = %+v, want %+v", s.Principal, want)
	}

	loaded, err := LoadSession(dir, s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if loaded.Principal != want {
		t.Fatalf("loaded principal = %+v, want %+v", loaded.Principal, want)
	}
}

func TestLoadSessionLegacyHeaderGetsDefaultPrincipal(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	hdr, err := json.Marshal(legacySessionHeader{
		ID:        "legacy-sess",
		CreatedAt: time.Now().UTC(),
		Version:   1,
	})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	msg, err := json.Marshal(llm.NewUserMessage("hello from legacy"))
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

	loaded, err := LoadSession(dir, "legacy-sess")
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if loaded.Principal != identity.LegacyPrincipal() {
		t.Fatalf("loaded legacy principal = %+v, want %+v", loaded.Principal, identity.LegacyPrincipal())
	}
}

func assertELN070(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected ELN-070 error, got nil")
	}
	var ufe *userfacingerr.UserFacingError
	if !errors.As(err, &ufe) {
		t.Fatalf("expected UserFacingError, got %T: %v", err, err)
	}
	if ufe.Code() != userfacingerr.ELN070 {
		t.Fatalf("expected code %q, got %q (err: %v)", userfacingerr.ELN070, ufe.Code(), err)
	}
}

func TestLoadSessionCorruptEmitsELN070(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	corruptCases := []struct {
		name    string
		content string
	}{
		{name: "empty file", content: ""},
		{name: "malformed header json", content: "{not valid json\n"},
		{name: "valid header then malformed message", content: `{"id":"sess-1","version":1}` + "\n" + "not-json\n"},
	}

	for _, tc := range corruptCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id := "corrupt-" + strings.ReplaceAll(tc.name, " ", "-")
			path := sessionPath(dir, id)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			_, err := LoadSession(dir, id)
			assertELN070(t, err)
		})
	}
}

func TestLoadSessionMissingFileNotELN070(t *testing.T) {
	t.Parallel()

	_, err := LoadSession(t.TempDir(), "nonexistent-sess")
	if err == nil {
		t.Fatal("expected error loading missing session")
	}
	var ufe *userfacingerr.UserFacingError
	if errors.As(err, &ufe) && ufe.Code() == userfacingerr.ELN070 {
		t.Fatalf("missing file must not emit ELN-070 (corrupt), got: %v", err)
	}
}

func TestReadSessionHeaderCorruptEmitsELN070(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "broken.jsonl")
	if err := os.WriteFile(path, []byte("{not valid\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := ReadSessionHeader(path)
	assertELN070(t, err)
}

// TestSession_AppendMessage_PreservesSourceInJSONL (Phase L1.2 / FU-SessionPersistSource)
// closes the L1.1 ship gap: Message.Source was added as a persist-only
// field with a dedicated MarshalPersist, but AppendMessage still marshals
// via the default json.Marshal (the LLM-wire MarshalJSON that intentionally
// drops Source). So pre-L1.2 chat/task writes land Source="" on disk even
// when the caller sets it. The load-side sanitiser L1.3 is about to depend
// on Source being present, so this round-trip has to hold before the chat
// write-side commits a "chat" tag.
func TestSession_AppendMessage_PreservesSourceInJSONL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	userMsg := llm.NewUserMessage("hello")
	userMsg.Source = llm.SourceChat
	asstMsg := llm.NewAssistantMessage("world")
	asstMsg.Source = llm.SourceChat
	for _, m := range []llm.Message{userMsg, asstMsg} {
		if err := s.AppendMessage(m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	loaded, err := LoadSession(dir, s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("loaded messages = %d, want 2", len(loaded.Messages))
	}
	for i, got := range loaded.Messages {
		if got.Source != llm.SourceChat {
			t.Errorf("loaded.Messages[%d].Source = %q, want %q (persist should use MarshalPersist, not MarshalJSON)", i, got.Source, llm.SourceChat)
		}
	}
}

// TestSession_AppendMessage_DefaultsEmptySourceToTask (Phase L1.4 /
// FU-TaskSourceStamp) supersedes the pre-L1.4 LegacySourceStaysEmpty
// guard. The L1.1 backward-compat contract only covered READ paths —
// sessions written before L1.1 continue to load back with Source="".
// On the WRITE path, Phase L1.4 closes the universal-message-schema
// loop: every task/team message that funnels through
// Session.AppendMessage now lands with Source=SourceTask when the
// caller didn't specify one. Chat callers (L1.2) already stamp
// SourceChat before reaching this boundary, so the default only
// catches task-origin writers that hadn't been sprinkled individually.
// This single invariant is what the load-side sanitiser L1.3 relies
// on: any non-chat Source on disk gets its tool blocks stripped.
func TestSession_AppendMessage_DefaultsEmptySourceToTask(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := s.AppendMessage(llm.NewUserMessage("plain")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	loaded, err := LoadSession(dir, s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("loaded messages = %d, want 1", len(loaded.Messages))
	}
	if got := loaded.Messages[0].Source; got != llm.SourceTask {
		t.Errorf("loaded.Messages[0].Source = %q, want %q (L1.4 write-side default for empty Source)", got, llm.SourceTask)
	}
	// In-memory snapshot must match the persisted shape — otherwise
	// downstream consumers (sanitize, auditors) would see a different
	// Source than what LoadSession returns.
	if got := s.Messages[0].Source; got != llm.SourceTask {
		t.Errorf("in-memory s.Messages[0].Source = %q, want %q (stamp must be visible pre-reload)", got, llm.SourceTask)
	}
}

// TestSession_AppendMessage_PreservesExplicitSource (Phase L1.4) pins
// the non-override invariant. Callers that set Source to SourceChat
// (L1.2 chat write-side) or an explicit SourceTask must see that
// value preserved on disk and in memory. If the write-side default
// ever started overriding non-empty Source, chat history would
// collapse into task-origin and the L1.3 sanitiser would strip
// chat-owned tool blocks again — the exact regression L1.2 + L1.3
// just closed.
func TestSession_AppendMessage_PreservesExplicitSource(t *testing.T) {
	t.Parallel()
	for _, want := range []string{llm.SourceChat, llm.SourceTask} {
		want := want
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			s, err := NewSession(dir)
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			m := llm.NewUserMessage("hi")
			m.Source = want
			if err := s.AppendMessage(m); err != nil {
				t.Fatalf("AppendMessage: %v", err)
			}
			loaded, err := LoadSession(dir, s.ID)
			if err != nil {
				t.Fatalf("LoadSession: %v", err)
			}
			if len(loaded.Messages) != 1 {
				t.Fatalf("loaded messages = %d, want 1", len(loaded.Messages))
			}
			if got := loaded.Messages[0].Source; got != want {
				t.Errorf("loaded.Messages[0].Source = %q, want %q (explicit caller source must not be overridden)", got, want)
			}
		})
	}
}
