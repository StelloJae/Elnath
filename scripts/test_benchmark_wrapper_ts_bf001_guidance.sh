#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CURRENT_WRAPPER="$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh"
BASELINE_WRAPPER="$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh"

python3 - "$CURRENT_WRAPPER" "$BASELINE_WRAPPER" "$REPO_ROOT" <<'PY'
from pathlib import Path
import re
import sys

current = Path(sys.argv[1])
baseline = Path(sys.argv[2])
repo_root = Path(sys.argv[3])
current_text = current.read_text()
baseline_text = baseline.read_text()
current_guidance = current_text.replace("\\`", "`")
baseline_guidance = baseline_text.replace("\\`", "`")

expected_cmd = "npx pnpm -C packages/vitest build && npx pnpm -C test/cli exec vitest --run test/worker-retry-telemetry.test.ts"
broad_cmd = "npx pnpm build && npx pnpm -C test/cli exec vitest --run test/worker-retry-telemetry.test.ts"

if expected_cmd not in current_text:
    raise SystemExit("current wrapper lost the narrow TS-BF-001 verification command")
if expected_cmd not in baseline_text:
    raise SystemExit("baseline wrapper lost the narrow TS-BF-001 verification command")
if broad_cmd in baseline_text:
    raise SystemExit("baseline wrapper still contains the broad TS-BF-001 verification command")

if "exact equality against the expected retry sequence" in current_guidance:
    raise SystemExit("TS-BF-001 guidance still suggests an exact global retry sequence assertion")
if "Benchmark TS-BF-001 retry telemetry guidance:" in baseline_guidance:
    raise SystemExit("baseline wrapper should not rewrite the baseline task prompt with TS-BF-001 repair guidance")

required_current_guidance = [
    "`reported-tasks` fixture contains multiple retry/repeat/failure cases",
    "Do not assert the global `test-retried` event list",
    "isolate the target retried test by task id/name",
    "resolve the target retried test id from Vitest state or reported entities",
    "ctx.state.getTestModules()",
    "taskId === targetTaskId",
    "filter retry events by `taskId === targetTaskId`",
    "Do not collect packs from `events.some(event => event[1] === 'test-retried')`",
    "retries a test with success",
    "Do not target generic retry titles such as `retries a test`",
    "Do not modify `test/cli/fixtures/reported-tasks/1_first.test.ts`",
    "Do not modify `test/cli/test/reported-tasks.test.ts`",
    "Do not filter packed task ids with filename or test-title substring checks",
    "packed reporter payload shape",
    "Do not rely on reporter delivery order",
    "sort the isolated target retry snapshots by `retryCount`",
    "the `task` callback argument is a task object, not an array of packs",
    "In `resolveTestRunner`, `testRunner.onTaskUpdate` receives a live task object",
    "build a one-entry packed payload like `[[task.id, result, task.meta]]`",
    "Do not call `task.map`",
    "Do not import `TaskResultPack` to make `task.map` compile",
    "Do not make `packages/runner/src/run.ts` the primary fix",
    "FIRST create `test/cli/test/worker-retry-telemetry.test.ts`",
    "Do not spend the recovery turn re-inspecting callback types until the focused test file exists",
    "Create the focused test file first, then adjust runtime code only if the assertion shows retry snapshots are missing",
    "runtime-only diff is incomplete",
    "Do not finish with only `packages/vitest/src/runtime/runners/index.ts` changed",
    "target task's retry telemetry is missing",
    "valid extra retry/fail events from other tests",
]
missing = [snippet for snippet in required_current_guidance if snippet not in current_guidance]
if missing:
    raise SystemExit("current wrapper missing TS-BF-001 guidance: " + ", ".join(missing))

if not re.search(r"target\s+task.*retryCount.*1.*2", current_guidance, re.S):
    raise SystemExit("TS-BF-001 guidance does not require retryCount 1/2 on the isolated target task")

recovery_guidance = current_guidance
required_recovery_guidance = [
    "test/cli/test/worker-retry-telemetry.test.ts",
    "No test files found",
    "FIRST create `test/cli/test/worker-retry-telemetry.test.ts`",
    "Do not spend the recovery turn re-inspecting callback types until the focused test file exists",
    "isolate the target retried test by task id/name",
    "Do not assert the global `test-retried` event list",
]
missing_recovery = [snippet for snippet in required_recovery_guidance if snippet not in recovery_guidance]
if missing_recovery:
    raise SystemExit("current wrapper missing TS-BF-001 recovery guidance: " + ", ".join(missing_recovery))

no_change_start = current_text.index('NO_CHANGE_PROMPT+="$(typescript_recovery_checklist)"')
no_change_end = current_text.index("RECOVERY_TIMEOUT", no_change_start)
no_change_block = current_text[no_change_start:no_change_end]
if 'NO_CHANGE_PROMPT+="$(ts_bf001_recovery_guidance)"' not in no_change_block:
    raise SystemExit("TS-BF-001 no-change recovery path is missing focused retry telemetry guidance")

if "task_recovery_timeout()" not in current_text:
    raise SystemExit("current wrapper is missing task-specific recovery timeout helper")
if "if is_ts_bf001_vitest_task || is_ts_bf002_nestjs_task; then" not in current_text or 'printf \'%s\\n\' "$ELNATH_TIMEOUT"' not in current_text:
    raise SystemExit("TS-BF-001 recovery should use the full ELNATH_TIMEOUT budget")
if "RECOVERY_TIMEOUT=$(task_recovery_timeout)" not in current_text:
    raise SystemExit("recovery paths should use task-specific recovery timeout")

for corpus in (
    "benchmarks/month3-canary-corpus.v1.json",
    "benchmarks/public-corpus.v1.json",
    "benchmarks/brownfield-primary.v1.json",
):
    path = repo_root / corpus
    if not path.exists():
        raise SystemExit(f"expected corpus file missing: {corpus}")

print("PASS: TS-BF-001 benchmark guidance rejects broad retry-stream assertions")
PY

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-ts-bf001-guidance.XXXXXX")"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

SOURCE_REPO="$TMP_DIR/source-repo"
mkdir -p "$SOURCE_REPO"
cat >"$SOURCE_REPO/package.json" <<'EOF'
{"scripts":{"test":"node fail.js"}}
EOF
cat >"$SOURCE_REPO/fail.js" <<'EOF'
console.error("No test files found, exiting with code 1");
console.error("filter: test/worker-retry-telemetry.test.ts");
process.exit(1);
EOF
git -C "$SOURCE_REPO" init -q
git -C "$SOURCE_REPO" add .
git -C "$SOURCE_REPO" -c user.name='Test User' -c user.email='test@example.com' commit -qm "init"

cat >"$TMP_DIR/fake-elnath.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
mkdir -p packages/vitest/src/runtime/runners test/cli/test
case "${FAKE_TS_BF001_SCENARIO:?}" in
  missing_focused_regression)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts"
    echo "Verification: still needs the focused worker retry telemetry test."
    ;;
  many_untracked_missing_focused_regression)
    mkdir -p aaa-generated
    for i in $(seq -w 1 120); do
      printf 'generated %s\n' "$i" > "aaa-generated/file-$i.txt"
    done
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts plus generated files"
    echo "Verification: still needs the focused worker retry telemetry test."
    ;;
  broad_retry_assertion)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry', () => {
  const retryEvents = [[1, 'run'], [2, 'run'], [3, 'run'], [4, 'run'], [5, 'fail']]
  expect(retryEvents).toEqual([[1, 'run'], [2, 'run']])
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: test currently asserts the retry event list."
    ;;
  packed_id_substring_matching)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry guesses target from packed id text', () => {
  const retryPacks = [
    ['opaque-task-id', { retryCount: 1, state: 'run' }],
    ['another-opaque-task-id', { retryCount: 2, state: 'run' }],
  ]
  const targetPacks = retryPacks.filter(pack =>
    pack[0].includes('1_first.test.ts')
    && pack[0].endsWith('first test > fails and retry #3')
  )
  expect(targetPacks.map(pack => pack[1]).filter(Boolean)).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ retryCount: 1, state: 'run' }),
      expect.objectContaining({ retryCount: 2, state: 'run' }),
    ]),
  )
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: test guesses target task from packed id filename/title substrings."
    ;;
  packed_id_substring_matching_destructured)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry guesses target from destructured packed id text', () => {
  const retryPacks = [
    ['opaque-task-id', { retryCount: 1, state: 'run' }],
    ['another-opaque-task-id', { retryCount: 2, state: 'run' }],
  ]
  const targetPacks = retryPacks.filter(([id]) =>
    id.includes('1_first.test.ts')
    && id.endsWith('first test > fails and retry #3')
  )
  expect(targetPacks.map(([, result]) => result)).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ retryCount: 1, state: 'run' }),
      expect.objectContaining({ retryCount: 2, state: 'run' }),
    ]),
  )
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: test guesses target task from destructured packed id filename/title substrings."
    ;;
  reported_tasks_fixture_mutation)
    mkdir -p test/cli/fixtures/reported-tasks packages/vitest/src/runtime/runners test/cli/test
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    cat > test/cli/fixtures/reported-tasks/1_first.test.ts <<'TS'
import { expect, it } from 'vitest'

let flakyCounter = 0
it('flaky test 1', { retry: 5 }, () => {
  expect(flakyCounter++).toBe(2)
})
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry uses a manufactured fixture target', () => {
  const targetTaskName = 'flaky test 1'
  expect(targetTaskName).toBe('flaky test 1')
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/fixtures/reported-tasks/1_first.test.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: generated test mutates the reported-tasks fixture to manufacture a target retry case."
    ;;
  reported_tasks_test_mutation)
    mkdir -p packages/vitest/src/runtime/runners test/cli/test
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    cat > test/cli/test/reported-tasks.test.ts <<'TS'
import { expect, it } from 'vitest'

it('asserts retry telemetry in the broad reported tasks suite', () => {
  expect(true).toBe(true)
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/reported-tasks.test.ts"
    echo "Verification: generated regression edits the broad reported-tasks test instead of the focused worker retry telemetry test."
    ;;
  flaky_test_1_target)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry targets an overfit fixture name', () => {
  const ctx = {
    state: {
      getTestModules() {
        return [{
          children: {
            allTests() {
              return [{ name: 'flaky test 1', id: 'target-task-id' }]
            },
          },
        }]
      },
    },
  }
  const targetTaskId = ctx.state.getTestModules()
    .flatMap(module => module.children.allTests())
    .find(test => test.name === 'flaky test 1')
    .id
  expect(targetTaskId).toBe('target-task-id')
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: generated test targets the stale flaky test 1 name."
    ;;
  wrong_runtime_pack_shape)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export function emitTask(task, events, rpc) {
  const taskSnapshots = task.map(([id, result, meta]) => [
    id,
    result ? { ...result, errors: result.errors?.slice() } : result,
    meta,
  ])
  return rpc().onTaskUpdate(taskSnapshots, events)
}
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry resolves target id from runtime state', () => {
  const targetTaskId = 'target-task-id'
  const retryPacks = [
    ['target-task-id', { retryCount: 1, state: 'run' }],
    ['target-task-id', { retryCount: 2, state: 'run' }],
  ]
  const targetRetryResults = retryPacks
    .filter(([taskId]) => taskId === targetTaskId)
    .map(([, result]) => result)
  expect(targetRetryResults).toEqual([
    { retryCount: 1, state: 'run' },
    { retryCount: 2, state: 'run' },
  ])
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: runtime patch maps task as though it were already packed task results."
    ;;
  direct_task_map_runtime_pack_shape)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export function emitTask(task, events, rpc) {
  return rpc().onTaskUpdate(task.map(([id, result, meta]) => [
    id,
    result ? { ...result } : result,
    meta,
  ]), events)
}
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry resolves target id from runtime state', () => {
  const targetTaskId = 'target-task-id'
  const retryPacks = [
    ['target-task-id', { retryCount: 1, state: 'run' }],
    ['target-task-id', { retryCount: 2, state: 'run' }],
  ]
  const targetRetryResults = retryPacks
    .filter(([taskId]) => taskId === targetTaskId)
    .map(([, result]) => result)
    .sort((a, b) => (a?.retryCount ?? 0) - (b?.retryCount ?? 0))
  expect(targetRetryResults).toEqual([
    { retryCount: 1, state: 'run' },
    { retryCount: 2, state: 'run' },
  ])
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: runtime patch passes task.map directly to rpc().onTaskUpdate."
    ;;
  multiline_task_map_runtime_pack_shape)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
import type { TaskResultPack } from '@vitest/runner'

export function emitTask(task, events, rpc, originalOnTaskUpdate, testRunner) {
  const taskPacks = task.map<TaskResultPack>(([id, result, meta]) => [
    id,
    result ? { ...structuredClone(result) } : result,
    meta,
  ])
  const p = rpc().onTaskUpdate(taskPacks, events)
  originalOnTaskUpdate?.call(testRunner, taskPacks, events)
  return p
}
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry resolves target id from runtime state', () => {
  const targetTaskId = 'target-task-id'
  const retryPacks = [
    ['target-task-id', { retryCount: 1, state: 'run' }],
    ['target-task-id', { retryCount: 2, state: 'run' }],
  ]
  const targetRetryResults = retryPacks
    .filter(([taskId]) => taskId === targetTaskId)
    .map(([, result]) => result)
  expect(targetRetryResults).toEqual([
    { retryCount: 1, state: 'run' },
    { retryCount: 2, state: 'run' },
  ])
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: runtime patch imports TaskResultPack and maps the callback task over multiple lines."
    ;;
  order_sensitive_retry_snapshot_assertion)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry resolves target id from runtime state but assumes reporter order', () => {
  const targetTaskId = 'target-task-id'
  const retryPacks = [
    ['target-task-id', { retryCount: 2, state: 'run' }],
    ['target-task-id', { retryCount: 1, state: 'run' }],
  ]
  const targetRetrySnapshots = retryPacks
    .filter(([taskId]) => taskId === targetTaskId)
    .map(([, result]) => ({ retryCount: result?.retryCount, state: result?.state }))
  expect(targetRetrySnapshots).toEqual([
    { retryCount: 1, state: 'run' },
    { retryCount: 2, state: 'run' },
  ])
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: focused test assumes reporter delivery order instead of sorting retry snapshots."
    ;;
  unscoped_retry_event_capture)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry resolves target id from runtime state but captures global retry events', () => {
  const targetTaskId = 'target-task-id'
  const retryPacks: Array<[string, { retryCount: number; state: string }]> = []
  const events: Array<[string, string]> = [['other-task-id', 'test-retried']]
  const packs: Array<[string, { retryCount: number; state: string }]> = [
    ['target-task-id', { retryCount: 0, state: 'run' }],
    ['target-task-id', { retryCount: 1, state: 'run' }],
    ['target-task-id', { retryCount: 2, state: 'run' }],
  ]

  if (events.some(event => event[1] === 'test-retried')) {
    retryPacks.push(...packs)
  }

  const targetRetrySnapshots = retryPacks
    .filter(([taskId]) => taskId === targetTaskId)
    .map(([, result]) => result)
    .filter(result => result.state === 'run')
    .sort((a, b) => a.retryCount - b.retryCount)

  expect(targetRetrySnapshots.map(result => result.retryCount)).toEqual([1, 2])
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: focused test captures packs from any test-retried event instead of the target task event."
    ;;
  focused_retry_events_variable)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry isolates the target task', () => {
  const targetTaskId = 'target'
  const taskEvents = [
    ['test-retried', 'target', 1],
    ['test-retried', 'target', 2],
    ['test-retried', 'other', 1],
  ]
  const retryEvents = taskEvents.filter(([, taskId]) => taskId === targetTaskId)
  expect(retryEvents.map((event) => event[2])).toEqual([1, 2])
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: focused test uses retryEvents as a local target-filtered variable."
    ;;
  runtime_target_id_resolution)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry resolves target id from runtime state', () => {
  const ctx = {
    state: {
      getTestModules() {
        return [{
          children: {
            allTests() {
              return [{ name: 'retries a test with success', id: 'target-task-id' }]
            },
          },
        }]
      },
    },
  }
  const targetTaskId = ctx.state.getTestModules()
    .flatMap(module => module.children.allTests())
    .find(test => test.name === 'retries a test with success')
    .id
  const retryPacks = [
    ['target-task-id', { retryCount: 1, state: 'run' }],
    ['target-task-id', { retryCount: 2, state: 'run' }],
    ['other-task-id', { retryCount: 1, state: 'run' }],
  ]
  const targetRetryResults = retryPacks
    .filter(([taskId]) => taskId === targetTaskId)
    .map(([, result]) => result)
    .sort((a, b) => (a?.retryCount ?? 0) - (b?.retryCount ?? 0))
  expect(targetRetryResults).toEqual([
    { retryCount: 1, state: 'run' },
    { retryCount: 2, state: 'run' },
  ])
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: focused test resolves target id from runtime state and filters by exact equality."
    ;;
  generic_retry_title_target)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry resolves the wrong generic retry title', () => {
  const ctx = {
    state: {
      getTestModules() {
        return [{
          children: {
            allTests() {
              return [
                { name: 'flaky test 1', id: 'target-task-id' },
                { name: 'retries a test with success', id: 'target-task-id' },
                { name: 'retries a test', id: 'generic-retry-title' },
              ]
            },
          },
        }]
      },
    },
  }
  const targetTaskId = ctx.state.getTestModules()
    .flatMap(module => module.children.allTests())
    .find(test => test.name === 'retries a test')
    .id
  const retryPacks = [
    ['target-task-id', { retryCount: 1, state: 'run' }],
    ['target-task-id', { retryCount: 2, state: 'run' }],
    ['generic-retry-title', { retryCount: 5, state: 'fail' }],
  ]
  const targetRetryResults = retryPacks
    .filter(([taskId]) => taskId === targetTaskId)
    .map(([, result]) => result)
  expect(targetRetryResults).toEqual([
    { retryCount: 1, state: 'run' },
    { retryCount: 2, state: 'run' },
  ])
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: focused test resolves a generic retry title instead of the intended leaf task."
    ;;
  generic_retry_title_target_destructured)
    cat > packages/vitest/src/runtime/runners/index.ts <<'TS'
export const clonedRetryResult = true
TS
    cat > test/cli/test/worker-retry-telemetry.test.ts <<'TS'
import { expect, test } from 'vitest'

test('retry telemetry resolves generic retry title with destructuring', () => {
  const ctx = {
    state: {
      getTestModules() {
        return [{
          children: {
            allTests() {
              return [
                { name: 'flaky test 1', id: 'target-task-id' },
                { name: 'retries a test with success', id: 'target-task-id' },
                { name: 'retries a test', id: 'generic-retry-title' },
              ]
            },
          },
        }]
      },
    },
  }
  const targetTaskId = ctx.state.getTestModules()
    .flatMap(module => module.children.allTests())
    .find(({ name }) => name === 'retries a test')
    .id
  const retryPacks = [
    ['target-task-id', { retryCount: 1, state: 'run' }],
    ['target-task-id', { retryCount: 2, state: 'run' }],
    ['generic-retry-title', { retryCount: 5, state: 'fail' }],
  ]
  const targetRetryResults = retryPacks
    .filter(([taskId]) => taskId === targetTaskId)
    .map(([, result]) => result)
  expect(targetRetryResults).toEqual([
    { retryCount: 1, state: 'run' },
    { retryCount: 2, state: 'run' },
  ])
})
TS
    echo "Modified files: packages/vitest/src/runtime/runners/index.ts, test/cli/test/worker-retry-telemetry.test.ts"
    echo "Verification: focused test resolves a destructured generic retry title instead of the intended leaf task."
    ;;
  *)
    echo "unknown FAKE_TS_BF001_SCENARIO=${FAKE_TS_BF001_SCENARIO}" >&2
    exit 2
    ;;
esac
EOF
chmod +x "$TMP_DIR/fake-elnath.sh"

run_wrapper_case() {
  local scenario="$1"
  local output_path="$2"
  FAKE_TS_BF001_SCENARIO="$scenario" \
  ELNATH_BIN="$TMP_DIR/fake-elnath.sh" \
  ELNATH_TIMEOUT=30 \
  ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
  HOME="$TMP_DIR/host-home" \
  "$CURRENT_WRAPPER" \
    "$output_path" \
    "TS-BF-001" \
    "brownfield_feature" \
    "typescript" \
    "Extend an existing TypeScript worker flow to emit retry telemetry without regressing current behavior." \
    "file://$SOURCE_REPO" \
    "" \
    "cli_dev_tool" \
    "month2_canary"
}

hash_corpora() {
  python3 - <<'PY' "$REPO_ROOT"
from hashlib import sha256
from pathlib import Path
import sys
root = Path(sys.argv[1])
corpora = [
    "benchmarks/month3-canary-corpus.v1.json",
    "benchmarks/public-corpus.v1.json",
    "benchmarks/brownfield-primary.v1.json",
]
for rel in corpora:
    path = root / rel
    print(rel + "=" + sha256(path.read_bytes()).hexdigest())
PY
}

before_hash="$(hash_corpora)"

run_wrapper_case missing_focused_regression "$TMP_DIR/ts-bf001-missing-test.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-missing-test.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "packages/vitest/src/runtime/runners/index.ts" in data["changed_files"], data
PY

run_wrapper_case many_untracked_missing_focused_regression "$TMP_DIR/ts-bf001-many-untracked-missing-test.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-many-untracked-missing-test.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert len(data["changed_files"]) <= 100, data
assert "packages/vitest/src/runtime/runners/index.ts" not in data["changed_files"], data
PY

run_wrapper_case broad_retry_assertion "$TMP_DIR/ts-bf001-broad-assertion.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-broad-assertion.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "test/cli/test/worker-retry-telemetry.test.ts" in data["changed_files"], data
PY

run_wrapper_case packed_id_substring_matching "$TMP_DIR/ts-bf001-packed-id-substring.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-packed-id-substring.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "test/cli/test/worker-retry-telemetry.test.ts" in data["changed_files"], data
PY

run_wrapper_case packed_id_substring_matching_destructured "$TMP_DIR/ts-bf001-packed-id-substring-destructured.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-packed-id-substring-destructured.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "test/cli/test/worker-retry-telemetry.test.ts" in data["changed_files"], data
PY

run_wrapper_case reported_tasks_fixture_mutation "$TMP_DIR/ts-bf001-reported-tasks-fixture-mutation.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-reported-tasks-fixture-mutation.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "test/cli/fixtures/reported-tasks/1_first.test.ts" in data["changed_files"], data
PY

run_wrapper_case reported_tasks_test_mutation "$TMP_DIR/ts-bf001-reported-tasks-test-mutation.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-reported-tasks-test-mutation.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "test/cli/test/reported-tasks.test.ts" in data["changed_files"], data
PY

run_wrapper_case flaky_test_1_target "$TMP_DIR/ts-bf001-flaky-test-1-target.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-flaky-test-1-target.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "test/cli/test/worker-retry-telemetry.test.ts" in data["changed_files"], data
PY

run_wrapper_case wrong_runtime_pack_shape "$TMP_DIR/ts-bf001-wrong-runtime-pack-shape.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-wrong-runtime-pack-shape.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "packages/vitest/src/runtime/runners/index.ts" in data["changed_files"], data
PY

run_wrapper_case direct_task_map_runtime_pack_shape "$TMP_DIR/ts-bf001-direct-task-map-runtime-pack-shape.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-direct-task-map-runtime-pack-shape.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "packages/vitest/src/runtime/runners/index.ts" in data["changed_files"], data
PY

run_wrapper_case multiline_task_map_runtime_pack_shape "$TMP_DIR/ts-bf001-multiline-task-map-runtime-pack-shape.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-multiline-task-map-runtime-pack-shape.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "packages/vitest/src/runtime/runners/index.ts" in data["changed_files"], data
PY

run_wrapper_case order_sensitive_retry_snapshot_assertion "$TMP_DIR/ts-bf001-order-sensitive-retry-snapshot-assertion.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-order-sensitive-retry-snapshot-assertion.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "test/cli/test/worker-retry-telemetry.test.ts" in data["changed_files"], data
PY

run_wrapper_case unscoped_retry_event_capture "$TMP_DIR/ts-bf001-unscoped-retry-event-capture.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-unscoped-retry-event-capture.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "test/cli/test/worker-retry-telemetry.test.ts" in data["changed_files"], data
PY

run_wrapper_case focused_retry_events_variable "$TMP_DIR/ts-bf001-focused-retry-events-variable.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-focused-retry-events-variable.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "verification_failed", data
assert "test/cli/test/worker-retry-telemetry.test.ts" in data["changed_files"], data
PY

run_wrapper_case runtime_target_id_resolution "$TMP_DIR/ts-bf001-runtime-target-id-resolution.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-runtime-target-id-resolution.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "verification_failed", data
assert "test/cli/test/worker-retry-telemetry.test.ts" in data["changed_files"], data
PY

run_wrapper_case generic_retry_title_target "$TMP_DIR/ts-bf001-generic-retry-title-target.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-generic-retry-title-target.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "test/cli/test/worker-retry-telemetry.test.ts" in data["changed_files"], data
PY

run_wrapper_case generic_retry_title_target_destructured "$TMP_DIR/ts-bf001-generic-retry-title-target-destructured.json"
python3 - <<'PY' "$TMP_DIR/ts-bf001-generic-retry-title-target-destructured.json"
import json
import sys
data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert "test/cli/test/worker-retry-telemetry.test.ts" in data["changed_files"], data
PY

after_hash="$(hash_corpora)"
if [[ "$before_hash" != "$after_hash" ]]; then
  echo "benchmark corpus was mutated" >&2
  exit 1
fi

echo "PASS: TS-BF-001 missing focused regression and broad retry assertions classify incomplete_patch"
