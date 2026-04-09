package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/llm"
)

type countingProvider struct {
	chatCalls   int
	streamCalls int
	streamText  string
}

func (p *countingProvider) Name() string { return "mock" }

func (p *countingProvider) Models() []llm.ModelInfo { return nil }

func (p *countingProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.chatCalls++
	if strings.Contains(req.System, "intent classifier") {
		return &llm.ChatResponse{
			Content: `{"intent":"question","confidence":0.95}`,
		}, nil
	}
	return &llm.ChatResponse{Content: "wiki summary"}, nil
}

func (p *countingProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.streamCalls++
	if p.streamText != "" {
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: p.streamText})
	}
	cb(llm.StreamEvent{
		Type:  llm.EventDone,
		Usage: &llm.UsageStats{InputTokens: 11, OutputTokens: 7},
	})
	return nil
}

func newTestExecutionRuntime(t *testing.T, provider llm.Provider) *executionRuntime {
	t.Helper()

	root := t.TempDir()
	cfg := &config.Config{
		DataDir:  filepath.Join(root, "data"),
		WikiDir:  filepath.Join(root, "wiki"),
		LogLevel: "error",
		Permission: config.PermissionConfig{
			Mode: "bypass",
		},
	}

	app, err := core.New(cfg)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}

	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		t.Fatalf("core.OpenDB: %v", err)
	}
	app.RegisterCloser("database", db)

	perm := agent.NewPermission(agent.WithMode(agent.ModeBypass))
	rt, err := buildExecutionRuntime(
		context.Background(),
		cfg,
		app,
		db,
		provider,
		"mock-model",
		"system prompt",
		perm,
	)
	if err != nil {
		t.Fatalf("buildExecutionRuntime: %v", err)
	}

	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Fatalf("app.Close: %v", err)
		}
	})

	return rt
}

func TestExecutionRuntimeRunTaskInvokesWorkflowAndUsageCallbacks(t *testing.T) {
	provider := &countingProvider{streamText: "hello from runtime"}
	rt := newTestExecutionRuntime(t, provider)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var gotIntent string
	var gotWorkflow string
	var streamed strings.Builder
	var gotUsage string

	messages, summary, err := rt.runTask(context.Background(), sess, nil, "what changed in Stella?", orchestrationOutput{
		OnWorkflow: func(intent conversation.Intent, workflow string) {
			gotIntent = string(intent)
			gotWorkflow = workflow
		},
		OnText: func(s string) {
			streamed.WriteString(s)
		},
		OnUsage: func(s string) {
			gotUsage = s
		},
	})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if gotIntent != "question" {
		t.Fatalf("intent = %q, want question", gotIntent)
	}
	if gotWorkflow != "single" {
		t.Fatalf("workflow = %q, want single", gotWorkflow)
	}
	if !strings.Contains(streamed.String(), "hello from runtime") {
		t.Fatalf("streamed output = %q, want runtime text", streamed.String())
	}
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if gotUsage == "" {
		t.Fatal("expected usage summary callback")
	}
	if provider.chatCalls == 0 || provider.streamCalls == 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want both > 0", provider.chatCalls, provider.streamCalls)
	}
	if len(messages) == 0 {
		t.Fatal("expected persisted messages")
	}
}

func TestExecutionRuntimeRunTaskEmitsStructuredProgressEvents(t *testing.T) {
	provider := &countingProvider{streamText: "hello from runtime"}
	rt := newTestExecutionRuntime(t, provider)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var events []daemon.ProgressEvent
	_, _, err = rt.runTask(context.Background(), sess, nil, "what changed in Stella?", orchestrationOutput{
		OnProgress: func(ev daemon.ProgressEvent) {
			events = append(events, ev)
		},
	})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("expected at least 3 progress events, got %d", len(events))
	}
	if events[0].Kind != daemon.ProgressKindWorkflow {
		t.Fatalf("first event kind = %q, want %q", events[0].Kind, daemon.ProgressKindWorkflow)
	}
	if events[0].Intent != "question" || events[0].Workflow != "single" {
		t.Fatalf("first event = %+v, want question/single", events[0])
	}

	var sawText, sawUsage bool
	for _, ev := range events {
		switch ev.Kind {
		case daemon.ProgressKindText:
			sawText = true
			if !strings.Contains(ev.Message, "hello from runtime") {
				t.Fatalf("text progress message = %q, want runtime text", ev.Message)
			}
		case daemon.ProgressKindUsage:
			sawUsage = true
			if ev.Message == "" {
				t.Fatal("expected non-empty usage progress message")
			}
		}
	}
	if !sawText {
		t.Fatal("expected text progress event")
	}
	if !sawUsage {
		t.Fatal("expected usage progress event")
	}
}

func TestDaemonTaskRunnerCreatesSessionAndUsesClassifier(t *testing.T) {
	provider := &countingProvider{streamText: "daemon answer"}
	rt := newTestExecutionRuntime(t, provider)

	var streamed strings.Builder
	result, err := rt.newDaemonTaskRunner()(context.Background(), "tell me a joke", func(s string) {
		streamed.WriteString(s)
	})
	if err != nil {
		t.Fatalf("daemon task runner: %v", err)
	}
	if !strings.Contains(result.Result, "daemon answer") {
		t.Fatalf("result = %q, want daemon answer", result.Result)
	}
	if result.SessionID == "" {
		t.Fatal("expected daemon task result to include session ID")
	}
	if provider.chatCalls == 0 {
		t.Fatal("expected classifier chat call before daemon execution")
	}
	if provider.streamCalls == 0 {
		t.Fatal("expected workflow stream call during daemon execution")
	}

	sess, err := rt.mgr.LoadLatestSession()
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("expected persisted session ID")
	}
	if result.SessionID != sess.ID {
		t.Fatalf("task result session_id = %q, want %q", result.SessionID, sess.ID)
	}
	if len(sess.Messages) < 2 {
		t.Fatalf("session message count = %d, want at least 2", len(sess.Messages))
	}
	if got := sess.Messages[0].Text(); got != "tell me a joke" {
		t.Fatalf("first session message = %q, want original user input", got)
	}
	last := sess.Messages[len(sess.Messages)-1].Text()
	if !strings.Contains(last, "daemon answer") {
		t.Fatalf("last session message = %q, want daemon answer", last)
	}
	history, err := rt.mgr.GetHistory(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) < 2 {
		t.Fatalf("history message count = %d, want at least 2", len(history))
	}
	if got := history[0].Text(); got != "tell me a joke" {
		t.Fatalf("first history message = %q, want original user input", got)
	}
	if got := history[len(history)-1].Text(); !strings.Contains(got, "daemon answer") {
		t.Fatalf("last history message = %q, want daemon answer", got)
	}
	if streamed.Len() == 0 {
		t.Fatal("expected streamed daemon output")
	}
}

func TestDaemonTaskRunnerReusesExistingSessionWhenPayloadRequestsFollowUp(t *testing.T) {
	provider := &countingProvider{streamText: "follow-up answer"}
	rt := newTestExecutionRuntime(t, provider)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, _, err = rt.runTask(context.Background(), sess, nil, "initial request", orchestrationOutput{})
	if err != nil {
		t.Fatalf("seed runTask: %v", err)
	}

	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:    "continue this work with more detail",
		SessionID: sess.ID,
		Surface:   "telegram",
	})
	result, err := rt.newDaemonTaskRunner()(context.Background(), payload, nil)
	if err != nil {
		t.Fatalf("daemon task runner follow-up: %v", err)
	}
	if result.SessionID != sess.ID {
		t.Fatalf("result.SessionID = %q, want %q", result.SessionID, sess.ID)
	}

	history, err := rt.mgr.GetHistory(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) < 4 {
		t.Fatalf("history message count = %d, want at least 4", len(history))
	}
	if got := history[len(history)-2].Text(); got != "continue this work with more detail" {
		t.Fatalf("follow-up user message = %q", got)
	}
	if got := history[len(history)-1].Text(); !strings.Contains(got, "follow-up answer") {
		t.Fatalf("follow-up assistant message = %q", got)
	}
}

func TestExecutionRuntimeWritesRouteAuditWhenEnabled(t *testing.T) {
	provider := &countingProvider{streamText: "hello from runtime"}
	rt := newTestExecutionRuntime(t, provider)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	auditPath := filepath.Join(t.TempDir(), "route-audit.jsonl")
	t.Setenv("ELNATH_EVAL_AUDIT_LOG", auditPath)

	_, _, err = rt.runTask(context.Background(), sess, nil, "fix regression in existing handler and add tests for middleware.go", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("ReadFile audit: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d, want 1", len(lines))
	}
	var record routeAuditRecord
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("unmarshal audit: %v", err)
	}
	if record.SessionID != sess.ID {
		t.Fatalf("session_id = %q, want %q", record.SessionID, sess.ID)
	}
	if record.Workflow != "single" || record.Intent != conversation.IntentQuestion {
		t.Fatalf("unexpected audit record: %+v", record)
	}
	if record.EstimatedFiles == 0 {
		t.Fatalf("expected estimated_files > 0")
	}
	if !record.ExistingCode {
		t.Fatalf("expected existing_code=true in audit record")
	}
}

func TestLikelyRepoFiles(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"internal/middleware/request_id.go",
		"internal/logging/logger.go",
		"cmd/server/main.go",
		"pkg/transport/context.go",
		"test/integration/request_id.test.ts",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		body := "package test\n"
		if strings.HasSuffix(path, "context.go") {
			body = "package test\n// request id middleware logger structured logging\n"
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	hints := likelyRepoFiles(root, "add request id middleware and thread logging", 5)
	if len(hints) == 0 {
		t.Fatal("expected non-empty hints")
	}
	if !strings.Contains(strings.Join(hints, "\n"), "request_id.go") {
		t.Fatalf("expected request_id.go in hints, got %v", hints)
	}
	if !strings.Contains(strings.Join(hints, "\n"), "context.go") {
		t.Fatalf("expected content-matched context.go in hints, got %v", hints)
	}
}

func TestLikelyRepoFilesPrefersRuntimeOverTests(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"packages/vitest/src/runtime/worker.ts":          "retry telemetry worker runtime",
		"packages/vitest/src/node/types/worker.ts":       "retry telemetry worker type",
		"test/cli/test/retry-telemetry.test.ts":          "retry telemetry worker test",
		"examples/opentelemetry/src/basic.test.ts":       "retry telemetry example",
		"test/browser/fixtures/user-event/retry.test.ts": "retry telemetry fixture",
	}
	for path, body := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	hints := likelyRepoFiles(root, "extend an existing TypeScript worker flow to emit retry telemetry without regressing current behavior", 5)
	if len(hints) == 0 {
		t.Fatal("expected non-empty hints")
	}
	joined := strings.Join(hints, "\n")
	if !strings.Contains(joined, "packages/vitest/src/runtime/worker.ts") {
		t.Fatalf("expected runtime worker hint, got %v", hints)
	}
	if strings.Index(joined, "test/cli/test/retry-telemetry.test.ts") != -1 && strings.Index(joined, "packages/vitest/src/runtime/worker.ts") > strings.Index(joined, "test/cli/test/retry-telemetry.test.ts") {
		t.Fatalf("expected runtime worker file to rank ahead of test file, got %v", hints)
	}
}

func TestKeywordHintsSkipsGenericBrownfieldWords(t *testing.T) {
	hints := keywordHints("extend an existing TypeScript worker flow to emit retry telemetry without regressing current behavior")
	joined := strings.Join(hints, ",")
	for _, banned := range []string{"extend", "existing", "flow", "emit", "current", "behavior", "regressing"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("expected %q to be filtered from keyword hints, got %v", banned, hints)
		}
	}
	for _, want := range []string{"retry", "telemetry", "worker"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in keyword hints, got %v", want, hints)
		}
	}
}
