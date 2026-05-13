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
	"reflect"
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
	isError bool
	execErr error
	calls   int
	command string
}

type stubWorkflow struct{ name string }

type captureRetryWorkflow struct {
	name     string
	response string
	input    orchestrator.WorkflowInput
}

type failingRetryWorkflow struct {
	name string
	err  error
}

type scopeDriftRetryWorkflow struct {
	name string
	path string
}

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

func (w *captureRetryWorkflow) Name() string { return w.name }

func (w *captureRetryWorkflow) Run(_ context.Context, input orchestrator.WorkflowInput) (*orchestrator.WorkflowResult, error) {
	w.input = input
	response := w.response
	if response == "" {
		response = w.name + " workflow"
	}
	return &orchestrator.WorkflowResult{
		Messages: append(input.Messages, llm.NewAssistantMessage(response)),
		Summary:  response,
		Workflow: w.name,
	}, nil
}

func (w *failingRetryWorkflow) Name() string { return w.name }

func (w *failingRetryWorkflow) Run(context.Context, orchestrator.WorkflowInput) (*orchestrator.WorkflowResult, error) {
	if w.err != nil {
		return nil, w.err
	}
	return nil, fmt.Errorf("retry workflow failed")
}

func (w *scopeDriftRetryWorkflow) Name() string { return w.name }

func (w *scopeDriftRetryWorkflow) Run(_ context.Context, input orchestrator.WorkflowInput) (*orchestrator.WorkflowResult, error) {
	messages := append([]llm.Message{}, input.Messages...)
	messages = append(messages,
		llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
			llm.ToolUseBlock{ID: "edit-1", Name: "edit_file", Input: json.RawMessage(`{"file_path":"` + w.path + `","old_string":"old","new_string":"new"}`)},
		}},
		llm.NewToolResultMessage("edit-1", "ok", false),
		llm.NewAssistantMessage("Done."),
	)
	return &orchestrator.WorkflowResult{
		Messages: messages,
		Summary:  "Done.",
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
	if t.execErr != nil {
		return nil, t.execErr
	}
	if t.isError {
		return tools.ErrorResult(t.output), nil
	}
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func TestSkillInvocationArgsIncludeRawAndNamedArguments(t *testing.T) {
	t.Parallel()

	got := skillInvocationArgs(&skill.Skill{
		Trigger:       "/review-pr",
		ArgumentNames: []string{"pr_number", "base"},
	}, []string{"42", "release", "branch"})
	if got["ARGUMENTS"] != "42 release branch" || got["arguments"] != "42 release branch" {
		t.Fatalf("raw args = %#v, want ARGUMENTS and arguments", got)
	}
	if got["pr_number"] != "42" || got["base"] != "release branch" {
		t.Fatalf("named args = %#v, want pr_number=42 base='release branch'", got)
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

func TestExecutionRuntimeEffortStatusExplainsAutoRoutingPolicy(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.Reasoning.EffortMode = "auto"
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/effort status", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /effort status: %v", err)
	}
	if provider.streamCalls != 0 {
		t.Fatalf("streamCalls = %d, want 0 for local effort status command", provider.streamCalls)
	}
	for _, want := range []string{
		"Effort level: auto.",
		"Auto routing policy:",
		"simple/status/progress/summary -> low",
		"implementation/debug/benchmark/CI -> high",
		"root-cause/security/architecture/autonomous -> xhigh",
		"Auto routing is heuristic",
		"Skill metadata effort overrides auto for that skill",
		"Manual override: /effort <level>",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary = %q, want contains %q", summary, want)
		}
	}
}

func TestExecutionRuntimeEffortHelpListsAllAcceptedLevels(t *testing.T) {
	provider := &countingProvider{streamText: "unused"}
	rt := newTestExecutionRuntime(t, provider)

	got := rt.applyEffortCommand([]string{"help"})
	for _, want := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "auto", "status"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help = %q, want contains %q", got, want)
		}
	}
}

func TestExecutionRuntimeEffortStatusReportsProviderCapability(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.Reasoning.EffortMode = "auto"
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/effort status", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /effort status: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local effort status command", provider.chatCalls, provider.streamCalls)
	}
	for _, want := range []string{
		"Provider effort capability: native_with_unsupported_retry",
		"Provider effort note: retry_without_reasoning",
		"Auto effort compatible: true",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary = %q, want contains %q", summary, want)
		}
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

func TestExecutionRuntimeRunTaskProviderSlashCommandJSONReportsConfiguredCandidates(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.OpenAIResponses.APIKey = "sk-test"
		cfg.OpenAIResponses.Model = "kimi-k2"
		cfg.OpenAIResponses.BaseURL = "https://api.moonshot.ai/v1"
		cfg.OpenAIResponses.ReasoningEffort = "high"
		cfg.OpenAIResponses.Timeout = 120
		cfg.Anthropic.APIKey = "anthropic-test"
		cfg.Anthropic.Model = "claude-sonnet-4-6"
		cfg.Anthropic.Timeout = 90
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/provider status --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider status --json: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local provider command", provider.chatCalls, provider.streamCalls)
	}

	var out providerStatusView
	if err := json.Unmarshal([]byte(summary), &out); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, summary)
	}
	if out.Provider != "openai-responses" || out.ProviderEffort != llm.ReasoningEffortNativeWithUnsupportedRetry {
		t.Fatalf("provider status = %+v, want active provider capabilities", out)
	}
	if len(out.ConfiguredProviders) != 2 {
		t.Fatalf("configured providers = %+v, want openai-responses and anthropic", out.ConfiguredProviders)
	}
	var responses providerConfigCandidateView
	for _, candidate := range out.ConfiguredProviders {
		if candidate.Provider == "openai-responses" {
			responses = candidate
		}
		if strings.Contains(candidate.Model, "sk-test") || strings.Contains(candidate.BaseURL, "sk-test") {
			t.Fatalf("candidate leaked API key: %+v", candidate)
		}
	}
	if responses.Model != "kimi-k2" || responses.BaseURL != "https://api.moonshot.ai/v1" || responses.ReasoningEffort != "high" || responses.RequestTimeoutSeconds != 120 {
		t.Fatalf("openai-responses candidate = %+v, want configured Responses-compatible metadata", responses)
	}
	if out.RuntimeProviderSwitchAvailable {
		t.Fatalf("runtime provider switch available = true, want false while reflection is startup-bound")
	}
	for _, want := range []string{"restart_required", "reflection_provider_startup_bound"} {
		if !containsString(out.ProviderSwitchBoundaries, want) {
			t.Fatalf("provider switch boundaries = %+v, missing %q", out.ProviderSwitchBoundaries, want)
		}
	}
}

func TestExecutionRuntimeRunTaskProviderSlashCommandOmitsReflectionBoundaryWhenSelfHealingDisabled(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = false
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/provider status --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider status --json: %v", err)
	}
	var out providerStatusView
	if err := json.Unmarshal([]byte(summary), &out); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, summary)
	}
	if containsString(out.ProviderSwitchBoundaries, "reflection_provider_startup_bound") {
		t.Fatalf("provider switch boundaries = %+v, should omit reflection boundary when self-healing is disabled", out.ProviderSwitchBoundaries)
	}
	if !out.RuntimeProviderSwitchAvailable || len(out.ProviderSwitchBoundaries) != 0 {
		t.Fatalf("provider switch availability = %t boundaries = %+v, want available with no boundaries", out.RuntimeProviderSwitchAvailable, out.ProviderSwitchBoundaries)
	}
}

func TestExecutionRuntimeRunTaskProviderSlashCommandJSONUsesSessionEffortOverride(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.Reasoning.EffortMode = "auto"
		cfg.Reasoning.Effort = "medium"
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, _, err := rt.runTask(context.Background(), sess, nil, "/effort max", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /effort max: %v", err)
	}
	_, summary, err := rt.runTask(context.Background(), sess, messages, "/provider status --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider status --json: %v", err)
	}

	var out providerStatusView
	if err := json.Unmarshal([]byte(summary), &out); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, summary)
	}
	if out.ReasoningEffortMode != "manual" || out.ConfiguredEffort != "xhigh" {
		t.Fatalf("runtime reasoning = mode %q effort %q, want manual/xhigh", out.ReasoningEffortMode, out.ConfiguredEffort)
	}
}

func TestExecutionRuntimeRunTaskProviderSlashCommandListsCandidates(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.OpenAIResponses.APIKey = "sk-test"
		cfg.OpenAIResponses.Model = "minimax-m2.7"
		cfg.OpenAIResponses.BaseURL = "https://api.minimax.io/v1"
		cfg.OpenAIResponses.Timeout = 150
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/provider candidates", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider candidates: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local provider command", provider.chatCalls, provider.streamCalls)
	}
	for _, want := range []string{"Configured providers:", "openai-responses", "minimax-m2.7", "https://api.minimax.io/v1", "150s"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary=%q missing %q", summary, want)
		}
	}
	if strings.Contains(summary, "sk-test") {
		t.Fatalf("summary leaked API key: %q", summary)
	}
}

func TestExecutionRuntimeRunTaskProviderSlashCommandChecksCandidateWithoutSwitching(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.OpenAIResponses.APIKey = "sk-test"
		cfg.OpenAIResponses.Model = "kimi-k2"
		cfg.OpenAIResponses.BaseURL = "https://api.moonshot.ai/v1"
		cfg.Anthropic.APIKey = "anthropic-test"
		cfg.Anthropic.Model = "claude-sonnet-4-6"
		cfg.Anthropic.Timeout = 90
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/provider check anthropic --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider check: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local provider check", provider.chatCalls, provider.streamCalls)
	}

	var out providerSelectionCheckView
	if err := json.Unmarshal([]byte(summary), &out); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, summary)
	}
	if out.RequestedProvider != "anthropic" || out.Provider != "anthropic" {
		t.Fatalf("provider check = %+v, want requested/active anthropic candidate", out)
	}
	if out.Model != "claude-sonnet-4-6" || out.RequestTimeoutSeconds != 90 {
		t.Fatalf("provider check = %+v, want anthropic model and timeout", out)
	}
	if !out.WouldSwitch || !out.RuntimeProviderSwitchAvailable {
		t.Fatalf("provider check switch fields = would_switch:%t runtime_available:%t, want true/true", out.WouldSwitch, out.RuntimeProviderSwitchAvailable)
	}
	if rt.provider.Name() != "openai-responses" || rt.wfCfg.Model != "mock-model" {
		t.Fatalf("runtime provider/model changed to %s/%s, want openai-responses/mock-model", rt.provider.Name(), rt.wfCfg.Model)
	}
}

func TestExecutionRuntimeRunTaskProviderSlashCommandSwitchesProvider(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.OpenAIResponses.APIKey = "sk-test"
		cfg.OpenAIResponses.Model = "kimi-k2"
		cfg.Anthropic.APIKey = "anthropic-test"
		cfg.Anthropic.Model = "claude-sonnet-4-6"
		cfg.Anthropic.Timeout = 90
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/provider use anthropic --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider use: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local provider use", provider.chatCalls, provider.streamCalls)
	}

	var out providerSelectionCheckView
	if err := json.Unmarshal([]byte(summary), &out); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, summary)
	}
	if !out.Switched || out.PreviousProvider != "openai-responses" || out.Provider != "anthropic" {
		t.Fatalf("provider switch = %+v, want switched openai-responses->anthropic", out)
	}
	if out.Model != "claude-sonnet-4-6" || out.RequestTimeoutSeconds != 90 {
		t.Fatalf("provider switch = %+v, want anthropic model and timeout", out)
	}
	if rt.provider.Name() != "anthropic" || rt.wfCfg.Model != "claude-sonnet-4-6" {
		t.Fatalf("runtime provider/model = %s/%s, want anthropic/claude-sonnet-4-6", rt.provider.Name(), rt.wfCfg.Model)
	}
}

func TestExecutionRuntimeRunTaskProviderSlashCommandBlocksSwitchWhenReflectionBound(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.OpenAIResponses.APIKey = "sk-test"
		cfg.OpenAIResponses.Model = "kimi-k2"
		cfg.Anthropic.APIKey = "anthropic-test"
		cfg.Anthropic.Model = "claude-sonnet-4-6"
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/provider use anthropic", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider use: %v", err)
	}
	if !strings.Contains(summary, "restart required") || !strings.Contains(summary, "reflection provider") {
		t.Fatalf("summary = %q, want restart boundary", summary)
	}
	if rt.provider.Name() != "openai-responses" || rt.wfCfg.Model != "mock-model" {
		t.Fatalf("runtime provider/model changed to %s/%s, want unchanged", rt.provider.Name(), rt.wfCfg.Model)
	}
}

func TestExecutionRuntimeRunTaskProviderSlashCommandBlocksSwitchInDaemonMode(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntimeWithConfig(t, provider, true, func(cfg *config.Config) {
		cfg.OpenAIResponses.APIKey = "sk-test"
		cfg.OpenAIResponses.Model = "kimi-k2"
		cfg.Anthropic.APIKey = "anthropic-test"
		cfg.Anthropic.Model = "claude-sonnet-4-6"
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/provider use anthropic", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider use: %v", err)
	}
	if !strings.Contains(summary, "Daemon mode uses a shared runtime") {
		t.Fatalf("summary = %q, want daemon shared runtime boundary", summary)
	}
	if rt.provider.Name() != "openai-responses" || rt.wfCfg.Model != "mock-model" {
		t.Fatalf("runtime provider/model changed to %s/%s, want unchanged", rt.provider.Name(), rt.wfCfg.Model)
	}

	_, jsonSummary, err := rt.runTask(context.Background(), sess, nil, "/provider check anthropic --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider check: %v", err)
	}
	var out providerSelectionCheckView
	if err := json.Unmarshal([]byte(jsonSummary), &out); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, jsonSummary)
	}
	if out.RuntimeProviderSwitchAvailable || !containsString(out.ProviderSwitchBoundaries, providerSwitchBoundaryDaemonSharedRuntime) {
		t.Fatalf("provider check = %+v, want daemon switch boundary", out)
	}
}

func TestExecutionRuntimeRunTaskProviderSlashCommandExplainsSwitchBoundary(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/provider status", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider status: %v", err)
	}
	for _, want := range []string{"Provider switching: restart required", "reflection provider"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary=%q missing %q", summary, want)
		}
	}
	if strings.Contains(summary, "compression budget") {
		t.Fatalf("summary=%q should not report compression budget startup-bound after dynamic budget update", summary)
	}
}

func TestExecutionRuntimeRunTaskProviderSlashCommandRedactsCredentialBearingBaseURL(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.OpenAIResponses.APIKey = "sk-test"
		cfg.OpenAIResponses.Model = "kimi-k2"
		cfg.OpenAIResponses.BaseURL = "https://alice:wonder@api.moonshot.ai/v1?api_key=secret-value&region=us&token=tok-value"
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/provider candidates", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider candidates: %v", err)
	}
	for _, leaked := range []string{"alice", "wonder", "secret-value", "tok-value", "sk-test"} {
		if strings.Contains(summary, leaked) {
			t.Fatalf("summary leaked %q: %q", leaked, summary)
		}
	}
	for _, want := range []string{"api.moonshot.ai", "region=us", "api_key=REDACTED", "token=REDACTED"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary=%q missing %q", summary, want)
		}
	}

	_, jsonSummary, err := rt.runTask(context.Background(), sess, nil, "/provider status --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider status --json: %v", err)
	}
	for _, leaked := range []string{"alice", "wonder", "secret-value", "tok-value", "sk-test"} {
		if strings.Contains(jsonSummary, leaked) {
			t.Fatalf("json summary leaked %q: %q", leaked, jsonSummary)
		}
	}
}

func TestExecutionRuntimeRunTaskProviderSlashCommandRedactsUnparseableBaseURL(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.OpenAIResponses.APIKey = "sk-test"
		cfg.OpenAIResponses.Model = "kimi-k2"
		cfg.OpenAIResponses.BaseURL = "https://alice:%zz@api.moonshot.ai/v1?api_key=secret-value&token=tok-value"
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/provider candidates", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /provider candidates: %v", err)
	}
	for _, leaked := range []string{"alice", "%zz", "secret-value", "tok-value", "sk-test"} {
		if strings.Contains(summary, leaked) {
			t.Fatalf("summary leaked %q: %q", leaked, summary)
		}
	}
	if !strings.Contains(summary, "base_url=REDACTED_INVALID_URL") {
		t.Fatalf("summary = %q, want invalid URL placeholder", summary)
	}
}

func TestExecutionRuntimeRunTaskCommandsSlashCommandListsCatalog(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var streamed strings.Builder
	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/commands", orchestrationOutput{
		OnText: func(s string) { streamed.WriteString(s) },
	})
	if err != nil {
		t.Fatalf("runTask /commands: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local commands command", provider.chatCalls, provider.streamCalls)
	}
	for _, want := range []string{"commands", "run", "skill", "/effort", "/model", "/provider", "/help", "/skills", "/version"} {
		if !strings.Contains(summary, want) || !strings.Contains(streamed.String(), want) {
			t.Fatalf("summary=%q streamed=%q missing %q", summary, streamed.String(), want)
		}
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
}

func TestExecutionRuntimeRunTaskCommandsSlashCommandListsSkillBackedSlashCommands(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	rt.skillReg = skill.NewRegistry()
	rt.skillReg.Add(&skill.Skill{
		Name:        "review-pr",
		Description: "Review PR with security and quality focus",
		Trigger:     "/review-pr <pr_number>",
		Source:      "claude-command-skill",
		Prompt:      "Review PR #{pr_number}",
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/commands --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /commands --json: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local commands command", provider.chatCalls, provider.streamCalls)
	}

	var entries []commandCatalogEntry
	if err := json.Unmarshal([]byte(summary), &entries); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, summary)
	}
	seen := map[string]commandCatalogEntry{}
	for _, entry := range entries {
		seen[entry.Name] = entry
	}
	entry, ok := seen["/review-pr"]
	if !ok {
		t.Fatalf("entries = %+v, want /review-pr skill-backed slash command", entries)
	}
	if entry.Category != "skill" || entry.ArgumentHint != "<pr_number>" || entry.Source != "claude-command-skill" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestExecutionRuntimeCommandsSlashCommandHidesNonUserInvocableSkillsByDefault(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	rt.skillReg = skill.NewRegistry()
	rt.skillReg.Add(&skill.Skill{Name: "visible-skill", Trigger: "/visible-skill", Prompt: "Visible."})
	rt.skillReg.Add(&skill.Skill{Name: "hidden-helper", Trigger: "/hidden-helper", Prompt: "Hidden.", Hidden: true})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/commands --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /commands --json: %v", err)
	}
	var entries []commandCatalogEntry
	if err := json.Unmarshal([]byte(summary), &entries); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, summary)
	}
	seen := map[string]commandCatalogEntry{}
	for _, entry := range entries {
		seen[entry.Name] = entry
	}
	if _, ok := seen["/visible-skill"]; !ok {
		t.Fatalf("commands = %+v, want visible skill command", entries)
	}
	if _, ok := seen["/hidden-helper"]; ok {
		t.Fatalf("commands = %+v, hidden skill should be omitted by default", entries)
	}

	_, summary, err = rt.runTask(context.Background(), sess, nil, "/commands --all --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /commands --all --json: %v", err)
	}
	entries = nil
	if err := json.Unmarshal([]byte(summary), &entries); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, summary)
	}
	seen = map[string]commandCatalogEntry{}
	for _, entry := range entries {
		seen[entry.Name] = entry
	}
	if entry, ok := seen["/hidden-helper"]; !ok || !entry.Hidden {
		t.Fatalf("commands = %+v, want hidden helper marked hidden with --all", entries)
	}
}

func TestExecutionRuntimeRunTaskSkillsSlashCommandListsCatalog(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	rt.skillReg = skill.NewRegistry()
	rt.skillReg.Add(&skill.Skill{
		Name:          "pr-review",
		Description:   "Review PR with security and quality focus",
		Trigger:       "/pr-review <pr_number>",
		ArgumentNames: []string{"pr_number"},
		BaseDir:       "/tmp/elnath-skills/pr-review",
		Source:        "codex-plugin-skill",
		Prompt:        "Review PR #{pr_number}",
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var streamed strings.Builder
	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/skills", orchestrationOutput{
		OnText: func(s string) { streamed.WriteString(s) },
	})
	if err != nil {
		t.Fatalf("runTask /skills: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local skills command", provider.chatCalls, provider.streamCalls)
	}
	for _, want := range []string{
		"Available skills:",
		"/pr-review <pr_number> - Review PR with security and quality focus",
	} {
		if !strings.Contains(summary, want) || !strings.Contains(streamed.String(), want) {
			t.Fatalf("summary=%q streamed=%q missing %q", summary, streamed.String(), want)
		}
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
}

func TestExecutionRuntimeSkillsSlashCommandHidesNonUserInvocableSkillsByDefault(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	rt.skillReg = skill.NewRegistry()
	rt.skillReg.Add(&skill.Skill{Name: "visible-skill", Trigger: "/visible-skill", Prompt: "Visible."})
	rt.skillReg.Add(&skill.Skill{Name: "hidden-helper", Trigger: "/hidden-helper", Prompt: "Hidden.", Hidden: true})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/skills", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /skills: %v", err)
	}
	if !strings.Contains(summary, "/visible-skill") {
		t.Fatalf("summary = %q, want visible skill", summary)
	}
	if strings.Contains(summary, "/hidden-helper") {
		t.Fatalf("summary = %q, hidden skill should be omitted by default", summary)
	}

	_, summary, err = rt.runTask(context.Background(), sess, nil, "/skills --all --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /skills --all --json: %v", err)
	}
	var out struct {
		Skills []struct {
			Name          string `json:"name"`
			UserInvocable bool   `json:"user_invocable"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(summary), &out); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, summary)
	}
	seen := map[string]bool{}
	for _, got := range out.Skills {
		seen[got.Name] = got.UserInvocable
	}
	if seen["visible-skill"] != true {
		t.Fatalf("visible-skill user_invocable = %v, want true", seen["visible-skill"])
	}
	if seen["hidden-helper"] != false {
		t.Fatalf("hidden-helper user_invocable = %v, want false", seen["hidden-helper"])
	}
}

func TestExecutionRuntimeBlocksDirectNonUserInvocableSkillExecution(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	rt.skillReg = skill.NewRegistry()
	rt.skillReg.Add(&skill.Skill{Name: "hidden-helper", Trigger: "/hidden-helper", Prompt: "Hidden.", Hidden: true})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, _, err = rt.runTask(context.Background(), sess, nil, "/hidden-helper", orchestrationOutput{})
	if err == nil || !strings.Contains(err.Error(), `skill "hidden-helper" is not user-invocable`) {
		t.Fatalf("runTask /hidden-helper error = %v, want non-user-invocable error", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for hidden direct skill", provider.chatCalls, provider.streamCalls)
	}
}

func TestExecutionRuntimeBlocksDirectNonUserInvocableSkillTriggerAlias(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	rt.skillReg = skill.NewRegistry()
	rt.skillReg.Add(&skill.Skill{Name: "internal-helper", Trigger: "/hidden-helper <target>", Prompt: "Hidden.", Hidden: true})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, _, err = rt.runTask(context.Background(), sess, nil, "/hidden-helper src", orchestrationOutput{})
	if err == nil || !strings.Contains(err.Error(), `skill "internal-helper" is not user-invocable`) {
		t.Fatalf("runTask /hidden-helper error = %v, want non-user-invocable error", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for hidden trigger alias", provider.chatCalls, provider.streamCalls)
	}
}

func TestExecutionRuntimeRunTaskSkillsSlashCommandJSONListsCatalog(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	rt.skillReg = skill.NewRegistry()
	rt.skillReg.Add(&skill.Skill{
		Name:          "pr-review",
		Description:   "Review PR with security and quality focus",
		Trigger:       "/pr-review <pr_number>",
		ArgumentNames: []string{"pr_number"},
		BaseDir:       "/tmp/elnath-skills/pr-review",
		Source:        "codex-plugin-skill",
		Prompt:        "Review PR #{pr_number}",
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/skills --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /skills --json: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local skills command", provider.chatCalls, provider.streamCalls)
	}

	var out struct {
		Action string `json:"action"`
		Skills []struct {
			Name          string   `json:"name"`
			Description   string   `json:"description"`
			Trigger       string   `json:"trigger"`
			ArgumentNames []string `json:"arguments"`
			BaseDir       string   `json:"base_dir"`
			Source        string   `json:"source"`
			TrustLevel    string   `json:"trust_level"`
			External      bool     `json:"external"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(summary), &out); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, summary)
	}
	if out.Action != "list" || len(out.Skills) != 1 {
		t.Fatalf("output = %+v, want one listed skill", out)
	}
	if got := out.Skills[0]; got.Name != "pr-review" || got.Trigger != "/pr-review <pr_number>" || got.BaseDir != "/tmp/elnath-skills/pr-review" {
		t.Fatalf("skill entry = %+v, want pr-review trigger metadata", got)
	}
	if got := out.Skills[0].ArgumentNames; !reflect.DeepEqual(got, []string{"pr_number"}) {
		t.Fatalf("arguments = %v, want [pr_number]", got)
	}
	if got := out.Skills[0]; got.Source != "codex-plugin-skill" || got.TrustLevel != "plugin_cache" || !got.External {
		t.Fatalf("trust metadata = %+v, want plugin_cache external", got)
	}
}

func TestExecutionRuntimeSkillInvocationToolUsesCurrentProviderModel(t *testing.T) {
	first := &countingProvider{streamText: "first provider"}
	second := &countingProvider{streamText: "second provider"}
	rt := newTestExecutionRuntime(t, first)
	rt.skillReg.Add(&skill.Skill{
		Name:   "probe",
		Prompt: "Probe current provider.",
		Status: "active",
	})

	tool, ok := rt.reg.Get("skill")
	if !ok {
		t.Fatal("runtime registry missing skill invocation tool")
	}

	rt.provider = second
	rt.wfCfg.Model = "second-model"
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"skill":"probe"}`))
	if err != nil {
		t.Fatalf("Execute skill tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("skill tool returned error: %s", res.Output)
	}
	if first.streamCalls != 0 {
		t.Fatalf("first provider stream calls = %d, want 0 after runtime provider switch", first.streamCalls)
	}
	if second.streamCalls != 1 {
		t.Fatalf("second provider stream calls = %d, want 1", second.streamCalls)
	}
	if second.lastModel != "second-model" {
		t.Fatalf("second provider model = %q, want second-model", second.lastModel)
	}
}

func TestExecutionRuntimeRegistersWorktreeListTool(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})

	tool, ok := rt.reg.Get("worktree_list")
	if !ok {
		t.Fatal("runtime registry missing worktree_list tool")
	}
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() {
		t.Fatalf("worktree_list metadata = concurrency:%t reversible:%t, want read-only metadata", tool.IsConcurrencySafe(nil), tool.Reversible())
	}
}

func TestExecutionRuntimeRegistersWorktreePruneTool(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})

	tool, ok := rt.reg.Get("worktree_prune")
	if !ok {
		t.Fatal("runtime registry missing worktree_prune tool")
	}
	if tool.IsConcurrencySafe(nil) || tool.Reversible() {
		t.Fatalf("worktree_prune metadata = concurrency:%t reversible:%t, want persistent mutation metadata", tool.IsConcurrencySafe(nil), tool.Reversible())
	}
}

func TestExecutionRuntimeRegistersWorktreeRunTool(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})

	tool, ok := rt.reg.Get("worktree_run")
	if !ok {
		t.Fatal("runtime registry missing worktree_run tool")
	}
	if tool.IsConcurrencySafe(nil) || tool.Reversible() {
		t.Fatalf("worktree_run metadata = concurrency:%t reversible:%t, want mutating execution metadata", tool.IsConcurrencySafe(nil), tool.Reversible())
	}
}

func TestExecutionRuntimeRegistersAgenticActorGraphTool(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})

	tool, ok := rt.reg.Get("agentic_actor_graph")
	if !ok {
		t.Fatal("runtime registry missing agentic_actor_graph tool")
	}
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() {
		t.Fatalf("agentic_actor_graph metadata = concurrency:%t reversible:%t, want read-only metadata", tool.IsConcurrencySafe(nil), tool.Reversible())
	}
	if !tools.ShouldDeferToolSchema(tool) {
		t.Fatal("agentic_actor_graph should defer initial schema")
	}
}

func TestExecutionRuntimeRegistersAgenticTaskEvidenceTool(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})

	tool, ok := rt.reg.Get("agentic_task_evidence")
	if !ok {
		t.Fatal("runtime registry missing agentic_task_evidence tool")
	}
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() {
		t.Fatalf("agentic_task_evidence metadata = concurrency:%t reversible:%t, want read-only metadata", tool.IsConcurrencySafe(nil), tool.Reversible())
	}
	if !tools.ShouldDeferToolSchema(tool) {
		t.Fatal("agentic_task_evidence should defer initial schema")
	}
}

func TestExecutionRuntimeRegistersAgenticDelegateCreateTool(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})

	tool, ok := rt.reg.Get("agentic_delegate_create")
	if !ok {
		t.Fatal("runtime registry missing agentic_delegate_create tool")
	}
	if tool.IsConcurrencySafe(nil) || tool.Reversible() {
		t.Fatalf("agentic_delegate_create metadata = concurrency:%t reversible:%t, want mutating metadata", tool.IsConcurrencySafe(nil), tool.Reversible())
	}
	if !tools.ShouldDeferToolSchema(tool) {
		t.Fatal("agentic_delegate_create should defer initial schema")
	}
}

func TestExecutionRuntimeRegistersAgenticDelegateListTool(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})

	tool, ok := rt.reg.Get("agentic_delegate_list")
	if !ok {
		t.Fatal("runtime registry missing agentic_delegate_list tool")
	}
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() {
		t.Fatalf("agentic_delegate_list metadata = concurrency:%t reversible:%t, want read-only metadata", tool.IsConcurrencySafe(nil), tool.Reversible())
	}
	if !tools.ShouldDeferToolSchema(tool) {
		t.Fatal("agentic_delegate_list should defer initial schema")
	}
}

func TestExecutionRuntimeRegistersAgenticDelegateEnqueueTool(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})

	tool, ok := rt.reg.Get("agentic_delegate_enqueue")
	if !ok {
		t.Fatal("runtime registry missing agentic_delegate_enqueue tool")
	}
	if tool.IsConcurrencySafe(nil) || tool.Reversible() {
		t.Fatalf("agentic_delegate_enqueue metadata = concurrency:%t reversible:%t, want mutating metadata", tool.IsConcurrencySafe(nil), tool.Reversible())
	}
	if !tools.ShouldDeferToolSchema(tool) {
		t.Fatal("agentic_delegate_enqueue should defer initial schema")
	}
}

func TestExecutionRuntimeRegistersAgenticMessageTools(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})

	send, ok := rt.reg.Get("agentic_message_send")
	if !ok {
		t.Fatal("runtime registry missing agentic_message_send tool")
	}
	if send.IsConcurrencySafe(nil) || send.Reversible() {
		t.Fatalf("agentic_message_send metadata = concurrency:%t reversible:%t, want mutating metadata", send.IsConcurrencySafe(nil), send.Reversible())
	}
	if !tools.ShouldDeferToolSchema(send) {
		t.Fatal("agentic_message_send should defer initial schema")
	}

	list, ok := rt.reg.Get("agentic_message_list")
	if !ok {
		t.Fatal("runtime registry missing agentic_message_list tool")
	}
	if !list.IsConcurrencySafe(nil) || !list.Reversible() {
		t.Fatalf("agentic_message_list metadata = concurrency:%t reversible:%t, want read-only metadata", list.IsConcurrencySafe(nil), list.Reversible())
	}
	if !tools.ShouldDeferToolSchema(list) {
		t.Fatal("agentic_message_list should defer initial schema")
	}
}

func TestExecutionRuntimeAgenticDelegateEnqueueToolQueuesDelegatedChild(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})
	ctx := context.Background()
	parent, err := rt.agenticStore.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "Parent",
		Prompt:             "coordinate work",
		Status:             agentic.TaskStatusProposed,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask parent: %v", err)
	}
	child, err := rt.agenticStore.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "Child",
		Prompt:             "execute delegated work",
		ParentID:           parent.ID,
		Status:             agentic.TaskStatusProposed,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask child: %v", err)
	}
	if _, err := rt.agenticStore.CreateTaskEdge(ctx, agentic.TaskEdge{ParentID: parent.ID, ChildID: child.ID, EdgeType: "delegates_to"}); err != nil {
		t.Fatalf("CreateTaskEdge: %v", err)
	}
	tool, ok := rt.reg.Get("agentic_delegate_enqueue")
	if !ok {
		t.Fatal("runtime registry missing agentic_delegate_enqueue tool")
	}

	result, err := tool.Execute(ctx, json.RawMessage(`{"child_task_id":`+fmt.Sprint(child.ID)+`,"operator_id":"runtime-test"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v, want success", result)
	}
	var out struct {
		ChildTaskID    int64  `json:"child_task_id"`
		ParentTaskID   int64  `json:"parent_task_id"`
		QueueTaskID    int64  `json:"queue_task_id"`
		DecisionStatus string `json:"decision_status"`
	}
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, result.Output)
	}
	if out.ChildTaskID != child.ID || out.ParentTaskID != parent.ID || out.QueueTaskID == 0 || out.DecisionStatus != agentic.TaskEnqueueStatusEnqueued {
		t.Fatalf("output = %+v, want queued delegated child", out)
	}
	updated, err := rt.agenticStore.GetAgenticTask(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask child: %v", err)
	}
	if updated.QueueTaskID != out.QueueTaskID || updated.Status != agentic.TaskStatusPending {
		t.Fatalf("updated child = %+v, want queue-backed pending child", updated)
	}
}

func TestExecutionRuntimeRegistersDeferredControlSurfaceTools(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})

	for _, name := range []string{
		"code_symbols",
		"sleep",
		"task_create", "user_question_answer", "user_question_list", "task_list", "task_get", "task_stop", "task_output", "task_monitor", "task_update",
		"schedule_create", "schedule_list", "schedule_delete",
		"enter_worktree", "worktree_list", "worktree_run", "worktree_prune", "exit_worktree",
		"agentic_actor_graph", "agentic_task_evidence", "agentic_delegate_create", "agentic_delegate_list", "agentic_delegate_status", "agentic_delegate_enqueue",
		"agentic_message_send", "agentic_message_list",
	} {
		tool, ok := rt.reg.Get(name)
		if !ok {
			t.Fatalf("runtime registry missing %s tool", name)
		}
		if !tools.ShouldDeferToolSchema(tool) {
			t.Fatalf("%s should defer initial schema", name)
		}
	}
}

type fakeRuntimeRunningCanceller struct {
	called  int
	taskID  int64
	reason  string
	stopped bool
}

func (f *fakeRuntimeRunningCanceller) CancelRunningTask(id int64, reason string) (bool, error) {
	f.called++
	f.taskID = id
	f.reason = reason
	return f.stopped, nil
}

func TestExecutionRuntimeBindsTaskStopRunningCanceller(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{streamText: "unused"})
	if rt.taskStopTool == nil {
		t.Fatal("taskStopTool is nil")
	}
	tool, ok := rt.reg.Get("task_stop")
	if !ok {
		t.Fatal("task_stop not registered")
	}
	if got, ok := tool.(*daemon.TaskStopTool); !ok || got != rt.taskStopTool {
		t.Fatalf("registered task_stop = %T, want runtime taskStopTool pointer", tool)
	}

	ctx := context.Background()
	queue, err := daemon.NewQueueNoRecover(rt.db.Main)
	if err != nil {
		t.Fatalf("NewQueueNoRecover: %v", err)
	}
	if _, _, err := queue.Enqueue(ctx, "runtime running task", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}

	canceller := &fakeRuntimeRunningCanceller{stopped: true}
	rt.bindRunningTaskCanceller(canceller)
	result, err := rt.taskStopTool.Execute(ctx, json.RawMessage(`{"id":`+fmt.Sprint(task.ID)+`,"reason":"operator stop"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v, want success", result)
	}
	if canceller.called != 1 || canceller.taskID != task.ID || canceller.reason != "operator stop" {
		t.Fatalf("canceller = %+v, want one call for task %d", canceller, task.ID)
	}
}

func TestExecutionRuntimeRunTaskHelpSlashCommandListsCatalog(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/help", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /help: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local help command", provider.chatCalls, provider.streamCalls)
	}
	for _, want := range []string{"Elnath commands:", "commands", "run", "skill"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary=%q missing %q", summary, want)
		}
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
}

func TestExecutionRuntimeRunTaskVersionSlashCommand(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/version", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /version: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local version command", provider.chatCalls, provider.streamCalls)
	}
	if summary != "elnath "+version {
		t.Fatalf("summary = %q, want version output", summary)
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
	if messages[1].Text() != summary {
		t.Fatalf("assistant message = %q, want %q", messages[1].Text(), summary)
	}
}

func TestExecutionRuntimeRunTaskStatusSlashCommand(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/status", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /status: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local status command", provider.chatCalls, provider.streamCalls)
	}
	for _, want := range []string{
		"Elnath runtime status:",
		"version:        " + version,
		"provider:       mock",
		"model:          mock-model",
		"effort:         auto",
		"permission:     bypass",
		"tool_exposure:  standard",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary = %q missing %q", summary, want)
		}
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
}

func TestExecutionRuntimeRunTaskStatusSlashCommandJSON(t *testing.T) {
	provider := &capabilityCountingProvider{}
	rt := newTestExecutionRuntime(t, provider)
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/status --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /status --json: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local status command", provider.chatCalls, provider.streamCalls)
	}

	var out struct {
		Version        string `json:"version"`
		Provider       string `json:"provider"`
		Model          string `json:"model"`
		EffortMode     string `json:"effort_mode"`
		ProviderEffort string `json:"provider_effort"`
		AutoCompatible bool   `json:"auto_effort_compatible"`
		PermissionMode string `json:"permission_mode"`
		ToolExposure   string `json:"tool_exposure"`
	}
	if err := json.Unmarshal([]byte(summary), &out); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, summary)
	}
	if out.Version != version || out.Provider != "openai-responses" || out.Model != "mock-model" || out.EffortMode != "auto" || out.ProviderEffort != llm.ReasoningEffortNativeWithUnsupportedRetry || !out.AutoCompatible || out.PermissionMode != "bypass" || out.ToolExposure != "standard" {
		t.Fatalf("status output = %+v", out)
	}
}

func TestExecutionRuntimeRunTaskPlanSlashCommandSwitchesAndRestoresMode(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	messages, summary, err := rt.runTask(context.Background(), sess, nil, "/plan", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /plan: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local plan command", provider.chatCalls, provider.streamCalls)
	}
	if rt.wfCfg.Permission.Mode() != agent.ModePlan {
		t.Fatalf("permission mode = %s, want plan", rt.wfCfg.Permission.Mode())
	}
	if !strings.Contains(summary, "Entered plan mode") {
		t.Fatalf("summary = %q, want plan mode entry", summary)
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}

	_, summary, err = rt.runTask(context.Background(), sess, messages, "/plan exit", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /plan exit: %v", err)
	}
	if rt.wfCfg.Permission.Mode() != agent.ModeBypass {
		t.Fatalf("permission mode = %s, want restored bypass", rt.wfCfg.Permission.Mode())
	}
	if !strings.Contains(summary, "Exited plan mode") {
		t.Fatalf("summary = %q, want plan mode exit", summary)
	}
}

func TestExecutionRuntimeRunTaskCommandsSlashCommandJSONIncludesRuntimeControls(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/commands --json", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /commands --json: %v", err)
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for local commands command", provider.chatCalls, provider.streamCalls)
	}

	var entries []commandCatalogEntry
	if err := json.Unmarshal([]byte(summary), &entries); err != nil {
		t.Fatalf("summary is not JSON: %v\n%s", err, summary)
	}
	seen := map[string]commandCatalogEntry{}
	for _, entry := range entries {
		seen[entry.Name] = entry
	}
	for _, want := range []string{"/effort", "/model", "/provider", "/help", "/skills", "/version", "/status", "/plan"} {
		entry, ok := seen[want]
		if !ok {
			t.Fatalf("missing runtime command %s in JSON catalog: %+v", want, entries)
		}
		if entry.Category != "runtime-control" {
			t.Fatalf("runtime command %s category = %q, want runtime-control", want, entry.Category)
		}
		if entry.ArgumentHint == "" {
			t.Fatalf("runtime command %s argument hint is empty", want)
		}
	}
}

func TestRuntimeLocalSlashCommandRegistry(t *testing.T) {
	specs := runtimeLocalSlashCommandSpecs()
	if len(specs) == 0 {
		t.Fatal("runtimeLocalSlashCommandSpecs returned no commands")
	}
	names := map[string]bool{}
	for _, spec := range specs {
		if spec.Name == "" {
			t.Fatalf("spec with empty name: %+v", spec)
		}
		if !strings.HasPrefix(spec.Name, "/") {
			t.Fatalf("spec name = %q, want leading slash", spec.Name)
		}
		if spec.Description == "" {
			t.Fatalf("spec %q has empty description", spec.Name)
		}
		if spec.ArgumentHint == "" {
			t.Fatalf("spec %q has empty argument hint", spec.Name)
		}
		if spec.Handler == nil {
			t.Fatalf("spec %q has nil handler", spec.Name)
		}
		names[spec.Name] = true
	}
	for _, want := range []string{"/effort", "/model", "/provider", "/commands", "/help", "/skills", "/version", "/status", "/plan"} {
		if !names[want] {
			t.Fatalf("runtime local slash registry missing %s; got %+v", want, specs)
		}
	}
	for _, spec := range specs {
		if spec.Name == "/skills" && (!strings.Contains(spec.ArgumentHint, "--all") || !strings.Contains(spec.ArgumentHint, "--hidden")) {
			t.Fatalf("/skills argument hint = %q, want --all and --hidden", spec.ArgumentHint)
		}
	}
}

func TestExecutionRuntimeSkillsSlashCommandHelpShowsHiddenFlags(t *testing.T) {
	provider := &countingProvider{streamText: "runtime answer"}
	rt := newTestExecutionRuntime(t, provider)
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "/skills --help", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask /skills --help: %v", err)
	}
	for _, want := range []string{"--json", "--all", "--hidden"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary = %q, want %s", summary, want)
		}
	}
	if provider.streamCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("provider calls = chat:%d stream:%d, want none for skills help", provider.chatCalls, provider.streamCalls)
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

func TestExecutionRuntimeUserQuestionAnswerRequiresPendingRequest(t *testing.T) {
	rt := newTestExecutionRuntime(t, &countingProvider{})
	tool, ok := rt.reg.Get("user_question_answer")
	if !ok {
		t.Fatal("user_question_answer tool missing")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"session_id":"sess-123","request_id":"missing-req","answer":"Use main."}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "request_id is not pending for session_id") {
		t.Fatalf("result = %+v, want runtime pending-question binding error", result)
	}

	if err := rt.outcomeStore.Append(learning.OutcomeRecord{
		Timestamp: time.Date(2026, 5, 13, 7, 0, 0, 0, time.UTC),
		ControlToolReceipts: []learning.ControlToolReceipt{{
			Tool:      "ask_user_question",
			Action:    "request",
			RequestID: "req-123",
			SessionID: "sess-123",
			Question:  "Which branch?",
		}},
	}); err != nil {
		t.Fatalf("Append outcome: %v", err)
	}

	result, err = tool.Execute(context.Background(), json.RawMessage(`{"session_id":"sess-123","request_id":"req-123","answer":"Use main."}`))
	if err != nil {
		t.Fatalf("bound Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("bound Execute returned error result: %s", result.Output)
	}
	if !strings.Contains(result.Output, `"type":"user_input_answer_enqueued"`) {
		t.Fatalf("bound result = %s, want enqueue output", result.Output)
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
		cfg.SelfHealing.CompletionRetryMax = 1
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "answer the status question", orchestrationOutput{})
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

func TestExecutionRuntimeRunTaskSelfHealingCorrectionUsesSecondBoundedRetry(t *testing.T) {
	provider := &sequenceStreamProvider{responses: []string{
		"I could not finish the patch.",
		"I still could not finish the patch.",
		"Done now.",
	}}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
		cfg.SelfHealing.CompletionRetryMax = 2
	})
	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, summary, err := rt.runTask(context.Background(), sess, nil, "answer the status question", orchestrationOutput{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if summary != "Done now." {
		t.Fatalf("summary = %q, want second retry result", summary)
	}
	if provider.idx != 3 {
		t.Fatalf("streamed responses = %d, want initial attempt plus two bounded retries", provider.idx)
	}
	records, err := rt.outcomeStore.Recent(1)
	if err != nil {
		t.Fatalf("Recent outcomes: %v", err)
	}
	if len(records) != 1 || !records[0].CorrectionAttempted || records[0].CorrectionAttempts != 2 {
		t.Fatalf("correction outcome = %+v, want two recorded correction attempts", records)
	}
	if records[0].CorrectionMaxAttempts != 2 || records[0].CorrectionStatus != "succeeded" {
		t.Fatalf("correction budget/status = max %d status %q warning %q retry %q/%q", records[0].CorrectionMaxAttempts, records[0].CorrectionStatus, records[0].CompletionWarning, records[0].RetryDecision, records[0].RetryReason)
	}
	if len(records[0].CorrectionAttemptDetails) != 2 {
		t.Fatalf("correction attempt details = %+v, want two entries", records[0].CorrectionAttemptDetails)
	}
	if records[0].CorrectionAttemptDetails[0].Attempt != 1 || records[0].CorrectionAttemptDetails[0].Status != "retrying" || records[0].CorrectionAttemptDetails[0].FailureFamily != "" {
		t.Fatalf("first correction detail = %+v", records[0].CorrectionAttemptDetails[0])
	}
	if records[0].CorrectionAttemptDetails[1].Attempt != 2 || records[0].CorrectionAttemptDetails[1].Status != "succeeded" {
		t.Fatalf("second correction detail = %+v", records[0].CorrectionAttemptDetails[1])
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

func TestExecutionRuntimeRunTaskSelfHealingRetryMaxZeroDisablesCorrection(t *testing.T) {
	provider := &sequenceStreamProvider{responses: []string{
		"I could not finish the patch.",
		"Done now.",
	}}
	rt := newTestExecutionRuntimeWithConfig(t, provider, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
		cfg.SelfHealing.CompletionRetryMax = 0
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
		t.Fatalf("streamed responses = %d, want retry disabled by completion_retry_max=0", provider.idx)
	}
	if rt.completionRetryMax != 0 {
		t.Fatalf("completionRetryMax = %d, want 0", rt.completionRetryMax)
	}
}

func TestCompletionRetryEscalatesAutoEffort(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 1
	wf := &captureRetryWorkflow{name: "single"}
	result := &orchestrator.WorkflowResult{
		Messages:              []llm.Message{llm.NewAssistantMessage("I could not finish the patch.")},
		Summary:               "I could not finish the patch.",
		FinishReason:          "stop",
		Workflow:              "single",
		ReasoningEffort:       "low",
		ReasoningEffortMode:   "auto",
		ReasoningEffortReason: "simple_keyword",
	}
	summary := completionContractSummary{
		CompletionWarning:     "final_response_reports_incomplete",
		ReasoningEffort:       "low",
		ReasoningEffortMode:   "auto",
		ReasoningEffortReason: "simple_keyword",
		RetryDecision:         completionRetryDecisionRetrySmallerScope,
		RetryReason:           "final_response_reports_incomplete",
	}

	_, gotSummary := rt.maybeRunCompletionRetry(context.Background(), wf, orchestrator.WorkflowInput{
		Provider: rt.provider,
		Config: orchestrator.WorkflowConfig{
			ReasoningEffortMode: "auto",
		},
	}, result, summary)

	if wf.input.Config.ReasoningEffortMode != "manual" || wf.input.Config.ReasoningEffort != "high" {
		t.Fatalf("retry effort config = mode %q effort %q, want manual/high", wf.input.Config.ReasoningEffortMode, wf.input.Config.ReasoningEffort)
	}
	if gotSummary.ReasoningEffort != "high" || gotSummary.ReasoningEffortMode != "manual" || gotSummary.ReasoningEffortReason != "correction_retry_escalation" {
		t.Fatalf("retry summary effort = effort %q mode %q reason %q", gotSummary.ReasoningEffort, gotSummary.ReasoningEffortMode, gotSummary.ReasoningEffortReason)
	}
}

func TestCompletionRetryPreservesVerificationRequirement(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 1
	wf := &captureRetryWorkflow{name: "single"}
	result := &orchestrator.WorkflowResult{
		Messages:     []llm.Message{llm.NewAssistantMessage("I could not finish the patch.")},
		Summary:      "I could not finish the patch.",
		FinishReason: "stop",
		Workflow:     "single",
	}
	observed := false
	summary := completionContractSummary{
		VerificationHint:     true,
		VerificationObserved: &observed,
		CompletionWarning:    "final_response_reports_incomplete",
		RetryDecision:        completionRetryDecisionRetrySmallerScope,
		RetryReason:          "final_response_reports_incomplete",
	}

	_, gotSummary := rt.maybeRunCompletionRetry(context.Background(), wf, orchestrator.WorkflowInput{
		Provider: rt.provider,
	}, result, summary)

	if !gotSummary.VerificationHint {
		t.Fatal("VerificationHint = false, want preserved true after correction retry")
	}
	if gotSummary.VerificationObserved == nil || *gotSummary.VerificationObserved {
		t.Fatalf("VerificationObserved = %v, want explicit false after unverified correction retry", gotSummary.VerificationObserved)
	}
	if gotSummary.RetryDecision != completionRetryDecisionRunVerification || gotSummary.RetryReason != "verification_hint_not_observed" {
		t.Fatalf("retry plan = %q/%q, want run_verification/verification_hint_not_observed", gotSummary.RetryDecision, gotSummary.RetryReason)
	}
	if gotSummary.CorrectionStatus != "succeeded" {
		t.Fatalf("CorrectionStatus = %q, want succeeded workflow correction with remaining verification requirement", gotSummary.CorrectionStatus)
	}
}

func TestCompletionRetryPreservesPriorAttemptWhenVerificationSkipFollowsCorrection(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 2
	wf := &captureRetryWorkflow{name: "single"}
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("answer the status question\n\ngo test ./cmd/elnath -count=1"),
			llm.NewAssistantMessage("I could not finish the answer."),
		},
		Summary:      "I could not finish the answer.",
		FinishReason: "stop",
		Workflow:     "single",
	}
	observed := false
	summary := completionContractSummary{
		VerificationHint:     true,
		VerificationObserved: &observed,
		CompletionWarning:    "final_response_reports_incomplete",
		RetryDecision:        completionRetryDecisionRetrySmallerScope,
		RetryReason:          "final_response_reports_incomplete",
	}

	_, gotSummary := rt.maybeRunCompletionRetry(context.Background(), wf, orchestrator.WorkflowInput{
		Provider: rt.provider,
	}, result, summary)

	if gotSummary.CorrectionStatus != "skipped" || gotSummary.CorrectionFailureFamily != "missing_verification_executor" {
		t.Fatalf("correction skip = status %q family %q", gotSummary.CorrectionStatus, gotSummary.CorrectionFailureFamily)
	}
	if gotSummary.CorrectionAttempts != 1 || gotSummary.CorrectionMaxAttempts != 2 {
		t.Fatalf("correction attempts = %d max %d, want prior smaller-scope attempt preserved", gotSummary.CorrectionAttempts, gotSummary.CorrectionMaxAttempts)
	}
	if len(gotSummary.CorrectionAttemptDetails) != 2 {
		t.Fatalf("correction attempt details = %+v, want correction plus skipped verification", gotSummary.CorrectionAttemptDetails)
	}
	if gotSummary.CorrectionAttemptDetails[0].Attempt != 1 || gotSummary.CorrectionAttemptDetails[0].Status != "succeeded" {
		t.Fatalf("first correction detail = %+v", gotSummary.CorrectionAttemptDetails[0])
	}
	if gotSummary.CorrectionAttemptDetails[1].Attempt != 2 || gotSummary.CorrectionAttemptDetails[1].Status != "skipped" || gotSummary.CorrectionAttemptDetails[1].FailureFamily != "missing_verification_executor" {
		t.Fatalf("second correction detail = %+v", gotSummary.CorrectionAttemptDetails[1])
	}
}

func TestCompletionRetryRecordsFailedCorrectionAttempt(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 1
	result := &orchestrator.WorkflowResult{
		Messages:     []llm.Message{llm.NewAssistantMessage("I could not finish the patch.")},
		Summary:      "I could not finish the patch.",
		FinishReason: "stop",
		Workflow:     "single",
	}
	summary := completionContractSummary{
		CompletionWarning: "final_response_reports_incomplete",
		RetryDecision:     completionRetryDecisionRetrySmallerScope,
		RetryReason:       "final_response_reports_incomplete",
	}

	gotResult, gotSummary := rt.maybeRunCompletionRetry(context.Background(), &failingRetryWorkflow{name: "single"}, orchestrator.WorkflowInput{
		Provider: rt.provider,
	}, result, summary)

	if gotResult != result {
		t.Fatal("failed correction retry should preserve original workflow result")
	}
	if !gotSummary.CorrectionAttempted || gotSummary.CorrectionAttempts != 1 {
		t.Fatalf("correction attempt = attempted %v attempts %d", gotSummary.CorrectionAttempted, gotSummary.CorrectionAttempts)
	}
	if gotSummary.CorrectionDecision != completionRetryDecisionRetrySmallerScope || gotSummary.CorrectionReason != "final_response_reports_incomplete" {
		t.Fatalf("correction reason = decision %q reason %q", gotSummary.CorrectionDecision, gotSummary.CorrectionReason)
	}
	if gotSummary.CorrectionStatus != "failed" || gotSummary.CorrectionFailureFamily != "workflow_error" {
		t.Fatalf("correction failure = status %q family %q", gotSummary.CorrectionStatus, gotSummary.CorrectionFailureFamily)
	}
}

func TestCompletionRetryRecordsMissingRetryWorkflow(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 1
	result := &orchestrator.WorkflowResult{
		Messages:     []llm.Message{llm.NewAssistantMessage("I could not finish the patch.")},
		Summary:      "I could not finish the patch.",
		FinishReason: "stop",
		Workflow:     "single",
	}
	summary := completionContractSummary{
		CompletionWarning: "final_response_reports_incomplete",
		RetryDecision:     completionRetryDecisionRetrySmallerScope,
		RetryReason:       "final_response_reports_incomplete",
	}

	gotResult, gotSummary := rt.maybeRunCompletionRetry(context.Background(), nil, orchestrator.WorkflowInput{
		Provider: rt.provider,
	}, result, summary)

	if gotResult != result {
		t.Fatal("missing retry workflow should preserve original result")
	}
	if gotSummary.CorrectionAttempted || gotSummary.CorrectionAttempts != 0 || gotSummary.CorrectionMaxAttempts != 1 {
		t.Fatalf("correction budget = attempted %v attempts %d max %d", gotSummary.CorrectionAttempted, gotSummary.CorrectionAttempts, gotSummary.CorrectionMaxAttempts)
	}
	if gotSummary.CorrectionStatus != "skipped" || gotSummary.CorrectionFailureFamily != "missing_retry_workflow" {
		t.Fatalf("missing workflow skip = status %q family %q", gotSummary.CorrectionStatus, gotSummary.CorrectionFailureFamily)
	}
	if gotSummary.CorrectionDecision != completionRetryDecisionRetrySmallerScope || gotSummary.CorrectionReason != "final_response_reports_incomplete" {
		t.Fatalf("correction decision = %q/%q", gotSummary.CorrectionDecision, gotSummary.CorrectionReason)
	}
}

func TestCompletionRetryMarksUnresolvedWarningFailed(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 1
	wf := &captureRetryWorkflow{name: "single", response: "I still could not finish the patch."}
	result := &orchestrator.WorkflowResult{
		Messages:     []llm.Message{llm.NewAssistantMessage("I could not finish the patch.")},
		Summary:      "I could not finish the patch.",
		FinishReason: "stop",
		Workflow:     "single",
	}
	summary := completionContractSummary{
		CompletionWarning: "final_response_reports_incomplete",
		RetryDecision:     completionRetryDecisionRetrySmallerScope,
		RetryReason:       "final_response_reports_incomplete",
	}

	_, gotSummary := rt.maybeRunCompletionRetry(context.Background(), wf, orchestrator.WorkflowInput{
		Provider: rt.provider,
	}, result, summary)

	if !gotSummary.CorrectionAttempted || gotSummary.CorrectionAttempts != 1 {
		t.Fatalf("correction attempt = attempted %v attempts %d", gotSummary.CorrectionAttempted, gotSummary.CorrectionAttempts)
	}
	if gotSummary.CompletionWarning != "final_response_reports_incomplete" {
		t.Fatalf("CompletionWarning = %q, want unresolved incomplete warning", gotSummary.CompletionWarning)
	}
	if gotSummary.CorrectionStatus != "failed" || gotSummary.CorrectionFailureFamily != "completion_warning_unresolved" {
		t.Fatalf("correction status = %q family %q, want failed completion_warning_unresolved", gotSummary.CorrectionStatus, gotSummary.CorrectionFailureFamily)
	}
}

func TestCompletionRetryFailsClosedOnScopeDrift(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 2
	result := &orchestrator.WorkflowResult{
		Messages:     []llm.Message{llm.NewAssistantMessage("I could not finish the patch.")},
		Summary:      "I could not finish the patch.",
		FinishReason: "stop",
		Workflow:     "single",
	}
	summary := completionContractSummary{
		CompletionWarning: "final_response_reports_incomplete",
		RetryDecision:     completionRetryDecisionRetrySmallerScope,
		RetryReason:       "final_response_reports_incomplete",
	}

	_, gotSummary := rt.maybeRunCompletionRetry(context.Background(), &scopeDriftRetryWorkflow{
		name: "single",
		path: "docs/unrelated.md",
	}, orchestrator.WorkflowInput{
		Provider: rt.provider,
		Config: orchestrator.WorkflowConfig{
			CorrectionScope: orchestrator.CorrectionScope{
				Label:        "daemon-only",
				AllowedPaths: []string{"internal/daemon/"},
			},
		},
	}, result, summary)

	if gotSummary.CompletionWarning != "scope_drift" {
		t.Fatalf("CompletionWarning = %q, want scope_drift", gotSummary.CompletionWarning)
	}
	if gotSummary.CorrectionStatus != "failed" || gotSummary.CorrectionFailureFamily != "scope_drift" {
		t.Fatalf("correction status = %q family %q, want failed/scope_drift", gotSummary.CorrectionStatus, gotSummary.CorrectionFailureFamily)
	}
	if gotSummary.RetryDecision != "" || gotSummary.RetryReason != "" {
		t.Fatalf("retry = %q/%q, want fail-closed empty retry", gotSummary.RetryDecision, gotSummary.RetryReason)
	}
	if len(gotSummary.CorrectionAttemptDetails) != 1 || gotSummary.CorrectionAttemptDetails[0].FailureFamily != "scope_drift" {
		t.Fatalf("correction attempt details = %+v, want scope_drift detail", gotSummary.CorrectionAttemptDetails)
	}
	if got := gotSummary.OutOfScopeChangedFiles; len(got) != 1 || got[0] != "docs/unrelated.md" {
		t.Fatalf("OutOfScopeChangedFiles = %#v, want docs/unrelated.md", gotSummary.OutOfScopeChangedFiles)
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
	if gotSummary.CorrectionMaxAttempts != 1 {
		t.Fatalf("CorrectionMaxAttempts = %d, want 1", gotSummary.CorrectionMaxAttempts)
	}
}

func TestCompletionRetryRecordsFailedVerificationCommand(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 1
	reg := tools.NewRegistry()
	bash := &recordingRuntimeTool{name: "bash", output: "FAIL", isError: true}
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
		t.Fatal("failed verification retry should preserve original workflow result")
	}
	if bash.calls != 1 || bash.command != "go test ./cmd/elnath -count=1" {
		t.Fatalf("bash calls = %d command = %q, want explicit verification command", bash.calls, bash.command)
	}
	if gotSummary.VerificationObserved == nil || !*gotSummary.VerificationObserved || gotSummary.VerificationCommand != "go test ./cmd/elnath -count=1" {
		t.Fatalf("failed verification receipt = observed %v command %q, want observed failed command", gotSummary.VerificationObserved, gotSummary.VerificationCommand)
	}
	if !gotSummary.CorrectionAttempted || gotSummary.CorrectionStatus != "failed" || gotSummary.CorrectionFailureFamily != "verification_command_failed" {
		t.Fatalf("correction failure summary = %+v", gotSummary)
	}
}

func TestCompletionRetryFailsClosedOnBroadVerificationCommand(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 1
	reg := tools.NewRegistry()
	bash := &recordingRuntimeTool{name: "bash", output: "FAIL", isError: true}
	reg.Register(bash)
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("fix the daemon\n\ngo test ./..."),
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

	if bash.calls != 1 || bash.command != "go test ./..." {
		t.Fatalf("bash calls = %d command = %q, want broad verification command", bash.calls, bash.command)
	}
	if gotSummary.VerificationClass != "broad" || gotSummary.VerificationOwnership != "harness" {
		t.Fatalf("verification policy = class %q ownership %q, want broad/harness", gotSummary.VerificationClass, gotSummary.VerificationOwnership)
	}
	if gotSummary.CorrectionStatus != "failed" || gotSummary.CorrectionFailureFamily != "broad_verification_failed" {
		t.Fatalf("correction failure = status %q family %q, want failed/broad_verification_failed", gotSummary.CorrectionStatus, gotSummary.CorrectionFailureFamily)
	}
	if gotSummary.RetryDecision != "" || gotSummary.RetryReason != "" {
		t.Fatalf("retry = %q/%q, want fail-closed empty retry", gotSummary.RetryDecision, gotSummary.RetryReason)
	}
}

func TestCompletionRetryRecordsVerificationExecutorErrorCommand(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 1
	reg := tools.NewRegistry()
	bash := &recordingRuntimeTool{name: "bash", execErr: fmt.Errorf("executor unavailable")}
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

	_, gotSummary := rt.maybeRunCompletionRetry(context.Background(), &stubWorkflow{name: "single"}, orchestrator.WorkflowInput{
		Session:  &agent.Session{ID: "verify-session"},
		Tools:    reg,
		Provider: rt.provider,
	}, result, summary)

	if bash.calls != 1 || bash.command != "go test ./cmd/elnath -count=1" {
		t.Fatalf("bash calls = %d command = %q, want explicit verification command", bash.calls, bash.command)
	}
	if gotSummary.VerificationObserved == nil || !*gotSummary.VerificationObserved || gotSummary.VerificationCommand != "go test ./cmd/elnath -count=1" {
		t.Fatalf("executor-error receipt = observed %v command %q, want attempted command", gotSummary.VerificationObserved, gotSummary.VerificationCommand)
	}
	if gotSummary.CorrectionStatus != "failed" || gotSummary.CorrectionFailureFamily != "verification_executor_error" {
		t.Fatalf("executor-error correction summary = %+v", gotSummary)
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
	if gotSummary.CorrectionStatus != "skipped" || gotSummary.CorrectionFailureFamily != "missing_explicit_verification_command" {
		t.Fatalf("correction skip = status %q family %q", gotSummary.CorrectionStatus, gotSummary.CorrectionFailureFamily)
	}
	if gotSummary.CorrectionDecision != completionRetryDecisionRunVerification || gotSummary.CorrectionReason != "verification_hint_not_observed" {
		t.Fatalf("correction decision = %q/%q, want run_verification/verification_hint_not_observed", gotSummary.CorrectionDecision, gotSummary.CorrectionReason)
	}
}

func TestCompletionRetryRecordsMissingVerificationExecutor(t *testing.T) {
	rt := newTestExecutionRuntimeWithConfig(t, &countingProvider{}, false, func(cfg *config.Config) {
		cfg.SelfHealing.Enabled = true
		cfg.SelfHealing.ObserveOnly = false
	})
	rt.completionRetryMax = 1
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
		Provider: rt.provider,
	}, result, summary)

	if gotResult != result {
		t.Fatal("missing verification executor should preserve original workflow result")
	}
	if gotSummary.CorrectionAttempted {
		t.Fatalf("correction attempted without executor: %+v", gotSummary)
	}
	if gotSummary.CorrectionStatus != "skipped" || gotSummary.CorrectionFailureFamily != "missing_verification_executor" {
		t.Fatalf("correction skip = status %q family %q", gotSummary.CorrectionStatus, gotSummary.CorrectionFailureFamily)
	}
	if gotSummary.CorrectionDecision != completionRetryDecisionRunVerification || gotSummary.CorrectionReason != "verification_hint_not_observed" {
		t.Fatalf("correction decision = %q/%q, want run_verification/verification_hint_not_observed", gotSummary.CorrectionDecision, gotSummary.CorrectionReason)
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
	t.Setenv("HOME", filepath.Join(root, "home"))
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

func TestExecutionRuntimeBuildsSkillCatalogFromCodexSkillRoots(t *testing.T) {
	root := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	writeRuntimeCompatSkill(t, filepath.Join(root, ".codex", "skills", "project-codex"), "Project Codex")
	writeRuntimeCompatSkill(t, filepath.Join(homeDir, ".codex", "skills", "user-codex"), "User Codex")
	writeRuntimeCompatSkill(t, filepath.Join(homeDir, ".agents", "skills", "agent-skill"), "Agent Skill")
	writeRuntimeCompatSkill(t, filepath.Join(homeDir, ".codex", "plugins", "cache", "openai-curated", "github", "63976030", "skills", "github"), "GitHub")

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

	rt, err := buildExecutionRuntime(
		context.Background(),
		cfg,
		app,
		db,
		&countingProvider{streamText: "runtime answer"},
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
	want := []string{"agent-skill", "github", "project-codex", "user-codex"}
	if got := rt.skillReg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("skillReg names = %v, want %v", got, want)
	}
}

func TestExecutionRuntimeCanDisablePluginCacheSkillRoots(t *testing.T) {
	root := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	writeRuntimeCompatSkill(t, filepath.Join(root, ".codex", "skills", "project-codex"), "Project Codex")
	writeRuntimeCompatSkill(t, filepath.Join(homeDir, ".codex", "plugins", "cache", "openai-curated", "github", "63976030", "skills", "github"), "GitHub")

	cfg := &config.Config{
		DataDir:  filepath.Join(root, "data"),
		WikiDir:  filepath.Join(root, "wiki"),
		LogLevel: "error",
		Permission: config.PermissionConfig{
			Mode: "bypass",
		},
		Skills: config.SkillsConfig{
			PluginCache: config.SkillPluginCacheModeDisabled,
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

	rt, err := buildExecutionRuntime(
		context.Background(),
		cfg,
		app,
		db,
		&countingProvider{streamText: "runtime answer"},
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
	want := []string{"project-codex"}
	if got := rt.skillReg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("skillReg names = %v, want %v", got, want)
	}
}

func TestExecutionRuntimeBuildsSkillCatalogFromLegacyCommands(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))

	commandsDir := filepath.Join(root, ".claude", "commands")
	if err := os.MkdirAll(commandsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---
description: Review code
---
Review the changed files.
`
	if err := os.WriteFile(filepath.Join(commandsDir, "review-code.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

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

	rt, err := buildExecutionRuntime(
		context.Background(),
		cfg,
		app,
		db,
		&countingProvider{streamText: "runtime answer"},
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
	if got := rt.skillReg.Names(); len(got) != 1 || got[0] != "review-code" {
		t.Fatalf("skillReg names = %v, want [review-code]", got)
	}
	sk, ok := rt.skillReg.Get("review-code")
	if !ok {
		t.Fatal("review-code skill missing")
	}
	if sk.Source != "claude-command-skill" || sk.Trigger != "/review-code" {
		t.Fatalf("skill metadata = source %q trigger %q", sk.Source, sk.Trigger)
	}
}

func writeRuntimeCompatSkill(t *testing.T, dir, description string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---
description: ` + description + `
---
Do the work.
`
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}
