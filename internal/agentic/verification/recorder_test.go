package verification_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/agentic/verification"

	_ "modernc.org/sqlite"
)

func TestVerificationStore_CreateRunPersistsVerdictAndReason(t *testing.T) {
	ctx := context.Background()
	_, store := newVerificationTestStore(t)
	task := createVerificationTestTask(t, ctx, store)
	recorder := verification.NewRecorder(store)

	run, err := recorder.Record(ctx, verification.RunRequest{
		TaskID:           task.ID,
		CriteriaJSON:     `{"criteria":["correctness"]}`,
		EvidenceRefsJSON: `{"refs":[{"type":"tool_result","hash":"sha256:abc"}]}`,
		Verdict:          agentic.VerificationVerdictPassed,
		Reason:           "all checks passed",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := store.GetVerificationRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetVerificationRun: %v", err)
	}
	if got.TaskID != task.ID || got.Verdict != agentic.VerificationVerdictPassed || got.Reason != "all checks passed" || got.CreatedAt.IsZero() {
		t.Fatalf("unexpected verification run: %+v", got)
	}
}

func TestVerificationStore_EvidenceRefsRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, store := newVerificationTestStore(t)
	task := createVerificationTestTask(t, ctx, store)
	recorder := verification.NewRecorder(store)
	evidenceRefs := `{"refs":[{"type":"receipt","id":42},{"type":"hash","value":"sha256:def"}]}`

	run, err := recorder.Record(ctx, verification.RunRequest{
		TaskID:           task.ID,
		CriteriaJSON:     `{"criteria":["verification"]}`,
		EvidenceRefsJSON: evidenceRefs,
		Verdict:          agentic.VerificationVerdictFailed,
		Reason:           "missing focused test evidence",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	if run.EvidenceRefsJSON != evidenceRefs {
		t.Fatalf("EvidenceRefsJSON = %q, want %q", run.EvidenceRefsJSON, evidenceRefs)
	}
}

func TestVerificationStore_CriteriaRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, store := newVerificationTestStore(t)
	task := createVerificationTestTask(t, ctx, store)
	recorder := verification.NewRecorder(store)
	criteria := `{"criteria":["correctness","completeness","verification"],"source":"ralph"}`

	run, err := recorder.Record(ctx, verification.RunRequest{
		TaskID:           task.ID,
		CriteriaJSON:     criteria,
		EvidenceRefsJSON: `{"refs":[]}`,
		Verdict:          agentic.VerificationVerdictPassed,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	if run.CriteriaJSON != criteria {
		t.Fatalf("CriteriaJSON = %q, want %q", run.CriteriaJSON, criteria)
	}
}

func TestVerificationStore_InconclusiveVerdictSupported(t *testing.T) {
	ctx := context.Background()
	_, store := newVerificationTestStore(t)
	task := createVerificationTestTask(t, ctx, store)
	recorder := verification.NewRecorder(store)

	run, err := recorder.Record(ctx, verification.RunRequest{
		TaskID:           task.ID,
		CriteriaJSON:     `{"criteria":["verification"]}`,
		EvidenceRefsJSON: `{"refs":[]}`,
		Verdict:          agentic.VerificationVerdictInconclusive,
		Reason:           "evidence was insufficient",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if run.Verdict != agentic.VerificationVerdictInconclusive {
		t.Fatalf("Verdict = %q, want %q", run.Verdict, agentic.VerificationVerdictInconclusive)
	}
}

func TestVerificationRecorder_RecordsRunForAgenticTask(t *testing.T) {
	ctx := context.Background()
	_, store := newVerificationTestStore(t)
	task := createVerificationTestTask(t, ctx, store)
	recorder := verification.NewRecorder(store)

	run, err := recorder.Record(ctx, verification.RunRequest{
		TaskID:           task.ID,
		CriteriaJSON:     `{"criteria":["correctness"]}`,
		EvidenceRefsJSON: `{"refs":[]}`,
		Verdict:          agentic.VerificationVerdictPassed,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if run.TaskID != task.ID {
		t.Fatalf("TaskID = %d, want %d", run.TaskID, task.ID)
	}
}

func TestVerificationRecorder_AllowsMissingVerifierActor(t *testing.T) {
	ctx := context.Background()
	_, store := newVerificationTestStore(t)
	task := createVerificationTestTask(t, ctx, store)
	recorder := verification.NewRecorder(store)

	run, err := recorder.Record(ctx, verification.RunRequest{
		TaskID:           task.ID,
		CriteriaJSON:     `{"criteria":["correctness"]}`,
		EvidenceRefsJSON: `{"refs":[]}`,
		Verdict:          agentic.VerificationVerdictPassed,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if run.VerifierActorID != 0 {
		t.Fatalf("VerifierActorID = %d, want 0", run.VerifierActorID)
	}
}

func TestVerificationRecorder_DoesNotCreateMemoryUpdates(t *testing.T) {
	ctx := context.Background()
	db, store := newVerificationTestStore(t)
	task := createVerificationTestTask(t, ctx, store)
	recorder := verification.NewRecorder(store)

	if _, err := recorder.Record(ctx, verification.RunRequest{
		TaskID:           task.ID,
		CriteriaJSON:     `{"criteria":["correctness"]}`,
		EvidenceRefsJSON: `{"refs":[]}`,
		Verdict:          agentic.VerificationVerdictPassed,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if got := tableCount(t, db, "memory_updates"); got != 0 {
		t.Fatalf("memory_updates count = %d, want 0", got)
	}
}

func TestVerificationRecorder_DoesNotCreateFollowups(t *testing.T) {
	ctx := context.Background()
	db, store := newVerificationTestStore(t)
	task := createVerificationTestTask(t, ctx, store)
	recorder := verification.NewRecorder(store)

	if _, err := recorder.Record(ctx, verification.RunRequest{
		TaskID:           task.ID,
		CriteriaJSON:     `{"criteria":["correctness"]}`,
		EvidenceRefsJSON: `{"refs":[]}`,
		Verdict:          agentic.VerificationVerdictPassed,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if got := tableCount(t, db, "followups"); got != 0 {
		t.Fatalf("followups count = %d, want 0", got)
	}
}

func TestVerificationRecorder_DoesNotGateQueueCompletion(t *testing.T) {
	ctx := context.Background()
	_, store := newVerificationTestStore(t)
	task := createVerificationTestTask(t, ctx, store)
	recorder := verification.NewRecorder(store)

	if _, err := recorder.Record(ctx, verification.RunRequest{
		TaskID:           task.ID,
		CriteriaJSON:     `{"criteria":["correctness"]}`,
		EvidenceRefsJSON: `{"refs":[]}`,
		Verdict:          agentic.VerificationVerdictFailed,
		Reason:           "verification failed",
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := store.GetAgenticTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask: %v", err)
	}
	if got.Status != task.Status || got.VerificationStatus != task.VerificationStatus {
		t.Fatalf("task state changed after verification record: before=%+v after=%+v", task, got)
	}
}

func TestVerificationRunRejectsInvalidVerdict(t *testing.T) {
	ctx := context.Background()
	db, store := newVerificationTestStore(t)
	task := createVerificationTestTask(t, ctx, store)
	recorder := verification.NewRecorder(store)

	_, err := recorder.Record(ctx, verification.RunRequest{
		TaskID:           task.ID,
		CriteriaJSON:     `{"criteria":["correctness"]}`,
		EvidenceRefsJSON: `{"refs":[]}`,
		Verdict:          "maybe",
	})
	if err == nil {
		t.Fatal("expected invalid verdict error")
	}
	if !strings.Contains(err.Error(), "invalid verdict") {
		t.Fatalf("error = %q, want invalid verdict", err.Error())
	}
	if got := tableCount(t, db, "verification_runs"); got != 0 {
		t.Fatalf("verification_runs count = %d, want 0", got)
	}
}

func TestVerificationRecorder_TruncatesLongReason(t *testing.T) {
	ctx := context.Background()
	_, store := newVerificationTestStore(t)
	task := createVerificationTestTask(t, ctx, store)
	recorder := verification.NewRecorder(store)

	run, err := recorder.Record(ctx, verification.RunRequest{
		TaskID:           task.ID,
		CriteriaJSON:     `{"criteria":["correctness"]}`,
		EvidenceRefsJSON: `{"refs":[]}`,
		Verdict:          agentic.VerificationVerdictFailed,
		Reason:           strings.Repeat("x", verification.MaxReasonBytes+512),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(run.Reason) > verification.MaxReasonBytes {
		t.Fatalf("reason length = %d, want <= %d", len(run.Reason), verification.MaxReasonBytes)
	}
}

func TestVerificationRecorder_RedactsSecretReason(t *testing.T) {
	ctx := context.Background()
	_, store := newVerificationTestStore(t)
	task := createVerificationTestTask(t, ctx, store)
	recorder := verification.NewRecorder(store)

	run, err := recorder.Record(ctx, verification.RunRequest{
		TaskID:           task.ID,
		CriteriaJSON:     `{"criteria":["correctness"]}`,
		EvidenceRefsJSON: `{"refs":[]}`,
		Verdict:          agentic.VerificationVerdictFailed,
		Reason:           "verifier quoted key=AKIAIOSFODNN7EXAMPLE in output",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if strings.Contains(run.Reason, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("reason was not redacted: %q", run.Reason)
	}
	if !strings.Contains(run.Reason, "[REDACTED:aws-access-key]") {
		t.Fatalf("reason = %q, want redaction marker", run.Reason)
	}
}

func newVerificationTestStore(t *testing.T) (*sql.DB, *agentic.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := agentic.InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return db, agentic.NewStore(db)
}

func createVerificationTestTask(t *testing.T, ctx context.Context, store *agentic.Store) *agentic.AgenticTask {
	t.Helper()
	task, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "Verify changed files",
		Prompt:             "Run focused tests and report evidence.",
		Status:             agentic.TaskStatusProposed,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return task
}

func tableCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
