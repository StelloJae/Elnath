package conversation

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
)

func TestCompactLessonSummaryIncludesToolStatsAndMessages(t *testing.T) {
	t.Parallel()

	msgs := []llm.Message{
		llm.NewUserMessage("hello"),
		llm.NewAssistantMessage("working on it"),
	}
	stats := []agent.ToolStat{{Name: "bash", Calls: 2, Errors: 1, TotalTime: 1500 * time.Millisecond}}
	summary, lastLine := CompactLessonSummary(msgs, stats, nil)
	if !strings.Contains(summary, "Tool stats:") {
		t.Fatalf("summary = %q, want tool stats header", summary)
	}
	if !strings.Contains(summary, "bash") || !strings.Contains(summary, "2 calls") {
		t.Fatalf("summary = %q, want bash tool stat line", summary)
	}
	if !strings.Contains(summary, "[user] hello") || !strings.Contains(summary, "[assistant] working on it") {
		t.Fatalf("summary = %q, want user and assistant messages", summary)
	}
	if lastLine != len(msgs) {
		t.Fatalf("lastLine = %d, want %d", lastLine, len(msgs))
	}
}

func TestCompactLessonSummaryUsesRecentMessagesOnly(t *testing.T) {
	t.Parallel()

	msgs := make([]llm.Message, 0, 12)
	for i := 1; i <= 12; i++ {
		msgs = append(msgs, llm.NewUserMessage(fmt.Sprintf("msg-%02d", i)))
	}
	summary, _ := CompactLessonSummary(msgs, nil, nil)
	if strings.Contains(summary, "msg-01") || strings.Contains(summary, "msg-02") {
		t.Fatalf("summary = %q, want oldest messages pruned", summary)
	}
	if !strings.Contains(summary, "msg-03") || !strings.Contains(summary, "msg-12") {
		t.Fatalf("summary = %q, want newest 10 messages", summary)
	}
}

func TestCompactLessonSummaryTruncatesLongContent(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 240)
	summary, _ := CompactLessonSummary([]llm.Message{llm.NewUserMessage(long)}, nil, nil)
	if strings.Contains(summary, long) {
		t.Fatalf("summary = %q, want truncated content", summary)
	}
	if !strings.Contains(summary, strings.Repeat("x", 100)) {
		t.Fatalf("summary = %q, want retained prefix", summary)
	}
}

func TestCompactLessonSummaryRedactsSecrets(t *testing.T) {
	t.Parallel()

	redact := func(s string) string {
		return strings.ReplaceAll(s, "AKIASECRET", "[REDACTED]")
	}
	msgs := []llm.Message{llm.NewUserMessage("token AKIASECRET should disappear")}
	stats := []agent.ToolStat{{Name: "AKIASECRET", Calls: 1, Errors: 0, TotalTime: time.Second}}
	summary, _ := CompactLessonSummary(msgs, stats, redact)
	if strings.Contains(summary, "AKIASECRET") {
		t.Fatalf("summary = %q, want secret redacted", summary)
	}
	if !strings.Contains(summary, "[REDACTED]") {
		t.Fatalf("summary = %q, want redacted placeholder", summary)
	}
}

func TestCompactLessonSummaryHandlesEmptyToolStats(t *testing.T) {
	t.Parallel()

	summary, _ := CompactLessonSummary([]llm.Message{llm.NewAssistantMessage("done")}, nil, nil)
	if !strings.Contains(summary, "Tool stats:") || !strings.Contains(summary, "(none)") {
		t.Fatalf("summary = %q, want empty tool stats marker", summary)
	}
}
