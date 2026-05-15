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
	Ready                          bool                          `json:"ready"`
	FailureFamily                  string                        `json:"failure_family,omitempty"`
	Issues                         []string                      `json:"issues,omitempty"`
	Remediation                    []string                      `json:"remediation,omitempty"`
	ReasoningEffort                string                        `json:"reasoning_effort"`
	ReasoningEffortMode            string                        `json:"reasoning_effort_mode"`
	ConfiguredEffort               string                        `json:"configured_effort"`
	ProviderEffort                 string                        `json:"provider_effort"`
	ProviderEffortNote             string                        `json:"provider_effort_note,omitempty"`
	EffortCompatibility            string                        `json:"effort_compatibility"`
	AutoEffortCompatible           bool                          `json:"auto_effort_compatible"`
	RequestTimeoutSeconds          int                           `json:"request_timeout_seconds"`
	RuntimeProviderSwitchAvailable bool                          `json:"runtime_provider_switch_available"`
	ProviderSwitchBoundaries       []string                      `json:"provider_switch_boundaries,omitempty"`
	ConfiguredProviders            []providerConfigCandidateView `json:"configured_providers,omitempty"`
}

type providerConfigCandidateView struct {
	Provider              string   `json:"provider"`
	Model                 string   `json:"model"`
	BaseURL               string   `json:"base_url,omitempty"`
	ReasoningEffort       string   `json:"reasoning_effort,omitempty"`
	EffortCompatibility   string   `json:"effort_compatibility,omitempty"`
	EffortNote            string   `json:"effort_note,omitempty"`
	Ready                 bool     `json:"ready"`
	AuthConfigured        bool     `json:"auth_configured"`
	TimeoutStatus         string   `json:"timeout_status"`
	FailureFamily         string   `json:"failure_family,omitempty"`
	Issues                []string `json:"issues,omitempty"`
	Remediation           []string `json:"remediation,omitempty"`
	RequestTimeoutSeconds int      `json:"request_timeout_seconds"`
}

type providerSelectionCheckView struct {
	RequestedProvider              string   `json:"requested_provider"`
	PreviousProvider               string   `json:"previous_provider,omitempty"`
	RequestedProviderConfigured    bool     `json:"requested_provider_configured"`
	Ready                          bool     `json:"ready"`
	FailureFamily                  string   `json:"failure_family,omitempty"`
	Issues                         []string `json:"issues,omitempty"`
	Remediation                    []string `json:"remediation,omitempty"`
	Provider                       string   `json:"provider"`
	Model                          string   `json:"model"`
	ProviderEffort                 string   `json:"provider_effort"`
	ProviderEffortNote             string   `json:"provider_effort_note,omitempty"`
	EffortCompatibility            string   `json:"effort_compatibility"`
	AutoEffortCompatible           bool     `json:"auto_effort_compatible"`
	RequestTimeoutSeconds          int      `json:"request_timeout_seconds"`
	WouldSwitch                    bool     `json:"would_switch"`
	Switched                       bool     `json:"switched"`
	RuntimeProviderSwitchAvailable bool     `json:"runtime_provider_switch_available"`
	ProviderSwitchBoundaries       []string `json:"provider_switch_boundaries,omitempty"`
}

const (
	providerSwitchBoundaryRestartRequired         = "restart_required"
	providerSwitchBoundaryReflectionStartupBound  = "reflection_provider_startup_bound"
	providerSwitchBoundaryCompressionStartupBound = "compression_budget_startup_bound"
	providerSwitchBoundaryDaemonSharedRuntime     = "daemon_shared_runtime"
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
		if jsonOut {
			view := providerStatusFailureView(cfg, err)
			view.ConfiguredProviders = configuredProviderCandidates(cfg)
			if encodeErr := json.NewEncoder(os.Stdout).Encode(view); encodeErr != nil {
				return encodeErr
			}
		}
		return fmt.Errorf("provider status: build provider: %w", err)
	}
	caps := llm.CapabilitiesOf(provider)
	view := providerStatusView{
		Provider:                 caps.Name,
		Model:                    model,
		Ready:                    true,
		ReasoningEffort:          caps.ReasoningEffort,
		ReasoningEffortMode:      cfg.Reasoning.EffortMode,
		ConfiguredEffort:         cfg.Reasoning.Effort,
		ProviderEffort:           caps.ReasoningEffort,
		ProviderEffortNote:       caps.ReasoningEffortFallback,
		EffortCompatibility:      caps.ReasoningEffort,
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

func providerStatusFailureView(cfg *config.Config, err error) providerStatusView {
	providerName := "unconfigured"
	if cfg != nil && strings.TrimSpace(cfg.Provider) != "" {
		providerName = config.NormalizeProviderName(cfg.Provider)
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	family := "provider_error"
	issues := []string{message}
	remediation := []string{"run elnath provider candidates --json and configure the requested provider before starting a session"}
	if strings.Contains(message, "no LLM provider configured") || strings.Contains(message, "not configured") {
		family = "auth_required"
		issues = []string{"No usable LLM provider is configured. Set ELNATH_OPENAI_RESPONSES_API_KEY, ELNATH_RESPONSES_API_KEY, ELNATH_OPENAI_API_KEY, or ELNATH_ANTHROPIC_API_KEY."}
		remediation = []string{
			"for OpenAI Responses-compatible providers, set openai_responses.api_key or ELNATH_OPENAI_RESPONSES_API_KEY",
			"set provider: openai_responses when you want Responses-compatible routing to win explicitly",
		}
	}
	return providerStatusView{
		Provider:                 providerName,
		Ready:                    false,
		FailureFamily:            family,
		Issues:                   issues,
		Remediation:              remediation,
		ReasoningEffortMode:      configString(cfg, func(c *config.Config) string { return c.Reasoning.EffortMode }),
		ConfiguredEffort:         configString(cfg, func(c *config.Config) string { return c.Reasoning.Effort }),
		ProviderSwitchBoundaries: providerSwitchBoundaries(cfg != nil && cfg.SelfHealing.Enabled),
	}
}

func configString(cfg *config.Config, fn func(*config.Config) string) string {
	if cfg == nil || fn == nil {
		return ""
	}
	return fn(cfg)
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
	view, err := providerSelectionDiagnosticViewForConfig(cfg, providerName)
	if err != nil {
		return fmt.Errorf("provider check: %w", err)
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		if err := enc.Encode(view); err != nil {
			return err
		}
		if !view.Ready {
			return fmt.Errorf("provider check: %s", strings.Join(view.Issues, "; "))
		}
		return nil
	}
	if !view.Ready {
		return fmt.Errorf("provider check: %s", strings.Join(view.Issues, "; "))
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
			EffortCompatibility:   llm.ReasoningEffortNativeWithUnsupportedRetry,
			EffortNote:            "retry_without_reasoning_on_400_or_422_unsupported_effort",
			Ready:                 cfg.OpenAIResponses.APIKey != "" && cfg.OpenAIResponses.Timeout > 0,
			AuthConfigured:        cfg.OpenAIResponses.APIKey != "",
			TimeoutStatus:         providerTimeoutStatus(cfg.OpenAIResponses.Timeout),
			FailureFamily:         providerConfigFailureFamily(cfg.OpenAIResponses.APIKey != "", cfg.OpenAIResponses.Timeout),
			Issues:                providerConfigIssues("openai_responses", cfg.OpenAIResponses.APIKey != "", cfg.OpenAIResponses.Timeout),
			Remediation:           providerConfigRemediation("openai_responses", cfg.OpenAIResponses.APIKey != "", cfg.OpenAIResponses.Timeout),
			RequestTimeoutSeconds: cfg.OpenAIResponses.Timeout,
		})
	}
	if cfg.Anthropic.APIKey != "" {
		out = append(out, providerConfigCandidateView{
			Provider:              "anthropic",
			Model:                 providerConfigModel(cfg.Anthropic.Model, "claude-sonnet-4-6"),
			BaseURL:               sanitizeProviderBaseURL(cfg.Anthropic.BaseURL),
			ReasoningEffort:       cfg.Anthropic.ReasoningEffort,
			EffortCompatibility:   llm.ReasoningEffortThinkingBudgetOnly,
			EffortNote:            "chat_request_reasoning_effort_not_mapped_to_anthropic_thinking_budget",
			Ready:                 cfg.Anthropic.APIKey != "" && cfg.Anthropic.Timeout > 0,
			AuthConfigured:        cfg.Anthropic.APIKey != "",
			TimeoutStatus:         providerTimeoutStatus(cfg.Anthropic.Timeout),
			FailureFamily:         providerConfigFailureFamily(cfg.Anthropic.APIKey != "", cfg.Anthropic.Timeout),
			Issues:                providerConfigIssues("anthropic", cfg.Anthropic.APIKey != "", cfg.Anthropic.Timeout),
			Remediation:           providerConfigRemediation("anthropic", cfg.Anthropic.APIKey != "", cfg.Anthropic.Timeout),
			RequestTimeoutSeconds: cfg.Anthropic.Timeout,
		})
	}
	if cfg.OpenAI.APIKey != "" {
		out = append(out, providerConfigCandidateView{
			Provider:              "openai",
			Model:                 providerConfigModel(cfg.OpenAI.Model, resolveFallbackModel(cfg)),
			BaseURL:               sanitizeProviderBaseURL(cfg.OpenAI.BaseURL),
			ReasoningEffort:       cfg.OpenAI.ReasoningEffort,
			EffortCompatibility:   llm.ReasoningEffortIgnored,
			Ready:                 cfg.OpenAI.APIKey != "" && cfg.OpenAI.Timeout > 0,
			AuthConfigured:        cfg.OpenAI.APIKey != "",
			TimeoutStatus:         providerTimeoutStatus(cfg.OpenAI.Timeout),
			FailureFamily:         providerConfigFailureFamily(cfg.OpenAI.APIKey != "", cfg.OpenAI.Timeout),
			Issues:                providerConfigIssues("openai", cfg.OpenAI.APIKey != "", cfg.OpenAI.Timeout),
			Remediation:           providerConfigRemediation("openai", cfg.OpenAI.APIKey != "", cfg.OpenAI.Timeout),
			RequestTimeoutSeconds: cfg.OpenAI.Timeout,
		})
	}
	if cfg.Ollama.Model != "" || cfg.Ollama.BaseURL != "" {
		out = append(out, providerConfigCandidateView{
			Provider:              "ollama",
			Model:                 providerConfigModel(cfg.Ollama.Model, "llama3.2"),
			BaseURL:               sanitizeProviderBaseURL(cfg.Ollama.BaseURL),
			EffortCompatibility:   llm.ReasoningEffortIgnored,
			Ready:                 cfg.Ollama.Model != "" || cfg.Ollama.BaseURL != "",
			AuthConfigured:        true,
			TimeoutStatus:         "not_applicable",
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
	view, err := providerSelectionDiagnosticViewForConfig(cfg, providerName)
	if err != nil {
		return providerSelectionCheckView{}, err
	}
	if !view.Ready {
		return providerSelectionCheckView{}, fmt.Errorf("%s", strings.Join(view.Issues, "; "))
	}
	return view, nil
}

func providerSelectionDiagnosticViewForConfig(cfg *config.Config, providerName string) (providerSelectionCheckView, error) {
	selected := config.NormalizeProviderName(providerName)
	if selected == "" {
		return providerSelectionCheckView{}, fmt.Errorf("provider name is required")
	}
	if !isConfiguredProviderCandidate(cfg, selected) {
		view := providerUnconfiguredSelectionDiagnostic(cfg, selected)
		if len(view.Issues) == 0 {
			return providerSelectionCheckView{}, fmt.Errorf("provider %q is not a configured provider candidate", selected)
		}
		return view, nil
	}
	provider, model, err := buildProviderForSelection(cfg, selected)
	if err != nil {
		return providerSelectionCheckView{}, err
	}
	caps := llm.CapabilitiesOf(provider)
	return providerSelectionCheckView{
		RequestedProvider:              selected,
		RequestedProviderConfigured:    true,
		Ready:                          true,
		Provider:                       caps.Name,
		Model:                          model,
		ProviderEffort:                 caps.ReasoningEffort,
		ProviderEffortNote:             caps.ReasoningEffortFallback,
		EffortCompatibility:            caps.ReasoningEffort,
		AutoEffortCompatible:           autoEffortCompatible(caps.ReasoningEffort),
		RequestTimeoutSeconds:          caps.RequestTimeoutSeconds,
		WouldSwitch:                    false,
		RuntimeProviderSwitchAvailable: false,
		ProviderSwitchBoundaries:       providerSwitchBoundaries(cfg != nil && cfg.SelfHealing.Enabled),
	}, nil
}

func providerUnconfiguredSelectionDiagnostic(cfg *config.Config, selected string) providerSelectionCheckView {
	view := providerSelectionCheckView{
		RequestedProvider:              selected,
		RequestedProviderConfigured:    false,
		Ready:                          false,
		Provider:                       selected,
		ProviderSwitchBoundaries:       providerSwitchBoundaries(cfg != nil && cfg.SelfHealing.Enabled),
		RuntimeProviderSwitchAvailable: false,
	}
	switch selected {
	case "openai-responses":
		timeout := 0
		authConfigured := false
		if cfg != nil {
			timeout = cfg.OpenAIResponses.Timeout
			authConfigured = cfg.OpenAIResponses.APIKey != ""
			view.Model = providerConfigModel(cfg.OpenAIResponses.Model, resolveFallbackModel(cfg))
			view.ProviderEffort = llm.ReasoningEffortNativeWithUnsupportedRetry
			view.ProviderEffortNote = "retry_without_reasoning_on_400_or_422_unsupported_effort"
			view.EffortCompatibility = llm.ReasoningEffortNativeWithUnsupportedRetry
			view.AutoEffortCompatible = true
			view.RequestTimeoutSeconds = timeout
		}
		view.FailureFamily = providerConfigFailureFamily(authConfigured, timeout)
		view.Issues = providerConfigIssues("openai_responses", authConfigured, timeout)
		view.Remediation = providerConfigRemediation("openai_responses", authConfigured, timeout)
	case "anthropic":
		timeout := 0
		authConfigured := false
		if cfg != nil {
			timeout = cfg.Anthropic.Timeout
			authConfigured = cfg.Anthropic.APIKey != ""
			view.Model = providerConfigModel(cfg.Anthropic.Model, "claude-sonnet-4-6")
			view.ProviderEffort = llm.ReasoningEffortThinkingBudgetOnly
			view.ProviderEffortNote = "chat_request_reasoning_effort_not_mapped_to_anthropic_thinking_budget"
			view.EffortCompatibility = llm.ReasoningEffortThinkingBudgetOnly
			view.RequestTimeoutSeconds = timeout
		}
		view.FailureFamily = providerConfigFailureFamily(authConfigured, timeout)
		view.Issues = providerConfigIssues("anthropic", authConfigured, timeout)
		view.Remediation = providerConfigRemediation("anthropic", authConfigured, timeout)
	case "openai":
		timeout := 0
		authConfigured := false
		if cfg != nil {
			timeout = cfg.OpenAI.Timeout
			authConfigured = cfg.OpenAI.APIKey != ""
			view.Model = providerConfigModel(cfg.OpenAI.Model, resolveFallbackModel(cfg))
			view.ProviderEffort = llm.ReasoningEffortIgnored
			view.EffortCompatibility = llm.ReasoningEffortIgnored
			view.RequestTimeoutSeconds = timeout
		}
		view.FailureFamily = providerConfigFailureFamily(authConfigured, timeout)
		view.Issues = providerConfigIssues("openai", authConfigured, timeout)
		view.Remediation = providerConfigRemediation("openai", authConfigured, timeout)
	}
	return view
}

func providerConfigFailureFamily(authConfigured bool, timeout int) string {
	switch {
	case !authConfigured:
		return "auth_required"
	case timeout <= 0:
		return "provider_timeout_invalid"
	default:
		return ""
	}
}

func providerTimeoutStatus(timeout int) string {
	if timeout <= 0 {
		return "invalid"
	}
	return "ok"
}

func providerConfigIssues(prefix string, authConfigured bool, timeout int) []string {
	var issues []string
	if !authConfigured {
		issues = append(issues, prefix+".api_key is not configured")
	}
	if timeout <= 0 {
		issues = append(issues, prefix+".timeout_seconds must be positive")
	}
	return issues
}

func providerConfigRemediation(prefix string, authConfigured bool, timeout int) []string {
	var remediation []string
	if !authConfigured {
		remediation = append(remediation, "set "+prefix+".api_key or the matching ELNATH_* API key environment variable")
	}
	if timeout <= 0 {
		remediation = append(remediation, "set "+prefix+".timeout_seconds to a positive value")
	}
	return remediation
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
	reflectionBound := containsProviderSwitchBoundary(boundaries, providerSwitchBoundaryReflectionStartupBound)
	compressionBound := containsProviderSwitchBoundary(boundaries, providerSwitchBoundaryCompressionStartupBound)
	daemonBound := containsProviderSwitchBoundary(boundaries, providerSwitchBoundaryDaemonSharedRuntime)
	switch {
	case daemonBound:
		return "Provider switching: restart required. Daemon mode uses a shared runtime, so provider changes must be made in config before daemon start."
	case reflectionBound && compressionBound:
		return "Provider switching: restart required. Runtime skill provider resolution is dynamic, but reflection provider and compression budget remain startup-bound."
	case reflectionBound:
		return "Provider switching: restart required. Runtime skill provider resolution is dynamic, but reflection provider remains startup-bound."
	case compressionBound:
		return "Provider switching: restart required. Runtime skill provider resolution is dynamic, but compression budget remains startup-bound."
	default:
		return "Provider switching: restart required."
	}
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
