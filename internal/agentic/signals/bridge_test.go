package signals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/ambient"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/scheduler"

	_ "modernc.org/sqlite"
)

func TestManualSignalBridge_RecordsSignalIfApplicable(t *testing.T) {
	ctx := context.Background()
	db, store := newSignalTestStore(t)
	bridge := NewBridge(store)
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{Prompt: "manual fix request", Surface: "cli"})

	if err := bridge.RecordManualSubmitSignal(ctx, payload, 42, false); err != nil {
		t.Fatalf("RecordManualSubmitSignal: %v", err)
	}

	signal := getOnlySignal(t, ctx, db)
	if signal.Source != SourceManual || signal.Type != TypeDaemonSubmit || signal.GoalID != 0 || signal.WatcherID == 0 || signal.DedupeKey != "manual:queue:42" || signal.Fingerprint == "" {
		t.Fatalf("unexpected manual signal: %+v", signal)
	}
	assertPayloadKeysAbsent(t, signal.PayloadJSON, "prompt", "session_id", "surface")
	assertPayloadDoesNotContain(t, signal.PayloadJSON, "manual fix request")
	assertWatcherCursor(t, ctx, store, signal.WatcherID, signal.DedupeKey)
}

func TestSignalBridge_RecordsSchedulerAndAmbientSignals(t *testing.T) {
	ctx := context.Background()
	db, store := newSignalTestStore(t)
	bridge := NewBridge(store)

	if err := bridge.RecordScheduledSignal(ctx, scheduler.ScheduledTask{
		Name:      "hourly",
		Type:      "research",
		Prompt:    "inspect benchmarks",
		Interval:  time.Hour,
		SessionID: "sess-1",
		Surface:   "daemon",
	}, 7, false, nil); err != nil {
		t.Fatalf("RecordScheduledSignal: %v", err)
	}
	if err := bridge.RecordAmbientSignal(ctx, ambient.BootTask{
		Path:   "boot/startup.md",
		Title:  "Startup check",
		Prompt: "inspect boot context",
		Schedule: ambient.Schedule{
			Type: ambient.ScheduleStartup,
		},
		Silent: true,
		Tags:   []string{"boot"},
	}); err != nil {
		t.Fatalf("RecordAmbientSignal: %v", err)
	}

	if got := countRows(t, db, "goal_signals"); got != 2 {
		t.Fatalf("goal_signals rows = %d, want 2", got)
	}
	for _, signal := range allSignals(t, ctx, db) {
		if signal.WatcherID == 0 {
			t.Fatalf("signal missing watcher linkage: %+v", signal)
		}
		assertWatcherCursor(t, ctx, store, signal.WatcherID, signal.DedupeKey)
		assertPayloadKeysAbsent(t, signal.PayloadJSON, "prompt", "session_id", "surface", "path", "title")
		assertPayloadDoesNotContain(t, signal.PayloadJSON, "inspect benchmarks", "inspect boot context", "sess-1")
	}
	if got := countRows(t, db, "signal_watchers"); got != 2 {
		t.Fatalf("signal_watchers rows = %d, want 2", got)
	}
}

func TestSchedulerBridge_RecordsRepeatedOccurrencesAndCursor(t *testing.T) {
	ctx := context.Background()
	db, store := newSignalTestStore(t)
	bridge := NewBridge(store)
	task := scheduler.ScheduledTask{
		Name:   "hourly",
		Type:   "research",
		Prompt: "inspect benchmarks with secret token",
	}

	if err := bridge.RecordScheduledSignal(ctx, task, 0, false, errors.New("enqueue failed with path /private/tmp/x")); err != nil {
		t.Fatalf("RecordScheduledSignal failed occurrence: %v", err)
	}
	if err := bridge.RecordScheduledSignal(ctx, task, 8, false, nil); err != nil {
		t.Fatalf("RecordScheduledSignal recovery occurrence: %v", err)
	}

	signals := allSignals(t, ctx, db)
	if len(signals) != 2 {
		t.Fatalf("signals = %d, want 2", len(signals))
	}
	if signals[0].DedupeKey == signals[1].DedupeKey || signals[0].ID == signals[1].ID {
		t.Fatalf("repeated scheduler observations collapsed into one signal: %+v", signals)
	}
	if !strings.Contains(signals[1].DedupeKey, "queue:8") {
		t.Fatalf("recovery signal dedupe key = %q, want queue task occurrence", signals[1].DedupeKey)
	}
	assertWatcherCursor(t, ctx, store, signals[1].WatcherID, signals[1].DedupeKey)
	assertPayloadKeysAbsent(t, signals[0].PayloadJSON, "prompt", "session_id", "surface", "enqueue_error_message")
	assertPayloadDoesNotContain(t, signals[0].PayloadJSON, "inspect benchmarks", "secret token", "/private/tmp/x", "enqueue failed")
}

func TestSchedulerBridge_RecordsExistedQueueAsNewOccurrence(t *testing.T) {
	ctx := context.Background()
	db, store := newSignalTestStore(t)
	bridge := NewBridge(store)
	task := scheduler.ScheduledTask{
		Name:   "hourly",
		Type:   "research",
		Prompt: "inspect benchmarks",
	}

	if err := bridge.RecordScheduledSignal(ctx, task, 8, false, nil); err != nil {
		t.Fatalf("RecordScheduledSignal first: %v", err)
	}
	if err := bridge.RecordScheduledSignal(ctx, task, 8, true, nil); err != nil {
		t.Fatalf("RecordScheduledSignal existed: %v", err)
	}

	signals := allSignals(t, ctx, db)
	if len(signals) != 2 {
		t.Fatalf("signals = %d, want 2", len(signals))
	}
	if signals[0].DedupeKey == signals[1].DedupeKey || !strings.Contains(signals[1].DedupeKey, "observed:") {
		t.Fatalf("existed queue observation reused stale signal: %+v", signals)
	}
	assertPayloadBool(t, signals[1].PayloadJSON, "existed", true)
	assertWatcherCursor(t, ctx, store, signals[1].WatcherID, signals[1].DedupeKey)
}

func TestAmbientBridge_RecordsRepeatedOccurrencesAndCursor(t *testing.T) {
	ctx := context.Background()
	db, store := newSignalTestStore(t)
	bridge := NewBridge(store)
	task := ambient.BootTask{
		Path:   "boot/startup.md",
		Title:  "Startup check",
		Prompt: "inspect boot context",
	}

	if err := bridge.RecordAmbientSignal(ctx, task); err != nil {
		t.Fatalf("RecordAmbientSignal first: %v", err)
	}
	if err := bridge.RecordAmbientSignal(ctx, task); err != nil {
		t.Fatalf("RecordAmbientSignal second: %v", err)
	}

	signals := allSignals(t, ctx, db)
	if len(signals) != 2 {
		t.Fatalf("signals = %d, want 2", len(signals))
	}
	if signals[0].DedupeKey == signals[1].DedupeKey || signals[0].ID == signals[1].ID {
		t.Fatalf("repeated ambient observations collapsed into one signal: %+v", signals)
	}
	assertWatcherCursor(t, ctx, store, signals[1].WatcherID, signals[1].DedupeKey)
	assertPayloadKeysAbsent(t, signals[0].PayloadJSON, "prompt", "path", "title", "tags")
	assertPayloadDoesNotContain(t, signals[0].PayloadJSON, "boot/startup.md", "Startup check", "inspect boot context")
}

func TestManualSignalBridge_RecordsExistedQueueAsNewOccurrence(t *testing.T) {
	ctx := context.Background()
	db, store := newSignalTestStore(t)
	bridge := NewBridge(store)
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{Prompt: "manual fix request", Surface: "cli"})

	if err := bridge.RecordManualSubmitSignal(ctx, payload, 42, false); err != nil {
		t.Fatalf("RecordManualSubmitSignal first: %v", err)
	}
	if err := bridge.RecordManualSubmitSignal(ctx, payload, 42, true); err != nil {
		t.Fatalf("RecordManualSubmitSignal existed: %v", err)
	}

	signals := allSignals(t, ctx, db)
	if len(signals) != 2 {
		t.Fatalf("signals = %d, want 2", len(signals))
	}
	if signals[0].DedupeKey == signals[1].DedupeKey || !strings.Contains(signals[1].DedupeKey, "observed:") {
		t.Fatalf("existed manual observation reused stale signal: %+v", signals)
	}
	assertPayloadBool(t, signals[1].PayloadJSON, "existed", true)
	assertPayloadKeysAbsent(t, signals[1].PayloadJSON, "prompt", "session_id", "surface")
	assertPayloadDoesNotContain(t, signals[1].PayloadJSON, "manual fix request")
	assertWatcherCursor(t, ctx, store, signals[1].WatcherID, signals[1].DedupeKey)
}

func TestSignalBridge_DoesNotCreateAgenticTask(t *testing.T) {
	ctx := context.Background()
	db, store := newSignalTestStore(t)
	bridge := NewBridge(store)

	if err := bridge.RecordManualSubmitSignal(ctx, "manual prompt", 42, false); err != nil {
		t.Fatalf("RecordManualSubmitSignal: %v", err)
	}

	if got := countRows(t, db, "agentic_tasks"); got != 0 {
		t.Fatalf("agentic_tasks rows = %d, want 0", got)
	}
}

func TestSignalBridge_NoAutonomousSideEffects(t *testing.T) {
	ctx := context.Background()
	db, store := newSignalTestStore(t)
	bridge := NewBridge(store)

	if err := bridge.RecordManualSubmitSignal(ctx, "manual prompt", 42, false); err != nil {
		t.Fatalf("RecordManualSubmitSignal: %v", err)
	}

	for _, table := range []string{
		"policy_decisions",
		"tool_action_receipts",
		"verification_runs",
		"memory_updates",
		"followups",
	} {
		if got := countRows(t, db, table); got != 0 {
			t.Fatalf("%s rows = %d, want 0", table, got)
		}
	}
}

func newSignalTestStore(t *testing.T) (*sql.DB, *agentic.Store) {
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

func getOnlySignal(t *testing.T, ctx context.Context, db *sql.DB) agentic.GoalSignal {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM goal_signals`).Scan(&id); err != nil {
		t.Fatalf("select only signal: %v", err)
	}
	store := agentic.NewStore(db)
	signal, err := store.GetGoalSignal(ctx, id)
	if err != nil {
		t.Fatalf("GetGoalSignal: %v", err)
	}
	return *signal
}

func allSignals(t *testing.T, ctx context.Context, db *sql.DB) []agentic.GoalSignal {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT id FROM goal_signals ORDER BY id`)
	if err != nil {
		t.Fatalf("select signals: %v", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan signal id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate signals: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close signal rows: %v", err)
	}
	store := agentic.NewStore(db)
	var signals []agentic.GoalSignal
	for _, id := range ids {
		signal, err := store.GetGoalSignal(ctx, id)
		if err != nil {
			t.Fatalf("GetGoalSignal(%d): %v", id, err)
		}
		signals = append(signals, *signal)
	}
	return signals
}

func assertWatcherCursor(t *testing.T, ctx context.Context, store *agentic.Store, watcherID int64, want string) {
	t.Helper()
	watcher, err := store.GetSignalWatcher(ctx, watcherID)
	if err != nil {
		t.Fatalf("GetSignalWatcher(%d): %v", watcherID, err)
	}
	if watcher.LastCursor != want {
		t.Fatalf("watcher cursor = %q, want %q", watcher.LastCursor, want)
	}
}

func assertPayloadDoesNotContain(t *testing.T, payload string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(payload, value) {
			t.Fatalf("payload %s contains forbidden value %q", payload, value)
		}
	}
}

func assertPayloadKeysAbsent(t *testing.T, payload string, keys ...string) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	for _, key := range keys {
		if _, ok := decoded[key]; ok {
			t.Fatalf("payload %s contains forbidden key %q", payload, key)
		}
	}
}

func assertPayloadBool(t *testing.T, payload, key string, want bool) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	got, ok := decoded[key].(bool)
	if !ok || got != want {
		t.Fatalf("payload %s has %q=%v, want %v", payload, key, decoded[key], want)
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
