#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-current-wrapper-guards.XXXXXX")"

python3 - <<'PY' "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh"
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text().replace("\\`", "`")
required = [
    "GO-BF-001 request-id logging guidance:",
    "`logger.go`",
    "Do not modify `gin.go` or the `Default()` middleware chain",
    "`TestCreateDefaultRouter` expects only the existing default logger/recovery handlers",
    "Preserve `LogFormatterParams.BodySize`",
    "Do not finish with only a `requestIDKey` constant or `RequestID` field",
    "add an opt-in `RequestID()` middleware",
    "set `param.RequestID` inside `LoggerWithConfig`",
    "Do not assert a local `buffer.String()` after `LoggerWithFormatter(...)` unless the test redirects `DefaultWriter`",
    "Prefer `LoggerWithConfig(LoggerConfig{Formatter: ..., Output: buffer})` for focused request-id logger tests",
    "GO-BF-002 graceful shutdown guidance:",
    "`caddy.go`",
    "`unsyncedStop(ctx Context)`",
    "structured Zap progress logs around each app `Stop()` call",
    "`zap.String(\"app\", name)`",
    "Do not finish with no diff",
    "`go test -p 1 ./... -count=1`",
    "GO-BUG-001 timeout propagation guidance:",
    "`command_run.go`",
    "preserve the parent deadline when a `Before` hook returns a replacement context",
    "Do not stop after diagnosing the context propagation path",
    "If recovery starts from no diff, make the smallest concrete patch in `command_run.go`",
    "Viper-specific guidance:",
    "`WatchConfig()`",
    "Add a focused regression test under the existing `TestWatchFile` test group",
    "Do not finish with only `viper.go` changed",
    "GO-BUG-002 no-change recovery guard:",
    "set `v.configFile = filename` immediately before `ReadInConfig()`",
    "If an exact-context patch misses due to spacing or indentation",
    "`rg -n \"realConfigFile = currentConfigFile|ReadInConfig\" viper.go`",
    "append a focused subtest under `TestWatchFile`",
    "If the `viper_test.go` insertion anchor misses",
    "`rg -n \"func TestWatchFile|OnConfigChange|WatchConfig\" viper_test.go`",
    "Use a bounded wait",
    "Do not add a bare `wg.Wait()`",
    "A `viper.go`-only diff is incomplete",
    "V8-PY-BUG-002 click parser guidance:",
    "`src/click/parser.py`",
    "`tests/test_parser.py`",
    "Do not spend the recovery turn re-reading broad Click internals after identifying `_Option` prefix handling",
    "Use the known-good focused regression shape: `++foo` must not make `+value` look like an option",
    "Do not chase the optional-value negative-number path first",
    "Run `python3 -m pytest tests/test_parser.py -q` before the final answer",
]
missing = [snippet for snippet in required if snippet not in text]
if missing:
    raise SystemExit("current wrapper missing GO-BF-002 guidance: " + ", ".join(missing))
PY

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
count_file="${FAKE_COUNT_FILE:-.fake-elnath-count}"
count=0
if [[ -f "$count_file" ]]; then
  count="$(cat "$count_file")"
fi
count=$((count + 1))
printf '%s' "$count" > "$count_file"

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
  regression_not_added_with_diff)
    printf '\npatched by fake elnath\n' >> README.md
    echo "Modified files: README.md"
    echo "Verification: go test ./... passed."
    echo "I attempted to add the focused regression test, but the insertion anchor did not match."
    echo "The focused regression test was not added."
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
  many_untracked_files)
    python3 - <<'PY'
from pathlib import Path
root = Path("generated")
root.mkdir(exist_ok=True)
for i in range(1, 121):
    (root / f"file-{i:03d}.txt").write_text(f"generated {i}\n")
PY
    echo "Modified files: generated/*"
    echo "Verification: go test ./... passed."
    ;;
  go_bug002_viper_only)
    cat > viper.go <<'GO'
package benchmark

func WatchConfig() {}
GO
    echo "Modified files: viper.go"
    echo "Verification: go test ./... passed."
    ;;
  go_bf001_trivial_request_id_key)
    cat > logger.go <<'GO'
package benchmark

const requestIDKey = "requestID"
GO
    echo "Modified files: logger.go"
    echo "Verification: go test ./... passed."
    ;;
  go_bf001_unwired_logger_formatter)
    cat > logger.go <<'GO'
package benchmark

type Context struct{}
type HandlerFunc func(*Context)
type LogFormatterParams struct{ RequestID string }

const requestIDKey = "requestID"

func (c *Context) Set(string, any) {}

func RequestID() HandlerFunc {
	return func(c *Context) {
		c.Set(requestIDKey, "req-1")
	}
}

func setFormatterParam() LogFormatterParams {
	return LogFormatterParams{RequestID: "req-1"}
}
GO
    cat > logger_test.go <<'GO'
package benchmark

import (
	"strings"
	"testing"
)

func LoggerWithFormatter(func(LogFormatterParams) string) HandlerFunc {
	return func(*Context) {}
}

func TestLoggerWithRequestID(t *testing.T) {
	buffer := new(strings.Builder)
	_ = LoggerWithFormatter(func(param LogFormatterParams) string {
		return param.RequestID
	})
	if buffer.String() == "req-1" {
		t.Fatal("unreachable")
	}
}
GO
    echo "Modified files: logger.go, logger_test.go"
    echo "Verification: go test ./... passed."
    ;;
  v8_mix_bf001_no_fixture_test)
    mkdir -p api/internal/accumulator
    cat > api/internal/accumulator/namereferencetransformer.go <<'GO'
package accumulator

func TransformNameReferencesInStableResourceOrder() {}
GO
    cat > go.work.sum <<'SUMEOF'
example.com/checksum v1.0.0 h1:abc
SUMEOF
    echo "Modified files: api/internal/accumulator/namereferencetransformer.go, go.work.sum"
    echo "Verification: go test ./... passed."
    ;;
  go_bug002_unbounded_wait_test)
    cat > viper.go <<'GO'
package benchmark

func WatchConfig() {}
GO
    cat > viper_test.go <<'GO'
package benchmark

import (
	"sync"
	"testing"
)

func TestWatchFile(t *testing.T) {
	var wg sync.WaitGroup
	wg.Wait()
}
GO
    echo "Modified files: viper.go, viper_test.go"
    echo "Verification: go test ./... passed."
    ;;
  go_bug002_viper_only_then_test)
    if [[ "$count" -eq 1 ]]; then
      cat > viper.go <<'GO'
package benchmark

func WatchConfig() {}
GO
      echo "Modified files: viper.go"
      echo "Verification: go test ./... passed."
    else
      cat > viper_test.go <<'GO'
package benchmark

import "testing"

func TestWatchFile(t *testing.T) {
	t.Helper()
}
GO
      echo "Modified files: viper.go, viper_test.go"
      echo "Verification: go test ./... passed."
    fi
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
  local task_id="${4:-GO-BF-002}"
  FAKE_SCENARIO="$scenario" \
  FAKE_COUNT_FILE="$TMP_DIR/fake-count-$scenario" \
  ELNATH_BIN="$TMP_DIR/fake-elnath.sh" \
  ELNATH_TIMEOUT=30 \
  ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
  HOME="$TMP_DIR/host-home" \
  "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" \
    "$output_path" \
    "$task_id" \
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
ns = {"data": data, "path": path}
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

run_wrapper_case regression_not_added_with_diff "$TMP_DIR/regression-not-added-with-diff.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/regression-not-added-with-diff.json" '
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

mkdir -p "$TMP_DIR/retained-tmp"
ELNATH_BENCHMARK_KEEP_TMP=1 \
ELNATH_BENCHMARK_WRAPPER_STDOUT_PATH="$TMP_DIR/complete-with-retained.stdout" \
ELNATH_BENCHMARK_WRAPPER_STDERR_PATH="$TMP_DIR/complete-with-retained.stderr" \
TMPDIR="$TMP_DIR/retained-tmp" \
run_wrapper_case complete_with_diff "$TMP_DIR/complete-with-retained.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/complete-with-retained.json" '
import json
from pathlib import Path
assert data["success"] is True, data
pointer = data.get("debug_evidence")
assert pointer and pointer.get("sidecar_path"), data
assert not Path(pointer["sidecar_path"]).is_absolute(), data
evidence = json.loads((Path(path).parent / pointer["sidecar_path"]).read_text())
assert evidence.get("retained_temp_root"), data
assert "wrapper_stdout_path" not in evidence, evidence
assert "wrapper_stderr_path" not in evidence, evidence
for key in ("run_log_path", "verification_log_path", "diff_path", "worktree_status_path"):
    path = evidence.get(key)
    assert path and Path(path).exists(), (key, data)
assert "README.md" in data["changed_files"], data
'

run_wrapper_case redaction_probe "$TMP_DIR/redaction-probe.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/redaction-probe.json" '
summary = data["trace_summary"]
assert len(summary) <= 500, data
assert "SECRET_TOKEN" not in summary, data
assert "x" * 100 not in summary, data
'

run_wrapper_case many_untracked_files "$TMP_DIR/many-untracked-files.json" "$SOURCE_REPO"
assert_json_case "$TMP_DIR/many-untracked-files.json" '
assert data["success"] is True, data
assert data["verification_passed"] is True, data
assert len(data["changed_files"]) <= 100, data
assert "generated/file-001.txt" in data["changed_files"], data
assert "generated/file-120.txt" not in data["changed_files"], data
'

run_wrapper_case go_bug002_viper_only "$TMP_DIR/go-bug002-viper-only.json" "$SOURCE_REPO" "GO-BUG-002"
assert_json_case "$TMP_DIR/go-bug002-viper-only.json" '
assert data["success"] is False, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "incomplete_patch", data
assert "viper.go" in data["changed_files"], data
'

run_wrapper_case go_bf001_trivial_request_id_key "$TMP_DIR/go-bf001-trivial-request-id-key.json" "$SOURCE_REPO" "GO-BF-001"
assert_json_case "$TMP_DIR/go-bf001-trivial-request-id-key.json" '
assert data["success"] is False, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "incomplete_patch", data
assert "logger.go" in data["changed_files"], data
assert "GO-BF-001" in data["notes"], data
'

run_wrapper_case go_bf001_unwired_logger_formatter "$TMP_DIR/go-bf001-unwired-logger-formatter.json" "$SOURCE_REPO" "GO-BF-001"
assert_json_case "$TMP_DIR/go-bf001-unwired-logger-formatter.json" '
assert data["success"] is False, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "incomplete_patch", data
assert "logger_test.go" in data["changed_files"], data
assert "logger output buffer" in data["notes"], data
'

run_wrapper_case v8_mix_bf001_no_fixture_test "$TMP_DIR/v8-mix-bf001-no-fixture-test.json" "$SOURCE_REPO" "V8-MIX-BF-001"
assert_json_case "$TMP_DIR/v8-mix-bf001-no-fixture-test.json" '
assert data["success"] is False, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "incomplete_patch", data
assert "api/internal/accumulator/namereferencetransformer.go" in data["changed_files"], data
assert "go.work.sum" in data["changed_files"], data
assert "V8-MIX-BF-001" in data["notes"], data
assert "test/fixture" in data["notes"], data
'

run_wrapper_case go_bug002_unbounded_wait_test "$TMP_DIR/go-bug002-unbounded-wait-test.json" "$SOURCE_REPO" "GO-BUG-002"
assert_json_case "$TMP_DIR/go-bug002-unbounded-wait-test.json" '
assert data["success"] is False, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "incomplete_patch", data
assert "viper.go" in data["changed_files"], data
assert "viper_test.go" in data["changed_files"], data
'

run_wrapper_case go_bug002_viper_only_then_test "$TMP_DIR/go-bug002-viper-only-then-test.json" "$SOURCE_REPO" "GO-BUG-002"
assert_json_case "$TMP_DIR/go-bug002-viper-only-then-test.json" '
assert data["success"] is True, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "", data
assert data["recovery_attempted"] is True, data
assert "viper.go" in data["changed_files"], data
assert "viper_test.go" in data["changed_files"], data
'

echo "PASS: current benchmark wrapper completion guards classify no-change/incomplete runs"
