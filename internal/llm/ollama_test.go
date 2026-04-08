package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newOllamaSSEServer(t *testing.T, lines []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, l := range lines {
			fmt.Fprint(w, l)
		}
	}))
}

// TestOllamaStreamViaHTTPTest verifies Ollama delegates to the OpenAI-compatible
// endpoint at its configured base URL and produces the expected events.
func TestOllamaStreamViaHTTPTest(t *testing.T) {
	lines := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n",
		"data: [DONE]\n\n",
	}
	srv := newOllamaSSEServer(t, lines)
	defer srv.Close()

	p := NewOllamaProvider("", "llama3.2", WithOllamaBaseURL(srv.URL))

	var text string
	err := p.Stream(context.Background(), ChatRequest{
		Messages: []Message{NewUserMessage("hi")},
	}, func(ev StreamEvent) {
		if ev.Type == EventTextDelta {
			text += ev.Content
		}
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if text != "hello" {
		t.Errorf("text = %q, want %q", text, "hello")
	}
}

// TestOllamaChat verifies Chat() works end-to-end via httptest server.
func TestOllamaChat(t *testing.T) {
	lines := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"World\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"!\"}}]}\n\n",
		"data: [DONE]\n\n",
	}
	srv := newOllamaSSEServer(t, lines)
	defer srv.Close()

	p := NewOllamaProvider("", "llama3.2", WithOllamaBaseURL(srv.URL))

	resp, err := p.Chat(context.Background(), ChatRequest{
		Messages: []Message{NewUserMessage("hello")},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Content != "World!" {
		t.Errorf("Content = %q, want %q", resp.Content, "World!")
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "end_turn")
	}
}

// TestOllamaBaseURLOverride verifies that WithOllamaBaseURL changes the endpoint
// the inner OpenAI provider talks to.
func TestOllamaBaseURLOverride(t *testing.T) {
	var requestPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOllamaProvider("", "llama3.2", WithOllamaBaseURL(srv.URL))
	_ = p.Stream(context.Background(), ChatRequest{}, func(StreamEvent) {})

	if !strings.HasSuffix(requestPath, "/chat/completions") {
		t.Errorf("request path = %q, want suffix /chat/completions", requestPath)
	}
	if p.inner.baseURL != srv.URL {
		t.Errorf("inner baseURL = %q, want %q", p.inner.baseURL, srv.URL)
	}
}

// TestOllamaDefaultBaseURL verifies the default base URL points to localhost Ollama.
func TestOllamaDefaultBaseURL(t *testing.T) {
	p := NewOllamaProvider("", "llama3.2")
	if p.inner.baseURL != defaultOllamaBaseURL {
		t.Errorf("baseURL = %q, want %q", p.inner.baseURL, defaultOllamaBaseURL)
	}
}

// TestOllamaProviderName is also tested in registry_test.go but included here
// for completeness of the ollama package tests.
func TestOllamaProviderNameAndModels(t *testing.T) {
	p := NewOllamaProvider("", "mistral:7b")

	if p.Name() != "ollama" {
		t.Errorf("Name() = %q, want %q", p.Name(), "ollama")
	}

	models := p.Models()
	if len(models) != 1 {
		t.Fatalf("Models() len = %d, want 1", len(models))
	}
	if models[0].ID != "mistral:7b" {
		t.Errorf("Models()[0].ID = %q, want %q", models[0].ID, "mistral:7b")
	}
}

// TestOllamaStreamToolCalls verifies Ollama correctly surfaces tool call events
// via its delegated OpenAI stream parser.
func TestOllamaStreamToolCalls(t *testing.T) {
	lines := []string{
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_o1\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"cmd\\\":\\\"date\\\"}\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"finish_reason\":\"tool_calls\",\"delta\":{}}]}\n\n",
		"data: [DONE]\n\n",
	}
	srv := newOllamaSSEServer(t, lines)
	defer srv.Close()

	p := NewOllamaProvider("", "llama3.2", WithOllamaBaseURL(srv.URL))

	var startSeen, doneSeen bool
	err := p.Stream(context.Background(), ChatRequest{}, func(ev StreamEvent) {
		switch ev.Type {
		case EventToolUseStart:
			startSeen = true
			if ev.ToolCall.ID != "call_o1" {
				t.Errorf("start ID = %q, want call_o1", ev.ToolCall.ID)
			}
		case EventToolUseDone:
			doneSeen = true
		}
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if !startSeen {
		t.Error("expected EventToolUseStart, not received")
	}
	if !doneSeen {
		t.Error("expected EventToolUseDone, not received")
	}
}

// TestOllamaHTTPError verifies error propagation from the inner provider.
func TestOllamaHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := NewOllamaProvider("", "llama3.2", WithOllamaBaseURL(srv.URL))

	err := p.Stream(context.Background(), ChatRequest{}, func(StreamEvent) {})
	if err == nil {
		t.Fatal("expected error for non-200 status, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error = %q, want to contain '503'", err.Error())
	}
}
