package learning

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
)

type extractorSpyProvider struct {
	response  *llm.ChatResponse
	err       error
	lastReq   llm.ChatRequest
	chatCalls int
}

func (p *extractorSpyProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.chatCalls++
	p.lastReq = req
	if p.err != nil {
		return nil, p.err
	}
	if p.response == nil {
		return &llm.ChatResponse{}, nil
	}
	return p.response, nil
}

func (p *extractorSpyProvider) Stream(context.Context, llm.ChatRequest, func(llm.StreamEvent)) error {
	return nil
}

func (p *extractorSpyProvider) Name() string { return "anthropic" }

func (p *extractorSpyProvider) Models() []llm.ModelInfo { return nil }

func TestAnthropicExtractorExtractParsesLessons(t *testing.T) {
	t.Parallel()

	provider := &extractorSpyProvider{response: &llm.ChatResponse{Content: `{"lessons":[{"topic":"ops","text":"Prefer smaller patches when tool failures repeat.","rationale":"Repeated bash failures pointed to an over-broad approach.","confidence":"high","persona_param":"caution","persona_direction":"increase","persona_magnitude":"small"}]}`}}
	extractor := NewAnthropicExtractor(provider, "")
	lessons, err := extractor.Extract(context.Background(), ExtractRequest{
		Topic:          "repo cleanup",
		Workflow:       "single",
		FinishReason:   "stop",
		Iterations:     2,
		MaxIterations:  8,
		RetryCount:     1,
		CompactSummary: "assistant fixed the test and verified it",
		ToolStats:      []AgentToolStat{{Name: "bash", Calls: 2, Errors: 1}},
		ExistingLessons: []LessonManifestEntry{{
			ID:    "abc12345",
			Topic: "ops",
			Text:  "Prior lesson",
		}},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("len(lessons) = %d, want 1", len(lessons))
	}
	lesson := lessons[0]
	if lesson.Topic != "ops" || lesson.Confidence != "high" {
		t.Fatalf("lesson = %#v, want parsed topic/confidence", lesson)
	}
	if lesson.PersonaParam != "caution" || lesson.PersonaDirection != "increase" || lesson.PersonaMagnitude != "small" {
		t.Fatalf("persona fields = %#v, want parsed values", lesson)
	}
	if lesson.Created.IsZero() {
		t.Fatal("lesson.Created = zero, want timestamp")
	}
	if provider.chatCalls != 1 {
		t.Fatalf("chatCalls = %d, want 1", provider.chatCalls)
	}
	if provider.lastReq.Model != "" {
		t.Fatalf("request model = %q, want empty (provider-default pass-through)", provider.lastReq.Model)
	}
	if !provider.lastReq.EnableCache {
		t.Fatal("EnableCache = false, want true")
	}
	if provider.lastReq.Temperature != 0 || provider.lastReq.MaxTokens != 1024 {
		t.Fatalf("request = %#v, want temperature=0 max_tokens=1024", provider.lastReq)
	}
	if len(provider.lastReq.Messages) != 1 || provider.lastReq.Messages[0].Role != llm.RoleUser {
		t.Fatalf("request messages = %#v, want one user message", provider.lastReq.Messages)
	}
	if text := provider.lastReq.Messages[0].Text(); !strings.Contains(text, "## Existing lessons") || !strings.Contains(text, "## Compact summary") {
		t.Fatalf("user prompt = %q, want manifest and summary sections", text)
	}
	if !strings.Contains(provider.lastReq.System, "Output STRICT JSON") {
		t.Fatalf("system prompt = %q, want strict JSON rule", provider.lastReq.System)
	}
}

func TestAnthropicExtractorExtractMalformedJSON(t *testing.T) {
	t.Parallel()

	provider := &extractorSpyProvider{response: &llm.ChatResponse{Content: `{"lessons":[`}}
	_, err := NewAnthropicExtractor(provider, "").Extract(context.Background(), ExtractRequest{})
	if err == nil {
		t.Fatal("Extract() error = nil, want parse error")
	}
	if !strings.Contains(err.Error(), "parse lesson response") {
		t.Fatalf("error = %v, want parse lesson response", err)
	}
}

func TestAnthropicExtractorExtractSkipsInvalidLessonEntries(t *testing.T) {
	t.Parallel()

	provider := &extractorSpyProvider{response: &llm.ChatResponse{Content: `{"lessons":[{"topic":"ops","text":"valid lesson","rationale":"valid rationale","confidence":"medium"},{"topic":"ops","text":"bad lesson","rationale":"bad rationale","confidence":"medium","persona_magnitude":"huge"}]}`}}
	lessons, err := NewAnthropicExtractor(provider, "").Extract(context.Background(), ExtractRequest{})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("len(lessons) = %d, want 1 valid lesson", len(lessons))
	}
	if lessons[0].Text != "valid lesson" {
		t.Fatalf("lesson text = %q, want valid lesson", lessons[0].Text)
	}
}

func TestAnthropicExtractorExtractSkipsMissingRequiredTopic(t *testing.T) {
	t.Parallel()

	provider := &extractorSpyProvider{response: &llm.ChatResponse{Content: `{"lessons":[{"text":"missing topic","rationale":"still invalid","confidence":"medium"}]}`}}
	lessons, err := NewAnthropicExtractor(provider, "").Extract(context.Background(), ExtractRequest{Topic: "fallback topic"})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(lessons) != 0 {
		t.Fatalf("len(lessons) = %d, want 0 for missing required topic", len(lessons))
	}
}

func TestAnthropicExtractorExtractEmptyLessons(t *testing.T) {
	t.Parallel()

	provider := &extractorSpyProvider{response: &llm.ChatResponse{Content: `{"lessons":[]}`}}
	lessons, err := NewAnthropicExtractor(provider, "").Extract(context.Background(), ExtractRequest{})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if lessons == nil {
		t.Fatal("lessons = nil, want empty slice")
	}
	if len(lessons) != 0 {
		t.Fatalf("len(lessons) = %d, want 0", len(lessons))
	}
}

func TestAnthropicExtractorExtractWrapsProviderErrors(t *testing.T) {
	t.Parallel()

	provider := &extractorSpyProvider{err: context.DeadlineExceeded}
	_, err := NewAnthropicExtractor(provider, "").Extract(context.Background(), ExtractRequest{})
	if err == nil {
		t.Fatal("Extract() error = nil, want wrapped provider error")
	}
	if !strings.Contains(err.Error(), "anthropic extract") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want wrapped deadline exceeded", err)
	}
}

func TestAnthropicExtractorExtractTruncatesLongFields(t *testing.T) {
	t.Parallel()

	longText := strings.Repeat("x", 220)
	longRationale := strings.Repeat("y", 220)
	provider := &extractorSpyProvider{response: &llm.ChatResponse{Content: `{"lessons":[{"topic":"ops","text":"` + longText + `","rationale":"` + longRationale + `","confidence":"medium"}]}`}}
	lessons, err := NewAnthropicExtractor(provider, "").Extract(context.Background(), ExtractRequest{})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len([]rune(lessons[0].Text)) > maxLessonTextLen {
		t.Fatalf("len(text) = %d, want <= %d", len([]rune(lessons[0].Text)), maxLessonTextLen)
	}
	if len([]rune(lessons[0].Rationale)) > maxLessonTextLen {
		t.Fatalf("len(rationale) = %d, want <= %d", len([]rune(lessons[0].Rationale)), maxLessonTextLen)
	}
	if !strings.HasSuffix(lessons[0].Text, "...") || !strings.HasSuffix(lessons[0].Rationale, "...") {
		t.Fatalf("lesson = %#v, want truncated ellipsis", lessons[0])
	}
}
