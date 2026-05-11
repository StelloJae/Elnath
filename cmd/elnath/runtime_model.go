package main

import (
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

const modelCommandUsage = "Usage: /model [model-id|default|status]"

func (rt *executionRuntime) tryModelCommand(
	sess *agent.Session,
	messages []llm.Message,
	input string,
	bus *event.Bus,
) ([]llm.Message, string, bool, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 || fields[0] != "/model" {
		return nil, "", false, nil
	}

	summary := rt.applyModelCommand(fields[1:])
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

func (rt *executionRuntime) applyModelCommand(args []string) string {
	if len(args) == 0 {
		return rt.currentModelMessage()
	}

	arg := strings.TrimSpace(args[0])
	if len(args) > 1 {
		return fmt.Sprintf("Invalid model argument: %s. %s", strings.Join(args, " "), modelCommandUsage)
	}

	switch strings.ToLower(arg) {
	case "help", "-h", "--help":
		return modelCommandUsage
	case "current", "status":
		return rt.currentModelMessage()
	case "default", "provider-default", "unset":
		rt.wfCfg.Model = ""
		return fmt.Sprintf("Model set to provider default for this session. Provider: %s.", rt.providerName())
	default:
		rt.wfCfg.Model = arg
		return fmt.Sprintf("Model set to %s for this session. Provider: %s.", arg, rt.providerName())
	}
}

func (rt *executionRuntime) currentModelMessage() string {
	model := strings.TrimSpace(rt.wfCfg.Model)
	if model == "" {
		return fmt.Sprintf("Model: provider default. Provider: %s.", rt.providerName())
	}
	return fmt.Sprintf("Current model: %s. Provider: %s.", model, rt.providerName())
}

func (rt *executionRuntime) providerName() string {
	if rt == nil || rt.provider == nil {
		return "unknown"
	}
	return rt.provider.Name()
}
