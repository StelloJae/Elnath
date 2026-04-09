package daemon

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

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
