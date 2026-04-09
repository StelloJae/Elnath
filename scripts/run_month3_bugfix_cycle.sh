#!/usr/bin/env bash
set -euo pipefail

cleanup_children() {
  pkill -P $$ 2>/dev/null || true
}
trap cleanup_children EXIT INT TERM

if [[ $# -lt 2 ]]; then
  cat <<'USAGE'
Usage:
  scripts/run_month3_bugfix_cycle.sh <bugfix-current-scorecard.json|bugfix-current-plan.json> <output-dir> [bugfix-corpus.json] [bugfix-baseline-plan.json]

Defaults:
  bugfix corpus         benchmarks/bugfix-primary.v1.json
  bugfix baseline plan  benchmarks/bugfix-baseline-plan.v1.json
  canary current plan   benchmarks/month3-canary-current-plan.v1.json
  canary corpus         benchmarks/month3-canary-corpus.v1.json
  canary baseline plan  benchmarks/month3-canary-baseline-plan.v1.json

Notes:
  - CURRENT_BIN and BASELINE_BIN are required when running plan files.
  - CURRENT_TIMEOUT / BASELINE_TIMEOUT are forwarded to the wrappers.
  - ELNATH_BENCHMARK_KEEP_TMP=1 preserves temp repos for debugging.
USAGE
  exit 1
fi

BUGFIX_CURRENT_INPUT="$1"
OUTPUT_DIR="$2"
BUGFIX_CORPUS="${3:-benchmarks/bugfix-primary.v1.json}"
BUGFIX_BASELINE_PLAN="${4:-benchmarks/bugfix-baseline-plan.v1.json}"
CANARY_CURRENT_PLAN="${MONTH3_CANARY_CURRENT_PLAN:-benchmarks/month3-canary-current-plan.v1.json}"
CANARY_CORPUS="${MONTH3_CANARY_CORPUS:-benchmarks/month3-canary-corpus.v1.json}"
CANARY_BASELINE_PLAN="${MONTH3_CANARY_BASELINE_PLAN:-benchmarks/month3-canary-baseline-plan.v1.json}"

mkdir -p "$OUTPUT_DIR" "$OUTPUT_DIR/canary"

BUGFIX_CURRENT_SCORECARD="$BUGFIX_CURRENT_INPUT"
BUGFIX_BASELINE_SCORECARD="$OUTPUT_DIR/bugfix-baseline-scorecard.json"
BUGFIX_REPORT="$OUTPUT_DIR/bugfix-report.md"
BUGFIX_CURRENT_PLAN_RUN="$OUTPUT_DIR/bugfix-current-plan.run.json"
BUGFIX_BASELINE_PLAN_RUN="$OUTPUT_DIR/bugfix-baseline-plan.run.json"

CANARY_CURRENT_SCORECARD="$OUTPUT_DIR/canary/current-scorecard.json"
CANARY_BASELINE_SCORECARD="$OUTPUT_DIR/canary/baseline-scorecard.json"
CANARY_REPORT="$OUTPUT_DIR/canary/benchmark-report.md"
CANARY_CURRENT_PLAN_RUN="$OUTPUT_DIR/canary/current-plan.run.json"
CANARY_BASELINE_PLAN_RUN="$OUTPUT_DIR/canary/baseline-plan.run.json"

plan_repeats() {
  local plan_path="$1"
  python3 - <<PY
import json
from pathlib import Path
data = json.loads(Path("$plan_path").read_text())
print(max(1, int(data.get("repeated_runs") or 1)))
PY
}

scorecard_is_usable() {
  local scorecard_path="$1"
  local expected_corpus="$2"
  local expected_repeats="$3"
  if [[ ! -f "$scorecard_path" ]]; then
    return 1
  fi
  if ! ./elnath eval summarize "$scorecard_path" >/dev/null 2>&1; then
    return 1
  fi
  python3 - <<PY >/dev/null 2>&1
import json, sys
from pathlib import Path
score = json.loads(Path("$scorecard_path").read_text())
corpus = json.loads(Path("$expected_corpus").read_text())
task_ids = sorted(t["id"] for t in corpus["tasks"])
results = score.get("results", [])
if not results:
    raise SystemExit(1)
expected_runs = max(1, int("$expected_repeats"))
if max(1, int(score.get("repeated_runs") or 1)) != expected_runs:
    raise SystemExit(1)
expected = {(task_id, run) for task_id in task_ids for run in range(1, expected_runs + 1)}
observed = set()
for result in results:
    task_id = result.get("task_id")
    run = max(1, int(result.get("run") or 1))
    key = (task_id, run)
    if key in observed:
        raise SystemExit(1)
    observed.add(key)
if observed != expected:
    raise SystemExit(1)
PY
}

if [[ "$BUGFIX_CURRENT_INPUT" == *plan*.json ]]; then
  BUGFIX_CURRENT_REPEATS="$(plan_repeats "$BUGFIX_CURRENT_INPUT")"
else
  BUGFIX_CURRENT_REPEATS="1"
fi
BUGFIX_BASELINE_REPEATS="$(plan_repeats "$BUGFIX_BASELINE_PLAN")"
CANARY_CURRENT_REPEATS="$(plan_repeats "$CANARY_CURRENT_PLAN")"
CANARY_BASELINE_REPEATS="$(plan_repeats "$CANARY_BASELINE_PLAN")"

echo "[1/9] validate bugfix corpus"
./elnath eval validate "$BUGFIX_CORPUS"

if [[ "$BUGFIX_CURRENT_INPUT" == *plan*.json ]]; then
  if scorecard_is_usable "$OUTPUT_DIR/bugfix-current-scorecard.json" "$BUGFIX_CORPUS" "$BUGFIX_CURRENT_REPEATS"; then
    echo "[2/9] reuse bugfix current"
    BUGFIX_CURRENT_SCORECARD="$OUTPUT_DIR/bugfix-current-scorecard.json"
    ./elnath eval summarize "$BUGFIX_CURRENT_SCORECARD" >/dev/null
  else
    echo "[2/9] run bugfix current"
    python3 - <<PY
import json
from pathlib import Path
src = Path("$BUGFIX_CURRENT_INPUT")
dst = Path("$BUGFIX_CURRENT_PLAN_RUN")
data = json.loads(src.read_text())
data["corpus_path"] = "$BUGFIX_CORPUS"
data["output_path"] = "$OUTPUT_DIR/bugfix-current-scorecard.json"
dst.write_text(json.dumps(data, indent=2) + "\n")
PY
    CURRENT_BIN="${CURRENT_BIN:?CURRENT_BIN must be set}" \
    ELNATH_TIMEOUT="${CURRENT_TIMEOUT:-180}" \
    ELNATH_BENCHMARK_KEEP_TMP="${ELNATH_BENCHMARK_KEEP_TMP:-}" \
    ./elnath eval run-current "$BUGFIX_CURRENT_PLAN_RUN"
    BUGFIX_CURRENT_SCORECARD="$OUTPUT_DIR/bugfix-current-scorecard.json"
  fi
else
  echo "[2/9] summarize bugfix current"
  ./elnath eval summarize "$BUGFIX_CURRENT_SCORECARD" >/dev/null
fi

echo "[3/9] run bugfix baseline"
if scorecard_is_usable "$BUGFIX_BASELINE_SCORECARD" "$BUGFIX_CORPUS" "$BUGFIX_BASELINE_REPEATS"; then
  echo "[3/9] reuse bugfix baseline"
  ./elnath eval summarize "$BUGFIX_BASELINE_SCORECARD" >/dev/null
else
  python3 - <<PY
import json
from pathlib import Path
src = Path("$BUGFIX_BASELINE_PLAN")
dst = Path("$BUGFIX_BASELINE_PLAN_RUN")
data = json.loads(src.read_text())
data["corpus_path"] = "$BUGFIX_CORPUS"
data["output_path"] = "$BUGFIX_BASELINE_SCORECARD"
dst.write_text(json.dumps(data, indent=2) + "\n")
PY
  BASELINE_BIN="${BASELINE_BIN:?BASELINE_BIN must be set}" \
  BASELINE_TIMEOUT="${BASELINE_TIMEOUT:-180}" \
  ELNATH_BENCHMARK_KEEP_TMP="${ELNATH_BENCHMARK_KEEP_TMP:-}" \
  ./elnath eval run-baseline "$BUGFIX_BASELINE_PLAN_RUN"
fi

echo "[4/9] write bugfix report"
./elnath eval report "$BUGFIX_CORPUS" "$BUGFIX_CURRENT_SCORECARD" "$BUGFIX_BASELINE_SCORECARD" "$BUGFIX_REPORT"

echo "[5/9] validate canary corpus"
./elnath eval validate "$CANARY_CORPUS"

echo "[6/9] run canary current"
if scorecard_is_usable "$CANARY_CURRENT_SCORECARD" "$CANARY_CORPUS" "$CANARY_CURRENT_REPEATS"; then
  echo "[6/9] reuse canary current"
  ./elnath eval summarize "$CANARY_CURRENT_SCORECARD" >/dev/null
else
  python3 - <<PY
import json
from pathlib import Path
src = Path("$CANARY_CURRENT_PLAN")
dst = Path("$CANARY_CURRENT_PLAN_RUN")
data = json.loads(src.read_text())
data["corpus_path"] = "$CANARY_CORPUS"
data["output_path"] = "$CANARY_CURRENT_SCORECARD"
dst.write_text(json.dumps(data, indent=2) + "\n")
PY
  CURRENT_BIN="${CURRENT_BIN:?CURRENT_BIN must be set}" \
  ELNATH_TIMEOUT="${CURRENT_TIMEOUT:-180}" \
  ELNATH_BENCHMARK_KEEP_TMP="${ELNATH_BENCHMARK_KEEP_TMP:-}" \
  ./elnath eval run-current "$CANARY_CURRENT_PLAN_RUN"
fi

echo "[7/9] run canary baseline"
if scorecard_is_usable "$CANARY_BASELINE_SCORECARD" "$CANARY_CORPUS" "$CANARY_BASELINE_REPEATS"; then
  echo "[7/9] reuse canary baseline"
  ./elnath eval summarize "$CANARY_BASELINE_SCORECARD" >/dev/null
else
  python3 - <<PY
import json
from pathlib import Path
src = Path("$CANARY_BASELINE_PLAN")
dst = Path("$CANARY_BASELINE_PLAN_RUN")
data = json.loads(src.read_text())
data["corpus_path"] = "$CANARY_CORPUS"
data["output_path"] = "$CANARY_BASELINE_SCORECARD"
dst.write_text(json.dumps(data, indent=2) + "\n")
PY
  BASELINE_BIN="${BASELINE_BIN:?BASELINE_BIN must be set}" \
  BASELINE_TIMEOUT="${BASELINE_TIMEOUT:-180}" \
  ELNATH_BENCHMARK_KEEP_TMP="${ELNATH_BENCHMARK_KEEP_TMP:-}" \
  ./elnath eval run-baseline "$CANARY_BASELINE_PLAN_RUN"
fi

echo "[8/9] write canary report"
./elnath eval report "$CANARY_CORPUS" "$CANARY_CURRENT_SCORECARD" "$CANARY_BASELINE_SCORECARD" "$CANARY_REPORT"

echo "[9/9] check canary deltas"
python3 - <<PY
import json, sys
from pathlib import Path
current = json.loads(Path("$CANARY_CURRENT_SCORECARD").read_text())
baseline = json.loads(Path("$CANARY_BASELINE_SCORECARD").read_text())
def rate(scorecard, key):
    results = scorecard['results']
    if not results:
        return 0.0
    if key == 'success':
        return sum(1 for r in results if r.get('success')) / len(results)
    if key == 'verification':
        return sum(1 for r in results if r.get('verification_passed')) / len(results)
    raise ValueError(key)
cur_success = rate(current, 'success')
base_success = rate(baseline, 'success')
cur_verify = rate(current, 'verification')
base_verify = rate(baseline, 'verification')
print(f'canary success: current={cur_success:.2f} baseline={base_success:.2f}')
print(f'canary verification: current={cur_verify:.2f} baseline={base_verify:.2f}')
if cur_success < base_success or cur_verify < base_verify:
    sys.exit(1)
PY

SUMMARY_PATH="$OUTPUT_DIR/summary.md"
python3 - "$BUGFIX_CURRENT_SCORECARD" "$BUGFIX_BASELINE_SCORECARD" "$CANARY_CURRENT_SCORECARD" "$CANARY_BASELINE_SCORECARD" "$BUGFIX_REPORT" "$CANARY_REPORT" "$SUMMARY_PATH" <<'PY'
import json
import sys
from pathlib import Path

def rate(scorecard, key):
    results = scorecard['results']
    if not results:
        return 0.0
    if key == 'success':
        return sum(1 for r in results if r.get('success')) / len(results)
    if key == 'verification':
        return sum(1 for r in results if r.get('verification_passed')) / len(results)
    if key == 'recovery':
        return sum(1 for r in results if r.get('recovery_succeeded')) / len(results)
    raise ValueError(key)

bugfix_current = json.loads(Path(sys.argv[1]).read_text())
bugfix_baseline = json.loads(Path(sys.argv[2]).read_text())
canary_current = json.loads(Path(sys.argv[3]).read_text())
canary_baseline = json.loads(Path(sys.argv[4]).read_text())
bugfix_report = sys.argv[5]
canary_report = sys.argv[6]
summary_path = Path(sys.argv[7])

lines = [
    '# Month 3 Evidence Snapshot',
    '',
    '## Bugfix primary slice',
    f'Source: `{bugfix_report}`',
    '',
    f'- Current vs baseline success delta: **{rate(bugfix_current, "success") - rate(bugfix_baseline, "success"):+.2f}**',
    f'- Current vs baseline verification delta: **{rate(bugfix_current, "verification") - rate(bugfix_baseline, "verification"):+.2f}**',
    f'- Current vs baseline recovery delta: **{rate(bugfix_current, "recovery") - rate(bugfix_baseline, "recovery"):+.2f}**',
    '',
    '## Carry-forward canary',
    f'Source: `{canary_report}`',
    '',
    f'- Current vs baseline success delta: **{rate(canary_current, "success") - rate(canary_baseline, "success"):+.2f}**',
    f'- Current vs baseline verification delta: **{rate(canary_current, "verification") - rate(canary_baseline, "verification"):+.2f}**',
    f'- Current vs baseline recovery delta: **{rate(canary_current, "recovery") - rate(canary_baseline, "recovery"):+.2f}**',
    '- Manual canary delta check: **PASS**',
    '',
    '## Interpretation',
    'This cycle preserved the canary and refreshed the bugfix-vs-baseline evidence from one command path. Remaining work is to keep increasing repeat count and reduce wrapper policy hotspots, not to re-plan the roadmap.',
]
summary_path.write_text('\n'.join(lines) + '\n')
PY

echo
printf 'Bugfix report:  %s\n' "$BUGFIX_REPORT"
printf 'Canary report:  %s\n' "$CANARY_REPORT"
printf 'Summary:        %s\n' "$SUMMARY_PATH"
printf 'Bugfix current: %s\n' "$BUGFIX_CURRENT_SCORECARD"
printf 'Bugfix base:    %s\n' "$BUGFIX_BASELINE_SCORECARD"
