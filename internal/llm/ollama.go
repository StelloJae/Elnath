package llm

import (
	"context"
	"net/http"
	"time"
)

const defaultOllamaBaseURL = "http://localhost:11434/v1"

// OllamaProvider implements Provider for local Ollama instances.
// Ollama exposes an OpenAI-compatible API, so this delegates to OpenAIProvider
// with adjusted defaults (local base URL, optional API key).
type OllamaProvider struct {
	inner *OpenAIProvider
	model string
}

// OllamaOption configures an OllamaProvider.
type OllamaOption func(*OllamaProvider)

// WithOllamaBaseURL overrides the default Ollama API URL.
func WithOllamaBaseURL(u string) OllamaOption {
	return func(p *OllamaProvider) {
		p.inner.baseURL = u
	}
}

// WithOllamaTimeout sets the HTTP client timeout.
func WithOllamaTimeout(d time.Duration) OllamaOption {
	return func(p *OllamaProvider) {
		p.inner.client = &http.Client{Timeout: d}
	}
}

// NewOllamaProvider creates an OllamaProvider.
// apiKey can be empty — Ollama doesn't require authentication by default.
func NewOllamaProvider(apiKey, model string, opts ...OllamaOption) *OllamaProvider {
	inner := NewOpenAIProvider(apiKey, model, WithOpenAIBaseURL(defaultOllamaBaseURL))
	p := &OllamaProvider{inner: inner, model: model}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *OllamaProvider) Name() string { return "ollama" }

func (p *OllamaProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return p.inner.Chat(ctx, req)
}

func (p *OllamaProvider) Stream(ctx context.Context, req ChatRequest, cb func(StreamEvent)) error {
	return p.inner.Stream(ctx, req, cb)
}

func (p *OllamaProvider) Models() []ModelInfo {
	return []ModelInfo{
		{ID: p.model, Name: p.model, MaxTokens: 32768},
	}
}
