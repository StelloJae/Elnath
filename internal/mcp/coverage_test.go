package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// errWriter always returns an error on Write.
type errWriter struct{ err error }

func (e *errWriter) Write(_ []byte) (int, error) { return 0, e.err }

// TestCloseNilCmd verifies that Close() on a pipe-backed client (no cmd) returns nil.
func TestCloseNilCmd(t *testing.T) {
	client, _ := makePipedClient(t)
	if err := client.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// TestCloseNilStdin verifies that Close() tolerates a nil stdin.
func TestCloseNilStdin(t *testing.T) {
	client := &Client{}
	if err := client.Close(); err != nil {
		t.Errorf("Close() with nil cmd/stdin = %v, want nil", err)
	}
}

// TestCallContextCancelledBeforeCall verifies that call() returns ctx.Err()
// immediately when the context is already cancelled.
func TestCallContextCancelledBeforeCall(t *testing.T) {
	client, _ := makePipedClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call

	_, err := client.call(ctx, "tools/list", nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("call() with cancelled ctx = %v, want context.Canceled", err)
	}
}

// TestCallContextCancelledDuringRead verifies that call() returns an error when
// the context is cancelled while waiting for a response (the inner ctx.Err() check).
func TestCallContextCancelledDuringRead(t *testing.T) {
	// Use a pipe where the server side never writes — so the scanner blocks.
	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()
	_ = serverInR // never read

	import_sink := serverInW // client writes here; we never consume it to force block on read

	ctx, cancel := context.WithCancel(context.Background())

	// We need the client's writeMessage to succeed so we get to the read loop.
	// Write the request into the pipe, then cancel the context before any response arrives.
	done := make(chan error, 1)
	go func() {
		// Drain client writes so writeMessage doesn't block.
		buf := make([]byte, 4096)
		serverInR.Read(buf) //nolint:errcheck
		// Now cancel so the inner ctx.Err() check fires on next loop iteration.
		cancel()
		// Close the write side of the server→client pipe so the scanner unblocks.
		serverOutW.Close()
		done <- nil
	}()

	_ = import_sink
	_ = serverOutR

	logger := makeDiscardLogger()
	client := newClientFromPipes(serverInW, serverOutR, logger)

	_, err := client.call(ctx, "tools/list", nil)
	<-done
	if err == nil {
		t.Error("call() expected error when context cancelled, got nil")
	}
}

// TestCallNotificationSkip verifies that call() skips server notifications
// (ID=0, no result/error) and continues reading until the real response arrives.
func TestCallNotificationSkip(t *testing.T) {
	client, srv := makePipedClient(t)

	done := make(chan error, 1)
	go func() {
		req := srv.readRequest()
		// Send a notification first (ID=0, no result/error) — should be skipped.
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"method":  "notifications/progress",
			// deliberately no "id", "result", or "error"
		})
		// Then send the real response.
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
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize with notification skip: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestWriteMessageError verifies writeMessage returns an error when the writer fails.
func TestWriteMessageError(t *testing.T) {
	w := &errWriter{err: errors.New("disk full")}
	err := writeMessage(w, map[string]any{"key": "value"})
	if err == nil {
		t.Fatal("writeMessage: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "write message") {
		t.Errorf("writeMessage error = %q, want it to contain 'write message'", err.Error())
	}
}

// TestReadResponseEOF verifies readResponse returns an error on EOF.
func TestReadResponseEOF(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader(""))
	_, err := readResponse(scanner)
	if err == nil {
		t.Fatal("readResponse: expected error on EOF, got nil")
	}
	if !strings.Contains(err.Error(), "connection closed") {
		t.Errorf("readResponse EOF error = %q, want 'connection closed'", err.Error())
	}
}

// TestReadResponseMalformedJSON verifies readResponse returns an error on bad JSON.
func TestReadResponseMalformedJSON(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("not-json\n"))
	_, err := readResponse(scanner)
	if err == nil {
		t.Fatal("readResponse: expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "unmarshal response") {
		t.Errorf("readResponse malformed error = %q, want 'unmarshal response'", err.Error())
	}
}

// TestReadResponseScanError verifies readResponse wraps a scanner error.
func TestReadResponseScanError(t *testing.T) {
	// A reader that returns an error mid-stream causes the scanner to surface it.
	r := &errorReader{err: errors.New("connection reset")}
	scanner := bufio.NewScanner(r)
	_, err := readResponse(scanner)
	if err == nil {
		t.Fatal("readResponse: expected error from scanner, got nil")
	}
}

// TestListToolsRPCError verifies ListTools propagates an RPC error response.
func TestListToolsRPCError(t *testing.T) {
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

		listReq := srv.readRequest()
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(listReq["id"]),
			"error": map[string]any{
				"code":    -32600,
				"message": "invalid request",
			},
		})
		done <- nil
	}()

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	_, err := client.ListTools(context.Background())
	if err == nil {
		t.Fatal("ListTools: expected error for RPC error response, got nil")
	}
	if !strings.Contains(err.Error(), "invalid request") {
		t.Errorf("ListTools error = %q, does not mention 'invalid request'", err.Error())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestInitializeRPCError verifies Initialize propagates an RPC error response.
func TestInitializeRPCError(t *testing.T) {
	client, srv := makePipedClient(t)

	done := make(chan error, 1)
	go func() {
		req := srv.readRequest()
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(req["id"]),
			"error": map[string]any{
				"code":    -32603,
				"message": "internal error",
			},
		})
		done <- nil
	}()

	err := client.Initialize(context.Background())
	if err == nil {
		t.Fatal("Initialize: expected error for RPC error response, got nil")
	}
	if !strings.Contains(err.Error(), "internal error") {
		t.Errorf("Initialize error = %q, does not mention 'internal error'", err.Error())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestInitializeBadResult verifies Initialize returns an error when result JSON is invalid.
func TestInitializeBadResult(t *testing.T) {
	client, srv := makePipedClient(t)

	done := make(chan error, 1)
	go func() {
		req := srv.readRequest()
		// Send result as a raw non-object value that will fail struct unmarshal.
		srv.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(req["id"]),
			"result":  "not-an-object",
		})
		done <- nil
	}()

	err := client.Initialize(context.Background())
	if err == nil {
		t.Fatal("Initialize: expected error for bad result, got nil")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestWriteMessageSuccess verifies the happy path of writeMessage.
func TestWriteMessageSuccess(t *testing.T) {
	var buf bytes.Buffer
	if err := writeMessage(&buf, map[string]string{"hello": "world"}); err != nil {
		t.Fatalf("writeMessage: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("writeMessage output %q missing expected content", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Error("writeMessage output should end with newline")
	}
}

// errorReader returns an error on the first Read call.
type errorReader struct{ err error }

func (r *errorReader) Read(_ []byte) (int, error) { return 0, r.err }

// makeDiscardLogger is a small helper used in the cancel-during-read test.
func makeDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
