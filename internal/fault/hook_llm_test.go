package fault

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stello/elnath/internal/fault/faulttype"
	"github.com/stello/elnath/internal/llm"
)

type hookProvider struct {
	streamCalls int
}

func (p *hookProvider) Name() string            { return "mock" }
func (p *hookProvider) Models() []llm.ModelInfo { return nil }
func (p *hookProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}
func (p *hookProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.streamCalls++
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "ok"})
	cb(llm.StreamEvent{Type: llm.EventDone})
	return nil
}

var _ llm.Provider = (*LLMFaultHook)(nil)

func TestLLMFaultHookReturnsInjectedError(t *testing.T) {
	inj := &toolHookInjector{active: true, shouldFault: true, err: &HTTP429Error{Scenario: "llm-anthropic-429-burst", RetryAfter: time.Second}}
	inner := &hookProvider{}
	hook := NewLLMFaultHook(inner, inj, testScenario("llm-anthropic-429-burst", faulttype.CategoryLLM, faulttype.FaultHTTP429Burst))

	err := hook.Stream(context.Background(), llm.ChatRequest{}, func(llm.StreamEvent) {})
	if err == nil {
		t.Fatal("Stream() error = nil, want injected error")
	}
	if inner.streamCalls != 0 {
		t.Fatalf("inner stream calls = %d, want 0", inner.streamCalls)
	}
}

func TestLLMFaultHookDelegatesWhenShouldFaultFalse(t *testing.T) {
	inj := &toolHookInjector{active: true}
	inner := &hookProvider{}
	hook := NewLLMFaultHook(inner, inj, testScenario("llm-provider-timeout", faulttype.CategoryLLM, faulttype.FaultTimeout))

	if err := hook.Stream(context.Background(), llm.ChatRequest{}, func(llm.StreamEvent) {}); err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	if inner.streamCalls != 1 {
		t.Fatalf("inner stream calls = %d, want 1", inner.streamCalls)
	}
}

func TestLLMFaultHookSkipsWrongCategory(t *testing.T) {
	inj := &toolHookInjector{active: true, shouldFault: true, err: errors.New("boom")}
	inner := &hookProvider{}
	hook := NewLLMFaultHook(inner, inj, testScenario("tool-bash-transient-fail", faulttype.CategoryTool, faulttype.FaultTransientError))

	if err := hook.Stream(context.Background(), llm.ChatRequest{}, func(llm.StreamEvent) {}); err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	if inner.streamCalls != 1 {
		t.Fatalf("inner stream calls = %d, want 1", inner.streamCalls)
	}
}
