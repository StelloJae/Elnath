package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ProviderError preserves provider/status metadata across wrapping so runtime
// classifiers do not need to recover decisions from free text alone.
type ProviderError struct {
	Provider   string
	StatusCode int
	Code       string
	Message    string
	Body       string
	Cause      error
}

func NewProviderHTTPError(provider string, statusCode int, body []byte) *ProviderError {
	code, message := parseProviderErrorBody(body)
	return &ProviderError{
		Provider:   provider,
		StatusCode: statusCode,
		Code:       code,
		Message:    message,
		Body:       strings.TrimSpace(string(body)),
	}
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	provider := e.Provider
	if provider == "" {
		provider = "provider"
	}

	parts := []string{provider}
	if e.StatusCode > 0 {
		parts = append(parts, fmt.Sprintf("http %d", e.StatusCode))
	}
	if e.Code != "" {
		parts = append(parts, "("+e.Code+")")
	}

	detail := strings.TrimSpace(e.Message)
	if detail == "" {
		detail = strings.TrimSpace(e.Body)
	}
	if detail != "" {
		return strings.Join(parts, ": ") + ": " + truncateProviderErrorDetail(detail)
	}
	if e.Cause != nil {
		return strings.Join(parts, ": ") + ": " + e.Cause.Error()
	}
	return strings.Join(parts, ": ")
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *ProviderError) ProviderName() string {
	if e == nil {
		return ""
	}
	return e.Provider
}

func (e *ProviderError) HTTPStatusCode() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

func (e *ProviderError) ProviderErrorCode() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func (e *ProviderError) ProviderErrorMessage() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func truncateProviderErrorDetail(s string) string {
	const max = 500
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func parseProviderErrorBody(body []byte) (string, string) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", ""
	}

	rawErr, ok := payload["error"]
	if !ok {
		return "", ""
	}
	switch errValue := rawErr.(type) {
	case string:
		return "", strings.TrimSpace(errValue)
	case map[string]any:
		code := firstString(errValue, "type", "code", "status")
		message := firstString(errValue, "message", "error")
		return code, message
	default:
		return "", ""
	}
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := values[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
