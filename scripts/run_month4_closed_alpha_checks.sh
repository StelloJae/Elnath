#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  scripts/run_month4_closed_alpha_checks.sh [--data-dir <dir>] [--report-out <path>]

Runs the lane-4 closed-alpha verification bundle:
- make lint
- make test
- make build
- scripts/test_alpha_telemetry_report.sh
- scripts/alpha_telemetry_report.sh (when the requested data dir already has elnath.db)

Options:
  --report-out PATH   Persist the live telemetry JSON snapshot when a database exists
USAGE
}

DATA_DIR="${ELNATH_DATA_DIR:-$HOME/.elnath/data}"
REPORT_OUT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --data-dir)
      [[ $# -ge 2 ]] || { echo "error: --data-dir requires a value" >&2; exit 1; }
      DATA_DIR="$2"
      shift 2
      ;;
    --report-out)
      [[ $# -ge 2 ]] || { echo "error: --report-out requires a value" >&2; exit 1; }
      REPORT_OUT="$2"
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

run_step() {
  local label="$1"
  shift
  echo "==> $label"
  "$@"
}

run_step "lint" make lint
run_step "test" make test
run_step "build" make build
run_step "telemetry reporter self-test" bash scripts/test_alpha_telemetry_report.sh

if [[ -f "$DATA_DIR/elnath.db" ]]; then
  if [[ -n "$REPORT_OUT" ]]; then
    run_step "telemetry summary" bash scripts/alpha_telemetry_report.sh --data-dir "$DATA_DIR" --out "$REPORT_OUT"
  else
    run_step "telemetry summary" bash scripts/alpha_telemetry_report.sh --data-dir "$DATA_DIR"
  fi
else
  echo "==> telemetry summary"
  echo "skipped: no database found at $DATA_DIR/elnath.db"
fi
