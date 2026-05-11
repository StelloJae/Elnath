package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

const commandsCommandUsage = "Usage: /commands [--json] [--all] or /help [--json] [--all]"

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

	summary := applyCommandsCommand(fields[1:])
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

func applyCommandsCommand(args []string) string {
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
	catalog := runtimeCommandCatalog(includeHidden)
	if hasFlag(args, "--json") {
		raw, err := json.MarshalIndent(catalog, "", "  ")
		if err != nil {
			return fmt.Sprintf("commands: marshal catalog: %v", err)
		}
		return string(raw)
	}
	return strings.TrimSpace(formatCommandCatalog(catalog))
}

func runtimeCommandCatalog(includeHidden bool) []commandCatalogEntry {
	entries := commandCatalog(includeHidden)
	for _, spec := range runtimeLocalSlashCommandSpecs() {
		entries = append(entries, commandCatalogEntry{
			Name:        spec.Name,
			Description: spec.Description,
			Category:    "runtime-control",
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries
}
