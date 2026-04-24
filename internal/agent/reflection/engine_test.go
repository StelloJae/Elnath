package reflection

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stello/elnath/internal/llm"
)

type fakeProvider struct {
	content string
	err     error
	onChat  func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
}

func (f *fakeProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if f.onChat != nil {
		return f.onChat(ctx, req)
	}
	if f.err != nil {
		return nil, f.err
	}
	return &llm.ChatResponse{Content: f.content}, nil
}

func (f *fakeProvider) Stream(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	return nil
}

func (f *fakeProvider) Name() string             { return "fake" }
func (f *fakeProvider) Models() []llm.ModelInfo  { return nil }

func newTestInput() Input {
	return Input{
		Transcript: []llm.Message{
			llm.NewUserMessage("please fix the failing test"),
			llm.NewAssistantMessage("ran bash; got error"),
		},
		ErrorSummary:  "exit code 1",
		TaskMeta:      TaskMeta{TaskID: "42", SessionID: "sess-xyz"},
		Fingerprint:   "ABCDEFGH1234",
		FinishReason:  "error",
		ErrorCategory: "server_error",
	}
}

func TestLLMEngine_ValidResponse(t *testing.T) {
	provider := &fakeProvider{
		content: `{"suggested_strategy":"retry_smaller_scope","reasoning":"too many files touched","task_summary":"fix failing test"}`,
	}
	eng := NewLLMEngine(provider, "gpt-5.5")

	rep, err := eng.Reflect(context.Background(), newTestInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.SuggestedStrategy != StrategyRetrySmallerScope {
		t.Fatalf("strategy mismatch: got %q", rep.SuggestedStrategy)
	}
	if rep.Reasoning != "too many files touched" {
		t.Fatalf("reasoning mismatch: got %q", rep.Reasoning)
	}
	if rep.TaskSummary != "fix failing test" {
		t.Fatalf("task summary mismatch: got %q", rep.TaskSummary)
	}
	if rep.Fingerprint != "ABCDEFGH1234" {
		t.Fatalf("fingerprint lost: got %q", rep.Fingerprint)
	}
	if rep.FinishReason != "error" || rep.ErrorCategory != "server_error" {
		t.Fatalf("metadata lost: %+v", rep)
	}
}

func TestLLMEngine_SchemaInvalid_FallsBackToUnknown(t *testing.T) {
	provider := &fakeProvider{content: `not valid json at all`}
	eng := NewLLMEngine(provider, "gpt-5.5")

	rep, err := eng.Reflect(context.Background(), newTestInput())
	if err != nil {
		t.Fatalf("schema invalid must not error: %v", err)
	}
	if rep.SuggestedStrategy != StrategyUnknown {
		t.Fatalf("expected unknown, got %q", rep.SuggestedStrategy)
	}
}

func TestLLMEngine_EnumOutOfRange_FallsBackToUnknown(t *testing.T) {
	provider := &fakeProvider{
		content: `{"suggested_strategy":"teleport_to_fix","reasoning":"","task_summary":""}`,
	}
	eng := NewLLMEngine(provider, "gpt-5.5")

	rep, err := eng.Reflect(context.Background(), newTestInput())
	if err != nil {
		t.Fatalf("enum out of range must not error: %v", err)
	}
	if rep.SuggestedStrategy != StrategyUnknown {
		t.Fatalf("expected unknown, got %q", rep.SuggestedStrategy)
	}
}

func TestLLMEngine_FencedJSON_Parsed(t *testing.T) {
	// Some models ignore "no markdown" instructions and wrap output anyway.
	provider := &fakeProvider{
		content: "sure thing!\n```json\n{\"suggested_strategy\":\"compress_context\",\"reasoning\":\"context overflow\",\"task_summary\":\"long task\"}\n```",
	}
	eng := NewLLMEngine(provider, "gpt-5.5")

	rep, err := eng.Reflect(context.Background(), newTestInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.SuggestedStrategy != StrategyCompressContext {
		t.Fatalf("expected compress_context, got %q", rep.SuggestedStrategy)
	}
}

func TestLLMEngine_ProviderError_Propagates(t *testing.T) {
	provider := &fakeProvider{err: errors.New("boom")}
	eng := NewLLMEngine(provider, "gpt-5.5")

	_, err := eng.Reflect(context.Background(), newTestInput())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLLMEngine_Timeout_EnforcedPerCall(t *testing.T) {
	provider := &fakeProvider{
		onChat: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	eng := NewLLMEngine(provider, "gpt-5.5", WithEngineTimeout(10*time.Millisecond))

	start := time.Now()
	_, err := eng.Reflect(context.Background(), newTestInput())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout not enforced; took %v", elapsed)
	}
}

func TestLLMEngine_ParentContext_Independent(t *testing.T) {
	// Parent cancellation must still flow down; engine does not ignore it.
	provider := &fakeProvider{
		onChat: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	eng := NewLLMEngine(provider, "gpt-5.5", WithEngineTimeout(5*time.Second))

	parent, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := eng.Reflect(parent, newTestInput())
	if err == nil {
		t.Fatal("expected error when parent cancelled before call")
	}
}

func TestExtractJSONObject_Balanced(t *testing.T) {
	in := `prefix {"a":{"b":1}} trailing`
	got := extractJSONObject(in)
	want := `{"a":{"b":1}}`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractJSONObject_QuoteEscape(t *testing.T) {
	in := `{"reason":"he said \"hi\" } ok"}`
	got := extractJSONObject(in)
	if got != in {
		t.Fatalf("quote escape broken: got %q", got)
	}
}
