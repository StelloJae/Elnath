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
	Handler      localSlashCommandHandler
}

type localSlashCommandHandler func(
	rt *executionRuntime,
	sess *agent.Session,
	messages []llm.Message,
	input string,
	bus *event.Bus,
) ([]llm.Message, string, bool, error)

func runtimeLocalSlashCommandSpecs() []localSlashCommandSpec {
	return []localSlashCommandSpec{
		{Name: "/effort", Description: "Inspect or set session reasoning effort.", ArgumentHint: "[auto|none|minimal|low|medium|high|xhigh|max|status]", Handler: (*executionRuntime).tryEffortCommand},
		{Name: "/model", Description: "Inspect or set the session request model.", ArgumentHint: "[status|default|unset|<model>] [--json]", Handler: (*executionRuntime).tryModelCommand},
		{Name: "/provider", Description: "Inspect or switch active provider capabilities and configured candidates.", ArgumentHint: "status|route|candidates|check <provider>|use <provider> [--json]", Handler: (*executionRuntime).tryProviderCommand},
		{Name: "/commands", Description: "List local command catalog entries.", ArgumentHint: "[--json] [--all|--hidden]", Handler: (*executionRuntime).tryCommandsCommand},
		{Name: "/help", Description: "Alias for the local command catalog.", ArgumentHint: "[--json] [--all|--hidden]", Handler: (*executionRuntime).tryCommandsCommand},
		{Name: "/skills", Description: "List registered skills without executing them.", ArgumentHint: "[--json] [--all|--hidden]", Handler: (*executionRuntime).trySkillsCommand},
		{Name: "/version", Description: "Print the Elnath version for this session.", ArgumentHint: "[--json]", Handler: (*executionRuntime).tryVersionCommand},
		{Name: "/status", Description: "Show local runtime session status.", ArgumentHint: "[--json]", Handler: (*executionRuntime).tryStatusCommand},
		{Name: "/plan", Description: "Enter, inspect, or exit local planning mode.", ArgumentHint: "[status|exit|<description>]", Handler: (*executionRuntime).tryPlanCommand},
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

	for _, spec := range runtimeLocalSlashCommandSpecs() {
		if fields[0] != spec.Name {
			continue
		}
		if spec.Handler == nil {
			return nil, "", true, nil
		}
		return spec.Handler(rt, sess, messages, input, bus)
	}
	return nil, "", false, nil
}
