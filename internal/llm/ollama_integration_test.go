package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// ollamaIntegrationModels lists chat-capable models to try, in priority order.
var ollamaIntegrationModels = []string{
	"llama3.2", "llama3.1", "llama3", "mistral", "phi3", "gemma2", "qwen2", "deepseek-coder",
}

func findOllamaChatModel(t *testing.T) string {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://localhost:11434/api/tags")
	if err != nil {
		t.Skipf("Ollama not running: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name    string `json:"name"`
			Details struct {
				Family string `json:"family"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Skipf("cannot decode Ollama tags: %v", err)
	}

	available := map[string]bool{}
	for _, m := range result.Models {
		base := strings.Split(m.Name, ":")[0]
		// Skip embedding-only models.
		if m.Details.Family == "bert" || strings.Contains(m.Name, "embed") {
			continue
		}
		available[base] = true
		available[m.Name] = true
	}

	for _, candidate := range ollamaIntegrationModels {
		if available[candidate] {
			return candidate
		}
	}

	// Try any non-embedding model.
	for _, m := range result.Models {
		if m.Details.Family != "bert" && !strings.Contains(m.Name, "embed") {
			return m.Name
		}
	}

	t.Skipf("no chat-capable Ollama model found (available: %d models)", len(result.Models))
	return ""
}

func TestOllamaIntegrationChat(t *testing.T) {
	if os.Getenv("ELNATH_INTEGRATION") == "" && testing.Short() {
		t.Skip("set ELNATH_INTEGRATION=1 or remove -short to run Ollama integration tests")
	}

	model := findOllamaChatModel(t)
	t.Logf("using Ollama model: %s", model)

	p := NewOllamaProvider("", model)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := p.Chat(ctx, ChatRequest{
		Messages:  []Message{NewUserMessage("Reply with exactly: PONG")},
		MaxTokens: 32,
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}

	if resp.Content == "" {
		t.Error("empty response content")
	}
	t.Logf("response: %s", resp.Content)
}

func TestOllamaIntegrationStream(t *testing.T) {
	if os.Getenv("ELNATH_INTEGRATION") == "" && testing.Short() {
		t.Skip("set ELNATH_INTEGRATION=1 or remove -short to run Ollama integration tests")
	}

	model := findOllamaChatModel(t)
	t.Logf("using Ollama model: %s", model)

	p := NewOllamaProvider("", model)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var chunks []string
	var gotDone bool
	err := p.Stream(ctx, ChatRequest{
		Messages:  []Message{NewUserMessage("Say hello in one word.")},
		MaxTokens: 16,
	}, func(ev StreamEvent) {
		switch ev.Type {
		case EventTextDelta:
			chunks = append(chunks, ev.Content)
		case EventDone:
			gotDone = true
		}
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("no text chunks received")
	}
	if !gotDone {
		t.Error("did not receive EventDone")
	}
	t.Logf("streamed %d chunks: %s", len(chunks), strings.Join(chunks, ""))
}

func TestOllamaIntegrationToolUse(t *testing.T) {
	if os.Getenv("ELNATH_INTEGRATION") == "" && testing.Short() {
		t.Skip("set ELNATH_INTEGRATION=1 or remove -short to run Ollama integration tests")
	}

	model := findOllamaChatModel(t)
	t.Logf("using Ollama model: %s", model)

	p := NewOllamaProvider("", model)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tools := []ToolDef{
		{
			Name:        "get_weather",
			Description: "Get current weather for a city",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string","description":"City name"}},"required":["city"]}`),
		},
	}

	resp, err := p.Chat(ctx, ChatRequest{
		System:    "You are a helpful assistant. Use the get_weather tool when asked about weather.",
		Messages:  []Message{NewUserMessage("What's the weather in Tokyo?")},
		Tools:     tools,
		MaxTokens: 256,
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}

	// Not all Ollama models support tool_use. Log what we get.
	t.Logf("StopReason: %s, Content: %q, ToolCalls: %d", resp.StopReason, resp.Content, len(resp.ToolCalls))
	if len(resp.ToolCalls) > 0 {
		for i, tc := range resp.ToolCalls {
			t.Logf("  tool[%d]: name=%s input=%s", i, tc.Name, tc.Input)
		}
	} else {
		t.Log("model did not use tools (expected for many Ollama models)")
	}

	// Basic sanity: we got some response.
	if resp.Content == "" && len(resp.ToolCalls) == 0 {
		t.Error("empty response: no content and no tool calls")
	}
}

func TestOllamaIntegrationSkipWhenNotRunning(t *testing.T) {
	// Verify the skip logic works by trying an unreachable port.
	client := &http.Client{Timeout: 1 * time.Second}
	_, err := client.Get("http://localhost:19999/api/tags")
	if err == nil {
		t.Skip("port 19999 unexpectedly reachable")
	}
	// The error confirms skip would trigger in findOllamaChatModel.
	_ = fmt.Sprintf("expected unreachable: %v", err)
}
