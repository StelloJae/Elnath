package wiki

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/stello/elnath/internal/llm"
)

// KnowledgeExtractor analyzes conversation messages via an LLM and creates
// structured entity/concept wiki pages (Karpathy-style knowledge extraction).
type KnowledgeExtractor struct {
	store    *Store
	provider llm.Provider
	logger   *slog.Logger
}

// NewKnowledgeExtractor creates a KnowledgeExtractor.
// logger may be nil; slog.Default() is used in that case.
func NewKnowledgeExtractor(store *Store, provider llm.Provider, logger *slog.Logger) *KnowledgeExtractor {
	if logger == nil {
		logger = slog.Default()
	}
	return &KnowledgeExtractor{store: store, provider: provider, logger: logger}
}

// extractionResult is the expected JSON structure from the LLM.
type extractionResult struct {
	Entities []extractedEntity  `json:"entities"`
	Concepts []extractedConcept `json:"concepts"`
}

type extractedEntity struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Summary string   `json:"summary"`
	Facts   []string `json:"facts"`
}

type extractedConcept struct {
	Name    string   `json:"name"`
	Summary string   `json:"summary"`
	Related []string `json:"related"`
}

const extractionPrompt = `Analyze the following conversation and extract notable entities and concepts.

Return ONLY valid JSON (no markdown fencing) in this exact format:
{
  "entities": [
    {"name": "EntityName", "type": "project|person|tool|organization|language", "summary": "one-line description", "facts": ["fact1", "fact2"]}
  ],
  "concepts": [
    {"name": "ConceptName", "summary": "one-line description", "related": ["entity1", "concept2"]}
  ]
}

Rules:
- Only extract entities and concepts that are substantive and worth remembering.
- Skip trivial greetings, filler, and meta-conversation.
- If nothing notable is discussed, return {"entities":[],"concepts":[]}.
- Keep summaries concise (one sentence).
- Keep facts atomic (one idea per fact).

Conversation:
`

// ExtractFromConversation analyzes conversation messages and creates entity/concept wiki pages.
// Called once at session end (not per-turn). Cost cap: max 1000 output tokens per extraction.
func (ke *KnowledgeExtractor) ExtractFromConversation(ctx context.Context, sessionID string, messages []llm.Message) error {
	if len(messages) == 0 {
		return nil
	}

	var sb strings.Builder
	for _, m := range messages {
		sb.WriteString(m.Role)
		sb.WriteString(": ")
		sb.WriteString(m.TextContent())
		sb.WriteByte('\n')
	}
	transcript := sb.String()

	prompt := extractionPrompt + transcript

	resp, err := ke.provider.Chat(ctx, llm.ChatRequest{
		Messages:  []llm.Message{llm.NewUserMessage(prompt)},
		MaxTokens: 1000,
	})
	if err != nil {
		return fmt.Errorf("wiki extract: llm chat: %w", err)
	}

	result, err := parseExtractionResult(resp.Content)
	if err != nil {
		ke.logger.Warn("wiki extract: failed to parse LLM response", "error", err, "session", sessionID)
		return nil
	}

	for _, entity := range result.Entities {
		if entity.Name == "" {
			continue
		}
		if err := ke.upsertEntity(entity); err != nil {
			ke.logger.Warn("wiki extract: failed to upsert entity", "name", entity.Name, "error", err)
		}
	}

	for _, concept := range result.Concepts {
		if concept.Name == "" {
			continue
		}
		if err := ke.upsertConcept(concept); err != nil {
			ke.logger.Warn("wiki extract: failed to upsert concept", "name", concept.Name, "error", err)
		}
	}

	return nil
}

// parseExtractionResult parses the LLM JSON response into an extractionResult.
func parseExtractionResult(raw string) (*extractionResult, error) {
	cleaned := strings.TrimSpace(raw)
	// Strip markdown code fences if present.
	if strings.HasPrefix(cleaned, "```") {
		lines := strings.Split(cleaned, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
		}
		if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			lines = lines[:len(lines)-1]
		}
		cleaned = strings.Join(lines, "\n")
	}

	cleaned = extractFirstJSONObject(cleaned)

	var result extractionResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("parse extraction json: %w", err)
	}
	return &result, nil
}

// extractFirstJSONObject finds the first balanced {...} in the input.
// This handles cases where the LLM returns multiple concatenated JSON objects.
func extractFirstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start == -1 {
		return s
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		c := s[i]
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}

// upsertEntity creates or appends facts to an entity wiki page.
func (ke *KnowledgeExtractor) upsertEntity(entity extractedEntity) error {
	slug := slugify(entity.Name)
	pagePath := "entities/" + slug + ".md"

	existing, err := ke.store.Read(pagePath)
	if err == nil {
		return ke.appendEntityFacts(existing, entity)
	}

	var factsBuilder strings.Builder
	for _, f := range entity.Facts {
		factsBuilder.WriteString("- ")
		factsBuilder.WriteString(f)
		factsBuilder.WriteByte('\n')
	}

	content := fmt.Sprintf("## %s\n\n**Type:** %s\n\n%s\n\n### Facts\n\n%s",
		entity.Name, entity.Type, entity.Summary, factsBuilder.String())

	page := &Page{
		Path:       pagePath,
		Title:      entity.Name,
		Type:       PageTypeEntity,
		Content:    content,
		Tags:       []string{entity.Type},
		Confidence: "medium",
	}

	return ke.store.Create(page)
}

// appendEntityFacts adds new facts to an existing entity page.
func (ke *KnowledgeExtractor) appendEntityFacts(existing *Page, entity extractedEntity) error {
	if len(entity.Facts) == 0 {
		return nil
	}

	var newFacts strings.Builder
	for _, f := range entity.Facts {
		if !strings.Contains(existing.Content, f) {
			newFacts.WriteString("- ")
			newFacts.WriteString(f)
			newFacts.WriteByte('\n')
		}
	}

	if newFacts.Len() == 0 {
		return nil
	}

	existing.Content = strings.TrimRight(existing.Content, "\n") + "\n" + newFacts.String()
	return ke.store.Update(existing)
}

// upsertConcept creates or updates a concept wiki page.
func (ke *KnowledgeExtractor) upsertConcept(concept extractedConcept) error {
	slug := slugify(concept.Name)
	pagePath := "concepts/" + slug + ".md"

	existing, err := ke.store.Read(pagePath)
	if err == nil {
		return ke.appendConceptRelated(existing, concept)
	}

	var relatedBuilder strings.Builder
	for _, r := range concept.Related {
		relatedBuilder.WriteString("- ")
		relatedBuilder.WriteString(r)
		relatedBuilder.WriteByte('\n')
	}

	content := fmt.Sprintf("## %s\n\n%s\n\n### Related\n\n%s",
		concept.Name, concept.Summary, relatedBuilder.String())

	page := &Page{
		Path:       pagePath,
		Title:      concept.Name,
		Type:       PageTypeConcept,
		Content:    content,
		Tags:       []string{"concept"},
		Confidence: "medium",
	}

	return ke.store.Create(page)
}

// appendConceptRelated adds new related items to an existing concept page.
func (ke *KnowledgeExtractor) appendConceptRelated(existing *Page, concept extractedConcept) error {
	if len(concept.Related) == 0 {
		return nil
	}

	var newRelated strings.Builder
	for _, r := range concept.Related {
		if !strings.Contains(existing.Content, r) {
			newRelated.WriteString("- ")
			newRelated.WriteString(r)
			newRelated.WriteByte('\n')
		}
	}

	if newRelated.Len() == 0 {
		return nil
	}

	existing.Content = strings.TrimRight(existing.Content, "\n") + "\n" + newRelated.String()
	return ke.store.Update(existing)
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// slugify converts a name to a URL-friendly slug: lowercase, spaces to hyphens,
// non-alphanumeric characters removed.
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = slugRe.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	return s
}
