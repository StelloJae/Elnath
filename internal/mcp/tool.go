package mcp

import (
	"context"
	"encoding/json"

	"github.com/stello/elnath/internal/tools"
)

// mcpTool adapts a remote MCP tool to the tools.Tool interface.
type mcpTool struct {
	info   ToolInfo
	client *Client
}

// NewTool wraps an MCP ToolInfo and its Client as a tools.Tool.
// The tool name is prefixed with "mcp_" to avoid collisions with built-in tools.
func NewTool(client *Client, info ToolInfo) tools.Tool {
	return &mcpTool{info: info, client: client}
}

func (t *mcpTool) Name() string {
	return "mcp_" + t.info.Name
}

func (t *mcpTool) Description() string {
	return t.info.Description
}

func (t *mcpTool) Schema() json.RawMessage {
	return t.info.InputSchema
}

func (t *mcpTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *mcpTool) Reversible() bool { return false }

func (t *mcpTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *mcpTool) Scope(json.RawMessage) tools.ToolScope {
	return tools.ConservativeScope()
}

func (t *mcpTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	text, isError, err := t.client.CallTool(ctx, t.info.Name, params)
	if err != nil {
		return tools.ErrorResult(err.Error()), nil
	}
	if isError {
		return tools.ErrorResult(text), nil
	}
	return tools.SuccessResult(text), nil
}
