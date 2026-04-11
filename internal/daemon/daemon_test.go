package daemon

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stello/elnath/internal/identity"
	_ "modernc.org/sqlite"
)

type mockTaskRunner struct {
	text string
	err  error
}

func (r mockTaskRunner) run(_ context.Context, _ string, onText func(string)) (TaskResult, error) {
	if r.err != nil {
		return TaskResult{}, r.err
	}
	if onText != nil && r.text != "" {
		onText(r.text)
	}
	return TaskResult{Result: r.text, Summary: r.text, SessionID: "sess-test"}, nil
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
func startDaemon(t *testing.T, q *Queue, socketPath string, runner TaskRunner, workers int) *Daemon {
	t.Helper()

	d := New(q, socketPath, workers, runner, nil)

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

	return d
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

func TestDaemonSubmitDeduplicatesByPrincipalAndPrompt(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	release := make(chan struct{})
	runner := func(_ context.Context, _ string, _ func(string)) (TaskResult, error) {
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

// TestDaemonSubmitEmptyPayload verifies that an empty payload returns an error.
func TestDaemonSubmitEmptyPayload(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	socketPath := filepath.Join(t.TempDir(), "test.sock")
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

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	startDaemon(t, q, socketPath, mockTaskRunner{text: "ok"}.run, 1)

	resp := sendIPC(t, socketPath, IPCRequest{Command: "submit"})
	if resp.OK {
		t.Fatal("expected error for missing payload, got OK")
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

	hangingRunner := func(ctx context.Context, _ string, _ func(string)) (TaskResult, error) {
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

	activeButSlowRunner := func(ctx context.Context, _ string, onText func(string)) (TaskResult, error) {
		for {
			select {
			case <-ctx.Done():
				return TaskResult{}, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				if onText != nil {
					onText("still working")
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
