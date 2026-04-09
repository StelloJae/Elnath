#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-.}"
RESULTS_DIR="$ROOT/benchmarks/results"
DOC_GLOBS=("$ROOT/README.md" "$ROOT/wiki" "$ROOT/docs")
CODE_GLOBS=("$ROOT/internal" "$ROOT/cmd" "$ROOT/README.md" "$ROOT/wiki" "$ROOT/docs")

if [[ ! -d "$ROOT" ]]; then
  echo "root path not found: $ROOT" >&2
  exit 2
fi

failures=0
rows=()

record() {
  local gate="$1"
  local status="$2"
  local evidence="$3"
  rows+=("$status|$gate|$evidence")
  if [[ "$status" == "FAIL" ]]; then
    failures=$((failures + 1))
  fi
}

first_match() {
  local pattern="$1"
  shift || true
  local path
  while IFS= read -r path; do
    [[ -n "$path" ]] && {
      printf '%s' "$path"
      return 0
    }
  done < <(find "$@" 2>/dev/null \( -type f -o -type l \) | sort | grep -E "$pattern" || true)
  return 1
}

if confirmatory=$(first_match 'confirmatory|month4-confirmatory|closed-alpha-checkpoint' "$RESULTS_DIR"); then
  record "confirmatory_canary" "PASS" "$confirmatory"
elif [[ -f "$RESULTS_DIR/canary-targeted-repair/review.md" ]] &&
  grep -qi 'pending follow-up' "$RESULTS_DIR/canary-targeted-repair/review.md"; then
  record "confirmatory_canary" "FAIL" "benchmarks/results/canary-targeted-repair/review.md still says confirmatory canary follow-up is pending"
else
  record "confirmatory_canary" "FAIL" "no confirmatory Month 3 canary artifact found under benchmarks/results"
fi

queueTest="$ROOT/internal/daemon/queue_test.go"
runtimeTest="$ROOT/cmd/elnath/runtime_test.go"
daemonTest="$ROOT/internal/daemon/daemon_test.go"
if [[ -f "$queueTest" && -f "$runtimeTest" && -f "$daemonTest" ]] &&
  grep -q 'TestRecoverStaleTimeoutMetrics' "$queueTest" &&
  grep -q 'TestExecutionRuntimeRunTaskEmitsStructuredProgressEvents' "$runtimeTest" &&
  grep -q 'TestDaemonSubmitAndStatus' "$daemonTest"; then
  record "continuity_runtime_core" "PASS" "internal/daemon/queue_test.go + internal/daemon/daemon_test.go + cmd/elnath/runtime_test.go"
else
  record "continuity_runtime_core" "FAIL" "missing core continuity/runtime regression coverage anchors"
fi

telegramHit=""
for target in "${CODE_GLOBS[@]}"; do
  [[ -e "$target" ]] || continue
  if telegramHit=$(rg -il 'telegram' "$target" 2>/dev/null | head -n 1); [[ -n "$telegramHit" ]]; then
    break
  fi
done
if [[ -n "$telegramHit" ]]; then
  record "telegram_operator_shell" "PASS" "$telegramHit"
else
  record "telegram_operator_shell" "FAIL" "no Telegram operator shell evidence found in cmd/internal/docs"
fi

docHit=""
for target in "${DOC_GLOBS[@]}"; do
  [[ -e "$target" ]] || continue
  if docHit=$(rg -il 'closed alpha|first successful task|known limits|troubleshooting' "$target" 2>/dev/null | head -n 1); [[ -n "$docHit" ]]; then
    break
  fi
done
if [[ -n "$docHit" ]]; then
  record "alpha_onboarding_docs" "PASS" "$docHit"
else
  record "alpha_onboarding_docs" "FAIL" "missing closed-alpha onboarding / troubleshooting / known-limits documentation evidence"
fi

queueGo="$ROOT/internal/daemon/queue.go"
if [[ -f "$queueGo" && -f "$queueTest" ]] &&
  grep -q 'type TimeoutMetrics struct' "$queueGo" &&
  grep -q 'FalseTimeoutRate' "$queueGo" &&
  grep -q 'TestRecoverStaleTimeoutMetrics' "$queueTest"; then
  record "telemetry_timeouts" "PASS" "internal/daemon/queue.go + internal/daemon/queue_test.go timeout metrics coverage"
else
  record "telemetry_timeouts" "FAIL" "timeout telemetry implementation or coverage anchors missing"
fi

printf '# Month 4 Closed Alpha Readiness Gates\n\n'
printf '| Status | Gate | Evidence |\n'
printf '| --- | --- | --- |\n'
for row in "${rows[@]}"; do
  IFS='|' read -r status gate evidence <<<"$row"
  printf '| %s | %s | %s |\n' "$status" "$gate" "$evidence"
done
printf '\n'

if (( failures > 0 )); then
  printf 'Overall: FAIL (%d gate(s) missing)\n' "$failures"
  exit 1
fi

printf 'Overall: PASS\n'
