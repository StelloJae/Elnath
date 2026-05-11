package main

import (
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

const providerCommandUsage = "Usage: /provider [status|help]"

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
	if len(args) > 1 {
		return fmt.Sprintf("Invalid provider argument: %s. %s", strings.Join(args, " "), providerCommandUsage)
	}

	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "help", "-h", "--help":
		return providerCommandUsage
	case "current", "status":
		return rt.currentProviderMessage()
	default:
		return "Runtime provider switching is not available in this session. Set provider in config.yaml or ELNATH_PROVIDER, then restart Elnath."
	}
}

func (rt *executionRuntime) currentProviderMessage() string {
	caps := llm.CapabilitiesOf(rt.provider)
	msg := fmt.Sprintf("Provider: %s. Reasoning effort: %s.", caps.Name, caps.ReasoningEffort)
	if caps.ReasoningEffortFallback != "" {
		msg += " Fallback: " + caps.ReasoningEffortFallback + "."
	}
	return msg
}
