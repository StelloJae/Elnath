#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  scripts/alpha_telemetry_report.sh [--data-dir <dir> | --db <path>]

Defaults:
  --data-dir ${ELNATH_DATA_DIR:-$HOME/.elnath/data}
  --db       <data-dir>/elnath.db

Summarizes Month 4 closed-alpha telemetry signals from the local Elnath SQLite state:
- task completion counts
- session-bound task counts
- completion-contract coverage
- timeout recovery / false-timeout metrics
- repeat-use session summary from conversation history
USAGE
}

DATA_DIR="${ELNATH_DATA_DIR:-$HOME/.elnath/data}"
DB_PATH=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --data-dir)
      [[ $# -ge 2 ]] || { echo "error: --data-dir requires a value" >&2; exit 1; }
      DATA_DIR="$2"
      shift 2
      ;;
    --db)
      [[ $# -ge 2 ]] || { echo "error: --db requires a value" >&2; exit 1; }
      DB_PATH="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "$DB_PATH" ]]; then
  DB_PATH="$DATA_DIR/elnath.db"
fi

if [[ ! -f "$DB_PATH" ]]; then
  echo "error: database not found: $DB_PATH" >&2
  exit 1
fi

python3 - "$DB_PATH" <<'PY'
import json
import sqlite3
import sys
from pathlib import Path


def table_exists(cur, name: str) -> bool:
    row = cur.execute(
        "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?",
        (name,),
    ).fetchone()
    return row is not None


db_path = Path(sys.argv[1])
conn = sqlite3.connect(str(db_path))
conn.row_factory = sqlite3.Row
cur = conn.cursor()

report = {
    "database": str(db_path),
    "tasks": {
        "total": 0,
        "pending": 0,
        "running": 0,
        "done": 0,
        "failed": 0,
        "session_bound": 0,
        "completion_contracts": 0,
        "idle_timeout_recoveries": 0,
        "active_but_killed_recoveries": 0,
        "false_timeout_rate": 0.0,
    },
    "sessions": {
        "total": 0,
        "with_messages": 0,
        "updated_last_7d": 0,
        "distinct_active_days_last_7d": 0,
    },
}

if table_exists(cur, "task_queue"):
    row = cur.execute(
        """
        SELECT
          COUNT(*) AS total,
          SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END) AS pending,
          SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END) AS running,
          SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END) AS done,
          SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed,
          SUM(CASE WHEN COALESCE(session_id, '') != '' THEN 1 ELSE 0 END) AS session_bound,
          SUM(CASE WHEN COALESCE(completion, '') != '' THEN 1 ELSE 0 END) AS completion_contracts,
          SUM(CASE WHEN timeout_class = 'idle' THEN 1 ELSE 0 END) AS idle_timeout_recoveries,
          SUM(CASE WHEN timeout_class = 'active_but_killed' THEN 1 ELSE 0 END) AS active_but_killed_recoveries
        FROM task_queue
        """
    ).fetchone()
    report["tasks"].update({
        "total": row["total"] or 0,
        "pending": row["pending"] or 0,
        "running": row["running"] or 0,
        "done": row["done"] or 0,
        "failed": row["failed"] or 0,
        "session_bound": row["session_bound"] or 0,
        "completion_contracts": row["completion_contracts"] or 0,
        "idle_timeout_recoveries": row["idle_timeout_recoveries"] or 0,
        "active_but_killed_recoveries": row["active_but_killed_recoveries"] or 0,
    })
    denom = report["tasks"]["idle_timeout_recoveries"] + report["tasks"]["active_but_killed_recoveries"]
    if denom:
        report["tasks"]["false_timeout_rate"] = round(
            report["tasks"]["active_but_killed_recoveries"] / denom,
            3,
        )

if table_exists(cur, "conversations"):
    row = cur.execute(
        """
        SELECT
          COUNT(*) AS total,
          SUM(CASE WHEN updated_at >= datetime('now', '-7 days') THEN 1 ELSE 0 END) AS updated_last_7d,
          COUNT(DISTINCT CASE WHEN updated_at >= datetime('now', '-7 days') THEN date(updated_at) END) AS distinct_active_days_last_7d
        FROM conversations
        """
    ).fetchone()
    report["sessions"].update({
        "total": row["total"] or 0,
        "updated_last_7d": row["updated_last_7d"] or 0,
        "distinct_active_days_last_7d": row["distinct_active_days_last_7d"] or 0,
    })

if table_exists(cur, "conversation_messages"):
    row = cur.execute(
        """
        SELECT COUNT(*)
        FROM (
          SELECT session_id
          FROM conversation_messages
          GROUP BY session_id
          HAVING COUNT(*) > 0
        )
        """
    ).fetchone()
    report["sessions"]["with_messages"] = row[0] or 0

print("Month 4 closed-alpha telemetry summary")
print(f"database: {report['database']}")
print()
print("Tasks")
print(f"  total: {report['tasks']['total']}")
print(f"  pending: {report['tasks']['pending']}")
print(f"  running: {report['tasks']['running']}")
print(f"  done: {report['tasks']['done']}")
print(f"  failed: {report['tasks']['failed']}")
print(f"  session_bound: {report['tasks']['session_bound']}")
print(f"  completion_contracts: {report['tasks']['completion_contracts']}")
print(f"  idle_timeout_recoveries: {report['tasks']['idle_timeout_recoveries']}")
print(f"  active_but_killed_recoveries: {report['tasks']['active_but_killed_recoveries']}")
print(f"  false_timeout_rate: {report['tasks']['false_timeout_rate']:.3f}")
print()
print("Sessions")
print(f"  total: {report['sessions']['total']}")
print(f"  with_messages: {report['sessions']['with_messages']}")
print(f"  updated_last_7d: {report['sessions']['updated_last_7d']}")
print(f"  distinct_active_days_last_7d: {report['sessions']['distinct_active_days_last_7d']}")
print()
print("json:")
print(json.dumps(report, indent=2, sort_keys=True))
PY
