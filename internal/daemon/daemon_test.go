package daemon

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/identity"
	_ "modernc.org/sqlite"
)

type mockTaskRunner struct {
	text string
	err  error
}

func (r mockTaskRunner) run(_ context.Context, _ string, sink event.Sink) (TaskResult, error) {
	if r.err != nil {
		return TaskResult{}, r.err
	}
	if sink != nil && r.text != "" {
		sink.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: r.text})
	}
	return TaskResult{Result: r.text, Summary: r.text, SessionID: "sess-test"}, nil
}

type mockPayloadTaskRunner struct {
	result TaskRunnerResult
	err    error

	mu      sync.Mutex
	payload TaskPayload
}

func (r *mockPayloadTaskRunner) Run(_ context.Context, payload TaskPayload, _ event.Sink) (TaskRunnerResult, error) {
	r.mu.Lock()
	r.payload = payload
	r.mu.Unlock()
	if r.err != nil {
		return TaskRunnerResult{}, r.err
	}
	return r.result, nil
}

func (r *mockPayloadTaskRunner) Payload() TaskPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.payload
}

var _ TaskRunner = (*mockPayloadTaskRunner)(nil)

type mockScheduler struct {
	started atomic.Bool
	stopped atomic.Bool
}

func (m *mockScheduler) Run(ctx context.Context) error {
	m.started.Store(true)
	<-ctx.Done()
	m.stopped.Store(true)
	return nil
}

type recordingTaskEnvelope struct {
	mu           sync.Mutex
	startErr     error
	succeedErr   error
	failErr      error
	reconcileErr error
	reconciled   bool
	started      []int64
	succeeded    []int64
	failed       []int64
}

type recordingSubmitSignalBridge struct {
	mu      sync.Mutex
	err     error
	payload string
	taskID  int64
	existed bool
	calls   int
}

type safeLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type recordingCompletionGate struct {
	validateErr error
	evaluateErr error
	decision    CompletionGateDecision

	mu            sync.Mutex
	validated     []int64
	evaluated     []int64
	agenticTaskID []int64
}

type closingDBCompletionGate struct {
	db *sql.DB
}

func (g closingDBCompletionGate) Validate(context.Context, Task, int64) error {
	return nil
}

func (g closingDBCompletionGate) Evaluate(context.Context, Task, int64) (CompletionGateDecision, error) {
	_ = g.db.Close()
	return CompletionGateDecision{Passed: true, Status: "passed", Reason: "verification passed"}, nil
}

func (g *recordingCompletionGate) Validate(_ context.Context, task Task, agenticTaskID int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.validated = append(g.validated, task.ID)
	g.agenticTaskID = append(g.agenticTaskID, agenticTaskID)
	return g.validateErr
}

func (g *recordingCompletionGate) Evaluate(_ context.Context, task Task, agenticTaskID int64) (CompletionGateDecision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.evaluated = append(g.evaluated, task.ID)
	g.agenticTaskID = append(g.agenticTaskID, agenticTaskID)
	if g.evaluateErr != nil {
		return CompletionGateDecision{}, g.evaluateErr
	}
	return g.decision, nil
}

func (g *recordingCompletionGate) snapshot() (validated, evaluated, agenticTaskIDs []int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]int64(nil), g.validated...), append([]int64(nil), g.evaluated...), append([]int64(nil), g.agenticTaskID...)
}

func (b *recordingSubmitSignalBridge) RecordManualSubmitSignal(_ context.Context, payload string, taskID int64, existed bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.payload = payload
	b.taskID = taskID
	b.existed = existed
	b.calls++
	return b.err
}

func (b *recordingSubmitSignalBridge) snapshot() (payload string, taskID int64, existed bool, calls int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.payload, b.taskID, b.existed, b.calls
}

func (e *recordingTaskEnvelope) Start(_ context.Context, task Task) (TaskEnvelopeRun, error) {
	if e.startErr != nil {
		return nil, e.startErr
	}
	e.mu.Lock()
	e.started = append(e.started, task.ID)
	e.mu.Unlock()
	return &recordingTaskEnvelopeRun{taskID: task.ID, envelope: e}, nil
}

func (e *recordingTaskEnvelope) Reconcile(context.Context) error {
	e.mu.Lock()
	e.reconciled = true
	err := e.reconcileErr
	e.mu.Unlock()
	return err
}

type recordingTaskEnvelopeRun struct {
	taskID   int64
	envelope *recordingTaskEnvelope
}

func (r *recordingTaskEnvelopeRun) AgenticTaskID() int64 {
	return r.taskID
}

func (r *recordingTaskEnvelopeRun) Succeed(context.Context) error {
	r.envelope.mu.Lock()
	defer r.envelope.mu.Unlock()
	r.envelope.succeeded = append(r.envelope.succeeded, r.taskID)
	return r.envelope.succeedErr
}

func (r *recordingTaskEnvelopeRun) Fail(context.Context) error {
	r.envelope.mu.Lock()
	defer r.envelope.mu.Unlock()
	r.envelope.failed = append(r.envelope.failed, r.taskID)
	return r.envelope.failErr
}

func (e *recordingTaskEnvelope) snapshot() (started, succeeded, failed []int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]int64(nil), e.started...), append([]int64(nil), e.succeeded...), append([]int64(nil), e.failed...)
}

func (e *recordingTaskEnvelope) wasReconciled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reconciled
}

func TestResolveTaskLogPrincipalFallsBackToDaemonPrincipal(t *testing.T) {
	fallback := identity.Principal{UserID: "stello", ProjectID: "elnath", Surface: "cli"}
	got := resolveTaskLogPrincipal(TaskPayload{Prompt: "legacy plain text"}, fallback)
	if got != fallback {
		t.Fatalf("resolveTaskLogPrincipal = %+v, want %+v", got, fallback)
	}
}

func TestResolveTaskLogPrincipalPrefersPayloadPrincipal(t *testing.T) {
	payloadPrincipal := identity.Principal{UserID: "42", ProjectID: "proj", Surface: "telegram"}
	fallback := identity.Principal{UserID: "stello", ProjectID: "elnath", Surface: "cli"}
	got := resolveTaskLogPrincipal(TaskPayload{Principal: payloadPrincipal}, fallback)
	if got != payloadPrincipal {
		t.Fatalf("resolveTaskLogPrincipal = %+v, want %+v", got, payloadPrincipal)
	}
}

// sendIPC connects to the Unix socket, writes a JSON-line request, and reads
// the JSON-line response.
func sendIPC(t *testing.T, socketPath string, req IPCRequest) IPCResponse {
	t.Helper()
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	data, _ := json.Marshal(req)
	conn.Write(append(data, '\n'))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatal("no response from daemon")
	}

	var resp IPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

// startDaemon spins up a Daemon in a background goroutine and waits until the
// Unix socket is ready. It registers a t.Cleanup to stop the daemon.
func startDaemon(t *testing.T, q *Queue, socketPath string, runner AgentTaskRunner, workers int) *Daemon {
	t.Helper()

	d := New(q, socketPath, workers, runner, nil)
	startDaemonInstance(t, d, socketPath)
	return d
}

func startDaemonInstance(t *testing.T, d *Daemon, socketPath string) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- d.Start(ctx)
	}()

	// Poll until the socket is accepting connections (max 2 s).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("unix", socketPath)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("daemon did not stop within timeout")
		}
	})
}

// pollTaskStatus retries queue.Get until the task reaches the expected status
// or the deadline elapses.
func pollTaskStatus(t *testing.T, q *Queue, id int64, want TaskStatus, timeout time.Duration) *Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := q.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get task %d: %v", id, err)
		}
		if task.Status == want {
			return task
		}
		time.Sleep(50 * time.Millisecond)
	}
	task, _ := q.Get(context.Background(), id)
	t.Fatalf("task %d: status = %q after %s, want %q", id, task.Status, timeout, want)
	return nil
}

func sameIDs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func shortDaemonSocketPath(prefix string) string {
	return filepath.Join("/tmp", prefix+"-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
}

func assertLogContains(t *testing.T, logs string, want ...string) {
	t.Helper()
	for _, text := range want {
		if !strings.Contains(logs, text) {
			t.Fatalf("log output missing %q:\n%s", text, logs)
		}
	}
}

// --- tests ---

// TestDaemonSubmitAndStatus verifies the end-to-end path:
// submit → task appears in status list → worker completes → status = done.
func TestDaemonSubmitAndStatus(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	startDaemon(t, q, socketPath, mockTaskRunner{text: "hello from mock"}.run, 1)

	// Submit a task via IPC.
	submitResp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "tell me a joke"),
	})
	if !submitResp.OK {
		t.Fatalf("submit: not OK: %s", submitResp.Err)
	}
	if extractExisted(t, submitResp) {
		t.Fatal("first submit should not report deduplication")
	}

	taskID := extractTaskID(t, submitResp)

	// Status should show the task (pending or running).
	statusResp := sendIPC(t, socketPath, IPCRequest{Command: "status"})
	if !statusResp.OK {
		t.Fatalf("status: not OK: %s", statusResp.Err)
	}
	tasks := extractTasks(t, statusResp)
	if len(tasks) == 0 {
		t.Fatal("status: expected at least one task")
	}
	found := false
	for _, tv := range tasks {
		if int64(tv["id"].(float64)) == taskID {
			found = true
			if _, ok := tv["progress"]; !ok {
				t.Fatalf("expected progress field in task view: %+v", tv)
			}
			if _, ok := tv["session_id"]; !ok {
				t.Fatalf("expected session_id field in task view: %+v", tv)
			}
			if _, ok := tv["completion"]; !ok {
				t.Fatalf("expected completion field in task view: %+v", tv)
			}
		}
	}
	if !found {
		t.Fatalf("submitted task %d not found in status response", taskID)
	}

	// Wait for the worker to finish.
	done := pollTaskStatus(t, q, taskID, StatusDone, 5*time.Second)
	if done.Result != "hello from mock" {
		t.Errorf("task result = %q, want %q", done.Result, "hello from mock")
	}
	if done.SessionID != "sess-test" {
		t.Errorf("session_id = %q, want %q", done.SessionID, "sess-test")
	}
}

func TestDaemonTaskEnvelopeDoesNotChangeQueueCompletion(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	envelope := &recordingTaskEnvelope{}
	socketPath := shortDaemonSocketPath("elnath-envelope-success")
	d := New(q, socketPath, 1, mockTaskRunner{text: "hello from mock"}.run, nil)
	d.WithTaskEnvelope(envelope)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "tell me a joke"),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)
	task := pollTaskStatus(t, q, taskID, StatusDone, 5*time.Second)
	if task.Result != "hello from mock" || task.Summary != "hello from mock" || task.SessionID != "sess-test" {
		t.Fatalf("unexpected completed task: %+v", task)
	}
	started, succeeded, failed := envelope.snapshot()
	if !sameIDs(started, []int64{taskID}) || !sameIDs(succeeded, []int64{taskID}) || len(failed) != 0 {
		t.Fatalf("envelope started=%v succeeded=%v failed=%v", started, succeeded, failed)
	}
}

func TestDaemonTaskEnvelopePassesAgenticTaskIDToRunner(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	envelope := &recordingTaskEnvelope{}
	seenAgenticTaskID := make(chan int64, 1)
	socketPath := shortDaemonSocketPath("elnath-envelope-context")
	d := New(q, socketPath, 1, func(ctx context.Context, _ string, _ event.Sink) (TaskResult, error) {
		id, ok := AgenticTaskIDFromContext(ctx)
		if !ok {
			return TaskResult{}, errors.New("missing agentic task id")
		}
		seenAgenticTaskID <- id
		return TaskResult{Result: "ok", Summary: "ok"}, nil
	}, nil)
	d.WithTaskEnvelope(envelope)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "tell me a joke"),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)
	pollTaskStatus(t, q, taskID, StatusDone, 5*time.Second)

	select {
	case got := <-seenAgenticTaskID:
		if got != taskID {
			t.Fatalf("agentic task id = %d, want queue task id %d", got, taskID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not observe agentic task id")
	}
}

func TestCompletionGate_DefaultLegacyDaemonTaskMarksDoneUnchanged(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	envelope := &recordingTaskEnvelope{}
	gate := &recordingCompletionGate{decision: CompletionGateDecision{Passed: false, Status: "blocked", Reason: "should not run"}}
	socketPath := shortDaemonSocketPath("elnath-completion-gate-legacy")
	d := New(q, socketPath, 1, mockTaskRunner{text: "legacy done"}.run, nil)
	d.WithTaskEnvelope(envelope)
	d.WithCompletionGate(gate)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "legacy task"),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)
	task := pollTaskStatus(t, q, taskID, StatusDone, 5*time.Second)
	if task.Result != "legacy done" {
		t.Fatalf("task result = %q, want legacy done", task.Result)
	}
	_, evaluated, _ := gate.snapshot()
	if len(evaluated) != 0 {
		t.Fatalf("legacy task evaluated completion gate: %v", evaluated)
	}
}

func TestCompletionGate_ExplicitGateRequiresPassedVerification(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	envelope := &recordingTaskEnvelope{}
	gate := &recordingCompletionGate{decision: CompletionGateDecision{
		Passed:            true,
		Status:            "passed",
		Reason:            "verification passed",
		VerificationRunID: 7,
	}}
	socketPath := shortDaemonSocketPath("elnath-completion-gate-pass")
	d := New(q, socketPath, 1, mockTaskRunner{text: "verified done"}.run, nil)
	d.WithTaskEnvelope(envelope)
	d.WithCompletionGate(gate)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, EncodeTaskPayload(TaskPayload{
			Prompt:                "gated task",
			AgenticCompletionGate: "verification",
		})),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)
	task := pollTaskStatus(t, q, taskID, StatusDone, 5*time.Second)
	if task.Result != "verified done" {
		t.Fatalf("task result = %q, want verified done", task.Result)
	}
	validated, evaluated, agenticIDs := gate.snapshot()
	if !sameIDs(validated, []int64{taskID}) || !sameIDs(evaluated, []int64{taskID}) || !sameIDs(agenticIDs, []int64{taskID, taskID}) {
		t.Fatalf("gate validated=%v evaluated=%v agenticIDs=%v, want task id %d", validated, evaluated, agenticIDs, taskID)
	}
}

func TestCompletionGate_BlocksWithoutVerification(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	envelope := &recordingTaskEnvelope{}
	gate := &recordingCompletionGate{decision: CompletionGateDecision{
		Passed: false,
		Status: "blocked",
		Reason: "missing verifier run",
	}}
	socketPath := shortDaemonSocketPath("elnath-completion-gate-block")
	d := New(q, socketPath, 1, mockTaskRunner{text: "runner succeeded but gate blocks"}.run, nil)
	d.WithTaskEnvelope(envelope)
	d.WithCompletionGate(gate)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, EncodeTaskPayload(TaskPayload{
			Prompt:                "gated task",
			AgenticCompletionGate: "verification",
		})),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)
	task := pollTaskStatus(t, q, taskID, StatusFailed, 5*time.Second)
	if !strings.Contains(task.Result, "completion gate blocked: missing verifier run") {
		t.Fatalf("task result = %q, want completion gate block reason", task.Result)
	}
	_, succeeded, failed := envelope.snapshot()
	if len(succeeded) != 0 || !sameIDs(failed, []int64{taskID}) {
		t.Fatalf("envelope succeeded=%v failed=%v, want failed task %d", succeeded, failed, taskID)
	}
}

func TestCompletionGate_ConfigObserveRejectsGateRequest(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	var runnerCalled atomic.Bool
	gate := &recordingCompletionGate{validateErr: errors.New("completion gate requested but config maximum is observe")}
	socketPath := shortDaemonSocketPath("elnath-completion-gate-observe")
	d := New(q, socketPath, 1, func(context.Context, string, event.Sink) (TaskResult, error) {
		runnerCalled.Store(true)
		return TaskResult{Result: "should not run"}, nil
	}, nil)
	d.WithTaskEnvelope(&recordingTaskEnvelope{})
	d.WithCompletionGate(gate)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, EncodeTaskPayload(TaskPayload{
			Prompt:                "gated task",
			AgenticCompletionGate: "verification",
		})),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	task := pollTaskStatus(t, q, extractTaskID(t, resp), StatusFailed, 5*time.Second)
	if runnerCalled.Load() {
		t.Fatal("runner called despite config rejecting completion gate")
	}
	if !strings.Contains(task.Result, "config maximum is observe") {
		t.Fatalf("task result = %q, want config rejection reason", task.Result)
	}
}

func TestCompletionGate_GateLedgerInsertFailurePreventsMarkDone(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	gate := &recordingCompletionGate{evaluateErr: errors.New("completion gate ledger: disk full")}
	socketPath := shortDaemonSocketPath("elnath-completion-gate-ledger-fail")
	d := New(q, socketPath, 1, mockTaskRunner{text: "runner succeeded"}.run, nil)
	d.WithTaskEnvelope(&recordingTaskEnvelope{})
	d.WithCompletionGate(gate)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, EncodeTaskPayload(TaskPayload{
			Prompt:                "gated task",
			AgenticCompletionGate: "verification",
		})),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	task := pollTaskStatus(t, q, extractTaskID(t, resp), StatusFailed, 5*time.Second)
	if !strings.Contains(task.Result, "completion gate ledger: disk full") {
		t.Fatalf("task result = %q, want ledger failure", task.Result)
	}
}

func TestCompletionGate_MarkDoneFailureAfterGatePassObservable(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	var logs safeLogBuffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	socketPath := shortDaemonSocketPath("elnath-completion-gate-markdone-fail")
	d := New(q, socketPath, 1, mockTaskRunner{text: "runner succeeded"}.run, logger)
	d.WithTaskEnvelope(&recordingTaskEnvelope{})
	d.WithCompletionGate(closingDBCompletionGate{db: db})
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, EncodeTaskPayload(TaskPayload{
			Prompt:                "gated task",
			AgenticCompletionGate: "verification",
		})),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), "worker: mark done") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("logs missing mark done failure:\n%s", logs.String())
}

func TestCompletionGate_MissingAgenticTaskIDFailsClosed(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	var runnerCalled atomic.Bool
	socketPath := shortDaemonSocketPath("elnath-completion-gate-missing-task")
	d := New(q, socketPath, 1, func(context.Context, string, event.Sink) (TaskResult, error) {
		runnerCalled.Store(true)
		return TaskResult{Result: "should not run"}, nil
	}, nil)
	d.WithCompletionGate(&recordingCompletionGate{decision: CompletionGateDecision{Passed: true, Status: "passed"}})
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, EncodeTaskPayload(TaskPayload{
			Prompt:                "gated task",
			AgenticCompletionGate: "verification",
		})),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	task := pollTaskStatus(t, q, extractTaskID(t, resp), StatusFailed, 5*time.Second)
	if runnerCalled.Load() {
		t.Fatal("runner called despite missing agentic task id")
	}
	if !strings.Contains(task.Result, "agentic task id is required") {
		t.Fatalf("task result = %q, want missing agentic task id", task.Result)
	}
}

func TestCompletionGate_DoesNotEnqueueProposedTasksOrWakeFollowups(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	d := New(q, shortDaemonSocketPath("elnath-completion-gate-no-enqueue"), 1, mockTaskRunner{text: "verified done"}.run, nil)
	d.WithTaskEnvelope(&recordingTaskEnvelope{})
	d.WithCompletionGate(&recordingCompletionGate{decision: CompletionGateDecision{Passed: true, Status: "passed"}})
	startDaemonInstance(t, d, d.socketPath)

	resp := sendIPC(t, d.socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, EncodeTaskPayload(TaskPayload{
			Prompt:                "gated task",
			AgenticCompletionGate: "verification",
		})),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	pollTaskStatus(t, q, extractTaskID(t, resp), StatusDone, 5*time.Second)
	tasks, err := q.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("queue tasks = %d, want only original task", len(tasks))
	}
}

func TestDaemonTaskEnvelopeFailureMarksFailed(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	envelope := &recordingTaskEnvelope{}
	socketPath := shortDaemonSocketPath("elnath-envelope-failure")
	d := New(q, socketPath, 1, mockTaskRunner{err: errors.New("runner boom")}.run, nil)
	d.WithTaskEnvelope(envelope)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "break"),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)
	task := pollTaskStatus(t, q, taskID, StatusFailed, 5*time.Second)
	if !strings.Contains(task.Result, "runner boom") {
		t.Fatalf("task result = %q, want runner boom", task.Result)
	}
	started, succeeded, failed := envelope.snapshot()
	if !sameIDs(started, []int64{taskID}) || len(succeeded) != 0 || !sameIDs(failed, []int64{taskID}) {
		t.Fatalf("envelope started=%v succeeded=%v failed=%v", started, succeeded, failed)
	}
}

func TestDaemonTaskEnvelopeCreationFailureDoesNotBlockQueueCompletion(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	var runnerCalled atomic.Bool
	envelope := &recordingTaskEnvelope{startErr: errors.New("envelope unavailable")}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	socketPath := shortDaemonSocketPath("elnath-envelope-start-failure")
	d := New(q, socketPath, 1, func(context.Context, string, event.Sink) (TaskResult, error) {
		runnerCalled.Store(true)
		return TaskResult{Result: "ran after envelope failure", Summary: "ran after envelope failure"}, nil
	}, logger)
	d.WithTaskEnvelope(envelope)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "break before run"),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)
	task := pollTaskStatus(t, q, taskID, StatusDone, 5*time.Second)
	if !runnerCalled.Load() {
		t.Fatal("runner was not called after envelope creation failure")
	}
	if task.Result != "ran after envelope failure" {
		t.Fatalf("task result = %q, want runner result", task.Result)
	}
	assertLogContains(t, logs.String(), "worker: task envelope start failed", "degraded_observability=true", "envelope unavailable")
}

func TestDaemonTaskEnvelopeSuccessUpdateFailureDoesNotCorruptQueueCompletion(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	envelope := &recordingTaskEnvelope{succeedErr: errors.New("success mirror unavailable")}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	socketPath := shortDaemonSocketPath("elnath-envelope-success-update-failure")
	d := New(q, socketPath, 1, mockTaskRunner{text: "hello from mock"}.run, logger)
	d.WithTaskEnvelope(envelope)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "tell me a joke"),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)
	task := pollTaskStatus(t, q, taskID, StatusDone, 5*time.Second)
	if task.Result != "hello from mock" {
		t.Fatalf("task result = %q, want runner result", task.Result)
	}
	assertLogContains(t, logs.String(), "worker: task envelope success update", "degraded_observability=true", "success mirror unavailable")
}

func TestDaemonTaskEnvelopeFailureUpdateFailureDoesNotCorruptQueueFailure(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	envelope := &recordingTaskEnvelope{failErr: errors.New("failure mirror unavailable")}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	socketPath := shortDaemonSocketPath("elnath-envelope-failure-update-failure")
	d := New(q, socketPath, 1, mockTaskRunner{err: errors.New("runner boom")}.run, logger)
	d.WithTaskEnvelope(envelope)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "break"),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)
	task := pollTaskStatus(t, q, taskID, StatusFailed, 5*time.Second)
	if !strings.Contains(task.Result, "runner boom") {
		t.Fatalf("task result = %q, want runner boom", task.Result)
	}
	assertLogContains(t, logs.String(), "worker: task envelope fail update", "degraded_observability=true", "failure mirror unavailable")
}

func TestDaemonTaskEnvelopeReconcileFailureDoesNotBlockQueueCompletion(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	envelope := &recordingTaskEnvelope{reconcileErr: errors.New("reconcile unavailable")}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	socketPath := shortDaemonSocketPath("elnath-envelope-reconcile-failure")
	d := New(q, socketPath, 1, mockTaskRunner{text: "hello from mock"}.run, logger)
	d.WithTaskEnvelope(envelope)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "tell me a joke"),
	})
	if !resp.OK {
		t.Fatalf("submit: not OK: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)
	task := pollTaskStatus(t, q, taskID, StatusDone, 5*time.Second)
	if task.Result != "hello from mock" {
		t.Fatalf("task result = %q, want runner result", task.Result)
	}
	if !envelope.wasReconciled() {
		t.Fatal("task envelope reconcile was not called")
	}
	assertLogContains(t, logs.String(), "daemon: task envelope reconcile failed", "degraded_observability=true", "reconcile unavailable")
}

func TestDaemonSubmitDeduplicatesByPrincipalAndPrompt(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	release := make(chan struct{})
	runner := func(_ context.Context, _ string, _ event.Sink) (TaskResult, error) {
		<-release
		return TaskResult{Result: "ok", Summary: "ok"}, nil
	}

	socketPath := filepath.Join("/tmp", "elnath-dedup-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	startDaemon(t, q, socketPath, runner, 1)
	t.Cleanup(func() { close(release) })

	raw := EncodeTaskPayload(TaskPayload{
		Prompt:    "tell me a joke",
		Surface:   "telegram",
		Principal: identity.Principal{UserID: "42", ProjectID: "proj-1", Surface: "telegram"},
	})

	first := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, raw)})
	if !first.OK {
		t.Fatalf("first submit: not OK: %s", first.Err)
	}
	if extractExisted(t, first) {
		t.Fatal("first submit should not report deduplication")
	}
	firstID := extractTaskID(t, first)

	second := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, raw)})
	if !second.OK {
		t.Fatalf("second submit: not OK: %s", second.Err)
	}
	if !extractExisted(t, second) {
		t.Fatal("second submit should report deduplication")
	}
	if secondID := extractTaskID(t, second); secondID != firstID {
		t.Fatalf("second submit task id = %d, want %d", secondID, firstID)
	}
}

func TestManualSignalBridge_RecordsSignalIfApplicable(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	bridge := &recordingSubmitSignalBridge{}
	socketPath := shortDaemonSocketPath("elnath-manual-signal")
	d := New(q, socketPath, 1, mockTaskRunner{text: "done"}.run, nil)
	d.WithSubmitSignalBridge(bridge)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "manual task"),
	})
	if !resp.OK {
		t.Fatalf("submit: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)
	payload, gotTaskID, existed, calls := bridge.snapshot()
	if calls != 1 || gotTaskID != taskID || existed || payload == "" {
		t.Fatalf("bridge payload=%q taskID=%d existed=%v calls=%d, want one non-deduped signal for task %d", payload, gotTaskID, existed, calls, taskID)
	}
}

func TestManualSignalBridge_FailureObservable(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	bridge := &recordingSubmitSignalBridge{err: errors.New("manual signal unavailable")}
	var logs bytes.Buffer
	socketPath := shortDaemonSocketPath("elnath-manual-signal-failure")
	d := New(q, socketPath, 1, mockTaskRunner{text: "done"}.run, slog.New(slog.NewTextHandler(&logs, nil)))
	d.WithSubmitSignalBridge(bridge)
	startDaemonInstance(t, d, socketPath)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "manual task"),
	})
	if !resp.OK {
		t.Fatalf("submit should preserve queue behavior after signal failure: %s", resp.Err)
	}
	if got := logs.String(); !strings.Contains(got, "daemon: submit signal bridge failed") || !strings.Contains(got, "manual signal unavailable") {
		t.Fatalf("logs = %q, want observable manual signal bridge failure", got)
	}
}

// TestDaemonSubmitEmptyPayload verifies that an empty payload returns an error.
func TestDaemonSubmitEmptyPayload(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join("/tmp", "elnath-whitespace-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	startDaemon(t, q, socketPath, mockTaskRunner{text: "ok"}.run, 1)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, ""),
	})
	if resp.OK {
		t.Fatal("expected error for empty payload, got OK")
	}
	if resp.Err == "" {
		t.Fatal("expected non-empty error message")
	}
}

// TestDaemonSubmitNoPayload verifies that a missing payload field also errors.
func TestDaemonSubmitNoPayload(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join("/tmp", "elnath-whitespace-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	startDaemon(t, q, socketPath, mockTaskRunner{text: "ok"}.run, 1)

	resp := sendIPC(t, socketPath, IPCRequest{Command: "submit"})
	if resp.OK {
		t.Fatal("expected error for missing payload, got OK")
	}
}

func TestDaemonSubmitWhitespacePayload(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join("/tmp", "elnath-whitespace-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	startDaemon(t, q, socketPath, mockTaskRunner{text: "ok"}.run, 1)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "   "),
	})
	if resp.OK {
		t.Fatal("expected error for whitespace-only payload, got OK")
	}
	if resp.Err == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestDaemonSubmitRejectsBlankStructuredAgentPayload(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join("/tmp", "elnath-blank-structured-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	startDaemon(t, q, socketPath, mockTaskRunner{text: "ok"}.run, 1)

	payload := EncodeTaskPayload(TaskPayload{Prompt: "   ", SessionID: "sess-1"})
	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, payload),
	})
	if resp.OK {
		t.Fatal("expected error for blank structured agent payload, got OK")
	}
	if resp.Err == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestDaemonSubmitRejectsBlankStructuredResearchPayload(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join("/tmp", "elnath-blank-research-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	d := startDaemon(t, q, socketPath, mockTaskRunner{text: "ok"}.run, 1)
	d.SetResearchRunner(&mockPayloadTaskRunner{result: TaskRunnerResult{Summary: "research summary", Result: "research result"}})

	payload := EncodeTaskPayload(TaskPayload{Type: TaskTypeResearch, Prompt: "   "})
	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, payload),
	})
	if resp.OK {
		t.Fatal("expected error for blank structured research payload, got OK")
	}
	if resp.Err == "" {
		t.Fatal("expected non-empty error message")
	}
}

// TestDaemonStopCommand verifies that the "stop" command triggers graceful shutdown.
func TestDaemonStopCommand(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	d := New(q, socketPath, 1, mockTaskRunner{text: "ok"}.run, nil)

	startDone := make(chan error, 1)
	go func() {
		startDone <- d.Start(context.Background())
	}()

	// Wait for socket to be ready.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("unix", socketPath)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	resp := sendIPC(t, socketPath, IPCRequest{Command: "stop"})
	if !resp.OK {
		t.Fatalf("stop: not OK: %s", resp.Err)
	}

	select {
	case err := <-startDone:
		if err != nil {
			t.Errorf("Start returned error after stop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop within 3 seconds after stop command")
	}
}

// TestDaemonUnknownCommand verifies that unrecognised commands return an error response.
func TestDaemonUnknownCommand(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	startDaemon(t, q, socketPath, mockTaskRunner{text: "ok"}.run, 1)

	resp := sendIPC(t, socketPath, IPCRequest{Command: "frobnicate"})
	if resp.OK {
		t.Fatal("expected error for unknown command, got OK")
	}
	if resp.Err == "" {
		t.Fatal("expected non-empty error message for unknown command")
	}
}

// TestDaemonWorkerCompletion verifies that a submitted task is marked done with
// the text produced by the mock agent.
func TestDaemonWorkerCompletion(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	const wantResult = "agent output text"
	startDaemon(t, q, socketPath, mockTaskRunner{text: wantResult}.run, 1)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "do some work"),
	})
	if !resp.OK {
		t.Fatalf("submit: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)

	task := pollTaskStatus(t, q, taskID, StatusDone, 5*time.Second)
	if task.Result != wantResult {
		t.Errorf("result = %q, want %q", task.Result, wantResult)
	}

	statusResp := sendIPC(t, socketPath, IPCRequest{Command: "status"})
	if !statusResp.OK {
		t.Fatalf("status: not OK: %s", statusResp.Err)
	}
	var completion map[string]interface{}
	for _, tv := range extractTasks(t, statusResp) {
		if int64(tv["id"].(float64)) != taskID {
			continue
		}
		rawCompletion, ok := tv["completion"]
		if !ok {
			t.Fatalf("expected completion payload for task view: %+v", tv)
		}
		completion, ok = rawCompletion.(map[string]interface{})
		if !ok {
			t.Fatalf("completion payload type = %T, want map", rawCompletion)
		}
		break
	}
	if completion == nil {
		t.Fatalf("task %d completion payload missing from status response", taskID)
	}
	if got := int64(completion["task_id"].(float64)); got != taskID {
		t.Fatalf("completion.task_id = %d, want %d", got, taskID)
	}
	if got := completion["session_id"].(string); got != "sess-test" {
		t.Fatalf("completion.session_id = %q, want %q", got, "sess-test")
	}
	if got := completion["status"].(string); got != string(StatusDone) {
		t.Fatalf("completion.status = %q, want %q", got, StatusDone)
	}
	if got := completion["summary"].(string); got != wantResult {
		t.Fatalf("completion.summary = %q, want %q", got, wantResult)
	}
}

// TestDaemonWorkerFailure verifies that when the mock agent returns an error,
// the task is marked as failed.
func TestDaemonWorkerFailure(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	runnerErr := errors.New("provider error 500")
	startDaemon(t, q, socketPath, mockTaskRunner{err: runnerErr}.run, 1)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "this will fail"),
	})
	if !resp.OK {
		t.Fatalf("submit: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)

	task := pollTaskStatus(t, q, taskID, StatusFailed, 5*time.Second)
	if task.Result == "" {
		t.Error("expected non-empty error message in task.Result")
	}
}

func TestDaemonStartRunsScheduler(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	d := New(q, socketPath, 1, mockTaskRunner{text: "ok"}.run, nil)
	sch := &mockScheduler{}
	d.WithScheduler(sch)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- d.Start(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sch.started.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sch.started.Load() {
		t.Fatal("scheduler did not start")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop within timeout")
	}

	if !sch.stopped.Load() {
		t.Fatal("scheduler did not stop")
	}
}

func TestDaemonStartDeliversExistingCompletions(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	ctx := context.Background()

	mustEnqueue(t, q, ctx, "completed before daemon start")
	task, _ := q.Next(ctx)
	if err := q.MarkDone(ctx, task.ID, "all good", "summary text"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	socketPath := filepath.Join("/tmp", "elnath-start-deliver-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	d := New(q, socketPath, 1, mockTaskRunner{text: "ok"}.run, nil)
	router := mustNewDeliveryRouter(t, db)
	sink := &recordingSink{name: "telegram"}
	router.Register(sink)
	d.WithDeliveryRouter(router)

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- d.Start(runCtx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sink.Count() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop within timeout")
	}

	if sink.Count() != 1 {
		t.Fatalf("sink received %d completions, want 1", sink.Count())
	}
	if sink.Completion(0).TaskID != task.ID {
		t.Fatalf("completion task_id = %d, want %d", sink.Completion(0).TaskID, task.ID)
	}
}

func TestDaemonResearchTaskRequiresConfiguredRunner(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join("/tmp", "elnath-research-missing-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	startDaemon(t, q, socketPath, mockTaskRunner{text: "agent should not run"}.run, 1)

	payload := EncodeTaskPayload(TaskPayload{Type: TaskTypeResearch, Prompt: "ambient research loop"})
	resp := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, payload)})
	if resp.OK {
		t.Fatal("expected submit-time error for missing research runner")
	}
	if !strings.Contains(resp.Err, "research runner not configured") {
		t.Fatalf("resp.Err = %q, want missing research runner error", resp.Err)
	}
	tasks, err := q.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("queued tasks = %d, want 0", len(tasks))
	}
}

func TestDaemonResearchTaskUsesConfiguredRunner(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join("/tmp", "elnath-research-runner-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	d := startDaemon(t, q, socketPath, func(_ context.Context, _ string, _ event.Sink) (TaskResult, error) {
		return TaskResult{}, errors.New("agent runner should not run for research tasks")
	}, 1)
	researchRunner := &mockPayloadTaskRunner{result: TaskRunnerResult{Summary: "research summary", Result: "research result"}}
	d.SetResearchRunner(researchRunner)

	payload := EncodeTaskPayload(TaskPayload{Type: TaskTypeResearch, Prompt: "ambient research loop", SessionID: "sess-123"})
	resp := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, payload)})
	if !resp.OK {
		t.Fatalf("submit: %s", resp.Err)
	}

	taskID := extractTaskID(t, resp)
	task := pollTaskStatus(t, q, taskID, StatusDone, 5*time.Second)
	if task.Result != "research result" {
		t.Fatalf("result = %q, want research result", task.Result)
	}
	if task.Summary != "research summary" {
		t.Fatalf("summary = %q, want research summary", task.Summary)
	}
	if task.SessionID != "sess-123" {
		t.Fatalf("session_id = %q, want sess-123", task.SessionID)
	}
	if got := researchRunner.Payload(); got != (TaskPayload{Type: TaskTypeResearch, Prompt: "ambient research loop", SessionID: "sess-123"}) {
		t.Fatalf("runner payload = %+v", got)
	}
}

func TestDaemonResearchTaskUsesRunnerSessionID(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join("/tmp", "elnath-research-session-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	d := startDaemon(t, q, socketPath, func(_ context.Context, _ string, _ event.Sink) (TaskResult, error) {
		return TaskResult{}, errors.New("agent runner should not run for research tasks")
	}, 1)
	d.SetResearchRunner(&mockPayloadTaskRunner{result: TaskRunnerResult{Summary: "research summary", Result: "research result", SessionID: "research-sess"}})

	payload := EncodeTaskPayload(TaskPayload{Type: TaskTypeResearch, Prompt: "ambient research loop"})
	resp := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, payload)})
	if !resp.OK {
		t.Fatalf("submit: %s", resp.Err)
	}

	taskID := extractTaskID(t, resp)
	task := pollTaskStatus(t, q, taskID, StatusDone, 5*time.Second)
	if task.SessionID != "research-sess" {
		t.Fatalf("session_id = %q, want research-sess", task.SessionID)
	}
}

func TestDaemonSubmitDoesNotDeduplicateAcrossTaskTypes(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	release := make(chan struct{})
	agentRunner := func(_ context.Context, _ string, _ event.Sink) (TaskResult, error) {
		<-release
		return TaskResult{Result: "agent result", Summary: "agent result"}, nil
	}

	socketPath := filepath.Join("/tmp", "elnath-type-dedup-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	d := startDaemon(t, q, socketPath, agentRunner, 1)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	d.SetResearchRunner(&mockPayloadTaskRunner{result: TaskRunnerResult{Summary: "research result", Result: "research result"}})

	principal := identity.Principal{UserID: "42", ProjectID: "proj-1", Surface: "telegram"}
	agentPayload := EncodeTaskPayload(TaskPayload{Prompt: "same prompt", Surface: "telegram", Principal: principal})
	first := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, agentPayload)})
	if !first.OK {
		t.Fatalf("first submit: %s", first.Err)
	}
	if extractExisted(t, first) {
		t.Fatal("first submit should not report deduplication")
	}
	firstID := extractTaskID(t, first)

	researchPayload := EncodeTaskPayload(TaskPayload{Type: TaskTypeResearch, Prompt: "same prompt", Surface: "telegram", Principal: principal})
	second := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, researchPayload)})
	if !second.OK {
		t.Fatalf("second submit: %s", second.Err)
	}
	if extractExisted(t, second) {
		t.Fatal("research submit should not deduplicate against agent task")
	}
	if secondID := extractTaskID(t, second); secondID == firstID {
		t.Fatalf("second task id = %d, want different from %d", secondID, firstID)
	}

	close(release)
	pollTaskStatus(t, q, firstID, StatusDone, 5*time.Second)
	pollTaskStatus(t, q, extractTaskID(t, second), StatusDone, 5*time.Second)
}

func TestDaemonSubmitDoesNotDeduplicateAcrossAgenticEnforcementModes(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	release := make(chan struct{})
	agentRunner := func(_ context.Context, _ string, _ event.Sink) (TaskResult, error) {
		<-release
		return TaskResult{Result: "agent result", Summary: "agent result"}, nil
	}

	socketPath := filepath.Join("/tmp", "elnath-enforcement-dedup-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	startDaemon(t, q, socketPath, agentRunner, 1)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	principal := identity.Principal{UserID: "42", ProjectID: "proj-1", Surface: "telegram"}
	legacyPayload := EncodeTaskPayload(TaskPayload{Prompt: "same prompt", Surface: "telegram", Principal: principal})
	first := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, legacyPayload)})
	if !first.OK {
		t.Fatalf("first submit: %s", first.Err)
	}
	if extractExisted(t, first) {
		t.Fatal("first submit should not report deduplication")
	}
	firstID := extractTaskID(t, first)

	gatewayPayload := EncodeTaskPayload(TaskPayload{
		Prompt:             "same prompt",
		Surface:            "telegram",
		Principal:          principal,
		AgenticEnforcement: "gateway",
	})
	second := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, gatewayPayload)})
	if !second.OK {
		t.Fatalf("second submit: %s", second.Err)
	}
	if extractExisted(t, second) {
		t.Fatal("gateway opt-in submit should not deduplicate against legacy task")
	}
	if secondID := extractTaskID(t, second); secondID == firstID {
		t.Fatalf("second task id = %d, want different from %d", secondID, firstID)
	}

	close(release)
	pollTaskStatus(t, q, firstID, StatusDone, 5*time.Second)
	pollTaskStatus(t, q, extractTaskID(t, second), StatusDone, 5*time.Second)
}

func TestDaemonSubmitDoesNotDeduplicateAcrossAgenticCompletionGateModes(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	release := make(chan struct{})
	agentRunner := func(_ context.Context, _ string, _ event.Sink) (TaskResult, error) {
		<-release
		return TaskResult{Result: "agent result", Summary: "agent result"}, nil
	}

	socketPath := filepath.Join("/tmp", "elnath-completion-dedup-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	d := New(q, socketPath, 1, agentRunner, nil)
	d.WithTaskEnvelope(&recordingTaskEnvelope{})
	d.WithCompletionGate(&recordingCompletionGate{decision: CompletionGateDecision{Passed: true, Status: "passed"}})
	startDaemonInstance(t, d, socketPath)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	principal := identity.Principal{UserID: "42", ProjectID: "proj-1", Surface: "telegram"}
	legacyPayload := EncodeTaskPayload(TaskPayload{Prompt: "same prompt", Surface: "telegram", Principal: principal})
	first := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, legacyPayload)})
	if !first.OK {
		t.Fatalf("first submit: %s", first.Err)
	}
	if extractExisted(t, first) {
		t.Fatal("first submit should not report deduplication")
	}
	firstID := extractTaskID(t, first)

	gatedPayload := EncodeTaskPayload(TaskPayload{
		Prompt:                "same prompt",
		Surface:               "telegram",
		Principal:             principal,
		AgenticCompletionGate: "verification",
	})
	second := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, gatedPayload)})
	if !second.OK {
		t.Fatalf("second submit: %s", second.Err)
	}
	if extractExisted(t, second) {
		t.Fatal("completion-gated submit should not deduplicate against legacy task")
	}
	if secondID := extractTaskID(t, second); secondID == firstID {
		t.Fatalf("second task id = %d, want different from %d", secondID, firstID)
	}

	close(release)
	pollTaskStatus(t, q, firstID, StatusDone, 5*time.Second)
	pollTaskStatus(t, q, extractTaskID(t, second), StatusDone, 5*time.Second)
}

func TestDaemonSubmitDeduplicatesEquivalentAgenticCompletionGateCasing(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	release := make(chan struct{})
	agentRunner := func(_ context.Context, _ string, _ event.Sink) (TaskResult, error) {
		<-release
		return TaskResult{Result: "agent result", Summary: "agent result"}, nil
	}

	socketPath := filepath.Join("/tmp", "elnath-completion-case-dedup-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	d := New(q, socketPath, 1, agentRunner, nil)
	d.WithTaskEnvelope(&recordingTaskEnvelope{})
	d.WithCompletionGate(&recordingCompletionGate{decision: CompletionGateDecision{Passed: true, Status: "passed"}})
	startDaemonInstance(t, d, socketPath)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	principal := identity.Principal{UserID: "42", ProjectID: "proj-1", Surface: "telegram"}
	firstPayload := EncodeTaskPayload(TaskPayload{
		Prompt:                "same prompt",
		Surface:               "telegram",
		Principal:             principal,
		AgenticCompletionGate: "verification",
	})
	first := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, firstPayload)})
	if !first.OK {
		t.Fatalf("first submit: %s", first.Err)
	}
	if extractExisted(t, first) {
		t.Fatal("first submit should not report deduplication")
	}
	firstID := extractTaskID(t, first)

	secondPayload := `{"prompt":"same prompt","surface":"telegram","principal":{"user_id":"42","project_id":"proj-1","surface":"telegram"},"agentic_completion_gate":" VERIFICATION "}`
	second := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, secondPayload)})
	if !second.OK {
		t.Fatalf("second submit: %s", second.Err)
	}
	if !extractExisted(t, second) {
		t.Fatal("equivalent completion gate casing should report deduplication")
	}
	if secondID := extractTaskID(t, second); secondID != firstID {
		t.Fatalf("second task id = %d, want deduped id %d", secondID, firstID)
	}

	close(release)
	pollTaskStatus(t, q, firstID, StatusDone, 5*time.Second)
}

func TestDaemonSubmitDoesNotDeduplicateAcrossSessions(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	release := make(chan struct{})
	agentRunner := func(_ context.Context, _ string, _ event.Sink) (TaskResult, error) {
		<-release
		return TaskResult{Result: "agent result", Summary: "agent result"}, nil
	}

	socketPath := filepath.Join("/tmp", "elnath-session-dedup-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	startDaemon(t, q, socketPath, agentRunner, 1)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	principal := identity.Principal{UserID: "42", ProjectID: "proj-1", Surface: "telegram"}
	firstPayload := EncodeTaskPayload(TaskPayload{Prompt: "same prompt", SessionID: "sess-1", Surface: "telegram", Principal: principal})
	first := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, firstPayload)})
	if !first.OK {
		t.Fatalf("first submit: %s", first.Err)
	}
	if extractExisted(t, first) {
		t.Fatal("first submit should not report deduplication")
	}
	firstID := extractTaskID(t, first)

	secondPayload := EncodeTaskPayload(TaskPayload{Prompt: "same prompt", SessionID: "sess-2", Surface: "telegram", Principal: principal})
	second := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, secondPayload)})
	if !second.OK {
		t.Fatalf("second submit: %s", second.Err)
	}
	if extractExisted(t, second) {
		t.Fatal("second submit should not deduplicate against a different session")
	}
	if secondID := extractTaskID(t, second); secondID == firstID {
		t.Fatalf("second task id = %d, want different from %d", secondID, firstID)
	}

	close(release)
	pollTaskStatus(t, q, firstID, StatusDone, 5*time.Second)
	pollTaskStatus(t, q, extractTaskID(t, second), StatusDone, 5*time.Second)
}

func TestDaemonSubmitDoesNotDeduplicateCollidingEncodedFields(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	release := make(chan struct{})
	agentRunner := func(_ context.Context, _ string, _ event.Sink) (TaskResult, error) {
		<-release
		return TaskResult{Result: "agent result", Summary: "agent result"}, nil
	}

	socketPath := filepath.Join("/tmp", "elnath-key-collision-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	d := startDaemon(t, q, socketPath, agentRunner, 1)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	d.SetResearchRunner(&mockPayloadTaskRunner{result: TaskRunnerResult{Summary: "research result", Result: "research result"}})

	principal := identity.Principal{UserID: "42", ProjectID: "proj-1", Surface: "telegram"}
	agentPayload := EncodeTaskPayload(TaskPayload{Prompt: "research:foo", Surface: "telegram", Principal: principal})
	first := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, agentPayload)})
	if !first.OK {
		t.Fatalf("first submit: %s", first.Err)
	}
	if extractExisted(t, first) {
		t.Fatal("first submit should not report deduplication")
	}
	firstID := extractTaskID(t, first)

	researchPayload := EncodeTaskPayload(TaskPayload{Type: TaskTypeResearch, Prompt: "foo", Surface: "telegram", Principal: principal})
	second := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, researchPayload)})
	if !second.OK {
		t.Fatalf("second submit: %s", second.Err)
	}
	if extractExisted(t, second) {
		t.Fatal("colliding encoded fields should not deduplicate distinct tasks")
	}
	if secondID := extractTaskID(t, second); secondID == firstID {
		t.Fatalf("second task id = %d, want different from %d", secondID, firstID)
	}

	close(release)
	pollTaskStatus(t, q, firstID, StatusDone, 5*time.Second)
	pollTaskStatus(t, q, extractTaskID(t, second), StatusDone, 5*time.Second)
}

// TestDaemonInvalidJSON verifies that a non-JSON line results in an error response.
func TestDaemonInvalidJSON(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	startDaemon(t, q, socketPath, mockTaskRunner{text: "ok"}.run, 1)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.Write([]byte("not json at all\n"))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatal("expected error response for invalid JSON")
	}
	var resp IPCResponse
	json.Unmarshal(scanner.Bytes(), &resp)
	if resp.OK {
		t.Fatal("expected error for invalid JSON, got OK")
	}
}

// TestDaemonInactivityTimeout verifies that a task hanging without progress
// updates is cancelled after the inactivity timeout.
func TestDaemonInactivityTimeout(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	hangingRunner := func(ctx context.Context, _ string, _ event.Sink) (TaskResult, error) {
		<-ctx.Done()
		return TaskResult{}, ctx.Err()
	}

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	d := startDaemon(t, q, socketPath, hangingRunner, 1)
	d.watchdogInterval = 100 * time.Millisecond
	d.WithTimeouts(500*time.Millisecond, 0)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "will hang"),
	})
	if !resp.OK {
		t.Fatalf("submit: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)

	task := pollTaskStatus(t, q, taskID, StatusFailed, 15*time.Second)
	if task.Status != StatusFailed {
		t.Fatalf("status = %q, want failed after inactivity timeout", task.Status)
	}
}

// TestDaemonWallClockTimeout verifies that a task exceeding the wall-clock
// deadline is cancelled even if it reports progress.
func TestDaemonWallClockTimeout(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	activeButSlowRunner := func(ctx context.Context, _ string, sink event.Sink) (TaskResult, error) {
		for {
			select {
			case <-ctx.Done():
				return TaskResult{}, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				if sink != nil {
					sink.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: "still working"})
				}
			}
		}
	}

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	d := startDaemon(t, q, socketPath, activeButSlowRunner, 1)
	d.WithTimeouts(0, 800*time.Millisecond)

	resp := sendIPC(t, socketPath, IPCRequest{
		Command: "submit",
		Payload: mustMarshalString(t, "will exceed wall clock"),
	})
	if !resp.OK {
		t.Fatalf("submit: %s", resp.Err)
	}
	taskID := extractTaskID(t, resp)

	task := pollTaskStatus(t, q, taskID, StatusFailed, 15*time.Second)
	if task.Status != StatusFailed {
		t.Fatalf("status = %q, want failed after wall-clock timeout", task.Status)
	}
}

// --- helpers ---

func mustMarshalString(t *testing.T, s string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal string: %v", err)
	}
	return b
}

func extractTaskID(t *testing.T, resp IPCResponse) int64 {
	t.Helper()
	m, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("submit response Data is not a map: %T", resp.Data)
	}
	idFloat, ok := m["task_id"].(float64)
	if !ok {
		t.Fatalf("task_id missing or not a number in response: %v", resp.Data)
	}
	return int64(idFloat)
}

func extractExisted(t *testing.T, resp IPCResponse) bool {
	t.Helper()
	m, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("submit response Data is not a map: %T", resp.Data)
	}
	value, ok := m["existed"]
	if !ok {
		t.Fatal("submit response missing existed flag")
	}
	existed, ok := value.(bool)
	if !ok {
		t.Fatalf("submit response existed is not a bool: %T", value)
	}
	return existed
}

func extractTasks(t *testing.T, resp IPCResponse) []map[string]interface{} {
	t.Helper()
	m, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("status response Data is not a map: %T", resp.Data)
	}
	raw, ok := m["tasks"]
	if !ok {
		t.Fatal("status response missing 'tasks' key")
	}
	slice, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("tasks is not a slice: %T", raw)
	}
	out := make([]map[string]interface{}, 0, len(slice))
	for _, item := range slice {
		entry, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("task entry is not a map: %T", item)
		}
		out = append(out, entry)
	}
	return out
}

// Compile-time assertion: openTestDB is shared with queue_test.go in the same
// package; this blank import ensures the sqlite driver is registered exactly once.
var _ *sql.DB = nil
