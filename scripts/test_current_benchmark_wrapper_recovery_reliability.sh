#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-current-recovery-reliability.XXXXXX")"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

create_source_repo() {
  local repo_dir="$1"
  mkdir -p "$repo_dir"
  cat >"$repo_dir/go.mod" <<'EOF'
module example.com/recovery

go 1.22
EOF
  cat >"$repo_dir/main.go" <<'EOF'
package recovery

func Answer() int { return 42 }
EOF
  cat >"$repo_dir/main_test.go" <<'EOF'
package recovery

import "testing"

func TestAnswer(t *testing.T) {
	if got := Answer(); got != 42 {
		t.Fatalf("Answer() = %d, want 42", got)
	}
}
EOF
  git -C "$repo_dir" init -q
  git -C "$repo_dir" add .
  git -C "$repo_dir" -c user.name='Test User' -c user.email='test@example.com' commit -qm "init"
}

create_fake_elnath() {
  local bin_path="$1"
  cat >"$bin_path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

prompt="${*: -1}"
count_file=".fake-elnath-count"
count=0
if [[ -f "$count_file" ]]; then
  count="$(cat "$count_file")"
fi
count=$((count + 1))
printf '%s' "$count" > "$count_file"

case "${FAKE_SCENARIO:?}" in
  incomplete_colon)
    if [[ "$count" -eq 1 ]]; then
      cat > main.go <<'GO'
package recovery

func Answer() int { return 0 }
GO
      echo "Modified files: main.go"
      echo "Verification: go test ./... failed."
    else
      echo "Incomplete: I fixed the first issue, but verification still fails because a remaining issue blocks completion."
      echo "Remaining issue: target identification problem remains."
    fi
    ;;
  incomplete_colon_after_preamble)
    if [[ "$count" -eq 1 ]]; then
      cat > main.go <<'GO'
package recovery

func Answer() int { return 0 }
GO
      echo "Modified files: main.go"
      echo "Verification: go test ./... failed."
    else
      echo "Progress update: I checked the retry path and compile output."
      echo "Incomplete: compile error remains after recovery."
    fi
    ;;
  remaining_target_issue)
    if [[ "$count" -eq 1 ]]; then
      cat > main.go <<'GO'
package recovery

func Answer() int { return 0 }
GO
      echo "Modified files: main.go"
      echo "Verification: go test ./... failed."
    else
      echo "I fixed the first issue, but verification is still failing because the target identification problem remains."
    fi
    ;;
  fixed_after_unresolved)
    cat > main.go <<'GO'
package recovery

func Answer() int { return 42 }
GO
    printf '\npatched by fake elnath\n' >> README.md
    echo "I found an unresolved import in the first pass, then fixed it."
    echo "Modified files: README.md"
    echo "Verification: go test ./... passed."
    ;;
  unused_import)
    cat > main.go <<'GO'
package recovery

import "fmt"

func Answer() int { return 42 }
GO
    echo "Modified files: main.go"
    echo "Verification: go test ./... failed."
    ;;
  unused_variable)
    cat > main.go <<'GO'
package recovery

func Answer() int {
	unused := 1
	return 42
}
GO
    echo "Modified files: main.go"
    echo "Verification: go test ./... failed."
    ;;
  assertion_failure)
    cat > main.go <<'GO'
package recovery

func Answer() int { return 0 }
GO
    echo "Modified files: main.go"
    echo "Verification: go test ./... failed."
    ;;
  record_prompt)
    printf '%s\n' "$prompt" > "$PROMPT_CAPTURE"
    cat > main.go <<'GO'
package recovery

func Answer() int { return 0 }
GO
    echo "Modified files: main.go"
    echo "Verification: go test ./... failed."
    ;;
  *)
    echo "unknown FAKE_SCENARIO=${FAKE_SCENARIO}" >&2
    exit 2
    ;;
esac
EOF
  chmod +x "$bin_path"
}

hash_static_inputs() {
  python3 - <<'PY' "$REPO_ROOT"
from hashlib import sha256
from pathlib import Path
import sys

root = Path(sys.argv[1])
paths = [
    "benchmarks/month3-canary-corpus.v1.json",
    "benchmarks/public-corpus.v1.json",
    "benchmarks/brownfield-primary.v1.json",
    "scripts/run_baseline_benchmark_wrapper.sh",
]
for rel in paths:
    path = root / rel
    print(rel + "=" + sha256(path.read_bytes()).hexdigest())
PY
}

run_wrapper_case() {
  local scenario="$1"
  local output_path="$2"
  local source_repo="$3"
  FAKE_SCENARIO="$scenario" \
  ELNATH_BIN="$TMP_DIR/fake-elnath.sh" \
  ELNATH_TIMEOUT=30 \
  ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
  HOME="$TMP_DIR/host-home" \
  "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" \
    "$output_path" \
    "GO-BF-002" \
    "bugfix" \
    "go" \
    "Extend an existing Go worker service so graceful shutdown emits structured progress logging and does not regress existing worker behavior." \
    "file://$source_repo" \
    "" \
    "service_backend" \
    "month3_canary"
}

assert_json_case() {
  local output_path="$1"
  local python_assert="$2"
  python3 - <<'PY' "$output_path" "$python_assert"
import json
import sys

path = sys.argv[1]
code = sys.argv[2]
data = json.load(open(path))
ns = {"data": data, "path": path}
exec(code, ns, ns)
PY
}

SOURCE_REPO="$TMP_DIR/source-repo"
create_source_repo "$SOURCE_REPO"
create_fake_elnath "$TMP_DIR/fake-elnath.sh"
mkdir -p "$TMP_DIR/host-home"
before_hash="$(hash_static_inputs)"

run_wrapper_case incomplete_colon "$TMP_DIR/incomplete-colon.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/incomplete-colon.json" '
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert data["final_incomplete_detected"] is True, data
'

run_wrapper_case incomplete_colon_after_preamble "$TMP_DIR/incomplete-colon-after-preamble.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/incomplete-colon-after-preamble.json" '
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert data["final_incomplete_detected"] is True, data
'

run_wrapper_case remaining_target_issue "$TMP_DIR/remaining-target-issue.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/remaining-target-issue.json" '
assert data["success"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert data["final_incomplete_detected"] is True, data
'

run_wrapper_case fixed_after_unresolved "$TMP_DIR/fixed-after-unresolved.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/fixed-after-unresolved.json" '
assert data["success"] is True, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "", data
assert data["final_incomplete_detected"] is False, data
'

run_wrapper_case unused_import "$TMP_DIR/unused-import.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/unused-import.json" '
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert data["final_incomplete_detected"] is False, data
'

run_wrapper_case unused_variable "$TMP_DIR/unused-variable.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/unused-variable.json" '
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert data["final_incomplete_detected"] is False, data
'

run_wrapper_case assertion_failure "$TMP_DIR/assertion-failure.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/assertion-failure.json" '
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "verification_failed", data
assert data["final_incomplete_detected"] is False, data
'

PROMPT_CAPTURE="$TMP_DIR/recovery-prompt.txt" run_wrapper_case record_prompt "$TMP_DIR/record-prompt.json" "$SOURCE_REPO"
python3 - <<'PY' "$TMP_DIR/recovery-prompt.txt"
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text()
required = [
    "compile cleanly",
    "unused imports",
    "unused variables",
    "verification still fails",
    "partial symptom fixes",
]
missing = [snippet for snippet in required if snippet not in text]
if missing:
    raise SystemExit("recovery prompt missing: " + ", ".join(missing))
PY

run_wrapper_case incomplete_colon "$TMP_DIR/redaction-check.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/redaction-check.json" '
summary = data["trace_summary"]
assert len(summary) <= 500, data
assert "Verification output" not in summary, data
assert "Incomplete:" not in summary, data
'

after_hash="$(hash_static_inputs)"
if [[ "$before_hash" != "$after_hash" ]]; then
  echo "benchmark corpus or baseline wrapper mutated" >&2
  exit 1
fi

echo "PASS: current benchmark wrapper recovery reliability guards classify incomplete recovery"
