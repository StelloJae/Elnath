package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/tools"
)

const commandCatalogToolName = "command_catalog"

type commandCatalogTool struct{}

func newCommandCatalogTool() *commandCatalogTool {
	return &commandCatalogTool{}
}

func (t *commandCatalogTool) Name() string { return commandCatalogToolName }

func (t *commandCatalogTool) Description() string {
	return "List or inspect Elnath command metadata without executing commands"
}

func (t *commandCatalogTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"action":         tools.StringEnum("Catalog action. Only list and show are supported; this tool never executes commands.", "list", "show"),
		"command":        tools.String("Command name for action=show."),
		"include_hidden": tools.Bool("Include hidden internal commands. Defaults to false."),
	}, []string{"action"})
}

func (t *commandCatalogTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *commandCatalogTool) Reversible() bool { return true }

func (t *commandCatalogTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *commandCatalogTool) ShouldCancelSiblingsOnError() bool { return false }

type commandCatalogToolInput struct {
	Action        string `json:"action"`
	Command       string `json:"command"`
	IncludeHidden bool   `json:"include_hidden"`
}

func (t *commandCatalogTool) Execute(_ context.Context, params json.RawMessage) (*tools.Result, error) {
	var input commandCatalogToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}

	action := strings.ToLower(strings.TrimSpace(input.Action))
	switch action {
	case "", "list":
		return marshalCommandCatalogToolOutput(map[string]any{
			"action":   "list",
			"commands": commandCatalog(input.IncludeHidden),
		})
	case "show":
		name := strings.TrimSpace(input.Command)
		if name == "" {
			return tools.ErrorResult("command_catalog: command is required for action=show"), nil
		}
		entry, ok := findCommandCatalogEntry(name, input.IncludeHidden)
		if !ok {
			return tools.ErrorResult(fmt.Sprintf("command_catalog: command %q not found", name)), nil
		}
		return marshalCommandCatalogToolOutput(map[string]any{
			"action":  "show",
			"command": entry,
		})
	default:
		return tools.ErrorResult(fmt.Sprintf("command_catalog: unsupported action %q; supported actions are list and show", input.Action)), nil
	}
}

func findCommandCatalogEntry(name string, includeHidden bool) (commandCatalogEntry, bool) {
	for _, entry := range commandCatalog(includeHidden) {
		if entry.Name == name {
			return entry, true
		}
		for _, alias := range entry.Aliases {
			if alias == name {
				return entry, true
			}
		}
	}
	return commandCatalogEntry{}, false
}

func marshalCommandCatalogToolOutput(output any) (*tools.Result, error) {
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("command_catalog: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}
