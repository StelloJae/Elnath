#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

python3 - <<'PY' \
  "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" \
  "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh"
import sys
from pathlib import Path

required = [
    "yarn install --immutable --mode=skip-build",
    "yarn install --mode=skip-build",
    "yarnPath:",
    "nodeLinker:",
    'startswith("yarn@")',
]

for path_arg in sys.argv[1:]:
    path = Path(path_arg)
    text = path.read_text()
    missing = [snippet for snippet in required if snippet not in text]
    if missing:
        raise SystemExit(f"{path.name} missing Yarn Berry install guard: {', '.join(missing)}")

print("PASS: benchmark wrappers use Yarn Berry install mode for node-modules projects")
PY
