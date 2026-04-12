#!/usr/bin/env bash
set -euo pipefail

cleanup_children() {
  pkill -P $$ 2>/dev/null || true
}
trap cleanup_children EXIT INT TERM

if [[ $# -lt 1 ]]; then
  cat <<'USAGE'
Usage:
  scripts/run_month3_gate_cycle.sh <output-dir> [corpus.json] [num-runs]

Runs the Month 3 Gate cycle:
  1. Validate corpus
  2. Run Elnath current N times (default 3)
  3. Run Claude Code baseline once
  4. Evaluate Month 3 Gate (MC4)

Defaults:
  corpus         benchmarks/public-corpus.v1.json
  num-runs       3

Required env:
  CURRENT_BIN    Path to current wrapper (scripts/run_current_benchmark_wrapper.sh)
  BASELINE_BIN   Path to baseline wrapper (scripts/run_baseline_benchmark_wrapper.sh)

Optional env:
  CURRENT_TIMEOUT / BASELINE_TIMEOUT   Per-task timeout seconds (default 180)
  BASELINE_TASK_CMD_TEMPLATE           Baseline system command template
  ELNATH_BENCHMARK_KEEP_TMP            Set to "1" to preserve temp repos
USAGE
  exit 1
fi

OUTPUT_DIR="$1"
CORPUS="${2:-benchmarks/public-corpus.v1.json}"
NUM_RUNS="${3:-3}"

if [[ "$NUM_RUNS" -lt 3 ]]; then
  echo "ERROR: Month 3 Gate requires at least 3 runs (got $NUM_RUNS)"
  exit 1
fi

mkdir -p "$OUTPUT_DIR"

BASELINE_SCORECARD="$OUTPUT_DIR/baseline-scorecard.json"

echo "=== Month 3 Gate Cycle ==="
echo "Corpus:   $CORPUS"
echo "Runs:     $NUM_RUNS"
echo "Output:   $OUTPUT_DIR"
echo

TOTAL_STEPS=$((NUM_RUNS + 3))

echo "[1/$TOTAL_STEPS] validate corpus"
./elnath eval validate "$CORPUS"

echo "[2/$TOTAL_STEPS] run baseline"
if [[ -f "$BASELINE_SCORECARD" ]] && ./elnath eval summarize "$BASELINE_SCORECARD" >/dev/null 2>&1; then
  echo "  reusing existing baseline scorecard"
else
  PLAN_RUN="$OUTPUT_DIR/baseline-plan.run.json"
  cat > "$PLAN_RUN" <<PLAN_EOF
{
  "version": "v1",
  "baseline": "claude-code",
  "corpus_path": "$CORPUS",
  "command_template": "\"\$BASELINE_BIN\" {{task_output}} {{task_id}} {{task_track}} {{task_language}} {{task_prompt}} {{task_repo}} {{task_repo_ref}} {{task_repo_class}} {{task_benchmark_family}}",
  "output_path": "$BASELINE_SCORECARD",
  "context": "month3-gate-baseline",
  "runtime_policy": "bypass",
  "repeated_runs": 1,
  "intervention_notes": false,
  "required_env": ["BASELINE_BIN"]
}
PLAN_EOF
  BASELINE_BIN="${BASELINE_BIN:?BASELINE_BIN must be set}" \
  BASELINE_TIMEOUT="${BASELINE_TIMEOUT:-180}" \
  ELNATH_BENCHMARK_KEEP_TMP="${ELNATH_BENCHMARK_KEEP_TMP:-}" \
  ./elnath eval run-baseline "$PLAN_RUN"
fi

CURRENT_SCORECARDS=()
for i in $(seq 1 "$NUM_RUNS"); do
  STEP=$((i + 2))
  SCORECARD="$OUTPUT_DIR/current-run-$i.json"
  CURRENT_SCORECARDS+=("$SCORECARD")

  echo "[$STEP/$TOTAL_STEPS] run current (run $i/$NUM_RUNS)"
  if [[ -f "$SCORECARD" ]] && ./elnath eval summarize "$SCORECARD" >/dev/null 2>&1; then
    echo "  reusing existing run $i scorecard"
    continue
  fi

  PLAN_RUN="$OUTPUT_DIR/current-run-$i-plan.json"
  cat > "$PLAN_RUN" <<PLAN_EOF
{
  "version": "v1",
  "system": "elnath-current",
  "baseline": "self",
  "corpus_path": "$CORPUS",
  "command_template": "\"\$CURRENT_BIN\" {{task_output}} {{task_id}} {{task_track}} {{task_language}} {{task_prompt}} {{task_repo}} {{task_repo_ref}} {{task_repo_class}} {{task_benchmark_family}}",
  "output_path": "$SCORECARD",
  "context": "month3-gate-run-$i",
  "runtime_policy": "bypass",
  "repeated_runs": 1,
  "intervention_notes": true,
  "required_env": ["CURRENT_BIN"]
}
PLAN_EOF
  CURRENT_BIN="${CURRENT_BIN:?CURRENT_BIN must be set}" \
  ELNATH_TIMEOUT="${CURRENT_TIMEOUT:-180}" \
  ELNATH_BENCHMARK_KEEP_TMP="${ELNATH_BENCHMARK_KEEP_TMP:-}" \
  ./elnath eval run-current "$PLAN_RUN"
done

FINAL_STEP=$TOTAL_STEPS
echo "[$FINAL_STEP/$TOTAL_STEPS] evaluate Month 3 Gate"
./elnath eval month3-gate "${CURRENT_SCORECARDS[@]}" "$BASELINE_SCORECARD"
GATE_EXIT=$?

echo
echo "=== Results ==="
echo "Baseline: $BASELINE_SCORECARD"
for i in $(seq 1 "$NUM_RUNS"); do
  echo "Run $i:    $OUTPUT_DIR/current-run-$i.json"
done

for i in $(seq 1 "$NUM_RUNS"); do
  echo
  echo "--- Run $i Summary ---"
  ./elnath eval summarize "$OUTPUT_DIR/current-run-$i.json" 2>/dev/null || true
done

echo
echo "--- Baseline Summary ---"
./elnath eval summarize "$BASELINE_SCORECARD" 2>/dev/null || true

if [[ "$GATE_EXIT" -eq 0 ]]; then
  echo
  echo "=== MONTH 3 GATE: PASS ==="
else
  echo
  echo "=== MONTH 3 GATE: FAIL ==="
fi

exit $GATE_EXIT
