#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-vitest-prepare-test.XXXXXX")"

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
  mkdir -p \
    "$repo_dir/packages/vitest" \
    "$repo_dir/packages/utils" \
    "$repo_dir/packages/runner" \
    "$repo_dir/packages/pretty-format" \
    "$repo_dir/packages/snapshot" \
    "$repo_dir/packages/spy" \
    "$repo_dir/packages/expect" \
    "$repo_dir/packages/mocker" \
    "$repo_dir/test/cli/test"
  cat >"$repo_dir/pnpm-lock.yaml" <<'EOF'
lockfileVersion: '9.0'
EOF
  cat >"$repo_dir/packages/vitest/package.json" <<'EOF'
{"name":"@vitest/vitest","private":true}
EOF
  cat >"$repo_dir/packages/utils/package.json" <<'EOF'
{"name":"@vitest/utils","private":true}
EOF
  cat >"$repo_dir/packages/runner/package.json" <<'EOF'
{"name":"@vitest/runner","private":true}
EOF
  cat >"$repo_dir/packages/pretty-format/package.json" <<'EOF'
{"name":"@vitest/pretty-format","private":true}
EOF
  cat >"$repo_dir/packages/snapshot/package.json" <<'EOF'
{"name":"@vitest/snapshot","private":true}
EOF
  cat >"$repo_dir/packages/spy/package.json" <<'EOF'
{"name":"@vitest/spy","private":true}
EOF
  cat >"$repo_dir/packages/expect/package.json" <<'EOF'
{"name":"@vitest/expect","private":true}
EOF
  cat >"$repo_dir/packages/mocker/package.json" <<'EOF'
{"name":"@vitest/mocker","private":true}
EOF
  cat >"$repo_dir/test/cli/package.json" <<'EOF'
{"name":"@vitest/test-cli","private":true}
EOF
  cat >"$repo_dir/test/cli/test/worker-retry-telemetry.test.ts" <<'EOF'
export {}
EOF
}

create_fake_npx() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"
  cat >"$bin_dir/npx" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_NPX_LOG:?}"
EOF
  chmod +x "$bin_dir/npx"
}

run_prepare() {
  local wrapper_path="$1"
  local repo_dir="$2"
  local npx_log="$3"
  local runner="$TMP_DIR/prepare-runner.sh"
  local function_src
  function_src="$(extract_function "$wrapper_path" "maybe_prepare_verification")"

  {
    echo '#!/usr/bin/env bash'
    echo 'set -euo pipefail'
    printf 'WORKTREE=%q\n' "$repo_dir"
    printf 'VERIFY_CMD=%q\n' "npx pnpm -C packages/vitest build && npx pnpm -C test/cli exec vitest --run test/worker-retry-telemetry.test.ts"
    printf 'VERIFICATION_CMD=%q\n' "npx pnpm -C packages/vitest build && npx pnpm -C test/cli exec vitest --run test/worker-retry-telemetry.test.ts"
    printf 'FAKE_NPX_LOG=%q\n' "$npx_log"
    printf 'PATH=%q\n' "$TMP_DIR/bin:$PATH"
    echo 'export FAKE_NPX_LOG PATH'
    printf '%s\n' "$function_src"
    echo 'cd "$WORKTREE"'
    echo 'maybe_prepare_verification'
  } >"$runner"

  bash "$runner"
}

FIXTURE_REPO="$TMP_DIR/vitest-fixture"
CURRENT_NPX_LOG="$TMP_DIR/current-npx.log"
BASELINE_NPX_LOG="$TMP_DIR/baseline-npx.log"
create_vitest_fixture "$FIXTURE_REPO"
create_fake_npx "$TMP_DIR/bin"

assert_vitest_prep_log() {
  local log_path="$1"
  grep -Fxq 'pnpm -C packages/pretty-format build' "$log_path"
  grep -Fxq 'pnpm -C packages/utils build' "$log_path"
  grep -Fxq 'pnpm -C packages/spy build' "$log_path"
  grep -Fxq 'pnpm -C packages/expect build' "$log_path"
  grep -Fxq 'pnpm -C packages/runner build' "$log_path"
  grep -Fxq 'pnpm -C packages/snapshot build' "$log_path"
  grep -Fxq 'pnpm -C packages/mocker build' "$log_path"
}

run_prepare "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" "$FIXTURE_REPO" "$CURRENT_NPX_LOG"
assert_vitest_prep_log "$CURRENT_NPX_LOG"

run_prepare "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh" "$FIXTURE_REPO" "$BASELINE_NPX_LOG"
assert_vitest_prep_log "$BASELINE_NPX_LOG"

echo "PASS: vitest retry telemetry verification prepares workspace package dist"
