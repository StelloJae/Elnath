#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 4 ]]; then
  cat <<'EOF'
Usage:
  scripts/example_current_benchmark_wrapper.sh <task-output.json> <task-id> <task-track> <task-language>

This is a skeleton wrapper for CURRENT_BIN.
Replace the TODO section with the logic that:
1. maps task-id to a real benchmark prompt/repo checkout
2. runs Elnath on that task
3. collects verification + intervention + recovery signals
4. writes one RunResult JSON object to <task-output.json>
EOF
  exit 1
fi

TASK_OUTPUT="$1"
TASK_ID="$2"
TASK_TRACK="$3"
TASK_LANGUAGE="$4"

# TODO:
# - resolve the task definition from the corpus/task id
# - run Elnath on the real task
# - determine success / verification / intervention / failure_family

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
  "notes": "Replace scripts/example_current_benchmark_wrapper.sh with a real CURRENT_BIN wrapper before trusting benchmark results."
}
EOF
