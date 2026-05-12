package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/stello/elnath/internal/tools"
)

type catalogTool struct {
	client *Client
	server string
	name   string
}

type catalogToolInput struct {
	Action    string          `json:"action"`
	URI       string          `json:"uri,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// NewCatalogTool exposes read-only MCP resource/prompt metadata for one
// configured server. Prompt retrieval returns messages but does not execute
// them.
func NewCatalogTool(client *Client, serverName string) tools.Tool {
	server := strings.TrimSpace(serverName)
	if server == "" {
		server = "server"
	}
	return &catalogTool{
		client: client,
		server: server,
		name:   "mcp_" + sanitizeToolNamePart(server) + "_catalog",
	}
}

func (t *catalogTool) Name() string { return t.name }

func (t *catalogTool) Description() string {
	return "List and read MCP resource/prompt metadata for " + t.server
}

func (t *catalogTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"action":    tools.StringEnum("Catalog action. Prompt retrieval returns messages but does not execute them.", "list_resources", "list_prompts", "read_resource", "get_prompt"),
		"uri":       tools.String("Resource URI for read_resource."),
		"name":      tools.String("Prompt name for get_prompt."),
		"arguments": tools.Property{Type: "object", Description: "Prompt arguments for get_prompt."},
	}, []string{"action"})
}

func (t *catalogTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *catalogTool) Reversible() bool { return true }

func (t *catalogTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *catalogTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *catalogTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	if t == nil || t.client == nil {
		return tools.ErrorResult("mcp_catalog: client unavailable"), nil
	}

	var input catalogToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}

	switch strings.ToLower(strings.TrimSpace(input.Action)) {
	case "list_resources":
		resources, err := t.client.ListResources(ctx)
		if err != nil {
			return tools.ErrorResult(err.Error()), nil
		}
		return marshalCatalogToolOutput(map[string]any{
			"action":    "list_resources",
			"server":    t.server,
			"resources": resources,
		})
	case "list_prompts":
		prompts, err := t.client.ListPrompts(ctx)
		if err != nil {
			return tools.ErrorResult(err.Error()), nil
		}
		return marshalCatalogToolOutput(map[string]any{
			"action":  "list_prompts",
			"server":  t.server,
			"prompts": prompts,
		})
	case "read_resource":
		uri := strings.TrimSpace(input.URI)
		if uri == "" {
			return tools.ErrorResult("mcp_catalog: uri is required for read_resource"), nil
		}
		result, err := t.client.ReadResource(ctx, uri)
		if err != nil {
			return tools.ErrorResult(err.Error()), nil
		}
		return marshalCatalogToolOutput(map[string]any{
			"action":   "read_resource",
			"server":   t.server,
			"contents": result.Contents,
		})
	case "get_prompt":
		name := strings.TrimSpace(input.Name)
		if name == "" {
			return tools.ErrorResult("mcp_catalog: name is required for get_prompt"), nil
		}
		result, err := t.client.GetPrompt(ctx, name, input.Arguments)
		if err != nil {
			return tools.ErrorResult(err.Error()), nil
		}
		return marshalCatalogToolOutput(map[string]any{
			"action":      "get_prompt",
			"server":      t.server,
			"description": result.Description,
			"messages":    result.Messages,
		})
	default:
		return tools.ErrorResult(fmt.Sprintf("mcp_catalog: unsupported action %q; supported actions are list_resources, list_prompts, read_resource, and get_prompt", input.Action)), nil
	}
}

func marshalCatalogToolOutput(output any) (*tools.Result, error) {
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("mcp_catalog: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

func sanitizeToolNamePart(s string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "server"
	}
	return out
}
