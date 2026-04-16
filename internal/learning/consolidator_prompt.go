package learning

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SynthesisEntry is a prior consolidation page loaded from the wiki, used as
// context so the LLM does not re-consolidate ground already covered.
type SynthesisEntry struct {
	ID      string
	Topic   string
	Summary string
	Created time.Time
}

// ConsolidationRequest collects the inputs shown to the consolidation LLM.
type ConsolidationRequest struct {
	Lessons        []Lesson
	PriorSyntheses []SynthesisEntry
	SessionContext string
}

// SynthesisItem is one consolidated insight emitted by the LLM. It supersedes
// the raw lessons referenced by SupersededLessonIDs.
type SynthesisItem struct {
	Text                string   `json:"synthesis_text"`
	TopicTags           []string `json:"topic_tags"`
	SupersededLessonIDs []string `json:"superseded_lesson_ids"`
	Confidence          string   `json:"confidence"`
}

// ConsolidationOutput is the parsed LLM response.
type ConsolidationOutput struct {
	Syntheses []SynthesisItem `json:"syntheses"`
}

const consolidationSystemPrompt = `You consolidate raw lessons into durable syntheses for Elnath's learning store.
Output STRICT JSON matching the syntheses schema. No commentary, no code fences.

Rules:
- 0-N syntheses per run. Prefer emitting none over forcing weak syntheses.
- Each synthesis must supersede at least 2 raw lessons.
- synthesis_text <= 400 chars. Lead with the durable insight, not the evidence.
- superseded_lesson_ids MUST be exact IDs from the provided raw lessons. Do not invent.
- topic_tags: 1-5 short labels that categorize the insight.
- confidence: "high" only when 3+ independent lessons agree. "medium" when 2-3 agree. "low" rarely.
- Skip lessons already well-represented in prior syntheses — do not re-consolidate.

Schema (top-level):
{ "syntheses": [
    { "synthesis_text": string,
      "topic_tags": [string],
      "superseded_lesson_ids": [string],
      "confidence": "high" | "medium" | "low" } ] }`

// BuildConsolidationPrompt assembles the system + user prompts for the
// lesson-consolidation LLM call. The user prompt follows the four-phase shape
// used by Claude Code's autoDream (orient → gather → consolidate → prune),
// adapted to emit structured JSON rather than invoke file tools.
func BuildConsolidationPrompt(req ConsolidationRequest) (string, string) {
	var b strings.Builder
	b.WriteString("# Dream: Lesson Consolidation\n\n")
	b.WriteString("You are performing a reflective pass over accumulated raw lessons to synthesize durable knowledge.\n\n")
	b.WriteString("---\n\n")

	b.WriteString("## Phase 1 — Orient\n\n")
	fmt.Fprintf(&b, "Prior syntheses (%d):\n", len(req.PriorSyntheses))
	if len(req.PriorSyntheses) == 0 {
		b.WriteString("(none)\n\n")
	} else {
		for _, s := range req.PriorSyntheses {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", s.ID, s.Topic, truncate(s.Summary, 120))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "Recent raw lessons (%d):\n", len(req.Lessons))
	if len(req.Lessons) == 0 {
		b.WriteString("(none)\n\n")
	} else {
		for _, l := range req.Lessons {
			topic := l.Topic
			if topic == "" {
				topic = "(no topic)"
			}
			fmt.Fprintf(&b, "- [%s] %s | conf=%s | %s\n", l.ID, truncate(topic, 80), l.Confidence, truncate(l.Text, 160))
		}
		b.WriteString("\n")
	}

	ctx := strings.TrimSpace(req.SessionContext)
	if ctx == "" {
		ctx = "(none)"
	}
	fmt.Fprintf(&b, "Session context:\n%s\n\n", ctx)

	b.WriteString("## Phase 2 — Gather\n\n")
	b.WriteString("Group the raw lessons by semantic topic — two lessons with different wording but the same underlying insight belong together. Literal-prefix matching is not sufficient.\n\n")

	b.WriteString("## Phase 3 — Consolidate\n\n")
	b.WriteString("For each group with 2+ lessons expressing the same insight:\n")
	b.WriteString("- Write one synthesis capturing the shared takeaway\n")
	b.WriteString("- List the superseded lesson IDs from the list above\n")
	b.WriteString("- Assign confidence based on how many independent lessons support it\n\n")
	b.WriteString("Skip groups already covered by a prior synthesis.\n\n")

	b.WriteString("## Phase 4 — Prune\n\n")
	b.WriteString("Return JSON only. Each synthesis's superseded_lesson_ids names the raw lessons it supersedes.\n")

	return consolidationSystemPrompt, b.String()
}

// ParseConsolidationResponse parses the LLM response and drops any item that
// fails validation: missing or empty fields, invalid confidence, fewer than
// two superseded IDs, or an ID not present in validLessonIDs (hallucination
// guard). Only malformed JSON fails the whole response.
func ParseConsolidationResponse(content string, validLessonIDs map[string]bool) (ConsolidationOutput, error) {
	var out ConsolidationOutput
	trimmed := strings.TrimSpace(content)
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return ConsolidationOutput{}, fmt.Errorf("parse consolidation response: %w", err)
	}

	filtered := make([]SynthesisItem, 0, len(out.Syntheses))
	for _, item := range out.Syntheses {
		clean, ok := validateSynthesisItem(item, validLessonIDs)
		if !ok {
			continue
		}
		filtered = append(filtered, clean)
	}
	out.Syntheses = filtered
	return out, nil
}

func validateSynthesisItem(item SynthesisItem, validLessonIDs map[string]bool) (SynthesisItem, bool) {
	text := strings.TrimSpace(item.Text)
	if text == "" {
		return SynthesisItem{}, false
	}

	tags := make([]string, 0, len(item.TopicTags))
	for _, t := range item.TopicTags {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	if len(tags) == 0 {
		return SynthesisItem{}, false
	}

	confidence := normalizeConfidence(item.Confidence)
	if confidence == "" {
		return SynthesisItem{}, false
	}

	seen := make(map[string]bool, len(item.SupersededLessonIDs))
	ids := make([]string, 0, len(item.SupersededLessonIDs))
	for _, id := range item.SupersededLessonIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if !validLessonIDs[id] {
			return SynthesisItem{}, false
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) < 2 {
		return SynthesisItem{}, false
	}

	return SynthesisItem{
		Text:                text,
		TopicTags:           tags,
		SupersededLessonIDs: ids,
		Confidence:          confidence,
	}, true
}
