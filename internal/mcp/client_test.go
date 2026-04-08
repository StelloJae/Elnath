package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// mockServer simulates an MCP server over io.Pipe pairs.
// It reads one JSON-RPC message per call to respond() and writes a reply.
type mockServer struct {
	t       *testing.T
	reader  *bufio.Scanner // reads from client
	writer  io.Writer      // writes to client
}

func newMockServer(t *testing.T, clientIn io.Reader, clientOut io.Writer) *mockServer {
	t.Helper()
	return &mockServer{
		t:      t,
		reader: bufio.NewScanner(clientIn),
		writer: clientOut,
	}
}

func (s *mockServer) readRequest() map[string]json.RawMessage {
	s.t.Helper()
	if !s.reader.Scan() {
		s.t.Fatal("mockServer: no more messages from client")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(s.reader.Bytes(), &m); err != nil {
		s.t.Fatalf("mockServer: unmarshal request: %v", err)
	}
	return m
}

func (s *mockServer) send(v any) {
	s.t.Helper()
	if err := writeMessage(s.writer, v); err != nil {
		s.t.Fatalf("mockServer: write response: %v", err)
	}
}

// makePipedClient creates a Client wired to a mockServer via io.Pipe pairs.
// The returned mockServer can be used to drive the protocol.
// serverIn  ← client writes
// serverOut → client reads
func makePipedClient(t *testing.T) (*Client, *mockServer) {
	t.Helper()
	// pipe from client → server
	serverInR, serverInW := io.Pipe()
	// pipe from server → client
	serverOutR, serverOutW := io.Pipe()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newClientFromPipes(serverInW, serverOutR, logger)

	srv := newMockServer(t, serverInR, serverOutW)
	return client, srv
}

func TestClientInitialize(t *testing.T) {
	client, srv := makePipedClient(t)

	done := make(chan error, 1)
	go func() {
		// 1. Read initialize request
		req := srv.readRequest()
		method := strings.Trim(string(req["method"]), `"`)
		if method != "initialize" {
			done <- fmt.Errorf("expected method=initialize, got %q", method)
			return
		}
		// 2. Send initialize response
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(req["id"]),
			"result": map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "test-server", "version": "1.0"},
			},
		})
		// 3. Read notifications/initialized (no id)
		notif := srv.readRequest()
		notifMethod := strings.Trim(string(notif["method"]), `"`)
		if notifMethod != "notifications/initialized" {
			done <- fmt.Errorf("expected notifications/initialized, got %q", notifMethod)
			return
		}
		if _, hasID := notif["id"]; hasID {
			done <- fmt.Errorf("notifications/initialized must not have an id field")
			return
		}
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientListTools(t *testing.T) {
	client, srv := makePipedClient(t)

	want := []ToolInfo{
		{
			Name:        "echo",
			Description: "Echo input",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		},
	}

	done := make(chan error, 1)
	go func() {
		// Handle initialize + notifications/initialized first
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

		// Handle tools/list
		listReq := srv.readRequest()
		method := strings.Trim(string(listReq["method"]), `"`)
		if method != "tools/list" {
			done <- fmt.Errorf("expected tools/list, got %q", method)
			return
		}
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(listReq["id"]),
			"result": map[string]any{
				"tools": want,
			},
		})
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	got, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("ListTools returned %d tools, want %d", len(got), len(want))
	}
	if got[0].Name != want[0].Name {
		t.Errorf("tool name = %q, want %q", got[0].Name, want[0].Name)
	}
	if got[0].Description != want[0].Description {
		t.Errorf("tool description = %q, want %q", got[0].Description, want[0].Description)
	}
}

func TestClientCallTool(t *testing.T) {
	client, srv := makePipedClient(t)

	done := make(chan error, 1)
	go func() {
		// initialize handshake
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

		// tools/call
		callReq := srv.readRequest()
		method := strings.Trim(string(callReq["method"]), `"`)
		if method != "tools/call" {
			done <- fmt.Errorf("expected tools/call, got %q", method)
			return
		}
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(callReq["id"]),
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "hello"},
				},
				"isError": false,
			},
		})
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	text, isError, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"hello"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if text != "hello" {
		t.Errorf("CallTool text = %q, want %q", text, "hello")
	}
	if isError {
		t.Error("CallTool isError = true, want false")
	}
}

func TestClientCallToolError(t *testing.T) {
	client, srv := makePipedClient(t)

	done := make(chan error, 1)
	go func() {
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

		callReq := srv.readRequest()
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(callReq["id"]),
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "tool failed"},
				},
				"isError": true,
			},
		})
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	text, isError, err := client.CallTool(context.Background(), "failing-tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if text != "tool failed" {
		t.Errorf("CallTool text = %q, want %q", text, "tool failed")
	}
	if !isError {
		t.Error("CallTool isError = false, want true")
	}
}

func TestClientCallRPCError(t *testing.T) {
	client, srv := makePipedClient(t)

	done := make(chan error, 1)
	go func() {
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

		callReq := srv.readRequest()
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(callReq["id"]),
			"error": map[string]any{
				"code":    -32601,
				"message": "method not found",
			},
		})
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	_, _, err := client.CallTool(context.Background(), "no-such-tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("CallTool: expected error for RPC error response, got nil")
	}
	if !strings.Contains(err.Error(), "method not found") {
		t.Errorf("error %q does not mention 'method not found'", err.Error())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
