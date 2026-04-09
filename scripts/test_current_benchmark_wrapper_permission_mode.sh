#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-current-wrapper-test.XXXXXX")"

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
printf 'ARGS:%s\n' "$*" >>"${ELNATH_FAKE_LOG:?}"
printf 'MODE:%s\n' "${ELNATH_PERMISSION_MODE:-}" >>"${ELNATH_FAKE_LOG:?}"
printf 'PWD:%s\n' "$PWD" >>"${ELNATH_FAKE_LOG:?}"
printf '\npatched by fake elnath\n' >> README.md
EOF
  chmod +x "$bin_path"
}

assert_success_json() {
  local output_path="$1"
  python3 - <<'PY' "$output_path"
import json, sys
path = sys.argv[1]
data = json.load(open(path))
assert data["success"] is True, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "", data
PY
}

run_case() {
  local permission_mode="$1"
  local fake_log="$2"
  local output_path="$3"
  local repo_url="$4"
  if [[ "$permission_mode" == "__unset__" ]]; then
    env \
      -u ELNATH_BENCHMARK_PERMISSION_MODE \
      ELNATH_FAKE_LOG="$fake_log" \
      ELNATH_BIN="$TMP_DIR/fake-elnath.sh" \
      ELNATH_TIMEOUT=30 \
      "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" \
        "$output_path" \
        "GO-BUG-001" \
        "bugfix" \
        "go" \
        "Make a small safe change and verify it." \
        "$repo_url" \
        "" \
        "brownfield" \
        "bugfix"
    return
  fi

  ELNATH_FAKE_LOG="$fake_log" \
  ELNATH_BIN="$TMP_DIR/fake-elnath.sh" \
  ELNATH_TIMEOUT=30 \
  ELNATH_BENCHMARK_PERMISSION_MODE="$permission_mode" \
  "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" \
    "$output_path" \
    "GO-BUG-001" \
    "bugfix" \
    "go" \
    "Make a small safe change and verify it." \
    "$repo_url" \
    "" \
    "brownfield" \
    "bugfix"
}

SOURCE_REPO="$TMP_DIR/source-repo"
create_source_repo "$SOURCE_REPO"
create_fake_elnath "$TMP_DIR/fake-elnath.sh"
REPO_URL="file://$SOURCE_REPO"

DEFAULT_LOG="$TMP_DIR/default.log"
DEFAULT_OUTPUT="$TMP_DIR/default.json"
run_case "__unset__" "$DEFAULT_LOG" "$DEFAULT_OUTPUT" "$REPO_URL"
assert_success_json "$DEFAULT_OUTPUT"
grep -Fq 'ARGS:run --non-interactive' "$DEFAULT_LOG"
grep -Fq 'MODE:bypass' "$DEFAULT_LOG"

OVERRIDE_LOG="$TMP_DIR/override.log"
OVERRIDE_OUTPUT="$TMP_DIR/override.json"
run_case "accept_edits" "$OVERRIDE_LOG" "$OVERRIDE_OUTPUT" "$REPO_URL"
assert_success_json "$OVERRIDE_OUTPUT"
grep -Fq 'MODE:accept_edits' "$OVERRIDE_LOG"

echo "PASS: benchmark wrapper forces non-interactive benchmark permission mode"
