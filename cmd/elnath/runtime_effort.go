package main

import (
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

const effortCommandUsage = "Usage: /effort [none|minimal|low|medium|high|xhigh|max|auto|status]"

func (rt *executionRuntime) tryEffortCommand(
	sess *agent.Session,
	messages []llm.Message,
	input string,
	bus *event.Bus,
) ([]llm.Message, string, bool, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 || fields[0] != "/effort" {
		return nil, "", false, nil
	}

	summary := rt.applyEffortCommand(fields[1:])
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

func (rt *executionRuntime) applyEffortCommand(args []string) string {
	if len(args) == 0 {
		return rt.currentEffortMessage()
	}

	arg := strings.ToLower(strings.TrimSpace(args[0]))
	if len(args) > 1 {
		return fmt.Sprintf("Invalid effort argument: %s. %s", strings.Join(args, " "), effortCommandUsage)
	}

	switch arg {
	case "help", "-h", "--help":
		return effortCommandUsage
	case "current", "status":
		return rt.currentEffortMessage()
	case "auto", "unset":
		rt.wfCfg.ReasoningEffortMode = "auto"
		rt.wfCfg.ReasoningEffort = ""
		return "Effort level set to auto. Elnath will route effort per task."
	case "max":
		rt.wfCfg.ReasoningEffortMode = "manual"
		rt.wfCfg.ReasoningEffort = "xhigh"
		return "Effort level set to xhigh (max alias) for this session."
	case "none", "minimal", "low", "medium", "high", "xhigh":
		rt.wfCfg.ReasoningEffortMode = "manual"
		rt.wfCfg.ReasoningEffort = arg
		return fmt.Sprintf("Effort level set to %s for this session.", arg)
	default:
		return fmt.Sprintf("Invalid effort argument: %s. Valid options are: none, minimal, low, medium, high, xhigh, max, auto", arg)
	}
}

func (rt *executionRuntime) currentEffortMessage() string {
	mode := strings.ToLower(strings.TrimSpace(rt.wfCfg.ReasoningEffortMode))
	effort := strings.ToLower(strings.TrimSpace(rt.wfCfg.ReasoningEffort))
	if mode == "auto" {
		policy := strings.Join([]string{autoEffortPolicyMessage(), rt.providerEffortStatusMessage()}, "\n")
		if effort != "" {
			return fmt.Sprintf("Effort level: auto (fallback %s).\n%s", effort, policy)
		}
		return fmt.Sprintf("Effort level: auto.\n%s", policy)
	}
	if effort == "" {
		return "Effort level: provider default."
	}
	return fmt.Sprintf("Current effort level: %s.", effort)
}

func autoEffortPolicyMessage() string {
	return strings.Join([]string{
		"Auto routing policy:",
		"- simple/status/progress/summary -> low",
		"- implementation/debug/benchmark/CI -> high",
		"- root-cause/security/architecture/autonomous -> xhigh",
		"- otherwise -> medium",
		"Auto routing is heuristic; use manual override when precision matters.",
		"Skill metadata effort overrides auto for that skill.",
		"Manual override: /effort <level>",
	}, "\n")
}

func (rt *executionRuntime) providerEffortStatusMessage() string {
	caps := llm.CapabilitiesOf(rt.provider)
	lines := []string{
		fmt.Sprintf("Provider effort capability: %s", caps.ReasoningEffort),
	}
	if strings.TrimSpace(caps.ReasoningEffortFallback) != "" {
		lines = append(lines, fmt.Sprintf("Provider effort note: %s", caps.ReasoningEffortFallback))
	}
	lines = append(lines, fmt.Sprintf("Auto effort compatible: %t", autoEffortCompatible(caps.ReasoningEffort)))
	return strings.Join(lines, "\n")
}
