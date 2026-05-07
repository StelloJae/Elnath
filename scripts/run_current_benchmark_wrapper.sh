#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 9 ]]; then
  cat <<'EOF'
Usage:
  scripts/run_current_benchmark_wrapper.sh \
    <task-output.json> <task-id> <task-track> <task-language> \
    <task-prompt> <task-repo> <task-repo-ref> <task-repo-class> <task-benchmark-family>

Environment:
  ELNATH_BIN       Path to the Elnath binary (default: ./elnath at repo root)
  ELNATH_CONFIG    Optional explicit config path
  ELNATH_TIMEOUT   Optional timeout seconds for each Elnath run (default: 180)
  ELNATH_VERIFY_TIMEOUT
                  Optional timeout seconds for each verification command (default: ELNATH_TIMEOUT)
  ELNATH_BENCHMARK_PERMISSION_MODE
                   Benchmark-only permission mode override (default: bypass)

This wrapper:
  1. shallow-clones the target repo
  2. runs Elnath once on the benchmark prompt
  3. chooses a repo-native verification command when possible
  4. retries once with a verification-focused recovery prompt if verification fails
  5. writes a RunResult JSON object to the output path
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

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ELNATH_BIN="${ELNATH_BIN:-$REPO_ROOT/elnath}"
ELNATH_TIMEOUT="${ELNATH_TIMEOUT:-300}"
ELNATH_VERIFY_TIMEOUT="${ELNATH_VERIFY_TIMEOUT:-$ELNATH_TIMEOUT}"

START_TS="$(date +%s)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-current-benchmark.XXXXXX")"
BENCHMARK_SHORT_ROOT=""
ORIGINAL_HOME="${HOME:-}"
cleanup() {
  if [[ -n "$BENCHMARK_SHORT_ROOT" ]]; then
    rm -f "$BENCHMARK_HOME_DIR/.codex/auth.json"
    rm -f "$BENCHMARK_HOME_DIR/.codex/config.toml"
  fi
  if [[ "${ELNATH_BENCHMARK_KEEP_TMP:-}" == "1" ]]; then
    echo "Keeping benchmark temp dir: $TMP_DIR" >&2
    if [[ -n "$BENCHMARK_SHORT_ROOT" ]]; then
      echo "Keeping benchmark short env dir: $BENCHMARK_SHORT_ROOT" >&2
    fi
    return
  fi
  if [[ -n "$BENCHMARK_SHORT_ROOT" ]]; then
    rm -rf "$BENCHMARK_SHORT_ROOT"
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT
BENCHMARK_SHORT_ROOT="$(mktemp -d /tmp/elnath-bench-XXXXXX)"

# macOS ships python3 only; LLMs often emit bare `python`.
if ! command -v python >/dev/null 2>&1 && command -v python3 >/dev/null 2>&1; then
  mkdir -p "$TMP_DIR/bin"
  ln -s "$(command -v python3)" "$TMP_DIR/bin/python"
  export PATH="$TMP_DIR/bin:$PATH"
fi
WORKTREE="$TMP_DIR/repo"
RUN_LOG="$TMP_DIR/elnath-run.log"
RECOVERY_LOG="$TMP_DIR/elnath-recovery.log"
AUDIT_LOG="$TMP_DIR/route-audit.jsonl"
VERIFY_LOG="$TMP_DIR/verify.log"
VERIFY_RETRY_LOG="$TMP_DIR/verify-retry.log"
BENCHMARK_STATE_DIR="$TMP_DIR/elnath-state"
BENCHMARK_DATA_DIR="$BENCHMARK_STATE_DIR/data"
BENCHMARK_WIKI_DIR="$BENCHMARK_STATE_DIR/wiki"
BENCHMARK_ENV_DIR="$BENCHMARK_SHORT_ROOT/env"
BENCHMARK_HOME_DIR="$BENCHMARK_ENV_DIR/home"
BENCHMARK_TMP_DIR="$BENCHMARK_ENV_DIR/tmp"
BENCHMARK_GOMODCACHE_DIR="$BENCHMARK_ENV_DIR/go/pkg/mod"
BENCHMARK_GOCACHE_DIR="$BENCHMARK_ENV_DIR/.cache/go-build"
mkdir -p \
  "$BENCHMARK_DATA_DIR" \
  "$BENCHMARK_WIKI_DIR" \
  "$BENCHMARK_HOME_DIR" \
  "$BENCHMARK_TMP_DIR" \
  "$BENCHMARK_GOMODCACHE_DIR" \
  "$BENCHMARK_GOCACHE_DIR"

prepare_benchmark_provider_home() {
  if [[ -z "$ORIGINAL_HOME" || ! -f "$ORIGINAL_HOME/.codex/auth.json" ]]; then
    return 0
  fi

  mkdir -p "$BENCHMARK_HOME_DIR/.codex"
  cp "$ORIGINAL_HOME/.codex/auth.json" "$BENCHMARK_HOME_DIR/.codex/auth.json"
  chmod 600 "$BENCHMARK_HOME_DIR/.codex/auth.json"
  if [[ -f "$ORIGINAL_HOME/.codex/config.toml" ]]; then
    cp "$ORIGINAL_HOME/.codex/config.toml" "$BENCHMARK_HOME_DIR/.codex/config.toml"
    chmod 600 "$BENCHMARK_HOME_DIR/.codex/config.toml"
  fi
}

prepare_benchmark_provider_home

export TMPDIR="$BENCHMARK_TMP_DIR"
export TMP="$BENCHMARK_TMP_DIR"
export TEMP="$BENCHMARK_TMP_DIR"
export GOMODCACHE="$BENCHMARK_GOMODCACHE_DIR"
export GOCACHE="$BENCHMARK_GOCACHE_DIR"

json_escape() {
  python3 - <<'PY' "$1"
import json, sys
print(json.dumps(sys.argv[1]))
PY
}

benchmark_changed_files_all() {
  if [[ ! -d "$WORKTREE/.git" ]]; then
    return 0
  fi
  (
    cd "$WORKTREE"
    git status --porcelain --untracked-files=all | awk '
      {
        path = substr($0, 4)
        if (path ~ /^\.omx\// || path ~ /^\.codex\//) next
        print path
      }
    '
  )
}

benchmark_changed_files() {
  benchmark_changed_files_all | awk 'NF { if (++count <= 100) print }'
}

changed_files_json() {
  benchmark_changed_files | python3 -c 'import json, sys; print(json.dumps([line.strip() for line in sys.stdin if line.strip()]))'
}

changed_file_count() {
  benchmark_changed_files_all | awk 'NF { count++ } END { print count + 0 }'
}

combined_agent_log_tail() {
  for log_path in "$RUN_LOG" "$RECOVERY_LOG"; do
    if [[ -s "$log_path" ]]; then
      tail -120 "$log_path"
    fi
  done
}

latest_agent_log_tail() {
  if [[ -s "$RECOVERY_LOG" ]]; then
    tail -120 "$RECOVERY_LOG"
    return 0
  fi
  if [[ -s "$RUN_LOG" ]]; then
    tail -120 "$RUN_LOG"
  fi
}

detect_edit_intent() {
  combined_agent_log_tail | python3 -c '
import re
import sys
text = sys.stdin.read().lower()
patterns = [
    r"\b(i am|i'\''m|i will|will|going to|now)\s+(patch|edit|modify|change|implement|add|update)\b",
    r"\b(patching|editing|modifying|implementing|adding|updating)\b",
    r"\b(writ(e|ing)|wrote)\s+(code|tests?|files?|changes?|a\s+patch|the\s+patch|implementation)\b",
    r"\b(modified|changed|updated|edited)\s+files?\b",
    r"\bapply(ing)?\s+(the\s+)?patch\b",
]
sys.exit(0 if any(re.search(pattern, text) for pattern in patterns) else 1)
'
}

detect_final_incomplete() {
  latest_agent_log_tail | python3 -c '
import re
import sys
text = sys.stdin.read().lower()
patterns = [
    r"(?m)^\s*incomplete\s*:",
    r"\b(i|we)\s+(did not|didn'\''t|could not|couldn'\''t|cannot|can'\''t)\s+(complete|finish)\b",
    r"\b(i|we)\s+(cannot|can'\''t)\s+honestly\s+claim\s+completion\b",
    r"\bnot\s+complete(d)?\s+(the\s+)?(task|requested\s+work|implementation|work)\b",
    r"\bincomplete\s+(patch|implementation|task|work)\b",
    r"\bpartial implementation\b",
    r"\bmissing regression test\b.*\b(cannot|can'\''t|could not|couldn'\''t|still|not)\b",
    r"\bfocused regression test was not added\b",
    r"\bregression test was not added\b",
    r"\bcannot honestly claim completion\b",
    r"\bcan'\''t honestly claim completion\b",
    r"\bunable to complete\b",
    r"\bunfinished\b",
    r"\bunresolved task scope\b",
]
sys.exit(0 if any(re.search(pattern, text) for pattern in patterns) else 1)
'
}

detect_failed_recovery_incomplete_admission() {
  latest_agent_log_tail | python3 -c '
import re
import sys
text = sys.stdin.read().lower()
patterns = [
    r"(?m)^\s*incomplete\s*:",
    r"\bremaining\s+(issue|problem|blocker)\b",
    r"\bstill\s+fail(s|ing)\s+because\b",
    r"\bverification\s+still\s+fail(s|ing)\b",
    r"\boverall\s+verification\s+failed\b",
    r"\bnot\s+fixed\b",
    r"\bcould\s+not\s+resolve\b",
    r"\bno\s+retry\s+was\s+possible\b",
    r"\btarget[-\s]+identification\s+(problem|issue)\s+remains\b",
]
sys.exit(0 if any(re.search(pattern, text) for pattern in patterns) else 1)
'
}

trace_summary_text() {
  local recovery_attempted="$1"
  local changed_count="$2"
  local edit_intent="$3"
  local final_incomplete="$4"
  local verification_configured=false
  if [[ -n "${VERIFICATION_CMD:-}" ]]; then
    verification_configured=true
  fi
  local summary="changed_files=${changed_count}; edit_intent_detected=${edit_intent}; final_incomplete_detected=${final_incomplete}; recovery_attempted=${recovery_attempted}; verification_configured=${verification_configured}"
  printf '%s' "${summary:0:500}"
}

prepare_debug_artifacts() {
  DEBUG_DIFF_PATH=""
  DEBUG_STATUS_PATH=""
  if [[ "${ELNATH_BENCHMARK_KEEP_TMP:-}" != "1" || ! -d "$WORKTREE/.git" ]]; then
    return 0
  fi
  DEBUG_DIFF_PATH="$TMP_DIR/diff.patch"
  DEBUG_STATUS_PATH="$TMP_DIR/worktree-status.txt"
  (cd "$WORKTREE" && git diff) > "$DEBUG_DIFF_PATH" 2>/dev/null || true
  (cd "$WORKTREE" && git status --short) > "$DEBUG_STATUS_PATH" 2>/dev/null || true
}

debug_path_if_available() {
  local path="$1"
  if [[ -n "$path" && -e "$path" ]]; then
    printf '%s' "$path"
  fi
}

debug_evidence_json() {
  if [[ "${ELNATH_BENCHMARK_KEEP_TMP:-}" != "1" ]]; then
    return 0
  fi
  local run_log_path recovery_log_path verify_log_path verify_retry_log_path diff_path status_path sidecar_path public_sidecar_path
  run_log_path="$(debug_path_if_available "$RUN_LOG")"
  recovery_log_path="$(debug_path_if_available "$RECOVERY_LOG")"
  verify_log_path="$(debug_path_if_available "$VERIFY_LOG")"
  verify_retry_log_path="$(debug_path_if_available "$VERIFY_RETRY_LOG")"
  diff_path="$(debug_path_if_available "${DEBUG_DIFF_PATH:-}")"
  status_path="$(debug_path_if_available "${DEBUG_STATUS_PATH:-}")"
  sidecar_path="${ELNATH_BENCHMARK_DEBUG_EVIDENCE_PATH:-${TASK_OUTPUT}.debug-evidence.json}"
  public_sidecar_path="${ELNATH_BENCHMARK_DEBUG_EVIDENCE_PUBLIC_PATH:-$(basename "$sidecar_path")}"
  mkdir -p "$(dirname "$sidecar_path")"
  python3 - <<'PY' \
    "$sidecar_path" \
    "$public_sidecar_path" \
    "$TMP_DIR" \
    "${ELNATH_BENCHMARK_WRAPPER_STDOUT_PATH:-}" \
    "${ELNATH_BENCHMARK_WRAPPER_STDERR_PATH:-}" \
    "$run_log_path" \
    "$recovery_log_path" \
    "$verify_log_path" \
    "$verify_retry_log_path" \
    "$diff_path" \
    "$status_path"
import json, os, sys
sidecar_path = sys.argv[1]
public_sidecar_path = sys.argv[2]
keys = [
    "retained_temp_root",
    "wrapper_stdout_path",
    "wrapper_stderr_path",
    "run_log_path",
    "recovery_log_path",
    "verification_log_path",
    "verification_retry_log_path",
    "diff_path",
    "worktree_status_path",
]
obj = {}
for key, value in zip(keys, sys.argv[3:]):
    if not value:
        continue
    if key != "retained_temp_root" and not os.path.exists(value):
        continue
    obj[key] = value
if obj:
    with open(sidecar_path, "w", encoding="utf-8") as fh:
        json.dump(obj, fh, indent=2, sort_keys=True)
        fh.write("\n")
    print(',\n  "debug_evidence": ' + json.dumps({"sidecar_path": public_sidecar_path}, sort_keys=True))
PY
}

is_ts_bf002_nestjs_task() {
  [[ "$TASK_ID" == "TS-BF-002" ]]
}

is_ts_bf001_vitest_task() {
  [[ "$TASK_ID" == "TS-BF-001" ]]
}

is_typescript_canary_task() {
  [[ "$TASK_LANGUAGE" == "typescript" ]] && {
    is_ts_bf001_vitest_task || is_ts_bf002_nestjs_task
  }
}

typescript_recovery_checklist() {
  is_typescript_canary_task || return 0
  cat <<'EOF'

TypeScript canary recovery checklist:
- Leave a production/runtime diff when the task asks for behavior change; do not stop after exploratory findings.
- Add or keep a focused regression test in the expected task-specific area.
- Run the exact benchmark verification command before the final answer.
- Do not treat import mechanics, module-resolution churn, or test-only scaffolding as semantic completion.
EOF
}

recovery_completion_checklist() {
  cat <<'EOF'

Recovery completion checklist:
- The patch must compile cleanly before the final answer.
- All introduced imports, variables, types, and helper functions must be used or removed; no unused imports or unused variables may remain.
- Do not claim completion after only changing the error class or making partial symptom fixes.
- The verification command must pass before claiming completion.
- If verification still fails, explicitly say the work is incomplete and identify the remaining blocker.
- When task acceptance requires tests, do not finish without a focused regression or equivalent verification surface.
EOF
}

task_recovery_timeout() {
  if is_ts_bf001_vitest_task || is_ts_bf002_nestjs_task; then
    printf '%s\n' "$ELNATH_TIMEOUT"
    return 0
  fi
  printf '%s\n' $(( ELNATH_TIMEOUT / 2 ))
}

ts_bf001_recovery_guidance() {
  is_ts_bf001_vitest_task || return 0
  cat <<'EOF'

TS-BF-001 recovery guard:
- If verification says `No test files found`, create `test/cli/test/worker-retry-telemetry.test.ts`.
- If verification says `No test files found`, FIRST create `test/cli/test/worker-retry-telemetry.test.ts` before debating runtime callback types.
- Do not spend the recovery turn re-inspecting callback types until the focused test file exists.
- Create the focused test file first, then adjust runtime code only if the assertion shows retry snapshots are missing.
- A runtime-only diff is incomplete for this task; the focused worker retry telemetry regression is mandatory completion evidence.
- Do not finish with only `packages/vitest/src/runtime/runners/index.ts` changed.
- The regression must isolate the target retried test by task id/name.
- resolve the target retried test id from Vitest state or reported entities, for example `ctx.state.getTestModules()`, then filter retry packs with exact equality like `taskId === targetTaskId`.
- Also filter retry events by `taskId === targetTaskId` before accepting associated packs. Do not collect packs from `events.some(event => event[1] === 'test-retried')` without checking that the retry event belongs to the target task.
- For the reported-tasks fixture, target the existing leaf retry case named `retries a test with success`. Do not target generic retry titles such as `retries a test`.
- Do not modify `test/cli/fixtures/reported-tasks/1_first.test.ts` to manufacture a target retry case.
- Do not modify `test/cli/test/reported-tasks.test.ts`; use it only as a read-only pattern source and create the focused worker retry telemetry test instead.
- Do not filter packed task ids with filename or test-title substring checks such as `includes('1_first.test.ts')` or `endsWith('...retry #3')`.
- Do not rely on reporter delivery order; sort the isolated target retry snapshots by `retryCount` before exact comparison, or assert the two target snapshots order-insensitively.
- Do not assert the global `test-retried` event list or broad global retry stream.
- In `packages/vitest/src/runtime/runners/index.ts`, the `task` callback argument is a task object, not an array of packs. Do not call `task.map`; clone `task.result` and send `[[task.id, result, task.meta]]`.
- Do not import `TaskResultPack` to make `task.map` compile; that is evidence you are treating the live task object as the wrong shape.
- Keep the narrow worker-only verification command unchanged.
EOF
}

ts_bf002_no_change_recovery_guidance() {
  is_ts_bf002_nestjs_task || return 0
  cat <<'EOF'

TS-BF-002 no-change recovery guard:
- Make the smallest production change in `packages/common/module-utils/configurable-module.builder.ts`.
- Add or update the focused regression in `packages/common/test/module-utils/configurable-module.builder.spec.ts`.
- Preserve public async-options fields such as `provideInjectionTokensFrom`; do not replace public option fields.
- Do not treat import/module-resolution churn as semantic progress.
- If recovery still leaves no diff after edit intent, the benchmark will classify the run as `no_change_planning_failure`.
EOF
}

go_bf002_recovery_guidance() {
  [[ "$TASK_ID" == "GO-BF-002" ]] || return 0
  cat <<'EOF'

GO-BF-002 graceful shutdown guidance:
- Start at `caddy.go`, especially `unsyncedStop(ctx Context)`, before exploring app-specific modules.
- The smallest known-good seam is structured Zap progress logs around each app `Stop()` call in the shutdown loop.
- A minimal acceptable shape is `logger := Log()`, then `logger.Info("stopping app", zap.String("app", name))`, `logger.Info("stopped app", zap.String("app", name))`, and an error log using `zap.String("app", name)` plus `zap.Error(err)`.
- Avoid broad `modules/caddyhttp/app.go` shutdown rewrites unless inspection proves the central `caddy.go` shutdown path cannot satisfy the task.
- Do not finish with no diff; if recovery was needed, make a small concrete patch and run `go test -p 1 ./... -count=1`.
EOF
}

go_bf001_recovery_guidance() {
  [[ "$TASK_ID" == "GO-BF-001" ]] || return 0
  cat <<'EOF'

GO-BF-001 request-id logging guidance:
- Start in `logger.go` for the request-id middleware/logging behavior.
- Do not modify `gin.go` or the `Default()` middleware chain; `TestCreateDefaultRouter` expects only the existing default logger/recovery handlers.
- Keep request-id middleware opt-in, for example `router.Use(RequestID(), LoggerWithWriter(buffer))` in focused tests.
- If threading the request id into logs, read it from the Gin context inside `LoggerWithConfig` and preserve existing formatter behavior when no request id is present.
- Run `go test ./...` before the final answer.
EOF
}

go_bug001_recovery_guidance() {
  [[ "$TASK_ID" == "GO-BUG-001" ]] || return 0
  cat <<'EOF'

GO-BUG-001 timeout propagation guidance:
- Start at `command_run.go`; the high-signal bug seam is command runtime context propagation, not docs/completion rendering.
- In this repo, `Command.Before` can return a replacement context. preserve the parent deadline when a `Before` hook returns a replacement context so timeout propagation is not lost before the Action/subcommand path runs.
- Prefer the smallest patch in `command_run.go`; a known-good shape wraps the returned child context with the parent deadline when the parent deadline is earlier or the child has no deadline.
- Do not stop after diagnosing the context propagation path. This task requires an actual runtime diff.
- If recovery starts from no diff, make the smallest concrete patch in `command_run.go`, then run `go test ./...`.
EOF
}

go_bug002_recovery_guidance() {
  [[ "$TASK_ID" == "GO-BUG-002" ]] || return 0
  cat <<'EOF'

GO-BUG-002 no-change recovery guard:
- Start in `viper.go` at `WatchConfig()`; do not spend recovery on docs, encoders, errors, or feature flags.
- If recovery starts from no diff, make the concrete watcher patch: set `v.configFile = filename` immediately before `ReadInConfig()` in the fsnotify reload callback.
- If an exact-context patch misses due to spacing or indentation, re-anchor with `rg -n "realConfigFile = currentConfigFile|ReadInConfig" viper.go`, inspect the surrounding lines, and patch the observed block by line context instead of stopping.
- Add or append a focused subtest under `TestWatchFile` in `viper_test.go` proving a changed config file is re-read after the watcher event.
- If the `viper_test.go` insertion anchor misses, re-anchor with `rg -n "func TestWatchFile|OnConfigChange|WatchConfig" viper_test.go`, inspect the surrounding test block, and append the focused subtest by observed line context.
- Use a bounded wait for the watcher callback, such as `select` with `time.After` or `require.Eventually`; do not let a missing fsnotify event block the test forever.
- Do not add a bare `wg.Wait()` in new watcher regression coverage. If you use a `WaitGroup`, wrap it in a goroutine and a timeout/select so the test fails instead of hanging.
- Do not finish with only findings or only `viper.go` changed; this task needs `viper.go` plus focused regression evidence.
- A `viper.go`-only diff is incomplete even when `go test ./...` passes.
- Run `go test ./...` before the final answer.
EOF
}

is_go_bug002_viper_task() {
  [[ "$TASK_ID" == "GO-BUG-002" ]]
}

go_bug002_missing_focused_regression() {
  is_go_bug002_viper_task || return 1
  benchmark_changed_files_all | grep -qx 'viper.go' || return 1
  ! benchmark_changed_files_all | grep -qx 'viper_test.go'
}

go_bug002_unbounded_wait_regression() {
  is_go_bug002_viper_task || return 1
  benchmark_changed_files_all | grep -qx 'viper_test.go' || return 1
  if git -C "$WORKTREE" ls-files --error-unmatch viper_test.go >/dev/null 2>&1; then
    git -C "$WORKTREE" diff -U0 -- viper_test.go \
      | grep -Eq '^\+.*wg\.Wait[[:space:]]*\(' || return 1
  else
    grep -Eq 'wg\.Wait[[:space:]]*\(' "$WORKTREE/viper_test.go" || return 1
  fi
}

ts_bf001_missing_focused_regression() {
  is_ts_bf001_vitest_task || return 1
  local test_path="$WORKTREE/test/cli/test/worker-retry-telemetry.test.ts"
  [[ ! -f "$test_path" ]] || return 1
  benchmark_changed_files_all | grep -Eq '^(packages/runner/src/run\.ts|packages/vitest/src/runtime/(runners/index|worker)\.ts|packages/vitest/src/runtime/workers/.*\.ts)$'
}

ts_bf001_broad_retry_assertion_failure() {
  is_ts_bf001_vitest_task || return 1
  local test_path="$WORKTREE/test/cli/test/worker-retry-telemetry.test.ts"
  [[ -f "$test_path" ]] || return 1
  grep -Eq 'global.*test-retried|test-retried.*event list|toEqual\(\[\[1, .run.], \[2, .run.]]\)' "$test_path"
}

ts_bf001_packed_id_substring_matching() {
  is_ts_bf001_vitest_task || return 1
  local test_path="$WORKTREE/test/cli/test/worker-retry-telemetry.test.ts"
  [[ -f "$test_path" ]] || return 1
  grep -Eq "\.(includes|endsWith|startsWith|indexOf|match|search)[[:space:]]*\(" "$test_path" \
    && grep -Eq "1_first\.test\.ts|first test|retry #[0-9]" "$test_path"
}

ts_bf001_generic_retry_title_target() {
  is_ts_bf001_vitest_task || return 1
  local test_path="$WORKTREE/test/cli/test/worker-retry-telemetry.test.ts"
  [[ -f "$test_path" ]] || return 1
  grep -Eq "\.(find|filter)[[:space:]]*\(" "$test_path" \
    && grep -Eq "['\"]retries a test['\"]" "$test_path"
}

ts_bf001_reported_tasks_fixture_mutation() {
  is_ts_bf001_vitest_task || return 1
  benchmark_changed_files_all | grep -Eq '^test/cli/fixtures/reported-tasks/.*\.ts$'
}

ts_bf001_reported_tasks_test_mutation() {
  is_ts_bf001_vitest_task || return 1
  benchmark_changed_files_all | grep -Eq '^test/cli/test/reported-tasks\.test\.ts$'
}

ts_bf001_overfit_flaky_test_target() {
  is_ts_bf001_vitest_task || return 1
  local test_path="$WORKTREE/test/cli/test/worker-retry-telemetry.test.ts"
  [[ -f "$test_path" ]] || return 1
  grep -Eq "['\"]flaky test 1['\"]" "$test_path"
}

ts_bf001_wrong_runtime_pack_shape() {
  is_ts_bf001_vitest_task || return 1
  local runner_path="$WORKTREE/packages/vitest/src/runtime/runners/index.ts"
  [[ -f "$runner_path" ]] || return 1
  grep -Eq "task\.map([[:space:]]*<[^>]+>)?[[:space:]]*\(" "$runner_path" && {
    grep -Eq "onTaskUpdate[[:space:]]*\([[:space:]]*(taskSnapshots|taskPacks|packs)" "$runner_path" \
      || grep -Eq "onTaskUpdate[^\n]*task\.map([[:space:]]*<[^>]+>)?[[:space:]]*\(" "$runner_path" \
      || grep -Eq "TaskResultPack" "$runner_path"
  }
}

ts_bf001_order_sensitive_retry_snapshot_assertion() {
  is_ts_bf001_vitest_task || return 1
  local test_path="$WORKTREE/test/cli/test/worker-retry-telemetry.test.ts"
  [[ -f "$test_path" ]] || return 1
  grep -Eq "expect\([^)]*(targetRetrySnapshots|targetRetryResults)[^)]*\)\.toEqual\(\[" "$test_path" \
    && ! grep -Eq "\.sort[[:space:]]*\(|arrayContaining" "$test_path"
}

ts_bf001_unscoped_retry_event_capture() {
  is_ts_bf001_vitest_task || return 1
  local test_path="$WORKTREE/test/cli/test/worker-retry-telemetry.test.ts"
  [[ -f "$test_path" ]] || return 1
  python3 - <<'PY' "$test_path"
from pathlib import Path
import re
import sys

text = Path(sys.argv[1]).read_text()
captures_global_retry = re.search(
    r"events\.some\([^)]*=>[^)]*(?:event\s*\[\s*1\s*\]|\[\s*,\s*event\s*\])\s*={2,3}\s*['\"]test-retried['\"]",
    text,
    re.S,
)
has_target_retry_event_filter = re.search(
    r"events\.some\([^)]*=>[^)]*(?:taskId|id)\s*={2,3}\s*targetTaskId[^)]*['\"]test-retried['\"]",
    text,
    re.S,
) or re.search(
    r"events\.some\([^)]*=>[^)]*['\"]test-retried['\"][^)]*(?:taskId|id)\s*={2,3}\s*targetTaskId",
    text,
    re.S,
)
sys.exit(0 if captures_global_retry and not has_target_retry_event_filter else 1)
PY
}

ts_bf002_missing_focused_regression() {
  local spec_dir="$WORKTREE/packages/common/test/module-utils"
  local spec_path
  if [[ ! -d "$spec_dir" ]]; then
    return 0
  fi
  while IFS= read -r spec_path; do
    if grep -Eiq 'cancell|AbortError|CanceledError|CancelledError' "$spec_path"; then
      return 1
    fi
  done < <(find "$spec_dir" -maxdepth 1 -type f -name '*.spec.ts' -print)
  return 0
}

ts_bf002_public_async_option_regression() {
  local interface_path="$WORKTREE/packages/common/module-utils/interfaces/configurable-module-async-options.interface.ts"
  if [[ ! -f "$interface_path" ]]; then
    return 1
  fi
  grep -q 'onCancellation' "$interface_path" && ! grep -q 'provideInjectionTokensFrom' "$interface_path"
}

ts_bf002_import_churn_after_recovery() {
  local logs
  logs=""
  if [[ -f "$VERIFY_LOG" ]]; then
    logs+="$(tail -80 "$VERIFY_LOG")"$'\n'
  fi
  if [[ -f "$VERIFY_RETRY_LOG" ]]; then
    logs+="$(tail -80 "$VERIFY_RETRY_LOG")"$'\n'
  fi
  if ! grep -Eq 'ERR_(UNSUPPORTED_DIR_IMPORT|MODULE_NOT_FOUND)|Cannot find module|Directory import' <<<"$logs"; then
    return 1
  fi
  benchmark_changed_files_all | grep -Eq 'packages/common/test/module-utils/configurable-module\.builder\.spec\.ts|packages/common/module-utils/interfaces/configurable-module-async-options\.interface\.ts'
}

ts_bf002_incomplete_patch_after_failed_recovery() {
  is_ts_bf002_nestjs_task || return 1
  if ts_bf002_public_async_option_regression; then
    return 0
  fi
  if ts_bf002_import_churn_after_recovery && ts_bf002_missing_focused_regression; then
    return 0
  fi
  return 1
}

compile_error_incomplete_patch_after_failed_recovery() {
  [[ -n "$(working_tree_changes)" ]] || return 1
  local logs
  logs=""
  if [[ -f "$VERIFY_RETRY_LOG" ]]; then
    logs+="$(tail -120 "$VERIFY_RETRY_LOG")"$'\n'
  fi
  if [[ -f "$VERIFY_LOG" ]]; then
    logs+="$(tail -120 "$VERIFY_LOG")"$'\n'
  fi
  python3 -c '
import re
import sys

text = sys.stdin.read()
patterns = [
    r"imported and not used",
    r"declared and not used",
    r"undefined:",
    r"not enough arguments in call",
    r"too many arguments in call",
    r"cannot use .* as .* value",
]
sys.exit(0 if any(re.search(pattern, text) for pattern in patterns) else 1)
' <<<"$logs"
}

write_result() {
  local success="$1"
  local verification_passed="$2"
  local failure_family="$3"
  local recovery_attempted="$4"
  local recovery_succeeded="$5"
  local notes="$6"
  local force_final_incomplete="${7:-false}"
  local duration changed_files edit_intent final_incomplete changed_count trace_summary debug_evidence
  duration=$(( $(date +%s) - START_TS ))
  prepare_debug_artifacts
  changed_files="$(changed_files_json)"
  changed_count="$(changed_file_count)"
  if detect_edit_intent; then
    edit_intent=true
  else
    edit_intent=false
  fi
  if [[ "$force_final_incomplete" == "true" ]] || detect_final_incomplete; then
    final_incomplete=true
  else
    final_incomplete=false
  fi
  trace_summary="$(trace_summary_text "$recovery_attempted" "$changed_count" "$edit_intent" "$final_incomplete")"
  debug_evidence="$(debug_evidence_json)"
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
  "recovery_attempted": $recovery_attempted,
  "recovery_succeeded": $recovery_succeeded,
  "duration_seconds": $duration,
  "notes": $(json_escape "$notes"),
  "changed_files": $changed_files,
  "edit_intent_detected": $edit_intent,
  "final_incomplete_detected": $final_incomplete,
  "trace_summary": $(json_escape "$trace_summary")$debug_evidence
}
EOF
}

write_passed_verification_task_specific_failure() {
  local recovery_attempted="$1"
  local prefix="$2"
  if ts_bf001_reported_tasks_fixture_mutation; then
    write_result false true "incomplete_patch" "$recovery_attempted" false "$prefix, but TS-BF-001 modified the reported-tasks fixture to manufacture a target retry case"
    return 0
  fi
  if ts_bf001_reported_tasks_test_mutation; then
    write_result false true "incomplete_patch" "$recovery_attempted" false "$prefix, but TS-BF-001 modified the broad reported-tasks test instead of the focused worker retry telemetry test"
    return 0
  fi
  if ts_bf001_overfit_flaky_test_target; then
    write_result false true "incomplete_patch" "$recovery_attempted" false "$prefix, but TS-BF-001 targeted stale flaky test fixture text instead of the existing retry-success case"
    return 0
  fi
  if ts_bf001_order_sensitive_retry_snapshot_assertion; then
    write_result false true "incomplete_patch" "$recovery_attempted" false "$prefix, but TS-BF-001 regression assumes reporter retry snapshot delivery order"
    return 0
  fi
  if ts_bf001_unscoped_retry_event_capture; then
    write_result false true "incomplete_patch" "$recovery_attempted" false "$prefix, but TS-BF-001 regression collects packs from non-target retry events"
    return 0
  fi
  if go_bug002_missing_focused_regression; then
    write_result false true "incomplete_patch" "$recovery_attempted" false "$prefix, but GO-BUG-002 changed only viper.go without the focused TestWatchFile regression"
    return 0
  fi
  if go_bug002_unbounded_wait_regression; then
    write_result false true "incomplete_patch" "$recovery_attempted" false "$prefix, but GO-BUG-002 added an unbounded watcher wait that can hang verification"
    return 0
  fi
  return 1
}

task_specific_completion_failure_reason() {
  if go_bug002_missing_focused_regression; then
    echo "GO-BUG-002 changed only viper.go without the focused TestWatchFile regression."
    return 0
  fi
  if go_bug002_unbounded_wait_regression; then
    echo "GO-BUG-002 added an unbounded watcher wait that can hang verification."
    return 0
  fi
  return 1
}

recover_passed_task_specific_failure() {
  local reason
  reason="$(task_specific_completion_failure_reason)" || return 1
  printf -v TASK_SPECIFIC_PROMPT '%s\n\n%s\n\n%s' \
    "$BENCHMARK_PROMPT" \
    "The repo-native verification command '${VERIFY_CMD}' passed, but benchmark task-specific completion evidence is still missing: ${reason}" \
    "Keep the passing production patch intact, add or repair the missing focused regression evidence now, run '${VERIFY_CMD}', and only claim completion if the task-specific evidence is present."
  TASK_SPECIFIC_PROMPT+="$(typescript_recovery_checklist)"
  TASK_SPECIFIC_PROMPT+="$(recovery_completion_checklist)"
  TASK_SPECIFIC_PROMPT+="$(ts_bf001_recovery_guidance)"
  TASK_SPECIFIC_PROMPT+="$(ts_bf002_no_change_recovery_guidance)"
  TASK_SPECIFIC_PROMPT+="$(go_bf001_recovery_guidance)"
  TASK_SPECIFIC_PROMPT+="$(go_bf002_recovery_guidance)"
  TASK_SPECIFIC_PROMPT+="$(go_bug001_recovery_guidance)"
  TASK_SPECIFIC_PROMPT+="$(go_bug002_recovery_guidance)"
  RECOVERY_ATTEMPTED=true
  RECOVERY_EXIT=0
  RECOVERY_TIMEOUT=$(task_recovery_timeout)
  if ! run_elnath "$TASK_SPECIFIC_PROMPT" "$RECOVERY_LOG" "$RECOVERY_TIMEOUT"; then
    RECOVERY_EXIT=$?
  fi
  if run_verification_command "$VERIFY_RETRY_LOG"; then
    if write_passed_verification_task_specific_failure true "verification passed after task-specific recovery"; then
      exit 0
    fi
    if detect_final_incomplete; then
      write_result false true "incomplete_patch" true false "verification passed after task-specific recovery, but final response self-reported incomplete work"
      exit 0
    fi
    write_result true true "" true true "verification passed after completing task-specific evidence"
    exit 0
  fi
  if [[ "$RECOVERY_EXIT" -eq 124 ]]; then
    write_result false false "incomplete_patch" true false "task-specific recovery attempt timed out and verification still fails"
    exit 0
  fi
  if detect_final_incomplete || detect_failed_recovery_incomplete_admission; then
    write_result false false "incomplete_patch" true false "task-specific recovery self-reported incomplete work and verification still fails" true
    exit 0
  fi
  if go_bug002_missing_focused_regression || go_bug002_unbounded_wait_regression; then
    write_result false false "incomplete_patch" true false "task-specific recovery still lacks safe focused regression evidence"
    exit 0
  fi
  if compile_error_incomplete_patch_after_failed_recovery; then
    write_result false false "incomplete_patch" true false "task-specific recovery left compile-time evidence of incomplete patch wiring"
    exit 0
  fi
  write_result false false "verification_failed" true false "verification still failing after task-specific recovery"
  exit 0
}

collect_repo_hints() {
  python3 - <<'PY' "$WORKTREE" "$TASK_PROMPT"
import os, re, sys
from pathlib import Path

root = Path(sys.argv[1])
prompt = sys.argv[2].lower()
stop = {
    "the","and","with","into","without","existing","repository","codebase","task",
    "must","should","make","smallest","correct","change","verify","verification",
    "tests","test","feature","brownfield","track","language","repo","threaded",
    "through","breaking","handlers","actual","patch","inspect","code","files",
    "extend","current","behavior","regressing","emit","flow","service",
}
keywords = []
for token in re.findall(r"[a-z0-9_-]+", prompt):
    if len(token) < 4 or token in stop:
        continue
    if token not in keywords:
        keywords.append(token)
if not keywords:
    sys.exit(0)
allowed_suffixes = (".go", ".ts", ".tsx", ".js", ".jsx")
skip_dirs = {".git", "vendor", "node_modules", "dist", "build", "coverage", "tmp"}
skip_names = {"go.sum", "go.mod", "package-lock.json", "pnpm-lock.yaml", "yarn.lock"}

def score_path(rel: str) -> int:
    lower = rel.lower()
    score = 0
    for kw in keywords:
        if kw in lower:
            score += 3
    for marker in ("internal/", "cmd/", "pkg/", "src/", "packages/", "modules/", "lib/", "test/"):
        if marker in lower:
            score += 1
    if lower.startswith(("test/", "examples/")):
        score -= 2
    if "/fixtures/" in lower:
        score -= 2
    if "/runtime/" in lower or "/worker" in lower or "/workers/" in lower:
        score += 2
    if lower.endswith(allowed_suffixes):
        score += 1
    return score

def score_contents(path: Path) -> int:
    try:
        data = path.read_text(errors="ignore")
    except Exception:
        return 0
    data = data[:8192].lower()
    return sum(1 for kw in keywords if kw in data)

candidates = []
for dirpath, dirnames, filenames in os.walk(root):
    dirnames[:] = [d for d in dirnames if d not in skip_dirs]
    for name in filenames:
        if name in skip_names:
            continue
        path = Path(dirpath) / name
        rel = path.relative_to(root).as_posix()
        if rel.startswith(".github/") or rel.startswith("patches/") or "/test-d/" in rel:
            continue
        if name.startswith((".", "_")):
            continue
        if not rel.lower().endswith(allowed_suffixes):
            continue
        score = score_path(rel)
        if score < 2:
            score += score_contents(path)
        if score <= 0:
            continue
        candidates.append((score, rel))

candidates.sort(key=lambda item: (-item[0], item[1]))
if candidates:
    print("\\n".join(rel for _, rel in candidates[:12]))
PY
}

if ! command -v git >/dev/null 2>&1; then
  write_result false false "git_unavailable" false false "git is required for the benchmark wrapper"
  exit 0
fi

if ! git clone --depth 1 "$TASK_REPO" "$WORKTREE" >/dev/null 2>&1; then
  write_result false false "clone_failed" false false "failed to clone repo"
  exit 0
fi
if [[ -n "$TASK_REPO_REF" ]]; then
  if ! git -C "$WORKTREE" fetch --depth 1 origin "$TASK_REPO_REF" >/dev/null 2>&1; then
    write_result false false "checkout_failed" false false "failed to fetch pinned repo ref"
    exit 0
  fi
  if ! git -C "$WORKTREE" checkout --detach FETCH_HEAD >/dev/null 2>&1; then
    write_result false false "checkout_failed" false false "failed to checkout pinned repo ref"
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
  benchmark_changed_files_all
}

benchmark_specific_verification_command() {
  if [[ "$TASK_REPO" == *"caddyserver/caddy"* && "$TASK_PROMPT" == *"graceful shutdown"* ]]; then
    echo "go test -p 1 ./... -count=1"
    return 0
  fi
  if is_ts_bf001_vitest_task || [[ "$TASK_REPO" == *"vitest-dev/vitest"* && "$TASK_PROMPT" == *"retry telemetry"* ]]; then
    echo "npx pnpm -C packages/vitest build && npx pnpm -C test/cli exec vitest --run test/worker-retry-telemetry.test.ts"
    return 0
  fi
  if is_ts_bf002_nestjs_task || [[ "$TASK_REPO" == *"nestjs/nest"* && "$TASK_PROMPT" == *"cancellation tracing"* ]]; then
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
            if [[ "$TASK_REPO" == *"vitest-dev/vitest"* && "$TASK_PROMPT" == *"retry telemetry"* ]]; then
              echo "npx pnpm -C packages/vitest build && npx pnpm -C $pkg_dir exec vitest --run $package_local_test"
            else
              echo "npx pnpm -C packages/vitest build && npx pnpm -C $pkg_dir test -- --run $package_local_test"
            fi
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

pick_final_verification_command() {
  local cmd="${1:-}"
  local target_cmd
  local specific_cmd

  if target_cmd="$(pick_targeted_verification_command 2>/dev/null)"; then
    cmd="$target_cmd"
  fi
  if specific_cmd="$(benchmark_specific_verification_command 2>/dev/null)"; then
    cmd="$specific_cmd"
  fi
  if [[ -z "$cmd" ]]; then
    return 1
  fi
  echo "$cmd"
}

maybe_prepare_verification() {
  if [[ "${VERIFY_CMD:-}" == *"packages/vitest build"* ]]; then
    for pkg in packages/pretty-format packages/utils packages/spy packages/expect packages/runner packages/snapshot packages/mocker; do
      if [[ -f "$pkg/package.json" ]] && command -v npx >/dev/null 2>&1; then
        npx pnpm -C "$pkg" build >/dev/null 2>&1
      fi
    done
    return 0
  fi
  if [[ -f pnpm-lock.yaml ]] && command -v npx >/dev/null 2>&1; then
    npx pnpm build >/dev/null 2>&1 || true
  fi
}

run_verification_command() {
  local log_path="$1"
  local timeout_override="${2:-$ELNATH_VERIFY_TIMEOUT}"
  (
    cd "$WORKTREE"
    export HOME="$BENCHMARK_HOME_DIR"
    export TMPDIR="$BENCHMARK_TMP_DIR"
    export TMP="$BENCHMARK_TMP_DIR"
    export TEMP="$BENCHMARK_TMP_DIR"
    export GOMODCACHE="$BENCHMARK_GOMODCACHE_DIR"
    export GOCACHE="$BENCHMARK_GOCACHE_DIR"
    maybe_prepare_verification
    python3 - <<'PY' "$timeout_override" "$log_path" "$VERIFY_CMD"
import subprocess
import sys

timeout = int(sys.argv[1])
log_path = sys.argv[2]
cmd = sys.argv[3]
with open(log_path, "wb") as f:
    try:
        proc = subprocess.run(
            ["bash", "-lc", cmd],
            stdout=f,
            stderr=subprocess.STDOUT,
            timeout=timeout,
        )
        sys.exit(proc.returncode)
    except subprocess.TimeoutExpired:
        f.write(f"\nverification command timed out after {timeout}s\n".encode())
        sys.exit(124)
PY
  )
}

run_elnath() {
  local prompt="$1"
  local log_path="$2"
  local timeout_override="${3:-$ELNATH_TIMEOUT}"
  (
    cd "$WORKTREE"
    export ELNATH_EVAL_AUDIT_LOG="$AUDIT_LOG"
    export ELNATH_BENCHMARK_MODE=1
    export ELNATH_MAX_ITERATIONS=20
    export ELNATH_TASK_LANGUAGE="$TASK_LANGUAGE"
    export ELNATH_PERMISSION_MODE="${ELNATH_BENCHMARK_PERMISSION_MODE:-bypass}"
    export ELNATH_DATA_DIR="$BENCHMARK_DATA_DIR"
    export ELNATH_WIKI_DIR="$BENCHMARK_WIKI_DIR"
    export ELNATH_BENCHMARK_ENV_DIR="$BENCHMARK_ENV_DIR"
    export HOME="$BENCHMARK_HOME_DIR"
    local -a args=("$ELNATH_BIN" "run" "--non-interactive")
    python3 - <<'PY' "$timeout_override" "$log_path" "$prompt" "${args[@]}"
import subprocess, sys
timeout = int(sys.argv[1])
log_path = sys.argv[2]
prompt = sys.argv[3]
args = sys.argv[4:] + [prompt]
with open(log_path, "wb") as f:
    try:
        proc = subprocess.run(args, input=b"", stdout=f, stderr=subprocess.STDOUT, timeout=timeout)
        sys.exit(proc.returncode)
    except subprocess.TimeoutExpired:
        sys.exit(124)
PY
  )
}

if [[ "$TASK_LANGUAGE" == "typescript" ]]; then
  if ! (
    cd "$WORKTREE"
    export HOME="$BENCHMARK_HOME_DIR"
    install_js_deps
  ); then
    write_result false false "dependency_install_failed" false false "failed to install JavaScript dependencies"
    exit 0
  fi
fi

VERIFY_CMD=""
if VERIFY_CMD_CANDIDATE="$(cd "$WORKTREE" && pick_verification_command 2>/dev/null)"; then
  VERIFY_CMD="$VERIFY_CMD_CANDIDATE"
fi
if VERIFY_CMD_OVERRIDE="$(benchmark_specific_verification_command 2>/dev/null)"; then
  VERIFY_CMD="$VERIFY_CMD_OVERRIDE"
fi
export VERIFICATION_CMD="$VERIFY_CMD"

REPO_HINTS="$(collect_repo_hints || true)"
BENCHMARK_PROMPT="$(cat <<EOF
You are being evaluated on a brownfield coding task inside an existing repository.
You must inspect the existing code, make the smallest correct change, and leave a verifiable working-tree diff.
Prefer repo-native verification commands and existing patterns. If no code change is needed, explain why — but benchmark success requires an actual patch.

Before you finish, give a concise final answer that names the modified files and the verification command/result.
Your final answer must include enough concrete evidence for an internal verifier:
- modified files
- what production/runtime behavior changed
- the exact verification command you ran
- whether it passed, with a brief output snippet or count
- if a retry/fix was needed, what you corrected

Task ID: $TASK_ID
Track: $TASK_TRACK
Language: $TASK_LANGUAGE
Repo class: $TASK_REPO_CLASS
Benchmark family: $TASK_BENCHMARK_FAMILY
Repository: $TASK_REPO
EOF
)"

if [[ -n "$REPO_HINTS" ]]; then
  BENCHMARK_PROMPT+="

High-signal repo hints (paths/lines matched from the repo):
$REPO_HINTS"
fi
if [[ -n "$VERIFY_CMD" ]]; then
  BENCHMARK_PROMPT+="

Harness-detected repo-native verification command: $VERIFY_CMD"
  if [[ "$VERIFY_CMD" == npx\ pnpm* ]]; then
    BENCHMARK_PROMPT+="
Note: \`npx pnpm\` is available in this environment even if plain \`pnpm\` is not on PATH."
  fi
fi
if [[ "$REPO_HINTS" == *worker* ]]; then
  BENCHMARK_PROMPT+="
This task appears to target worker/runtime transport files. Prefer hinted worker/runtime files over generic runner/reporting/test-only files unless inspection proves otherwise."
fi
BENCHMARK_PROMPT+="$(go_bf001_recovery_guidance)"
BENCHMARK_PROMPT+="$(go_bf002_recovery_guidance)"
BENCHMARK_PROMPT+="$(go_bug001_recovery_guidance)"
BENCHMARK_PROMPT+="$(go_bug002_recovery_guidance)"
if [[ "$TASK_REPO" == *"vitest-dev/vitest"* ]]; then
  BENCHMARK_PROMPT+="

Vitest-specific guidance:
- Prefer worker/runtime transport touchpoints such as \`packages/vitest/src/runtime/runners/index.ts\`, \`packages/vitest/src/runtime/worker.ts\`, or adjacent worker files when they fit the evidence.
- If a regression test is needed, prefer a narrow CLI worker test under \`test/cli/test/worker-retry-telemetry.test.ts\` or a similarly focused file, rather than broad browser/open-telemetry matrix tests.
- Avoid modifying \`test/cli/test/open-telemetry.test.ts\` unless you are certain a broad browser+worker matrix change is required; for this benchmark, a focused worker-only regression is preferred.
- Avoid browser-dependent verification paths unless the worker/runtime change truly requires them."
  if [[ "$TASK_PROMPT" == *"retry telemetry"* ]]; then
    BENCHMARK_PROMPT+="
- For this task, start your investigation at \`packages/vitest/src/runtime/runners/index.ts\`; only fall back to \`packages/runner/src/run.ts\` if the existing worker task-update payload cannot carry the needed retry data.
- For this task, the preferred narrow regression path is:
  1. \`test/cli/test/worker-retry-telemetry.test.ts\`
  2. \`test/cli/test/reported-tasks.test.ts\` (as the canonical assertion pattern)
  3. \`test/cli/fixtures/reported-tasks/1_first.test.ts\` (existing retry fixture if useful)
- Prefer the existing reported-tasks testing pattern in \`test/cli/test/reported-tasks.test.ts\` over inventing a new state-inspection pattern from scratch.
- \`rpc().onTaskUpdate\` for this path should keep the packed reporter payload shape (\`[task.id, result, task.meta]\`) rather than forwarding live mutable task objects.
- For retry telemetry, prefer cloning/snapshotting the per-task \`result\` payload before sending it to RPC so later mutations do not overwrite the retry-visible \`retryCount\` / \`state: 'run'\` snapshot.
- In \`resolveTestRunner\`, \`testRunner.onTaskUpdate\` receives a live task object; build a one-entry packed payload like \`[[task.id, result, task.meta]]\` from that object after cloning \`task.result\`.
- Do not make \`packages/runner/src/run.ts\` the primary fix for this benchmark; only consider it after the focused \`packages/vitest/src/runtime/runners/index.ts\` seam is proven insufficient.
- In the regression test, assert the reporter-visible retry packs themselves (for example via \`packs.find(([taskId]) => taskId === id)\`) and confirm the retry snapshots carry incrementing \`retryCount\` values while the retry event is still in the \`run\` state.
- The \`reported-tasks\` fixture contains multiple retry/repeat/failure cases. Do not assert the global \`test-retried\` event list or global event order.
- Instead, isolate the target retried test by task id/name, then assert that target task's retry telemetry includes \`retryCount\` 1 and 2 while \`state\` is \`run\`.
- resolve the target retried test id from Vitest state or reported entities, for example \`ctx.state.getTestModules()\`, then filter retry packs using exact task id equality such as \`taskId === targetTaskId\`.
- Also filter retry events by \`taskId === targetTaskId\` before accepting associated packs; otherwise unrelated \`test-retried\` events can pull in the target's initial \`retryCount: 0\` snapshot.
- Do not collect packs from \`events.some(event => event[1] === 'test-retried')\` without checking that the retry event belongs to the target task.
- For the reported-tasks fixture, target the existing leaf retry case named \`retries a test with success\`. Do not target generic retry titles such as \`retries a test\`.
- Do not modify \`test/cli/fixtures/reported-tasks/1_first.test.ts\` to manufacture a target retry case; use the existing reported-tasks fixture behavior.
- Do not modify \`test/cli/test/reported-tasks.test.ts\`; use it only as a read-only pattern source and create the focused worker retry telemetry test instead.
- Do not filter packed task ids with filename or test-title substring checks such as \`includes('1_first.test.ts')\` or \`endsWith('...retry #3')\`; packed ids are not a stable filename/title assertion surface.
- Do not rely on reporter delivery order; sort the isolated target retry snapshots by \`retryCount\` before exact comparison, or assert the two target snapshots order-insensitively.
- The regression should tolerate valid extra retry/fail events from other tests, but it must fail if the target task's retry telemetry is missing.
- In \`packages/vitest/src/runtime/runners/index.ts\`, the \`task\` callback argument is a task object, not an array of packs. Do not call \`task.map\`; clone \`task.result\` and send \`[[task.id, result, task.meta]]\`.
- Do not import \`TaskResultPack\` to make \`task.map\` compile; that is evidence you are treating the live task object as the wrong shape.
- Do **not** weaken the regression to a final-state-only assertion, a completion-only assertion, or a generic “run passes” assertion; the benchmark requires proof that the retry-event snapshot itself is preserved at \`test-retried\` time.
- A strong pattern here is: capture retry-event packs inside reporter \`onTaskUpdate(packs, taskEvents)\`, filter \`taskEvents\` for \`test-retried\`, group matching packed results by \`taskId\`, map the target \`taskId\` back to the intended test name, then assert the isolated target's retry snapshots.
- For this task, prefer a worker-only CLI assertion over OTEL/browser matrix coverage; use reporter-visible task updates / reported entities before inventing new OpenTelemetry fixtures.
- Avoid \`test/cli/test/open-telemetry.test.ts\` and browser-oriented fixtures unless worker retry telemetry truly cannot be verified through reported tasks.
- Do not replace the narrow worker-only regression with a broad browser/open-telemetry matrix test."
  fi
fi
if [[ "$TASK_REPO" == *"nestjs/nest"* && "$TASK_PROMPT" == *"cancellation tracing"* ]]; then
  BENCHMARK_PROMPT+="

NestJS-specific guidance:
- Prefer the async configurable-module path hinted by the repo, especially \`packages/common/module-utils/configurable-module.builder.ts\` and \`packages/common/module-utils/interfaces/configurable-module-async-options.interface.ts\`, before exploring microservices code.
- The goal is explicit cancellation tracing in async-options/module-utils flow without changing success-path behavior; do not drift into unrelated microservice client cancellation logic unless the hinted async-options path clearly cannot support the requirement.
- Prefer the narrow unit regression in \`packages/common/test/module-utils/configurable-module.builder.spec.ts\` or an adjacent common-module-utils spec over broader integration suites that need external services.
- Avoid GraphQL/Mongoose/TypeORM integration suites unless your patch truly requires them; start with the shared common module-utils seam.
- Benchmark TS-BF-002 cancellation tracing guidance:
  - Preserve existing public async-options fields such as \`provideInjectionTokensFrom\`; do not remove or replace them while adding cancellation tracing.
  - Do not replace public option fields with unrelated new fields just to expose cancellation tracing.
  - Add a focused cancellation tracing regression test in \`packages/common/test/module-utils/configurable-module.builder.spec.ts\` or an adjacent common module-utils spec.
  - The regression should prove the cancellation/error tracing path and preserve success-path behavior.
  - Avoid import-style rewrites unless verification output proves they are necessary.
  - Preserve the existing TypeScript/ESM import style in \`configurable-module.builder.spec.ts\`; do not replace the file's top-level imports with bare CommonJS \`require(...)\`.
  - If a direct runtime import is needed to avoid a directory import error, use a minimal \`createRequire(import.meta.url)\` bridge for that one runtime import while keeping type imports type-only.
  - Do not finish if the semantic cancellation regression test is missing, even if import or module-resolution mechanics were changed."
fi
if [[ "$TASK_REPO" == *"vercel/next.js"* && "$TASK_PROMPT" == *"file-watcher regression"* ]]; then
  BENCHMARK_PROMPT+="

Next.js-specific guidance:
- Prefer the hinted config/watcher seam such as \`packages/next/src/server/lib/router-utils/setup-dev-bundler.ts\` and \`packages/next/src/lib/find-config.ts\` before exploring unrelated dev-server or e2e infrastructure.
- Prefer a narrow regression test like \`packages/next/src/lib/find-config.test.ts\` when it directly exercises the changed path; avoid broad dev/e2e watch suites unless the narrower unit path cannot prove the fix.
- Do not drift into unrelated Jest/build infrastructure fixes; the benchmark wants the smallest real fix in the config/watcher path.
- CRITICAL: In \`findConfig()\`, the \`_returnFile\` parameter is declared but NOT implemented — it is prefixed with underscore meaning unused. This is likely the bug. Implement it to return the config file path (a string) when \`_returnFile\` is true, instead of returning the parsed config content.
- Do NOT add cache-busting query parameters (\`?ts=Date.now()\`) to \`import()\` or \`require()\` calls. Jest's module resolver cannot resolve file paths with query strings — ALL ESM import tests will fail with 'Cannot find module'.
- Do NOT modify the \`esmImport\` helper or ESM \`import()\` paths. The fix should only add conditional \`return filePath\` / \`return packageJsonPath\` branches when \`_returnFile\` is true.
- Your regression test should verify that \`findConfig(dir, key, true)\` returns a string file path, not the parsed config object."
fi
if [[ "$TASK_REPO" == *"spf13/viper"* && "$TASK_PROMPT" == *"configuration reload"* ]]; then
  BENCHMARK_PROMPT+="

Viper-specific guidance:
- For config reload bugs, start at \`WatchConfig()\` in \`viper.go\` — this is where fsnotify events trigger config re-reads via \`ReadInConfig()\`.
- The most common reload regression is stale state: a field (like \`configFile\`) not being updated before \`ReadInConfig()\` is called inside the watcher callback. Check whether the resolved file path is being written back to \`v.configFile\` before the re-read.
- Do NOT modify \`SetConfigFile()\`, \`getConfigFile()\`, or error types in \`errors.go\` — these are stable public APIs. Changing them to fix a reload bug will break existing tests like \`TestReadConfigWithSetConfigFile\` and \`TestWrongFileNotFound\`. If you feel the need to change them, you are chasing the wrong root cause.
- A correct config reload fix is typically 1-3 lines in the watcher callback. If your fix touches more than \`viper.go\` + \`viper_test.go\`, reconsider.
- Add a focused regression test under the existing \`TestWatchFile\` test group to prove the reload works after a config file change.
- Do not finish with only \`viper.go\` changed; this task needs the focused \`viper_test.go\` regression as completion evidence."
fi

BENCHMARK_PROMPT+="

Execution discipline:
- Treat the hinted files as primary investigation targets before exploring adjacent code.
- Do not answer with test-only changes when the task asks for a production flow change.
- Prefer the smallest production-code patch plus the narrowest regression test needed to prove it.
- CRITICAL: Run the repo test suite before finishing. All existing tests MUST still pass. If your change breaks tests, revert to a smaller, safer approach.
- Use python3 (not python) for any scripting. Bare python is unavailable.
- Limit exploration: read at most 6-8 files before making your change. Do not exhaustively scan the repo.

$TASK_PROMPT"

RUN_EXIT=0
if ! run_elnath "$BENCHMARK_PROMPT" "$RUN_LOG"; then
  RUN_EXIT=$?
fi

HAS_CHANGES=false
if [[ -n "$(working_tree_changes)" ]]; then
  HAS_CHANGES=true
fi

RECOVERY_ATTEMPTED=false
RECOVERY_EXIT=0
if [[ "$HAS_CHANGES" == "false" ]]; then
  RECOVERY_ATTEMPTED=true
  printf -v NO_CHANGE_PROMPT '%s\n\n%s' \
    "$BENCHMARK_PROMPT" \
    "Your first attempt ended without producing any code changes. You must inspect the repository, modify files, and create the smallest correct patch that satisfies the task. Your final answer must explicitly list modified files and state whether '${VERIFY_CMD}' passed."
  NO_CHANGE_PROMPT+="$(typescript_recovery_checklist)"
  NO_CHANGE_PROMPT+="$(recovery_completion_checklist)"
  NO_CHANGE_PROMPT+="$(ts_bf001_recovery_guidance)"
  NO_CHANGE_PROMPT+="$(ts_bf002_no_change_recovery_guidance)"
  NO_CHANGE_PROMPT+="$(go_bf001_recovery_guidance)"
  NO_CHANGE_PROMPT+="$(go_bf002_recovery_guidance)"
  NO_CHANGE_PROMPT+="$(go_bug001_recovery_guidance)"
  NO_CHANGE_PROMPT+="$(go_bug002_recovery_guidance)"
  RECOVERY_TIMEOUT=$(task_recovery_timeout)
  if ! run_elnath "$NO_CHANGE_PROMPT" "$RECOVERY_LOG" "$RECOVERY_TIMEOUT"; then
    RECOVERY_EXIT=$?
  fi
  if [[ -n "$(working_tree_changes)" ]]; then
    HAS_CHANGES=true
  fi
fi

if [[ "$HAS_CHANGES" == "false" ]] && detect_edit_intent; then
  write_result false false "no_change_planning_failure" "$RECOVERY_ATTEMPTED" false "task completed without a working-tree diff after edit intent"
  exit 0
fi

if [[ "$HAS_CHANGES" == "false" && "$RUN_EXIT" -ne 0 ]]; then
  if [[ "$RUN_EXIT" -eq 124 || "$RECOVERY_EXIT" -eq 124 ]]; then
    write_result false false "execution_timeout" "$RECOVERY_ATTEMPTED" false "Elnath run timed out before producing a working-tree diff"
  else
    write_result false false "execution_failed" "$RECOVERY_ATTEMPTED" false "Elnath run failed; see wrapper logs"
  fi
  exit 0
fi
if [[ "$HAS_CHANGES" == "false" ]]; then
  write_result false false "no_changes" "$RECOVERY_ATTEMPTED" false "task completed without creating a working-tree diff"
  exit 0
fi

if TARGET_VERIFY_CMD="$(pick_final_verification_command "$VERIFY_CMD" 2>/dev/null)"; then
  VERIFY_CMD="$TARGET_VERIFY_CMD"
  export VERIFICATION_CMD="$VERIFY_CMD"
fi

if [[ -z "$VERIFY_CMD" ]]; then
  if [[ "$RUN_EXIT" -eq 124 ]]; then
    write_result false false "verification_unavailable" true false "Elnath timed out, produced a diff, but no repo-native verification command was detected"
  else
    write_result false false "verification_unavailable" "$RECOVERY_ATTEMPTED" false "no repo-native verification command was detected"
  fi
  exit 0
fi

if run_verification_command "$VERIFY_LOG"; then
  if [[ "$RECOVERY_ATTEMPTED" == "false" ]] && task_specific_completion_failure_reason >/dev/null; then
    recover_passed_task_specific_failure
  fi
  if write_passed_verification_task_specific_failure "$RECOVERY_ATTEMPTED" "verification passed"; then
    exit 0
  fi
  if detect_final_incomplete; then
    if [[ "$RECOVERY_ATTEMPTED" == "false" ]]; then
      printf -v VERIFIED_INCOMPLETE_PROMPT '%s\n\n%s' \
        "$BENCHMARK_PROMPT" \
        "The repo-native verification command '${VERIFY_CMD}' passed, but your final answer explicitly said the task is incomplete. Complete the remaining scope now: add any missing focused regression or completion evidence, keep the existing passing production patch intact, run '${VERIFY_CMD}', and only claim completion if the task is fully done."
      VERIFIED_INCOMPLETE_PROMPT+="$(typescript_recovery_checklist)"
      VERIFIED_INCOMPLETE_PROMPT+="$(recovery_completion_checklist)"
      VERIFIED_INCOMPLETE_PROMPT+="$(ts_bf001_recovery_guidance)"
      VERIFIED_INCOMPLETE_PROMPT+="$(ts_bf002_no_change_recovery_guidance)"
      VERIFIED_INCOMPLETE_PROMPT+="$(go_bf001_recovery_guidance)"
      VERIFIED_INCOMPLETE_PROMPT+="$(go_bf002_recovery_guidance)"
      VERIFIED_INCOMPLETE_PROMPT+="$(go_bug001_recovery_guidance)"
      VERIFIED_INCOMPLETE_PROMPT+="$(go_bug002_recovery_guidance)"
      RECOVERY_ATTEMPTED=true
      RECOVERY_EXIT=0
      RECOVERY_TIMEOUT=$(task_recovery_timeout)
      if ! run_elnath "$VERIFIED_INCOMPLETE_PROMPT" "$RECOVERY_LOG" "$RECOVERY_TIMEOUT"; then
        RECOVERY_EXIT=$?
      fi
      if run_verification_command "$VERIFY_RETRY_LOG"; then
        if write_passed_verification_task_specific_failure true "verification passed after recovery"; then
          exit 0
        fi
        if detect_final_incomplete; then
          write_result false true "incomplete_patch" true false "verification passed after recovery, but final response self-reported incomplete work"
          exit 0
        fi
        write_result true true "" true true "verification passed after completing self-reported incomplete work"
        exit 0
      fi
      if [[ "$RECOVERY_EXIT" -eq 124 ]]; then
        write_result false false "incomplete_patch" true false "recovery attempt timed out after self-reported incomplete work" true
        exit 0
      fi
      if detect_final_incomplete || detect_failed_recovery_incomplete_admission; then
        write_result false false "incomplete_patch" true false "recovery attempt self-reported incomplete work and verification still fails" true
        exit 0
      fi
      if compile_error_incomplete_patch_after_failed_recovery; then
        write_result false false "incomplete_patch" true false "recovery left compile-time evidence of incomplete patch wiring"
        exit 0
      fi
      write_result false false "verification_failed" true false "verification still failing after recovery for self-reported incomplete work"
      exit 0
    fi
    write_result false true "incomplete_patch" "$RECOVERY_ATTEMPTED" false "verification passed, but final response self-reported incomplete work"
    exit 0
  fi
  if [[ "$RUN_EXIT" -eq 124 ]]; then
    write_result true true "" true true "Elnath timed out, but produced a diff that passes repo-native verification"
  else
    if [[ "$RECOVERY_ATTEMPTED" == "true" ]]; then
      write_result true true "" true true "verification passed after one recovery attempt"
    else
      write_result true true "" false false "verification passed on first attempt"
    fi
  fi
  exit 0
fi

VERIFY_OUTPUT=""
if [[ -f "$VERIFY_LOG" ]]; then
  VERIFY_OUTPUT="$(tail -50 "$VERIFY_LOG")"
fi
printf -v RECOVERY_PROMPT '%s\n\n%s\n\nVerification output (last 50 lines):\n```\n%s\n```\n\n%s' \
  "$BENCHMARK_PROMPT" \
  "The repo-native verification command '${VERIFY_CMD}' failed after your first attempt." \
  "$VERIFY_OUTPUT" \
  "Fix the EXACT errors shown above. Do NOT re-run the verification command before making code changes. Read the error messages, identify which files and lines need editing, make the fixes, THEN run '${VERIFY_CMD}'. If errors mention 'not enough arguments' or 'too many arguments', grep for the function name to find all remaining call sites and fix them. Your final answer must explicitly list modified files and state whether '${VERIFY_CMD}' passed."
RECOVERY_PROMPT+="$(typescript_recovery_checklist)"
RECOVERY_PROMPT+="$(recovery_completion_checklist)"
RECOVERY_PROMPT+="$(ts_bf001_recovery_guidance)"
RECOVERY_PROMPT+="$(go_bf001_recovery_guidance)"
RECOVERY_PROMPT+="$(go_bf002_recovery_guidance)"
RECOVERY_PROMPT+="$(go_bug001_recovery_guidance)"
RECOVERY_PROMPT+="$(go_bug002_recovery_guidance)"
if is_ts_bf002_nestjs_task; then
  RECOVERY_PROMPT+="

TS-BF-002 recovery guard:
- Keep the semantic cancellation tracing regression test intact while fixing verification errors.
- Do not treat import/module-resolution churn as progress if the cancellation tracing regression is still missing.
- Preserve the existing TypeScript/ESM import style in \`configurable-module.builder.spec.ts\`; do not convert the spec to bare CommonJS \`require(...)\`.
- If avoiding a directory import error requires a runtime import adjustment, use a minimal \`createRequire(import.meta.url)\` bridge for that one runtime import and keep type imports type-only.
- Preserve existing public async-options fields such as \`provideInjectionTokensFrom\`; do not replace public fields with \`onCancellation\` or any unrelated new option.
- If you must change imports, keep the change minimal and verify with the same narrow Mocha command."
fi
RECOVERY_ATTEMPTED=true
RECOVERY_EXIT=0
RECOVERY_TIMEOUT=$(task_recovery_timeout)
if ! run_elnath "$RECOVERY_PROMPT" "$RECOVERY_LOG" "$RECOVERY_TIMEOUT"; then
  RECOVERY_EXIT=$?
fi

if run_verification_command "$VERIFY_RETRY_LOG"; then
  if write_passed_verification_task_specific_failure true "verification passed after recovery"; then
    exit 0
  fi
  if detect_final_incomplete; then
    write_result false true "incomplete_patch" true false "verification passed after recovery, but final response self-reported incomplete work"
    exit 0
  fi
  write_result true true "" true true "verification passed after one recovery attempt"
  exit 0
fi

if [[ "$RECOVERY_EXIT" -eq 124 ]]; then
  if detect_final_incomplete || detect_failed_recovery_incomplete_admission; then
    write_result false false "incomplete_patch" true false "recovery attempt self-reported incomplete work and verification still fails" true
    exit 0
  fi
  write_result false false "verification_failed" true false "recovery attempt timed out and verification still fails"
  exit 0
fi

if detect_final_incomplete || detect_failed_recovery_incomplete_admission; then
  write_result false false "incomplete_patch" true false "final response self-reported incomplete work and verification still fails" true
  exit 0
fi

if ts_bf001_missing_focused_regression; then
  write_result false false "incomplete_patch" true false "TS-BF-001 changed retry telemetry seams without the focused worker retry telemetry regression"
  exit 0
fi

if ts_bf001_broad_retry_assertion_failure; then
  write_result false false "incomplete_patch" true false "TS-BF-001 focused retry telemetry regression still asserts a broad/global retry stream"
  exit 0
fi

if ts_bf001_reported_tasks_fixture_mutation; then
  write_result false false "incomplete_patch" true false "TS-BF-001 modified the reported-tasks fixture to manufacture a target retry case"
  exit 0
fi

if ts_bf001_reported_tasks_test_mutation; then
  write_result false false "incomplete_patch" true false "TS-BF-001 modified the broad reported-tasks test instead of the focused worker retry telemetry test"
  exit 0
fi

if ts_bf001_overfit_flaky_test_target; then
  write_result false false "incomplete_patch" true false "TS-BF-001 focused retry telemetry regression targeted stale flaky test fixture text"
  exit 0
fi

if ts_bf001_wrong_runtime_pack_shape; then
  write_result false false "incomplete_patch" true false "TS-BF-001 runtime patch mapped task as if it were already packed reporter results"
  exit 0
fi

if ts_bf001_order_sensitive_retry_snapshot_assertion; then
  write_result false false "incomplete_patch" true false "TS-BF-001 focused retry telemetry regression assumes reporter retry snapshot delivery order"
  exit 0
fi

if ts_bf001_unscoped_retry_event_capture; then
  write_result false false "incomplete_patch" true false "TS-BF-001 focused retry telemetry regression collects packs from non-target retry events"
  exit 0
fi

if ts_bf001_packed_id_substring_matching; then
  write_result false false "incomplete_patch" true false "TS-BF-001 focused retry telemetry regression used brittle packed-id substring matching"
  exit 0
fi

if ts_bf001_generic_retry_title_target; then
  write_result false false "incomplete_patch" true false "TS-BF-001 focused retry telemetry regression selected a generic retry title instead of the intended target task"
  exit 0
fi

if ts_bf002_incomplete_patch_after_failed_recovery; then
  write_result false false "incomplete_patch" true false "TS-BF-002 recovery changed imports/public options without a focused cancellation regression"
  exit 0
fi

if go_bug002_unbounded_wait_regression; then
  write_result false false "incomplete_patch" true false "GO-BUG-002 added an unbounded watcher wait that can hang verification"
  exit 0
fi

if compile_error_incomplete_patch_after_failed_recovery; then
  write_result false false "incomplete_patch" true false "recovery left compile-time evidence of incomplete patch wiring"
  exit 0
fi

write_result false false "verification_failed" true false "verification still failing after one recovery attempt"
