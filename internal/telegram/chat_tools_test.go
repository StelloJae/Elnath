package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

// --- FU-CR2b: chat-side tool_use → tool_result loop ---

type chatExecCall struct {
	name   string
	params string
}

type chatMockExecutor struct {
	mu      sync.Mutex
	calls   []chatExecCall
	results map[string]*tools.Result
	err     error
}

func newChatMockExecutor() *chatMockExecutor {
	return &chatMockExecutor{results: map[string]*tools.Result{}}
}

func (e *chatMockExecutor) setResult(name string, r *tools.Result) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.results[name] = r
}

func (e *chatMockExecutor) Execute(_ context.Context, name string, params json.RawMessage) (*tools.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, chatExecCall{name: name, params: string(params)})
	if e.err != nil {
		return nil, e.err
	}
	if r, ok := e.results[name]; ok {
		return r, nil
	}
	return &tools.Result{Output: "default ok"}, nil
}

func (e *chatMockExecutor) snapshot() []chatExecCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]chatExecCall, len(e.calls))
	copy(out, e.calls)
	return out
}

func findToolResult(req llm.ChatRequest, toolUseID string) (llm.ToolResultBlock, bool) {
	for _, msg := range req.Messages {
		for _, blk := range msg.Content {
			if tr, ok := blk.(llm.ToolResultBlock); ok && tr.ToolUseID == toolUseID {
				return tr, true
			}
		}
	}
	return llm.ToolResultBlock{}, false
}

func TestChatResponder_ExecutesToolUseAndReinjects(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{
		steps: []chatProviderStep{
			{toolUses: []chatProviderToolUse{
				{id: "tu_1", name: "web_fetch", input: `{"url":"https://example.com"}`},
			}},
			{text: "The page says hello world."},
		},
	}
	exec := newChatMockExecutor()
	exec.setResult("web_fetch", &tools.Result{Output: "<html>example body</html>"})

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolDefs:     []llm.ToolDef{{Name: "web_fetch", Description: "fetch"}},
		ToolExecutor: exec,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "fetch the page", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := exec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("executor calls = %d, want 1", len(calls))
	}
	if calls[0].name != "web_fetch" {
		t.Errorf("executor call name = %q, want web_fetch", calls[0].name)
	}
	if !strings.Contains(calls[0].params, "example.com") {
		t.Errorf("executor params = %q, want to contain example.com", calls[0].params)
	}

	reqs := provider.capturedRequests()
	if len(reqs) != 2 {
		t.Fatalf("provider stream calls = %d, want 2", len(reqs))
	}

	tr, ok := findToolResult(reqs[1], "tu_1")
	if !ok {
		t.Fatal("expected second request to carry tool_result for tu_1")
	}
	if !strings.Contains(tr.Content, "example body") {
		t.Errorf("tool_result content = %q, want to contain 'example body'", tr.Content)
	}
	if tr.IsError {
		t.Error("tool_result IsError = true, want false (executor returned success)")
	}

	last := bot.lastText()
	if !strings.Contains(last, "hello world") {
		t.Errorf("final bot text = %q, want to contain 'hello world'", last)
	}

	rs := bot.allReactions()
	if len(rs) != 2 || rs[0].emoji != "✍" || rs[1].emoji != "👍" {
		t.Errorf("reactions = %+v, want [✍, 👍] (writing then complete)", rs)
	}
}

func TestChatResponder_HandlesToolExecutionError(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{
		steps: []chatProviderStep{
			{toolUses: []chatProviderToolUse{
				{id: "tu_1", name: "web_fetch", input: `{"url":"https://broken.example"}`},
			}},
			{text: "Sorry, I couldn't fetch that page."},
		},
	}
	exec := newChatMockExecutor()
	exec.setResult("web_fetch", &tools.Result{Output: "fetch failed: HTTP 500", IsError: true})

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolDefs:     []llm.ToolDef{{Name: "web_fetch"}},
		ToolExecutor: exec,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "fetch broken", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reqs := provider.capturedRequests()
	if len(reqs) != 2 {
		t.Fatalf("provider stream calls = %d, want 2 (model gets a chance to recover)", len(reqs))
	}

	tr, ok := findToolResult(reqs[1], "tu_1")
	if !ok {
		t.Fatal("expected second request to carry tool_result for tu_1")
	}
	if !tr.IsError {
		t.Error("tool_result IsError = false, want true (executor reported error)")
	}
	if !strings.Contains(tr.Content, "HTTP 500") {
		t.Errorf("tool_result content = %q, want to contain HTTP 500", tr.Content)
	}

	last := bot.lastText()
	if !strings.Contains(last, "Sorry") {
		t.Errorf("final bot text = %q, want recovery text", last)
	}
	rs := bot.allReactions()
	if len(rs) != 2 || rs[0].emoji != "✍" || rs[1].emoji != "👍" {
		t.Errorf("reactions = %+v, want [✍, 👍] — tool fired (so ✍) and chat completed normally (so 👍) despite tool IsError", rs)
	}
}

func TestChatResponder_EnforcesMaxToolIterations(t *testing.T) {
	bot := newChatMockBot()
	steps := make([]chatProviderStep, 10)
	for i := range steps {
		steps[i] = chatProviderStep{toolUses: []chatProviderToolUse{
			{id: "tu_loop", name: "web_fetch", input: `{"url":"https://stuck.example"}`},
		}}
	}
	provider := &chatMockProvider{steps: steps}
	exec := newChatMockExecutor()

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolDefs:     []llm.ToolDef{{Name: "web_fetch"}},
		ToolExecutor: exec,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "loop", 55)
	if err == nil {
		t.Fatal("expected error from infinite tool loop, got nil")
	}
	if !strings.Contains(err.Error(), "iterations") {
		t.Errorf("error = %v, want to mention iterations", err)
	}

	if calls := provider.callCountSnapshot(); calls != maxChatToolIterations {
		t.Errorf("provider stream calls = %d, want %d (capped)", calls, maxChatToolIterations)
	}
	if execCalls := len(exec.snapshot()); execCalls != maxChatToolIterations {
		t.Errorf("executor calls = %d, want %d", execCalls, maxChatToolIterations)
	}

	rs := bot.allReactions()
	if len(rs) != 2 || rs[0].emoji != "✍" || rs[1].emoji != "😢" {
		t.Errorf("reactions = %+v, want [✍, 😢] — tool fired (so ✍) then iteration cap tripped (so 😢)", rs)
	}

	sends := bot.allSendTexts()
	foundErr := false
	for _, s := range sends {
		if strings.Contains(s, "Error") {
			foundErr = true
			break
		}
	}
	if !foundErr {
		t.Errorf("expected error message sent to user, got sends: %v", sends)
	}
}

func TestFilterChatToolDefsAllowlist(t *testing.T) {
	defs := []llm.ToolDef{
		{Name: "read_file"},
		{Name: "bash"},
		{Name: "web_fetch"},
		{Name: "write_file"},
		{Name: "edit_file"},
		{Name: "glob"},
		{Name: "grep"},
		{Name: "git"},
		{Name: "web_search"},
		{Name: "create_skill"},
	}
	got := FilterChatToolDefs(defs, DefaultChatToolAllowlist)

	wantNames := map[string]bool{
		"read_file": true, "glob": true, "grep": true,
		"web_fetch": true, "web_search": true,
	}
	if len(got) != len(wantNames) {
		t.Fatalf("filtered len = %d, want %d (got %v)", len(got), len(wantNames), toolNames(got))
	}
	for _, d := range got {
		if !wantNames[d.Name] {
			t.Errorf("unexpected tool in filtered set: %q", d.Name)
		}
	}

	for _, banned := range []string{"bash", "write_file", "edit_file", "git", "create_skill"} {
		for _, d := range got {
			if d.Name == banned {
				t.Errorf("destructive tool %q leaked through allowlist", banned)
			}
		}
	}
}

func TestFilterChatToolDefs_EmptyInputs(t *testing.T) {
	if got := FilterChatToolDefs(nil, DefaultChatToolAllowlist); got != nil {
		t.Errorf("FilterChatToolDefs(nil, ...) = %v, want nil", got)
	}
	defs := []llm.ToolDef{{Name: "read_file"}}
	if got := FilterChatToolDefs(defs, nil); got != nil {
		t.Errorf("FilterChatToolDefs(defs, nil) = %v, want nil", got)
	}
}

func toolNames(defs []llm.ToolDef) []string {
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.Name
	}
	return out
}

// --- FU-PromptNow + FU-ChatToolGuide: chat system prompt anchors ---

func TestChatResponder_PrependsCurrentTimeHeader(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	fixedNow := time.Date(2026, 4, 21, 0, 50, 0, 0, time.UTC)
	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatNow(func() time.Time { return fixedNow }))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if !strings.Contains(req.System, "현재 시간 (KST):") {
		t.Errorf("System prompt missing time header: %q", req.System)
	}
	// 00:50 UTC = 09:50 KST
	if !strings.Contains(req.System, "2026-04-21 09:50") {
		t.Errorf("System prompt missing KST timestamp 2026-04-21 09:50: %q", req.System)
	}
}

func TestChatResponder_PrependsToolGuideWhenLoopActive(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	exec := newChatMockExecutor()
	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolDefs:     []llm.ToolDef{{Name: "web_fetch"}},
		ToolExecutor: exec,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if !strings.Contains(req.System, "## 도구 사용 지침") {
		t.Errorf("System prompt missing tool guide section header when loop is active: %q", req.System)
	}
	if !strings.Contains(req.System, "web_fetch") {
		t.Errorf("Tool guide should name allowlisted tools (web_fetch): %q", req.System)
	}
}

// TestChatResponder_ToolGuideContainsStrongInstructionBlock asserts the
// structured markers introduced by FU-ChatToolGuideStrong: mandatory-call
// trigger vocabulary, the numbered execution rules, and the parallel-tool_use
// cue. These specific anchors guard against silent regressions to a weaker
// one-liner nudge.
func TestChatResponder_ToolGuideContainsStrongInstructionBlock(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	exec := newChatMockExecutor()
	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolDefs:     []llm.ToolDef{{Name: "web_fetch"}, {Name: "web_search"}},
		ToolExecutor: exec,
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	wantMarkers := []string{
		"반드시 도구를 호출",
		"\"지금/오늘/최근/최신\"",
		"실행 규칙:",
		"병렬 tool_use",
		"web_search",
		"read_file",
		"glob",
		"grep",
	}
	for _, m := range wantMarkers {
		if !strings.Contains(req.System, m) {
			t.Errorf("strong instruction block missing marker %q in System prompt:\n%s", m, req.System)
		}
	}
}

func TestChatResponder_OmitsToolGuideWhenNoExecutor(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	cr := NewChatResponder(provider, bot, "chat-42", nil)

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if strings.Contains(req.System, "## 도구 사용 지침") {
		t.Errorf("System prompt should not include tool guide when no executor wired: %q", req.System)
	}
	if !strings.Contains(req.System, "현재 시간 (KST):") {
		t.Errorf("Time header should still be present even without tool loop: %q", req.System)
	}
}

func TestChatResponder_OmitsToolGuideWhenToolDefsEmpty(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	exec := newChatMockExecutor()
	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolExecutor: exec,
		// ToolDefs intentionally empty
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if strings.Contains(req.System, "## 도구 사용 지침") {
		t.Errorf("Tool guide should be skipped when ToolDefs empty: %q", req.System)
	}
}

// --- FU-ChatToolResultCap (P2): tool_result content cap before history append ---

func TestCapChatToolResult(t *testing.T) {
	t.Run("below cap passes through", func(t *testing.T) {
		in := strings.Repeat("a", chatToolResultCap-100)
		if got := capChatToolResult(in); got != in {
			t.Errorf("below-cap input was modified: len=%d", len(got))
		}
	})
	t.Run("equal to cap passes through", func(t *testing.T) {
		in := strings.Repeat("a", chatToolResultCap)
		if got := capChatToolResult(in); got != in {
			t.Errorf("at-cap input was modified: len=%d", len(got))
		}
	})
	t.Run("above cap truncates and marks", func(t *testing.T) {
		in := strings.Repeat("a", chatToolResultCap*2+123)
		got := capChatToolResult(in)
		if len(got) <= chatToolResultCap {
			t.Errorf("truncated output len=%d should exceed cap (cap+marker)", len(got))
		}
		if !strings.HasPrefix(got, strings.Repeat("a", chatToolResultCap)) {
			t.Error("truncated prefix did not preserve first cap bytes")
		}
		if !strings.Contains(got, "중략") {
			t.Errorf("marker missing: %q", got[len(got)-120:])
		}
		wantOrig := fmt.Sprintf("원본 %d bytes", chatToolResultCap*2+123)
		if !strings.Contains(got, wantOrig) {
			t.Errorf("marker should name original size %q; tail=%q", wantOrig, got[len(got)-120:])
		}
	})
	t.Run("empty input passes through", func(t *testing.T) {
		if got := capChatToolResult(""); got != "" {
			t.Errorf("empty input was modified: %q", got)
		}
	})
}

func TestChatResponder_CapsLargeToolResultBeforeReinjection(t *testing.T) {
	bigBody := strings.Repeat("x", chatToolResultCap*3) // 192 KiB
	bot := newChatMockBot()
	provider := &chatMockProvider{
		steps: []chatProviderStep{
			{toolUses: []chatProviderToolUse{
				{id: "tu_big", name: "web_fetch", input: `{"url":"https://big.example"}`},
			}},
			{text: "요약 답변"},
		},
	}
	exec := newChatMockExecutor()
	exec.setResult("web_fetch", &tools.Result{Output: bigBody})

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolDefs:     []llm.ToolDef{{Name: "web_fetch"}},
		ToolExecutor: exec,
	}))

	if err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "fetch big", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reqs := provider.capturedRequests()
	if len(reqs) < 2 {
		t.Fatalf("provider stream calls = %d, want >= 2 (tool-loop path)", len(reqs))
	}
	tr, ok := findToolResult(reqs[1], "tu_big")
	if !ok {
		t.Fatal("expected second request to carry tool_result for tu_big")
	}
	if len(tr.Content) >= len(bigBody) {
		t.Errorf("tool_result content not capped: got %d bytes, input %d bytes", len(tr.Content), len(bigBody))
	}
	if !strings.Contains(tr.Content, "중략") {
		t.Error("tool_result content missing truncation marker")
	}
}

// --- FU-ChatProgressNote: "doing X" note streamed before each tool call ---

func TestChatToolProgressNote_FormatsByTool(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantSubs []string
	}{
		{"web_search", `{"query":"today popular stocks"}`, []string{"🔍", "web_search", "today popular stocks"}},
		{"web_fetch", `{"url":"https://naver.com"}`, []string{"📄", "web_fetch", "naver.com"}},
		{"read_file", `{"path":"/etc/hosts"}`, []string{"📄", "read_file", "/etc/hosts"}},
		{"glob", `{"pattern":"**/*.go"}`, []string{"🔎", "glob", "**/*.go"}},
		{"grep", `{"pattern":"TODO"}`, []string{"🔎", "grep", "TODO"}},
		{"web_fetch", `{}`, []string{"📄", "web_fetch", "URL"}},
		{"web_fetch", `not-json`, []string{"📄", "web_fetch", "URL"}},
		{"web_search", "", []string{"🔍", "web_search"}},
		{"unknown_tool", `{}`, []string{"🔧", "unknown_tool"}},
	}
	for _, tc := range cases {
		got := chatToolProgressNote(tc.name, tc.input)
		if got == "" {
			t.Errorf("chatToolProgressNote(%q, %q) returned empty", tc.name, tc.input)
			continue
		}
		for _, want := range tc.wantSubs {
			if !strings.Contains(got, want) {
				t.Errorf("chatToolProgressNote(%q, %q) = %q; missing %q", tc.name, tc.input, got, want)
			}
		}
	}
}

func TestChatResponder_EmitsProgressNoteBeforeToolExecution(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{
		steps: []chatProviderStep{
			{toolUses: []chatProviderToolUse{
				{id: "tu_1", name: "web_fetch", input: `{"url":"https://example.com/foo"}`},
			}},
			{text: "답변 완료."},
		},
	}
	exec := newChatMockExecutor()
	exec.setResult("web_fetch", &tools.Result{Output: "body"})

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolDefs:     []llm.ToolDef{{Name: "web_fetch"}},
		ToolExecutor: exec,
	}))

	if err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "fetch it", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	last := bot.lastText()
	for _, want := range []string{"web_fetch", "example.com/foo", "답변 완료"} {
		if !strings.Contains(last, want) {
			t.Errorf("final bot text missing %q — got:\n%s", want, last)
		}
	}
}

// --- FU-TgToolReaction: ✍ reaction during chat tool execution ---

// TestChatResponder_EntryWritingReactionShownEvenWithoutTool asserts that
// ✍ is set at chat-path entry (FU-ChatEntryWorking / P1) regardless of
// whether tool_use fires later in the turn. Audit 2026-04-21 found 87% of
// chat_direct turns never reach the tool loop; without entry-side ✍ those
// turns look like 👀 → silence → 👍 to the partner.
func TestChatResponder_EntryWritingReactionShownEvenWithoutTool(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{
		steps: []chatProviderStep{
			{text: "direct answer, no tools."},
		},
	}
	exec := newChatMockExecutor()

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolDefs:     []llm.ToolDef{{Name: "web_fetch"}},
		ToolExecutor: exec,
	}))

	if err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rs := bot.allReactions()
	if len(rs) != 2 || rs[0].emoji != "✍" || rs[1].emoji != "👍" {
		t.Errorf("reactions = %+v, want [✍, 👍] (entry-side ✍ then terminal 👍 even with no tool)", rs)
	}
}

// TestChatResponder_WritingReactionSetOnlyOnceAcrossIterations asserts the ✍
// is sent exactly once even when the model emits tool_use across multiple
// iterations. Spamming SetReaction every iteration would be wasteful and
// could look flickery to partners.
func TestChatResponder_WritingReactionSetOnlyOnceAcrossIterations(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{
		steps: []chatProviderStep{
			{toolUses: []chatProviderToolUse{
				{id: "tu_1", name: "web_fetch", input: `{"url":"https://a"}`},
			}},
			{toolUses: []chatProviderToolUse{
				{id: "tu_2", name: "web_search", input: `{"q":"b"}`},
			}},
			{text: "summary"},
		},
	}
	exec := newChatMockExecutor()
	exec.setResult("web_fetch", &tools.Result{Output: "a"})
	exec.setResult("web_search", &tools.Result{Output: "b"})

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolDefs:     []llm.ToolDef{{Name: "web_fetch"}, {Name: "web_search"}},
		ToolExecutor: exec,
	}))

	if err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "multi", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rs := bot.allReactions()
	writingCount := 0
	for _, r := range rs {
		if r.emoji == "✍" {
			writingCount++
		}
	}
	if writingCount != 1 {
		t.Errorf("✍ reaction count = %d, want 1 (debounced across iterations). reactions = %+v", writingCount, rs)
	}
	if len(rs) == 0 || rs[len(rs)-1].emoji != "👍" {
		t.Errorf("final reaction = %+v, want ending with 👍", rs)
	}
}

// --- FU-ChatMaxTokens: per-step max_tokens is the expanded constant ---

func TestChatResponder_UsesExpandedMaxTokens_LegacyPath(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "ok"}
	cr := NewChatResponder(provider, bot, "chat-42", nil)

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := provider.capturedRequest(t)
	if req.MaxTokens != chatMaxTokens {
		t.Errorf("legacy path MaxTokens = %d, want %d (chatMaxTokens)", req.MaxTokens, chatMaxTokens)
	}
	if chatMaxTokens < 4096 {
		t.Errorf("chatMaxTokens = %d, want >= 4096 (FU-ChatMaxTokens floor)", chatMaxTokens)
	}
}

func TestChatResponder_UsesExpandedMaxTokens_ToolLoopPath(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{
		steps: []chatProviderStep{
			{toolUses: []chatProviderToolUse{
				{id: "tu_1", name: "web_fetch", input: `{"url":"https://example.com"}`},
			}},
			{text: "done"},
		},
	}
	exec := newChatMockExecutor()
	exec.setResult("web_fetch", &tools.Result{Output: "body"})

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolDefs:     []llm.ToolDef{{Name: "web_fetch"}},
		ToolExecutor: exec,
	}))

	if err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "fetch", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reqs := provider.capturedRequests()
	if len(reqs) < 2 {
		t.Fatalf("provider stream calls = %d, want >= 2 (tool-loop path)", len(reqs))
	}
	for i, r := range reqs {
		if r.MaxTokens != chatMaxTokens {
			t.Errorf("tool-loop step %d MaxTokens = %d, want %d", i, r.MaxTokens, chatMaxTokens)
		}
	}
}

// --- FU-ChatObs: chat outcome carries iterations + tool stats ---

func TestChatResponder_OutcomeRecordsToolLoopStats(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{
		steps: []chatProviderStep{
			{toolUses: []chatProviderToolUse{
				{id: "tu_1", name: "web_fetch", input: `{"url":"https://example.com"}`},
			}},
			{text: "All done."},
		},
	}
	exec := newChatMockExecutor()
	exec.setResult("web_fetch", &tools.Result{Output: "fetched"})
	store := &mockOutcomeAppender{}

	cr := NewChatResponder(provider, bot, "chat-42", nil,
		WithOutcomeStore(store),
		WithChatPipeline(ChatPipelineDeps{
			ToolDefs:     []llm.ToolDef{{Name: "web_fetch"}},
			ToolExecutor: exec,
		}),
	)

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "fetch", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	records := store.snapshot()
	if len(records) != 1 {
		t.Fatalf("outcome records = %d, want 1", len(records))
	}
	r := records[0]
	if r.Iterations != 2 {
		t.Errorf("Iterations = %d, want 2 (1 tool step + 1 final text step)", r.Iterations)
	}
	if r.MaxIterations != maxChatToolIterations {
		t.Errorf("MaxIterations = %d, want %d", r.MaxIterations, maxChatToolIterations)
	}
	if len(r.ToolStats) != 1 {
		t.Fatalf("ToolStats len = %d, want 1", len(r.ToolStats))
	}
	if r.ToolStats[0].Name != "web_fetch" || r.ToolStats[0].Calls != 1 {
		t.Errorf("ToolStats[0] = %+v, want {web_fetch, calls:1}", r.ToolStats[0])
	}
	if r.ToolStats[0].Errors != 0 {
		t.Errorf("ToolStats[0].Errors = %d, want 0", r.ToolStats[0].Errors)
	}
}

func TestChatResponder_OutcomeRecordsToolErrorCount(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{
		steps: []chatProviderStep{
			{toolUses: []chatProviderToolUse{
				{id: "tu_1", name: "web_fetch", input: `{"url":"https://x"}`},
			}},
			{text: "sorry"},
		},
	}
	exec := newChatMockExecutor()
	exec.setResult("web_fetch", &tools.Result{Output: "boom", IsError: true})
	store := &mockOutcomeAppender{}

	cr := NewChatResponder(provider, bot, "chat-42", nil,
		WithOutcomeStore(store),
		WithChatPipeline(ChatPipelineDeps{
			ToolDefs:     []llm.ToolDef{{Name: "web_fetch"}},
			ToolExecutor: exec,
		}),
	)

	if err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "x", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	records := store.snapshot()
	if len(records) != 1 || len(records[0].ToolStats) != 1 {
		t.Fatalf("expected 1 outcome with 1 ToolStats entry, got %+v", records)
	}
	if records[0].ToolStats[0].Errors != 1 {
		t.Errorf("ToolStats[0].Errors = %d, want 1", records[0].ToolStats[0].Errors)
	}
}

func TestChatResponder_OutcomeRecordsLegacyIterationOne(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "hi"}
	store := &mockOutcomeAppender{}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithOutcomeStore(store))

	if err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	records := store.snapshot()
	if len(records) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(records))
	}
	if records[0].Iterations != 1 {
		t.Errorf("legacy Iterations = %d, want 1", records[0].Iterations)
	}
	if records[0].ToolStats != nil {
		t.Errorf("legacy ToolStats = %+v, want nil", records[0].ToolStats)
	}
}

func TestChatResponder_LegacyPathUnchangedWhenExecutorMissing(t *testing.T) {
	bot := newChatMockBot()
	provider := &chatMockProvider{response: "plain reply"}

	cr := NewChatResponder(provider, bot, "chat-42", nil, WithChatPipeline(ChatPipelineDeps{
		ToolDefs: []llm.ToolDef{{Name: "web_fetch"}},
	}))

	err := cr.Respond(context.Background(), identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}, "hi", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reqs := provider.capturedRequests()
	if len(reqs) != 1 {
		t.Fatalf("provider stream calls = %d, want 1 (legacy single-stream path)", len(reqs))
	}
	if len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "web_fetch" {
		t.Errorf("Tools = %v, want [web_fetch] forwarded", reqs[0].Tools)
	}

	for _, msg := range reqs[0].Messages {
		for _, blk := range msg.Content {
			if _, ok := blk.(llm.ToolResultBlock); ok {
				t.Error("legacy path emitted tool_result, want none")
			}
		}
	}

	last := bot.lastText()
	if !strings.Contains(last, "plain reply") {
		t.Errorf("final bot text = %q, want 'plain reply'", last)
	}
}
