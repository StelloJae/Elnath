package llm

import (
	"context"
	"strings"
	"testing"
	"time"
)

type capabilityTestProvider struct{}

func (capabilityTestProvider) Chat(context.Context, ChatRequest) (*ChatResponse, error) {
	return nil, nil
}

func (capabilityTestProvider) Stream(context.Context, ChatRequest, func(StreamEvent)) error {
	return nil
}

func (capabilityTestProvider) Name() string { return "cap-test" }

func (capabilityTestProvider) Models() []ModelInfo { return nil }

func (capabilityTestProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		ReasoningEffort:         ReasoningEffortNative,
		ReasoningEffortFallback: "none",
	}
}

type plainCapabilityTestProvider struct{}

func (plainCapabilityTestProvider) Chat(context.Context, ChatRequest) (*ChatResponse, error) {
	return nil, nil
}

func (plainCapabilityTestProvider) Stream(context.Context, ChatRequest, func(StreamEvent)) error {
	return nil
}

func (plainCapabilityTestProvider) Name() string { return "plain-test" }

func (plainCapabilityTestProvider) Models() []ModelInfo { return nil }

func TestCapabilitiesOf(t *testing.T) {
	capable := CapabilitiesOf(capabilityTestProvider{})
	if capable.Name != "cap-test" || capable.ReasoningEffort != ReasoningEffortNative || capable.ReasoningEffortFallback != "none" {
		t.Fatalf("capable = %+v", capable)
	}

	plain := CapabilitiesOf(plainCapabilityTestProvider{})
	if plain.Name != "plain-test" || plain.ReasoningEffort != ReasoningEffortUnknown {
		t.Fatalf("plain = %+v, want provider name with unknown effort capability", plain)
	}

	nilCaps := CapabilitiesOf(nil)
	if nilCaps.Name != "unknown" || nilCaps.ReasoningEffort != ReasoningEffortUnknown {
		t.Fatalf("nil caps = %+v", nilCaps)
	}
}

func TestProviderCapabilitiesByProvider(t *testing.T) {
	tests := []struct {
		name        string
		provider    Provider
		wantEffort  string
		wantNote    string
		wantTimeout int
	}{
		{
			name:        "responses",
			provider:    NewResponsesProvider("key", "gpt-5.5", "", WithResponsesTimeout(77*time.Second)),
			wantEffort:  ReasoningEffortNativeWithUnsupportedRetry,
			wantNote:    "retry_without_reasoning",
			wantTimeout: 77,
		},
		{
			name:        "codex",
			provider:    NewCodexOAuthProvider("gpt-5.5", WithCodexOAuthTimeout(42*time.Second)),
			wantEffort:  ReasoningEffortNativeWithUnsupportedRetry,
			wantNote:    "retry_without_reasoning",
			wantTimeout: 42,
		},
		{
			name:        "anthropic",
			provider:    NewAnthropicProvider("key", "claude-sonnet-4-6", WithAnthropicTimeout(33*time.Second)),
			wantEffort:  ReasoningEffortThinkingBudgetOnly,
			wantNote:    "thinking_budget",
			wantTimeout: 33,
		},
		{
			name:        "openai",
			provider:    NewOpenAIProvider("key", "gpt-5.5", WithOpenAITimeout(45*time.Second)),
			wantEffort:  ReasoningEffortIgnored,
			wantTimeout: 45,
		},
		{
			name:        "ollama",
			provider:    NewOllamaProvider("", "llama3.2", WithOllamaTimeout(55*time.Second)),
			wantEffort:  ReasoningEffortIgnored,
			wantTimeout: 55,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CapabilitiesOf(tt.provider)
			if got.Name != tt.provider.Name() {
				t.Fatalf("Name = %q, want %q", got.Name, tt.provider.Name())
			}
			if got.ReasoningEffort != tt.wantEffort {
				t.Fatalf("ReasoningEffort = %q, want %q", got.ReasoningEffort, tt.wantEffort)
			}
			if tt.wantNote != "" && !strings.Contains(got.ReasoningEffortFallback, tt.wantNote) {
				t.Fatalf("ReasoningEffortFallback = %q, want contains %q", got.ReasoningEffortFallback, tt.wantNote)
			}
			if got.RequestTimeoutSeconds != tt.wantTimeout {
				t.Fatalf("RequestTimeoutSeconds = %d, want %d", got.RequestTimeoutSeconds, tt.wantTimeout)
			}
		})
	}
}
