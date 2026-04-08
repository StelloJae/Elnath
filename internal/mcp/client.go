package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ToolInfo describes a single tool exposed by an MCP server.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Client manages a connection to an external MCP server over stdio.
type Client struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	nextID  atomic.Int64
	mu      sync.Mutex
	logger  *slog.Logger
}

// NewClient launches the given command as an MCP server and returns a Client
// ready for Initialize to be called.
func NewClient(ctx context.Context, command string, args []string, env []string, logger *slog.Logger) (*Client, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start server: %w", err)
	}

	return &Client{
		cmd:     cmd,
		stdin:   stdin,
		scanner: bufio.NewScanner(stdout),
		logger:  logger,
	}, nil
}

// newClientFromPipes creates a Client from pre-existing pipes (for testing).
func newClientFromPipes(stdin io.WriteCloser, stdout io.Reader, logger *slog.Logger) *Client {
	return &Client{
		stdin:   stdin,
		scanner: bufio.NewScanner(stdout),
		logger:  logger,
	}
}

// Initialize performs the MCP handshake: sends initialize, reads the response,
// then sends notifications/initialized.
func (c *Client) Initialize(ctx context.Context) error {
	params, err := json.Marshal(map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "elnath",
			"version": "0.2.0",
		},
	})
	if err != nil {
		return fmt.Errorf("mcp: marshal initialize params: %w", err)
	}

	resp, err := c.call(ctx, "initialize", json.RawMessage(params))
	if err != nil {
		return fmt.Errorf("mcp: initialize: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("mcp: initialize: %w", resp.Error)
	}

	// Validate the server responded with a compatible protocol version.
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("mcp: parse initialize result: %w", err)
	}
	c.logger.Debug("mcp: connected", "protocolVersion", result.ProtocolVersion)

	// Send the required notifications/initialized notification (no id).
	notif := Notification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	c.mu.Lock()
	err = writeMessage(c.stdin, notif)
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("mcp: send notifications/initialized: %w", err)
	}
	return nil
}

// ListTools calls tools/list and returns all tools advertised by the server.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	resp, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: tools/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp: tools/list: %w", resp.Error)
	}

	var result struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("mcp: parse tools/list result: %w", err)
	}
	return result.Tools, nil
}

// CallTool invokes a named tool with the given JSON arguments.
// It returns the concatenated text content, an isError flag, and any transport error.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (string, bool, error) {
	params, err := json.Marshal(map[string]any{
		"name":      name,
		"arguments": json.RawMessage(arguments),
	})
	if err != nil {
		return "", false, fmt.Errorf("mcp: marshal call params: %w", err)
	}

	resp, err := c.call(ctx, "tools/call", json.RawMessage(params))
	if err != nil {
		return "", false, fmt.Errorf("mcp: tools/call %s: %w", name, err)
	}
	if resp.Error != nil {
		return "", false, fmt.Errorf("mcp: tools/call %s: %w", name, resp.Error)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", false, fmt.Errorf("mcp: parse tools/call result: %w", err)
	}

	var parts []string
	for _, c := range result.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n"), result.IsError, nil
}

// Close shuts down the MCP server process gracefully.
func (c *Client) Close() error {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Signal(sigterm())
	}

	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		return <-done
	}
}

// call is the internal helper that sends a JSON-RPC request and reads the response.
// It skips any server-to-client notifications (messages without an id) that may
// arrive before the expected response.
func (c *Client) call(ctx context.Context, method string, params any) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	id := c.nextID.Add(1)

	var rawParams json.RawMessage
	if params != nil {
		switch v := params.(type) {
		case json.RawMessage:
			rawParams = v
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("marshal params: %w", err)
			}
			rawParams = b
		}
	}

	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  rawParams,
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := writeMessage(c.stdin, req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	// Read responses, skipping any interleaved server notifications.
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := readResponse(c.scanner)
		if err != nil {
			return nil, err
		}
		// A response with ID 0 and no error/result is likely a notification — skip it.
		if resp.ID == 0 && resp.Result == nil && resp.Error == nil {
			continue
		}
		return resp, nil
	}
}
