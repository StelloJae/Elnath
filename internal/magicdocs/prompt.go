package magicdocs

import (
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

const systemPrompt = `You are a knowledge extraction agent for Elnath's wiki. Given a batch of agent activity events, extract wiki-worthy knowledge.

Return JSON (no markdown fences): {"pages": [...]} or {"pages": []} if nothing worth keeping.

Each page object:
{
  "action": "create" or "update",
  "path": "<type>/<slug>.md",
  "title": "Page Title",
  "type": "entity" | "concept" | "source" | "analysis" | "map",
  "content": "Markdown body (no frontmatter)",
  "confidence": "high" | "medium" | "low",
  "tags": ["tag1", "tag2"]
}

Rules:
- Only extract NOVEL knowledge: facts, insights, patterns, conclusions
- Do NOT extract: raw tool output, mechanical progress, debugging noise, trivial observations
- For "update": path must point to an existing auto-generated page
- Prefer "analysis" type for research findings, "concept" for discovered patterns
- Write content in Korean (matching the wiki's language)
- Be concise: 100-500 words per page
- Use lowercase slugs with hyphens for paths (e.g. "analyses/go-error-wrapping.md")`

func buildPrompt(req ExtractionRequest, f FilterResult, model, systemPrefix string) llm.ChatRequest {
	var sb strings.Builder
	sb.WriteString("## Signal Events (핵심)\n")
	for i, e := range f.Signal {
		sb.WriteString(fmt.Sprintf("[%d] %s: %s\n", i+1, e.EventType(), summarizeEvent(e)))
	}
	if len(f.Context) > 0 {
		sb.WriteString("\n## Context Events (맥락)\n")
		for i, e := range f.Context {
			sb.WriteString(fmt.Sprintf("[%d] %s: %s\n", i+1, e.EventType(), summarizeEvent(e)))
		}
	}

	system := systemPrompt
	if trimmed := strings.TrimSpace(systemPrefix); trimmed != "" {
		system = trimmed + "\n\n" + systemPrompt
	}

	return llm.ChatRequest{
		Model:     model,
		System:    system,
		MaxTokens: 4096,
		Messages: []llm.Message{
			llm.NewUserMessage(sb.String()),
		},
	}
}

func summarizeEvent(e event.Event) string {
	switch ev := e.(type) {
	case event.ResearchProgressEvent:
		return fmt.Sprintf("phase=%s round=%d %s", ev.Phase, ev.Round, ev.Message)
	case event.HypothesisEvent:
		return fmt.Sprintf("id=%s status=%s %q", ev.HypothesisID, ev.Status, ev.Statement)
	case event.AgentFinishEvent:
		return fmt.Sprintf("reason=%s", ev.FinishReason)
	case event.SkillExecuteEvent:
		return fmt.Sprintf("skill=%s status=%s", ev.SkillName, ev.Status)
	case event.DaemonTaskEvent:
		return fmt.Sprintf("task=%s status=%s", ev.TaskID, ev.Status)
	case event.ToolUseDoneEvent:
		return fmt.Sprintf("tool=%s id=%s", ev.Name, ev.ID)
	case event.ToolProgressEvent:
		return fmt.Sprintf("tool=%s %s", ev.ToolName, ev.Preview)
	case event.CompressionEvent:
		return fmt.Sprintf("before=%d after=%d", ev.BeforeCount, ev.AfterCount)
	case event.WorkflowProgressEvent:
		return fmt.Sprintf("intent=%s workflow=%s", ev.Intent, ev.Workflow)
	case event.UsageProgressEvent:
		return ev.Summary
	case event.SessionResumeEvent:
		return fmt.Sprintf("resumed=%s surface=%s", ev.ResumedSessionID, ev.Surface)
	case event.ClassifiedErrorEvent:
		return fmt.Sprintf("class=%s err=%v", ev.Classification, ev.Err)
	default:
		return e.EventType()
	}
}
