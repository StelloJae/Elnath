package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/agentic"
	agenticruntime "github.com/stello/elnath/internal/agentic/runtime"
	"github.com/stello/elnath/internal/audit"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/orchestrator"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/skill"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

type countingProvider struct {
	chatCalls           int
	streamCalls         int
	streamText          string
	lastSystem          string
	lastModel           string
	lastReasoningEffort string
}

type capabilityCountingProvider struct {
	countingProvider
}

type sequenceStreamProvider struct {
	chatCalls int
	responses []string
	idx       int
}

type scriptedSkillProvider struct {
	streamCalls int
	lastSystem  string
}

type researchRuntimeProvider struct {
	responses  []string
	idx        int
	lastSystem string
	usage      llm.UsageStats
}

type learningRuntimeProvider struct {
	chatCalls   int
	streamCalls int
}

type runtimeMockTool struct {
	name   string
	output string
}

type recordingRuntimeTool struct {
	name    string
	output  string
	calls   int
	command string
}

type stubWorkflow struct{ name string }

type captureLearningWorkflow struct {
	name        string
	sawLearning bool
}

type runtimeCompressionHook struct {
	tracker  *tools.ReadTracker
	readPath string
	calls    [][2]int
	reset    bool
}

func (h *runtimeCompressionHook) PreToolUse(context.Context, string, json.RawMessage) (agent.HookResult, error) {
	return agent.HookResult{Action: agent.HookAllow}, nil
}

func (h *runtimeCompressionHook) PostToolUse(context.Context, string, json.RawMessage, *tools.Result) error {
	return nil
}

func (h *runtimeCompressionHook) OnCompression(_ context.Context, beforeCount, afterCount int) error {
	h.calls = append(h.calls, [2]int{beforeCount, afterCount})
	h.reset = h.tracker.CheckRead(h.readPath, 1, 1) == ""
	return nil
}

func (w *stubWorkflow) Name() string { return w.name }

func (w *stubWorkflow) Run(_ context.Context, input orchestrator.WorkflowInput) (*orchestrator.WorkflowResult, error) {
	return &orchestrator.WorkflowResult{
		Messages: append(input.Messages, llm.NewAssistantMessage(w.name+" workflow")),
		Summary:  w.name + " workflow",
		Workflow: w.name,
	}, nil
}

func (w *captureLearningWorkflow) Name() string { return w.name }

func (w *captureLearningWorkflow) Run(_ context.Context, input orchestrator.WorkflowInput) (*orchestrator.WorkflowResult, error) {
	w.sawLearning = input.Learning != nil
	return &orchestrator.WorkflowResult{
		Messages: append(input.Messages, llm.NewAssistantMessage(w.name+" workflow")),
		Summary:  w.name + " workflow",
		Workflow: w.name,
	}, nil
}

type errorWorkflow struct {
	name string
	err  error
}

func (w *errorWorkflow) Name() string { return w.name }

func (w *errorWorkflow) Run(_ context.Context, _ orchestrator.WorkflowInput) (*orchestrator.WorkflowResult, error) {
	return nil, w.err
}

func TestCompressionHookContextWindowFiresAfterDedupReset(t *testing.T) {
	cw := conversation.NewContextWindow()
	tracker := tools.NewReadTracker()
	readPath := filepath.Join(t.TempDir(), "tracked.txt")
	if msg := tracker.CheckRead(readPath, 1, 1); msg != "" {
		t.Fatalf("initial CheckRead = %q, want empty", msg)
	}

	hooks := agent.NewHookRegistry()
	hook := &runtimeCompressionHook{tracker: tracker, readPath: readPath}
	hooks.Add(hook)
	wrapper := newCompressionHookContextWindow(cw, hooks, tracker)

	body := strings.Repeat("a", 400)
	msgs := make([]llm.Message, 12)
	msgs[0] = llm.NewUserMessage(body)
	for i := 1; i < 4; i++ {
		msgs[i] = llm.NewAssistantMessage(body)
	}
	for i := 4; i < len(msgs); i++ {
		if i%2 == 0 {
			msgs[i] = llm.NewUserMessage(body)
		} else {
			msgs[i] = llm.NewAssistantMessage(body)
		}
	}

	provider := &countingProvider{}
	result, err := wrapper.CompressMessages(context.Background(), provider, msgs, 200)
	if err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if len(result) >= len(msgs) {
		t.Fatalf("message count = %d, want less than %d", len(result), len(msgs))
	}
	if len(hook.calls) != 1 {
		t.Fatalf("compression hook calls = %d, want 1", len(hook.calls))
	}
	if hook.calls[0] != [2]int{len(msgs), len(result)} {
		t.Fatalf("compression hook args = %v, want [%d %d]", hook.calls[0], len(msgs), len(result))
	}
	if !hook.reset {
		t.Fatal("expected read dedup reset before compression hook")
	}
}

func (p *countingProvider) Name() string { return "mock" }

func (p *countingProvider) Models() []llm.ModelInfo { return nil }

func (p *capabilityCountingProvider) Name() string { return "openai-responses" }

func (p *capabilityCountingProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{
		Name:                    p.Name(),
		ReasoningEffort:         llm.ReasoningEffortNativeWithUnsupportedRetry,
		ReasoningEffortFallback: "retry_without_reasoning_on_400_or_422_unsupported_effort",
	}
}

func (p *countingProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.chatCalls++
	if strings.Contains(req.System, "intent classifier") {
		return &llm.ChatResponse{
			Content: `{"intent":"question","confidence":0.95}`,
		}, nil
	}
	return &llm.ChatResponse{Content: "wiki summary"}, nil
}

func (p *countingProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.streamCalls++
	p.lastSystem = req.System
	p.lastModel = req.Model
	p.lastReasoningEffort = req.ReasoningEffort
	if p.streamText != "" {
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: p.streamText})
	}
	cb(llm.StreamEvent{
		Type:  llm.EventDone,
		Usage: &llm.UsageStats{InputTokens: 11, OutputTokens: 7},
	})
	return nil
}

func (p *sequenceStreamProvider) Name() string { return "mock" }

func (p *sequenceStreamProvider) Models() []llm.ModelInfo { return nil }

func (p *sequenceStreamProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.chatCalls++
	if strings.Contains(req.System, "intent classifier") {
		return &llm.ChatResponse{Content: `{"intent":"question","confidence":0.95}`}, nil
	}
	return &llm.ChatResponse{}, nil
}

func (p *sequenceStreamProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	text := ""
	if len(p.responses) > 0 {
		if p.idx < len(p.responses) {
			text = p.responses[p.idx]
		} else {
			text = p.responses[len(p.responses)-1]
		}
		p.idx++
	}
	if text != "" {
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: text})
	}
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 11, OutputTokens: 7}})
	return nil
}

func (p *scriptedSkillProvider) Name() string { return "mock" }

func (p *scriptedSkillProvider) Models() []llm.ModelInfo { return nil }

func (p *scriptedSkillProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *scriptedSkillProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.streamCalls++
	p.lastSystem = req.System
	if p.streamCalls == 1 {
		cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: "tool-1", Name: "mock_tool"}})
		cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: "tool-1", Name: "mock_tool", Input: `{}`}})
		cb(llm.StreamEvent{Type: llm.EventDone})
		return nil
	}
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "skill output"})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 7, OutputTokens: 5}})
	return nil
}

func (p *researchRuntimeProvider) Name() string { return "mock" }

func (p *researchRuntimeProvider) Models() []llm.ModelInfo { return nil }

func (p *researchRuntimeProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if strings.Contains(req.System, "intent classifier") {
		return &llm.ChatResponse{Content: `{"intent":"question","confidence":0.95}`}, nil
	}
	p.lastSystem = req.System
	content := p.responses[p.idx]
	p.idx++
	return &llm.ChatResponse{Content: content}, nil
}

func (p *researchRuntimeProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.lastSystem = req.System
	content := p.responses[p.idx]
	p.idx++
	usage := p.usage
	if usage == (llm.UsageStats{}) {
		usage = llm.UsageStats{InputTokens: 10, OutputTokens: 5}
	}
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: content})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &usage})
	return nil
}

func (p *learningRuntimeProvider) Name() string { return "mock" }

func (p *learningRuntimeProvider) Models() []llm.ModelInfo { return nil }

func (p *learningRuntimeProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.chatCalls++
	if strings.Contains(req.System, "intent classifier") {
		return &llm.ChatResponse{Content: `{"intent":"question","confidence":0.95}`}, nil
	}
	return &llm.ChatResponse{}, nil
}

func (p *learningRuntimeProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.streamCalls++
	if p.streamCalls == 1 {
		cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: "bash-1", Name: "bash"}})
		cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: "bash-1", Name: "bash", Input: `{"command":"pwd"}`}})
		cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
		return nil
	}
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "done"})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 5, OutputTokens: 3}})
	return nil
}

func (t *runtimeMockTool) Name() string                           { return t.name }
func (t *runtimeMockTool) Description() string                    { return t.name }
func (t *runtimeMockTool) Schema() json.RawMessage                { return json.RawMessage(`{"type":"object"}`) }
func (t *runtimeMockTool) IsConcurrencySafe(json.RawMessage) bool { return false }
func (t *runtimeMockTool) Reversible() bool                       { return false }
func (t *runtimeMockTool) Scope(json.RawMessage) tools.ToolScope  { return tools.ConservativeScope() }
func (t *runtimeMockTool) ShouldCancelSiblingsOnError() bool      { return false }
func (t *runtimeMockTool) Execute(context.Context, json.RawMessage) (*tools.Result, error) {
	return tools.SuccessResult(t.output), nil
}

func (t *recordingRuntimeTool) Name() string                           { return t.name }
func (t *recordingRuntimeTool) Description() string                    { return t.name }
func (t *recordingRuntimeTool) Schema() json.RawMessage                { return json.RawMessage(`{"type":"object"}`) }
func (t *recordingRuntimeTool) IsConcurrencySafe(json.RawMessage) bool { return false }
func (t *recordingRuntimeTool) Reversible() bool                       { return false }
func (t *recordingRuntimeTool) Scope(json.RawMessage) tools.ToolScope {
	return tools.ConservativeScope()
}
func (t *recordingRuntimeTool) ShouldCancelSiblingsOnError() bool { return false }
func (t *recordingRuntimeTool) Execute(_ context.Context, params json.RawMessage) (*tools.Result, error) {
	t.calls++
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return tools.ErrorResult(err.Error()), nil
	}
	t.command = payload.Command
	return tools.SuccessResult(t.output), nil
}

func countExactUserTurns(messages []llm.Message, want string) int {
	count := 0
	for _, msg := range messages {
		if msg.Role == llm.RoleUser && msg.Text() == want {
			count++
		}
	}
	return count
}

func newTestExecutionRuntime(t *testing.T, provider llm.Provider) *executionRuntime {
	t.Helper()
	return newTestExecutionRuntimeWithMode(t, provider, false)
}

func newTestExecutionRuntimeWithMode(t *testing.T, provider llm.Provider, daemonMode bool) *executionRuntime {
	t.Helper()
	return newTestExecutionRuntimeWithConfig(t, provider, daemonMode, func(*config.Config) {})
}

func newTestExecutionRuntimeWithConfig(t *testing.T, provider llm.Provider, daemonMode bool, mutate func(*config.Config)) *executionRuntime {
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
	mutate(cfg)

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
		self.New(cfg.DataDir),
		"",
		perm,
		root,
		nil,
		identity.LegacyPrincipal(),
		daemonMode,
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

func TestBenchmarkModeUsesRootWorkDirWithoutSessionWorkspace(t *testing.T) {
	t.Setenv("ELNATH_BENCHMARK_MODE", "1")
	envDir := t.TempDir()
	t.Setenv("ELNATH_BENCHMARK_ENV_DIR", envDir)

	rt := newTestExecutionRuntime(t, &countingProvider{})
	sess := &agent.Session{ID: "bench-session"}

	if got := rt.sessionRenderWorkDir(sess); got != rt.workDir {
		t.Fatalf("sessionRenderWorkDir() = %q, want benchmark root %q", got, rt.workDir)
	}
	if _, err := os.Stat(filepath.Join(rt.workDir, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("benchmark session workdir created sessions directory: %v", err)
	}

	ctx := rt.toolContextForSession(context.Background(), sess)
	if got := tools.SessionIDFrom(ctx); got != sess.ID {
		t.Fatalf("benchmark tool context session id = %q, want %q", got, sess.ID)
	}
	sessionDir, err := tools.SessionWorkDirFromContext(ctx, rt.guard)
	if err != nil {
		t.Fatalf("SessionWorkDirFromContext: %v", err)
	}
	if sessionDir != rt.workDir {
		t.Fatalf("benchmark tool session dir = %q, want root %q", sessionDir, rt.workDir)
	}
	if got := tools.SessionEnvDirFrom(ctx); got != envDir {
		t.Fatalf("benchmark tool env dir = %q, want %q", got, envDir)
	}
}

func seedRuntimeSessionPage(t *testing.T, idx *wiki.Index, path, title, content string, tags []string) {
	t.Helper()

	now := time.Now().UTC()
	if err := idx.Upsert(&wiki.Page{
		Path:    path,
		Title:   title,
		Type:    wiki.PageTypeSource,
		Tags:    tags,
		Content: content,
		Created: now,
		Updated: now,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
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

func TestProgressObserverDispatchesRepresentativeEventTypesAndIgnoresUnknown(t *testing.T) {
	var got []daemon.ProgressEvent
	observer := progressObserver{onProgress: func(ev daemon.ProgressEvent) {
		got = append(got, ev)
	}}

	observer.OnEvent(event.WorkflowProgressEvent{Intent: "question", Workflow: "single"})
	observer.OnEvent(event.ToolProgressEvent{ToolName: "wiki_search", Preview: "looking up docs"})
	observer.OnEvent(event.TextDeltaEvent{Content: "partial output"})
	observer.OnEvent(event.UsageProgressEvent{Summary: "tokens: 42"})
	observer.OnEvent(event.ResearchProgressEvent{Message: "researching"})
	observer.OnEvent(event.IterationStartEvent{})

	if len(got) != 5 {
		t.Fatalf("progress events = %d, want 5", len(got))
	}
	if got[0].Kind != daemon.ProgressKindWorkflow || got[0].Intent != "question" || got[0].Workflow != "single" {
		t.Fatalf("workflow event = %+v, want workflow/question/single", got[0])
	}
	if got[1].Kind != daemon.ProgressKindTool || got[1].ToolName != "wiki_search" || got[1].Preview != "looking up docs" {
		t.Fatalf("tool event = %+v, want tool/wiki_search/looking up docs", got[1])
	}
	if got[2].Kind != daemon.ProgressKindText || got[2].Message == "" {
		t.Fatalf("text event = %+v, want text with non-empty message", got[2])
	}
	if got[3].Kind != daemon.ProgressKindUsage || got[3].Message != "tokens: 42" {
		t.Fatalf("usage event = %+v, want usage/tokens: 42", got[3])
	}
	if got[4].Kind != daemon.ProgressKindText || got[4].Message == "" {
		t.Fatalf("research event = %+v, want text with non-empty message", got[4])
	}
}

func TestLegacyCallbackObserverDispatchesRepresentativeEventTypesAndIgnoresUnknown(t *testing.T) {
	var (
		gotIntent   conversation.Intent
		gotWorkflow string
		gotText     []string
		gotUsage    string
	)
	observer := legacyCallbackObserver{
		onWorkflow: func(intent conversation.Intent, workflow string) {
			gotIntent = intent
			gotWorkflow = workflow
		},
		onText: func(s string) {
			gotText = append(gotText, s)
		},
		onUsage: func(summary string) {
			gotUsage = summary
		},
	}

	observer.OnEvent(event.WorkflowProgressEvent{Intent: "question", Workflow: "single"})
	observer.OnEvent(event.TextDeltaEvent{Content: "hello"})
	observer.OnEvent(event.ResearchProgressEvent{Message: "from research"})
	observer.OnEvent(event.UsageProgressEvent{Summary: "tokens: 42"})
	observer.OnEvent(event.IterationStartEvent{})

	if gotIntent != conversation.Intent("question") || gotWorkflow != "single" {
		t.Fatalf("workflow callback = (%q, %q), want (question, single)", gotIntent, gotWorkflow)
	}
	if len(gotText) != 2 || gotText[0] != "hello" || gotText[1] != "from research" {
		t.Fatalf("text callbacks = %#v, want [hello from research]", gotText)
	}
	if gotUsage != "tokens: 42" {
		t.Fatalf("usage callback = %q, want tokens: 42", gotUsage)
	}
}

func TestNewBusDefaultFallbackStillUsesTerminalObserverWithoutLegacyCallbacks(t *testing.T) {
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

func TestExecutionRuntimeRunTaskAppliesWorkflowPreference(t *testing.T) {
	provider := &countingProvider{}
	rt := newTestExecutionRuntime(t, provider)
	rt.principal = identity.Principal{UserID: "legacy", ProjectID: "elnath", Surface: "cli"}
	rt.router = orchestrator.NewRouter(map[string]orchestrator.Workflow{
		"single":   &stubWorkflow{name: "single"},
		"research": &stubWorkflow{name: "research"},
	})

	relPath := filepath.Join("projects", "elnath", "routing-preferences.md")
	absPath := filepath.Join(rt.wikiStore.WikiDir(), relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	raw := `---
title: Project Routing Preferences
type: concept
preferred_workflows:
  question: research
---

Prefer research for question intents.
`
	if err := os.WriteFile(absPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var gotWorkflow string
	_, summary, err := rt.runTask(context.Background(), sess, nil, "what changed in Stella?", orchestrationOutput{
		OnWorkflow: func(_ conversation.Intent, workflow string) {
			gotWorkflow = workflow
		},
	})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if gotWorkflow != "research" {
		t.Fatalf("workflow = %q, want research", gotWorkflow)
	}
	if summary != "research workflow" {
		t.Fatalf("summary = %q, want %q", summary, "research workflow")
	}
}

func TestExecutionRuntimeRunTaskIgnoresMalformedWorkflowPreference(t *testing.T) {
	provider := &countingProvider{}
	rt := newTestExecutionRuntime(t, provider)
	rt.principal = identity.Principal{UserID: "legacy", ProjectID: "elnath", Surface: "cli"}
	rt.router = orchestrator.NewRouter(map[string]orchestrator.Workflow{
		"single":   &stubWorkflow{name: "single"},
		"research": &stubWorkflow{name: "research"},
	})

	relPath := filepath.Join("projects", "elnath", "routing-preferences.md")
	absPath := filepath.Join(rt.wikiStore.WikiDir(), relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	raw := `---
title: Broken Routing Preferences
type: concept
preferred_workflows: [question
---
`
	if err := os.WriteFile(absPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var gotWorkflow string
	_, _, err = rt.runTask(context.Background(), sess, nil, "what changed in Stella?", orchestrationOutput{
		OnWorkflow: func(_ conversation.Intent, workflow string) {
			gotWorkflow = workflow
		},
	})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if gotWorkflow != "single" {
		t.Fatalf("workflow = %q, want single", gotWorkflow)
	}
}

func TestExecutionRuntimeRunTaskRecordsOutcomeOnWorkflowError(t *testing.T) {
	provider := &countingProvider{}
	rt := newTestExecutionRuntime(t, provider)
	rt.principal = identity.Principal{UserID: "legacy", ProjectID: "elnath", Surface: "cli"}
	rt.router = orchestrator.NewRouter(map[string]orchestrator.Workflow{
		"single": &errorWorkflow{name: "single", err: fmt.Errorf("boom")},
	})

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, _, err = rt.runTask(context.Background(), sess, nil, "what changed in Stella?", orchestrationOutput{})
	if err == nil {
		t.Fatal("runTask: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("runTask error = %v, want error wrapping boom", err)
	}

	outcomePath := filepath.Join(rt.app.Config.DataDir, "outcomes.jsonl")
	data, readErr := os.ReadFile(outcomePath)
	if readErr != nil {
		t.Fatalf("read outcomes: %v", readErr)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("outcomes.jsonl lines = %d, want 1 non-empty line; got %q", len(lines), string(data))
	}

	var rec learning.OutcomeRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("decode outcome record: %v", err)
	}
	if rec.Success {
		t.Errorf("outcome Success = true, want false")
	}
	if rec.FinishReason != "error" {
		t.Errorf("outcome FinishReason = %q, want %q", rec.FinishReason, "error")
	}
	if rec.Workflow != "single" {
		t.Errorf("outcome Workflow = %q, want single", rec.Workflow)
	}
	if rec.ProjectID != "elnath" {
		t.Errorf("outcome ProjectID = %q, want elnath", rec.ProjectID)
	}
}

func TestExecutionRuntimeRecordOutcomeSkipsAgenticOutcomeWithoutPassedVerification(t *testing.T) {
	ctx := context.Background()
	provider := &countingProvider{}
	rt := newTestExecutionRuntime(t, provider)
	task, err := rt.agenticStore.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "Unverified routing outcome",
		Prompt:             "do not teach routing yet",
		Status:             agentic.TaskStatusSucceeded,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}

	rt.recordOutcome(ctx, outcomeInput{
		agenticTaskID: task.ID,
		routeCtx:      &orchestrator.RoutingContext{ProjectID: "elnath"},
		intent:        conversation.IntentQuestion,
		workflow:      "single",
		finishReason:  "success",
		success:       true,
		userInput:     "what changed?",
	})

	records, err := rt.outcomeStore.ForProject("elnath", 10)
	if err != nil {
		t.Fatalf("ForProject: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("outcome records = %+v, want none before passed verification", records)
	}
}

func TestExecutionRuntimeRecordOutcomeAppendsAgenticOutcomeAfterPassedVerification(t *testing.T) {
	ctx := context.Background()
	provider := &countingProvider{}
	rt := newTestExecutionRuntime(t, provider)
	task, err := rt.agenticStore.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "Verified routing outcome",
		Prompt:             "routing may learn this",
		Status:             agentic.TaskStatusSucceeded,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	if _, err := rt.agenticStore.CreateVerificationRun(ctx, agentic.VerificationRun{
		TaskID:           task.ID,
		CriteriaJSON:     `{"kind":"routing-outcome"}`,
		EvidenceRefsJSON: `[]`,
		Verdict:          agentic.VerificationVerdictPassed,
		Reason:           "verified outcome",
	}); err != nil {
		t.Fatalf("CreateVerificationRun: %v", err)
	}

	rt.recordOutcome(ctx, outcomeInput{
		agenticTaskID: task.ID,
		routeCtx:      &orchestrator.RoutingContext{ProjectID: "elnath"},
		intent:        conversation.IntentQuestion,
		workflow:      "single",
		finishReason:  "success",
		success:       true,
		userInput:     "what changed?",
	})

	records, err := rt.outcomeStore.ForProject("elnath", 10)
	if err != nil {
		t.Fatalf("ForProject: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("outcome records = %d, want 1", len(records))
	}
	if records[0].Workflow != "single" || !records[0].Success {
		t.Fatalf("unexpected outcome record: %+v", records[0])
	}
}

func TestDaemonTaskRunnerRecordsOutcomeOnLoadSessionFailure(t *testing.T) {
	provider := &countingProvider{}
	rt := newTestExecutionRuntime(t, provider)

	ownerPrincipal := identity.Principal{UserID: "owner", ProjectID: "proj-a", Surface: "cli"}
	sess, err := rt.mgr.NewSessionWithPrincipal(ownerPrincipal)
	if err != nil {
		t.Fatalf("NewSessionWithPrincipal: %v", err)
	}

	otherPrincipal := identity.Principal{UserID: "intruder", ProjectID: "proj-b", Surface: "telegram"}
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:    "steal this session",
		SessionID: sess.ID,
		Surface:   "telegram",
		Principal: otherPrincipal,
	})
	if _, err := rt.newDaemonTaskRunner()(context.Background(), payload, nil); err == nil {
		t.Fatal("expected load session failure, got nil")
	}

	outcomePath := filepath.Join(rt.app.Config.DataDir, "outcomes.jsonl")
	data, readErr := os.ReadFile(outcomePath)
	if readErr != nil {
		t.Fatalf("read outcomes: %v", readErr)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("outcomes.jsonl lines = %d, want 1 non-empty; got %q", len(lines), string(data))
	}

	var rec learning.OutcomeRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("decode outcome: %v", err)
	}
	if rec.Success {
		t.Errorf("Success = true, want false")
	}
	if rec.FinishReason != "load_session_failed" {
		t.Errorf("FinishReason = %q, want load_session_failed", rec.FinishReason)
	}
	if rec.ProjectID != "proj-b" {
		t.Errorf("ProjectID = %q, want proj-b", rec.ProjectID)
	}
}

func TestDaemonTaskRunnerSkipsAgenticSetupFailureOutcomeWithoutPassedVerification(t *testing.T) {
	ctx := context.Background()
	provider := &countingProvider{}
	rt := newTestExecutionRuntime(t, provider)
	agenticTask, err := rt.agenticStore.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "Unverified setup failure",
		Prompt:             "load stale session",
		Status:             agentic.TaskStatusRunning,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}

	ownerPrincipal := identity.Principal{UserID: "owner", ProjectID: "proj-a", Surface: "cli"}
	sess, err := rt.mgr.NewSessionWithPrincipal(ownerPrincipal)
	if err != nil {
		t.Fatalf("NewSessionWithPrincipal: %v", err)
	}
	otherPrincipal := identity.Principal{UserID: "intruder", ProjectID: "proj-b", Surface: "telegram"}
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:    "steal this session",
		SessionID: sess.ID,
		Surface:   "telegram",
		Principal: otherPrincipal,
	})
	ctx = daemon.WithAgenticTaskID(ctx, agenticTask.ID)
	if _, err := rt.newDaemonTaskRunner()(ctx, payload, nil); err == nil {
		t.Fatal("expected load session failure, got nil")
	}

	records, err := rt.outcomeStore.ForProject("proj-b", 10)
	if err != nil {
		t.Fatalf("ForProject: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("agentic setup failure outcomes = %+v, want none before passed verification", records)
	}
}

func TestExecutionRuntimeResearchWorkflowAppliesLearning(t *testing.T) {
	provider := &researchRuntimeProvider{responses: []string{
		`[{"id":"H1","statement":"Useful hypothesis","rationale":"Because","test_plan":"Do X","priority":1}]`,
		`I investigated. {"findings":"Found something","evidence":"Data","confidence":"high","supported":true}`,
		`[{"id":"H2","statement":"Useful hypothesis 2","rationale":"Because","test_plan":"Do Y","priority":1}]`,
		`I investigated. {"findings":"Found something else","evidence":"More data","confidence":"high","supported":true}`,
		`Research summary`,
	}}
	rt := newTestExecutionRuntime(t, provider)
	rt.principal = identity.Principal{UserID: "legacy", ProjectID: "elnath", Surface: "cli"}

	relPath := filepath.Join("projects", "elnath", "routing-preferences.md")
	absPath := filepath.Join(rt.wikiStore.WikiDir(), relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	raw := `---
title: Project Routing Preferences
type: concept
preferred_workflows:
  question: research
---

Prefer research for question intents.
`
	if err := os.WriteFile(absPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	before := rt.selfState.GetPersona()
	input := "what changed in AKIAIOSFODNN7EXAMPLE?"

	messages, _, err := rt.runTask(context.Background(), sess, nil, input, orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(messages))
	}
	if messages[0].Role != llm.RoleUser || messages[1].Role != llm.RoleAssistant {
		t.Fatalf("message roles = [%s, %s], want [user, assistant]", messages[0].Role, messages[1].Role)
	}
	if got := countExactUserTurns(messages, input); got != 1 {
		t.Fatalf("exact user turn count = %d, want 1", got)
	}

	data, err := os.ReadFile(filepath.Join(rt.workDir, "data", "lessons.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile lessons.jsonl: %v", err)
	}
	if !strings.Contains(string(data), "Found something") {
		t.Fatalf("lessons.jsonl = %q, want persisted research lesson", string(data))
	}
	if strings.Contains(string(data), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("lessons.jsonl = %q, want secret redacted on research path", string(data))
	}
	if !strings.Contains(string(data), "[REDACTED:aws-access-key]") {
		t.Fatalf("lessons.jsonl = %q, want aws redaction marker on research path", string(data))
	}
	if strings.Contains(string(data), `"source":"agent"`) {
		t.Fatalf("lessons.jsonl = %q, want no agent lesson on research path", string(data))
	}
	if rt.selfState.GetPersona().Persistence <= before.Persistence {
		t.Fatalf("Persistence = %v, want > %v", rt.selfState.GetPersona().Persistence, before.Persistence)
	}
}

func TestExecutionRuntimeResearchWorkflowUsesConfiguredLimitsAndUsageTracking(t *testing.T) {
	provider := &researchRuntimeProvider{
		responses: []string{
			`[{"id":"H1","statement":"Useful hypothesis","rationale":"Because","test_plan":"Do X","priority":1}]`,
			`I investigated. {"findings":"Found something","evidence":"Data","confidence":"high","supported":true}`,
			`[{"id":"H2","statement":"Useful hypothesis 2","rationale":"Because","test_plan":"Do Y","priority":1}]`,
			`I investigated. {"findings":"Found something else","evidence":"More data","confidence":"high","supported":true}`,
			`Research summary`,
		},
		usage: llm.UsageStats{InputTokens: 100_000, OutputTokens: 100_000},
	}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.Research.MaxRounds = 2
		cfg.Research.CostCapUSD = 10.0
	})
	rt.principal = identity.Principal{UserID: "legacy", ProjectID: "elnath", Surface: "cli"}

	relPath := filepath.Join("projects", "elnath", "routing-preferences.md")
	absPath := filepath.Join(rt.wikiStore.WikiDir(), relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	raw := `---
title: Project Routing Preferences
type: concept
preferred_workflows:
  question: research
---

Prefer research for question intents.
`
	if err := os.WriteFile(absPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, _, err = rt.runTask(context.Background(), sess, nil, "what changed in Stella?", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rt.workDir, "data", "lessons.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile lessons.jsonl: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "Found something else") {
		t.Fatalf("lessons.jsonl = %q, want second-round lesson", got)
	}
	if !strings.Contains(got, "exceeded budget") {
		t.Fatalf("lessons.jsonl = %q, want budget lesson from tracked usage", got)
	}
}

func TestExecutionRuntimeSingleWorkflowPersistsAgentLessons(t *testing.T) {
	provider := &learningRuntimeProvider{}
	rt := newTestExecutionRuntime(t, provider)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, _, err = rt.runTask(context.Background(), sess, nil, "what changed in Stella?", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rt.workDir, "data", "lessons.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile lessons.jsonl: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `"source":"agent:single"`) {
		t.Fatalf("lessons.jsonl = %q, want agent lesson", got)
	}
	if !strings.Contains(got, "Efficient completion") {
		t.Fatalf("lessons.jsonl = %q, want efficient completion lesson", got)
	}
}

func TestExecutionRuntimeSingleWorkflowRedactsTopic(t *testing.T) {
	provider := &learningRuntimeProvider{}
	rt := newTestExecutionRuntime(t, provider)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	input := "what changed in AKIAIOSFODNN7EXAMPLE?"
	_, _, err = rt.runTask(context.Background(), sess, nil, input, orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rt.workDir, "data", "lessons.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile lessons.jsonl: %v", err)
	}
	got := string(data)
	if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("lessons.jsonl = %q, want secret redacted on agent path", got)
	}
	if !strings.Contains(got, "[REDACTED:aws-access-key]") {
		t.Fatalf("lessons.jsonl = %q, want aws redaction marker on agent path", got)
	}
}

func TestExecutionRuntimeSingleWorkflowLearningDisabledDoesNotPersistAgentLessons(t *testing.T) {
	provider := &learningRuntimeProvider{}
	rt := newTestExecutionRuntime(t, provider)
	rt.learningStore = nil

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, _, err = rt.runTask(context.Background(), sess, nil, "what changed in Stella?", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rt.workDir, "data", "lessons.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("lessons file stat err = %v, want not exists", err)
	}
}

func TestExecutionRuntimeLearningInjectedForAllAgentWorkflows(t *testing.T) {
	for _, workflowName := range []string{"single", "team", "ralph", "autopilot"} {
		t.Run(workflowName, func(t *testing.T) {
			provider := &countingProvider{}
			rt := newTestExecutionRuntime(t, provider)
			rt.principal = identity.Principal{UserID: "legacy", ProjectID: "elnath", Surface: "cli"}
			capture := &captureLearningWorkflow{name: workflowName}
			rt.router = orchestrator.NewRouter(map[string]orchestrator.Workflow{
				"single":     &stubWorkflow{name: "single"},
				workflowName: capture,
			})

			relPath := filepath.Join("projects", "elnath", "routing-preferences.md")
			absPath := filepath.Join(rt.wikiStore.WikiDir(), relPath)
			if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			raw := `---
title: Project Routing Preferences
type: concept
preferred_workflows:
  question: ` + workflowName + `
---

Prefer the requested workflow for question intents.
`
			if err := os.WriteFile(absPath, []byte(raw), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			sess, err := rt.mgr.NewSession()
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			_, _, err = rt.runTask(context.Background(), sess, nil, "what changed in Stella?", orchestrationOutput{})
			if err != nil {
				t.Fatalf("runTask: %v", err)
			}
			if !capture.sawLearning {
				t.Fatalf("workflow %q did not receive learning deps", workflowName)
			}
		})
	}
}

func TestExecutionRuntimeLearningDepsLLMDisabledByDefault(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{})
	deps := rt.learningDeps()
	if deps == nil {
		t.Fatal("learningDeps() = nil, want deps")
	}
	if deps.LLMExtractor != nil {
		t.Fatalf("LLMExtractor = %#v, want nil", deps.LLMExtractor)
	}
	if deps.Breaker != nil {
		t.Fatalf("Breaker = %#v, want nil", deps.Breaker)
	}
	if deps.CursorStore == nil {
		t.Fatal("CursorStore = nil, want initialized store")
	}
	if deps.ComplexityGate.MinMessages != 5 || !deps.ComplexityGate.RequireToolCall {
		t.Fatalf("ComplexityGate = %#v, want min_messages=5 require_tool_call=true", deps.ComplexityGate)
	}
}

func TestExecutionRuntimeLearningDepsLLMEnabled(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.LLMExtraction.Enabled = true
		cfg.LLMExtraction.MinMessages = 7
	})
	deps := rt.learningDeps()
	if deps == nil {
		t.Fatal("learningDeps() = nil, want deps")
	}
	// Without a dedicated Anthropic credential, lesson extraction reuses the
	// main provider (here countingProvider) wrapped in an AnthropicExtractor.
	if _, ok := deps.LLMExtractor.(*learning.AnthropicExtractor); !ok {
		t.Fatalf("LLMExtractor type = %T, want *learning.AnthropicExtractor (reusing main provider)", deps.LLMExtractor)
	}
	if deps.Breaker == nil {
		t.Fatal("Breaker = nil, want initialized breaker")
	}
	if deps.CursorStore == nil {
		t.Fatal("CursorStore = nil, want initialized store")
	}
	if deps.ComplexityGate.MinMessages != 7 || !deps.ComplexityGate.RequireToolCall {
		t.Fatalf("ComplexityGate = %#v, want min_messages=7 require_tool_call=true", deps.ComplexityGate)
	}
}

func TestExecutionRuntimeLearningDepsLLMUsesAnthropicExtractorWhenConfigured(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.LLMExtraction.Enabled = true
		cfg.Anthropic.APIKey = "test-key"
	})
	deps := rt.learningDeps()
	if deps == nil {
		t.Fatal("learningDeps() = nil, want deps")
	}
	if _, ok := deps.LLMExtractor.(*learning.AnthropicExtractor); !ok {
		t.Fatalf("LLMExtractor type = %T, want *learning.AnthropicExtractor", deps.LLMExtractor)
	}
	if deps.Breaker == nil {
		t.Fatal("Breaker = nil, want initialized breaker")
	}
}

func TestDaemonTaskRunnerCreatesSessionAndUsesClassifier(t *testing.T) {
	provider := &countingProvider{streamText: "daemon answer"}
	rt := newTestExecutionRuntime(t, provider)

	var streamed strings.Builder
	result, err := rt.newDaemonTaskRunner()(context.Background(), "tell me a joke", event.OnTextToSink(func(s string) {
		streamed.WriteString(s)
	}))
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

func TestDaemonTaskRunnerPersistsAgentLessons(t *testing.T) {
	provider := &learningRuntimeProvider{}
	rt := newTestExecutionRuntime(t, provider)

	result, err := rt.newDaemonTaskRunner()(context.Background(), "tell me current directory", nil)
	if err != nil {
		t.Fatalf("daemon task runner: %v", err)
	}
	if result.SessionID == "" {
		t.Fatal("expected daemon task runner session ID")
	}

	data, err := os.ReadFile(filepath.Join(rt.workDir, "data", "lessons.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile lessons.jsonl: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `"source":"agent:single"`) {
		t.Fatalf("lessons.jsonl = %q, want agent lesson", got)
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

func TestDaemonTaskRunnerCreatesSessionWithPayloadPrincipal(t *testing.T) {
	provider := &countingProvider{streamText: "daemon answer"}
	rt := newTestExecutionRuntime(t, provider)
	principal := identity.Principal{UserID: "telegram-user", ProjectID: "elnath", Surface: "telegram"}

	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:    "tell me a joke",
		Surface:   "telegram",
		Principal: principal,
	})
	result, err := rt.newDaemonTaskRunner()(context.Background(), payload, nil)
	if err != nil {
		t.Fatalf("daemon task runner: %v", err)
	}

	sess, err := rt.mgr.LoadSession(result.SessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if sess.Principal != principal {
		t.Fatalf("session principal = %+v, want %+v", sess.Principal, principal)
	}
}

func TestDaemonTaskRunnerFollowUpRecordsResumeEvent(t *testing.T) {
	provider := &countingProvider{streamText: "follow-up answer"}
	rt := newTestExecutionRuntime(t, provider)
	createdBy := identity.Principal{UserID: "77", ProjectID: "elnath", Surface: "telegram"}
	resumedBy := identity.Principal{UserID: "77", ProjectID: "elnath", Surface: "telegram"}

	sess, err := rt.mgr.NewSessionWithPrincipal(createdBy)
	if err != nil {
		t.Fatalf("NewSessionWithPrincipal: %v", err)
	}
	_, _, err = rt.runTask(context.Background(), sess, nil, "initial request", orchestrationOutput{})
	if err != nil {
		t.Fatalf("seed runTask: %v", err)
	}

	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:    "continue from telegram",
		SessionID: sess.ID,
		Surface:   "telegram",
		Principal: resumedBy,
	})
	if _, err := rt.newDaemonTaskRunner()(context.Background(), payload, nil); err != nil {
		t.Fatalf("daemon task runner follow-up: %v", err)
	}

	resumes, err := agent.LoadSessionResumeEvents(rt.app.Config.DataDir, sess.ID)
	if err != nil {
		t.Fatalf("LoadSessionResumeEvents: %v", err)
	}
	if len(resumes) != 1 {
		t.Fatalf("resume count = %d, want 1", len(resumes))
	}
	if resumes[0].Principal != resumedBy {
		t.Fatalf("resume principal = %+v, want %+v", resumes[0].Principal, resumedBy)
	}
}

func TestDaemonTaskRunnerCreatesTelegramSessionResumableFromCLI(t *testing.T) {
	provider := &countingProvider{streamText: "daemon answer"}
	rt := newTestExecutionRuntime(t, provider)
	t.Setenv("USER", "stello")
	telegramPrincipal := identity.ResolveTelegramPrincipal(77, rt.workDir)

	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:    "tell me a joke",
		Surface:   "telegram",
		Principal: telegramPrincipal,
	})
	result, err := rt.newDaemonTaskRunner()(context.Background(), payload, nil)
	if err != nil {
		t.Fatalf("daemon task runner: %v", err)
	}

	latest, err := rt.mgr.LoadLatestSession(identity.ResolveCLIPrincipal(nil, "", rt.workDir))
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if latest.ID != result.SessionID {
		t.Fatalf("latest session = %q, want %q", latest.ID, result.SessionID)
	}
}

func TestDaemonTaskRunnerRejectsFollowUpForDifferentPrincipal(t *testing.T) {
	provider := &countingProvider{streamText: "follow-up answer"}
	rt := newTestExecutionRuntime(t, provider)

	sess, err := rt.mgr.NewSessionWithPrincipal(identity.Principal{UserID: "owner", ProjectID: "elnath", Surface: "telegram"})
	if err != nil {
		t.Fatalf("NewSessionWithPrincipal: %v", err)
	}
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:    "steal this session",
		SessionID: sess.ID,
		Surface:   "telegram",
		Principal: identity.Principal{UserID: "intruder", ProjectID: "elnath", Surface: "telegram"},
	})

	_, err = rt.newDaemonTaskRunner()(context.Background(), payload, nil)
	if err == nil {
		t.Fatal("daemon task runner different-principal follow-up error = nil, want error")
	}
}

func TestInteractiveSessionIngestEventIncludesResumeHistory(t *testing.T) {
	provider := &countingProvider{}
	rt := newTestExecutionRuntime(t, provider)
	principal := identity.Principal{UserID: "12345", ProjectID: "elnath", Surface: "telegram"}

	sess, err := rt.mgr.NewSessionWithPrincipal(principal)
	if err != nil {
		t.Fatalf("NewSessionWithPrincipal: %v", err)
	}
	if err := sess.AppendMessage(llm.NewUserMessage("hello from telegram")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := sess.RecordResume(identity.Principal{UserID: "stello@host", ProjectID: "elnath", Surface: "cli"}); err != nil {
		t.Fatalf("RecordResume: %v", err)
	}

	event, err := interactiveSessionIngestEvent(rt.app.Config.DataDir, sess, sess.Messages)
	if err != nil {
		t.Fatalf("interactiveSessionIngestEvent: %v", err)
	}
	if len(event.Resumes) != 1 {
		t.Fatalf("resume count = %d, want 1", len(event.Resumes))
	}
	if event.Resumes[0].Principal != "cli:stello@host" {
		t.Fatalf("resume principal = %q, want cli:stello@host", event.Resumes[0].Principal)
	}
}

func TestExecutionRuntimeMaybeAutoDocumentSessionIngestsStructuredPage(t *testing.T) {
	provider := &countingProvider{}
	rt := newTestExecutionRuntime(t, provider)

	rt.maybeAutoDocumentSession(context.Background(), wiki.IngestEvent{
		SessionID: "sess-cli",
		Messages: []llm.Message{
			llm.NewUserMessage("hello runtime ingest"),
			llm.NewAssistantMessage("structured wiki page"),
		},
		Reason:    "interactive_session",
		Principal: "cli:stello",
	})

	page, err := rt.wikiStore.Read("sessions/sess-cli.md")
	if err != nil {
		t.Fatalf("Read session page: %v", err)
	}
	if !strings.Contains(page.Content, "## Session Metadata") {
		t.Fatalf("expected metadata section, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "**Principal**: cli:stello") {
		t.Fatalf("expected principal metadata, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "wiki summary") {
		t.Fatalf("expected provider summary, got:\n%s", page.Content)
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

func TestExecutionRuntimeKeepsAuditTrailAvailableAcrossRuns(t *testing.T) {
	provider := &countingProvider{streamText: "hello from runtime"}
	rt := newTestExecutionRuntime(t, provider)
	if rt.auditTrail == nil {
		t.Fatal("auditTrail = nil, want initialized trail")
	}
	if rt.wfCfg.Hooks == nil {
		t.Fatal("Hooks = nil, want secret hook registry")
	}

	result := &tools.Result{Output: "token=sk-ant-api03-" + strings.Repeat("a", 80)}
	if err := rt.wfCfg.Hooks.RunPostToolUse(context.Background(), "bash", nil, result); err != nil {
		t.Fatalf("RunPostToolUse() error = %v", err)
	}
	if !strings.Contains(result.Output, "[REDACTED:anthropic-api-key]") {
		t.Fatalf("Output = %q, want redacted token", result.Output)
	}

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, _, err := rt.runTask(context.Background(), sess, nil, "what changed in Stella?", orchestrationOutput{}); err != nil {
		t.Fatalf("runTask: %v", err)
	}

	if err := rt.auditTrail.Log(audit.Event{Type: audit.EventSecretDetected}); err != nil {
		t.Fatalf("auditTrail.Log() after runTask error = %v", err)
	}

	auditPath := filepath.Join(rt.app.Config.DataDir, "audit.jsonl")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("ReadFile audit: %v", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Fatal("audit file is empty, want logged event")
	}
}

func TestExecutionRuntimeRegistersSecretHookWhenAuditTrailUnavailable(t *testing.T) {
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
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Fatalf("app.Close: %v", err)
		}
	})

	badDataDir := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(badDataDir, []byte("blocking audit dir"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg.DataDir = badDataDir

	perm := agent.NewPermission(agent.WithMode(agent.ModeBypass))
	rt, err := buildExecutionRuntime(
		context.Background(),
		cfg,
		app,
		db,
		&countingProvider{},
		"mock-model",
		self.New(cfg.DataDir),
		"",
		perm,
		root,
		nil,
		identity.LegacyPrincipal(),
		false,
	)
	if err != nil {
		t.Fatalf("buildExecutionRuntime: %v", err)
	}
	if rt.auditTrail != nil {
		t.Fatal("auditTrail != nil, want nil when audit trail initialization fails")
	}
	if rt.wfCfg.Hooks == nil {
		t.Fatal("Hooks = nil, want secret hook registry")
	}

	result := &tools.Result{Output: "token=sk-ant-api03-" + strings.Repeat("a", 80)}
	if err := rt.wfCfg.Hooks.RunPostToolUse(context.Background(), "bash", nil, result); err != nil {
		t.Fatalf("RunPostToolUse() error = %v", err)
	}
	if !strings.Contains(result.Output, "[REDACTED:anthropic-api-key]") {
		t.Fatalf("Output = %q, want redacted token", result.Output)
	}
}

func TestExecutionRuntimeBuildsPerRequestSystemPrompt(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	if err := os.WriteFile(filepath.Join(rt.workDir, "CLAUDE.md"), []byte("project instructions from CLAUDE"), 0o644); err != nil {
		t.Fatalf("WriteFile CLAUDE.md: %v", err)
	}
	seedRuntimeSessionPage(t, rt.wikiIdx, "sessions/sess-memory.md", "Session sess-memory", "## Summary\n\nResumed work on the prompt graph.", []string{"session", "interactive_session"})
	if err := os.WriteFile(filepath.Join(rt.workDir, "data", "lessons.jsonl"), []byte(`{"id":"l1","text":"Prefer focused experiments.","source":"go patterns","confidence":"high","created":"2026-04-13T00:00:00Z"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile lessons.jsonl: %v", err)
	}

	for _, path := range []string{
		"internal/middleware/request_id.go",
		"pkg/transport/context.go",
	} {
		full := filepath.Join(rt.workDir, path)
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

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, _, err = rt.runTask(context.Background(), sess, nil, "fix regression in existing handler and add tests for request id middleware", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}

	checks := []string{
		"You are Elnath.",
		"<<context_files>>",
		"project instructions from CLAUDE",
		"Operational state:",
		"- Mode: interactive",
		"- Messages in conversation: 1",
		"You have access to tools",
		"__DYNAMIC_BOUNDARY__",
		"<<memory_context>>",
		"[Session sess-memory]",
		"Recent lessons:",
		"Prefer focused experiments.",
		"# Execution Discipline",
		"Project context:",
		"internal/middleware/request_id.go",
		"Report outcomes faithfully",
	}
	for _, want := range checks {
		if !strings.Contains(provider.lastSystem, want) {
			t.Fatalf("system prompt missing %q\n%s", want, provider.lastSystem)
		}
	}
	if got := strings.Count(provider.lastSystem, "__DYNAMIC_BOUNDARY__"); got != 1 {
		t.Fatalf("boundary count = %d, want 1\n%s", got, provider.lastSystem)
	}
}

func TestExecutionRuntimeBuildsDaemonModeSystemPrompt(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntimeWithMode(t, provider, true)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, _, err = rt.runTask(context.Background(), sess, nil, "summarize the daemon status", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if !strings.Contains(provider.lastSystem, "- Mode: daemon") {
		t.Fatalf("system prompt missing daemon mode\n%s", provider.lastSystem)
	}
}

func TestExecutionRuntimeAddsLocaleInstructionForKoreanInput(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, _, err = rt.runTask(context.Background(), sess, nil, "안녕, 오늘 날짜 알려줘", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if !strings.Contains(provider.lastSystem, "Respond in Korean.") {
		t.Fatalf("system prompt missing locale instruction\n%s", provider.lastSystem)
	}
}

func TestExecutionRuntimeInheritsLocaleForShortFollowUp(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, _, err := rt.runTask(context.Background(), sess, nil, "안녕, 지금 뭐해?", orchestrationOutput{})
	if err != nil {
		t.Fatalf("first runTask: %v", err)
	}
	_, _, err = rt.runTask(context.Background(), sess, messages, "네", orchestrationOutput{})
	if err != nil {
		t.Fatalf("second runTask: %v", err)
	}
	if !strings.Contains(provider.lastSystem, "Respond in Korean.") {
		t.Fatalf("system prompt missing inherited locale instruction\n%s", provider.lastSystem)
	}
}

func TestExecutionRuntimePromptSessionSummaryUsesPriorPreparedHistory(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, _, err := rt.runTask(context.Background(), sess, nil, "first user request", orchestrationOutput{})
	if err != nil {
		t.Fatalf("first runTask: %v", err)
	}

	_, _, err = rt.runTask(context.Background(), sess, messages, "second user request", orchestrationOutput{})
	if err != nil {
		t.Fatalf("second runTask: %v", err)
	}
	if !strings.Contains(provider.lastSystem, "Recent conversation:") {
		t.Fatalf("system prompt missing session summary\n%s", provider.lastSystem)
	}
	if !strings.Contains(provider.lastSystem, "first user request") {
		t.Fatalf("system prompt missing prior user message\n%s", provider.lastSystem)
	}
	if strings.Contains(provider.lastSystem, "second user request") {
		t.Fatalf("system prompt should not duplicate current user input\n%s", provider.lastSystem)
	}
}

func TestExecutionRuntimeRunTaskDoesNotDuplicateCurrentUserTurn(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, _, err := rt.runTask(context.Background(), sess, nil, "first user request", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
	if got := messages[0].Text(); got != "first user request" {
		t.Fatalf("messages[0] = %q, want original user request", got)
	}

	history, err := rt.mgr.GetHistory(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if got := history[0].Text(); got != "first user request" {
		t.Fatalf("history[0] = %q, want original user request", got)
	}
}

func TestExecutionRuntimeRunTaskDoesNotDuplicateCurrentUserTurn_Autopilot(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	rt.router = orchestrator.NewRouter(map[string]orchestrator.Workflow{
		"single": orchestrator.NewAutopilotWorkflow(),
	})

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, _, err := rt.runTask(context.Background(), sess, nil, "build a new feature", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if got := countExactUserTurns(messages, "build a new feature"); got != 1 {
		t.Fatalf("exact user turn count = %d, want 1", got)
	}
}

func TestExecutionRuntimeRunTaskDoesNotDuplicateCurrentUserTurn_Ralph(t *testing.T) {
	provider := &sequenceStreamProvider{responses: []string{"runtime answer", "PASS"}}
	rt := newTestExecutionRuntime(t, provider)
	rt.router = orchestrator.NewRouter(map[string]orchestrator.Workflow{
		"single": orchestrator.NewRalphWorkflow(),
	})

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, _, err := rt.runTask(context.Background(), sess, nil, "fix regression and add tests", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if got := countExactUserTurns(messages, "fix regression and add tests"); got != 1 {
		t.Fatalf("exact user turn count = %d, want 1", got)
	}
}

func TestExecutionRuntimeRunTaskPersistsVerifierRunWithAgenticContext(t *testing.T) {
	ctx := context.Background()
	provider := &sequenceStreamProvider{responses: []string{"runtime answer", "PASS"}}
	rt := newTestExecutionRuntime(t, provider)
	rt.router = orchestrator.NewRouter(map[string]orchestrator.Workflow{
		"single": orchestrator.NewRalphWorkflow(),
	})

	task, err := rt.agenticStore.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "Runtime verifier persistence",
		Prompt:             "fix regression and add tests",
		Status:             agentic.TaskStatusRunning,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	ctx = daemon.WithAgenticTaskID(ctx, task.ID)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, _, err := rt.runTask(ctx, sess, nil, "fix regression and add tests", orchestrationOutput{}); err != nil {
		t.Fatalf("runTask: %v", err)
	}

	runs, err := rt.agenticStore.ListVerificationRunsByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListVerificationRunsByTask: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("verification runs = %d, want 1", len(runs))
	}
	if runs[0].Verdict != agentic.VerificationVerdictPassed {
		t.Fatalf("verdict = %q, want %q", runs[0].Verdict, agentic.VerificationVerdictPassed)
	}
	if strings.Contains(runs[0].EvidenceRefsJSON, "runtime answer") {
		t.Fatalf("evidence refs should not persist raw output: %s", runs[0].EvidenceRefsJSON)
	}
}

func TestDaemonBackedRalphPersistsVerifierRunAndMarksDone(t *testing.T) {
	ctx := context.Background()
	provider := &sequenceStreamProvider{responses: []string{"runtime answer", "PASS"}}
	rt := newTestExecutionRuntime(t, provider)
	rt.router = orchestrator.NewRouter(map[string]orchestrator.Workflow{
		"single": orchestrator.NewRalphWorkflow(),
	})

	queue, err := daemon.NewQueue(rt.db.Main)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{Prompt: "fix regression and add tests"})
	queueTaskID, _, err := queue.Enqueue(ctx, payload, "pr8-e2e-success")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	stopDaemon := startRuntimeDaemonForTest(t, daemon.New(queue, testSocketPath(t, "pr8-e2e"), 1, rt.newDaemonTaskRunner(), rt.app.Logger), rt.agenticStore)
	defer stopDaemon()

	task := pollRuntimeQueueStatus(t, queue, queueTaskID, daemon.StatusDone)
	if !strings.Contains(task.Result, "runtime answer") {
		t.Fatalf("task result = %q, want runtime answer", task.Result)
	}
	agenticTask, err := rt.agenticStore.GetAgenticTaskByQueueTaskID(ctx, queueTaskID)
	if err != nil {
		t.Fatalf("GetAgenticTaskByQueueTaskID: %v", err)
	}
	runs, err := rt.agenticStore.ListVerificationRunsByTask(ctx, agenticTask.ID)
	if err != nil {
		t.Fatalf("ListVerificationRunsByTask: %v", err)
	}
	if len(runs) != 1 || runs[0].Verdict != agentic.VerificationVerdictPassed {
		t.Fatalf("verification runs = %+v, want one passed run", runs)
	}
}

func TestDaemonBackedRalphRecorderFailureStillMarksDone(t *testing.T) {
	ctx := context.Background()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	oldDefaultLogger := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(oldDefaultLogger) })

	provider := &sequenceStreamProvider{responses: []string{"runtime answer", "PASS"}}
	rt := newTestExecutionRuntime(t, provider)
	rt.router = orchestrator.NewRouter(map[string]orchestrator.Workflow{
		"single": orchestrator.NewRalphWorkflow(),
	})
	rt.app.Logger = logger

	queue, err := daemon.NewQueue(rt.db.Main)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{Prompt: "fix regression and add tests"})
	queueTaskID, _, err := queue.Enqueue(ctx, payload, "pr8-e2e-recorder-failure")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	envelopeStore := rt.agenticStore
	badDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open bad db: %v", err)
	}
	t.Cleanup(func() { _ = badDB.Close() })
	rt.agenticStore = agentic.NewStore(badDB)

	stopDaemon := startRuntimeDaemonForTest(t, daemon.New(queue, testSocketPath(t, "pr8-e2e-fail"), 1, rt.newDaemonTaskRunner(), rt.app.Logger), envelopeStore)
	defer stopDaemon()

	task := pollRuntimeQueueStatus(t, queue, queueTaskID, daemon.StatusDone)
	if !strings.Contains(task.Result, "runtime answer") {
		t.Fatalf("task result = %q, want runtime answer", task.Result)
	}
	for _, want := range []string{"verification run persistence failed", "degraded_observability=true"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("logs missing %q:\n%s", want, logs.String())
		}
	}
}

func TestParseSkillArgs(t *testing.T) {
	t.Parallel()

	got := parseSkillArgs("/pr-review <pr_number>", []string{"42"})
	if len(got) != 1 || got["pr_number"] != "42" {
		t.Fatalf("parseSkillArgs() = %#v, want pr_number=42", got)
	}
	got = parseSkillArgs("/search <query>", []string{"hello", "world"})
	if len(got) != 1 || got["query"] != "hello world" {
		t.Fatalf("parseSkillArgs() with multi-word arg = %#v, want query=hello world", got)
	}
	got = parseSkillArgs("/rename <from> <to>", []string{"old", "new", "name"})
	if len(got) != 2 || got["from"] != "old" || got["to"] != "new name" {
		t.Fatalf("parseSkillArgs() with trailing words = %#v, want from=old to='new name'", got)
	}
	got = parseSkillArgs("/pr-review <pr_number>", nil)
	if len(got) != 1 || got["pr_number"] != "" {
		t.Fatalf("parseSkillArgs() with missing arg = %#v, want pr_number empty", got)
	}
}

func startRuntimeDaemonForTest(t *testing.T, d *daemon.Daemon, store *agentic.Store) func() {
	t.Helper()
	d.WithTaskEnvelope(agenticruntime.NewDaemonEnvelope(store))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- d.Start(ctx)
	}()

	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("daemon Start: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("daemon did not stop within timeout")
		}
	}
}

func pollRuntimeQueueStatus(t *testing.T, q *daemon.Queue, taskID int64, want daemon.TaskStatus) *daemon.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := q.Get(context.Background(), taskID)
		if err != nil {
			t.Fatalf("queue Get: %v", err)
		}
		if task.Status == want {
			return task
		}
		if task.Status == daemon.StatusFailed {
			t.Fatalf("task failed while waiting for %s: %+v", want, task)
		}
		time.Sleep(25 * time.Millisecond)
	}
	task, err := q.Get(context.Background(), taskID)
	if err != nil {
		t.Fatalf("queue Get after timeout: %v", err)
	}
	t.Fatalf("task status = %s, want %s: %+v", task.Status, want, task)
	return nil
}

func TestExecutionRuntimeRunTaskExecutesSkillSlashCommand(t *testing.T) {
	provider := &countingProvider{streamText: "skill output"}
	rt := newTestExecutionRuntime(t, provider)
	rt.skillReg = skill.NewRegistry()
	rt.skillReg.Add(&skill.Skill{
		Name:    "pr-review",
		Trigger: "/pr-review <pr_number>",
		Prompt:  "Review PR #{pr_number}",
	})

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var streamed strings.Builder
	var gotUsage string
	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/pr-review 42", orchestrationOutput{
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
	if summary != "skill output" {
		t.Fatalf("summary = %q, want %q", summary, "skill output")
	}
	if provider.chatCalls != 0 {
		t.Fatalf("chatCalls = %d, want 0 for direct skill execution", provider.chatCalls)
	}
	if provider.streamCalls != 1 {
		t.Fatalf("streamCalls = %d, want 1", provider.streamCalls)
	}
	if !strings.HasSuffix(provider.lastSystem, "Review PR #42") {
		t.Fatalf("system prompt = %q, want suffix %q (skill body last section)", provider.lastSystem, "Review PR #42")
	}
	if !strings.Contains(provider.lastSystem, "You are Elnath.") {
		t.Fatalf("system prompt missing pipeline identity prefix: %q", provider.lastSystem)
	}
	if !strings.Contains(streamed.String(), "Executing skill: pr-review\n") {
		t.Fatalf("streamed = %q, want execution banner", streamed.String())
	}
	if !strings.Contains(streamed.String(), "skill output") {
		t.Fatalf("streamed = %q, want skill output", streamed.String())
	}
	if gotUsage == "" {
		t.Fatal("expected usage summary callback for skill execution")
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
	if got := messages[0].Text(); got != "/pr-review 42" {
		t.Fatalf("first message = %q, want %q", got, "/pr-review 42")
	}
	if got := messages[1].Text(); got != "skill output" {
		t.Fatalf("assistant output = %q, want %q", got, "skill output")
	}
	reloaded, err := rt.mgr.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(reloaded.Messages) != 2 {
		t.Fatalf("reloaded messages len = %d, want 2", len(reloaded.Messages))
	}
	if got := reloaded.Messages[0].Text(); got != "/pr-review 42" {
		t.Fatalf("reloaded first message = %q, want %q", got, "/pr-review 42")
	}
	if got := reloaded.Messages[1].Text(); got != "skill output" {
		t.Fatalf("reloaded assistant output = %q, want %q", got, "skill output")
	}
}

func TestExecutionRuntimeRunTaskEffortSlashCommandSwitchesToAuto(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.Reasoning.EffortMode = "manual"
		cfg.Reasoning.Effort = "high"
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var streamed strings.Builder
	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/effort auto", orchestrationOutput{
		OnText: func(s string) { streamed.WriteString(s) },
	})
	if err != nil {
		t.Fatalf("runTask /effort auto: %v", err)
	}
	if provider.streamCalls != 0 {
		t.Fatalf("streamCalls = %d, want 0 for local effort command", provider.streamCalls)
	}
	if rt.wfCfg.ReasoningEffortMode != "auto" || rt.wfCfg.ReasoningEffort != "" {
		t.Fatalf("reasoning config = mode %q effort %q, want auto/empty", rt.wfCfg.ReasoningEffortMode, rt.wfCfg.ReasoningEffort)
	}
	if !strings.Contains(summary, "auto") || !strings.Contains(streamed.String(), "auto") {
		t.Fatalf("summary=%q streamed=%q, want auto message", summary, streamed.String())
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}

	_, _, err = rt.runTask(context.Background(), sess, messages, "quick status summary", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask status: %v", err)
	}
	if provider.lastReasoningEffort != "low" {
		t.Fatalf("ReasoningEffort = %q, want low", provider.lastReasoningEffort)
	}
}

func TestExecutionRuntimeRunTaskEffortSlashCommandPinsManualEffort(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.Reasoning.EffortMode = "auto"
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/effort max", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /effort max: %v", err)
	}
	if provider.streamCalls != 0 {
		t.Fatalf("streamCalls = %d, want 0 for local effort command", provider.streamCalls)
	}
	if rt.wfCfg.ReasoningEffortMode != "manual" || rt.wfCfg.ReasoningEffort != "xhigh" {
		t.Fatalf("reasoning config = mode %q effort %q, want manual/xhigh", rt.wfCfg.ReasoningEffortMode, rt.wfCfg.ReasoningEffort)
	}
	if !strings.Contains(summary, "xhigh") {
		t.Fatalf("summary = %q, want xhigh", summary)
	}

	_, _, err = rt.runTask(context.Background(), sess, messages, "quick status summary", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask status: %v", err)
	}
	if provider.lastReasoningEffort != "xhigh" {
		t.Fatalf("ReasoningEffort = %q, want xhigh", provider.lastReasoningEffort)
	}
}

func TestExecutionRuntimeRunTaskModelSlashCommandPinsModel(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var streamed strings.Builder
	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/model kimi-k2", orchestrationOutput{
		OnText: func(s string) { streamed.WriteString(s) },
	})
	if err != nil {
		t.Fatalf("runTask /model: %v", err)
	}
	if provider.streamCalls != 0 {
		t.Fatalf("streamCalls = %d, want 0 for local model command", provider.streamCalls)
	}
	if rt.wfCfg.Model != "kimi-k2" {
		t.Fatalf("runtime model = %q, want kimi-k2", rt.wfCfg.Model)
	}
	if !strings.Contains(summary, "kimi-k2") || !strings.Contains(streamed.String(), "kimi-k2") {
		t.Fatalf("summary=%q streamed=%q, want model message", summary, streamed.String())
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}

	_, _, err = rt.runTask(context.Background(), sess, messages, "quick status summary", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask status: %v", err)
	}
	if provider.lastModel != "kimi-k2" {
		t.Fatalf("Model = %q, want kimi-k2", provider.lastModel)
	}
}

func TestExecutionRuntimeRunTaskModelSlashCommandCanUseProviderDefault(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	rt.wfCfg.Model = "kimi-k2"
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/model default", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /model default: %v", err)
	}
	if rt.wfCfg.Model != "" {
		t.Fatalf("runtime model = %q, want provider default", rt.wfCfg.Model)
	}
	if !strings.Contains(summary, "provider default") {
		t.Fatalf("summary = %q, want provider default message", summary)
	}

	_, _, err = rt.runTask(context.Background(), sess, messages, "quick status summary", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask status: %v", err)
	}
	if provider.lastModel != "" {
		t.Fatalf("Model = %q, want empty provider default request model", provider.lastModel)
	}
}

func TestExecutionRuntimeRunTaskProviderSlashCommandReportsCapabilities(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntime(t, provider)
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var streamed strings.Builder
	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/provider status", orchestrationOutput{
		OnText: func(s string) { streamed.WriteString(s) },
	})
	if err != nil {
		t.Fatalf("runTask /provider status: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local provider command", provider.chatCalls, provider.streamCalls)
	}
	for _, want := range []string{"openai-responses", llm.ReasoningEffortNativeWithUnsupportedRetry, "retry_without_reasoning"} {
		if !strings.Contains(summary, want) || !strings.Contains(streamed.String(), want) {
			t.Fatalf("summary=%q streamed=%q missing %q", summary, streamed.String(), want)
		}
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
}

func TestExecutionRuntimeRunTaskProviderSlashCommandRejectsRuntimeSwitch(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntime(t, provider)
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/provider anthropic", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider anthropic: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local provider command", provider.chatCalls, provider.streamCalls)
	}
	if !strings.Contains(summary, "Runtime provider switching is not available") {
		t.Fatalf("summary = %q, want runtime-switch boundary", summary)
	}
}

func TestExecutionRuntimeRunTaskSelfHealingCorrectionRetriesIncompleteFinal(t *testing.T) {
	provider := &sequenceStreamProvider{responses: []string{
		"I could not finish the patch.",
		"Done now.",
	}}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "fix the existing handler", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if summary != "Done now." {
		t.Fatalf("summary = %q, want retry result", summary)
	}
	if provider.idx != 2 {
		t.Fatalf("streamed responses = %d, want 2 correction attempts", provider.idx)
	}
	records, err := rt.outcomeStore.Recent(1)
	if err != nil {
		t.Fatalf("Recent outcomes: %v", err)
	}
	if len(records) != 1 || !records[0].CorrectionAttempted || records[0].CorrectionAttempts != 1 {
		t.Fatalf("correction outcome = %+v, want one recorded correction attempt", records)
	}
	if records[0].CorrectionDecision != completionRetryDecisionRetrySmallerScope || records[0].CorrectionReason != "final_response_reports_incomplete" {
		t.Fatalf("correction outcome reason = decision %q reason %q", records[0].CorrectionDecision, records[0].CorrectionReason)
	}
}

func TestExecutionRuntimeRunTaskSelfHealingObserveOnlyDoesNotRetryIncompleteFinal(t *testing.T) {
	provider := &sequenceStreamProvider{responses: []string{
		"I could not finish the patch.",
		"Done now.",
	}}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = true
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "fix the existing handler", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if summary != "I could not finish the patch." {
		t.Fatalf("summary = %q, want first attempt result", summary)
	}
	if provider.idx != 1 {
		t.Fatalf("streamed responses = %d, want no correction retry in observe-only mode", provider.idx)
	}
}

func TestCompletionRetryRunsExplicitVerificationCommand(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 1
	reg := tools.NewRegistry()
	bash := &recordingRuntimeTool{name: "bash", output: "PASS"}
	reg.Register(bash)
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("fix the existing handler\n\ngo test ./cmd/elnath -count=1"),
			llm.NewAssistantMessage("Done."),
		},
		Summary:      "Done.",
		FinishReason: "stop",
		Workflow:     "single",
	}
	observed := false
	summary := completionContractSummary{
		VerificationHint:     true,
		VerificationObserved: &observed,
		RetryDecision:        completionRetryDecisionRunVerification,
		RetryReason:          "verification_hint_not_observed",
	}

	gotResult, gotSummary := rt.maybeRunCompletionRetry(context.Background(), &stubWorkflow{name: "single"}, orchestrator.WorkflowInput{
		Session:  &agent.Session{ID: "verify-session"},
		Tools:    reg,
		Provider: rt.provider,
	}, result, summary)

	if gotResult != result {
		t.Fatal("verification retry should preserve original workflow result")
	}
	if bash.calls != 1 || bash.command != "go test ./cmd/elnath -count=1" {
		t.Fatalf("bash calls = %d command = %q, want explicit verification command", bash.calls, bash.command)
	}
	if gotSummary.VerificationObserved == nil || !*gotSummary.VerificationObserved || gotSummary.VerificationCommand != "go test ./cmd/elnath -count=1" {
		t.Fatalf("verification summary = observed %v command %q", gotSummary.VerificationObserved, gotSummary.VerificationCommand)
	}
	if !gotSummary.CorrectionAttempted || gotSummary.CorrectionAttempts != 1 || gotSummary.CorrectionDecision != completionRetryDecisionRunVerification {
		t.Fatalf("correction summary = %+v", gotSummary)
	}
}

func TestCompletionRetryDoesNotInferVerificationFromProse(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 1
	reg := tools.NewRegistry()
	bash := &recordingRuntimeTool{name: "bash", output: "PASS"}
	reg.Register(bash)
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("fix the existing handler and run go test ./cmd/elnath -count=1"),
			llm.NewAssistantMessage("Done."),
		},
		Summary:      "Done.",
		FinishReason: "stop",
		Workflow:     "single",
	}
	observed := false
	summary := completionContractSummary{
		VerificationHint:     true,
		VerificationObserved: &observed,
		RetryDecision:        completionRetryDecisionRunVerification,
		RetryReason:          "verification_hint_not_observed",
	}

	_, gotSummary := rt.maybeRunCompletionRetry(context.Background(), &stubWorkflow{name: "single"}, orchestrator.WorkflowInput{
		Session:  &agent.Session{ID: "verify-session"},
		Tools:    reg,
		Provider: rt.provider,
	}, result, summary)

	if bash.calls != 0 {
		t.Fatalf("bash calls = %d, want no inferred verification execution", bash.calls)
	}
	if gotSummary.CorrectionAttempted {
		t.Fatalf("correction attempted from prose-only command: %+v", gotSummary)
	}
}

func TestExecutionRuntimeRunTaskPersistsFullSkillTranscript(t *testing.T) {
	provider := &scriptedSkillProvider{}
	rt := newTestExecutionRuntime(t, provider)
	rt.reg.Register(&runtimeMockTool{name: "mock_tool", output: "tool output"})
	rt.skillReg = skill.NewRegistry()
	rt.skillReg.Add(&skill.Skill{
		Name:          "search",
		Trigger:       "/search <query>",
		Prompt:        "Search {query}",
		RequiredTools: []string{"mock_tool"},
	})

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/search hello world", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if summary != "skill output" {
		t.Fatalf("summary = %q, want %q", summary, "skill output")
	}
	if !strings.HasSuffix(provider.lastSystem, "Search hello world") {
		t.Fatalf("system prompt = %q, want suffix %q (skill body last section)", provider.lastSystem, "Search hello world")
	}
	if !strings.Contains(provider.lastSystem, "You are Elnath.") {
		t.Fatalf("system prompt missing pipeline identity prefix: %q", provider.lastSystem)
	}
	if len(messages) != 4 {
		t.Fatalf("messages len = %d, want 4", len(messages))
	}
	if got := messages[0].Text(); got != "/search hello world" {
		t.Fatalf("first message = %q, want %q", got, "/search hello world")
	}
	if !containsToolResult(messages) {
		t.Fatal("messages missing tool result transcript")
	}
	if got := messages[len(messages)-1].Text(); got != "skill output" {
		t.Fatalf("last message = %q, want %q", got, "skill output")
	}

	reloaded, err := rt.mgr.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(reloaded.Messages) != 4 {
		t.Fatalf("reloaded messages len = %d, want 4", len(reloaded.Messages))
	}
	if !containsToolResult(reloaded.Messages) {
		t.Fatal("reloaded messages missing tool result transcript")
	}
}

func TestExecutionRuntimeRunTaskExecutesPrefixedSkillSlashCommand(t *testing.T) {
	provider := &countingProvider{streamText: "skill output"}
	rt := newTestExecutionRuntime(t, provider)
	rt.skillReg = skill.NewRegistry()
	rt.skillReg.Add(&skill.Skill{
		Name:    "pr-review",
		Trigger: "/pr-review <pr_number>",
		Prompt:  "Review PR #{pr_number}",
	})

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "[Skill: pr-review] /pr-review 42", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if summary != "skill output" {
		t.Fatalf("summary = %q, want %q", summary, "skill output")
	}
	if provider.streamCalls != 1 {
		t.Fatalf("streamCalls = %d, want 1", provider.streamCalls)
	}
	if !strings.HasSuffix(provider.lastSystem, "Review PR #42") {
		t.Fatalf("system prompt = %q, want suffix %q (skill body last section)", provider.lastSystem, "Review PR #42")
	}
	if !strings.Contains(provider.lastSystem, "You are Elnath.") {
		t.Fatalf("system prompt missing pipeline identity prefix: %q", provider.lastSystem)
	}
	stats, err := rt.skillTracker.UsageStats()
	if err != nil {
		t.Fatalf("UsageStats() error = %v", err)
	}
	if got := stats["pr-review"]; got != 1 {
		t.Fatalf("UsageStats()[%q] = %d, want 1", "pr-review", got)
	}
}

func TestDaemonTaskRunnerHandlesSkillPromoteType(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Type:   daemon.TaskTypeSkillPromote,
		Prompt: "promote queued drafts",
	})

	result, err := rt.newDaemonTaskRunner()(context.Background(), payload, nil)
	if err != nil {
		t.Fatalf("daemon task runner skill-promote error = %v, want nil", err)
	}
	if !strings.Contains(result.Summary, "promoted") {
		t.Fatalf("summary = %q, want promoted summary", result.Summary)
	}
}

func containsToolResult(messages []llm.Message) bool {
	for _, msg := range messages {
		for _, block := range msg.Content {
			if _, ok := block.(llm.ToolResultBlock); ok {
				return true
			}
		}
	}
	return false
}

func TestExecutionRuntimeBuildsSkillCatalogFromWiki(t *testing.T) {
	root := t.TempDir()
	wikiDir := filepath.Join(root, "wiki")
	if err := os.MkdirAll(filepath.Join(wikiDir, "skills"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	page := `---
title: "PR Review"
type: analysis
tags: [skill]
name: pr-review
description: "Review PR with security and quality focus"
trigger: "/pr-review <pr_number>"
required_tools: [bash, read_file]
---

Review PR #{pr_number}.`
	if err := os.WriteFile(filepath.Join(wikiDir, "skills", "pr-review.md"), []byte(page), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{
		DataDir:  filepath.Join(root, "data"),
		WikiDir:  wikiDir,
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
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Fatalf("app.Close: %v", err)
		}
	})

	provider := &countingProvider{streamText: "runtime answer"}
	rt, err := buildExecutionRuntime(
		context.Background(),
		cfg,
		app,
		db,
		provider,
		"mock-model",
		self.New(cfg.DataDir),
		"",
		agent.NewPermission(agent.WithMode(agent.ModeBypass)),
		root,
		nil,
		identity.LegacyPrincipal(),
		false,
	)
	if err != nil {
		t.Fatalf("buildExecutionRuntime: %v", err)
	}
	if rt.skillReg == nil {
		t.Fatal("skillReg = nil, want loaded registry")
	}
	if got := rt.skillReg.Names(); len(got) != 1 || got[0] != "pr-review" {
		t.Fatalf("skillReg names = %v, want [pr-review]", got)
	}

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, _, err = rt.runTask(context.Background(), sess, nil, "fix request id middleware", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	checks := []string{
		"Available skills (invoke via /name):",
		"/pr-review <pr_number> — Review PR with security and quality focus",
	}
	for _, want := range checks {
		if !strings.Contains(provider.lastSystem, want) {
			t.Fatalf("system prompt missing %q\n%s", want, provider.lastSystem)
		}
	}
}
