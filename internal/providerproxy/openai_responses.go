package providerproxy

import (
	"context"
	"errors"
	"strings"

	"github.com/stello/elnath/internal/config"
)

const defaultOpenAIResponsesProxyBaseURL = "https://api.openai.com/v1"

var openAIResponsesAllowedPaths = []string{"/models", "/responses"}

type OpenAIResponsesAdapter struct {
	apiKey  string
	baseURL string
}

func OpenAIResponsesAdapterFromConfig(cfg *config.Config) (*OpenAIResponsesAdapter, error) {
	if cfg == nil {
		return &OpenAIResponsesAdapter{baseURL: defaultOpenAIResponsesProxyBaseURL}, nil
	}
	baseURL := strings.TrimSpace(cfg.OpenAIResponses.BaseURL)
	if baseURL == "" {
		baseURL = defaultOpenAIResponsesProxyBaseURL
	}
	return &OpenAIResponsesAdapter{
		apiKey:  strings.TrimSpace(cfg.OpenAIResponses.APIKey),
		baseURL: strings.TrimRight(baseURL, "/"),
	}, nil
}

func (a *OpenAIResponsesAdapter) Name() string {
	return "openai-responses"
}

func (a *OpenAIResponsesAdapter) DisplayName() string {
	return "OpenAI Responses-compatible"
}

func (a *OpenAIResponsesAdapter) AllowedPaths() []string {
	return append([]string(nil), openAIResponsesAllowedPaths...)
}

func (a *OpenAIResponsesAdapter) Credential(context.Context) (Credential, error) {
	if a == nil || strings.TrimSpace(a.apiKey) == "" {
		return Credential{}, errors.New("openai_responses.api_key is not configured")
	}
	if strings.TrimSpace(a.baseURL) == "" {
		return Credential{}, errors.New("openai_responses.base_url is not configured")
	}
	return Credential{
		Bearer:      a.apiKey,
		TokenType:   "Bearer",
		BaseURL:     strings.TrimRight(a.baseURL, "/"),
		Refreshable: false,
	}, nil
}

func (a *OpenAIResponsesAdapter) Status(ctx context.Context) ProviderStatus {
	status := ProviderStatus{
		Provider:         a.Name(),
		DisplayName:      a.DisplayName(),
		AllowedPaths:     sortedAllowedPaths(a.AllowedPaths()),
		BaseURL:          sanitizeBaseURLForOutput(a.baseURL),
		APIKeyConfigured: a != nil && strings.TrimSpace(a.apiKey) != "",
		Refreshable:      false,
	}
	_, err := a.Credential(ctx)
	if err != nil {
		status.AuthFailure = err.Error()
		return status
	}
	status.Ready = true
	status.Authenticated = true
	return status
}
