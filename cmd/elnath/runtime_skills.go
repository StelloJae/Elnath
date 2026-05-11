package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/skill"
)

const skillsCommandUsage = "Usage: /skills [--json]"

type runtimeSkillCatalogEntry struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Trigger       string   `json:"trigger,omitempty"`
	RequiredTools []string `json:"required_tools,omitempty"`
	Paths         []string `json:"paths,omitempty"`
	Model         string   `json:"model,omitempty"`
	Effort        string   `json:"effort,omitempty"`
	Status        string   `json:"status,omitempty"`
	Source        string   `json:"source,omitempty"`
}

func (rt *executionRuntime) trySkillsCommand(
	sess *agent.Session,
	messages []llm.Message,
	input string,
	bus *event.Bus,
) ([]llm.Message, string, bool, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 || fields[0] != "/skills" {
		return nil, "", false, nil
	}

	summary := rt.applySkillsCommand(fields[1:])
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

func (rt *executionRuntime) applySkillsCommand(args []string) string {
	for _, arg := range args {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "help", "-h", "--help":
			return skillsCommandUsage
		case "--json":
			continue
		default:
			return fmt.Sprintf("Invalid skills argument: %s. %s", strings.Join(args, " "), skillsCommandUsage)
		}
	}

	entries := runtimeSkillCatalog(rt.skillReg)
	if hasFlag(args, "--json") {
		raw, err := json.MarshalIndent(map[string]any{
			"action": "list",
			"skills": entries,
		}, "", "  ")
		if err != nil {
			return fmt.Sprintf("skills: marshal catalog: %v", err)
		}
		return string(raw)
	}
	return formatRuntimeSkillCatalog(entries)
}

func runtimeSkillCatalog(reg *skill.Registry) []runtimeSkillCatalogEntry {
	if reg == nil {
		return nil
	}
	skills := reg.List()
	entries := make([]runtimeSkillCatalogEntry, 0, len(skills))
	for _, sk := range skills {
		entries = append(entries, runtimeSkillCatalogEntry{
			Name:          sk.Name,
			Description:   sk.Description,
			Trigger:       sk.Trigger,
			RequiredTools: append([]string(nil), sk.RequiredTools...),
			Paths:         append([]string(nil), sk.Paths...),
			Model:         sk.Model,
			Effort:        sk.Effort,
			Status:        sk.Status,
			Source:        sk.Source,
		})
	}
	return entries
}

func formatRuntimeSkillCatalog(entries []runtimeSkillCatalogEntry) string {
	if len(entries) == 0 {
		return "No skills registered."
	}

	var b strings.Builder
	b.WriteString("Available skills:\n")
	for _, entry := range entries {
		trigger := strings.TrimSpace(entry.Trigger)
		if trigger == "" {
			trigger = "/" + entry.Name
		}
		b.WriteString("  ")
		b.WriteString(trigger)
		if entry.Description != "" {
			b.WriteString(" - ")
			b.WriteString(entry.Description)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
