package onboarding

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ValidationResult represents the outcome of an API key validation.
type ValidationResult struct {
	Valid bool
	Error error
}

func validateOnboardingKey(ctx context.Context, apiKey string) ValidationResult {
	switch detectProviderFromAPIKey(apiKey) {
	case "openai_responses":
		return ValidationResult{
			Valid: true,
			Error: fmt.Errorf("Responses-compatible API keys are not remotely validated during onboarding"),
		}
	case "anthropic":
		return ValidateAnthropicKey(ctx, apiKey)
	default:
		return ValidationResult{Valid: false, Error: fmt.Errorf("empty key")}
	}
}

func detectProviderFromAPIKey(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	if strings.HasPrefix(apiKey, "sk-ant-") {
		return "anthropic"
	}
	return "openai_responses"
}

// ValidateAnthropicKey makes a lightweight API call to verify the key.
// Returns valid=true if the key authenticates successfully.
// Network errors are reported but don't mark the key as invalid.
func ValidateAnthropicKey(ctx context.Context, apiKey string) ValidationResult {
	if apiKey == "" {
		return ValidationResult{Valid: false, Error: fmt.Errorf("empty key")}
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	body := `{"model":"claude-haiku-4-5","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", strings.NewReader(body))
	if err != nil {
		return ValidationResult{Valid: false, Error: fmt.Errorf("create request: %w", err)}
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ValidationResult{Valid: false, Error: fmt.Errorf("network error: %w", err)}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return ValidationResult{Valid: true}
	case http.StatusUnauthorized:
		return ValidationResult{Valid: false, Error: fmt.Errorf("authentication failed")}
	case http.StatusForbidden:
		return ValidationResult{Valid: false, Error: fmt.Errorf("access denied")}
	default:
		// Rate limit, server error, etc. — key is likely valid.
		return ValidationResult{Valid: true, Error: fmt.Errorf("status %d (key accepted)", resp.StatusCode)}
	}
}
