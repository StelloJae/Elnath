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

python3 - "$CURRENT_WRAPPER" <<'PY'
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text()
if 'TASK_VERIFICATION_COMMAND="${10:-${ELNATH_BENCHMARK_TASK_VERIFICATION_COMMAND:-}}"' not in text:
    raise SystemExit("current wrapper should accept optional verification-command argument or env")
if '[[ "$TASK_LANGUAGE" == "typescript" || "$TASK_LANGUAGE" == "javascript" ]]' not in text:
    raise SystemExit("current wrapper should install JavaScript dependencies for javascript as well as typescript tasks")
PY

echo "PASS: v8 verification-command wrapper contract"
