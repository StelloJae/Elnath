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
printf 'DATA:%s\n' "${ELNATH_DATA_DIR:-}" >>"${ELNATH_FAKE_LOG:?}"
printf 'WIKI:%s\n' "${ELNATH_WIKI_DIR:-}" >>"${ELNATH_FAKE_LOG:?}"
printf 'HOME:%s\n' "${HOME:-}" >>"${ELNATH_FAKE_LOG:?}"
printf 'TMPDIR:%s\n' "${TMPDIR:-}" >>"${ELNATH_FAKE_LOG:?}"
printf 'TMP:%s\n' "${TMP:-}" >>"${ELNATH_FAKE_LOG:?}"
printf 'TEMP:%s\n' "${TEMP:-}" >>"${ELNATH_FAKE_LOG:?}"
printf 'GOMODCACHE:%s\n' "${GOMODCACHE:-}" >>"${ELNATH_FAKE_LOG:?}"
printf 'GOCACHE:%s\n' "${GOCACHE:-}" >>"${ELNATH_FAKE_LOG:?}"
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

assert_isolated_elnath_state() {
  local log_path="$1"
  python3 - <<'PY' "$log_path"
from pathlib import Path
import os
import sys

log = Path(sys.argv[1]).read_text().splitlines()
values = {}
for line in log:
    key, sep, value = line.partition(":")
    if sep:
        values.setdefault(key, value)

pwd = values.get("PWD", "")
data = values.get("DATA", "")
wiki = values.get("WIKI", "")
assert pwd, values
assert data, values
assert wiki, values
for label, path in (("DATA", data), ("WIKI", wiki)):
    common = os.path.commonpath([os.path.abspath(pwd), os.path.abspath(path)])
    assert common != os.path.abspath(pwd), f"{label} dir is inside benchmark target repo: {path}"
PY
}

assert_isolated_benchmark_env() {
  local log_path="$1"
  python3 - <<'PY' "$log_path"
from pathlib import Path
import os
import sys

log = Path(sys.argv[1]).read_text().splitlines()
values = {}
for line in log:
    key, sep, value = line.partition(":")
    if sep:
        values.setdefault(key, value)

pwd = values.get("PWD", "")
assert pwd, values
for label in ("HOME", "TMPDIR", "TMP", "TEMP", "GOMODCACHE", "GOCACHE"):
    path = values.get(label, "")
    assert path, f"{label} was not set for benchmark run: {values}"
    common = os.path.commonpath([os.path.abspath(pwd), os.path.abspath(path)])
    assert common != os.path.abspath(pwd), f"{label} is inside benchmark target repo: {path}"
PY
}

assert_no_benchmark_noise_in_target_repo() {
  local log_path="$1"
  python3 - <<'PY' "$log_path"
from pathlib import Path
import sys

log = Path(sys.argv[1]).read_text().splitlines()
values = {}
for line in log:
    key, sep, value = line.partition(":")
    if sep:
        values.setdefault(key, value)

pwd = Path(values["PWD"])
for name in ("go", "Library", ".tmp", ".cache"):
    assert not (pwd / name).exists(), f"benchmark target repo gained {name}"
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
assert_isolated_elnath_state "$DEFAULT_LOG"
assert_isolated_benchmark_env "$DEFAULT_LOG"
assert_no_benchmark_noise_in_target_repo "$DEFAULT_LOG"
grep -Fq 'ARGS:run --non-interactive' "$DEFAULT_LOG"
grep -Fq 'MODE:bypass' "$DEFAULT_LOG"

OVERRIDE_LOG="$TMP_DIR/override.log"
OVERRIDE_OUTPUT="$TMP_DIR/override.json"
run_case "accept_edits" "$OVERRIDE_LOG" "$OVERRIDE_OUTPUT" "$REPO_URL"
assert_success_json "$OVERRIDE_OUTPUT"
assert_isolated_elnath_state "$OVERRIDE_LOG"
assert_isolated_benchmark_env "$OVERRIDE_LOG"
assert_no_benchmark_noise_in_target_repo "$OVERRIDE_LOG"
grep -Fq 'MODE:accept_edits' "$OVERRIDE_LOG"

echo "PASS: benchmark wrapper forces non-interactive benchmark permission mode"
