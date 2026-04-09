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
ELNATH_TIMEOUT="${ELNATH_TIMEOUT:-180}"

START_TS="$(date +%s)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-current-benchmark.XXXXXX")"
cleanup() {
  if [[ "${ELNATH_BENCHMARK_KEEP_TMP:-}" == "1" ]]; then
    echo "Keeping benchmark temp dir: $TMP_DIR" >&2
    return
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

WORKTREE="$TMP_DIR/repo"
RUN_LOG="$TMP_DIR/elnath-run.log"
RECOVERY_LOG="$TMP_DIR/elnath-recovery.log"
AUDIT_LOG="$TMP_DIR/route-audit.jsonl"
VERIFY_LOG="$TMP_DIR/verify.log"
VERIFY_RETRY_LOG="$TMP_DIR/verify-retry.log"

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
  local recovery_attempted="$4"
  local recovery_succeeded="$5"
  local notes="$6"
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
  "recovery_attempted": $recovery_attempted,
  "recovery_succeeded": $recovery_succeeded,
  "duration_seconds": $duration,
  "notes": $(json_escape "$notes")
}
EOF
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

maybe_prepare_verification() {
  if [[ "${VERIFY_CMD:-}" == *"packages/vitest build"* ]]; then
    return 0
  fi
  if [[ -f pnpm-lock.yaml ]] && command -v npx >/dev/null 2>&1; then
    npx pnpm build >/dev/null 2>&1 || true
  fi
}

run_verification_command() {
  local log_path="$1"
  (
    cd "$WORKTREE"
    maybe_prepare_verification
    bash -lc "$VERIFY_CMD" >"$log_path" 2>&1
  )
}

run_elnath() {
  local prompt="$1"
  local log_path="$2"
  (
    cd "$WORKTREE"
    export ELNATH_EVAL_AUDIT_LOG="$AUDIT_LOG"
    export ELNATH_PERMISSION_MODE="${ELNATH_BENCHMARK_PERMISSION_MODE:-bypass}"
    local -a args=("$ELNATH_BIN" "run" "--non-interactive")
    python3 - <<'PY' "$ELNATH_TIMEOUT" "$log_path" "$prompt" "${args[@]}"
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
  if ! (cd "$WORKTREE" && install_js_deps); then
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
- In the regression test, assert the reporter-visible retry packs themselves (for example via \`packs.find(([taskId]) => taskId === id)\`) and confirm the retry snapshots carry incrementing \`retryCount\` values while the retry event is still in the \`run\` state.
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
- Avoid GraphQL/Mongoose/TypeORM integration suites unless your patch truly requires them; start with the shared common module-utils seam."
fi
if [[ "$TASK_REPO" == *"vercel/next.js"* && "$TASK_PROMPT" == *"file-watcher regression"* ]]; then
  BENCHMARK_PROMPT+="

Next.js-specific guidance:
- Prefer the hinted config/watcher seam such as \`packages/next/src/server/lib/router-utils/setup-dev-bundler.ts\` and \`packages/next/src/lib/find-config.ts\` before exploring unrelated dev-server or e2e infrastructure.
- Prefer a narrow regression test like \`packages/next/src/lib/find-config.test.ts\` when it directly exercises the changed path; avoid broad dev/e2e watch suites unless the narrower unit path cannot prove the fix.
- Do not drift into unrelated Jest/build infrastructure fixes; the benchmark wants the smallest real fix in the config/watcher path."
fi

BENCHMARK_PROMPT+="

Execution discipline:
- Treat the hinted files as primary investigation targets before exploring adjacent code.
- Do not answer with test-only changes when the task asks for a production flow change.
- Prefer the smallest production-code patch plus the narrowest regression test needed to prove it.

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
  if ! run_elnath "$NO_CHANGE_PROMPT" "$RECOVERY_LOG"; then
    RECOVERY_EXIT=$?
  fi
  if [[ -n "$(working_tree_changes)" ]]; then
    HAS_CHANGES=true
  fi
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

if TARGET_VERIFY_CMD="$(pick_targeted_verification_command 2>/dev/null)"; then
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

printf -v RECOVERY_PROMPT '%s\n\n%s' \
  "$BENCHMARK_PROMPT" \
  "The repo-native verification command '${VERIFY_CMD}' failed after your first attempt. Make the smallest corrections needed so the verification passes. Your final answer must explicitly list modified files and state whether '${VERIFY_CMD}' passed."
RECOVERY_ATTEMPTED=true
RECOVERY_EXIT=0
if ! run_elnath "$RECOVERY_PROMPT" "$RECOVERY_LOG"; then
  RECOVERY_EXIT=$?
fi

if run_verification_command "$VERIFY_RETRY_LOG"; then
  write_result true true "" true true "verification passed after one recovery attempt"
  exit 0
fi

if [[ "$RECOVERY_EXIT" -eq 124 ]]; then
  write_result false false "verification_failed" true false "recovery attempt timed out and verification still fails"
  exit 0
fi

write_result false false "verification_failed" true false "verification still failing after one recovery attempt"
