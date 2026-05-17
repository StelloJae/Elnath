package agentic

import (
	"database/sql"
	"fmt"
	"strconv"
)

func InitSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS standing_goals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			autonomy_level TEXT NOT NULL,
			risk_budget TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS signal_watchers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			source TEXT NOT NULL,
			config_json TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			interval_s INTEGER NOT NULL DEFAULT 0,
			last_cursor TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS goal_signals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			watcher_id INTEGER REFERENCES signal_watchers(id) ON DELETE SET NULL,
			source TEXT NOT NULL,
			type TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			severity INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			dedupe_key TEXT NOT NULL DEFAULT '',
			observed_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agentic_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			signal_id INTEGER REFERENCES goal_signals(id) ON DELETE SET NULL,
			parent_id INTEGER REFERENCES agentic_tasks(id) ON DELETE SET NULL,
			queue_task_id INTEGER,
			title TEXT NOT NULL,
			prompt TEXT NOT NULL,
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			risk_level TEXT NOT NULL,
			autonomy_decision TEXT NOT NULL,
			approval_request_id TEXT NOT NULL DEFAULT '',
			verification_status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			due_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS task_edges (
			parent_id INTEGER NOT NULL REFERENCES agentic_tasks(id) ON DELETE CASCADE,
			child_id INTEGER NOT NULL REFERENCES agentic_tasks(id) ON DELETE CASCADE,
			edge_type TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			UNIQUE(parent_id, child_id, edge_type)
		)`,
		`CREATE TABLE IF NOT EXISTS agent_actors (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES agentic_tasks(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			state_json TEXT NOT NULL DEFAULT '{}',
			inbox_json TEXT NOT NULL DEFAULT '[]',
			outbox_json TEXT NOT NULL DEFAULT '[]',
			tool_allowlist_json TEXT NOT NULL DEFAULT '[]',
			budget_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS actor_handoffs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES agentic_tasks(id) ON DELETE CASCADE,
			from_actor_id INTEGER NOT NULL REFERENCES agent_actors(id) ON DELETE CASCADE,
			to_actor_id INTEGER NOT NULL REFERENCES agent_actors(id) ON DELETE CASCADE,
			handoff_type TEXT NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS policy_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES agentic_tasks(id) ON DELETE CASCADE,
			actor_id INTEGER REFERENCES agent_actors(id) ON DELETE SET NULL,
			action_kind TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			risk_level TEXT NOT NULL,
			decision TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			policy_version TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS tool_action_receipts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES agentic_tasks(id) ON DELETE CASCADE,
			actor_id INTEGER REFERENCES agent_actors(id) ON DELETE SET NULL,
			policy_decision_id INTEGER REFERENCES policy_decisions(id) ON DELETE SET NULL,
			approval_request_id TEXT NOT NULL DEFAULT '',
			tool_name TEXT NOT NULL,
			tool_call_id TEXT NOT NULL DEFAULT '',
			input_hash TEXT NOT NULL,
			output_hash TEXT NOT NULL DEFAULT '',
			raw_output_hash TEXT NOT NULL DEFAULT '',
			visible_output_hash TEXT NOT NULL DEFAULT '',
			output_summary TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			failure_reason TEXT NOT NULL DEFAULT '',
			hook_provenance_json TEXT NOT NULL DEFAULT '',
			reversible INTEGER NOT NULL DEFAULT 0,
			started_at INTEGER NOT NULL,
			completed_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS verification_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES agentic_tasks(id) ON DELETE CASCADE,
			verifier_actor_id INTEGER REFERENCES agent_actors(id) ON DELETE SET NULL,
			criteria_json TEXT NOT NULL,
			evidence_refs_json TEXT NOT NULL,
			verdict TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS completion_gates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES agentic_tasks(id) ON DELETE CASCADE,
			queue_task_id INTEGER,
			verification_run_id INTEGER REFERENCES verification_runs(id) ON DELETE SET NULL,
			status TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			receipt_summary_json TEXT NOT NULL DEFAULT '{}',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS task_enqueue_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES agentic_tasks(id) ON DELETE CASCADE,
			queue_task_id INTEGER,
			operator_id TEXT NOT NULL DEFAULT '',
			decision TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			requested_enforcement TEXT NOT NULL DEFAULT '',
			requested_completion_gate TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			failure_reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS memory_updates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES agentic_tasks(id) ON DELETE CASCADE,
			receipt_id INTEGER REFERENCES tool_action_receipts(id) ON DELETE SET NULL,
			verification_run_id INTEGER REFERENCES verification_runs(id) ON DELETE SET NULL,
			target TEXT NOT NULL,
			operation TEXT NOT NULL,
			payload_hash TEXT NOT NULL,
			status TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			applied_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS followups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER REFERENCES agentic_tasks(id) ON DELETE SET NULL,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			reason TEXT NOT NULL,
			status TEXT NOT NULL,
			trigger_at INTEGER NOT NULL,
			created_task_id INTEGER REFERENCES agentic_tasks(id) ON DELETE SET NULL,
			dedupe_key TEXT NOT NULL DEFAULT '',
			failure_reason TEXT NOT NULL DEFAULT '',
			processed_at INTEGER,
			wake_agent INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS activation_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			execution_policy TEXT NOT NULL,
			limit_n INTEGER NOT NULL,
			followup_processed INTEGER NOT NULL DEFAULT 0,
			followup_created INTEGER NOT NULL DEFAULT 0,
			followup_skipped INTEGER NOT NULL DEFAULT 0,
			followup_failed INTEGER NOT NULL DEFAULT 0,
			signal_processed INTEGER NOT NULL DEFAULT 0,
			signal_created INTEGER NOT NULL DEFAULT 0,
			signal_linked INTEGER NOT NULL DEFAULT 0,
			signal_failed INTEGER NOT NULL DEFAULT 0,
			enqueue_performed INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_goal_signals_goal ON goal_signals(goal_id)`,
		`CREATE INDEX IF NOT EXISTS idx_agentic_tasks_goal ON agentic_tasks(goal_id)`,
		`CREATE INDEX IF NOT EXISTS idx_agentic_tasks_signal ON agentic_tasks(signal_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_agentic_tasks_queue_task_id ON agentic_tasks(queue_task_id) WHERE queue_task_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_actor_handoffs_task ON actor_handoffs(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_actor_handoffs_from ON actor_handoffs(from_actor_id)`,
		`CREATE INDEX IF NOT EXISTS idx_actor_handoffs_to ON actor_handoffs(to_actor_id)`,
		`CREATE INDEX IF NOT EXISTS idx_policy_decisions_task ON policy_decisions(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_action_receipts_task ON tool_action_receipts(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_verification_runs_task ON verification_runs(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_completion_gates_task ON completion_gates(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_completion_gates_queue_task ON completion_gates(queue_task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_task_enqueue_decisions_task ON task_enqueue_decisions(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_task_enqueue_decisions_queue_task ON task_enqueue_decisions(queue_task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_updates_task ON memory_updates(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_followups_goal ON followups(goal_id)`,
		`CREATE INDEX IF NOT EXISTS idx_followups_due ON followups(status, trigger_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_followups_dedupe_key ON followups(dedupe_key) WHERE dedupe_key != ''`,
		`CREATE INDEX IF NOT EXISTS idx_activation_runs_status ON activation_runs(status)`,
		`CREATE INDEX IF NOT EXISTS idx_activation_runs_created ON activation_runs(created_at)`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("agentic: init schema: %w", err)
		}
	}
	if err := ensureAgenticTaskGoalNullable(db); err != nil {
		return err
	}
	if err := ensureAgenticTaskIndexes(db); err != nil {
		return err
	}
	if err := ensureSignalWatcherGoalNullable(db); err != nil {
		return err
	}
	if err := ensureSignalWatcherIndexes(db); err != nil {
		return err
	}
	if err := ensureGoalSignalGoalNullable(db); err != nil {
		return err
	}
	if err := ensureGoalSignalIndexes(db); err != nil {
		return err
	}
	if err := ensureToolActionReceiptColumns(db); err != nil {
		return err
	}
	if err := ensureMemoryUpdateColumns(db); err != nil {
		return err
	}
	if err := ensureMemoryUpdateIndexes(db); err != nil {
		return err
	}
	if err := ensureFollowupColumns(db); err != nil {
		return err
	}
	if err := ensureFollowupIndexes(db); err != nil {
		return err
	}
	if err := ensureTaskEnqueueDecisionIndexes(db); err != nil {
		return err
	}
	return nil
}

func ensureTaskEnqueueDecisionIndexes(db *sql.DB) error {
	if _, err := db.Exec(`
		WITH ranked AS (
			SELECT id,
			       ROW_NUMBER() OVER (
			           PARTITION BY task_id
			           ORDER BY CASE WHEN status = 'enqueued' THEN 0 ELSE 1 END, id DESC
			       ) AS rn
			FROM task_enqueue_decisions
			WHERE status IN ('pending','enqueued')
		)
		UPDATE task_enqueue_decisions
		SET status = 'failed',
		    failure_reason = CASE
		        WHEN failure_reason = '' THEN 'superseded duplicate active enqueue decision'
		        ELSE failure_reason
		    END
		WHERE id IN (SELECT id FROM ranked WHERE rn > 1)
	`); err != nil {
		return fmt.Errorf("agentic: reconcile task enqueue decisions: %w", err)
	}
	stmts := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_task_enqueue_decisions_task_enqueued ON task_enqueue_decisions(task_id) WHERE status = 'enqueued'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_task_enqueue_decisions_task_active ON task_enqueue_decisions(task_id) WHERE status IN ('pending','enqueued')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("agentic: ensure task enqueue indexes: %w", err)
		}
	}
	return nil
}

func ensureFollowupColumns(db *sql.DB) error {
	columns := []struct {
		name string
		def  string
	}{
		{"dedupe_key", "TEXT NOT NULL DEFAULT ''"},
		{"failure_reason", "TEXT NOT NULL DEFAULT ''"},
		{"processed_at", "INTEGER"},
		{"wake_agent", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, column := range columns {
		exists, err := columnExists(db, "followups", column.name)
		if err != nil {
			return fmt.Errorf("agentic: inspect followups.%s: %w", column.name, err)
		}
		if exists {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE followups ADD COLUMN %s %s", column.name, column.def)); err != nil {
			return fmt.Errorf("agentic: add followups.%s: %w", column.name, err)
		}
	}
	return nil
}

func ensureFollowupIndexes(db *sql.DB) error {
	stmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_followups_goal ON followups(goal_id)`,
		`CREATE INDEX IF NOT EXISTS idx_followups_due ON followups(status, trigger_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_followups_dedupe_key ON followups(dedupe_key) WHERE dedupe_key != ''`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("agentic: ensure followup indexes: %w", err)
		}
	}
	return nil
}

func ensureMemoryUpdateColumns(db *sql.DB) error {
	columns := []struct {
		name string
		def  string
	}{
		{"source", "TEXT NOT NULL DEFAULT ''"},
		{"reason", "TEXT NOT NULL DEFAULT ''"},
		{"applied_at", "INTEGER"},
	}
	for _, column := range columns {
		exists, err := columnExists(db, "memory_updates", column.name)
		if err != nil {
			return fmt.Errorf("agentic: inspect memory_updates.%s: %w", column.name, err)
		}
		if exists {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE memory_updates ADD COLUMN %s %s", column.name, column.def)); err != nil {
			return fmt.Errorf("agentic: add memory_updates.%s: %w", column.name, err)
		}
	}
	return nil
}

func ensureMemoryUpdateIndexes(db *sql.DB) error {
	stmts := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_updates_dedupe_null ON memory_updates(task_id, target, operation, payload_hash, source) WHERE verification_run_id IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_updates_dedupe_run ON memory_updates(task_id, verification_run_id, target, operation, payload_hash, source) WHERE verification_run_id IS NOT NULL`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("agentic: ensure memory update indexes: %w", err)
		}
	}
	return nil
}

func ensureToolActionReceiptColumns(db *sql.DB) error {
	columns := []struct {
		name string
		def  string
	}{
		{"tool_call_id", "TEXT NOT NULL DEFAULT ''"},
		{"raw_output_hash", "TEXT NOT NULL DEFAULT ''"},
		{"visible_output_hash", "TEXT NOT NULL DEFAULT ''"},
		{"failure_reason", "TEXT NOT NULL DEFAULT ''"},
		{"hook_provenance_json", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		exists, err := columnExists(db, "tool_action_receipts", column.name)
		if err != nil {
			return fmt.Errorf("agentic: inspect tool_action_receipts.%s: %w", column.name, err)
		}
		if exists {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE tool_action_receipts ADD COLUMN %s %s", column.name, column.def)); err != nil {
			return fmt.Errorf("agentic: add tool_action_receipts.%s: %w", column.name, err)
		}
	}
	return nil
}

func ensureSignalWatcherGoalNullable(db *sql.DB) error {
	notNull, err := columnNotNull(db, "signal_watchers", "goal_id")
	if err != nil {
		return fmt.Errorf("agentic: inspect signal_watchers.goal_id: %w", err)
	}
	if !notNull {
		return nil
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("agentic: disable foreign keys for signal_watchers migration: %w", err)
	}
	defer db.Exec(`PRAGMA foreign_keys=ON`) //nolint:errcheck

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("agentic: migrate signal_watchers: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmts := []string{
		`CREATE TABLE signal_watchers_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			source TEXT NOT NULL,
			config_json TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			interval_s INTEGER NOT NULL DEFAULT 0,
			last_cursor TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO signal_watchers_new(id, goal_id, source, config_json, enabled, interval_s, last_cursor, created_at, updated_at)
		 SELECT id, goal_id, source, config_json, enabled, interval_s, last_cursor, created_at, updated_at
		 FROM signal_watchers`,
		`DROP TABLE signal_watchers`,
		`ALTER TABLE signal_watchers_new RENAME TO signal_watchers`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("agentic: migrate signal_watchers: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("agentic: migrate signal_watchers: commit: %w", err)
	}
	return nil
}

func ensureSignalWatcherIndexes(db *sql.DB) error {
	if err := uniquifySignalWatcherKeys(db); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_signal_watchers_source_config ON signal_watchers(COALESCE(goal_id, 0), source, config_json)`); err != nil {
		return fmt.Errorf("agentic: ensure signal_watchers index: %w", err)
	}
	return nil
}

func uniquifySignalWatcherKeys(db *sql.DB) error {
	rows, err := db.Query(`
		SELECT id, config_json FROM signal_watchers
		WHERE id NOT IN (
			SELECT MIN(id) FROM signal_watchers
			GROUP BY COALESCE(goal_id, 0), source, config_json
		)
	`)
	if err != nil {
		return fmt.Errorf("agentic: inspect duplicate signal_watchers keys: %w", err)
	}
	defer rows.Close()

	type duplicate struct {
		id         int64
		configJSON string
	}
	var duplicates []duplicate
	for rows.Next() {
		var dup duplicate
		if err := rows.Scan(&dup.id, &dup.configJSON); err != nil {
			return fmt.Errorf("agentic: scan duplicate signal_watchers key: %w", err)
		}
		duplicates = append(duplicates, dup)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("agentic: scan duplicate signal_watchers keys: %w", err)
	}
	for _, dup := range duplicates {
		replacement := fmt.Sprintf(`{"legacy_duplicate_id":%d,"legacy_config_json":%s}`, dup.id, strconv.Quote(dup.configJSON))
		if _, err := db.Exec(`UPDATE signal_watchers SET config_json = ? WHERE id = ?`, replacement, dup.id); err != nil {
			return fmt.Errorf("agentic: rewrite duplicate signal_watchers key: %w", err)
		}
	}
	return nil
}

func ensureGoalSignalGoalNullable(db *sql.DB) error {
	notNull, err := columnNotNull(db, "goal_signals", "goal_id")
	if err != nil {
		return fmt.Errorf("agentic: inspect goal_signals.goal_id: %w", err)
	}
	if !notNull {
		return nil
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("agentic: disable foreign keys for goal_signals migration: %w", err)
	}
	defer db.Exec(`PRAGMA foreign_keys=ON`) //nolint:errcheck

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("agentic: migrate goal_signals: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmts := []string{
		`CREATE TABLE goal_signals_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			watcher_id INTEGER REFERENCES signal_watchers(id) ON DELETE SET NULL,
			source TEXT NOT NULL,
			type TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			severity INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			dedupe_key TEXT NOT NULL DEFAULT '',
			observed_at INTEGER NOT NULL
		)`,
		`INSERT INTO goal_signals_new(id, goal_id, watcher_id, source, type, payload_json, fingerprint, severity, status, dedupe_key, observed_at)
		 SELECT id, goal_id, watcher_id, source, type, payload_json, fingerprint, severity, status, dedupe_key, observed_at
		 FROM goal_signals`,
		`DROP TABLE goal_signals`,
		`ALTER TABLE goal_signals_new RENAME TO goal_signals`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("agentic: migrate goal_signals: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("agentic: migrate goal_signals: commit: %w", err)
	}
	return nil
}

func ensureGoalSignalIndexes(db *sql.DB) error {
	if err := uniquifyGoalSignalDedupeKeys(db); err != nil {
		return err
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_goal_signals_goal ON goal_signals(goal_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_goal_signals_dedupe ON goal_signals(COALESCE(goal_id, 0), source, type, dedupe_key) WHERE dedupe_key <> ''`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("agentic: ensure goal_signals index: %w", err)
		}
	}
	return nil
}

func uniquifyGoalSignalDedupeKeys(db *sql.DB) error {
	rows, err := db.Query(`
		SELECT id, dedupe_key FROM goal_signals
		WHERE dedupe_key <> ''
			AND id NOT IN (
				SELECT MIN(id) FROM goal_signals
				WHERE dedupe_key <> ''
				GROUP BY COALESCE(goal_id, 0), source, type, dedupe_key
			)
	`)
	if err != nil {
		return fmt.Errorf("agentic: inspect duplicate goal_signals dedupe keys: %w", err)
	}
	defer rows.Close()

	type duplicate struct {
		id        int64
		dedupeKey string
	}
	var duplicates []duplicate
	for rows.Next() {
		var dup duplicate
		if err := rows.Scan(&dup.id, &dup.dedupeKey); err != nil {
			return fmt.Errorf("agentic: scan duplicate goal_signals dedupe key: %w", err)
		}
		duplicates = append(duplicates, dup)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("agentic: scan duplicate goal_signals dedupe keys: %w", err)
	}
	for _, dup := range duplicates {
		if _, err := db.Exec(`UPDATE goal_signals SET dedupe_key = ? WHERE id = ?`, fmt.Sprintf("%s:legacy:%d", dup.dedupeKey, dup.id), dup.id); err != nil {
			return fmt.Errorf("agentic: rewrite duplicate goal_signals dedupe key: %w", err)
		}
	}
	return nil
}

func ensureAgenticTaskGoalNullable(db *sql.DB) error {
	notNull, err := columnNotNull(db, "agentic_tasks", "goal_id")
	if err != nil {
		return fmt.Errorf("agentic: inspect agentic_tasks.goal_id: %w", err)
	}
	if !notNull {
		return nil
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("agentic: disable foreign keys for migration: %w", err)
	}
	defer db.Exec(`PRAGMA foreign_keys=ON`) //nolint:errcheck

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("agentic: migrate agentic_tasks: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmts := []string{
		`CREATE TABLE agentic_tasks_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			signal_id INTEGER REFERENCES goal_signals(id) ON DELETE SET NULL,
			parent_id INTEGER REFERENCES agentic_tasks(id) ON DELETE SET NULL,
			queue_task_id INTEGER,
			title TEXT NOT NULL,
			prompt TEXT NOT NULL,
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			risk_level TEXT NOT NULL,
			autonomy_decision TEXT NOT NULL,
			approval_request_id TEXT NOT NULL DEFAULT '',
			verification_status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			due_at INTEGER
		)`,
		`INSERT INTO agentic_tasks_new(id, goal_id, signal_id, parent_id, queue_task_id, title, prompt, status, priority, risk_level, autonomy_decision, approval_request_id, verification_status, created_at, updated_at, due_at)
		 SELECT id, goal_id, signal_id, parent_id, queue_task_id, title, prompt, status, priority, risk_level, autonomy_decision, approval_request_id, verification_status, created_at, updated_at, due_at
		 FROM agentic_tasks`,
		`DROP TABLE agentic_tasks`,
		`ALTER TABLE agentic_tasks_new RENAME TO agentic_tasks`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("agentic: migrate agentic_tasks: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("agentic: migrate agentic_tasks: commit: %w", err)
	}
	return nil
}

func ensureAgenticTaskIndexes(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("agentic: ensure agentic_tasks indexes: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if err := uniquifyAgenticTaskSignalIDsTx(tx); err != nil {
		return err
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_agentic_tasks_goal ON agentic_tasks(goal_id)`,
		`CREATE INDEX IF NOT EXISTS idx_agentic_tasks_signal ON agentic_tasks(signal_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_agentic_tasks_signal_unique ON agentic_tasks(signal_id) WHERE signal_id IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_agentic_tasks_queue_task_id ON agentic_tasks(queue_task_id) WHERE queue_task_id IS NOT NULL`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("agentic: ensure agentic_tasks index: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("agentic: ensure agentic_tasks indexes: commit: %w", err)
	}
	return nil
}

func uniquifyAgenticTaskSignalIDsTx(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT id FROM agentic_tasks
		WHERE signal_id IS NOT NULL
			AND id NOT IN (
				SELECT MIN(id) FROM agentic_tasks
				WHERE signal_id IS NOT NULL
				GROUP BY signal_id
			)
	`)
	if err != nil {
		return fmt.Errorf("agentic: inspect duplicate agentic_tasks signal_id keys: %w", err)
	}
	defer rows.Close()

	var duplicates []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("agentic: scan duplicate agentic_tasks signal_id key: %w", err)
		}
		duplicates = append(duplicates, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("agentic: scan duplicate agentic_tasks signal_id keys: %w", err)
	}
	for _, id := range duplicates {
		if _, err := tx.Exec(`UPDATE agentic_tasks SET signal_id = NULL WHERE id = ?`, id); err != nil {
			return fmt.Errorf("agentic: clear duplicate agentic_tasks signal_id key: %w", err)
		}
	}
	return nil
}

func columnNotNull(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return notNull != 0, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, fmt.Errorf("column %s.%s not found", table, column)
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}
