package main

import (
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

type localSlashCommandSpec struct {
	Name         string
	Description  string
	ArgumentHint string
}

func runtimeLocalSlashCommandSpecs() []localSlashCommandSpec {
	return []localSlashCommandSpec{
		{Name: "/effort", Description: "Inspect or set session reasoning effort.", ArgumentHint: "[auto|none|minimal|low|medium|high|xhigh|max|status]"},
		{Name: "/model", Description: "Inspect or set the session request model.", ArgumentHint: "[status|default|unset|<model>] [--json]"},
		{Name: "/provider", Description: "Inspect or switch active provider capabilities and configured candidates.", ArgumentHint: "status|candidates|check <provider>|use <provider> [--json]"},
		{Name: "/commands", Description: "List local command catalog entries.", ArgumentHint: "[--json] [--all]"},
		{Name: "/help", Description: "Alias for the local command catalog.", ArgumentHint: "[--json] [--all]"},
		{Name: "/skills", Description: "List registered skills without executing them.", ArgumentHint: "[--json]"},
		{Name: "/version", Description: "Print the Elnath version for this session.", ArgumentHint: "[--json]"},
		{Name: "/status", Description: "Show local runtime session status.", ArgumentHint: "[--json]"},
		{Name: "/plan", Description: "Enter, inspect, or exit local planning mode.", ArgumentHint: "[status|exit|<description>]"},
	}
}

func (rt *executionRuntime) tryLocalSlashCommand(
	sess *agent.Session,
	messages []llm.Message,
	input string,
	bus *event.Bus,
) ([]llm.Message, string, bool, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return nil, "", false, nil
	}

	switch fields[0] {
	case "/effort":
		return rt.tryEffortCommand(sess, messages, input, bus)
	case "/model":
		return rt.tryModelCommand(sess, messages, input, bus)
	case "/provider":
		return rt.tryProviderCommand(sess, messages, input, bus)
	case "/commands", "/help":
		return rt.tryCommandsCommand(sess, messages, input, bus)
	case "/skills":
		return rt.trySkillsCommand(sess, messages, input, bus)
	case "/version":
		return rt.tryVersionCommand(sess, messages, input, bus)
	case "/status":
		return rt.tryStatusCommand(sess, messages, input, bus)
	case "/plan":
		return rt.tryPlanCommand(sess, messages, input, bus)
	default:
		return nil, "", false, nil
	}
}
