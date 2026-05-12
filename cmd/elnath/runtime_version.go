package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

const versionCommandUsage = "Usage: /version [--json]"

func (rt *executionRuntime) tryVersionCommand(
	sess *agent.Session,
	messages []llm.Message,
	input string,
	bus *event.Bus,
) ([]llm.Message, string, bool, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 || fields[0] != "/version" {
		return nil, "", false, nil
	}

	summary := applyVersionCommand(fields[1:])
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

func applyVersionCommand(args []string) string {
	if len(args) == 0 {
		return fmt.Sprintf("elnath %s", version)
	}
	if len(args) == 1 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "help", "-h", "--help":
			return versionCommandUsage
		case "--json":
			raw, err := json.MarshalIndent(struct {
				Version string `json:"version"`
			}{Version: version}, "", "  ")
			if err != nil {
				return fmt.Sprintf("version: marshal JSON: %v", err)
			}
			return string(raw)
		}
	}
	return fmt.Sprintf("Invalid version argument: %s. %s", strings.Join(args, " "), versionCommandUsage)
}
