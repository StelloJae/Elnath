package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"

	_ "modernc.org/sqlite"
)

func openTestQueueDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(p); err != nil {
			t.Fatalf("exec pragma %q: %v", p, err)
		}
	}
	return db
}

func zeroTime() time.Time { return time.Time{} }

func TestCmdTaskUsage(t *testing.T) {
	stdout, _ := captureOutput(t, func() {
		if err := cmdTask(context.Background(), nil); err != nil {
			t.Fatalf("cmdTask usage: %v", err)
		}
	})
	if !strings.Contains(stdout, "Usage: elnath task") {
		t.Fatalf("stdout = %q, want task usage", stdout)
	}
	for _, want := range []string{"monitor <id>", "output <id>", "stop <id>", "answer", "cancel-question", "handoff <id>"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want usage to contain %q", stdout, want)
		}
	}
}

func TestCmdTaskUnknownSubcommand(t *testing.T) {
	err := cmdTask(context.Background(), []string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown task subcommand: bogus") {
		t.Fatalf("cmdTask(bogus) err = %v, want unknown subcommand", err)
	}
}

func TestCmdTaskCancelQuestionWithStoreRecordsOutcome(t *testing.T) {
	store := learning.NewOutcomeStore(filepath.Join(t.TempDir(), "outcomes.jsonl"))
	if err := store.Append(learning.OutcomeRecord{
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

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskCancelQuestionWithStore(context.Background(), store, []string{
			"--session", "sess-123",
			"--request", "req-123",
			"--reason", "operator changed direction",
		}); err != nil {
			t.Fatalf("cmdTaskCancelQuestionWithStore: %v", err)
		}
	})
	if !strings.Contains(stdout, "Question cancelled") || !strings.Contains(stdout, "req-123") {
		t.Fatalf("stdout = %q, want cancel summary", stdout)
	}
	records, err := store.Recent(0)
	if err != nil {
		t.Fatalf("Recent outcomes: %v", err)
	}
	if pending := learning.PendingUserQuestions(records, "sess-123", 10); len(pending) != 0 {
		t.Fatalf("pending = %+v, want CLI cancel receipt to close req-123", pending)
	}
}

func TestCmdTaskShowMissingArgs(t *testing.T) {
	err := cmdTaskShow(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("cmdTaskShow() err = %v, want usage error", err)
	}
}

func TestCmdTaskShowInvalidID(t *testing.T) {
	err := cmdTaskShow(context.Background(), []string{"abc"})
	if err == nil || !strings.Contains(err.Error(), "invalid task ID") {
		t.Fatalf("cmdTaskShow(abc) err = %v, want invalid task ID", err)
	}
}

func TestCmdTaskMonitorWithQueueShowsSnapshot(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "monitor me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	if _, err := queue.UpdateAnnotation(ctx, task.ID, "working", "halfway"); err != nil {
		t.Fatalf("UpdateAnnotation: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskMonitorWithQueue(ctx, queue, []string{fmt.Sprint(id)}); err != nil {
			t.Fatalf("cmdTaskMonitorWithQueue: %v", err)
		}
	})
	for _, want := range []string{"ID:", "Status:       running", "Retrieval:    snapshot", "Observation:  mode=snapshot", "Progress:     working", "Summary:      halfway"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestCmdTaskMonitorWithQueueRendersStructuredProgress(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "monitor structured progress", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	rawProgress := daemon.EncodeProgressEvent(daemon.ToolPhaseProgressEvent("bash", "go test ./cmd/elnath", "running", 42, false))
	if _, err := queue.UpdateAnnotation(ctx, task.ID, rawProgress, "tests running"); err != nil {
		t.Fatalf("UpdateAnnotation: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskMonitorWithQueue(ctx, queue, []string{fmt.Sprint(id)}); err != nil {
			t.Fatalf("cmdTaskMonitorWithQueue: %v", err)
		}
	})
	if !strings.Contains(stdout, "Progress:     bash: go test ./cmd/elnath (running)") {
		t.Fatalf("stdout = %q, want rendered structured progress", stdout)
	}
	if strings.Contains(stdout, `"version"`) {
		t.Fatalf("stdout = %q, should not dump raw progress JSON", stdout)
	}
}

func TestCmdTaskMonitorWithQueueJSONWaitsForUpdate(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "wait me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	initial, err := queue.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get initial: %v", err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = queue.UpdateAnnotation(ctx, task.ID, "changed progress", "changed summary")
	}()

	stdout, _ := captureOutput(t, func() {
		err := cmdTaskMonitorWithQueue(ctx, queue, []string{
			fmt.Sprint(id),
			"--json",
			"--wait",
			"--since-updated-at", initial.UpdatedAt.Format(time.RFC3339Nano),
			"--timeout-ms", "500",
		})
		if err != nil {
			t.Fatalf("cmdTaskMonitorWithQueue: %v", err)
		}
	})
	for _, want := range []string{`"retrieval_status":"changed"`, `"mode":"wait_for_update"`, `"timeout_ms":500`, `"progress":"changed progress"`, `"summary":"changed summary"`} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestCmdTaskOutputWithQueueReturnsTail(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "output me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	if err := queue.MarkDone(ctx, task.ID, "abcdef", "done summary"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskOutputWithQueue(ctx, queue, []string{fmt.Sprint(id), "--max-chars", "3"}); err != nil {
			t.Fatalf("cmdTaskOutputWithQueue: %v", err)
		}
	})
	for _, want := range []string{"Field:        result", "Truncated:    true", "Content:\ndef"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestCmdTaskOutputWithQueueRendersStructuredProgress(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "output structured progress", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	rawProgress := daemon.EncodeProgressEvent(daemon.ToolPhaseProgressEvent("bash", "go test ./cmd/elnath", "running", 42, false))
	if err := queue.UpdateProgress(ctx, task.ID, rawProgress); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskOutputWithQueue(ctx, queue, []string{fmt.Sprint(id), "--field", "progress"}); err != nil {
			t.Fatalf("cmdTaskOutputWithQueue: %v", err)
		}
	})
	if !strings.Contains(stdout, "Content:\nbash: go test ./cmd/elnath (running)") {
		t.Fatalf("stdout = %q, want rendered structured progress", stdout)
	}
	if strings.Contains(stdout, `"version"`) {
		t.Fatalf("stdout = %q, should not dump raw progress JSON", stdout)
	}
}

func TestCmdTaskStopWithQueueCancelsPendingTask(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "stop me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskStopWithQueue(ctx, queue, []string{fmt.Sprint(id), "--reason", "operator stop", "--json"}); err != nil {
			t.Fatalf("cmdTaskStopWithQueue: %v", err)
		}
	})
	for _, want := range []string{`"accepted":true`, `"terminal":true`, `"status":"failed"`} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
	task, err := queue.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get stopped task: %v", err)
	}
	if task.Status != daemon.StatusFailed || !strings.Contains(task.Result, "operator stop") {
		t.Fatalf("task = %+v, want failed with operator reason", task)
	}
}

func TestCmdTaskStopWithQueuePlainTextShowsAcceptedState(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "stop plain", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskStopWithQueue(ctx, queue, []string{fmt.Sprint(id), "--reason", "operator stop"}); err != nil {
			t.Fatalf("cmdTaskStopWithQueue: %v", err)
		}
	})
	for _, want := range []string{"Accepted:        true", "Terminal:        true", "Status:          failed", "Reason:          operator stop"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestCmdTaskStopWithQueueRejectsRunningTask(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "running stop", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if task, err := queue.Next(ctx); err != nil {
		t.Fatalf("Next: %v", err)
	} else if task == nil {
		t.Fatal("Next returned nil")
	}

	err = cmdTaskStopWithQueue(ctx, queue, []string{fmt.Sprint(id)})
	if err == nil {
		t.Fatal("cmdTaskStopWithQueue running task err = nil, want pending-only error")
	}
	for _, want := range []string{"pending tasks only", "daemon runtime support", "elnath task monitor"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want %q", err.Error(), want)
		}
	}
}

func TestCmdTaskAnswerWithQueueEnqueuesBoundAnswer(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	store := learning.NewOutcomeStore(filepath.Join(t.TempDir(), "outcomes.jsonl"))
	if err := store.Append(learning.OutcomeRecord{
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

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskAnswerWithQueue(ctx, queue, store, []string{
			"--session", "sess-123",
			"--request", "req-123",
			"--answer", "Use main.",
		}); err != nil {
			t.Fatalf("cmdTaskAnswerWithQueue: %v", err)
		}
	})
	if !strings.Contains(stdout, "Answer task:") || !strings.Contains(stdout, "elnath task monitor") {
		t.Fatalf("stdout = %q, want answer enqueue summary", stdout)
	}

	tasks, err := queue.List(ctx)
	if err != nil {
		t.Fatalf("List tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v, want one answer resume task", tasks)
	}
	payload := daemon.ParseTaskPayload(tasks[0].Payload)
	if payload.SessionID != "sess-123" || !strings.Contains(payload.Prompt, "Request ID:\nreq-123") || !strings.Contains(payload.Prompt, "Answer:\nUse main.") {
		t.Fatalf("payload = %+v, want session-bound answer resume", payload)
	}
	records, err := store.Recent(0)
	if err != nil {
		t.Fatalf("Recent outcomes: %v", err)
	}
	if pending := learning.PendingUserQuestions(records, "sess-123", 10); len(pending) != 0 {
		t.Fatalf("pending = %+v, want CLI answer receipt to close req-123", pending)
	}
}

func TestCmdTaskAnswerWithQueueAcceptsChoiceFlag(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	store := learning.NewOutcomeStore(filepath.Join(t.TempDir(), "outcomes.jsonl"))
	if err := store.Append(learning.OutcomeRecord{
		Timestamp: time.Date(2026, 5, 13, 7, 0, 0, 0, time.UTC),
		ControlToolReceipts: []learning.ControlToolReceipt{{
			Tool:          "ask_user_question",
			Action:        "request",
			RequestID:     "req-123",
			SessionID:     "sess-123",
			Question:      "Which branch?",
			Options:       []string{"main", "new"},
			AllowFreeText: false,
		}},
	}); err != nil {
		t.Fatalf("Append outcome: %v", err)
	}

	if err := cmdTaskAnswerWithQueue(ctx, queue, store, []string{
		"--session", "sess-123",
		"--request", "req-123",
		"--choice", "2",
	}); err != nil {
		t.Fatalf("cmdTaskAnswerWithQueue: %v", err)
	}

	tasks, err := queue.List(ctx)
	if err != nil {
		t.Fatalf("List tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v, want one answer resume task", tasks)
	}
	payload := daemon.ParseTaskPayload(tasks[0].Payload)
	if !strings.Contains(payload.Prompt, "Answer:\nnew") {
		t.Fatalf("payload = %+v, want choice normalized to option text", payload)
	}
}

func TestCmdTaskAnswerWithQueueRejectsStaleRequest(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	store := learning.NewOutcomeStore(filepath.Join(t.TempDir(), "outcomes.jsonl"))

	err := cmdTaskAnswerWithQueue(ctx, queue, store, []string{
		"--session", "sess-123",
		"--request", "missing",
		"--answer", "Use main.",
	})
	if err == nil || !strings.Contains(err.Error(), "request_id is not pending for session_id") {
		t.Fatalf("cmdTaskAnswerWithQueue err = %v, want stale request error", err)
	}
	tasks, listErr := queue.List(ctx)
	if listErr != nil {
		t.Fatalf("List tasks: %v", listErr)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no enqueue for stale answer", tasks)
	}
}

func TestCmdTaskHandoffWithQueuePrintsResumeRecap(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	dataDir := t.TempDir()
	sess, err := agent.NewSession(dataDir, identity.Principal{UserID: "tg-77", ProjectID: "elnath", Surface: "telegram"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := sess.AppendMessages([]llm.Message{
		llm.NewUserMessage("continue the roadmap"),
		llm.NewAssistantMessage("working on handoff recap"),
	}); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}
	if err := sess.RecordResume(identity.Principal{UserID: "stello@local", ProjectID: "elnath", Surface: "cli"}); err != nil {
		t.Fatalf("RecordResume: %v", err)
	}

	id, _, err := queue.Enqueue(ctx, "handoff me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil || task.ID != id {
		t.Fatalf("task = %+v, want %d", task, id)
	}
	if err := queue.BindSession(ctx, id, sess.ID); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	if err := queue.MarkDone(ctx, id, "finished result", "done summary"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskHandoffWithQueue(ctx, queue, dataDir, []string{fmt.Sprint(id)}); err != nil {
			t.Fatalf("cmdTaskHandoffWithQueue: %v", err)
		}
	})
	for _, want := range []string{
		"Task handoff",
		"Status:       done",
		"Session:",
		"Resume:       elnath task resume",
		"Messages:     2",
		"Resumes:      1",
		"user: continue the roadmap",
		"assistant: working on handoff recap",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestCmdTaskHandoffWithQueueJSONIncludesRetirement(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	dataDir := t.TempDir()
	sess, err := agent.NewSession(dataDir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := sess.AppendMessage(llm.NewUserMessage("this session wedged")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := sess.RecordRetirement("task_timeout_idle", "post_tool_quiet_timeout", "start_new_session_or_operator_review"); err != nil {
		t.Fatalf("RecordRetirement: %v", err)
	}

	id, _, err := queue.Enqueue(ctx, "handoff me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil || task.ID != id {
		t.Fatalf("task = %+v, want %d", task, id)
	}
	if err := queue.BindSession(ctx, id, sess.ID); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	if err := queue.MarkFailedWithMetadata(ctx, id, "timed out", daemon.TaskFailureMetadata{
		FailureClass:            "task_timeout_idle",
		ShouldRetireSession:     true,
		SessionRetirementReason: "post_tool_quiet_timeout",
		NextAction:              "start_new_session_or_operator_review",
	}); err != nil {
		t.Fatalf("MarkFailedWithMetadata: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskHandoffWithQueue(ctx, queue, dataDir, []string{fmt.Sprint(id), "--json"}); err != nil {
			t.Fatalf("cmdTaskHandoffWithQueue: %v", err)
		}
	})
	for _, want := range []string{
		`"retired": true`,
		`"failure_class": "task_timeout_idle"`,
		`"next_action": "start_new_session_or_operator_review"`,
		`"resume_command": "elnath task resume`,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestCmdTaskHandoffWithQueueRequestRecordsHandoffState(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	dataDir := t.TempDir()
	sess, taskID := seedTaskHandoffFixture(t, ctx, queue, dataDir)

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskHandoffWithQueue(ctx, queue, dataDir, []string{fmt.Sprint(taskID), "--request", "telegram"}); err != nil {
			t.Fatalf("cmdTaskHandoffWithQueue: %v", err)
		}
	})
	for _, want := range []string{
		"Handoff:     requested surface=telegram",
		"Resume:       elnath task resume",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
	status, err := agent.LoadSessionHandoffStatus(dataDir, sess.ID)
	if err != nil {
		t.Fatalf("LoadSessionHandoffStatus: %v", err)
	}
	if status == nil || status.State != "requested" || status.Surface != "telegram" {
		t.Fatalf("handoff status = %+v, want requested telegram", status)
	}
}

func TestCmdTaskHandoffWithQueueRecordsLifecycleState(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	dataDir := t.TempDir()
	sess, taskID := seedTaskHandoffFixture(t, ctx, queue, dataDir)

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskHandoffWithQueue(ctx, queue, dataDir, []string{
			fmt.Sprint(taskID),
			"--state", "claimed",
			"--surface", "cli",
			"--reason", "claimed by local operator",
			"--json",
		}); err != nil {
			t.Fatalf("cmdTaskHandoffWithQueue: %v", err)
		}
	})
	for _, want := range []string{
		`"handoff": {`,
		`"state": "claimed"`,
		`"surface": "cli"`,
		`"reason": "claimed by local operator"`,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
	status, err := agent.LoadSessionHandoffStatus(dataDir, sess.ID)
	if err != nil {
		t.Fatalf("LoadSessionHandoffStatus: %v", err)
	}
	if status == nil || status.State != "claimed" || status.Surface != "cli" || status.Reason != "claimed by local operator" {
		t.Fatalf("handoff status = %+v, want claimed cli", status)
	}
	if status.Principal.Surface != "cli" {
		t.Fatalf("handoff principal = %+v, want cli operator principal", status.Principal)
	}
}

func TestCmdTaskHandoffWithQueueMarkdownOutput(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	dataDir := t.TempDir()
	sess, taskID := seedTaskHandoffFixture(t, ctx, queue, dataDir)

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskHandoffWithQueue(ctx, queue, dataDir, []string{fmt.Sprint(taskID), "--markdown"}); err != nil {
			t.Fatalf("cmdTaskHandoffWithQueue: %v", err)
		}
	})
	for _, want := range []string{
		"# Task Handoff",
		"- Task ID: " + fmt.Sprint(taskID),
		"- Session: " + sess.ID,
		"## Last Messages",
		"- user: finish product runtime milestone",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestCmdTaskHandoffWithQueueSaveWritesMarkdown(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	dataDir := t.TempDir()
	_, taskID := seedTaskHandoffFixture(t, ctx, queue, dataDir)

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskHandoffWithQueue(ctx, queue, dataDir, []string{fmt.Sprint(taskID), "--save"}); err != nil {
			t.Fatalf("cmdTaskHandoffWithQueue: %v", err)
		}
	})
	if !strings.Contains(stdout, "Saved handoff:") {
		t.Fatalf("stdout = %q, want saved path", stdout)
	}
	path := strings.TrimSpace(strings.TrimPrefix(stdout, "Saved handoff:"))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile saved handoff: %v", err)
	}
	if !strings.Contains(string(body), "# Task Handoff") || !strings.Contains(string(body), "finish product runtime milestone") {
		t.Fatalf("saved body = %q, want markdown handoff", string(body))
	}
}

func seedTaskHandoffFixture(t *testing.T, ctx context.Context, queue *daemon.Queue, dataDir string) (*agent.Session, int64) {
	t.Helper()
	sess, err := agent.NewSession(dataDir, identity.Principal{UserID: "tg-77", ProjectID: "elnath", Surface: "telegram"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := sess.AppendMessages([]llm.Message{
		llm.NewUserMessage("finish product runtime milestone"),
		llm.NewAssistantMessage("created handoff recap command"),
	}); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	id, _, err := queue.Enqueue(ctx, "handoff me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if task, err := queue.Next(ctx); err != nil {
		t.Fatalf("Next: %v", err)
	} else if task == nil {
		t.Fatal("Next returned nil")
	}
	if err := queue.BindSession(ctx, id, sess.ID); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	if err := queue.MarkDone(ctx, id, "finished result", "done summary"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	return sess, id
}

func TestBuildTaskResumeHandoffContextIncludesCompactRecap(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	dataDir := t.TempDir()
	sess, id := seedTaskHandoffFixture(t, ctx, queue, dataDir)

	got, err := buildTaskResumeHandoffContext(ctx, queue, dataDir, id)
	if err != nil {
		t.Fatalf("buildTaskResumeHandoffContext: %v", err)
	}
	for _, want := range []string{
		"task_id: 1",
		"status: done",
		"session_id: " + sess.ID,
		"summary: done summary",
		"user: finish product runtime milestone",
		"assistant: created handoff recap command",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("context = %q, want %q", got, want)
		}
	}
}

func TestConsumeTaskResumeHandoffContextOnlyOnce(t *testing.T) {
	pending := "task_id: 42"
	ctx := consumeTaskResumeHandoffContext(context.Background(), &pending)
	if got := taskResumeHandoffContextFromContext(ctx); got != "task_id: 42" {
		t.Fatalf("first context = %q, want handoff", got)
	}
	if pending != "" {
		t.Fatalf("pending = %q, want consumed", pending)
	}
	ctx = consumeTaskResumeHandoffContext(context.Background(), &pending)
	if got := taskResumeHandoffContextFromContext(ctx); got != "" {
		t.Fatalf("second context = %q, want empty", got)
	}
}

func TestTaskResumeHandoffContextRequested(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "none", args: []string{"elnath", "run"}, want: false},
		{name: "task resume context", args: []string{"elnath", "run", "--task-resume-handoff-context", "42"}, want: true},
		{name: "continue task", args: []string{"elnath", "run", "--continue-task", "42"}, want: true},
		{name: "bare flag", args: []string{"elnath", "run", "--task-resume-handoff-context"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskResumeHandoffContextRequested(tt.args); got != tt.want {
				t.Fatalf("taskResumeHandoffContextRequested(%v) = %t, want %t", tt.args, got, tt.want)
			}
		})
	}
}

func TestCmdTaskResumeMissingArgs(t *testing.T) {
	err := cmdTaskResume(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("cmdTaskResume() err = %v, want usage error", err)
	}
}

func TestCmdTaskResumeInvalidID(t *testing.T) {
	err := cmdTaskResume(context.Background(), []string{"xyz"})
	if err == nil || !strings.Contains(err.Error(), "invalid task ID") {
		t.Fatalf("cmdTaskResume(xyz) err = %v, want invalid task ID", err)
	}
}

func TestResolveTaskSession(t *testing.T) {
	db := openTestQueueDB(t)
	queue, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	ctx := context.Background()

	id, _, err := queue.Enqueue(ctx, "test task", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// No session bound yet.
	_, err = resolveTaskSession(db, id)
	if err == nil || !strings.Contains(err.Error(), "no session bound") {
		t.Fatalf("resolveTaskSession (no session) err = %v, want no session", err)
	}

	// Bind a session and resolve.
	if err := queue.BindSession(ctx, id, "sess-abc-123"); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	sid, err := resolveTaskSession(db, id)
	if err != nil {
		t.Fatalf("resolveTaskSession: %v", err)
	}
	if sid != "sess-abc-123" {
		t.Fatalf("resolveTaskSession = %q, want %q", sid, "sess-abc-123")
	}
}

func TestResolveTaskSessionNotFound(t *testing.T) {
	db := openTestQueueDB(t)
	_, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	_, err = resolveTaskSession(db, 999)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("resolveTaskSession(999) err = %v, want not found", err)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"short", 10, "short"},
		{"exactly ten", 11, "exactly ten"},
		{"this is a long string", 10, "this is..."},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}

func TestFormatTimestamp(t *testing.T) {
	got := formatTimestamp(zeroTime())
	if got != "-" {
		t.Errorf("formatTimestamp(zero) = %q, want %q", got, "-")
	}
}

func TestCmdTaskAnswerWithQueueRejectsTimedOutRequest(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	store := learning.NewOutcomeStore(filepath.Join(t.TempDir(), "outcomes.jsonl"))
	if err := store.Append(learning.OutcomeRecord{
		Timestamp: time.Now().Add(-2 * time.Second),
		ControlToolReceipts: []learning.ControlToolReceipt{{
			Tool:           "ask_user_question",
			Action:         "request",
			RequestID:      "req-timeout",
			SessionID:      "sess-123",
			Question:       "Still needed?",
			TimeoutSeconds: 1,
		}},
	}); err != nil {
		t.Fatalf("Append outcome: %v", err)
	}

	err := cmdTaskAnswerWithQueue(ctx, queue, store, []string{
		"--session", "sess-123",
		"--request", "req-timeout",
		"--answer", "Use main.",
	})
	if err == nil || !strings.Contains(err.Error(), "request_id is not pending for session_id") {
		t.Fatalf("cmdTaskAnswerWithQueue err = %v, want timed-out request rejection", err)
	}
	tasks, listErr := queue.List(ctx)
	if listErr != nil {
		t.Fatalf("List tasks: %v", listErr)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no enqueue for timed-out answer", tasks)
	}
}

func newCmdTaskTestQueue(t *testing.T) *daemon.Queue {
	t.Helper()
	queue, err := daemon.NewQueue(openTestQueueDB(t))
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	return queue
}
