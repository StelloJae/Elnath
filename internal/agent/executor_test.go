package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

type fakeTool struct {
	name        string
	safe        bool
	reversible  bool
	scope       tools.ToolScope
	cancelOnErr bool
	sleep       time.Duration
	failWith    error
	failResult  bool

	mu         sync.Mutex
	startedAt  time.Time
	finishedAt time.Time
}

func (t *fakeTool) Name() string                           { return t.name }
func (t *fakeTool) Description() string                    { return t.name }
func (t *fakeTool) Schema() json.RawMessage                { return json.RawMessage(`{"type":"object"}`) }
func (t *fakeTool) IsConcurrencySafe(json.RawMessage) bool { return t.safe }
func (t *fakeTool) Reversible() bool                       { return t.reversible }
func (t *fakeTool) Scope(json.RawMessage) tools.ToolScope  { return t.scope }
func (t *fakeTool) ShouldCancelSiblingsOnError() bool      { return t.cancelOnErr }

func (t *fakeTool) Execute(ctx context.Context, _ json.RawMessage) (*tools.Result, error) {
	t.mu.Lock()
	t.startedAt = time.Now()
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.finishedAt = time.Now()
		t.mu.Unlock()
	}()

	if t.sleep > 0 {
		select {
		case <-time.After(t.sleep):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if t.failWith != nil {
		return nil, t.failWith
	}
	if t.failResult {
		return tools.ErrorResult(t.name + " failed"), nil
	}
	return tools.SuccessResult(t.name + " ok"), nil
}

func (t *fakeTool) interval() (time.Time, time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.startedAt, t.finishedAt
}

type instrumentedTool struct {
	inner tools.Tool
	sleep time.Duration

	mu        sync.Mutex
	intervals [][2]time.Time
}

func (t *instrumentedTool) Name() string            { return t.inner.Name() }
func (t *instrumentedTool) Description() string     { return t.inner.Description() }
func (t *instrumentedTool) Schema() json.RawMessage { return t.inner.Schema() }
func (t *instrumentedTool) IsConcurrencySafe(p json.RawMessage) bool {
	return t.inner.IsConcurrencySafe(p)
}
func (t *instrumentedTool) Reversible() bool                        { return t.inner.Reversible() }
func (t *instrumentedTool) Scope(p json.RawMessage) tools.ToolScope { return t.inner.Scope(p) }
func (t *instrumentedTool) ShouldCancelSiblingsOnError() bool {
	return t.inner.ShouldCancelSiblingsOnError()
}

func (t *instrumentedTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	start := time.Now()
	defer func() {
		t.mu.Lock()
		t.intervals = append(t.intervals, [2]time.Time{start, time.Now()})
		t.mu.Unlock()
	}()

	if t.sleep > 0 {
		select {
		case <-time.After(t.sleep):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return t.inner.Execute(ctx, params)
}

func (t *instrumentedTool) intervalsSnapshot() [][2]time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][2]time.Time, len(t.intervals))
	copy(out, t.intervals)
	return out
}

func TestPartition_AllReadsParallel(t *testing.T) {
	root := t.TempDir()
	r1 := &fakeTool{name: "read_a", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(root, "a")}}, sleep: 50 * time.Millisecond}
	r2 := &fakeTool{name: "read_b", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(root, "b")}}, sleep: 50 * time.Millisecond}
	r3 := &fakeTool{name: "read_c", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(root, "c")}}, sleep: 50 * time.Millisecond}

	reg := tools.NewRegistry()
	reg.Register(r1)
	reg.Register(r2)
	reg.Register(r3)

	start := time.Now()
	messages, err := newTestAgent(reg).executeTools(context.Background(), nil, []llm.ToolUseBlock{
		{ID: "r1", Name: r1.Name(), Input: json.RawMessage(`{}`)},
		{ID: "r2", Name: r2.Name(), Input: json.RawMessage(`{}`)},
		{ID: "r3", Name: r3.Name(), Input: json.RawMessage(`{}`)},
	}, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("executeTools: %v", err)
	}
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("wallclock = %s, want < 100ms", elapsed)
	}
	assertToolResultIDs(t, messages, []string{"r1", "r2", "r3"})
	assertStartWindow(t, 20*time.Millisecond, r1, r2, r3)
}

func TestPartition_WritesDifferentPaths_Parallel(t *testing.T) {
	root := t.TempDir()
	w1 := &fakeTool{name: "write_a", scope: tools.ToolScope{WritePaths: []string{filepath.Join(root, "a")}, Persistent: true}, sleep: 50 * time.Millisecond}
	w2 := &fakeTool{name: "write_b", scope: tools.ToolScope{WritePaths: []string{filepath.Join(root, "b")}, Persistent: true}, sleep: 50 * time.Millisecond}
	w3 := &fakeTool{name: "write_c", scope: tools.ToolScope{WritePaths: []string{filepath.Join(root, "c")}, Persistent: true}, sleep: 50 * time.Millisecond}

	reg := tools.NewRegistry()
	reg.Register(w1)
	reg.Register(w2)
	reg.Register(w3)

	start := time.Now()
	messages, err := newTestAgent(reg).executeTools(context.Background(), nil, []llm.ToolUseBlock{
		{ID: "w1", Name: w1.Name(), Input: json.RawMessage(`{}`)},
		{ID: "w2", Name: w2.Name(), Input: json.RawMessage(`{}`)},
		{ID: "w3", Name: w3.Name(), Input: json.RawMessage(`{}`)},
	}, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("executeTools: %v", err)
	}
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("wallclock = %s, want < 100ms", elapsed)
	}
	assertToolResultIDs(t, messages, []string{"w1", "w2", "w3"})
}

func TestPartition_WritesSamePath_Serial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	w1 := &fakeTool{name: "write_one", scope: tools.ToolScope{WritePaths: []string{path}, Persistent: true}, sleep: 50 * time.Millisecond}
	w2 := &fakeTool{name: "write_two", scope: tools.ToolScope{WritePaths: []string{path}, Persistent: true}, sleep: 50 * time.Millisecond}

	reg := tools.NewRegistry()
	reg.Register(w1)
	reg.Register(w2)

	start := time.Now()
	messages, err := newTestAgent(reg).executeTools(context.Background(), nil, []llm.ToolUseBlock{
		{ID: "w1", Name: w1.Name(), Input: json.RawMessage(`{}`)},
		{ID: "w2", Name: w2.Name(), Input: json.RawMessage(`{}`)},
	}, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("executeTools: %v", err)
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("wallclock = %s, want >= 100ms", elapsed)
	}
	assertToolResultIDs(t, messages, []string{"w1", "w2"})
	assertIntervalsDisjoint(t, w1, w2)
}

func TestPartition_BashBlocksReads(t *testing.T) {
	workDir := t.TempDir()
	bash := &fakeTool{name: "bash_like", scope: tools.ToolScope{WritePaths: []string{workDir}, Persistent: true}, sleep: 30 * time.Millisecond}
	r1 := &fakeTool{name: "read_a", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(workDir, "a")}}, sleep: 30 * time.Millisecond}
	r2 := &fakeTool{name: "read_b", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(workDir, "b")}}, sleep: 30 * time.Millisecond}

	reg := tools.NewRegistry()
	reg.Register(bash)
	reg.Register(r1)
	reg.Register(r2)

	start := time.Now()
	messages, err := newTestAgent(reg).executeTools(context.Background(), nil, []llm.ToolUseBlock{
		{ID: "bash", Name: bash.Name(), Input: json.RawMessage(`{}`)},
		{ID: "r1", Name: r1.Name(), Input: json.RawMessage(`{}`)},
		{ID: "r2", Name: r2.Name(), Input: json.RawMessage(`{}`)},
	}, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("executeTools: %v", err)
	}
	if elapsed < 60*time.Millisecond || elapsed >= 90*time.Millisecond {
		t.Fatalf("wallclock = %s, want bash batch then parallel reads", elapsed)
	}
	assertToolResultIDs(t, messages, []string{"bash", "r1", "r2"})
	bashStart, bashFinish := bash.interval()
	r1Start, _ := r1.interval()
	r2Start, _ := r2.interval()
	if r1Start.Before(bashFinish) || r2Start.Before(bashFinish) {
		t.Fatalf("read batch started before bash finished: bash=%s..%s r1=%s r2=%s", bashStart, bashFinish, r1Start, r2Start)
	}
}

func TestPartition_MixedOrder_PreservesResults(t *testing.T) {
	root := t.TempDir()
	r1 := &fakeTool{name: "r1", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(root, "r1")}}, sleep: 10 * time.Millisecond}
	w1 := &fakeTool{name: "w1", scope: tools.ToolScope{WritePaths: []string{filepath.Join(root, "a")}, Persistent: true}, sleep: 10 * time.Millisecond}
	r2 := &fakeTool{name: "r2", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(root, "a", "child")}}, sleep: 10 * time.Millisecond}
	r3 := &fakeTool{name: "r3", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(root, "b", "child")}}, sleep: 10 * time.Millisecond}
	w2 := &fakeTool{name: "w2", scope: tools.ToolScope{WritePaths: []string{filepath.Join(root, "b")}, Persistent: true}, sleep: 10 * time.Millisecond}
	r4 := &fakeTool{name: "r4", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(root, "r4")}}, sleep: 10 * time.Millisecond}

	reg := tools.NewRegistry()
	for _, tool := range []*fakeTool{r1, w1, r2, r3, w2, r4} {
		reg.Register(tool)
	}

	calls := []llm.ToolUseBlock{
		{ID: "R1", Name: r1.Name(), Input: json.RawMessage(`{}`)},
		{ID: "W1", Name: w1.Name(), Input: json.RawMessage(`{}`)},
		{ID: "R2", Name: r2.Name(), Input: json.RawMessage(`{}`)},
		{ID: "R3", Name: r3.Name(), Input: json.RawMessage(`{}`)},
		{ID: "W2", Name: w2.Name(), Input: json.RawMessage(`{}`)},
		{ID: "R4", Name: r4.Name(), Input: json.RawMessage(`{}`)},
	}

	messages, err := newTestAgent(reg).executeTools(context.Background(), nil, calls, nil)
	if err != nil {
		t.Fatalf("executeTools: %v", err)
	}
	assertToolResultIDs(t, messages, []string{"R1", "W1", "R2", "R3", "W2", "R4"})
	for _, block := range toolResultBlocks(t, messages) {
		if block.IsError {
			t.Fatalf("tool result %s unexpectedly errored", block.ToolUseID)
		}
	}
}

func TestCancel_BashFailureCancelsSiblings(t *testing.T) {
	bash := &fakeTool{name: "bash_like", scope: tools.ToolScope{WritePaths: []string{filepath.Join(t.TempDir(), "x")}, Persistent: true}, cancelOnErr: true, sleep: 5 * time.Millisecond, failWith: errors.New("boom")}
	read := &fakeTool{name: "slow_read", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{"/tmp/y"}}, sleep: 500 * time.Millisecond}

	reg := tools.NewRegistry()
	reg.Register(bash)
	reg.Register(read)

	start := time.Now()
	_, err := newTestAgent(reg).executeTools(context.Background(), nil, []llm.ToolUseBlock{
		{ID: "bash", Name: bash.Name(), Input: json.RawMessage(`{}`)},
		{ID: "read", Name: read.Name(), Input: json.RawMessage(`{}`)},
	}, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected fatal error, got nil")
	}
	if !errors.Is(err, core.ErrToolExecution) {
		t.Fatalf("error = %v, want wrapping core.ErrToolExecution", err)
	}
	if elapsed >= 50*time.Millisecond {
		t.Fatalf("wallclock = %s, want < 50ms", elapsed)
	}
	readStart, readFinish := read.interval()
	if readFinish.Sub(readStart) >= 500*time.Millisecond {
		t.Fatalf("slow read ran full duration: %s", readFinish.Sub(readStart))
	}
}

func TestCancel_RealBashFailureCancelsSiblings(t *testing.T) {
	workDir := t.TempDir()
	bashTool := tools.NewBashTool(tools.NewPathGuard(workDir, nil))
	read := &fakeTool{name: "slow_read", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(t.TempDir(), "outside")}}, sleep: 500 * time.Millisecond}

	reg := tools.NewRegistry()
	reg.Register(bashTool)
	reg.Register(read)

	start := time.Now()
	_, err := newTestAgent(reg).executeTools(context.Background(), nil, []llm.ToolUseBlock{
		{ID: "bash", Name: bashTool.Name(), Input: json.RawMessage(`{"command":"false","timeout_ms":1000}`)},
		{ID: "read", Name: read.Name(), Input: json.RawMessage(`{}`)},
	}, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected fatal error, got nil")
	}
	if !errors.Is(err, core.ErrToolExecution) {
		t.Fatalf("error = %v, want wrapping core.ErrToolExecution", err)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("wallclock = %s, want real bash failure to cancel sibling", elapsed)
	}
	readStart, readFinish := read.interval()
	if readFinish.Sub(readStart) >= 500*time.Millisecond {
		t.Fatalf("slow read ran full duration: %s", readFinish.Sub(readStart))
	}
}

func TestCancel_ReadFailureDoesNotCancelSiblings(t *testing.T) {
	fail := &fakeTool{name: "fail_read", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(t.TempDir(), "a")}}, sleep: 5 * time.Millisecond, failWith: errors.New("nope")}
	slow := &fakeTool{name: "slow_read", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(t.TempDir(), "b")}}, sleep: 80 * time.Millisecond}

	reg := tools.NewRegistry()
	reg.Register(fail)
	reg.Register(slow)

	start := time.Now()
	messages, err := newTestAgent(reg).executeTools(context.Background(), nil, []llm.ToolUseBlock{
		{ID: "fail", Name: fail.Name(), Input: json.RawMessage(`{}`)},
		{ID: "slow", Name: slow.Name(), Input: json.RawMessage(`{}`)},
	}, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("executeTools: %v", err)
	}
	blocks := toolResultBlocks(t, messages)
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2", len(blocks))
	}
	if !blocks[0].IsError || blocks[1].IsError {
		t.Fatalf("unexpected result states: %+v", blocks)
	}
	if elapsed < 80*time.Millisecond {
		t.Fatalf("wallclock = %s, want slow sibling to keep running", elapsed)
	}
	slowStart, slowFinish := slow.interval()
	if slowFinish.Sub(slowStart) < 70*time.Millisecond {
		t.Fatalf("slow read was canceled early: %s", slowFinish.Sub(slowStart))
	}
}

func TestPartition_ConservativeScopeSerializes(t *testing.T) {
	guard := tools.NewPathGuard(t.TempDir(), nil)
	badWrite := &instrumentedTool{inner: tools.NewWriteTool(guard), sleep: 40 * time.Millisecond}
	read := &fakeTool{name: "slow_read", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(t.TempDir(), "outside")}}, sleep: 40 * time.Millisecond}

	reg := tools.NewRegistry()
	reg.Register(badWrite)
	reg.Register(read)

	start := time.Now()
	messages, err := newTestAgent(reg).executeTools(context.Background(), nil, []llm.ToolUseBlock{
		{ID: "write", Name: badWrite.Name(), Input: json.RawMessage("{not valid")},
		{ID: "read", Name: read.Name(), Input: json.RawMessage(`{}`)},
	}, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("executeTools: %v", err)
	}
	if elapsed < 80*time.Millisecond {
		t.Fatalf("wallclock = %s, want conservative scope to force serialization", elapsed)
	}
	blocks := toolResultBlocks(t, messages)
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2", len(blocks))
	}
	if !blocks[0].IsError || blocks[1].IsError {
		t.Fatalf("unexpected result states: %+v", blocks)
	}
	intervals := badWrite.intervalsSnapshot()
	if len(intervals) != 1 {
		t.Fatalf("len(intervals) = %d, want 1", len(intervals))
	}
	readStart, readFinish := read.interval()
	if !intervalsDisjoint(intervals[0], [2]time.Time{readStart, readFinish}) {
		t.Fatalf("conservative write overlapped with read: write=%v read=%s..%s", intervals[0], readStart, readFinish)
	}
}

func TestPartition_RaceDetector(t *testing.T) {
	for i := 0; i < 200; i++ {
		root := t.TempDir()
		r1 := &fakeTool{name: "r1", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(root, "r1")}}, sleep: time.Millisecond}
		w1 := &fakeTool{name: "w1", scope: tools.ToolScope{WritePaths: []string{filepath.Join(root, "a")}, Persistent: true}, sleep: time.Millisecond}
		r2 := &fakeTool{name: "r2", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(root, "a", "child")}}, sleep: time.Millisecond}
		r3 := &fakeTool{name: "r3", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(root, "b", "child")}}, sleep: time.Millisecond}
		w2 := &fakeTool{name: "w2", scope: tools.ToolScope{WritePaths: []string{filepath.Join(root, "b")}, Persistent: true}, sleep: time.Millisecond}
		r4 := &fakeTool{name: "r4", safe: true, reversible: true, scope: tools.ToolScope{ReadPaths: []string{filepath.Join(root, "r4")}}, sleep: time.Millisecond}

		reg := tools.NewRegistry()
		for _, tool := range []*fakeTool{r1, w1, r2, r3, w2, r4} {
			reg.Register(tool)
		}

		messages, err := newTestAgent(reg).executeTools(context.Background(), nil, []llm.ToolUseBlock{
			{ID: "R1", Name: r1.Name(), Input: json.RawMessage(`{}`)},
			{ID: "W1", Name: w1.Name(), Input: json.RawMessage(`{}`)},
			{ID: "R2", Name: r2.Name(), Input: json.RawMessage(`{}`)},
			{ID: "R3", Name: r3.Name(), Input: json.RawMessage(`{}`)},
			{ID: "W2", Name: w2.Name(), Input: json.RawMessage(`{}`)},
			{ID: "R4", Name: r4.Name(), Input: json.RawMessage(`{}`)},
		}, nil)
		if err != nil {
			t.Fatalf("iteration %d executeTools: %v", i, err)
		}
		assertToolResultIDs(t, messages, []string{"R1", "W1", "R2", "R3", "W2", "R4"})
	}
}

func TestWikiWriteSerializes_Integration(t *testing.T) {
	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	wrapped := &instrumentedTool{inner: wiki.NewWikiWriteTool(store), sleep: 40 * time.Millisecond}

	reg := tools.NewRegistry()
	reg.Register(wrapped)

	messages, err := newTestAgent(reg).executeTools(context.Background(), nil, []llm.ToolUseBlock{
		{ID: "w1", Name: wrapped.Name(), Input: json.RawMessage(`{"path":"concepts/foo.md","title":"Foo","content":"one","type":"concept"}`)},
		{ID: "w2", Name: wrapped.Name(), Input: json.RawMessage(`{"path":"concepts/foo.md","title":"Foo","content":"two","type":"concept"}`)},
	}, nil)
	if err != nil {
		t.Fatalf("executeTools: %v", err)
	}
	assertToolResultIDs(t, messages, []string{"w1", "w2"})
	for _, block := range toolResultBlocks(t, messages) {
		if block.IsError {
			t.Fatalf("wiki_write result %s unexpectedly errored", block.ToolUseID)
		}
	}
	intervals := wrapped.intervalsSnapshot()
	if len(intervals) != 2 {
		t.Fatalf("len(intervals) = %d, want 2", len(intervals))
	}
	if !intervalsDisjoint(intervals[0], intervals[1]) {
		t.Fatalf("wiki_write intervals overlapped: %v", intervals)
	}
	page, err := store.Read("concepts/foo.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if strings.TrimSpace(page.Content) != "two" {
		t.Fatalf("final page content = %q, want %q", page.Content, "two")
	}
}

func TestWikiReadWriteSamePath_Serial(t *testing.T) {
	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Upsert(&wiki.Page{Path: "concepts/foo.md", Title: "Foo", Type: wiki.PageTypeConcept, Content: "one"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	readTool := &instrumentedTool{inner: wiki.NewWikiReadTool(store), sleep: 40 * time.Millisecond}
	writeTool := &instrumentedTool{inner: wiki.NewWikiWriteTool(store), sleep: 40 * time.Millisecond}

	reg := tools.NewRegistry()
	reg.Register(readTool)
	reg.Register(writeTool)

	messages, err := newTestAgent(reg).executeTools(context.Background(), nil, []llm.ToolUseBlock{
		{ID: "read", Name: readTool.Name(), Input: json.RawMessage(`{"path":"concepts/foo.md"}`)},
		{ID: "write", Name: writeTool.Name(), Input: json.RawMessage(`{"path":"concepts/foo.md","title":"Foo","content":"two","type":"concept"}`)},
	}, nil)
	if err != nil {
		t.Fatalf("executeTools: %v", err)
	}
	assertToolResultIDs(t, messages, []string{"read", "write"})
	readIntervals := readTool.intervalsSnapshot()
	writeIntervals := writeTool.intervalsSnapshot()
	if len(readIntervals) != 1 || len(writeIntervals) != 1 {
		t.Fatalf("unexpected intervals: read=%v write=%v", readIntervals, writeIntervals)
	}
	if !intervalsDisjoint(readIntervals[0], writeIntervals[0]) {
		t.Fatalf("wiki read/write intervals overlapped: read=%v write=%v", readIntervals, writeIntervals)
	}
}

func newTestAgent(reg *tools.Registry) *Agent {
	return New(&mockProvider{}, reg, WithPermission(NewPermission(WithMode(ModeBypass))))
}

func toolResultBlocks(t *testing.T, messages []llm.Message) []llm.ToolResultBlock {
	t.Helper()
	blocks := make([]llm.ToolResultBlock, 0, len(messages))
	for i, msg := range messages {
		for j, content := range msg.Content {
			block, ok := content.(llm.ToolResultBlock)
			if !ok {
				t.Fatalf("message[%d] block[%d] type = %T, want llm.ToolResultBlock", i, j, content)
			}
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func assertToolResultIDs(t *testing.T, messages []llm.Message, want []string) {
	t.Helper()
	blocks := toolResultBlocks(t, messages)
	if len(blocks) != len(want) {
		t.Fatalf("len(blocks) = %d, want %d", len(blocks), len(want))
	}
	for i, block := range blocks {
		if block.ToolUseID != want[i] {
			t.Fatalf("block[%d].ToolUseID = %q, want %q", i, block.ToolUseID, want[i])
		}
	}
}

func assertStartWindow(t *testing.T, window time.Duration, tools ...*fakeTool) {
	t.Helper()
	var minStart, maxStart time.Time
	for i, tool := range tools {
		start, _ := tool.interval()
		if start.IsZero() {
			t.Fatalf("tool[%d] did not start", i)
		}
		if minStart.IsZero() || start.Before(minStart) {
			minStart = start
		}
		if maxStart.IsZero() || start.After(maxStart) {
			maxStart = start
		}
	}
	if maxStart.Sub(minStart) > window {
		t.Fatalf("start window = %s, want <= %s", maxStart.Sub(minStart), window)
	}
}

func assertIntervalsDisjoint(t *testing.T, a, b *fakeTool) {
	t.Helper()
	aStart, aFinish := a.interval()
	bStart, bFinish := b.interval()
	if !intervalsDisjoint([2]time.Time{aStart, aFinish}, [2]time.Time{bStart, bFinish}) {
		t.Fatalf("intervals overlapped: %s..%s with %s..%s", aStart, aFinish, bStart, bFinish)
	}
}

func intervalsDisjoint(a, b [2]time.Time) bool {
	return !a[1].After(b[0]) || !b[1].After(a[0])
}
