#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-targeted-verify-test.XXXXXX")"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

extract_function() {
  python3 - "$1" "$2" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
name = sys.argv[2]
lines = path.read_text().splitlines()
start = None
for idx, line in enumerate(lines):
    if line.startswith(f"{name}() {{"):
        start = idx
        break
if start is None:
    raise SystemExit(f"function {name} not found in {path}")

depth = 0
selected = []
for line in lines[start:]:
    selected.append(line)
    depth += line.count("{")
    depth -= line.count("}")
    if depth == 0:
        break

print("\n".join(selected))
PY
}

extract_optional_function() {
  python3 - "$1" "$2" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
name = sys.argv[2]
lines = path.read_text().splitlines()
start = None
for idx, line in enumerate(lines):
    if line.startswith(f"{name}() {{"):
        start = idx
        break
if start is None:
    sys.exit(0)

depth = 0
selected = []
for line in lines[start:]:
    selected.append(line)
    depth += line.count("{")
    depth -= line.count("}")
    if depth == 0:
        break

print("\n".join(selected))
PY
}

extract_task_matchers() {
  local wrapper_path="$1"
  for fn in \
    is_ts_bf001_vitest_task \
    is_ts_bf002_nestjs_task \
    is_v8_ts_bug003_axios_task \
    is_v8_ts_bug004_undici_task
  do
    extract_optional_function "$wrapper_path" "$fn"
  done
}

create_vitest_fixture() {
  local repo_dir="$1"
  mkdir -p "$repo_dir/packages/vitest" "$repo_dir/test/cli/test"
  cat >"$repo_dir/pnpm-lock.yaml" <<'EOF'
lockfileVersion: '9.0'
EOF
  cat >"$repo_dir/packages/vitest/package.json" <<'EOF'
{"name":"@fixture/vitest","private":true}
EOF
  cat >"$repo_dir/test/cli/package.json" <<'EOF'
{"name":"@fixture/test-cli","private":true}
EOF
  cat >"$repo_dir/test/cli/test/worker-retry-telemetry.test.ts" <<'EOF'
export {}
EOF
  git -C "$repo_dir" init -q
  git -C "$repo_dir" add .
  git -C "$repo_dir" -c user.name='Test User' -c user.email='test@example.com' commit -qm "init"
  printf '\n// changed\n' >>"$repo_dir/test/cli/test/worker-retry-telemetry.test.ts"
}

create_go_fixture() {
  local repo_dir="$1"
  mkdir -p "$repo_dir"
  cat >"$repo_dir/go.mod" <<'EOF'
module example.com/generic

go 1.22
EOF
  cat >"$repo_dir/main.go" <<'EOF'
package generic

func Answer() int { return 42 }
EOF
  git -C "$repo_dir" init -q
  git -C "$repo_dir" add .
  git -C "$repo_dir" -c user.name='Test User' -c user.email='test@example.com' commit -qm "init"
  printf '\n// changed\n' >>"$repo_dir/main.go"
}

run_pick_targeted_command() {
  local wrapper_path="$1"
  local repo_dir="$2"
  local runner="$TMP_DIR/runner.sh"
  local function_src
  function_src="$(extract_function "$wrapper_path" "pick_targeted_verification_command")"

  {
    echo '#!/usr/bin/env bash'
    echo 'set -euo pipefail'
    printf 'WORKTREE=%q\n' "$repo_dir"
    printf 'TASK_REPO=%q\n' "https://github.com/vitest-dev/vitest"
    printf 'TASK_PROMPT=%q\n' "Extend an existing TypeScript worker flow to emit retry telemetry without regressing current behavior."
    echo 'working_tree_changes() { git diff --name-only; }'
    printf '%s\n' "$function_src"
    echo 'pick_targeted_verification_command'
  } >"$runner"

  bash "$runner"
}

run_benchmark_specific_command() {
  local wrapper_path="$1"
  local task_repo="$2"
  local task_prompt="$3"
  local runner="$TMP_DIR/specific-runner.sh"
  local function_src
  local matcher_src
  function_src="$(extract_function "$wrapper_path" "benchmark_specific_verification_command")"
  matcher_src="$(extract_task_matchers "$wrapper_path")"

  {
    echo '#!/usr/bin/env bash'
    echo 'set -euo pipefail'
    printf 'TASK_ID=%q\n' ""
    printf 'TASK_REPO=%q\n' "$task_repo"
    printf 'TASK_PROMPT=%q\n' "$task_prompt"
    printf '%s\n' "$matcher_src"
    printf '%s\n' "$function_src"
    echo 'benchmark_specific_verification_command'
  } >"$runner"

  bash "$runner"
}

run_final_command_after_changes() {
  local wrapper_path="$1"
  local repo_dir="$2"
  local task_repo="$3"
  local task_prompt="$4"
  local runner="$TMP_DIR/final-runner.sh"
  local pick_src
  local specific_src
  local final_src
  local matcher_src
  pick_src="$(extract_function "$wrapper_path" "pick_targeted_verification_command")"
  specific_src="$(extract_function "$wrapper_path" "benchmark_specific_verification_command")"
  final_src="$(extract_function "$wrapper_path" "pick_final_verification_command")"
  matcher_src="$(extract_task_matchers "$wrapper_path")"

  {
    echo '#!/usr/bin/env bash'
    echo 'set -euo pipefail'
    printf 'WORKTREE=%q\n' "$repo_dir"
    printf 'TASK_REPO=%q\n' "$task_repo"
    printf 'TASK_PROMPT=%q\n' "$task_prompt"
    echo 'working_tree_changes() { git diff --name-only; }'
    printf '%s\n' "$matcher_src"
    printf '%s\n' "$specific_src"
    printf '%s\n' "$pick_src"
    printf '%s\n' "$final_src"
    echo 'pick_final_verification_command "go test ./..."'
  } >"$runner"

  bash "$runner"
}

assert_vitest_targeted_command() {
  local wrapper_rel="$1"
  local actual="$2"
  local expected="npx pnpm -C packages/vitest build && npx pnpm -C test/cli exec vitest --run test/worker-retry-telemetry.test.ts"
  if [[ "$actual" != "$expected" ]]; then
    echo "FAIL: $wrapper_rel produced unexpected targeted verification command" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    exit 1
  fi
}

assert_generic_go_command() {
  local wrapper_rel="$1"
  local actual="$2"
  local expected="go test ./..."
  if [[ "$actual" != "$expected" ]]; then
    echo "FAIL: $wrapper_rel changed generic Go verification command" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    exit 1
  fi
}

assert_caddy_serialized_command() {
  local wrapper_rel="$1"
  local actual="$2"
  local expected="go test -p 1 ./... -count=1"
  if [[ "$actual" != "$expected" ]]; then
    echo "FAIL: $wrapper_rel produced unexpected Caddy verification command" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    exit 1
  fi
  if [[ "$actual" != *"./..."* ]]; then
    echo "FAIL: $wrapper_rel Caddy verification command must retain ./... full coverage" >&2
    exit 1
  fi
}

assert_axios_focused_command() {
  local wrapper_rel="$1"
  local actual="$2"
  local expected="npm exec -- vitest run --project unit tests/unit/composeSignals.test.js"
  if [[ "$actual" != "$expected" ]]; then
    echo "FAIL: $wrapper_rel produced unexpected axios focused verification command" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    exit 1
  fi
}

assert_undici_focused_command() {
  local wrapper_rel="$1"
  local actual="$2"
  local expected="node --test test/client-request.js"
  if [[ "$actual" != "$expected" ]]; then
    echo "FAIL: $wrapper_rel produced unexpected undici focused verification command" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    exit 1
  fi
}

FIXTURE_REPO="$TMP_DIR/vitest-fixture"
create_vitest_fixture "$FIXTURE_REPO"

CURRENT_CMD="$(run_pick_targeted_command "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" "$FIXTURE_REPO")"
assert_vitest_targeted_command "scripts/run_current_benchmark_wrapper.sh" "$CURRENT_CMD"

BASELINE_CMD="$(run_pick_targeted_command "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh" "$FIXTURE_REPO")"
assert_vitest_targeted_command "scripts/run_baseline_benchmark_wrapper.sh" "$BASELINE_CMD"

GO_FIXTURE_REPO="$TMP_DIR/go-fixture"
create_go_fixture "$GO_FIXTURE_REPO"

CURRENT_GO_CMD="$(run_pick_targeted_command "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" "$GO_FIXTURE_REPO")"
assert_generic_go_command "scripts/run_current_benchmark_wrapper.sh" "$CURRENT_GO_CMD"

BASELINE_GO_CMD="$(run_pick_targeted_command "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh" "$GO_FIXTURE_REPO")"
assert_generic_go_command "scripts/run_baseline_benchmark_wrapper.sh" "$BASELINE_GO_CMD"

CADDY_REPO="https://github.com/caddyserver/caddy"
CADDY_PROMPT="Extend an existing Go worker service so graceful shutdown emits structured progress logging and does not regress existing worker behavior."

CURRENT_CADDY_CMD="$(run_benchmark_specific_command "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" "$CADDY_REPO" "$CADDY_PROMPT" || true)"
assert_caddy_serialized_command "scripts/run_current_benchmark_wrapper.sh" "$CURRENT_CADDY_CMD"

BASELINE_CADDY_CMD="$(run_benchmark_specific_command "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh" "$CADDY_REPO" "$CADDY_PROMPT" || true)"
assert_caddy_serialized_command "scripts/run_baseline_benchmark_wrapper.sh" "$BASELINE_CADDY_CMD"

CURRENT_CADDY_FINAL_CMD="$(run_final_command_after_changes "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" "$GO_FIXTURE_REPO" "$CADDY_REPO" "$CADDY_PROMPT" || true)"
assert_caddy_serialized_command "scripts/run_current_benchmark_wrapper.sh final" "$CURRENT_CADDY_FINAL_CMD"

BASELINE_CADDY_FINAL_CMD="$(run_final_command_after_changes "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh" "$GO_FIXTURE_REPO" "$CADDY_REPO" "$CADDY_PROMPT" || true)"
assert_caddy_serialized_command "scripts/run_baseline_benchmark_wrapper.sh final" "$BASELINE_CADDY_FINAL_CMD"

AXIOS_REPO="https://github.com/axios/axios"
AXIOS_PROMPT="Fix an abort or timeout edge behavior in the request flow and cover it with a targeted regression test."
CURRENT_AXIOS_CMD="$(run_benchmark_specific_command "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" "$AXIOS_REPO" "$AXIOS_PROMPT" || true)"
assert_axios_focused_command "scripts/run_current_benchmark_wrapper.sh axios" "$CURRENT_AXIOS_CMD"
BASELINE_AXIOS_CMD="$(run_benchmark_specific_command "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh" "$AXIOS_REPO" "$AXIOS_PROMPT" || true)"
assert_axios_focused_command "scripts/run_baseline_benchmark_wrapper.sh axios" "$BASELINE_AXIOS_CMD"

UNDICI_REPO="https://github.com/nodejs/undici"
UNDICI_PROMPT="Fix an abort or cancellation edge behavior in client request handling and verify the behavior with a focused test."
CURRENT_UNDICI_CMD="$(run_benchmark_specific_command "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" "$UNDICI_REPO" "$UNDICI_PROMPT" || true)"
assert_undici_focused_command "scripts/run_current_benchmark_wrapper.sh undici" "$CURRENT_UNDICI_CMD"
BASELINE_UNDICI_CMD="$(run_benchmark_specific_command "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh" "$UNDICI_REPO" "$UNDICI_PROMPT" || true)"
assert_undici_focused_command "scripts/run_baseline_benchmark_wrapper.sh undici" "$BASELINE_UNDICI_CMD"

echo "PASS: targeted benchmark verification commands stay scoped"
