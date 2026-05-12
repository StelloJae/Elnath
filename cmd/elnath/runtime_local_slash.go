package main

import (
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

type localSlashCommandSpec struct {
	Name        string
	Description string
}

func runtimeLocalSlashCommandSpecs() []localSlashCommandSpec {
	return []localSlashCommandSpec{
		{Name: "/effort", Description: "Inspect or set session reasoning effort."},
		{Name: "/model", Description: "Inspect or set the session request model."},
		{Name: "/provider", Description: "Inspect active provider capabilities, configured candidates, and safe candidate checks."},
		{Name: "/commands", Description: "List local command catalog entries."},
		{Name: "/help", Description: "Alias for the local command catalog."},
		{Name: "/skills", Description: "List registered skills without executing them."},
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
	default:
		return nil, "", false, nil
	}
}
