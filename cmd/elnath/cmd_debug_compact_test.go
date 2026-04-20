package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/llm"
)

type debugCompactMockProvider struct {
	chatFn func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
}

func (m *debugCompactMockProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.chatFn != nil {
		return m.chatFn(ctx, req)
	}
	return &llm.ChatResponse{Content: "compacted summary text"}, nil
}

func (m *debugCompactMockProvider) Stream(_ context.Context, _ llm.ChatRequest, _ func(llm.StreamEvent)) error {
	return nil
}

func (m *debugCompactMockProvider) Name() string           { return "mock" }
func (m *debugCompactMockProvider) Models() []llm.ModelInfo { return nil }

func seedLargeSession(t *testing.T, dataDir string, count int) *agent.Session {
	t.Helper()
	sess, err := agent.NewSession(dataDir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for i := 0; i < count; i++ {
		if err := sess.AppendMessage(llm.NewUserMessage(strings.Repeat("filler-content ", 40))); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
		if err := sess.AppendMessage(llm.NewAssistantMessage(strings.Repeat("response-content ", 40))); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	return sess
}

func TestDebugCompact_ErrorsOnMissingSessionID(t *testing.T) {
	var buf bytes.Buffer
	err := runDebugCompact(context.Background(), debugCompactParams{
		DataDir: t.TempDir(),
		Out:     &buf,
	})
	if err == nil {
		t.Fatal("missing session id: err = nil, want error")
	}
	if !strings.Contains(err.Error(), "session") {
		t.Fatalf("err = %q, want message referencing session", err.Error())
	}
}

func TestDebugCompact_ErrorsOnUnknownSession(t *testing.T) {
	var buf bytes.Buffer
	err := runDebugCompact(context.Background(), debugCompactParams{
		SessionID: "nope-nope-nope",
		DataDir:   t.TempDir(),
		Out:       &buf,
	})
	if err == nil {
		t.Fatal("unknown session: err = nil, want error")
	}
}

func TestDebugCompact_UnderBudgetReportsNoop(t *testing.T) {
	dir := t.TempDir()
	sess := seedLargeSession(t, dir, 2)

	var buf bytes.Buffer
	err := runDebugCompact(context.Background(), debugCompactParams{
		SessionID:     sess.ID,
		DataDir:       dir,
		Provider:      &debugCompactMockProvider{},
		ContextWindow: conversation.NewContextWindow(),
		Budget:        1_000_000,
		Out:           &buf,
	})
	if err != nil {
		t.Fatalf("runDebugCompact: %v", err)
	}

	output := buf.String()
	for _, want := range []string{"pre", "post", sess.ID} {
		if !strings.Contains(strings.ToLower(output), want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestDebugCompact_OverBudgetReducesMessageCount(t *testing.T) {
	dir := t.TempDir()
	sess := seedLargeSession(t, dir, 40)

	var buf bytes.Buffer
	err := runDebugCompact(context.Background(), debugCompactParams{
		SessionID:     sess.ID[:13],
		DataDir:       dir,
		Provider:      &debugCompactMockProvider{},
		ContextWindow: conversation.NewContextWindow(),
		Budget:        1_200,
		Out:           &buf,
		JSONOut:       true,
	})
	if err != nil {
		t.Fatalf("runDebugCompact: %v", err)
	}

	var payload struct {
		SessionID      string `json:"session_id"`
		PreCount       int    `json:"pre_count"`
		PostCount      int    `json:"post_count"`
		PreTokens      int    `json:"pre_tokens"`
		PostTokens     int    `json:"post_tokens"`
		SummaryPreview string `json:"summary_preview"`
		Budget         int    `json:"budget"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal json output: %v\nraw=%s", err, buf.String())
	}
	if payload.SessionID != sess.ID {
		t.Errorf("session_id = %q, want full UUID %q (prefix should resolve)", payload.SessionID, sess.ID)
	}
	if payload.PreCount <= payload.PostCount {
		t.Errorf("pre_count=%d post_count=%d: want pre > post after compaction", payload.PreCount, payload.PostCount)
	}
	if payload.PostTokens > payload.PreTokens {
		t.Errorf("post_tokens=%d pre_tokens=%d: compaction must not increase tokens", payload.PostTokens, payload.PreTokens)
	}
	if payload.Budget != 1_200 {
		t.Errorf("budget echo = %d, want 1200", payload.Budget)
	}
}
