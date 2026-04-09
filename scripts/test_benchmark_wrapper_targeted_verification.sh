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

FIXTURE_REPO="$TMP_DIR/vitest-fixture"
create_vitest_fixture "$FIXTURE_REPO"

CURRENT_CMD="$(run_pick_targeted_command "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" "$FIXTURE_REPO")"
assert_vitest_targeted_command "scripts/run_current_benchmark_wrapper.sh" "$CURRENT_CMD"

BASELINE_CMD="$(run_pick_targeted_command "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh" "$FIXTURE_REPO")"
assert_vitest_targeted_command "scripts/run_baseline_benchmark_wrapper.sh" "$BASELINE_CMD"

echo "PASS: targeted vitest verification stays on the narrow exec vitest path"
