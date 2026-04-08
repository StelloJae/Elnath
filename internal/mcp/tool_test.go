package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/tools"
)

// mockMCPClient wraps a real Client backed by pipes, driven by a goroutine.
// For tool_test we use a simpler approach: drive CallTool via a goroutine.

func newTestClient(t *testing.T) (*Client, *mockServer) {
	t.Helper()
	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newClientFromPipes(serverInW, serverOutR, logger)
	srv := newMockServer(t, serverInR, serverOutW)
	return client, srv
}

func handshake(t *testing.T, srv *mockServer) {
	t.Helper()
	req := srv.readRequest()
	srv.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(req["id"]),
		"result": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "test", "version": "1.0"},
		},
	})
	srv.readRequest() // notifications/initialized
}

func TestMCPToolName(t *testing.T) {
	client, _ := newTestClient(t)
	info := ToolInfo{Name: "search", Description: "Search something", InputSchema: json.RawMessage(`{}`)}
	tool := NewTool(client, info)
	if tool.Name() != "mcp_search" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "mcp_search")
	}
}

func TestMCPToolDescription(t *testing.T) {
	client, _ := newTestClient(t)
	info := ToolInfo{Name: "x", Description: "Does X", InputSchema: json.RawMessage(`{}`)}
	tool := NewTool(client, info)
	if tool.Description() != "Does X" {
		t.Errorf("Description() = %q, want %q", tool.Description(), "Does X")
	}
}

func TestMCPToolSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	client, _ := newTestClient(t)
	info := ToolInfo{Name: "x", Description: "x", InputSchema: schema}
	tool := NewTool(client, info)
	if string(tool.Schema()) != string(schema) {
		t.Errorf("Schema() = %q, want %q", tool.Schema(), schema)
	}
}

func TestMCPToolExecute(t *testing.T) {
	client, srv := newTestClient(t)
	info := ToolInfo{Name: "echo", Description: "Echo", InputSchema: json.RawMessage(`{}`)}
	tool := NewTool(client, info)

	done := make(chan error, 1)
	go func() {
		handshake(t, srv)
		callReq := srv.readRequest()
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(callReq["id"]),
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "pong"},
				},
				"isError": false,
			},
		})
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"text":"ping"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if res.Output != "pong" {
		t.Errorf("Output = %q, want %q", res.Output, "pong")
	}
	if res.IsError {
		t.Error("IsError = true, want false")
	}
}

func TestMCPToolExecuteError(t *testing.T) {
	client, srv := newTestClient(t)
	info := ToolInfo{Name: "broken", Description: "Broken tool", InputSchema: json.RawMessage(`{}`)}
	tool := NewTool(client, info)

	done := make(chan error, 1)
	go func() {
		handshake(t, srv)
		callReq := srv.readRequest()
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(callReq["id"]),
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "something broke"},
				},
				"isError": true,
			},
		})
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("IsError = false, want true")
	}
	if !strings.Contains(res.Output, "something broke") {
		t.Errorf("Output %q does not contain expected text", res.Output)
	}
}

// Verify NewTool returns a value satisfying the tools.Tool interface.
func TestMCPToolInterface(t *testing.T) {
	client, _ := newTestClient(t)
	info := ToolInfo{Name: "x", Description: "x", InputSchema: json.RawMessage(`{}`)}
	var _ tools.Tool = NewTool(client, info)
}
