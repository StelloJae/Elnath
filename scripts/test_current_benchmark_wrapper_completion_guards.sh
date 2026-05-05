#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-current-wrapper-guards.XXXXXX")"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

create_source_repo() {
  local repo_dir="$1"
  mkdir -p "$repo_dir"
  cat >"$repo_dir/go.mod" <<'EOF'
module example.com/benchmark

go 1.22
EOF
  cat >"$repo_dir/main.go" <<'EOF'
package benchmark

func Answer() int { return 42 }
EOF
  cat >"$repo_dir/main_test.go" <<'EOF'
package benchmark

import "testing"

func TestAnswer(t *testing.T) {
	if got := Answer(); got != 42 {
		t.Fatalf("Answer() = %d, want 42", got)
	}
}
EOF
  cat >"$repo_dir/README.md" <<'EOF'
benchmark fixture
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
case "${FAKE_SCENARIO:?}" in
  intent_no_diff)
    echo "I am patching the graceful shutdown behavior now."
    echo "I will modify the worker file and add verification evidence."
    ;;
  neutral_no_diff)
    echo "Nothing to do."
    echo "The repository already matches the requested state."
    ;;
  no_diff_writing_findings)
    echo "I am writing up findings."
    echo "The repository already matches the requested state."
    ;;
  incomplete_with_diff)
    printf '\npatched by fake elnath\n' >> README.md
    echo "I changed README.md, but I did not complete the requested runtime fix."
    echo "Missing regression test, cannot honestly claim completion."
    ;;
  complete_with_diff)
    printf '\npatched by fake elnath\n' >> README.md
    echo "Modified files: README.md"
    echo "Verification: go test ./... passed."
    ;;
  fixed_after_unresolved)
    printf '\npatched by fake elnath\n' >> README.md
    echo "I found an unresolved import in the first pass, then fixed it."
    echo "Modified files: README.md"
    echo "Verification: go test ./... passed."
    ;;
  redaction_probe)
    printf '\npatched by fake elnath\n' >> README.md
    python3 - <<'PY'
print("SECRET_TOKEN=should-not-appear " + "x" * 2000)
PY
    echo "Modified files: README.md"
    echo "Verification: go test ./... passed."
    ;;
  *)
    echo "unknown FAKE_SCENARIO=${FAKE_SCENARIO}" >&2
    exit 2
    ;;
esac
EOF
  chmod +x "$bin_path"
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
    "Fix the benchmark fixture and verify it." \
    "file://$source_repo" \
    "" \
    "brownfield" \
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
ns = {"data": data}
exec(code, ns, ns)
PY
}

SOURCE_REPO="$TMP_DIR/source-repo"
create_source_repo "$SOURCE_REPO"
create_fake_elnath "$TMP_DIR/fake-elnath.sh"
mkdir -p "$TMP_DIR/host-home"

run_wrapper_case intent_no_diff "$TMP_DIR/intent-no-diff.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/intent-no-diff.json" '
assert data["success"] is False, data
assert data["failure_family"] == "no_change_planning_failure", data
assert data["edit_intent_detected"] is True, data
assert data["changed_files"] == [], data
'

run_wrapper_case neutral_no_diff "$TMP_DIR/neutral-no-diff.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/neutral-no-diff.json" '
assert data["success"] is False, data
assert data["failure_family"] == "no_changes", data
assert data["edit_intent_detected"] is False, data
'

run_wrapper_case no_diff_writing_findings "$TMP_DIR/no-diff-writing-findings.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/no-diff-writing-findings.json" '
assert data["success"] is False, data
assert data["failure_family"] == "no_changes", data
assert data["edit_intent_detected"] is False, data
'

run_wrapper_case incomplete_with_diff "$TMP_DIR/incomplete-with-diff.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/incomplete-with-diff.json" '
assert data["success"] is False, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "incomplete_patch", data
assert data["final_incomplete_detected"] is True, data
assert "README.md" in data["changed_files"], data
'

run_wrapper_case complete_with_diff "$TMP_DIR/complete-with-diff.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/complete-with-diff.json" '
assert data["success"] is True, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "", data
assert data["final_incomplete_detected"] is False, data
assert "README.md" in data["changed_files"], data
'

run_wrapper_case fixed_after_unresolved "$TMP_DIR/fixed-after-unresolved.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/fixed-after-unresolved.json" '
assert data["success"] is True, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "", data
assert data["final_incomplete_detected"] is False, data
assert "README.md" in data["changed_files"], data
'

run_wrapper_case redaction_probe "$TMP_DIR/redaction-probe.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/redaction-probe.json" '
summary = data["trace_summary"]
assert len(summary) <= 500, data
assert "SECRET_TOKEN" not in summary, data
assert "x" * 100 not in summary, data
'

echo "PASS: current benchmark wrapper completion guards classify no-change/incomplete runs"
