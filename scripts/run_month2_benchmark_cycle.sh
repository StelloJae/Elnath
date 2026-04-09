#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 ]]; then
  cat <<'EOF'
Usage:
  scripts/run_month2_benchmark_cycle.sh <current-scorecard.json|current-plan.json> <output-dir> [corpus.json] [baseline-plan.json]

Example:
  BASELINE_BIN=./scripts/my-baseline-wrapper.sh \
  scripts/run_month2_benchmark_cycle.sh \
    benchmarks/results/current-scorecard.v1.json \
    benchmarks/results/cycle-001

Notes:
  - BASELINE_BIN must be set for `elnath eval run-baseline`
  - If the first argument is a `*plan*.json` file, CURRENT_BIN must be set and the script will generate the current scorecard first
  - CURRENT_TIMEOUT and BASELINE_TIMEOUT are forwarded to the wrappers when set
  - ELNATH_BENCHMARK_KEEP_TMP=1 preserves temp benchmark repos for debugging
  - Default corpus: benchmarks/public-corpus.v1.json
  - Default baseline plan: benchmarks/baseline-plan.v1.json
EOF
  exit 1
fi

CURRENT_SCORECARD="$1"
OUTPUT_DIR="$2"
CORPUS_PATH="${3:-benchmarks/public-corpus.v1.json}"
BASELINE_PLAN="${4:-benchmarks/baseline-plan.v1.json}"

mkdir -p "$OUTPUT_DIR"

BASELINE_SCORECARD="$OUTPUT_DIR/baseline-scorecard.json"
REPORT_PATH="$OUTPUT_DIR/benchmark-report.md"
CURRENT_PLAN="$OUTPUT_DIR/current-plan.run.json"

echo "[1/6] validate corpus"
./elnath eval validate "$CORPUS_PATH"

if [[ "$CURRENT_SCORECARD" == *plan*.json ]]; then
  echo "[2/6] run current system"
  if [[ -z "${CURRENT_BIN:-}" ]]; then
    echo "CURRENT_BIN is required when the first argument is a current plan file" >&2
    exit 1
  fi
  python3 - <<PY
import json
from pathlib import Path
src = Path("$CURRENT_SCORECARD")
dst = Path("$CURRENT_PLAN")
data = json.loads(src.read_text())
data["corpus_path"] = "$CORPUS_PATH"
data["output_path"] = "$OUTPUT_DIR/current-scorecard.json"
dst.write_text(json.dumps(data, indent=2) + "\\n")
PY
  CURRENT_BIN="${CURRENT_BIN:?CURRENT_BIN must be set when running current plan}" \
  ELNATH_TIMEOUT="${CURRENT_TIMEOUT:-180}" \
  ELNATH_BENCHMARK_KEEP_TMP="${ELNATH_BENCHMARK_KEEP_TMP:-}" \
  ./elnath eval run-current "$CURRENT_PLAN"
  CURRENT_SCORECARD="$OUTPUT_DIR/current-scorecard.json"
else
  echo "[2/6] validate current scorecard"
  ./elnath eval summarize "$CURRENT_SCORECARD" >/dev/null
fi

echo "[3/6] run baseline"
TMP_PLAN="$OUTPUT_DIR/baseline-plan.run.json"
python3 - <<PY
import json
from pathlib import Path
src = Path("$BASELINE_PLAN")
dst = Path("$TMP_PLAN")
data = json.loads(src.read_text())
data["corpus_path"] = "$CORPUS_PATH"
data["output_path"] = "$BASELINE_SCORECARD"
dst.write_text(json.dumps(data, indent=2) + "\\n")
PY
BASELINE_BIN="${BASELINE_BIN:?BASELINE_BIN must be set}" \
BASELINE_TIMEOUT="${BASELINE_TIMEOUT:-180}" \
ELNATH_BENCHMARK_KEEP_TMP="${ELNATH_BENCHMARK_KEEP_TMP:-}" \
./elnath eval run-baseline "$TMP_PLAN"

echo "[4/6] write markdown report"
./elnath eval report "$CORPUS_PATH" "$CURRENT_SCORECARD" "$BASELINE_SCORECARD" "$REPORT_PATH"

echo "[5/6] run anti-vanity rules"
./elnath eval rules "$CORPUS_PATH" "$CURRENT_SCORECARD"

echo "[6/6] evaluate Month 2 gate"
./elnath eval gate-month2 "$CORPUS_PATH" "$CURRENT_SCORECARD" "$BASELINE_SCORECARD"

echo
echo "Cycle complete."
echo "Baseline scorecard: $BASELINE_SCORECARD"
echo "Benchmark report:   $REPORT_PATH"
