#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  scripts/alpha_telemetry_report.sh [--data-dir <dir> | --db <path>] [--json] [--out <path>]

Defaults:
  --data-dir ${ELNATH_DATA_DIR:-$HOME/.elnath/data}
  --db       <data-dir>/elnath.db

Summarizes Month 4 closed-alpha telemetry signals from the local Elnath SQLite state:
- task completion counts
- session-bound task counts
- continuation / Telegram follow-up counts from structured daemon payloads
- completion-contract coverage
- timeout recovery / false-timeout metrics
- approval decision counts
- repeat-use session summary from conversation history

Options:
  --json       Print JSON only
  --out PATH   Write the JSON payload to PATH as an archival artifact
USAGE
}

DATA_DIR="${ELNATH_DATA_DIR:-$HOME/.elnath/data}"
DB_PATH=""
OUTPUT_MODE="text"
OUT_PATH=""

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
    --json)
      OUTPUT_MODE="json"
      shift
      ;;
    --out)
      [[ $# -ge 2 ]] || { echo "error: --out requires a value" >&2; exit 1; }
      OUT_PATH="$2"
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

python3 - "$DB_PATH" "$OUTPUT_MODE" "$OUT_PATH" <<'PY'
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


def table_columns(cur, name: str) -> set[str]:
    return {row[1] for row in cur.execute(f"PRAGMA table_info({name})")}


def ratio(numerator: int, denominator: int) -> float:
    if denominator <= 0:
        return 0.0
    return round(numerator / denominator, 3)


db_path = Path(sys.argv[1])
output_mode = sys.argv[2]
out_path = Path(sys.argv[3]) if sys.argv[3] else None
conn = sqlite3.connect(str(db_path))
conn.row_factory = sqlite3.Row
cur = conn.cursor()

report = {
    "database": str(db_path),
    "schema_warnings": [],
    "tasks": {
        "total": 0,
        "pending": 0,
        "running": 0,
        "done": 0,
        "failed": 0,
        "terminal": 0,
        "session_bound": 0,
        "terminal_session_bound": 0,
        "completion_contracts": 0,
        "continuation_requests": 0,
        "telegram_followups": 0,
        "idle_timeout_recoveries": 0,
        "active_but_killed_recoveries": 0,
        "false_timeout_rate": 0.0,
        "session_binding_rate": 0.0,
        "completion_contract_coverage": 0.0,
        "completion_handoff_rate": 0.0,
        "completion_rate": 0.0,
    },
    "approvals": {
        "total": 0,
        "pending": 0,
        "approved": 0,
        "denied": 0,
        "resolved_rate": 0.0,
    },
    "sessions": {
        "total": 0,
        "with_messages": 0,
        "task_linked": 0,
        "resume_followup_sessions": 0,
        "resume_followup_rate": 0.0,
        "updated_last_7d": 0,
        "distinct_active_days_last_7d": 0,
        "repeat_use_sessions": 0,
        "repeat_use_rate": 0.0,
        "recent_activity_rate": 0.0,
    },
}

if table_exists(cur, "task_queue"):
    task_cols = table_columns(cur, "task_queue")
    required_task_cols = {"session_id", "completion", "timeout_class", "completed_at"}
    missing_task_cols = sorted(required_task_cols - task_cols)
    if missing_task_cols:
        report["schema_warnings"].append(
            "task_queue missing columns required for some alpha telemetry signals: "
            + ", ".join(missing_task_cols)
        )
    row = cur.execute(
        f"""
        SELECT
          COUNT(*) AS total,
          SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END) AS pending,
          SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END) AS running,
          SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END) AS done,
          SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed,
          SUM(CASE WHEN status IN ('done', 'failed') THEN 1 ELSE 0 END) AS terminal,
          {"SUM(CASE WHEN COALESCE(session_id, '') != '' THEN 1 ELSE 0 END)" if "session_id" in task_cols else "0"} AS session_bound,
          {"SUM(CASE WHEN status IN ('done', 'failed') AND COALESCE(session_id, '') != '' THEN 1 ELSE 0 END)" if "session_id" in task_cols else "0"} AS terminal_session_bound,
          {"SUM(CASE WHEN COALESCE(completion, '') != '' THEN 1 ELSE 0 END)" if "completion" in task_cols else "0"} AS completion_contracts,
          {"SUM(CASE WHEN status IN ('done', 'failed') AND COALESCE(session_id, '') != '' AND COALESCE(completion, '') != '' THEN 1 ELSE 0 END)" if {"session_id", "completion"}.issubset(task_cols) else "0"} AS completion_handoffs,
          {"SUM(CASE WHEN timeout_class = 'idle' THEN 1 ELSE 0 END)" if "timeout_class" in task_cols else "0"} AS idle_timeout_recoveries,
          {"SUM(CASE WHEN timeout_class = 'active_but_killed' THEN 1 ELSE 0 END)" if "timeout_class" in task_cols else "0"} AS active_but_killed_recoveries
        FROM task_queue
        """
    ).fetchone()
    report["tasks"].update({
        "total": row["total"] or 0,
        "pending": row["pending"] or 0,
        "running": row["running"] or 0,
        "done": row["done"] or 0,
        "failed": row["failed"] or 0,
        "terminal": row["terminal"] or 0,
        "session_bound": row["session_bound"] or 0,
        "terminal_session_bound": row["terminal_session_bound"] or 0,
        "completion_contracts": row["completion_contracts"] or 0,
        "completion_handoffs": row["completion_handoffs"] or 0,
        "idle_timeout_recoveries": row["idle_timeout_recoveries"] or 0,
        "active_but_killed_recoveries": row["active_but_killed_recoveries"] or 0,
    })
    denom = report["tasks"]["idle_timeout_recoveries"] + report["tasks"]["active_but_killed_recoveries"]
    if denom:
        report["tasks"]["false_timeout_rate"] = ratio(
            report["tasks"]["active_but_killed_recoveries"],
            denom,
        )
    report["tasks"]["session_binding_rate"] = ratio(report["tasks"]["session_bound"], report["tasks"]["total"])
    report["tasks"]["completion_contract_coverage"] = ratio(
        report["tasks"]["completion_contracts"],
        report["tasks"]["terminal"],
    )
    report["tasks"]["completion_handoff_rate"] = ratio(
        report["tasks"]["completion_handoffs"],
        report["tasks"]["terminal_session_bound"],
    )
    report["tasks"]["completion_rate"] = ratio(report["tasks"]["done"], report["tasks"]["terminal"])

    continuation_requests = 0
    telegram_followups = 0
    for task_row in cur.execute("SELECT payload FROM task_queue"):
        raw_payload = (task_row["payload"] or "").strip()
        if not raw_payload.startswith("{"):
            continue
        try:
            payload = json.loads(raw_payload)
        except json.JSONDecodeError:
            continue
        if not isinstance(payload, dict):
            continue
        session_id = str(payload.get("session_id", "") or "").strip()
        surface = str(payload.get("surface", "") or "").strip().lower()
        if session_id:
            continuation_requests += 1
            if surface == "telegram":
                telegram_followups += 1
    report["tasks"]["continuation_requests"] = continuation_requests
    report["tasks"]["telegram_followups"] = telegram_followups

if table_exists(cur, "approval_requests"):
    row = cur.execute(
        """
        SELECT
          COUNT(*) AS total,
          SUM(CASE WHEN decision = 'pending' THEN 1 ELSE 0 END) AS pending,
          SUM(CASE WHEN decision = 'approved' THEN 1 ELSE 0 END) AS approved,
          SUM(CASE WHEN decision = 'denied' THEN 1 ELSE 0 END) AS denied
        FROM approval_requests
        """
    ).fetchone()
    report["approvals"].update({
        "total": row["total"] or 0,
        "pending": row["pending"] or 0,
        "approved": row["approved"] or 0,
        "denied": row["denied"] or 0,
    })
    report["approvals"]["resolved_rate"] = ratio(
        report["approvals"]["approved"] + report["approvals"]["denied"],
        report["approvals"]["total"],
    )

if table_exists(cur, "conversations"):
    conversation_cols = table_columns(cur, "conversations")
    if not {"created_at", "updated_at"}.issubset(conversation_cols):
        report["schema_warnings"].append(
            "conversations missing created_at/updated_at columns required for repeat-use telemetry"
        )
    row = cur.execute(
        f"""
        SELECT
          COUNT(*) AS total,
          {"SUM(CASE WHEN date(updated_at) > date(created_at) THEN 1 ELSE 0 END)" if {"created_at", "updated_at"}.issubset(conversation_cols) else "0"} AS repeat_use_sessions,
          {"SUM(CASE WHEN updated_at >= datetime('now', '-7 days') THEN 1 ELSE 0 END)" if "updated_at" in conversation_cols else "0"} AS updated_last_7d,
          {"COUNT(DISTINCT CASE WHEN updated_at >= datetime('now', '-7 days') THEN date(updated_at) END)" if "updated_at" in conversation_cols else "0"} AS distinct_active_days_last_7d
        FROM conversations
        """
    ).fetchone()
    report["sessions"].update({
        "total": row["total"] or 0,
        "repeat_use_sessions": row["repeat_use_sessions"] or 0,
        "updated_last_7d": row["updated_last_7d"] or 0,
        "distinct_active_days_last_7d": row["distinct_active_days_last_7d"] or 0,
    })
    report["sessions"]["repeat_use_rate"] = ratio(
        report["sessions"]["repeat_use_sessions"],
        report["sessions"]["total"],
    )
    report["sessions"]["recent_activity_rate"] = ratio(
        report["sessions"]["updated_last_7d"],
        report["sessions"]["total"],
    )

if table_exists(cur, "conversation_messages"):
    message_cols = table_columns(cur, "conversation_messages")
    if "session_id" not in message_cols:
        report["schema_warnings"].append(
            "conversation_messages missing session_id column required for message coverage telemetry"
        )
    else:
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

if (
    table_exists(cur, "task_queue")
    and table_exists(cur, "conversations")
    and {"session_id", "completed_at"}.issubset(table_columns(cur, "task_queue"))
):
    row = cur.execute(
        """
        WITH terminal_task_sessions AS (
          SELECT session_id, MAX(completed_at) AS last_completed_at
          FROM task_queue
          WHERE status IN ('done', 'failed') AND COALESCE(session_id, '') != ''
          GROUP BY session_id
        )
        SELECT
          COUNT(*) AS task_linked,
          SUM(
            CASE
              WHEN julianday(c.updated_at) > julianday(datetime(terminal_task_sessions.last_completed_at / 1000.0, 'unixepoch'))
              THEN 1 ELSE 0
            END
          ) AS resume_followup_sessions
        FROM terminal_task_sessions
        JOIN conversations c ON c.id = terminal_task_sessions.session_id
        """
    ).fetchone()
    report["sessions"].update({
        "task_linked": row["task_linked"] or 0,
        "resume_followup_sessions": row["resume_followup_sessions"] or 0,
    })
    report["sessions"]["resume_followup_rate"] = ratio(
        report["sessions"]["resume_followup_sessions"],
        report["sessions"]["task_linked"],
    )
elif table_exists(cur, "task_queue") and table_exists(cur, "conversations"):
    report["schema_warnings"].append(
        "task_queue missing session_id/completed_at columns required for resume follow-up telemetry"
    )

json_payload = json.dumps(report, indent=2, sort_keys=True)
if out_path is not None:
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json_payload + "\n")

if output_mode == "json":
    print(json_payload)
    sys.exit(0)

print("Month 4 closed-alpha telemetry summary")
print(f"database: {report['database']}")
if out_path is not None:
    print(f"artifact: {out_path}")
if report["schema_warnings"]:
    print("schema_warnings:")
    for warning in report["schema_warnings"]:
        print(f"  - {warning}")
print()
print("Tasks")
print(f"  total: {report['tasks']['total']}")
print(f"  pending: {report['tasks']['pending']}")
print(f"  running: {report['tasks']['running']}")
print(f"  done: {report['tasks']['done']}")
print(f"  failed: {report['tasks']['failed']}")
print(f"  terminal: {report['tasks']['terminal']}")
print(f"  session_bound: {report['tasks']['session_bound']}")
print(f"  session_binding_rate: {report['tasks']['session_binding_rate']:.3f}")
print(f"  terminal_session_bound: {report['tasks']['terminal_session_bound']}")
print(f"  completion_contracts: {report['tasks']['completion_contracts']}")
print(f"  completion_contract_coverage: {report['tasks']['completion_contract_coverage']:.3f}")
print(f"  completion_handoffs: {report['tasks']['completion_handoffs']}")
print(f"  completion_handoff_rate: {report['tasks']['completion_handoff_rate']:.3f}")
print(f"  completion_rate: {report['tasks']['completion_rate']:.3f}")
print(f"  continuation_requests: {report['tasks']['continuation_requests']}")
print(f"  telegram_followups: {report['tasks']['telegram_followups']}")
print(f"  idle_timeout_recoveries: {report['tasks']['idle_timeout_recoveries']}")
print(f"  active_but_killed_recoveries: {report['tasks']['active_but_killed_recoveries']}")
print(f"  false_timeout_rate: {report['tasks']['false_timeout_rate']:.3f}")
print()
print("Approvals")
print(f"  total: {report['approvals']['total']}")
print(f"  pending: {report['approvals']['pending']}")
print(f"  approved: {report['approvals']['approved']}")
print(f"  denied: {report['approvals']['denied']}")
print(f"  resolved_rate: {report['approvals']['resolved_rate']:.3f}")
print()
print("Sessions")
print(f"  total: {report['sessions']['total']}")
print(f"  with_messages: {report['sessions']['with_messages']}")
print(f"  task_linked: {report['sessions']['task_linked']}")
print(f"  resume_followup_sessions: {report['sessions']['resume_followup_sessions']}")
print(f"  resume_followup_rate: {report['sessions']['resume_followup_rate']:.3f}")
print(f"  updated_last_7d: {report['sessions']['updated_last_7d']}")
print(f"  recent_activity_rate: {report['sessions']['recent_activity_rate']:.3f}")
print(f"  distinct_active_days_last_7d: {report['sessions']['distinct_active_days_last_7d']}")
print(f"  repeat_use_sessions: {report['sessions']['repeat_use_sessions']}")
print(f"  repeat_use_rate: {report['sessions']['repeat_use_rate']:.3f}")
print()
print("json:")
print(json_payload)
PY
