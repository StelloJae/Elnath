#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CURRENT_WRAPPER="$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-v8-verification-command.XXXXXX")"

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

run_pick_verification_command_with_corpus_command() {
  local wrapper_path="$1"
  local runner="$TMP_DIR/runner.sh"
  local function_src
  local python_usable_src
  local normalize_src
  python_usable_src="$(extract_function "$wrapper_path" "python_command_usable")"
  normalize_src="$(extract_function "$wrapper_path" "normalize_task_verification_command")"
  function_src="$(extract_function "$wrapper_path" "pick_verification_command")"

  {
    echo '#!/usr/bin/env bash'
    echo 'set -euo pipefail'
    printf 'TASK_VERIFICATION_COMMAND=%q\n' "cd kyaml && go test ./..."
    printf '%s\n' "$python_usable_src"
    printf '%s\n' "$normalize_src"
    printf '%s\n' "$function_src"
    echo 'pick_verification_command'
  } >"$runner"

  bash "$runner"
}

run_pick_python_command_with_unusable_python() {
  local wrapper_path="$1"
  local runner="$TMP_DIR/python-runner.sh"
  local bin_dir="$TMP_DIR/bin"
  local normalize_src
  local python_usable_src
  local pick_src
  python_usable_src="$(extract_function "$wrapper_path" "python_command_usable")"
  normalize_src="$(extract_function "$wrapper_path" "normalize_task_verification_command")"
  pick_src="$(extract_function "$wrapper_path" "pick_verification_command")"

  mkdir -p "$bin_dir"
  cat >"$bin_dir/python" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
  cat >"$bin_dir/python3" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "$bin_dir/python" "$bin_dir/python3"

  {
    echo '#!/usr/bin/env bash'
    echo 'set -euo pipefail'
    printf 'PATH=%q\n' "$bin_dir"
    printf 'TASK_VERIFICATION_COMMAND=%q\n' "python -m pytest tests/test_requests.py -q"
    printf '%s\n' "$python_usable_src"
    printf '%s\n' "$normalize_src"
    printf '%s\n' "$pick_src"
    echo 'pick_verification_command'
  } >"$runner"

  bash "$runner"
}

run_python_pytest_detection_with_non_pytest_python_command() {
  local wrapper_path="$1"
  local runner="$TMP_DIR/python-detection-runner.sh"
  local function_src
  function_src="$(extract_function "$wrapper_path" "python_pytest_verification_task")"

  {
    echo '#!/usr/bin/env bash'
    echo 'set -euo pipefail'
    printf 'TASK_LANGUAGE=%q\n' "python"
    printf 'VERIFY_CMD=%q\n' "python scripts/check_metadata.py"
    printf 'TASK_VERIFICATION_COMMAND=%q\n' "python scripts/check_metadata.py"
    printf '%s\n' "$function_src"
    echo 'if python_pytest_verification_task "$VERIFY_CMD"; then echo yes; else echo no; fi'
  } >"$runner"

  bash "$runner"
}

run_setup_status_filters_wrapper_generated_files() {
  local wrapper_path="$1"
  local runner="$TMP_DIR/setup-status-runner.sh"
  local repo="$TMP_DIR/setup-status-repo"
  local benchmark_changed_src
  local record_src
  benchmark_changed_src="$(extract_function "$wrapper_path" "benchmark_changed_files_all")"
  record_src="$(extract_function "$wrapper_path" "record_wrapper_setup_status")"

  mkdir -p "$repo"
  git -C "$repo" init -q

  {
    echo '#!/usr/bin/env bash'
    echo 'set -euo pipefail'
    printf 'WORKTREE=%q\n' "$repo"
    printf 'WRAPPER_SETUP_STATUS_PATH=%q\n' "$TMP_DIR/wrapper-setup-status.txt"
    printf '%s\n' "$benchmark_changed_src"
    printf '%s\n' "$record_src"
    cat <<'SH'
mkdir -p "$WORKTREE/editable_check_pkg.egg-info"
printf metadata >"$WORKTREE/editable_check_pkg.egg-info/PKG-INFO"
record_wrapper_setup_status
printf model >"$WORKTREE/model_change.py"
benchmark_changed_files_all
SH
  } >"$runner"

  bash "$runner"
}

actual="$(run_pick_verification_command_with_corpus_command "$CURRENT_WRAPPER" || true)"
expected="cd kyaml && go test ./..."
if [[ "$actual" != "$expected" ]]; then
  echo "FAIL: current wrapper should prefer corpus-provided verification command" >&2
  echo "expected: $expected" >&2
  echo "actual:   $actual" >&2
  exit 1
fi

actual_python="$(run_pick_python_command_with_unusable_python "$CURRENT_WRAPPER" || true)"
expected_python="python3 -m pytest tests/test_requests.py -q"
if [[ "$actual_python" != "$expected_python" ]]; then
  echo "FAIL: current wrapper should rewrite unusable bare python verification command to python3" >&2
  echo "expected: $expected_python" >&2
  echo "actual:   $actual_python" >&2
  exit 1
fi

actual_non_pytest_python="$(run_python_pytest_detection_with_non_pytest_python_command "$CURRENT_WRAPPER" || true)"
if [[ "$actual_non_pytest_python" != "no" ]]; then
  echo "FAIL: Python verification prep should be limited to pytest verification commands" >&2
  echo "expected: no" >&2
  echo "actual:   $actual_non_pytest_python" >&2
  exit 1
fi

actual_setup_filter="$(run_setup_status_filters_wrapper_generated_files "$CURRENT_WRAPPER" || true)"
expected_setup_filter="model_change.py"
if [[ "$actual_setup_filter" != "$expected_setup_filter" ]]; then
  echo "FAIL: wrapper setup-generated files should not count as model changes" >&2
  echo "expected: $expected_setup_filter" >&2
  echo "actual:   $actual_setup_filter" >&2
  exit 1
fi

python3 - "$CURRENT_WRAPPER" <<'PY'
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text()
if 'TASK_VERIFICATION_COMMAND="${10:-${ELNATH_BENCHMARK_TASK_VERIFICATION_COMMAND:-}}"' not in text:
    raise SystemExit("current wrapper should accept optional verification-command argument or env")
if '[[ "$TASK_LANGUAGE" == "typescript" || "$TASK_LANGUAGE" == "javascript" ]]' not in text:
    raise SystemExit("current wrapper should install JavaScript dependencies for javascript as well as typescript tasks")
if 'prepare_python_verification_env() {' not in text:
    raise SystemExit("current wrapper should prepare a Python verification virtualenv for Python pytest tasks")
if '-m venv "$BENCHMARK_PYTHON_VENV"' not in text:
    raise SystemExit("Python verification prep should create a virtualenv")
if '-m pip install -e . pytest' not in text:
    raise SystemExit("Python verification prep should install the repo editable package and pytest")
if 'requirements-dev.txt' not in text:
    raise SystemExit("Python verification prep should prefer repo dev/test requirements when present")
if 'PATH="$BENCHMARK_PYTHON_VENV/bin:$PATH"' not in text:
    raise SystemExit("Python verification prep should prepend the virtualenv bin dir for exact verification commands")
if 'BENCHMARK_PIP_CACHE_DIR=' not in text:
    raise SystemExit("Python verification prep should use a benchmark-scoped pip cache")
if 'export HOME="$BENCHMARK_HOME_DIR"' not in text.split('prepare_python_verification_env "$VERIFY_CMD"', 1)[0].rsplit('if ! (', 1)[-1]:
    raise SystemExit("Python verification prep should run with benchmark HOME")
if 'export PIP_CACHE_DIR="$BENCHMARK_PIP_CACHE_DIR"' not in text:
    raise SystemExit("Python verification prep should export benchmark-scoped PIP_CACHE_DIR")
if 'VIRTUAL_ENV/bin/activate' not in text:
    raise SystemExit("verification should activate the Python virtualenv inside the verification shell")
if 'WRAPPER_SETUP_STATUS_PATH=' not in text:
    raise SystemExit("current wrapper should record wrapper setup-generated files")
if 'record_wrapper_setup_status' not in text:
    raise SystemExit("current wrapper should snapshot wrapper setup state before Elnath edits")
if 'grep -vxF -f "$WRAPPER_SETUP_STATUS_PATH"' not in text:
    raise SystemExit("working-tree change detection should ignore wrapper setup-generated files")
pick_idx = text.index('if VERIFY_CMD_CANDIDATE=')
prep_idx = text.index('prepare_python_verification_env "$VERIFY_CMD"')
if not pick_idx < prep_idx:
    raise SystemExit("Python verification prep should run after the exact verification command is selected")
PY

echo "PASS: v8 verification-command wrapper contract"
