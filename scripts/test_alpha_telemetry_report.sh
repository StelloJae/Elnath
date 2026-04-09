#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-alpha-telemetry-test.XXXXXX")"
DB_PATH="$TMP_DIR/elnath.db"
JSON_PATH="$TMP_DIR/report.json"
LEGACY_DB_PATH="$TMP_DIR/legacy-elnath.db"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

python3 - "$DB_PATH" <<'PY'
import sqlite3
import sys
from datetime import datetime, timezone


def to_ms(value: str) -> int:
    return int(datetime.fromisoformat(value.replace(" ", "T")).replace(tzinfo=timezone.utc).timestamp() * 1000)

conn = sqlite3.connect(sys.argv[1])
cur = conn.cursor()
cur.executescript(
    """
    CREATE TABLE task_queue (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      payload TEXT NOT NULL,
      session_id TEXT NOT NULL DEFAULT '',
      status TEXT NOT NULL DEFAULT 'pending',
      progress TEXT NOT NULL DEFAULT '',
      summary TEXT NOT NULL DEFAULT '',
      result TEXT NOT NULL DEFAULT '',
      completion TEXT NOT NULL DEFAULT '',
      timeout_class TEXT NOT NULL DEFAULT '',
      idle_timeout_count INTEGER NOT NULL DEFAULT 0,
      active_timeout_count INTEGER NOT NULL DEFAULT 0,
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL DEFAULT 0,
      started_at INTEGER NOT NULL DEFAULT 0,
      completed_at INTEGER NOT NULL DEFAULT 0
    );
    CREATE TABLE conversations (
      id TEXT PRIMARY KEY,
      created_at DATETIME NOT NULL,
      updated_at DATETIME NOT NULL
    );
    CREATE TABLE conversation_messages (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      session_id TEXT NOT NULL,
      role TEXT NOT NULL,
      content TEXT NOT NULL,
      created_at DATETIME NOT NULL
    );
    CREATE TABLE approval_requests (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      tool_name TEXT NOT NULL,
      input TEXT NOT NULL DEFAULT '',
      decision TEXT NOT NULL DEFAULT 'pending',
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL
    );
    """
)
cur.executemany(
    "INSERT INTO task_queue (payload, session_id, status, completion, timeout_class, created_at, completed_at) VALUES (?, ?, ?, ?, ?, 0, ?)",
    [
        ('{"prompt":"task one","session_id":"sess-1","surface":"telegram"}', "sess-1", "done", '{"task_id":1}', "idle"),
        ('{"prompt":"task two","session_id":"sess-2","surface":"cli"}', "sess-2", "done", '{"task_id":2}', "active_but_killed"),
        ("task three", "", "failed", "", ""),
        ("task four", "sess-3", "running", "", ""),
    ],
)
cur.executemany(
    "INSERT INTO conversations (id, created_at, updated_at) VALUES (?, ?, ?)",
    [
        ("sess-1", "2026-04-07 10:00:00", "2026-04-09 12:00:00"),
        ("sess-2", "2026-04-09 09:00:00", "2026-04-09 09:30:00"),
        ("sess-3", "2026-03-01 09:00:00", "2026-03-02 09:00:00"),
    ],
)
cur.executemany(
    "INSERT INTO conversation_messages (session_id, role, content, created_at) VALUES (?, 'user', '{}', datetime('now'))",
    [("sess-1",), ("sess-1",), ("sess-2",)],
)
cur.executemany(
    "INSERT INTO approval_requests (tool_name, input, decision, created_at, updated_at) VALUES (?, ?, ?, 0, 0)",
    [
        ("bash", '{"cmd":"git status"}', "approved"),
        ("bash", '{"cmd":"npm test"}', "pending"),
        ("git", '{"subcommand":"push"}', "denied"),
    ],
)
conn.commit()
conn.close()
PY

OUTPUT="$($REPO_ROOT/scripts/alpha_telemetry_report.sh --db "$DB_PATH" --out "$JSON_PATH")"

grep -F "total: 4" <<<"$OUTPUT" >/dev/null
grep -F "done: 2" <<<"$OUTPUT" >/dev/null
grep -F "failed: 1" <<<"$OUTPUT" >/dev/null
grep -F "terminal: 3" <<<"$OUTPUT" >/dev/null
grep -F "session_binding_rate: 0.500" <<<"$OUTPUT" >/dev/null
grep -F "terminal_session_bound: 2" <<<"$OUTPUT" >/dev/null
grep -F "completion_contract_coverage: 0.667" <<<"$OUTPUT" >/dev/null
grep -F "completion_handoffs: 2" <<<"$OUTPUT" >/dev/null
grep -F "completion_handoff_rate: 1.000" <<<"$OUTPUT" >/dev/null
grep -F "completion_rate: 0.667" <<<"$OUTPUT" >/dev/null
grep -F "false_timeout_rate: 0.500" <<<"$OUTPUT" >/dev/null
grep -F "continuation_requests: 2" <<<"$OUTPUT" >/dev/null
grep -F "telegram_followups: 1" <<<"$OUTPUT" >/dev/null
grep -F "pending: 1" <<<"$OUTPUT" >/dev/null
grep -F "approved: 1" <<<"$OUTPUT" >/dev/null
grep -F "denied: 1" <<<"$OUTPUT" >/dev/null
grep -F "with_messages: 2" <<<"$OUTPUT" >/dev/null
grep -F "task_linked: 2" <<<"$OUTPUT" >/dev/null
grep -F "resume_followup_sessions: 1" <<<"$OUTPUT" >/dev/null
grep -F "resume_followup_rate: 0.500" <<<"$OUTPUT" >/dev/null
grep -F "repeat_use_sessions: 2" <<<"$OUTPUT" >/dev/null
grep -F "repeat_use_rate: 0.667" <<<"$OUTPUT" >/dev/null

JSON_OUTPUT="$($REPO_ROOT/scripts/alpha_telemetry_report.sh --db "$DB_PATH" --json)"
python3 - "$JSON_OUTPUT" "$JSON_PATH" <<'PY'
import json
import sys
from pathlib import Path
stdout_payload = json.loads(sys.argv[1])
file_payload = json.loads(Path(sys.argv[2]).read_text())
assert stdout_payload == file_payload
assert stdout_payload["tasks"]["session_binding_rate"] == 0.5
assert stdout_payload["tasks"]["completion_contract_coverage"] == 0.667
assert stdout_payload["tasks"]["completion_handoff_rate"] == 1.0
assert stdout_payload["tasks"]["completion_rate"] == 0.667
assert stdout_payload["tasks"]["continuation_requests"] == 2
assert stdout_payload["tasks"]["telegram_followups"] == 1
assert stdout_payload["approvals"]["pending"] == 1
assert stdout_payload["approvals"]["approved"] == 1
assert stdout_payload["approvals"]["denied"] == 1
assert stdout_payload["sessions"]["recent_activity_rate"] == 0.667
assert stdout_payload["sessions"]["resume_followup_rate"] == 0.5
assert stdout_payload["sessions"]["repeat_use_rate"] == 0.667
PY

echo "PASS: alpha telemetry report summarizes and archives task/session signals"

python3 - "$LEGACY_DB_PATH" <<'PY'
import sqlite3
import sys

conn = sqlite3.connect(sys.argv[1])
cur = conn.cursor()
cur.executescript(
    """
    CREATE TABLE task_queue (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      payload TEXT NOT NULL,
      status TEXT NOT NULL DEFAULT 'pending',
      result TEXT NOT NULL DEFAULT '',
      created_at INTEGER NOT NULL,
      started_at INTEGER NOT NULL DEFAULT 0,
      completed_at INTEGER NOT NULL DEFAULT 0
    );
    CREATE TABLE conversations (
      id TEXT PRIMARY KEY,
      created_at DATETIME NOT NULL,
      updated_at DATETIME NOT NULL
    );
    CREATE TABLE conversation_messages (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      session_id TEXT NOT NULL,
      role TEXT NOT NULL,
      content TEXT NOT NULL,
      created_at DATETIME NOT NULL
    );
    """
)
cur.execute(
    "INSERT INTO task_queue (payload, status, result, created_at, started_at, completed_at) VALUES ('legacy task', 'done', 'ok', 0, 0, 0)"
)
cur.execute(
    "INSERT INTO conversations (id, created_at, updated_at) VALUES ('legacy-sess', '2026-04-08 09:00:00', '2026-04-09 09:00:00')"
)
cur.execute(
    "INSERT INTO conversation_messages (session_id, role, content, created_at) VALUES ('legacy-sess', 'user', '{}', '2026-04-09 09:00:00')"
)
conn.commit()
conn.close()
PY

LEGACY_OUTPUT="$($REPO_ROOT/scripts/alpha_telemetry_report.sh --db "$LEGACY_DB_PATH")"
grep -F "schema_warnings:" <<<"$LEGACY_OUTPUT" >/dev/null
grep -F "task_queue missing columns required for some alpha telemetry signals" <<<"$LEGACY_OUTPUT" >/dev/null
grep -F "session_bound: 0" <<<"$LEGACY_OUTPUT" >/dev/null
grep -F "completion_handoffs: 0" <<<"$LEGACY_OUTPUT" >/dev/null
grep -F "resume_followup_sessions: 0" <<<"$LEGACY_OUTPUT" >/dev/null

echo "PASS: alpha telemetry report tolerates legacy task_queue schemas"
