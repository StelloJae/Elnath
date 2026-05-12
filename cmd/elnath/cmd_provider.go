package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/llm"
)

type providerStatusView struct {
	Provider                       string                        `json:"provider"`
	Model                          string                        `json:"model"`
	ReasoningEffort                string                        `json:"reasoning_effort"`
	ReasoningEffortMode            string                        `json:"reasoning_effort_mode"`
	ConfiguredEffort               string                        `json:"configured_effort"`
	ProviderEffort                 string                        `json:"provider_effort"`
	ProviderEffortNote             string                        `json:"provider_effort_note,omitempty"`
	AutoEffortCompatible           bool                          `json:"auto_effort_compatible"`
	RequestTimeoutSeconds          int                           `json:"request_timeout_seconds"`
	RuntimeProviderSwitchAvailable bool                          `json:"runtime_provider_switch_available"`
	ProviderSwitchBoundaries       []string                      `json:"provider_switch_boundaries,omitempty"`
	ConfiguredProviders            []providerConfigCandidateView `json:"configured_providers,omitempty"`
}

type providerConfigCandidateView struct {
	Provider              string `json:"provider"`
	Model                 string `json:"model"`
	BaseURL               string `json:"base_url,omitempty"`
	ReasoningEffort       string `json:"reasoning_effort,omitempty"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"`
}

type providerSelectionCheckView struct {
	RequestedProvider              string   `json:"requested_provider"`
	Provider                       string   `json:"provider"`
	Model                          string   `json:"model"`
	ProviderEffort                 string   `json:"provider_effort"`
	ProviderEffortNote             string   `json:"provider_effort_note,omitempty"`
	AutoEffortCompatible           bool     `json:"auto_effort_compatible"`
	RequestTimeoutSeconds          int      `json:"request_timeout_seconds"`
	WouldSwitch                    bool     `json:"would_switch"`
	RuntimeProviderSwitchAvailable bool     `json:"runtime_provider_switch_available"`
	ProviderSwitchBoundaries       []string `json:"provider_switch_boundaries,omitempty"`
}

const (
	providerSwitchBoundaryRestartRequired         = "restart_required"
	providerSwitchBoundaryReflectionStartupBound  = "reflection_provider_startup_bound"
	providerSwitchBoundaryCompressionStartupBound = "compression_budget_startup_bound"
)

func cmdProvider(_ context.Context, args []string) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	switch args[0] {
	case "status":
		return providerStatus(args[1:])
	case "candidates":
		return providerCandidates(args[1:])
	case "check":
		return providerCheck(args[1:])
	case "help", "-h", "--help":
		return providerUsage()
	default:
		return fmt.Errorf("provider: unknown subcommand %q (try: elnath provider help)", args[0])
	}
}

func providerUsage() error {
	fmt.Fprintln(os.Stdout, "Usage: elnath provider [status [--json]|candidates [--json]|check <provider> [--json]]")
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
	applyGlobalFlagOverrides(cfg, os.Args)
	provider, model, err := buildProvider(cfg)
	if err != nil {
		return fmt.Errorf("provider status: build provider: %w", err)
	}
	caps := llm.CapabilitiesOf(provider)
	view := providerStatusView{
		Provider:                 caps.Name,
		Model:                    model,
		ReasoningEffort:          caps.ReasoningEffort,
		ReasoningEffortMode:      cfg.Reasoning.EffortMode,
		ConfiguredEffort:         cfg.Reasoning.Effort,
		ProviderEffort:           caps.ReasoningEffort,
		ProviderEffortNote:       caps.ReasoningEffortFallback,
		AutoEffortCompatible:     autoEffortCompatible(caps.ReasoningEffort),
		RequestTimeoutSeconds:    caps.RequestTimeoutSeconds,
		ProviderSwitchBoundaries: providerSwitchBoundaries(cfg.SelfHealing.Enabled),
		ConfiguredProviders:      configuredProviderCandidates(cfg),
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
	fmt.Fprintln(os.Stdout, formatProviderSwitchBoundary(view.ProviderSwitchBoundaries))
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

func providerCandidates(args []string) error {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "help", "-h", "--help":
			return providerUsage()
		default:
			return fmt.Errorf("provider candidates: unknown flag %q", arg)
		}
	}

	cfg, err := loadProviderCommandConfig()
	if err != nil {
		return fmt.Errorf("provider candidates: %w", err)
	}
	candidates := configuredProviderCandidates(cfg)
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		return enc.Encode(candidates)
	}
	fmt.Fprintln(os.Stdout, formatProviderCandidates(candidates))
	return nil
}

func providerCheck(args []string) error {
	providerName := ""
	jsonOut := false
	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonOut = true
		case arg == "help" || arg == "-h" || arg == "--help":
			return providerUsage()
		case providerName == "":
			providerName = arg
		default:
			return fmt.Errorf("provider check: unexpected argument %q", arg)
		}
	}
	if providerName == "" {
		return fmt.Errorf("provider check: provider is required")
	}

	cfg, err := loadProviderCommandConfig()
	if err != nil {
		return fmt.Errorf("provider check: %w", err)
	}
	view, err := providerSelectionCheckViewForConfig(cfg, providerName)
	if err != nil {
		return fmt.Errorf("provider check: %w", err)
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		return enc.Encode(view)
	}
	fmt.Fprintf(os.Stdout, "Provider check: %s configured\n", view.Provider)
	fmt.Fprintf(os.Stdout, "Model: %s\n", view.Model)
	fmt.Fprintf(os.Stdout, "Reasoning effort capability: %s\n", view.ProviderEffort)
	if view.ProviderEffortNote != "" {
		fmt.Fprintf(os.Stdout, "Reasoning effort note: %s\n", view.ProviderEffortNote)
	}
	fmt.Fprintf(os.Stdout, "Auto effort compatible: %t\n", view.AutoEffortCompatible)
	fmt.Fprintf(os.Stdout, "Request timeout: %ds\n", view.RequestTimeoutSeconds)
	fmt.Fprintln(os.Stdout, "This does not switch any running session.")
	fmt.Fprintln(os.Stdout, formatProviderSwitchBoundary(view.ProviderSwitchBoundaries))
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
			BaseURL:               sanitizeProviderBaseURL(baseURL),
			ReasoningEffort:       cfg.OpenAIResponses.ReasoningEffort,
			RequestTimeoutSeconds: cfg.OpenAIResponses.Timeout,
		})
	}
	if cfg.Anthropic.APIKey != "" {
		out = append(out, providerConfigCandidateView{
			Provider:              "anthropic",
			Model:                 providerConfigModel(cfg.Anthropic.Model, "claude-sonnet-4-6"),
			BaseURL:               sanitizeProviderBaseURL(cfg.Anthropic.BaseURL),
			ReasoningEffort:       cfg.Anthropic.ReasoningEffort,
			RequestTimeoutSeconds: cfg.Anthropic.Timeout,
		})
	}
	if cfg.OpenAI.APIKey != "" {
		out = append(out, providerConfigCandidateView{
			Provider:              "openai",
			Model:                 providerConfigModel(cfg.OpenAI.Model, resolveFallbackModel(cfg)),
			BaseURL:               sanitizeProviderBaseURL(cfg.OpenAI.BaseURL),
			ReasoningEffort:       cfg.OpenAI.ReasoningEffort,
			RequestTimeoutSeconds: cfg.OpenAI.Timeout,
		})
	}
	if cfg.Ollama.Model != "" || cfg.Ollama.BaseURL != "" {
		out = append(out, providerConfigCandidateView{
			Provider:              "ollama",
			Model:                 providerConfigModel(cfg.Ollama.Model, "llama3.2"),
			BaseURL:               sanitizeProviderBaseURL(cfg.Ollama.BaseURL),
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

func providerSelectionCheckViewForConfig(cfg *config.Config, providerName string) (providerSelectionCheckView, error) {
	selected := config.NormalizeProviderName(providerName)
	if selected == "" {
		return providerSelectionCheckView{}, fmt.Errorf("provider name is required")
	}
	if !isConfiguredProviderCandidate(cfg, selected) {
		return providerSelectionCheckView{}, fmt.Errorf("provider %q is not a configured provider candidate", selected)
	}
	provider, model, err := buildProviderForSelection(cfg, selected)
	if err != nil {
		return providerSelectionCheckView{}, err
	}
	caps := llm.CapabilitiesOf(provider)
	return providerSelectionCheckView{
		RequestedProvider:              selected,
		Provider:                       caps.Name,
		Model:                          model,
		ProviderEffort:                 caps.ReasoningEffort,
		ProviderEffortNote:             caps.ReasoningEffortFallback,
		AutoEffortCompatible:           autoEffortCompatible(caps.ReasoningEffort),
		RequestTimeoutSeconds:          caps.RequestTimeoutSeconds,
		WouldSwitch:                    false,
		RuntimeProviderSwitchAvailable: false,
		ProviderSwitchBoundaries:       providerSwitchBoundaries(cfg != nil && cfg.SelfHealing.Enabled),
	}, nil
}

func isConfiguredProviderCandidate(cfg *config.Config, selected string) bool {
	selected = config.NormalizeProviderName(selected)
	if selected == "" {
		return false
	}
	for _, candidate := range configuredProviderCandidates(cfg) {
		if config.NormalizeProviderName(candidate.Provider) == selected {
			return true
		}
	}
	return false
}

func providerSwitchBoundaries(reflectionStartupBound bool) []string {
	boundaries := []string{
		providerSwitchBoundaryRestartRequired,
		providerSwitchBoundaryCompressionStartupBound,
	}
	if reflectionStartupBound {
		boundaries = append(boundaries, providerSwitchBoundaryReflectionStartupBound)
	}
	return boundaries
}

func formatProviderSwitchBoundary(boundaries []string) string {
	if len(boundaries) == 0 {
		return "Provider switching: unavailable."
	}
	if containsProviderSwitchBoundary(boundaries, providerSwitchBoundaryReflectionStartupBound) {
		return "Provider switching: restart required. Runtime skill provider resolution is dynamic, but reflection provider and compression budget remain startup-bound."
	}
	return "Provider switching: restart required. Runtime skill provider resolution is dynamic, and compression budget remains startup-bound."
}

func containsProviderSwitchBoundary(boundaries []string, want string) bool {
	for _, boundary := range boundaries {
		if boundary == want {
			return true
		}
	}
	return false
}

func sanitizeProviderBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "REDACTED_INVALID_URL"
	}
	u.User = nil
	q := u.Query()
	for key, values := range q {
		if !isSensitiveProviderURLQueryKey(key) {
			continue
		}
		for i := range values {
			values[i] = "REDACTED"
		}
		q[key] = values
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func isSensitiveProviderURLQueryKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{"api_key", "apikey", "key", "token", "secret", "password", "passwd", "auth", "bearer"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}

func autoEffortCompatible(providerEffort string) bool {
	switch providerEffort {
	case llm.ReasoningEffortNative, llm.ReasoningEffortNativeWithUnsupportedRetry:
		return true
	default:
		return false
	}
}

func loadProviderCommandConfig() (*config.Config, error) {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	applyGlobalFlagOverrides(cfg, os.Args)
	return cfg, nil
}
