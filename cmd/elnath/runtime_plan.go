package main

import (
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

const planCommandUsage = "Usage: /plan [status|exit|<description>]"

func (rt *executionRuntime) tryPlanCommand(
	sess *agent.Session,
	messages []llm.Message,
	input string,
	bus *event.Bus,
) ([]llm.Message, string, bool, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 || fields[0] != "/plan" {
		return nil, "", false, nil
	}

	summary := rt.applyPlanCommand(fields[1:])
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

func (rt *executionRuntime) applyPlanCommand(args []string) string {
	if len(args) == 1 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "help", "-h", "--help":
			return planCommandUsage
		case "current", "status":
			return rt.currentPlanModeMessage()
		case "exit", "off":
			return rt.exitPlanModeMessage()
		}
	}
	if len(args) > 0 && strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		return fmt.Sprintf("Invalid plan argument: %s. %s", strings.Join(args, " "), planCommandUsage)
	}
	return rt.enterPlanModeMessage(strings.Join(args, " "))
}

func (rt *executionRuntime) enterPlanModeMessage(description string) string {
	if rt == nil || rt.planModeController == nil {
		return "Plan mode controller unavailable."
	}
	previous, current := rt.planModeController.Enter()
	lines := []string{
		"Entered plan mode.",
		fmt.Sprintf("previous_mode: %s", previous.String()),
		fmt.Sprintf("current_mode: %s", current.String()),
		"Implementation tools are now limited to read-only planning surfaces until plan mode is exited.",
	}
	if description = strings.TrimSpace(description); description != "" {
		lines = append(lines, "plan_prompt: "+description)
	}
	return strings.Join(lines, "\n")
}

func (rt *executionRuntime) exitPlanModeMessage() string {
	if rt == nil || rt.planModeController == nil {
		return "Plan mode controller unavailable."
	}
	previous, current, restored := rt.planModeController.Exit()
	if !restored {
		return fmt.Sprintf("Plan mode was not active. current_mode: %s", current.String())
	}
	return strings.Join([]string{
		"Exited plan mode.",
		fmt.Sprintf("restored_mode: %s", previous.String()),
		fmt.Sprintf("current_mode: %s", current.String()),
	}, "\n")
}

func (rt *executionRuntime) currentPlanModeMessage() string {
	mode := agent.ModeDefault
	if rt != nil && rt.wfCfg.Permission != nil {
		mode = rt.wfCfg.Permission.Mode()
	}
	if mode == agent.ModePlan {
		return "Plan mode status: active"
	}
	return fmt.Sprintf("Plan mode status: inactive (current_mode: %s)", mode.String())
}
