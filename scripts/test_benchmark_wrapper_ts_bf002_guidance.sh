#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CURRENT_WRAPPER="$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh"
BASELINE_WRAPPER="$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-ts-bf002-guidance.XXXXXX")"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

python3 - "$CURRENT_WRAPPER" "$BASELINE_WRAPPER" "$REPO_ROOT" <<'PY'
from pathlib import Path
import sys

current = Path(sys.argv[1])
baseline = Path(sys.argv[2])
repo_root = Path(sys.argv[3])
current_text = current.read_text()
baseline_text = baseline.read_text()
current_guidance = current_text.replace("\\`", "`")

expected_cmd = "./node_modules/.bin/mocha packages/common/test/module-utils/configurable-module.builder.spec.ts --require ts-node/register --require tsconfig-paths/register --require node_modules/reflect-metadata/Reflect.js --require hooks/mocha-init-hook.ts"

if expected_cmd not in current_text:
    raise SystemExit("current wrapper lost the narrow TS-BF-002 verification command")
if expected_cmd not in baseline_text:
    raise SystemExit("baseline wrapper lost the narrow TS-BF-002 verification command")

required_current_guidance = [
    "Preserve existing public async-options fields such as `provideInjectionTokensFrom`",
    "Do not replace public option fields",
    "Do not add or replace public option fields with `onCancellation`",
    "focused cancellation tracing regression test",
    "preserve success-path behavior",
    "Avoid import-style rewrites unless verification output proves they are necessary",
    "Preserve the existing TypeScript/ESM import style",
    "do not replace the file's top-level imports with bare CommonJS `require(...)`",
    "use a minimal `createRequire(import.meta.url)` bridge",
    "keeping type imports type-only",
    "Do not finish if the semantic cancellation regression test is missing",
]
missing = [snippet for snippet in required_current_guidance if snippet not in current_guidance]
if missing:
    raise SystemExit("current wrapper missing TS-BF-002 guidance: " + ", ".join(missing))

if "Benchmark TS-BF-002 cancellation tracing guidance:" in baseline_text:
    raise SystemExit("baseline wrapper should not rewrite the baseline task prompt with TS-BF-002 guidance")

if "packages/common/module-utils/configurable-module.builder.ts" not in current_guidance:
    raise SystemExit("TS-BF-002 guidance should name the module-utils production seam")
if "packages/common/test/module-utils/configurable-module.builder.spec.ts" not in current_guidance:
    raise SystemExit("TS-BF-002 guidance should name the focused module-utils spec seam")
if "if is_ts_bf001_vitest_task || is_ts_bf002_nestjs_task; then" not in current_text:
    raise SystemExit("TS-BF-002 recovery should use the full ELNATH_TIMEOUT budget")
if "RECOVERY_TIMEOUT=$(task_recovery_timeout)" not in current_text:
    raise SystemExit("recovery paths should use task-specific recovery timeout")

no_change_prompt = current_guidance
required_no_change_recovery = [
    "packages/common/module-utils/configurable-module.builder.ts",
    "packages/common/test/module-utils/configurable-module.builder.spec.ts",
    "provideInjectionTokensFrom",
    "no_change_planning_failure",
    "Do not treat import/module-resolution churn as semantic progress",
]
missing_no_change = [snippet for snippet in required_no_change_recovery if snippet not in no_change_prompt]
if missing_no_change:
    raise SystemExit("current wrapper missing TS-BF-002 no-change recovery guidance: " + ", ".join(missing_no_change))

recovery_start = current_guidance.index("TS-BF-002 recovery guard:")
recovery_end = current_guidance.index("RECOVERY_ATTEMPTED=true", recovery_start)
recovery_block = current_guidance[recovery_start:recovery_end]
required_recovery_guidance = [
    "fix every runtime directory import used by the focused spec",
    "../../module-utils",
    "ConfigurableModuleBuilder",
    "import/module-resolution fixes are not completion",
    "If verification already passes but TS-BF-002 task-specific evidence is missing, add the missing focused cancellation regression before touching module imports",
    "Rerun the exact TS-BF-002 Mocha verification command after final import/test edits",
    expected_cmd,
    "Do not claim completion if verification still fails before semantic assertions",
]
missing_recovery = [snippet for snippet in required_recovery_guidance if snippet not in recovery_block]
if missing_recovery:
    raise SystemExit("current wrapper missing TS-BF-002 import-recovery completion guidance: " + ", ".join(missing_recovery))

required_prompt_wiring = [
    'TASK_SPECIFIC_PROMPT+="$(ts_bf002_recovery_guidance)"',
    'VERIFIED_INCOMPLETE_PROMPT+="$(ts_bf002_recovery_guidance)"',
    'RECOVERY_PROMPT+="$(ts_bf002_recovery_guidance)"',
]
missing_wiring = [snippet for snippet in required_prompt_wiring if snippet not in current_text]
if missing_wiring:
    raise SystemExit("TS-BF-002 recovery guidance is not wired into every recovery path: " + ", ".join(missing_wiring))

guard_start = current_text.index("is_ts_bf002_nestjs_task()")
guard_end = current_text.index("ts_bf002_missing_focused_regression()", guard_start)
guard_body = current_text[guard_start:guard_end]
if 'TASK_ID" == "TS-BF-002"' not in guard_body:
    raise SystemExit("TS-BF-002 guard should be keyed to TASK_ID")
if "TASK_REPO" in guard_body or "TASK_PROMPT" in guard_body:
    raise SystemExit("TS-BF-002 guard should not apply to other NestJS cancellation prompts")

for corpus in (
    "benchmarks/month3-canary-corpus.v1.json",
    "benchmarks/public-corpus.v1.json",
    "benchmarks/brownfield-primary.v1.json",
):
    path = repo_root / corpus
    if not path.exists():
        raise SystemExit(f"expected corpus file missing: {corpus}")

print("PASS: TS-BF-002 guidance preserves public options and focused regression requirements")
PY

SOURCE_REPO="$TMP_DIR/source-repo"
mkdir -p "$SOURCE_REPO"
cat >"$SOURCE_REPO/package.json" <<'EOF'
{"scripts":{"test":"node fail.js"}}
EOF
cat >"$SOURCE_REPO/fail.js" <<'EOF'
console.error("Error: Cannot find module '/repo/packages/common/module-utils/configurable-module.builder' imported from /repo/packages/common/test/module-utils/configurable-module.builder.spec.ts");
process.exit(1);
EOF
mkdir -p "$SOURCE_REPO/packages/common/module-utils/interfaces"
cat > "$SOURCE_REPO/packages/common/module-utils/interfaces/configurable-module-async-options.interface.ts" <<'TS'
export interface ConfigurableModuleAsyncOptions {
  provideInjectionTokensFrom?: unknown[];
}
TS
git -C "$SOURCE_REPO" init -q
git -C "$SOURCE_REPO" add .
git -C "$SOURCE_REPO" -c user.name='Test User' -c user.email='test@example.com' commit -qm "init"

SOURCE_REPO_WITH_ONCANCELLATION="$TMP_DIR/source-repo-with-oncancellation"
cp -R "$SOURCE_REPO" "$SOURCE_REPO_WITH_ONCANCELLATION"
mkdir -p "$SOURCE_REPO_WITH_ONCANCELLATION/packages/common/module-utils/interfaces"
cat > "$SOURCE_REPO_WITH_ONCANCELLATION/packages/common/module-utils/interfaces/configurable-module-async-options.interface.ts" <<'TS'
export interface ConfigurableModuleAsyncOptions {
  onCancellation?: (reason: unknown) => void;
  provideInjectionTokensFrom?: unknown[];
}
TS
git -C "$SOURCE_REPO_WITH_ONCANCELLATION" add .
git -C "$SOURCE_REPO_WITH_ONCANCELLATION" -c user.name='Test User' -c user.email='test@example.com' commit -qm "seed existing onCancellation"

cat >"$TMP_DIR/fake-elnath.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${FAKE_PRODUCTION_ONLY_PASS:-}" == "1" ]]; then
  mkdir -p \
    packages/common/module-utils \
    node_modules/.bin
  cat > packages/common/module-utils/configurable-module.builder.ts <<'TS'
export class ConfigurableModuleBuilder {
  traceCancellation(error: unknown) {
    if (error instanceof Error && error.name === 'AbortError') {
      return error.message;
    }
  }
}
TS
  cat > node_modules/.bin/mocha <<'SH'
#!/usr/bin/env bash
exit 0
SH
  chmod +x node_modules/.bin/mocha
  echo "I added cancellation tracing in module-utils."
  exit 0
fi
mkdir -p \
  packages/common/module-utils/interfaces \
  packages/common/test/module-utils
if [[ "${FAKE_MANY_UNTRACKED:-}" == "1" ]]; then
  mkdir -p aaa-generated
  for i in $(seq -w 1 120); do
    printf 'generated %s\n' "$i" > "aaa-generated/file-$i.txt"
  done
fi
if [[ "${FAKE_EXISTING_ONCANCELLATION:-}" == "1" ]]; then
  :
elif [[ "${FAKE_ADJACENT_REGRESSION:-}" == "1" ]]; then
  cat > packages/common/module-utils/interfaces/configurable-module-async-options.interface.ts <<'TS'
export interface ConfigurableModuleAsyncOptions {
  provideInjectionTokensFrom?: unknown[];
}
TS
elif [[ "${FAKE_PUBLIC_OPTION_CHURN:-}" == "1" ]]; then
  cat > packages/common/module-utils/interfaces/configurable-module-async-options.interface.ts <<'TS'
export interface ConfigurableModuleAsyncOptions {
  onCancellation?: (reason: unknown) => void;
  provideInjectionTokensFrom?: unknown[];
}
TS
else
  cat > packages/common/module-utils/interfaces/configurable-module-async-options.interface.ts <<'TS'
export interface ConfigurableModuleAsyncOptions {
  onCancellation?: (reason: unknown) => void;
}
TS
fi
cat > packages/common/test/module-utils/configurable-module.builder.spec.ts <<'TS'
const { expect } = require('chai');
const { ConfigurableModuleBuilder } = require('../../module-utils');
type Provider = import('../../interfaces').Provider;
TS
if [[ "${FAKE_ADJACENT_REGRESSION:-}" == "1" ]]; then
  cat > packages/common/test/module-utils/configurable-module.cancellation.spec.ts <<'TS'
describe('ConfigurableModuleBuilder cancellation tracing', () => {
  it('keeps AbortError cancellation tracing focused on async options', () => {});
});
TS
elif [[ "${FAKE_PUBLIC_OPTION_CHURN:-}" == "1" ]]; then
  cat > packages/common/test/module-utils/configurable-module.cancellation.spec.ts <<'TS'
describe('ConfigurableModuleBuilder cancellation tracing', () => {
  it('keeps AbortError cancellation tracing focused on async options', () => {});
});
TS
fi
echo "I changed module-utils imports to fix verification."
echo "Verification still needs the same command."
EOF
chmod +x "$TMP_DIR/fake-elnath.sh"

OUT="$TMP_DIR/ts-bf002-result.json"
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

ELNATH_BIN="$TMP_DIR/fake-elnath.sh" \
ELNATH_TIMEOUT=30 \
ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
HOME="$TMP_DIR/host-home" \
"$CURRENT_WRAPPER" \
  "$OUT" \
  "TS-BF-002" \
  "brownfield_feature" \
  "typescript" \
  "Extend an existing TypeScript async task flow to emit explicit cancellation tracing without changing success-path semantics." \
  "file://$SOURCE_REPO" \
  "" \
  "service_backend" \
  "month2_canary"

after_hash="$(hash_corpora)"

python3 - <<'PY' "$OUT" "$before_hash" "$after_hash"
import json
import sys

path, before_hash, after_hash = sys.argv[1:4]
data = json.load(open(path))
assert before_hash == after_hash, "benchmark corpus was mutated"
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert data["changed_files"], data
summary = data.get("trace_summary", "")
assert len(summary) <= 500, data
assert "Cannot find module '/repo" not in summary, data
PY

OUT_MANY="$TMP_DIR/ts-bf002-many-untracked-result.json"

FAKE_MANY_UNTRACKED=1 \
ELNATH_BIN="$TMP_DIR/fake-elnath.sh" \
ELNATH_TIMEOUT=30 \
ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
HOME="$TMP_DIR/host-home" \
"$CURRENT_WRAPPER" \
  "$OUT_MANY" \
  "TS-BF-002" \
  "brownfield_feature" \
  "typescript" \
  "Extend an existing TypeScript async task flow to emit explicit cancellation tracing without changing success-path semantics." \
  "file://$SOURCE_REPO" \
  "" \
  "service_backend" \
  "month2_canary"

python3 - <<'PY' "$OUT_MANY"
import json
import sys

data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "incomplete_patch", data
assert len(data["changed_files"]) <= 100, data
assert "packages/common/test/module-utils/configurable-module.builder.spec.ts" not in data["changed_files"], data
PY

OUT_ADJACENT="$TMP_DIR/ts-bf002-adjacent-result.json"

FAKE_ADJACENT_REGRESSION=1 \
ELNATH_BIN="$TMP_DIR/fake-elnath.sh" \
ELNATH_TIMEOUT=30 \
ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
HOME="$TMP_DIR/host-home" \
"$CURRENT_WRAPPER" \
  "$OUT_ADJACENT" \
  "TS-BF-002" \
  "brownfield_feature" \
  "typescript" \
  "Extend an existing TypeScript async task flow to emit explicit cancellation tracing without changing success-path semantics." \
  "file://$SOURCE_REPO" \
  "" \
  "service_backend" \
  "month2_canary"

python3 - <<'PY' "$OUT_ADJACENT"
import json
import sys

data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] == "verification_failed", data
PY

OUT_PUBLIC_CHURN="$TMP_DIR/ts-bf002-public-option-churn-result.json"

FAKE_PUBLIC_OPTION_CHURN=1 \
ELNATH_BIN="$TMP_DIR/fake-elnath.sh" \
ELNATH_TIMEOUT=30 \
ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
HOME="$TMP_DIR/host-home" \
"$CURRENT_WRAPPER" \
  "$OUT_PUBLIC_CHURN" \
  "TS-BF-002" \
  "brownfield_feature" \
  "typescript" \
  "Extend an existing TypeScript async task flow to emit explicit cancellation tracing without changing success-path semantics." \
  "file://$SOURCE_REPO" \
  "" \
  "service_backend" \
  "month2_canary"

python3 - <<'PY' "$OUT_PUBLIC_CHURN"
import json
import sys

data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["failure_family"] == "incomplete_patch", data
PY

OUT_EXISTING_ONCANCELLATION="$TMP_DIR/ts-bf002-existing-oncancellation-result.json"

FAKE_EXISTING_ONCANCELLATION=1 \
ELNATH_BIN="$TMP_DIR/fake-elnath.sh" \
ELNATH_TIMEOUT=30 \
ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
HOME="$TMP_DIR/host-home" \
"$CURRENT_WRAPPER" \
  "$OUT_EXISTING_ONCANCELLATION" \
  "TS-BF-002" \
  "brownfield_feature" \
  "typescript" \
  "Extend an existing TypeScript async task flow to emit explicit cancellation tracing without changing success-path semantics." \
  "file://$SOURCE_REPO_WITH_ONCANCELLATION" \
  "" \
  "service_backend" \
  "month2_canary"

python3 - <<'PY' "$OUT_EXISTING_ONCANCELLATION"
import json
import sys

data = json.load(open(sys.argv[1]))
assert data["failure_family"] != "incomplete_patch", data
PY

OUT_PASS_NO_REGRESSION="$TMP_DIR/ts-bf002-pass-without-regression-result.json"

FAKE_PRODUCTION_ONLY_PASS=1 \
ELNATH_BIN="$TMP_DIR/fake-elnath.sh" \
ELNATH_TIMEOUT=30 \
ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
HOME="$TMP_DIR/host-home" \
"$CURRENT_WRAPPER" \
  "$OUT_PASS_NO_REGRESSION" \
  "TS-BF-002" \
  "brownfield_feature" \
  "typescript" \
  "Extend an existing TypeScript async task flow to emit explicit cancellation tracing without changing success-path semantics." \
  "file://$SOURCE_REPO" \
  "" \
  "service_backend" \
  "month2_canary"

python3 - <<'PY' "$OUT_PASS_NO_REGRESSION"
import json
import sys

data = json.load(open(sys.argv[1]))
assert data["success"] is False, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "incomplete_patch", data
PY

echo "PASS: TS-BF-002 import-churn recovery is classified as incomplete_patch"
