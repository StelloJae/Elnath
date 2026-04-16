package conversation

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/stello/elnath/internal/llm"
)

const (
	compressionSummaryMaxTokens = 512
	summaryReturnMessage        = "Return only the requested summary."

	structuredSummaryConversationPlaceholder = "<INSERT CONVERSATION HERE>"
	structuredSummaryExistingPlaceholder     = "<INSERT EXISTING SUMMARY HERE>"
	structuredSummaryNewMessagesPlaceholder  = "<INSERT NEW MESSAGES HERE>"

	structuredSummaryTemplate = `# Session Summary

## 1. User goal
<primary objective the user is working toward, one paragraph>

## 2. Completed steps
<bullet list of concrete actions taken, in chronological order; keep every entry below 25 words>

## 3. Current focus
<what the agent is actively working on right now, one paragraph>

## 4. Files touched
<list "path - action (read/write/edit)" entries; do not include ephemeral tool_result markers>

## 5. Outstanding TODOs
<bullet list of work items not yet started; cross-reference user goal>

## 6. Blockers / unresolved
<list known blockers with symptoms; empty list allowed>

## 7. Key decisions
<bullet list of decisions with one-line rationale each>

## 8. Open questions
<questions the agent has for the user, or ambiguities requiring clarification>

## 9. Next action
<single most specific next step, expressed as an imperative sentence>`

	structuredSummaryPromptNewSession = `You are compressing a conversation. Produce a structured summary using this exact template:

` + structuredSummaryTemplate + `

Rules:
- Fill every section. Write "(none)" if a section has no content yet.
- Keep total output under 2000 tokens.
- Do not echo raw tool outputs or code blocks from the conversation.
- Section 9 must be an imperative sentence.

Conversation:
` + structuredSummaryConversationPlaceholder

	structuredSummaryPromptIterative = `You are updating a structured conversation summary. Merge the new messages into the existing summary.

Existing summary:
` + structuredSummaryExistingPlaceholder + `

New messages since last compression:
` + structuredSummaryNewMessagesPlaceholder + `

Rules:
- Use the same 9-section template as the existing summary.
- Preserve section 1 (user goal) unless the user has explicitly pivoted.
- Add to section 2 (completed steps) rather than rewriting prior entries.
- Move items from section 5 (Outstanding TODOs) to section 2 when they finish.
- Keep total output under 2000 tokens.
- Do not echo raw tool outputs or code blocks.
- Section 9 must be an imperative sentence.

Output only the updated summary, no preamble.`

	legacyUnstructuredSummaryPrompt = `Summarize the following conversation history concisely.
Preserve key decisions, facts, and context needed to continue the conversation.
Output only the summary, no preamble.`
)

var (
	structuredSummarySectionPattern = regexp.MustCompile(`(?m)^\s*##\s*([1-9])\.\s+.+$`)
	structuredSummaryHeadingPattern = regexp.MustCompile(`(?m)^\s*##\s+.+$`)
)

// parseStructuredSummary returns the structured summary body if the content matches
// the expected 9-section shape.
func parseStructuredSummary(content string) (body string, ok bool) {
	normalized := normalizeStructuredSummary(content)
	if !strings.HasPrefix(normalized, "# Session Summary") {
		return "", false
	}

	body = normalized
	if len(structuredSummaryHeadingPattern.FindAllString(body, -1)) != 9 {
		return "", false
	}

	matches := structuredSummarySectionPattern.FindAllStringSubmatch(body, -1)
	if len(matches) != 9 {
		return "", false
	}
	for i := 0; i < 9; i++ {
		sectionNumber, err := strconv.Atoi(matches[i][1])
		if err != nil || sectionNumber != i+1 {
			return "", false
		}
	}

	return body, true
}

// isStructuredSummaryMessage returns true when msg is an assistant structured summary.
func isStructuredSummaryMessage(msg llm.Message) bool {
	if msg.Role != llm.RoleAssistant {
		return false
	}
	_, ok := parseStructuredSummary(msg.Text())
	return ok
}

func normalizeStructuredSummary(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.TrimSpace(content)
}

func latestStructuredSummary(messages []llm.Message) (summary string, index int, ok bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleAssistant {
			continue
		}
		body, matched := parseStructuredSummary(messages[i].Text())
		if matched {
			return body, i, true
		}
	}
	return "", -1, false
}

func compressionTranscript(messages []llm.Message) string {
	if len(messages) == 0 {
		return "(none)"
	}

	var sb strings.Builder
	for _, msg := range messages {
		fmt.Fprintf(&sb, "%s: %s\n", messageSummaryLabel(msg), compactSummaryText(msg))
	}
	return strings.TrimSpace(sb.String())
}

func newSessionSummaryRequest(messages []llm.Message) llm.ChatRequest {
	prompt := strings.Replace(structuredSummaryPromptNewSession, structuredSummaryConversationPlaceholder, compressionTranscript(messages), 1)
	return llm.ChatRequest{
		System:    prompt,
		Messages:  []llm.Message{llm.NewUserMessage(summaryReturnMessage)},
		MaxTokens: compressionSummaryMaxTokens,
	}
}

func iterativeSummaryRequest(existingSummary string, newMessages []llm.Message) llm.ChatRequest {
	prompt := strings.Replace(structuredSummaryPromptIterative, structuredSummaryExistingPlaceholder, existingSummary, 1)
	prompt = strings.Replace(prompt, structuredSummaryNewMessagesPlaceholder, compressionTranscript(newMessages), 1)
	return llm.ChatRequest{
		System:    prompt,
		Messages:  []llm.Message{llm.NewUserMessage(summaryReturnMessage)},
		MaxTokens: compressionSummaryMaxTokens,
	}
}

func legacySummaryRequest(messages []llm.Message) llm.ChatRequest {
	return llm.ChatRequest{
		System:    legacyUnstructuredSummaryPrompt,
		Messages:  []llm.Message{llm.NewUserMessage(compressionTranscript(messages))},
		MaxTokens: compressionSummaryMaxTokens,
	}
}
