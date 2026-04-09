#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir -p \
  "$TMP_DIR/scripts" \
  "$TMP_DIR/benchmarks/results/test-cycle/canary"

cp "$REPO_ROOT/scripts/run_month3_bugfix_cycle.sh" "$TMP_DIR/scripts/"
cp \
  "$REPO_ROOT/benchmarks/bugfix-primary.v1.json" \
  "$REPO_ROOT/benchmarks/bugfix-baseline-plan.v1.json" \
  "$REPO_ROOT/benchmarks/month3-canary-current-plan.v1.json" \
  "$REPO_ROOT/benchmarks/month3-canary-corpus.v1.json" \
  "$REPO_ROOT/benchmarks/month3-canary-baseline-plan.v1.json" \
  "$TMP_DIR/benchmarks/"
cp \
  "$REPO_ROOT/benchmarks/results/month3-cycle-004/bugfix-current-scorecard.json" \
  "$REPO_ROOT/benchmarks/results/month3-cycle-004/bugfix-baseline-scorecard.json" \
  "$TMP_DIR/benchmarks/results/test-cycle/"
cp \
  "$REPO_ROOT/benchmarks/results/month3-cycle-004/canary/current-scorecard.json" \
  "$REPO_ROOT/benchmarks/results/month3-cycle-004/canary/baseline-scorecard.json" \
  "$TMP_DIR/benchmarks/results/test-cycle/canary/"

cat > "$TMP_DIR/elnath" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "eval" && "${2:-}" == "validate" ]]; then
  echo "Corpus OK: fake validator"
  exit 0
fi

if [[ "${1:-}" == "eval" && "${2:-}" == "summarize" ]]; then
  exit 0
fi

if [[ "${1:-}" == "eval" && "${2:-}" == "report" ]]; then
  output_path="${6:?missing output path}"
  printf '# fake report\n' > "$output_path"
  echo "Benchmark report written: $output_path"
  exit 0
fi

echo "unexpected fake elnath invocation: $*" >&2
exit 1
EOF
chmod +x "$TMP_DIR/elnath" "$TMP_DIR/scripts/run_month3_bugfix_cycle.sh"

(
  cd "$TMP_DIR"
  ./scripts/run_month3_bugfix_cycle.sh \
    benchmarks/results/test-cycle/bugfix-current-scorecard.json \
    benchmarks/results/test-cycle >/tmp/month3-summary-test.log
)

SUMMARY_PATH="$TMP_DIR/benchmarks/results/test-cycle/summary.md"

grep -Fq 'Current failure families: **no_changes=2**' "$SUMMARY_PATH"
grep -Fq 'Baseline failure families: **execution_timeout=4**' "$SUMMARY_PATH"
grep -Fq 'Manual canary delta check: **INCONCLUSIVE (both current and baseline failed every canary task)**' "$SUMMARY_PATH"
grep -Fq 'This cycle is inconclusive: current failed every bugfix and canary task' "$SUMMARY_PATH"

echo "PASS: month3 summary marks all-failure zero-delta cycles inconclusive"
