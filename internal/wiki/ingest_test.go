package wiki

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/llm"
)

// mockLLMProvider is a minimal Provider that returns a fixed summary.
type mockLLMProvider struct{}

func (m *mockLLMProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: "Summary bullet points"}, nil
}

func (m *mockLLMProvider) Stream(_ context.Context, _ llm.ChatRequest, _ func(llm.StreamEvent)) error {
	return nil
}

func (m *mockLLMProvider) Name() string            { return "mock" }
func (m *mockLLMProvider) Models() []llm.ModelInfo { return nil }

// gitInDir runs a git command inside dir and fails the test on error.
func gitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	fullArgs := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", fullArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestParseGitLog covers the pure parsing logic.
func TestParseGitLog(t *testing.T) {
	t.Run("normal output with multiple entries", func(t *testing.T) {
		raw := "abc1234|feat: add thing|Alice|2024-01-15T10:00:00Z\n" +
			"def5678|fix: broken stuff|Bob|2024-02-20T12:30:00Z\n"

		commits, err := parseGitLog(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(commits) != 2 {
			t.Fatalf("want 2 commits, got %d", len(commits))
		}

		c0 := commits[0]
		if c0.Hash != "abc1234" {
			t.Errorf("hash: want %q, got %q", "abc1234", c0.Hash)
		}
		if c0.Subject != "feat: add thing" {
			t.Errorf("subject: want %q, got %q", "feat: add thing", c0.Subject)
		}
		if c0.Author != "Alice" {
			t.Errorf("author: want %q, got %q", "Alice", c0.Author)
		}
		wantTime, _ := time.Parse(time.RFC3339, "2024-01-15T10:00:00Z")
		if !c0.Date.Equal(wantTime) {
			t.Errorf("date: want %v, got %v", wantTime, c0.Date)
		}
		if commits[1].Hash != "def5678" {
			t.Errorf("second commit hash: want %q, got %q", "def5678", commits[1].Hash)
		}
	})

	t.Run("empty input returns empty slice", func(t *testing.T) {
		commits, err := parseGitLog("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(commits) != 0 {
			t.Errorf("want 0 commits, got %d", len(commits))
		}
	})

	t.Run("malformed lines with fewer than 4 fields are skipped", func(t *testing.T) {
		raw := "onlyone\n" +
			"two|fields\n" +
			"three|fields|here\n" +
			"abc1234|real commit|Author|2024-03-01T00:00:00Z\n"

		commits, err := parseGitLog(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(commits) != 1 {
			t.Fatalf("want 1 commit (malformed lines skipped), got %d", len(commits))
		}
		if commits[0].Hash != "abc1234" {
			t.Errorf("hash: want %q, got %q", "abc1234", commits[0].Hash)
		}
	})

	t.Run("invalid date falls back to approximately now", func(t *testing.T) {
		before := time.Now().Add(-time.Second)
		raw := "abc1234|some commit|Author|not-a-date\n"

		commits, err := parseGitLog(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(commits) != 1 {
			t.Fatalf("want 1 commit, got %d", len(commits))
		}
		after := time.Now().Add(time.Second)
		if commits[0].Date.Before(before) || commits[0].Date.After(after) {
			t.Errorf("fallback date %v not within expected range [%v, %v]", commits[0].Date, before, after)
		}
	})
}

// TestIngestGitLog creates a real git repo and exercises IngestGitLog end-to-end.
func TestIngestGitLog(t *testing.T) {
	repoDir := t.TempDir()

	gitInDir(t, repoDir, "init")
	gitInDir(t, repoDir, "config", "user.email", "test@example.com")
	gitInDir(t, repoDir, "config", "user.name", "Test User")

	testFile := filepath.Join(repoDir, "readme.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	gitInDir(t, repoDir, "add", ".")
	gitInDir(t, repoDir, "commit", "-m", "initial commit")

	store := newTestStore(t)
	ing := NewIngester(store, nil)

	ctx := context.Background()
	since := time.Now().Add(-24 * time.Hour)

	if err := ing.IngestGitLog(ctx, repoDir, since); err != nil {
		t.Fatalf("IngestGitLog: %v", err)
	}

	pages, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("expected at least one page created, got none")
	}

	repoName := filepath.Base(repoDir)
	for _, p := range pages {
		wantPrefix := "sources/git/" + repoName + "/"
		if !strings.HasPrefix(p.Path, wantPrefix) {
			t.Errorf("unexpected page path %q, want prefix %q", p.Path, wantPrefix)
			continue
		}
		base := strings.TrimPrefix(p.Path, wantPrefix)
		// Expect exactly 8 hex chars + ".md"
		if len(base) != 8+len(".md") {
			t.Errorf("page base name %q: want 8-char hash + .md", base)
		}
		if got := p.PageSource(); got != SourceIngest {
			t.Errorf("page %q source = %q, want %q", p.Path, got, SourceIngest)
		}
	}
}

// TestIngestSession verifies structured session ingest without an LLM provider.
func TestIngestSession(t *testing.T) {
	store := newTestStore(t)
	ing := NewIngester(store, nil)

	sessionID := "sess-001"
	startedAt := time.Date(2026, time.April, 11, 9, 30, 0, 0, time.UTC)
	event := IngestEvent{
		SessionID: sessionID,
		Messages: []llm.Message{
			llm.NewUserMessage("Hello, how are you?"),
			llm.NewUserMessage("Tell me about Go."),
		},
		Reason:    "interactive_session",
		Principal: "cli:stello",
		StartedAt: startedAt,
		Duration:  2*time.Minute + 5*time.Second,
	}

	ctx := context.Background()
	if err := ing.IngestSession(ctx, event); err != nil {
		t.Fatalf("IngestSession: %v", err)
	}

	wantPath := "sessions/" + sessionID + ".md"
	page, err := store.Read(wantPath)
	if err != nil {
		t.Fatalf("store.Read(%q): %v", wantPath, err)
	}

	if !strings.Contains(page.Content, "## Session Metadata") {
		t.Fatalf("expected metadata section, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "**Session ID**: "+sessionID) {
		t.Fatalf("expected session ID in metadata, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "**Reason**: interactive_session") {
		t.Fatalf("expected reason in metadata, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "**Principal**: cli:stello") {
		t.Fatalf("expected principal in metadata, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "**Started**: "+startedAt.Format(time.RFC3339)) {
		t.Fatalf("expected start time in metadata, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "**Duration**: 2m5s") {
		t.Fatalf("expected duration in metadata, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "Hello, how are you?") {
		t.Errorf("first turn missing from transcript content")
	}
	if !strings.Contains(page.Content, "Tell me about Go.") {
		t.Errorf("second turn missing from transcript content")
	}
	if !strings.Contains(page.Content, "## Transcript") {
		t.Errorf("expected transcript header in content")
	}
	if strings.Contains(page.Content, "## Summary") {
		t.Errorf("unexpected ## Summary section when provider is nil")
	}
	if !slicesEqual(page.Tags, []string{"session", "interactive_session", "principal:cli:stello"}) {
		t.Fatalf("tags = %v, want session tags", page.Tags)
	}
	if got := page.PageSource(); got != SourceIngest {
		t.Errorf("session page source = %q, want %q", got, SourceIngest)
	}
}

// TestIngestSessionWithProvider verifies that a mock provider's summary is included.
func TestIngestSessionWithProvider(t *testing.T) {
	store := newTestStore(t)
	ing := NewIngester(store, &mockLLMProvider{})

	sessionID := "sess-002"
	event := IngestEvent{
		SessionID: sessionID,
		Messages: []llm.Message{
			llm.NewUserMessage("What is the capital of France?"),
		},
		Reason: "task_completed",
	}

	ctx := context.Background()
	if err := ing.IngestSession(ctx, event); err != nil {
		t.Fatalf("IngestSession: %v", err)
	}

	wantPath := "sessions/" + sessionID + ".md"
	page, err := store.Read(wantPath)
	if err != nil {
		t.Fatalf("store.Read(%q): %v", wantPath, err)
	}

	if !strings.Contains(page.Content, "## Session Metadata") {
		t.Fatalf("expected metadata section, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "Summary bullet points") {
		t.Errorf("expected mock summary in content, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "What is the capital of France?") {
		t.Errorf("transcript missing from content")
	}
	if !strings.Contains(page.Content, "## Summary") {
		t.Errorf("expected ## Summary header in content")
	}
	if !strings.Contains(page.Content, "## Transcript") {
		t.Errorf("expected ## Transcript header in content")
	}
	if !slicesEqual(page.Tags, []string{"session", "task_completed"}) {
		t.Fatalf("tags = %v, want session/task_completed", page.Tags)
	}
}

func TestIngestSessionIncludesResumeHistory(t *testing.T) {
	store := newTestStore(t)
	ing := NewIngester(store, nil)

	resumedAt := time.Date(2026, time.April, 13, 10, 30, 0, 0, time.UTC)
	event := IngestEvent{
		SessionID: "sess-resume",
		Messages: []llm.Message{
			llm.NewUserMessage("continue the telegram session from CLI"),
		},
		Reason:    "task_completed",
		Principal: "telegram:12345",
		Resumes: []ResumeRecord{
			{Surface: "cli", Principal: "cli:stello@host", At: resumedAt},
		},
	}

	if err := ing.IngestSession(context.Background(), event); err != nil {
		t.Fatalf("IngestSession: %v", err)
	}

	page, err := store.Read("sessions/sess-resume.md")
	if err != nil {
		t.Fatalf("store.Read: %v", err)
	}
	if !strings.Contains(page.Content, "**Principal**: telegram:12345") {
		t.Fatalf("expected original principal in metadata, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "**Resumed by**: cli:stello@host ("+resumedAt.Format(time.RFC3339)+")") {
		t.Fatalf("expected resume history in metadata, got:\n%s", page.Content)
	}
}

// TestIngestSessionEmpty verifies that empty turns return nil without creating a page.
func TestIngestSessionEmpty(t *testing.T) {
	store := newTestStore(t)
	ing := NewIngester(store, nil)

	ctx := context.Background()
	if err := ing.IngestSession(ctx, IngestEvent{SessionID: "sess-empty"}); err != nil {
		t.Fatalf("IngestSession with empty turns: %v", err)
	}

	pages, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(pages) != 0 {
		t.Errorf("expected no pages for empty turns, got %d", len(pages))
	}
}

func slicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestIngestFile verifies that a regular file is ingested into the store.
func TestIngestFile(t *testing.T) {
	store := newTestStore(t)
	ing := NewIngester(store, nil)

	tmpFile := filepath.Join(t.TempDir(), "notes.txt")
	fileContent := "This is some important note content."
	if err := os.WriteFile(tmpFile, []byte(fileContent), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	ctx := context.Background()
	if err := ing.IngestFile(ctx, tmpFile); err != nil {
		t.Fatalf("IngestFile: %v", err)
	}

	wantPath := "sources/files/notes.txt.md"
	page, err := store.Read(wantPath)
	if err != nil {
		t.Fatalf("store.Read(%q): %v", wantPath, err)
	}

	if !strings.Contains(page.Content, fileContent) {
		t.Errorf("file content missing from page body, got:\n%s", page.Content)
	}
	if got := page.PageSource(); got != SourceIngest {
		t.Errorf("file page source = %q, want %q", got, SourceIngest)
	}
}

// TestIngestFileNotFound verifies that IngestFile returns an error for missing files.
func TestIngestFileNotFound(t *testing.T) {
	store := newTestStore(t)
	ing := NewIngester(store, nil)

	ctx := context.Background()
	err := ing.IngestFile(ctx, "/nonexistent/path/to/file.txt")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}
