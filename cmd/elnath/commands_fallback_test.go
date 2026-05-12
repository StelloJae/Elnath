package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/config"
)

func TestResolveFallbackModel(t *testing.T) {
	t.Run("uses config field when set", func(t *testing.T) {
		cfg := &config.Config{FallbackModel: "gpt-custom"}
		if got := resolveFallbackModel(cfg); got != "gpt-custom" {
			t.Fatalf("resolveFallbackModel = %q, want %q", got, "gpt-custom")
		}
	})

	t.Run("defaults to centralized constant when cfg field empty", func(t *testing.T) {
		cfg := &config.Config{FallbackModel: ""}
		if got := resolveFallbackModel(cfg); got != "gpt-5.5" {
			t.Fatalf("resolveFallbackModel = %q, want %q", got, "gpt-5.5")
		}
	})

	t.Run("nil cfg falls back to centralized constant", func(t *testing.T) {
		if got := resolveFallbackModel(nil); got != "gpt-5.5" {
			t.Fatalf("resolveFallbackModel(nil) = %q, want %q", got, "gpt-5.5")
		}
	})
}

func TestBuildProviderPrefersExplicitOpenAIResponses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Anthropic.APIKey = "anthropic-key"
	cfg.OpenAIResponses.APIKey = "responses-key"
	cfg.OpenAIResponses.BaseURL = "https://api.moonshot.ai/v1"
	cfg.OpenAIResponses.Model = "kimi-k2"
	cfg.OpenAIResponses.ReasoningEffort = "high"

	provider, model, err := buildProvider(cfg)
	if err != nil {
		t.Fatalf("buildProvider: %v", err)
	}
	if provider.Name() != "openai-responses" {
		t.Fatalf("provider.Name() = %q, want openai-responses", provider.Name())
	}
	if model != "kimi-k2" {
		t.Fatalf("model = %q, want kimi-k2", model)
	}
}

func TestBuildProviderOpenAIResponsesUsesFallbackModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Anthropic.APIKey = ""
	cfg.OpenAI.APIKey = ""
	cfg.OpenAIResponses.APIKey = "responses-key"
	cfg.OpenAIResponses.BaseURL = "https://api.openai.com/v1"
	cfg.OpenAIResponses.Model = ""
	cfg.FallbackModel = "gpt-5.5"

	provider, model, err := buildProvider(cfg)
	if err != nil {
		t.Fatalf("buildProvider: %v", err)
	}
	if provider.Name() != "openai-responses" {
		t.Fatalf("provider.Name() = %q, want openai-responses", provider.Name())
	}
	if model != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5", model)
	}
}

func TestBuildProviderPrefersCodexOAuthOverAnthropicByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	auth := `{"auth_mode":"chatgpt","tokens":{"access_token":"codex-token","account_id":"acct"}}`
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(auth), 0o600); err != nil {
		t.Fatalf("WriteFile auth.json: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Anthropic.APIKey = "anthropic-key"
	cfg.OpenAIResponses.APIKey = ""
	cfg.OpenAI.APIKey = ""

	provider, model, err := buildProvider(cfg)
	if err != nil {
		t.Fatalf("buildProvider: %v", err)
	}
	if provider.Name() != "codex" {
		t.Fatalf("provider.Name() = %q, want codex", provider.Name())
	}
	if model != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5", model)
	}
}

func TestBuildProviderHonorsExplicitProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Provider = "anthropic"
	cfg.Anthropic.APIKey = "anthropic-key"
	cfg.Anthropic.Model = "claude-sonnet-4-6"
	cfg.OpenAIResponses.APIKey = "responses-key"
	cfg.OpenAIResponses.Model = "kimi-k2"

	provider, model, err := buildProvider(cfg)
	if err != nil {
		t.Fatalf("buildProvider: %v", err)
	}
	if provider.Name() != "anthropic" {
		t.Fatalf("provider.Name() = %q, want anthropic", provider.Name())
	}
	if model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q, want claude-sonnet-4-6", model)
	}
}

func TestBuildProviderHonorsExplicitProviderWithCodexOAuthAvailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	auth := `{"auth_mode":"chatgpt","tokens":{"access_token":"codex-token","account_id":"acct"}}`
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(auth), 0o600); err != nil {
		t.Fatalf("WriteFile auth.json: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Provider = "anthropic"
	cfg.Anthropic.APIKey = "anthropic-key"
	cfg.Anthropic.Model = "claude-sonnet-4-6"
	cfg.OpenAIResponses.APIKey = ""

	provider, model, err := buildProvider(cfg)
	if err != nil {
		t.Fatalf("buildProvider: %v", err)
	}
	if provider.Name() != "anthropic" {
		t.Fatalf("provider.Name() = %q, want anthropic", provider.Name())
	}
	if model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q, want claude-sonnet-4-6", model)
	}
}

func TestBuildProviderHonorsOpenAIResponsesProviderAlias(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Provider = "responses"
	cfg.Anthropic.APIKey = "anthropic-key"
	cfg.OpenAIResponses.APIKey = "responses-key"
	cfg.OpenAIResponses.BaseURL = "https://api.minimax.io/v1"
	cfg.OpenAIResponses.Model = "minimax-m2.7"

	provider, model, err := buildProvider(cfg)
	if err != nil {
		t.Fatalf("buildProvider: %v", err)
	}
	if provider.Name() != "openai-responses" {
		t.Fatalf("provider.Name() = %q, want openai-responses", provider.Name())
	}
	if model != "minimax-m2.7" {
		t.Fatalf("model = %q, want minimax-m2.7", model)
	}
}

func TestBuildProviderForSelectionDoesNotMutateConfiguredProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Provider = ""
	cfg.Anthropic.APIKey = "anthropic-key"
	cfg.Anthropic.Model = "claude-sonnet-4-6"
	cfg.OpenAIResponses.APIKey = "responses-key"
	cfg.OpenAIResponses.BaseURL = "https://api.moonshot.ai/v1"
	cfg.OpenAIResponses.Model = "kimi-k2"

	provider, model, err := buildProviderForSelection(cfg, "anthropic")
	if err != nil {
		t.Fatalf("buildProviderForSelection: %v", err)
	}
	if provider.Name() != "anthropic" {
		t.Fatalf("provider.Name() = %q, want anthropic", provider.Name())
	}
	if model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q, want claude-sonnet-4-6", model)
	}
	if cfg.Provider != "" {
		t.Fatalf("cfg.Provider = %q, want unchanged empty provider", cfg.Provider)
	}

	defaultProvider, defaultModel, err := buildProvider(cfg)
	if err != nil {
		t.Fatalf("buildProvider: %v", err)
	}
	if defaultProvider.Name() != "openai-responses" || defaultModel != "kimi-k2" {
		t.Fatalf("default provider/model = %s/%s, want openai-responses/kimi-k2", defaultProvider.Name(), defaultModel)
	}
}

func TestBuildProviderRejectsUnconfiguredExplicitProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Provider = "openai"
	cfg.Anthropic.APIKey = "anthropic-key"
	cfg.OpenAI.APIKey = ""
	cfg.OpenAIResponses.APIKey = ""

	_, _, err := buildProvider(cfg)
	if err == nil {
		t.Fatal("buildProvider error = nil, want unconfigured provider error")
	}
	if !strings.Contains(err.Error(), "selected but not configured") {
		t.Fatalf("error = %q, want selected-but-not-configured", err)
	}
}

func TestBuildProviderNoProviderMessagePrefersResponses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Anthropic.APIKey = ""
	cfg.OpenAI.APIKey = ""
	cfg.OpenAIResponses.APIKey = ""

	_, _, err := buildProvider(cfg)
	if err == nil {
		t.Fatal("buildProvider error = nil, want no-provider error")
	}
	msg := err.Error()
	responses := strings.Index(msg, "ELNATH_OPENAI_RESPONSES_API_KEY")
	anthropic := strings.Index(msg, "ELNATH_ANTHROPIC_API_KEY")
	if responses < 0 || anthropic < 0 {
		t.Fatalf("error %q missing provider guidance", msg)
	}
	if responses > anthropic {
		t.Fatalf("error %q should mention Responses before Anthropic", msg)
	}
}
