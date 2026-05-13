package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/skill"
)

const commandsCommandUsage = "Usage: /commands [--json] [--all|--hidden] or /help [--json] [--all|--hidden]"

func (rt *executionRuntime) tryCommandsCommand(
	sess *agent.Session,
	messages []llm.Message,
	input string,
	bus *event.Bus,
) ([]llm.Message, string, bool, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 || (fields[0] != "/commands" && fields[0] != "/help") {
		return nil, "", false, nil
	}

	summary := rt.applyCommandsCommand(fields[1:])
	if bus != nil {
		bus.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: summary + "\n"})
	}

	delta := []llm.Message{
		llm.NewUserMessage(input),
		llm.NewAssistantMessage(summary),
	}
	updated := append(messages, delta...)
	if sess != nil {
		if err := sess.AppendMessages(delta); err != nil && rt != nil && rt.app != nil {
			rt.app.Logger.Warn("session persist failed", "error", err)
		}
		sess.Messages = updated
	}
	return updated, summary, true, nil
}

func (rt *executionRuntime) applyCommandsCommand(args []string) string {
	for _, arg := range args {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "help", "-h", "--help":
			return commandsCommandUsage
		case "--json", "--all", "--hidden":
			continue
		default:
			return fmt.Sprintf("Invalid commands argument: %s. %s", strings.Join(args, " "), commandsCommandUsage)
		}
	}
	includeHidden := hasFlag(args, "--all") || hasFlag(args, "--hidden")
	var skillReg *skill.Registry
	if rt != nil {
		skillReg = rt.skillReg
	}
	catalog := runtimeCommandCatalogWithSkills(skillReg, includeHidden)
	if hasFlag(args, "--json") {
		raw, err := json.MarshalIndent(catalog, "", "  ")
		if err != nil {
			return fmt.Sprintf("commands: marshal catalog: %v", err)
		}
		return string(raw)
	}
	return strings.TrimSpace(formatCommandCatalog(catalog))
}

func applyCommandsCommand(args []string) string {
	return (*executionRuntime)(nil).applyCommandsCommand(args)
}

func runtimeCommandCatalog(includeHidden bool) []commandCatalogEntry {
	return runtimeCommandCatalogWithSkills(nil, includeHidden)
}

func runtimeCommandCatalogWithSkills(skillReg *skill.Registry, includeHidden bool) []commandCatalogEntry {
	entries := commandCatalog(includeHidden)
	for _, spec := range runtimeLocalSlashCommandSpecs() {
		entries = append(entries, commandCatalogEntry{
			Name:            spec.Name,
			Description:     spec.Description,
			Category:        "runtime-control",
			ArgumentHint:    spec.ArgumentHint,
			Surface:         "runtime_slash",
			ExecutionPolicy: "local_runtime_control",
		})
	}
	entries = append(entries, skillBackedCommandCatalogEntries(skillReg, entries, includeHidden)...)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries
}

func skillBackedCommandCatalogEntries(skillReg *skill.Registry, existing []commandCatalogEntry, includeHidden bool) []commandCatalogEntry {
	if skillReg == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(existing))
	for _, entry := range existing {
		seen[entry.Name] = struct{}{}
		for _, alias := range entry.Aliases {
			seen[alias] = struct{}{}
		}
	}

	var entries []commandCatalogEntry
	for _, sk := range skillReg.List() {
		if !sk.UserInvocable() && !includeHidden {
			continue
		}
		entry, ok := skillBackedCommandCatalogEntry(sk)
		if !ok {
			continue
		}
		if _, exists := seen[entry.Name]; exists {
			continue
		}
		seen[entry.Name] = struct{}{}
		entries = append(entries, entry)
	}
	return entries
}

func skillBackedCommandCatalogEntry(sk *skill.Skill) (commandCatalogEntry, bool) {
	if sk == nil || strings.TrimSpace(sk.Name) == "" {
		return commandCatalogEntry{}, false
	}
	trigger := strings.TrimSpace(sk.Trigger)
	if trigger == "" {
		trigger = "/" + sk.Name
	}
	fields := strings.Fields(trigger)
	name := "/" + sk.Name
	var hint string
	if len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
		name = fields[0]
		if len(fields) > 1 {
			hint = strings.Join(fields[1:], " ")
		}
	}
	return commandCatalogEntry{
		Name:            name,
		Description:     sk.Description,
		Category:        "skill",
		ArgumentHint:    hint,
		Source:          sk.Source,
		Hidden:          !sk.UserInvocable(),
		Surface:         "skill_slash",
		ExecutionPolicy: "skill_prompt_invocation",
		ModelCallable:   true,
	}, true
}
