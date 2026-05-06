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
git -C "$SOURCE_REPO" init -q
git -C "$SOURCE_REPO" add .
git -C "$SOURCE_REPO" -c user.name='Test User' -c user.email='test@example.com' commit -qm "init"

cat >"$TMP_DIR/fake-elnath.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
mkdir -p \
  packages/common/module-utils/interfaces \
  packages/common/test/module-utils
if [[ "${FAKE_MANY_UNTRACKED:-}" == "1" ]]; then
  mkdir -p aaa-generated
  for i in $(seq -w 1 120); do
    printf 'generated %s\n' "$i" > "aaa-generated/file-$i.txt"
  done
fi
if [[ "${FAKE_ADJACENT_REGRESSION:-}" == "1" ]]; then
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

echo "PASS: TS-BF-002 import-churn recovery is classified as incomplete_patch"
