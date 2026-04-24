package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseServer returns an httptest.Server that writes the given SSE lines to each request.
func sseServer(t *testing.T, lines []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, l := range lines {
			fmt.Fprint(w, l)
		}
	}))
}

func newTestOpenAI(t *testing.T, srv *httptest.Server) *OpenAIProvider {
	t.Helper()
	return NewOpenAIProvider("test-key", "gpt-5.5",
		WithOpenAIBaseURL(srv.URL),
	)
}

// ---- Stream tests ----

func TestOpenAIStreamTextDelta(t *testing.T) {
	lines := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n",
		"data: [DONE]\n\n",
	}
	srv := sseServer(t, lines)
	defer srv.Close()

	p := newTestOpenAI(t, srv)

	var events []StreamEvent
	err := p.Stream(context.Background(), ChatRequest{}, func(ev StreamEvent) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var text string
	for _, ev := range events {
		if ev.Type == EventTextDelta {
			text += ev.Content
		}
	}
	if text != "Hello world" {
		t.Errorf("accumulated text = %q, want %q", text, "Hello world")
	}

	last := events[len(events)-1]
	if last.Type != EventDone {
		t.Errorf("last event type = %q, want %q", last.Type, EventDone)
	}
}

func TestOpenAIStreamToolCalls(t *testing.T) {
	lines := []string{
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_abc\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"cmd\\\":\\\"ls\\\"}\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"finish_reason\":\"tool_calls\",\"delta\":{}}]}\n\n",
		"data: [DONE]\n\n",
	}
	srv := sseServer(t, lines)
	defer srv.Close()

	p := newTestOpenAI(t, srv)

	var events []StreamEvent
	err := p.Stream(context.Background(), ChatRequest{}, func(ev StreamEvent) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var starts, deltas, dones int
	for _, ev := range events {
		switch ev.Type {
		case EventToolUseStart:
			starts++
			if ev.ToolCall.ID != "call_abc" {
				t.Errorf("start ID = %q, want %q", ev.ToolCall.ID, "call_abc")
			}
			if ev.ToolCall.Name != "bash" {
				t.Errorf("start Name = %q, want %q", ev.ToolCall.Name, "bash")
			}
		case EventToolUseDelta:
			deltas++
			if !strings.Contains(ev.ToolCall.Input, "ls") {
				t.Errorf("delta Input = %q, want contains 'ls'", ev.ToolCall.Input)
			}
		case EventToolUseDone:
			dones++
			if ev.ToolCall.ID != "call_abc" {
				t.Errorf("done ID = %q, want %q", ev.ToolCall.ID, "call_abc")
			}
		}
	}

	if starts != 1 {
		t.Errorf("EventToolUseStart count = %d, want 1", starts)
	}
	if deltas != 1 {
		t.Errorf("EventToolUseDelta count = %d, want 1", deltas)
	}
	if dones != 1 {
		t.Errorf("EventToolUseDone count = %d, want 1", dones)
	}
}

func TestOpenAIStreamMultipleToolCalls(t *testing.T) {
	lines := []string{
		// First tool call — index 0
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"cmd\\\":\\\"ls\\\"}\"}}]}}]}\n\n",
		// Second tool call — index 1
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call_2\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\"{\\\"path\\\":\\\"/tmp\\\"}\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"finish_reason\":\"tool_calls\",\"delta\":{}}]}\n\n",
		"data: [DONE]\n\n",
	}
	srv := sseServer(t, lines)
	defer srv.Close()

	p := newTestOpenAI(t, srv)

	startIDs := map[string]string{} // id -> name
	doneIDs := map[string]bool{}

	err := p.Stream(context.Background(), ChatRequest{}, func(ev StreamEvent) {
		switch ev.Type {
		case EventToolUseStart:
			startIDs[ev.ToolCall.ID] = ev.ToolCall.Name
		case EventToolUseDone:
			doneIDs[ev.ToolCall.ID] = true
		}
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	if len(startIDs) != 2 {
		t.Errorf("EventToolUseStart count = %d, want 2", len(startIDs))
	}
	if len(doneIDs) != 2 {
		t.Errorf("EventToolUseDone count = %d, want 2", len(doneIDs))
	}
	if startIDs["call_1"] != "bash" {
		t.Errorf("call_1 name = %q, want bash", startIDs["call_1"])
	}
	if startIDs["call_2"] != "read_file" {
		t.Errorf("call_2 name = %q, want read_file", startIDs["call_2"])
	}
}

func TestOpenAIStreamDone(t *testing.T) {
	lines := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n",
		"data: [DONE]\n\n",
	}
	srv := sseServer(t, lines)
	defer srv.Close()

	p := newTestOpenAI(t, srv)

	var gotDone bool
	err := p.Stream(context.Background(), ChatRequest{}, func(ev StreamEvent) {
		if ev.Type == EventDone {
			gotDone = true
		}
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if !gotDone {
		t.Error("expected EventDone, not received")
	}
}

func TestOpenAIStreamNoDone(t *testing.T) {
	// Stream closes without [DONE] — provider must still emit EventDone.
	lines := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n",
	}
	srv := sseServer(t, lines)
	defer srv.Close()

	p := newTestOpenAI(t, srv)

	var gotDone bool
	err := p.Stream(context.Background(), ChatRequest{}, func(ev StreamEvent) {
		if ev.Type == EventDone {
			gotDone = true
		}
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if !gotDone {
		t.Error("expected EventDone even without [DONE] marker")
	}
}

// ---- Chat tests ----

func TestOpenAIChat(t *testing.T) {
	lines := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"Answer\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\" here\"}}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\n",
		"data: [DONE]\n\n",
	}
	srv := sseServer(t, lines)
	defer srv.Close()

	p := newTestOpenAI(t, srv)

	resp, err := p.Chat(context.Background(), ChatRequest{
		Messages: []Message{NewUserMessage("hello")},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Content != "Answer here" {
		t.Errorf("Content = %q, want %q", resp.Content, "Answer here")
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "end_turn")
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", resp.Usage.InputTokens)
	}
}

func TestOpenAIChatToolUse(t *testing.T) {
	lines := []string{
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_xyz\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"cmd\\\":\\\"pwd\\\"}\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"finish_reason\":\"tool_calls\",\"delta\":{}}]}\n\n",
		"data: [DONE]\n\n",
	}
	srv := sseServer(t, lines)
	defer srv.Close()

	p := newTestOpenAI(t, srv)

	resp, err := p.Chat(context.Background(), ChatRequest{
		Messages: []Message{NewUserMessage("run pwd")},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "tool_use")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_xyz" {
		t.Errorf("ToolCall.ID = %q, want %q", tc.ID, "call_xyz")
	}
	if tc.Name != "bash" {
		t.Errorf("ToolCall.Name = %q, want %q", tc.Name, "bash")
	}
	if !strings.Contains(tc.Input, "pwd") {
		t.Errorf("ToolCall.Input = %q, want contains 'pwd'", tc.Input)
	}
}

// ---- HTTP error test ----

func TestOpenAIHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := newTestOpenAI(t, srv)

	err := p.Stream(context.Background(), ChatRequest{}, func(StreamEvent) {})
	if err == nil {
		t.Fatal("expected error for non-200 status, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, want to contain '401'", err.Error())
	}
}

// ---- toOpenAIMessages table-driven tests ----

func TestToOpenAIMessagesRoles(t *testing.T) {
	tests := []struct {
		name     string
		msgs     []Message
		wantLen  int
		wantRole []string
	}{
		{
			name:     "user text",
			msgs:     []Message{NewUserMessage("hello")},
			wantLen:  1,
			wantRole: []string{"user"},
		},
		{
			name:     "assistant text",
			msgs:     []Message{NewAssistantMessage("hi")},
			wantLen:  1,
			wantRole: []string{"assistant"},
		},
		{
			name: "assistant with tool_use",
			msgs: []Message{
				{
					Role: RoleAssistant,
					Content: []ContentBlock{
						TextBlock{Text: "using tool"},
						ToolUseBlock{ID: "t1", Name: "bash", Input: json.RawMessage(`{"cmd":"ls"}`)},
					},
				},
			},
			wantLen:  1,
			wantRole: []string{"assistant"},
		},
		{
			name: "user with tool_result",
			msgs: []Message{
				{
					Role: RoleUser,
					Content: []ContentBlock{
						ToolResultBlock{ToolUseID: "t1", Content: "ok"},
					},
				},
			},
			// tool results become standalone "tool" role messages
			wantLen:  1,
			wantRole: []string{"tool"},
		},
		{
			name: "system messages are dropped",
			msgs: []Message{
				{Role: "system", Content: []ContentBlock{TextBlock{Text: "sys"}}},
				NewUserMessage("hello"),
			},
			wantLen:  1,
			wantRole: []string{"user"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := toOpenAIMessages(tc.msgs)
			if err != nil {
				t.Fatalf("toOpenAIMessages error: %v", err)
			}
			if len(got) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tc.wantLen)
			}
			for i, role := range tc.wantRole {
				if got[i].Role != role {
					t.Errorf("msg[%d].Role = %q, want %q", i, got[i].Role, role)
				}
			}
		})
	}
}

func TestToOpenAIMessagesToolCallsField(t *testing.T) {
	msgs := []Message{
		{
			Role: RoleAssistant,
			Content: []ContentBlock{
				ToolUseBlock{ID: "t1", Name: "bash", Input: json.RawMessage(`{"cmd":"ls"}`)},
			},
		},
	}
	got, err := toOpenAIMessages(msgs)
	if err != nil {
		t.Fatalf("toOpenAIMessages error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if len(got[0].ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(got[0].ToolCalls))
	}
	tc := got[0].ToolCalls[0]
	if tc.ID != "t1" || tc.Function.Name != "bash" {
		t.Errorf("ToolCall = {%q %q}, want {t1 bash}", tc.ID, tc.Function.Name)
	}
}

func TestToOpenAIMessagesToolResultToolCallID(t *testing.T) {
	msgs := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				ToolResultBlock{ToolUseID: "t1", Content: "output"},
			},
		},
	}
	got, err := toOpenAIMessages(msgs)
	if err != nil {
		t.Fatalf("toOpenAIMessages error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ToolCallID != "t1" {
		t.Errorf("ToolCallID = %q, want %q", got[0].ToolCallID, "t1")
	}
	if got[0].Content != "output" {
		t.Errorf("Content = %q, want %q", got[0].Content, "output")
	}
}

// ---- Provider metadata ----

func TestOpenAIProviderMetadata(t *testing.T) {
	p := NewOpenAIProvider("key", "gpt-5.5")

	if p.Name() != "openai" {
		t.Errorf("Name() = %q, want %q", p.Name(), "openai")
	}

	models := p.Models()
	if len(models) == 0 {
		t.Fatal("Models() returned empty slice")
	}
	var found bool
	for _, m := range models {
		if m.ID == "gpt-5.5" {
			found = true
		}
	}
	if !found {
		t.Error("Models() does not include gpt-5.5")
	}
}

// ---- Request headers ----

func TestOpenAIRequestHeaders(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOpenAIProvider("my-secret-key", "gpt-5.5", WithOpenAIBaseURL(srv.URL))
	_ = p.Stream(context.Background(), ChatRequest{}, func(StreamEvent) {})

	want := "Bearer my-secret-key"
	if gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
	}
}
