#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK_SCRIPT="$REPO_ROOT/scripts/check_month4_alpha_readiness.sh"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

make_fixture() {
  local root="$1"
  mkdir -p \
    "$root/benchmarks/results/canary-targeted-repair" \
    "$root/internal/daemon" \
    "$root/cmd/elnath" \
    "$root/wiki"

  cat >"$root/internal/daemon/queue.go" <<'EOGO'
package daemon
type TimeoutMetrics struct {
	FalseTimeoutRate float64
}
EOGO

  cat >"$root/internal/daemon/queue_test.go" <<'EOGO'
package daemon
func TestRecoverStaleTimeoutMetrics() {}
EOGO

  cat >"$root/internal/daemon/daemon_test.go" <<'EOGO'
package daemon
func TestDaemonSubmitAndStatus() {}
EOGO

  cat >"$root/cmd/elnath/runtime_test.go" <<'EOGO'
package main
func TestExecutionRuntimeRunTaskEmitsStructuredProgressEvents() {}
EOGO
}

fail_root="$TMP_DIR/fail"
make_fixture "$fail_root"
cat >"$fail_root/benchmarks/results/canary-targeted-repair/review.md" <<'EOF_FAIL'
Canary-only recapture: still pending follow-up after the targeted repair evidence is integrated.
EOF_FAIL

if "$CHECK_SCRIPT" "$fail_root" >"$TMP_DIR/fail.out" 2>&1; then
  echo "expected fail fixture to return non-zero" >&2
  exit 1
fi
grep -Fq 'FAIL | confirmatory_canary' "$TMP_DIR/fail.out"
grep -Fq 'FAIL | telegram_operator_shell' "$TMP_DIR/fail.out"
grep -Fq 'FAIL | alpha_onboarding_docs' "$TMP_DIR/fail.out"

pass_root="$TMP_DIR/pass"
make_fixture "$pass_root"
mkdir -p \
  "$pass_root/benchmarks/results/month4-confirmatory-canary" \
  "$pass_root/internal/telegram"

cat >"$pass_root/benchmarks/results/month4-confirmatory-canary/summary.md" <<'EOF_PASS'
# Confirmatory canary checkpoint
EOF_PASS

cat >"$pass_root/internal/telegram/bridge.go" <<'EOF_PASS'
package telegram
EOF_PASS

cat >"$pass_root/wiki/closed-alpha-setup.md" <<'EOF_PASS'
# Closed alpha setup

First successful task
EOF_PASS

cat >"$pass_root/wiki/closed-alpha-runbook.md" <<'EOF_PASS'
# Closed alpha runbook

Telemetry snapshot
EOF_PASS

cat >"$pass_root/wiki/closed-alpha-known-limits.md" <<'EOF_PASS'
# Closed alpha limits

Telegram must stay a thin companion shell.
EOF_PASS

"$CHECK_SCRIPT" "$pass_root" >"$TMP_DIR/pass.out" 2>&1
grep -Fq 'PASS | confirmatory_canary' "$TMP_DIR/pass.out"
grep -Fq 'PASS | telegram_operator_shell' "$TMP_DIR/pass.out"
grep -Fq 'PASS | alpha_onboarding_docs' "$TMP_DIR/pass.out"
grep -Fq 'Overall: PASS' "$TMP_DIR/pass.out"

echo "PASS: month4 readiness gate rejects docs-only evidence and passes once every required artifact exists"
