package prompt

import (
	"context"
	"strings"

	"github.com/stello/elnath/internal/llm"
)

// SessionSummaryNode renders a simple last-N summary of user messages.
type SessionSummaryNode struct {
	priority int
	maxMsgs  int
	maxChars int
}

func NewSessionSummaryNode(priority, maxMsgs, maxChars int) *SessionSummaryNode {
	if maxMsgs <= 0 {
		maxMsgs = 5
	}
	if maxChars <= 0 {
		maxChars = 800
	}
	return &SessionSummaryNode{priority: priority, maxMsgs: maxMsgs, maxChars: maxChars}
}

func (n *SessionSummaryNode) Name() string {
	return "session_summary"
}

func (n *SessionSummaryNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *SessionSummaryNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil || state == nil || len(state.Messages) == 0 {
		return "", nil
	}

	collected := make([]string, 0, n.maxMsgs)
	for i := len(state.Messages) - 1; i >= 0 && len(collected) < n.maxMsgs; i-- {
		msg := state.Messages[i]
		if msg.Role != llm.RoleUser {
			continue
		}
		text := strings.TrimSpace(msg.Text())
		if text == "" {
			continue
		}
		collected = append(collected, text)
	}
	if len(collected) == 0 {
		return "", nil
	}

	for left, right := 0, len(collected)-1; left < right; left, right = left+1, right-1 {
		collected[left], collected[right] = collected[right], collected[left]
	}

	var b strings.Builder
	b.WriteString("Recent conversation:\n")
	for i, text := range collected {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("- ")
		b.WriteString(text)
	}

	return truncateString(b.String(), n.maxChars), nil
}

func truncateString(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars])
}
