package mcp

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestMCPCatalogToolNameSanitizesServerName(t *testing.T) {
	tool := NewCatalogTool(nil, "Git Hub!")
	if tool.Name() != "mcp_git_hub_catalog" {
		t.Fatalf("Name() = %q, want mcp_git_hub_catalog", tool.Name())
	}
}

func TestMCPCatalogToolListsResources(t *testing.T) {
	client, srv := newTestClient(t)
	tool := NewCatalogTool(client, "github")

	done := make(chan error, 1)
	go func() {
		handshake(t, srv)
		listReq := srv.readRequest()
		if method := strings.Trim(string(listReq["method"]), `"`); method != "resources/list" {
			done <- fmt.Errorf("expected resources/list, got %q", method)
			return
		}
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(listReq["id"]),
			"result": map[string]any{
				"resources": []ResourceInfo{{
					URI:         "file:///repo/README.md",
					Name:        "README",
					Description: "Project README",
					MIMEType:    "text/markdown",
				}},
			},
		})
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list_resources"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error: %s", res.Output)
	}
	var out struct {
		Action    string         `json:"action"`
		Server    string         `json:"server"`
		Resources []ResourceInfo `json:"resources"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "list_resources" || out.Server != "github" || len(out.Resources) != 1 {
		t.Fatalf("output = %+v, want one github resource", out)
	}
}

func TestMCPCatalogToolListsPrompts(t *testing.T) {
	client, srv := newTestClient(t)
	tool := NewCatalogTool(client, "github")

	done := make(chan error, 1)
	go func() {
		handshake(t, srv)
		listReq := srv.readRequest()
		if method := strings.Trim(string(listReq["method"]), `"`); method != "prompts/list" {
			done <- fmt.Errorf("expected prompts/list, got %q", method)
			return
		}
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(listReq["id"]),
			"result": map[string]any{
				"prompts": []PromptInfo{{
					Name:        "review",
					Description: "Review code",
					Arguments:   []PromptArgument{{Name: "path", Required: true}},
				}},
			},
		})
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list_prompts"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error: %s", res.Output)
	}
	var out struct {
		Action  string       `json:"action"`
		Server  string       `json:"server"`
		Prompts []PromptInfo `json:"prompts"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "list_prompts" || out.Server != "github" || len(out.Prompts) != 1 || out.Prompts[0].Name != "review" {
		t.Fatalf("output = %+v, want one github prompt", out)
	}
}

func TestMCPCatalogToolReadsResource(t *testing.T) {
	client, srv := newTestClient(t)
	tool := NewCatalogTool(client, "github")

	done := make(chan error, 1)
	go func() {
		handshake(t, srv)
		readReq := srv.readRequest()
		if method := strings.Trim(string(readReq["method"]), `"`); method != "resources/read" {
			done <- fmt.Errorf("expected resources/read, got %q", method)
			return
		}
		var params struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(readReq["params"], &params); err != nil {
			done <- fmt.Errorf("unmarshal resources/read params: %w", err)
			return
		}
		if params.URI != "file:///repo/README.md" {
			done <- fmt.Errorf("uri = %q", params.URI)
			return
		}
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(readReq["id"]),
			"result": map[string]any{
				"contents": []map[string]any{{
					"uri":      "file:///repo/README.md",
					"mimeType": "text/markdown",
					"text":     "hello from readme",
				}},
			},
		})
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"read_resource","uri":"file:///repo/README.md"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error: %s", res.Output)
	}
	var out struct {
		Action   string            `json:"action"`
		Server   string            `json:"server"`
		Contents []ResourceContent `json:"contents"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "read_resource" || out.Server != "github" || len(out.Contents) != 1 || out.Contents[0].Text != "hello from readme" {
		t.Fatalf("output = %+v", out)
	}
}

func TestMCPCatalogToolGetsPrompt(t *testing.T) {
	client, srv := newTestClient(t)
	tool := NewCatalogTool(client, "github")

	done := make(chan error, 1)
	go func() {
		handshake(t, srv)
		promptReq := srv.readRequest()
		if method := strings.Trim(string(promptReq["method"]), `"`); method != "prompts/get" {
			done <- fmt.Errorf("expected prompts/get, got %q", method)
			return
		}
		var params struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		if err := json.Unmarshal(promptReq["params"], &params); err != nil {
			done <- fmt.Errorf("unmarshal prompts/get params: %w", err)
			return
		}
		if params.Name != "review" || params.Arguments["path"] != "README.md" {
			done <- fmt.Errorf("prompt params = %+v", params)
			return
		}
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(promptReq["id"]),
			"result": map[string]any{
				"description": "Review prompt",
				"messages": []map[string]any{{
					"role":    "user",
					"content": map[string]any{"type": "text", "text": "Review README.md"},
				}},
			},
		})
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"get_prompt","name":"review","arguments":{"path":"README.md"}}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error: %s", res.Output)
	}
	var out struct {
		Action      string          `json:"action"`
		Server      string          `json:"server"`
		Description string          `json:"description"`
		Messages    []PromptMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "get_prompt" || out.Server != "github" || out.Description != "Review prompt" || len(out.Messages) != 1 {
		t.Fatalf("output = %+v", out)
	}
	if out.Messages[0].Role != "user" || out.Messages[0].Content.Text != "Review README.md" {
		t.Fatalf("message = %+v", out.Messages[0])
	}
}

func TestMCPCatalogToolRequiresResourceURI(t *testing.T) {
	client, _ := newTestClient(t)
	tool := NewCatalogTool(client, "github")

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"read_resource"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Output, "uri is required") {
		t.Fatalf("Execute result = %+v", res)
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
