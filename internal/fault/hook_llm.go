package fault

import (
	"context"
	"errors"
	"fmt"

	"github.com/stello/elnath/internal/fault/faulttype"
	"github.com/stello/elnath/internal/llm"
)

type LLMFaultHook struct {
	inner    llm.Provider
	injector Injector
	scenario *faulttype.Scenario
}

func NewLLMFaultHook(p llm.Provider, inj Injector, s *faulttype.Scenario) *LLMFaultHook {
	return &LLMFaultHook{inner: p, injector: inj, scenario: s}
}

func (h *LLMFaultHook) Name() string { return h.inner.Name() }

func (h *LLMFaultHook) Models() []llm.ModelInfo { return h.inner.Models() }

func (h *LLMFaultHook) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return h.inner.Chat(ctx, req)
}

func (h *LLMFaultHook) Stream(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	if !h.injector.Active() {
		return h.inner.Stream(ctx, req, cb)
	}
	if h.scenario == nil || h.scenario.Category != faulttype.CategoryLLM {
		return h.inner.Stream(ctx, req, cb)
	}
	if h.injector.ShouldFault(h.scenario) {
		if err := h.injector.InjectFault(ctx, h.scenario); err != nil {
			return wrapLLMFaultError(h.scenario, err)
		}
	}
	return h.inner.Stream(ctx, req, cb)
}

func wrapLLMFaultError(s *faulttype.Scenario, err error) error {
	var http429 *HTTP429Error
	if errors.As(err, &http429) {
		return fmt.Errorf("llm fault hook (%s): 429: %w", s.Name, err)
	}
	var malformed *MalformedJSONError
	if errors.As(err, &malformed) {
		return fmt.Errorf("llm fault hook (%s): 503 malformed json: %w", s.Name, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("llm fault hook (%s): 503 timeout: %w", s.Name, err)
	}
	return fmt.Errorf("llm fault hook (%s): %w", s.Name, err)
}
