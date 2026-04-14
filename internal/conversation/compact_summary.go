package conversation

import (
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
)

const (
	compactLessonMessageLimit = 10
	compactLessonTextLimit    = 200
)

func CompactLessonSummary(messages []llm.Message, toolStats []agent.ToolStat, redact func(string) string) (text string, lastLine int) {
	var b strings.Builder
	b.WriteString("Tool stats:\n")
	if len(toolStats) == 0 {
		b.WriteString("  (none)\n")
	} else {
		for _, stat := range toolStats {
			line := fmt.Sprintf("  %s: %d calls / %d errors / %s\n", redactString(redact, stat.Name), stat.Calls, stat.Errors, stat.TotalTime)
			b.WriteString(redactString(redact, line))
		}
	}

	b.WriteString("\nLast 10 messages:\n")
	recent := messages
	if len(recent) > compactLessonMessageLimit {
		recent = recent[len(recent)-compactLessonMessageLimit:]
	}
	if len(recent) == 0 {
		b.WriteString("  (none)")
		return b.String(), 0
	}
	for _, msg := range recent {
		label := messageSummaryLabel(msg)
		content := redactString(redact, compactSummaryText(msg))
		content = truncateCompactSummary(content, compactLessonTextLimit)
		fmt.Fprintf(&b, "  [%s] %s\n", label, content)
	}
	return strings.TrimSuffix(b.String(), "\n"), len(messages)
}

func messageSummaryLabel(msg llm.Message) string {
	for _, block := range msg.Content {
		switch block.(type) {
		case llm.ToolResultBlock:
			return "tool_result"
		case llm.ToolUseBlock:
			return "tool_use"
		}
	}
	if msg.Role != "" {
		return msg.Role
	}
	return "message"
}

func compactSummaryText(msg llm.Message) string {
	parts := make([]string, 0, len(msg.Content))
	for _, block := range msg.Content {
		switch b := block.(type) {
		case llm.TextBlock:
			if text := strings.TrimSpace(b.Text); text != "" {
				parts = append(parts, text)
			}
		case llm.ToolUseBlock:
			if len(b.Input) > 0 {
				parts = append(parts, fmt.Sprintf("%s %s", b.Name, strings.TrimSpace(string(b.Input))))
			} else {
				parts = append(parts, b.Name)
			}
		case llm.ToolResultBlock:
			content := strings.TrimSpace(b.Content)
			if b.IsError {
				parts = append(parts, "error: "+content)
			} else {
				parts = append(parts, content)
			}
		case llm.ThinkingBlock:
			if thinking := strings.TrimSpace(b.Thinking); thinking != "" {
				parts = append(parts, thinking)
			}
		case llm.ImageBlock:
			parts = append(parts, "[image]")
		}
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, " | ")
}

func truncateCompactSummary(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" || n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 3 {
		return string(runes[:n])
	}
	return string(runes[:n-3]) + "..."
}

func redactString(redact func(string) string, s string) string {
	if redact == nil {
		return s
	}
	return redact(s)
}
