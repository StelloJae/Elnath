package onboarding

import (
	"path/filepath"
	"testing"
)

func TestRunQuickstartUsesOpenAIResponsesEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ELNATH_OPENAI_RESPONSES_API_KEY", "sk-responses-quickstart")
	t.Setenv("ELNATH_OPENAI_RESPONSES_BASE_URL", "https://api.minimax.io/v1")
	t.Setenv("ELNATH_OPENAI_RESPONSES_MODEL", "minimax-m2.7")
	t.Setenv("ELNATH_OPENAI_RESPONSES_REASONING_EFFORT", "medium")

	result, err := RunQuickstart(filepath.Join(home, ".elnath", "config.yaml"), "test-version")
	if err != nil {
		t.Fatalf("RunQuickstart error = %v", err)
	}
	if result.ProviderDetected != "openai_responses" {
		t.Fatalf("ProviderDetected = %q, want openai_responses", result.ProviderDetected)
	}
	if result.Provider != "openai_responses" {
		t.Fatalf("Provider = %q, want openai_responses", result.Provider)
	}
	if result.OpenAIResponsesAPIKey != "sk-responses-quickstart" {
		t.Fatalf("OpenAIResponsesAPIKey = %q", result.OpenAIResponsesAPIKey)
	}
	if result.OpenAIResponsesBaseURL != "https://api.minimax.io/v1" {
		t.Fatalf("OpenAIResponsesBaseURL = %q", result.OpenAIResponsesBaseURL)
	}
	if result.OpenAIResponsesModel != "minimax-m2.7" {
		t.Fatalf("OpenAIResponsesModel = %q", result.OpenAIResponsesModel)
	}
	if result.OpenAIResponsesReasoningEffort != "medium" {
		t.Fatalf("OpenAIResponsesReasoningEffort = %q", result.OpenAIResponsesReasoningEffort)
	}
}
