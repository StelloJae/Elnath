#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CURRENT_WRAPPER="$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh"
BASELINE_WRAPPER="$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh"

python3 - "$CURRENT_WRAPPER" "$BASELINE_WRAPPER" "$REPO_ROOT" <<'PY'
from pathlib import Path
import re
import sys

current = Path(sys.argv[1])
baseline = Path(sys.argv[2])
repo_root = Path(sys.argv[3])
current_text = current.read_text()
baseline_text = baseline.read_text()
current_guidance = current_text.replace("\\`", "`")
baseline_guidance = baseline_text.replace("\\`", "`")

expected_cmd = "npx pnpm -C packages/vitest build && npx pnpm -C test/cli exec vitest --run test/worker-retry-telemetry.test.ts"
broad_cmd = "npx pnpm build && npx pnpm -C test/cli exec vitest --run test/worker-retry-telemetry.test.ts"

if expected_cmd not in current_text:
    raise SystemExit("current wrapper lost the narrow TS-BF-001 verification command")
if expected_cmd not in baseline_text:
    raise SystemExit("baseline wrapper lost the narrow TS-BF-001 verification command")
if broad_cmd in baseline_text:
    raise SystemExit("baseline wrapper still contains the broad TS-BF-001 verification command")

if "exact equality against the expected retry sequence" in current_guidance:
    raise SystemExit("TS-BF-001 guidance still suggests an exact global retry sequence assertion")
if "Benchmark TS-BF-001 retry telemetry guidance:" in baseline_guidance:
    raise SystemExit("baseline wrapper should not rewrite the baseline task prompt with TS-BF-001 repair guidance")

required_current_guidance = [
    "`reported-tasks` fixture contains multiple retry/repeat/failure cases",
    "Do not assert the global `test-retried` event list",
    "isolate the target retried test by task id/name",
    "target task's retry telemetry is missing",
    "valid extra retry/fail events from other tests",
]
missing = [snippet for snippet in required_current_guidance if snippet not in current_guidance]
if missing:
    raise SystemExit("current wrapper missing TS-BF-001 guidance: " + ", ".join(missing))

if not re.search(r"target\s+task.*retryCount.*1.*2", current_guidance, re.S):
    raise SystemExit("TS-BF-001 guidance does not require retryCount 1/2 on the isolated target task")

for corpus in (
    "benchmarks/month3-canary-corpus.v1.json",
    "benchmarks/public-corpus.v1.json",
    "benchmarks/brownfield-primary.v1.json",
):
    path = repo_root / corpus
    if not path.exists():
        raise SystemExit(f"expected corpus file missing: {corpus}")

print("PASS: TS-BF-001 benchmark guidance rejects broad retry-stream assertions")
PY
