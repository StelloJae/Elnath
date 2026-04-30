package memory

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/learning"

	_ "modernc.org/sqlite"
)

func TestMemoryGate_AllowsLearningLessonAfterPassedVerification(t *testing.T) {
	ctx := context.Background()
	db, store := newMemoryTestStore(t)
	task := createMemoryTestTask(t, ctx, store)
	run := createMemoryTestVerification(t, ctx, store, task.ID, agentic.VerificationVerdictPassed)
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))

	added, update, err := NewGate(store).AppendLearningLesson(ctx, LearningWriteRequest{
		TaskID:            task.ID,
		VerificationRunID: run.ID,
		Source:            SourceAgentic,
	}, lessonStore, learning.Lesson{
		Text:       "verified lesson",
		Topic:      "agentic-pr9",
		Source:     "test",
		Confidence: "high",
	})
	if err != nil {
		t.Fatalf("AppendLearningLesson: %v", err)
	}
	if !added {
		t.Fatalf("AppendLearningLesson added=false, want true")
	}
	if update.Status != agentic.MemoryUpdateStatusApplied || update.VerificationRunID != run.ID || update.Source != SourceAgentic || update.AppliedAt.Time.IsZero() {
		t.Fatalf("unexpected memory update: %+v", update)
	}
	if update.Reason == "" {
		t.Fatalf("applied update reason is empty: %+v", update)
	}
	if got := mustLessonCount(t, lessonStore); got != 1 {
		t.Fatalf("lesson count = %d, want 1", got)
	}
	assertMemoryTableCount(t, db, "memory_updates", 1)
}

func TestMemoryGate_BlocksLearningLessonAfterFailedVerification(t *testing.T) {
	ctx := context.Background()
	db, store := newMemoryTestStore(t)
	task := createMemoryTestTask(t, ctx, store)
	run := createMemoryTestVerification(t, ctx, store, task.ID, agentic.VerificationVerdictFailed)
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))

	added, update, err := NewGate(store).AppendLearningLesson(ctx, LearningWriteRequest{
		TaskID: task.ID,
		Source: SourceAgentic,
	}, lessonStore, learning.Lesson{Text: "should not persist", Source: "test"})
	if err != nil {
		t.Fatalf("AppendLearningLesson: %v", err)
	}
	if added {
		t.Fatalf("AppendLearningLesson added=true, want false")
	}
	if update.Status != agentic.MemoryUpdateStatusBlocked || update.VerificationRunID != run.ID {
		t.Fatalf("unexpected blocked update: %+v", update)
	}
	if got := mustLessonCount(t, lessonStore); got != 0 {
		t.Fatalf("lesson count = %d, want 0", got)
	}
	assertMemoryTableCount(t, db, "memory_updates", 1)
}

func TestMemoryGate_BlocksLearningLessonWithoutVerification(t *testing.T) {
	ctx := context.Background()
	db, store := newMemoryTestStore(t)
	task := createMemoryTestTask(t, ctx, store)
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))

	added, update, err := NewGate(store).AppendLearningLesson(ctx, LearningWriteRequest{
		TaskID: task.ID,
		Source: SourceAgentic,
	}, lessonStore, learning.Lesson{Text: "no verifier", Source: "test"})
	if err != nil {
		t.Fatalf("AppendLearningLesson: %v", err)
	}
	if added {
		t.Fatalf("AppendLearningLesson added=true, want false")
	}
	if update.Status != agentic.MemoryUpdateStatusBlocked || update.Reason == "" {
		t.Fatalf("unexpected missing-verification update: %+v", update)
	}
	if got := mustLessonCount(t, lessonStore); got != 0 {
		t.Fatalf("lesson count = %d, want 0", got)
	}
	assertMemoryTableCount(t, db, "memory_updates", 1)
}

func TestMemoryGate_BlockedDecisionIsIdempotentForSamePayloadHash(t *testing.T) {
	ctx := context.Background()
	db, store := newMemoryTestStore(t)
	task := createMemoryTestTask(t, ctx, store)
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	req := LearningWriteRequest{
		TaskID:      task.ID,
		Source:      SourceAgentic,
		PayloadHash: "blocked-payload-hash",
	}

	added1, update1, err := NewGate(store).AppendLearningLesson(ctx, req, lessonStore, learning.Lesson{Text: "blocked", Source: "test"})
	if err != nil {
		t.Fatalf("AppendLearningLesson first: %v", err)
	}
	added2, update2, err := NewGate(store).AppendLearningLesson(ctx, req, lessonStore, learning.Lesson{Text: "blocked", Source: "test"})
	if err != nil {
		t.Fatalf("AppendLearningLesson second: %v", err)
	}
	if added1 || added2 {
		t.Fatalf("added first/second = %v/%v, want false/false", added1, added2)
	}
	if update1 == nil || update2 == nil || update1.ID != update2.ID {
		t.Fatalf("blocked updates differ: first=%+v second=%+v", update1, update2)
	}
	if got := mustLessonCount(t, lessonStore); got != 0 {
		t.Fatalf("lesson count = %d, want 0", got)
	}
	assertMemoryTableCount(t, db, "memory_updates", 1)
}

func TestMemoryGate_AllowsExplicitUserMemoryWithoutVerification(t *testing.T) {
	ctx := context.Background()
	db, store := newMemoryTestStore(t)
	task := createMemoryTestTask(t, ctx, store)
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))

	added, update, err := NewGate(store).AppendLearningLesson(ctx, LearningWriteRequest{
		TaskID: task.ID,
		Source: SourceUserExplicit,
	}, lessonStore, learning.Lesson{Text: "user asked to remember", Source: "user"})
	if err != nil {
		t.Fatalf("AppendLearningLesson: %v", err)
	}
	if !added {
		t.Fatalf("AppendLearningLesson added=false, want true")
	}
	if update.Status != agentic.MemoryUpdateStatusApplied || update.Source != SourceUserExplicit || update.VerificationRunID != 0 {
		t.Fatalf("unexpected explicit-user update: %+v", update)
	}
	if update.Reason == "" {
		t.Fatalf("explicit-user applied reason is empty: %+v", update)
	}
	if got := mustLessonCount(t, lessonStore); got != 1 {
		t.Fatalf("lesson count = %d, want 1", got)
	}
	assertMemoryTableCount(t, db, "memory_updates", 1)
}

func TestMemoryGate_UsesLatestVerificationRun(t *testing.T) {
	ctx := context.Background()
	_, store := newMemoryTestStore(t)
	task := createMemoryTestTask(t, ctx, store)
	createMemoryTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, time.Unix(10, 0))
	latest := createMemoryTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictFailed, time.Unix(20, 0))
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))

	added, update, err := NewGate(store).AppendLearningLesson(ctx, LearningWriteRequest{
		TaskID: task.ID,
		Source: SourceAgentic,
	}, lessonStore, learning.Lesson{Text: "latest failed", Source: "test"})
	if err != nil {
		t.Fatalf("AppendLearningLesson: %v", err)
	}
	if added {
		t.Fatalf("AppendLearningLesson added=true, want false")
	}
	if update.VerificationRunID != latest.ID || update.Status != agentic.MemoryUpdateStatusBlocked {
		t.Fatalf("gate did not use latest run: %+v latest=%+v", update, latest)
	}
}

func TestMemoryGate_DoesNotChangeLegacyLearningWithoutAgenticContext(t *testing.T) {
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))

	added, err := AppendLegacyLearningLesson(lessonStore, learning.Lesson{Text: "legacy lesson", Source: "legacy"})
	if err != nil {
		t.Fatalf("AppendLegacyLearningLesson: %v", err)
	}
	if !added {
		t.Fatalf("AppendLegacyLearningLesson added=false, want true")
	}
	if got := mustLessonCount(t, lessonStore); got != 1 {
		t.Fatalf("lesson count = %d, want 1", got)
	}
}

func TestMemoryGate_DoesNotGateQueueMarkDone(t *testing.T) {
	ctx := context.Background()
	db, store := newMemoryTestStore(t)
	q, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	queueID, existed, err := q.Enqueue(ctx, "memory gate queue task", "")
	if err != nil || existed {
		t.Fatalf("Enqueue: id=%d existed=%v err=%v", queueID, existed, err)
	}
	queueTask, err := q.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if queueTask == nil || queueTask.ID != queueID {
		t.Fatalf("Next task = %+v, want id %d", queueTask, queueID)
	}
	task := createMemoryTestTaskWithQueue(t, ctx, store, queueTask.ID)
	createMemoryTestVerification(t, ctx, store, task.ID, agentic.VerificationVerdictFailed)

	if err := q.MarkDone(ctx, queueTask.ID, "ok", "queue completion remains independent"); err != nil {
		t.Fatalf("MarkDone with failed verifier: %v", err)
	}
}

func TestMemoryGate_DoesNotCreateFollowups(t *testing.T) {
	ctx := context.Background()
	db, store := newMemoryTestStore(t)
	task := createMemoryTestTask(t, ctx, store)
	createMemoryTestVerification(t, ctx, store, task.ID, agentic.VerificationVerdictFailed)
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))

	_, _, err := NewGate(store).AppendLearningLesson(ctx, LearningWriteRequest{
		TaskID: task.ID,
		Source: SourceAgentic,
	}, lessonStore, learning.Lesson{Text: "blocked", Source: "test"})
	if err != nil {
		t.Fatalf("AppendLearningLesson: %v", err)
	}
	assertMemoryTableCount(t, db, "followups", 0)
}

func TestMemoryGate_DoesNotCreatePolicyApprovalsOrReceipts(t *testing.T) {
	ctx := context.Background()
	db, store := newMemoryTestStore(t)
	if _, err := daemon.NewApprovalStore(db); err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	task := createMemoryTestTask(t, ctx, store)
	createMemoryTestVerification(t, ctx, store, task.ID, agentic.VerificationVerdictPassed)
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))

	_, _, err := NewGate(store).AppendLearningLesson(ctx, LearningWriteRequest{
		TaskID: task.ID,
		Source: SourceAgentic,
	}, lessonStore, learning.Lesson{Text: "allowed", Source: "test"})
	if err != nil {
		t.Fatalf("AppendLearningLesson: %v", err)
	}
	for _, table := range []string{"policy_decisions", "approval_requests", "tool_action_receipts"} {
		assertMemoryTableCount(t, db, table, 0)
	}
}

func TestMemoryGate_FailClosedWhenMemoryLedgerUnavailable(t *testing.T) {
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))

	added, update, err := NewGate(nil).AppendLearningLesson(context.Background(), LearningWriteRequest{
		TaskID: 1,
		Source: SourceAgentic,
	}, lessonStore, learning.Lesson{Text: "must not write", Source: "test"})
	if err == nil {
		t.Fatalf("AppendLearningLesson error=nil, want failure")
	}
	if added || update != nil {
		t.Fatalf("AppendLearningLesson added/update = %v/%+v, want false/nil", added, update)
	}
	if got := mustLessonCount(t, lessonStore); got != 0 {
		t.Fatalf("lesson count = %d, want 0", got)
	}
}

func TestMemoryGate_IdempotentForSamePayloadHash(t *testing.T) {
	ctx := context.Background()
	db, store := newMemoryTestStore(t)
	task := createMemoryTestTask(t, ctx, store)
	createMemoryTestVerification(t, ctx, store, task.ID, agentic.VerificationVerdictPassed)
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	req := LearningWriteRequest{
		TaskID:      task.ID,
		Source:      SourceAgentic,
		PayloadHash: "fixed-payload-hash",
	}

	added1, update1, err := NewGate(store).AppendLearningLesson(ctx, req, lessonStore, learning.Lesson{Text: "same", Source: "test"})
	if err != nil {
		t.Fatalf("AppendLearningLesson first: %v", err)
	}
	added2, update2, err := NewGate(store).AppendLearningLesson(ctx, req, lessonStore, learning.Lesson{Text: "same", Source: "test"})
	if err != nil {
		t.Fatalf("AppendLearningLesson second: %v", err)
	}
	if !added1 || added2 {
		t.Fatalf("added first/second = %v/%v, want true/false", added1, added2)
	}
	if update1.ID != update2.ID {
		t.Fatalf("updates differ: first=%+v second=%+v", update1, update2)
	}
	if got := mustLessonCount(t, lessonStore); got != 1 {
		t.Fatalf("lesson count = %d, want 1", got)
	}
	assertMemoryTableCount(t, db, "memory_updates", 1)
}

func TestMemoryGate_PendingDecisionIsIdempotentForSamePayloadHash(t *testing.T) {
	ctx := context.Background()
	db, store := newMemoryTestStore(t)
	task := createMemoryTestTask(t, ctx, store)
	run := createMemoryTestVerification(t, ctx, store, task.ID, agentic.VerificationVerdictPassed)
	req := Request{
		TaskID:      task.ID,
		Target:      TargetLearningLesson,
		Operation:   OperationAppend,
		PayloadHash: "pending-payload-hash",
		Source:      SourceAgentic,
	}

	decision1, err := NewGate(store).Decide(ctx, req)
	if err != nil {
		t.Fatalf("Decide first: %v", err)
	}
	decision2, err := NewGate(store).Decide(ctx, req)
	if err != nil {
		t.Fatalf("Decide second: %v", err)
	}
	if !decision1.Allowed || !decision2.Allowed {
		t.Fatalf("allowed first/second = %v/%v, want true/true", decision1.Allowed, decision2.Allowed)
	}
	if decision1.Update == nil || decision2.Update == nil || decision1.Update.ID != decision2.Update.ID {
		t.Fatalf("pending updates differ: first=%+v second=%+v", decision1.Update, decision2.Update)
	}
	if decision2.Update.Status != agentic.MemoryUpdateStatusPending || decision2.Update.VerificationRunID != run.ID {
		t.Fatalf("unexpected pending update: %+v", decision2.Update)
	}
	assertMemoryTableCount(t, db, "memory_updates", 1)
}

func TestMemoryGate_IdempotentForSameLessonIgnoresCreatedAt(t *testing.T) {
	ctx := context.Background()
	db, store := newMemoryTestStore(t)
	task := createMemoryTestTask(t, ctx, store)
	createMemoryTestVerification(t, ctx, store, task.ID, agentic.VerificationVerdictPassed)
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	req := LearningWriteRequest{
		TaskID: task.ID,
		Source: SourceAgentic,
	}

	added1, update1, err := NewGate(store).AppendLearningLesson(ctx, req, lessonStore, learning.Lesson{
		Text:    "same stable lesson",
		Source:  "test",
		Created: time.Unix(1, 0),
	})
	if err != nil {
		t.Fatalf("AppendLearningLesson first: %v", err)
	}
	added2, update2, err := NewGate(store).AppendLearningLesson(ctx, req, lessonStore, learning.Lesson{
		Text:    "same stable lesson",
		Source:  "test",
		Created: time.Unix(2, 0),
	})
	if err != nil {
		t.Fatalf("AppendLearningLesson second: %v", err)
	}
	if !added1 || added2 {
		t.Fatalf("added first/second = %v/%v, want true/false", added1, added2)
	}
	if update1.ID != update2.ID {
		t.Fatalf("updates differ: first=%+v second=%+v", update1, update2)
	}
	assertMemoryTableCount(t, db, "memory_updates", 1)
}

func TestMemoryGate_PayloadHashUsesRedactedLesson(t *testing.T) {
	ctx := context.Background()
	_, store := newMemoryTestStore(t)
	task := createMemoryTestTask(t, ctx, store)
	createMemoryTestVerification(t, ctx, store, task.ID, agentic.VerificationVerdictPassed)
	lessonStore := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	lesson := learning.Lesson{
		Text:       "token SECRET-123 should be redacted",
		Topic:      "SECRET-123",
		Source:     "test",
		Rationale:  "because SECRET-123",
		Evidence:   []string{"SECRET-123"},
		Confidence: "high",
	}
	redact := func(s string) string {
		if s == "" {
			return s
		}
		return "redacted"
	}

	_, update, err := NewGate(store).AppendLearningLesson(ctx, LearningWriteRequest{
		TaskID: task.ID,
		Source: SourceAgentic,
		Redact: redact,
	}, lessonStore, lesson)
	if err != nil {
		t.Fatalf("AppendLearningLesson: %v", err)
	}
	rawHash, err := hashLesson(lesson, nil)
	if err != nil {
		t.Fatalf("hashLesson raw: %v", err)
	}
	redactedHash, err := hashLesson(lesson, redact)
	if err != nil {
		t.Fatalf("hashLesson redacted: %v", err)
	}
	if update.PayloadHash == rawHash {
		t.Fatalf("payload hash used raw lesson hash: %s", update.PayloadHash)
	}
	if update.PayloadHash != redactedHash {
		t.Fatalf("payload hash = %s, want redacted hash %s", update.PayloadHash, redactedHash)
	}
}

func TestMemoryGate_RecordFailureObservable(t *testing.T) {
	ctx := context.Background()
	db, store := newMemoryTestStore(t)
	task := createMemoryTestTask(t, ctx, store)
	createMemoryTestVerification(t, ctx, store, task.ID, agentic.VerificationVerdictPassed)
	writerErr := errors.New("write target failed")

	added, update, err := NewGate(store).AppendLearningLesson(ctx, LearningWriteRequest{
		TaskID: task.ID,
		Source: SourceAgentic,
	}, failingLearningWriter{err: writerErr}, learning.Lesson{Text: "will fail", Source: "test"})
	if !errors.Is(err, writerErr) {
		t.Fatalf("AppendLearningLesson err = %v, want %v", err, writerErr)
	}
	if added {
		t.Fatalf("AppendLearningLesson added=true, want false")
	}
	if update == nil || update.Status != agentic.MemoryUpdateStatusFailed || update.Reason == "" {
		t.Fatalf("unexpected failed update: %+v", update)
	}
	assertMemoryTableCount(t, db, "memory_updates", 1)
}

type failingLearningWriter struct {
	err error
}

func (w failingLearningWriter) AppendNew(learning.Lesson) (bool, error) {
	return false, w.err
}

func newMemoryTestStore(t *testing.T) (*sql.DB, *agentic.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if err := agentic.InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return db, agentic.NewStore(db)
}

func createMemoryTestTask(t *testing.T, ctx context.Context, store *agentic.Store) *agentic.AgenticTask {
	t.Helper()
	return createMemoryTestTaskWithQueue(t, ctx, store, 0)
}

func createMemoryTestTaskWithQueue(t *testing.T, ctx context.Context, store *agentic.Store, queueTaskID int64) *agentic.AgenticTask {
	t.Helper()
	task, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		QueueTaskID:        queueTaskID,
		Title:              "Memory gate task",
		Prompt:             "Gate memory updates.",
		Status:             agentic.TaskStatusSucceeded,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationVerdictPassed,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return task
}

func createMemoryTestVerification(t *testing.T, ctx context.Context, store *agentic.Store, taskID int64, verdict string) *agentic.VerificationRun {
	t.Helper()
	return createMemoryTestVerificationAt(t, ctx, store, taskID, verdict, time.Now().UTC())
}

func createMemoryTestVerificationAt(t *testing.T, ctx context.Context, store *agentic.Store, taskID int64, verdict string, createdAt time.Time) *agentic.VerificationRun {
	t.Helper()
	run, err := store.CreateVerificationRun(ctx, agentic.VerificationRun{
		TaskID:           taskID,
		CriteriaJSON:     `{"kind":"memory-gate"}`,
		EvidenceRefsJSON: `[]`,
		Verdict:          verdict,
		Reason:           "test verifier result",
		CreatedAt:        createdAt,
	})
	if err != nil {
		t.Fatalf("CreateVerificationRun: %v", err)
	}
	return run
}

func mustLessonCount(t *testing.T, store *learning.Store) int {
	t.Helper()
	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List lessons: %v", err)
	}
	return len(lessons)
}

func assertMemoryTableCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
