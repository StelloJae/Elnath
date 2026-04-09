package daemon

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openApprovalTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "elnath.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestApprovalStoreCreateListDecideAndWait(t *testing.T) {
	db := openApprovalTestDB(t)
	store, err := NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}

	req, err := store.Create(context.Background(), "bash", []byte(`{"cmd":"git status"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if req.Decision != ApprovalDecisionPending {
		t.Fatalf("Decision = %q, want pending", req.Decision)
	}

	pending, err := store.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != req.ID {
		t.Fatalf("pending = %+v, want request %d", pending, req.ID)
	}

	done := make(chan bool, 1)
	go func() {
		approved, waitErr := store.Wait(context.Background(), req.ID, 5*time.Millisecond)
		if waitErr != nil {
			t.Errorf("Wait: %v", waitErr)
			return
		}
		done <- approved
	}()

	time.Sleep(20 * time.Millisecond)
	if err := store.Decide(context.Background(), req.ID, true); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	select {
	case approved := <-done:
		if !approved {
			t.Fatal("approved = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after approval")
	}

	pending, err = store.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending after decide: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after decide = %+v, want empty", pending)
	}

	row, err := store.Get(context.Background(), req.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Decision != ApprovalDecisionApproved {
		t.Fatalf("final decision = %q, want approved", row.Decision)
	}
}

func TestApprovalStoreWaitHonorsContextCancellation(t *testing.T) {
	db := openApprovalTestDB(t)
	store, err := NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	req, err := store.Create(context.Background(), "bash", []byte(`{"cmd":"rm -rf /tmp/nope"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	approved, err := store.Wait(ctx, req.ID, 5*time.Millisecond)
	if err == nil {
		t.Fatal("Wait error = nil, want context deadline exceeded")
	}
	if approved {
		t.Fatal("approved = true, want false when context cancels")
	}
}
