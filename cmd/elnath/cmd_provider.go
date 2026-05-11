package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/llm"
)

type providerStatusView struct {
	Provider              string                        `json:"provider"`
	Model                 string                        `json:"model"`
	ReasoningEffort       string                        `json:"reasoning_effort"`
	ReasoningEffortMode   string                        `json:"reasoning_effort_mode"`
	ConfiguredEffort      string                        `json:"configured_effort"`
	ProviderEffort        string                        `json:"provider_effort"`
	ProviderEffortNote    string                        `json:"provider_effort_note,omitempty"`
	AutoEffortCompatible  bool                          `json:"auto_effort_compatible"`
	RequestTimeoutSeconds int                           `json:"request_timeout_seconds"`
	ConfiguredProviders   []providerConfigCandidateView `json:"configured_providers,omitempty"`
}

type providerConfigCandidateView struct {
	Provider              string `json:"provider"`
	Model                 string `json:"model"`
	BaseURL               string `json:"base_url,omitempty"`
	ReasoningEffort       string `json:"reasoning_effort,omitempty"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"`
}

func cmdProvider(_ context.Context, args []string) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	switch args[0] {
	case "status":
		return providerStatus(args[1:])
	case "help", "-h", "--help":
		return providerUsage()
	default:
		return fmt.Errorf("provider: unknown subcommand %q (try: elnath provider help)", args[0])
	}
}

func providerUsage() error {
	fmt.Fprintln(os.Stdout, "Usage: elnath provider status [--json]")
	return nil
}

func providerStatus(args []string) error {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "help", "-h", "--help":
			return providerUsage()
		default:
			return fmt.Errorf("provider status: unknown flag %q", arg)
		}
	}

	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("provider status: load config: %w", err)
	}
	provider, model, err := buildProvider(cfg)
	if err != nil {
		return fmt.Errorf("provider status: build provider: %w", err)
	}
	caps := llm.CapabilitiesOf(provider)
	view := providerStatusView{
		Provider:              caps.Name,
		Model:                 model,
		ReasoningEffort:       caps.ReasoningEffort,
		ReasoningEffortMode:   cfg.Reasoning.EffortMode,
		ConfiguredEffort:      cfg.Reasoning.Effort,
		ProviderEffort:        caps.ReasoningEffort,
		ProviderEffortNote:    caps.ReasoningEffortFallback,
		AutoEffortCompatible:  autoEffortCompatible(caps.ReasoningEffort),
		RequestTimeoutSeconds: caps.RequestTimeoutSeconds,
		ConfiguredProviders:   configuredProviderCandidates(cfg),
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		return enc.Encode(view)
	}
	fmt.Fprintf(os.Stdout, "Provider: %s\n", view.Provider)
	fmt.Fprintf(os.Stdout, "Model: %s\n", view.Model)
	fmt.Fprintf(os.Stdout, "Reasoning effort capability: %s\n", view.ProviderEffort)
	if view.ProviderEffortNote != "" {
		fmt.Fprintf(os.Stdout, "Reasoning effort note: %s\n", view.ProviderEffortNote)
	}
	fmt.Fprintf(os.Stdout, "Configured reasoning: mode=%s effort=%s\n", view.ReasoningEffortMode, view.ConfiguredEffort)
	fmt.Fprintf(os.Stdout, "Auto effort compatible: %t\n", view.AutoEffortCompatible)
	fmt.Fprintf(os.Stdout, "Request timeout: %ds\n", view.RequestTimeoutSeconds)
	if len(view.ConfiguredProviders) > 0 {
		fmt.Fprintln(os.Stdout, "Configured providers:")
		for _, candidate := range view.ConfiguredProviders {
			base := ""
			if candidate.BaseURL != "" {
				base = " base_url=" + candidate.BaseURL
			}
			effort := ""
			if candidate.ReasoningEffort != "" {
				effort = " effort=" + candidate.ReasoningEffort
			}
			fmt.Fprintf(os.Stdout, "  - %s model=%s timeout=%ds%s%s\n",
				candidate.Provider,
				candidate.Model,
				candidate.RequestTimeoutSeconds,
				base,
				effort,
			)
		}
	}
	return nil
}

func configuredProviderCandidates(cfg *config.Config) []providerConfigCandidateView {
	if cfg == nil {
		return nil
	}

	var out []providerConfigCandidateView
	if cfg.OpenAIResponses.APIKey != "" {
		baseURL := cfg.OpenAIResponses.BaseURL
		if baseURL == "" {
			baseURL = defaultOpenAIBaseURLForResponses()
		}
		out = append(out, providerConfigCandidateView{
			Provider:              "openai-responses",
			Model:                 providerConfigModel(cfg.OpenAIResponses.Model, resolveFallbackModel(cfg)),
			BaseURL:               baseURL,
			ReasoningEffort:       cfg.OpenAIResponses.ReasoningEffort,
			RequestTimeoutSeconds: cfg.OpenAIResponses.Timeout,
		})
	}
	if cfg.Anthropic.APIKey != "" {
		out = append(out, providerConfigCandidateView{
			Provider:              "anthropic",
			Model:                 providerConfigModel(cfg.Anthropic.Model, "claude-sonnet-4-6"),
			BaseURL:               cfg.Anthropic.BaseURL,
			ReasoningEffort:       cfg.Anthropic.ReasoningEffort,
			RequestTimeoutSeconds: cfg.Anthropic.Timeout,
		})
	}
	if cfg.OpenAI.APIKey != "" {
		out = append(out, providerConfigCandidateView{
			Provider:              "openai",
			Model:                 providerConfigModel(cfg.OpenAI.Model, resolveFallbackModel(cfg)),
			BaseURL:               cfg.OpenAI.BaseURL,
			ReasoningEffort:       cfg.OpenAI.ReasoningEffort,
			RequestTimeoutSeconds: cfg.OpenAI.Timeout,
		})
	}
	if cfg.Ollama.Model != "" || cfg.Ollama.BaseURL != "" {
		out = append(out, providerConfigCandidateView{
			Provider:              "ollama",
			Model:                 providerConfigModel(cfg.Ollama.Model, "llama3.2"),
			BaseURL:               cfg.Ollama.BaseURL,
			RequestTimeoutSeconds: 0,
		})
	}
	return out
}

func providerConfigModel(model, fallback string) string {
	if model != "" {
		return model
	}
	return fallback
}

func autoEffortCompatible(providerEffort string) bool {
	switch providerEffort {
	case llm.ReasoningEffortNative, llm.ReasoningEffortNativeWithUnsupportedRetry:
		return true
	default:
		return false
	}
}
