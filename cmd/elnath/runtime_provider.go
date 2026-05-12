package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

const providerCommandUsage = "Usage: /provider [status [--json]|candidates [--json]|check <provider> [--json]|use <provider> [--json]|help]"

func (rt *executionRuntime) tryProviderCommand(
	sess *agent.Session,
	messages []llm.Message,
	input string,
	bus *event.Bus,
) ([]llm.Message, string, bool, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 || fields[0] != "/provider" {
		return nil, "", false, nil
	}

	summary := rt.applyProviderCommand(fields[1:])
	if bus != nil {
		bus.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: summary + "\n"})
	}

	delta := []llm.Message{
		llm.NewUserMessage(input),
		llm.NewAssistantMessage(summary),
	}
	updated := append(messages, delta...)
	if sess != nil {
		if err := sess.AppendMessages(delta); err != nil {
			rt.app.Logger.Warn("session persist failed", "error", err)
		}
		sess.Messages = updated
	}
	return updated, summary, true, nil
}

func (rt *executionRuntime) applyProviderCommand(args []string) string {
	if len(args) == 0 {
		return rt.currentProviderMessage()
	}

	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "help", "-h", "--help":
		if len(args) > 1 {
			return invalidProviderArgument(args)
		}
		return providerCommandUsage
	case "--json":
		if len(args) > 1 {
			return invalidProviderArgument(args)
		}
		return rt.currentProviderJSONMessage()
	case "current", "status":
		return rt.applyProviderStatusCommand(args[1:])
	case "candidates":
		return rt.applyProviderCandidatesCommand(args[1:])
	case "check":
		return rt.applyProviderCheckCommand(args[1:])
	case "use", "switch":
		return rt.applyProviderUseCommand(args[1:])
	default:
		return "Runtime provider switching is not available in this session. Set provider in config.yaml or ELNATH_PROVIDER, then restart Elnath."
	}
}

func (rt *executionRuntime) applyProviderStatusCommand(args []string) string {
	if len(args) == 0 {
		return rt.currentProviderMessage()
	}
	if len(args) == 1 && strings.ToLower(strings.TrimSpace(args[0])) == "--json" {
		return rt.currentProviderJSONMessage()
	}
	return invalidProviderArgument(args)
}

func (rt *executionRuntime) applyProviderCandidatesCommand(args []string) string {
	if len(args) == 0 {
		return formatProviderCandidates(configuredProviderCandidates(rt.currentConfig()))
	}
	if len(args) == 1 && strings.ToLower(strings.TrimSpace(args[0])) == "--json" {
		raw, err := json.MarshalIndent(configuredProviderCandidates(rt.currentConfig()), "", "  ")
		if err != nil {
			return fmt.Sprintf("provider candidates: marshal JSON: %v", err)
		}
		return string(raw)
	}
	return invalidProviderArgument(args)
}

func (rt *executionRuntime) applyProviderCheckCommand(args []string) string {
	if len(args) == 0 || len(args) > 2 {
		return invalidProviderArgument(args)
	}
	jsonOut := false
	providerName := ""
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if strings.EqualFold(arg, "--json") {
			jsonOut = true
			continue
		}
		if providerName != "" {
			return invalidProviderArgument(args)
		}
		providerName = arg
	}
	if providerName == "" {
		return invalidProviderArgument(args)
	}

	view, err := rt.providerSelectionCheckView(providerName)
	if err != nil {
		return fmt.Sprintf("Provider check failed: %v", err)
	}
	if jsonOut {
		raw, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return fmt.Sprintf("provider check: marshal JSON: %v", err)
		}
		return string(raw)
	}
	msg := fmt.Sprintf("Provider check: %s configured. Model: %s. Reasoning effort: %s. Request timeout: %ds.",
		view.Provider,
		view.Model,
		view.ProviderEffort,
		view.RequestTimeoutSeconds,
	)
	if view.ProviderEffortNote != "" {
		msg += " Fallback: " + view.ProviderEffortNote + "."
	}
	msg += " This does not switch the current session."
	msg += "\n" + formatRuntimeProviderSwitchBoundary(view.RuntimeProviderSwitchAvailable, view.ProviderSwitchBoundaries)
	return msg
}

func (rt *executionRuntime) providerSelectionCheckView(providerName string) (providerSelectionCheckView, error) {
	view, err := providerSelectionCheckViewForConfig(rt.currentConfig(), providerName)
	if err != nil {
		return providerSelectionCheckView{}, err
	}
	view.WouldSwitch = config.NormalizeProviderName(view.Provider) != config.NormalizeProviderName(rt.providerName())
	view.RuntimeProviderSwitchAvailable = rt.runtimeProviderSwitchAvailable()
	view.ProviderSwitchBoundaries = rt.runtimeProviderSwitchBoundaries()
	return view, nil
}

func (rt *executionRuntime) applyProviderUseCommand(args []string) string {
	if len(args) == 0 || len(args) > 2 {
		return invalidProviderArgument(args)
	}
	jsonOut := false
	providerName := ""
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if strings.EqualFold(arg, "--json") {
			jsonOut = true
			continue
		}
		if providerName != "" {
			return invalidProviderArgument(args)
		}
		providerName = arg
	}
	if providerName == "" {
		return invalidProviderArgument(args)
	}

	view, err := rt.switchProviderForSession(providerName)
	if err != nil {
		return fmt.Sprintf("Provider switch failed: %v\n%s", err, formatRuntimeProviderSwitchBoundary(false, rt.runtimeProviderSwitchBoundaries()))
	}
	if jsonOut {
		raw, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return fmt.Sprintf("provider use: marshal JSON: %v", err)
		}
		return string(raw)
	}
	msg := fmt.Sprintf("Provider switched: %s -> %s. Model: %s. Reasoning effort: %s. Request timeout: %ds.",
		view.PreviousProvider,
		view.Provider,
		view.Model,
		view.ProviderEffort,
		view.RequestTimeoutSeconds,
	)
	if view.ProviderEffortNote != "" {
		msg += " Fallback: " + view.ProviderEffortNote + "."
	}
	return msg
}

func (rt *executionRuntime) switchProviderForSession(providerName string) (providerSelectionCheckView, error) {
	view, err := rt.providerSelectionCheckView(providerName)
	if err != nil {
		return providerSelectionCheckView{}, err
	}
	if !rt.runtimeProviderSwitchAvailable() {
		return view, fmt.Errorf("runtime provider switch unavailable")
	}
	provider, model, err := buildProviderForSelection(rt.currentConfig(), providerName)
	if err != nil {
		return providerSelectionCheckView{}, err
	}
	previous := rt.providerName()
	rt.provider = provider
	rt.wfCfg.Model = model
	providerCW := resolveProviderContextWindow(provider, model)
	rt.wfCfg.CompressionMaxTokens = conversation.ResolveCompressionBudget(providerCW, rt.currentConfig().MaxContextTokens)
	if rt.mgr != nil {
		rt.mgr.WithProvider(provider).WithProviderContextWindow(providerCW)
	}

	caps := llm.CapabilitiesOf(provider)
	return providerSelectionCheckView{
		RequestedProvider:              config.NormalizeProviderName(providerName),
		PreviousProvider:               previous,
		Provider:                       caps.Name,
		Model:                          model,
		ProviderEffort:                 caps.ReasoningEffort,
		ProviderEffortNote:             caps.ReasoningEffortFallback,
		AutoEffortCompatible:           autoEffortCompatible(caps.ReasoningEffort),
		RequestTimeoutSeconds:          caps.RequestTimeoutSeconds,
		WouldSwitch:                    false,
		Switched:                       config.NormalizeProviderName(previous) != config.NormalizeProviderName(caps.Name),
		RuntimeProviderSwitchAvailable: true,
	}, nil
}

func (rt *executionRuntime) runtimeProviderSwitchAvailable() bool {
	return rt != nil && rt.reflectPool == nil
}

func (rt *executionRuntime) runtimeProviderSwitchBoundaries() []string {
	if rt == nil {
		return providerSwitchBoundaries(false)
	}
	if rt.reflectPool != nil {
		return []string{providerSwitchBoundaryRestartRequired, providerSwitchBoundaryReflectionStartupBound}
	}
	return nil
}

func (rt *executionRuntime) currentProviderMessage() string {
	view := rt.currentProviderStatusView()
	msg := fmt.Sprintf("Provider: %s. Model: %s. Reasoning effort: %s.", view.Provider, view.Model, view.ProviderEffort)
	if view.ProviderEffortNote != "" {
		msg += " Fallback: " + view.ProviderEffortNote + "."
	}
	msg += fmt.Sprintf(" Auto effort compatible: %t. Request timeout: %ds.", view.AutoEffortCompatible, view.RequestTimeoutSeconds)
	if len(view.ConfiguredProviders) > 0 {
		msg += "\n" + formatProviderCandidates(view.ConfiguredProviders)
	}
	msg += "\n" + formatRuntimeProviderSwitchBoundary(view.RuntimeProviderSwitchAvailable, view.ProviderSwitchBoundaries)
	msg += " Use /model and /effort for in-session overrides."
	return msg
}

func (rt *executionRuntime) currentProviderJSONMessage() string {
	raw, err := json.MarshalIndent(rt.currentProviderStatusView(), "", "  ")
	if err != nil {
		return fmt.Sprintf("provider status: marshal JSON: %v", err)
	}
	return string(raw)
}

func (rt *executionRuntime) currentProviderStatusView() providerStatusView {
	cfg := rt.currentConfig()
	caps := llm.CapabilitiesOf(rt.provider)
	model := strings.TrimSpace(rt.wfCfg.Model)
	if model == "" {
		model = "provider default"
	}
	view := providerStatusView{
		Provider:                       caps.Name,
		Model:                          model,
		ReasoningEffort:                caps.ReasoningEffort,
		ReasoningEffortMode:            rt.wfCfg.ReasoningEffortMode,
		ConfiguredEffort:               rt.wfCfg.ReasoningEffort,
		ProviderEffort:                 caps.ReasoningEffort,
		ProviderEffortNote:             caps.ReasoningEffortFallback,
		AutoEffortCompatible:           autoEffortCompatible(caps.ReasoningEffort),
		RequestTimeoutSeconds:          caps.RequestTimeoutSeconds,
		RuntimeProviderSwitchAvailable: rt.runtimeProviderSwitchAvailable(),
		ProviderSwitchBoundaries:       rt.runtimeProviderSwitchBoundaries(),
		ConfiguredProviders:            configuredProviderCandidates(cfg),
	}
	return view
}

func (rt *executionRuntime) currentConfig() *config.Config {
	if rt == nil || rt.app == nil {
		return nil
	}
	return rt.app.Config
}

func formatProviderCandidates(candidates []providerConfigCandidateView) string {
	if len(candidates) == 0 {
		return "Configured providers: none found in current config."
	}
	var b strings.Builder
	b.WriteString("Configured providers:")
	for _, candidate := range candidates {
		base := ""
		if candidate.BaseURL != "" {
			base = " base_url=" + candidate.BaseURL
		}
		effort := ""
		if candidate.ReasoningEffort != "" {
			effort = " effort=" + candidate.ReasoningEffort
		}
		fmt.Fprintf(&b, "\n  - %s model=%s timeout=%ds%s%s",
			candidate.Provider,
			candidate.Model,
			candidate.RequestTimeoutSeconds,
			base,
			effort,
		)
	}
	return b.String()
}

func invalidProviderArgument(args []string) string {
	return fmt.Sprintf("Invalid provider argument: %s. %s", strings.Join(args, " "), providerCommandUsage)
}

func formatRuntimeProviderSwitchBoundary(available bool, boundaries []string) string {
	if available {
		return "Provider switching: available with /provider use <provider>."
	}
	return formatProviderSwitchBoundary(boundaries)
}
