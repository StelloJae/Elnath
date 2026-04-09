#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 9 ]]; then
  cat <<'EOF'
Usage:
  scripts/run_baseline_benchmark_wrapper.sh \
    <task-output.json> <task-id> <task-track> <task-language> \
    <task-prompt> <task-repo> <task-repo-ref> <task-repo-class> <task-benchmark-family>

Environment:
  BASELINE_TASK_CMD_TEMPLATE   Shell command template used to execute the external baseline
  BASELINE_TIMEOUT             Optional timeout seconds for each baseline run (default: 180)

Available placeholders inside BASELINE_TASK_CMD_TEMPLATE:
  {{task_id}}
  {{task_track}}
  {{task_language}}
  {{task_prompt}}
  {{task_repo}}
  {{task_repo_ref}}
  {{task_repo_class}}
  {{task_benchmark_family}}

Example:
  export BASELINE_TASK_CMD_TEMPLATE='omx run "{{task_prompt}}"'
  export BASELINE_RESULT_SUCCESS_REGEX='PASS'

This wrapper:
  1. shallow-clones the target repo
  2. runs the external baseline command template inside the repo
  3. optionally applies a repo-native verification command if one can be detected
  4. writes one RunResult JSON object to the output path
EOF
  exit 1
fi

TASK_OUTPUT="$1"
TASK_ID="$2"
TASK_TRACK="$3"
TASK_LANGUAGE="$4"
TASK_PROMPT="$5"
TASK_REPO="$6"
TASK_REPO_REF="$7"
TASK_REPO_CLASS="$8"
TASK_BENCHMARK_FAMILY="$9"

if [[ -z "${BASELINE_TASK_CMD_TEMPLATE:-}" ]]; then
  cat <<'EOF' >&2
BASELINE_TASK_CMD_TEMPLATE is required.
Example:
  export BASELINE_TASK_CMD_TEMPLATE='codex run "{{task_prompt}}"'
EOF
  exit 1
fi

START_TS="$(date +%s)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-baseline-benchmark.XXXXXX")"
BASELINE_TIMEOUT="${BASELINE_TIMEOUT:-180}"
cleanup() {
  if [[ "${ELNATH_BENCHMARK_KEEP_TMP:-}" == "1" ]]; then
    echo "Keeping benchmark temp dir: $TMP_DIR" >&2
    return
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

WORKTREE="$TMP_DIR/repo"
RUN_LOG="$TMP_DIR/baseline-run.log"
VERIFY_LOG="$TMP_DIR/verify.log"

json_escape() {
  python3 - <<'PY' "$1"
import json, sys
print(json.dumps(sys.argv[1]))
PY
}

write_result() {
  local success="$1"
  local verification_passed="$2"
  local failure_family="$3"
  local notes="$4"
  local duration
  duration=$(( $(date +%s) - START_TS ))
  cat > "$TASK_OUTPUT" <<EOF
{
  "task_id": $(json_escape "$TASK_ID"),
  "track": $(json_escape "$TASK_TRACK"),
  "language": $(json_escape "$TASK_LANGUAGE"),
  "success": $success,
  "intervention_count": 0,
  "intervention_needed": false,
  "verification_command": $(json_escape "${VERIFICATION_CMD:-}"),
  "verification_passed": $verification_passed,
  "failure_family": $(json_escape "$failure_family"),
  "recovery_attempted": false,
  "recovery_succeeded": false,
  "duration_seconds": $duration,
  "notes": $(json_escape "$notes")
}
EOF
}

if ! git clone --depth 1 "$TASK_REPO" "$WORKTREE" >/dev/null 2>&1; then
  write_result false false "clone_failed" "failed to clone repo"
  exit 0
fi
if [[ -n "$TASK_REPO_REF" ]]; then
  if ! git -C "$WORKTREE" fetch --depth 1 origin "$TASK_REPO_REF" >/dev/null 2>&1; then
    write_result false false "checkout_failed" "failed to fetch pinned repo ref"
    exit 0
  fi
  if ! git -C "$WORKTREE" checkout --detach FETCH_HEAD >/dev/null 2>&1; then
    write_result false false "checkout_failed" "failed to checkout pinned repo ref"
    exit 0
  fi
fi

install_js_deps() {
  if [[ ! -f package.json ]]; then
    return 0
  fi
  if [[ -f pnpm-lock.yaml ]]; then
    if command -v pnpm >/dev/null 2>&1; then
      pnpm install --frozen-lockfile --ignore-scripts >/dev/null
      return 0
    fi
    if command -v npx >/dev/null 2>&1; then
      npx pnpm install --frozen-lockfile --ignore-scripts >/dev/null
      return 0
    fi
  fi
  if python3 - <<'PY'
import json, sys
from pathlib import Path
pkg = json.loads(Path("package.json").read_text())
sys.exit(0 if str(pkg.get("packageManager", "")).startswith("pnpm@") else 1)
PY
  then
    if command -v npx >/dev/null 2>&1; then
      npx pnpm install --frozen-lockfile --ignore-scripts >/dev/null
      return 0
    fi
  fi
  if [[ -f yarn.lock ]] && command -v yarn >/dev/null 2>&1; then
    yarn install --frozen-lockfile --ignore-scripts >/dev/null 2>&1 || yarn install --ignore-scripts >/dev/null
    return 0
  fi
  if command -v npm >/dev/null 2>&1; then
    if [[ -f package-lock.json ]]; then
      npm ci --ignore-scripts >/dev/null
    else
      npm install --ignore-scripts >/dev/null
    fi
    return 0
  fi
  return 1
}

pick_verification_command() {
  if [[ -f go.mod ]] && command -v go >/dev/null 2>&1; then
    echo "go test ./..."
    return 0
  fi
  if [[ -f package.json ]]; then
    if ! python3 - <<'PY'
import json, sys
from pathlib import Path
pkg = json.loads(Path("package.json").read_text())
sys.exit(0 if pkg.get("scripts", {}).get("test") else 1)
PY
    then
      return 1
    fi
    if [[ -f pnpm-lock.yaml ]] && command -v pnpm >/dev/null 2>&1; then
      echo "pnpm test"
      return 0
    fi
    if [[ -f pnpm-lock.yaml ]] && command -v npx >/dev/null 2>&1; then
      echo "npx pnpm test"
      return 0
    fi
    if python3 - <<'PY'
import json, sys
from pathlib import Path
pkg = json.loads(Path("package.json").read_text())
sys.exit(0 if str(pkg.get("packageManager", "")).startswith("pnpm@") else 1)
PY
    then
      if command -v npx >/dev/null 2>&1; then
        echo "npx pnpm test"
        return 0
      fi
    fi
    if [[ -f yarn.lock ]] && command -v yarn >/dev/null 2>&1; then
      echo "yarn test"
      return 0
    fi
    if command -v npm >/dev/null 2>&1; then
      echo "npm test -- --runInBand"
      return 0
    fi
  fi
  return 1
}

working_tree_changes() {
  cd "$WORKTREE"
  git status --porcelain | awk '
    {
      path = substr($0, 4)
      if (path ~ /^\.omx\// || path ~ /^\.codex\//) next
      print path
    }
  '
}

benchmark_specific_verification_command() {
  if [[ "$TASK_REPO" == *"vitest-dev/vitest"* && "$TASK_PROMPT" == *"retry telemetry"* ]]; then
    echo "npx pnpm build && npx pnpm -C test/cli exec vitest --run test/worker-retry-telemetry.test.ts"
    return 0
  fi
  if [[ "$TASK_REPO" == *"nestjs/nest"* && "$TASK_PROMPT" == *"cancellation tracing"* ]]; then
    echo "./node_modules/.bin/mocha packages/common/test/module-utils/configurable-module.builder.spec.ts --require ts-node/register --require tsconfig-paths/register --require node_modules/reflect-metadata/Reflect.js --require hooks/mocha-init-hook.ts"
    return 0
  fi
  if [[ "$TASK_REPO" == *"vercel/next.js"* && "$TASK_PROMPT" == *"file-watcher regression"* ]]; then
    echo "pnpm testonly packages/next/src/lib/find-config.test.ts"
    return 0
  fi
  return 1
}

pick_targeted_verification_command() {
  local changed
  changed="$( { cd "$WORKTREE" && git diff --name-only; working_tree_changes; } | awk 'NF' | sort -u )"
  if [[ -z "$changed" ]]; then
    return 1
  fi

  if [[ -f "$WORKTREE/go.mod" ]] && command -v go >/dev/null 2>&1; then
    echo "go test ./..."
    return 0
  fi

  while IFS= read -r path; do
    [[ -n "$path" ]] || continue
    if [[ "$path" =~ ^test/([^/]+)/test/([^/]+\.(test|spec)\.[cm]?[jt]sx?)$ ]]; then
      local pkg_dir="test/${BASH_REMATCH[1]}"
      local test_file="${BASH_REMATCH[2]}"
      local package_local_test="test/${test_file}"
      if [[ -f "$WORKTREE/$pkg_dir/package.json" ]]; then
        if [[ -f "$WORKTREE/pnpm-lock.yaml" ]] && command -v npx >/dev/null 2>&1; then
          if [[ -f "$WORKTREE/packages/vitest/package.json" ]]; then
            echo "npx pnpm -C packages/vitest build && npx pnpm -C $pkg_dir test -- --run $package_local_test"
          else
            echo "npx pnpm -C $pkg_dir test -- --run $package_local_test"
          fi
          return 0
        fi
        if [[ -f "$WORKTREE/yarn.lock" ]] && command -v yarn >/dev/null 2>&1; then
          echo "cd $pkg_dir && yarn test $package_local_test"
          return 0
        fi
        if command -v npm >/dev/null 2>&1; then
          echo "cd $pkg_dir && npm test -- --run $package_local_test"
          return 0
        fi
      fi
    fi
  done <<<"$changed"

  if [[ "$TASK_REPO" == *"vitest-dev/vitest"* && "$TASK_PROMPT" == *"retry telemetry"* && -f "$WORKTREE/packages/vitest/package.json" ]]; then
    echo "npx pnpm -C packages/vitest build && npx pnpm -C test/cli exec vitest --run test/worker-retry-telemetry.test.ts"
    return 0
  fi

  return 1
}

maybe_prepare_verification() {
  if [[ "${VERIFICATION_CMD:-}" == *"packages/vitest build"* ]]; then
    return 0
  fi
  if [[ -f pnpm-lock.yaml ]] && command -v npx >/dev/null 2>&1; then
    npx pnpm build >/dev/null 2>&1 || true
  fi
}

run_verification_command() {
  (
    cd "$WORKTREE"
    maybe_prepare_verification
    bash -lc "$VERIFICATION_CMD" >"$VERIFY_LOG" 2>&1
  )
}

shell_quote() {
  python3 - <<'PY' "$1"
import shlex, sys
print(shlex.quote(sys.argv[1]))
PY
}

render_template() {
  python3 - <<'PY' "$BASELINE_TASK_CMD_TEMPLATE" "$TASK_ID" "$TASK_TRACK" "$TASK_LANGUAGE" "$TASK_PROMPT" "$TASK_REPO" "$TASK_REPO_REF" "$TASK_REPO_CLASS" "$TASK_BENCHMARK_FAMILY"
import shlex, sys
template, task_id, task_track, task_language, task_prompt, task_repo, task_repo_ref, task_repo_class, task_benchmark_family = sys.argv[1:]
values = {
    "task_id": task_id,
    "task_track": task_track,
    "task_language": task_language,
    "task_prompt": task_prompt,
    "task_repo": task_repo,
    "task_repo_ref": task_repo_ref,
    "task_repo_class": task_repo_class,
    "task_benchmark_family": task_benchmark_family,
}
for key, value in values.items():
    template = template.replace("{{" + key + "}}", shlex.quote(value))
print(template)
PY
}

COMMAND="$(render_template)"

if [[ "$TASK_LANGUAGE" == "typescript" ]]; then
  if ! (cd "$WORKTREE" && install_js_deps); then
    write_result false false "dependency_install_failed" "failed to install JavaScript dependencies"
    exit 0
  fi
fi

BASELINE_EXIT=0
if python3 - <<'PY' "$WORKTREE" "$RUN_LOG" "$COMMAND" "$BASELINE_TIMEOUT"
import os, signal, subprocess, sys
worktree, log_path, command, timeout = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
with open(log_path, "wb") as f:
    proc = subprocess.Popen(
        ["bash", "-lc", command],
        cwd=worktree,
        stdout=f,
        stderr=subprocess.STDOUT,
        start_new_session=True,
    )
    try:
        sys.exit(proc.wait(timeout=timeout))
    except subprocess.TimeoutExpired:
        try:
            os.killpg(proc.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            proc.wait()
        sys.exit(124)
PY
then
  :
else
  BASELINE_EXIT=$?
fi

HAS_CHANGES=false
if [[ -n "$(working_tree_changes)" ]]; then
  HAS_CHANGES=true
fi

if [[ "$BASELINE_EXIT" -ne 0 && "$HAS_CHANGES" == "false" ]]; then
  if [[ "$BASELINE_EXIT" -eq 124 ]]; then
    write_result false false "execution_timeout" "baseline command timed out before producing a working-tree diff"
    exit 0
  fi
  write_result false false "execution_failed" "baseline command failed; see baseline wrapper log"
  exit 0
fi

if [[ "$HAS_CHANGES" == "false" ]]; then
  write_result false false "no_changes" "baseline command completed without creating a working-tree diff"
  exit 0
fi

VERIFICATION_CMD=""
if VERIFY_CMD_CANDIDATE="$(cd "$WORKTREE" && pick_verification_command 2>/dev/null)"; then
  VERIFICATION_CMD="$VERIFY_CMD_CANDIDATE"
fi
if VERIFY_CMD_OVERRIDE="$(benchmark_specific_verification_command 2>/dev/null)"; then
  VERIFICATION_CMD="$VERIFY_CMD_OVERRIDE"
fi
if TARGET_VERIFY_CMD="$(pick_targeted_verification_command 2>/dev/null)"; then
  VERIFICATION_CMD="$TARGET_VERIFY_CMD"
fi
export VERIFICATION_CMD

if [[ -z "$VERIFICATION_CMD" ]]; then
  if [[ "$BASELINE_EXIT" -eq 124 ]]; then
    write_result false false "verification_unavailable" "baseline command timed out, produced a diff, but no repo-native verification command was detected"
  else
    write_result true false "verification_unavailable" "baseline command succeeded but no repo-native verification command was detected"
  fi
  exit 0
fi

if run_verification_command; then
  if [[ "$BASELINE_EXIT" -eq 124 ]]; then
    write_result true true "" "baseline command timed out, but the produced diff passes repo-native verification"
  else
    write_result true true "" "baseline command and repo-native verification both succeeded"
  fi
  exit 0
fi

write_result false false "verification_failed" "baseline command ran but repo-native verification failed"
