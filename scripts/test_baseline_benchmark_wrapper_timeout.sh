#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-baseline-timeout-test.XXXXXX")"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

python3 - <<'PY' "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh"
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text()
required = [
    "BASELINE_VERIFY_TIMEOUT",
    "verification command timed out after",
]
missing = [snippet for snippet in required if snippet not in text]
if missing:
    raise SystemExit("baseline wrapper missing bounded verification timeout support: " + ", ".join(missing))
PY

SOURCE_REPO="$TMP_DIR/source-repo"
mkdir -p "$SOURCE_REPO"
cat >"$SOURCE_REPO/go.mod" <<'EOF'
module example.com/baseline

go 1.22
EOF
cat >"$SOURCE_REPO/main.go" <<'EOF'
package baseline

func Answer() int { return 41 }
EOF
rm -f "$SOURCE_REPO/main.go" "$SOURCE_REPO/go.mod"
cat >"$SOURCE_REPO/package.json" <<'EOF'
{"scripts":{"test":"sh -c 'sleep 30'"}}
EOF
cat >"$SOURCE_REPO/index.js" <<'EOF'
module.exports = 41
EOF
git -C "$SOURCE_REPO" init -q
git -C "$SOURCE_REPO" add .
git -C "$SOURCE_REPO" -c user.name='Test User' -c user.email='test@example.com' commit -qm "init"

OUTPUT="$TMP_DIR/result.json"
BASELINE_TASK_CMD_TEMPLATE='printf "\n// baseline changed\n" >> index.js' \
BASELINE_TIMEOUT=5 \
BASELINE_VERIFY_TIMEOUT=1 \
"$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh" \
  "$OUTPUT" \
  "JS-BUG-001" \
  "bugfix" \
  "javascript" \
  "Fix a JavaScript regression and verify it with the package test script." \
  "file://$SOURCE_REPO" \
  "" \
  "service_backend" \
  "brownfield_primary"

python3 - <<'PY' "$OUTPUT"
import json
import sys

data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "verification_failed", data
assert "timed out" in data["notes"], data
PY

echo "PASS: baseline benchmark wrapper verification timeout is bounded"
