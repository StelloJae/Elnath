#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 4 ]]; then
  cat <<'EOF'
Usage:
  scripts/example_baseline_benchmark_wrapper.sh <task-output.json> <task-id> <task-track> <task-language>

This is a skeleton wrapper for BASELINE_BIN.
Replace the TODO section with the logic that:
1. maps task-id to the benchmark prompt/repo checkout
2. runs Claude Code / Codex CLI under your OMX/OMC workflow
3. collects the same RunResult fields as Elnath
4. writes one RunResult JSON object to <task-output.json>
EOF
  exit 1
fi

TASK_OUTPUT="$1"
TASK_ID="$2"
TASK_TRACK="$3"
TASK_LANGUAGE="$4"

# TODO:
# - resolve task id to the real benchmark task
# - run the external baseline workflow
# - collect success / verification / intervention / recovery signals

cat > "$TASK_OUTPUT" <<EOF
{
  "task_id": "$TASK_ID",
  "track": "$TASK_TRACK",
  "language": "$TASK_LANGUAGE",
  "success": false,
  "intervention_count": 0,
  "intervention_needed": false,
  "verification_passed": false,
  "failure_family": "wrapper_not_implemented",
  "recovery_attempted": false,
  "recovery_succeeded": false,
  "duration_seconds": 0,
  "notes": "Replace scripts/example_baseline_benchmark_wrapper.sh with a real BASELINE_BIN wrapper before trusting benchmark results."
}
EOF
