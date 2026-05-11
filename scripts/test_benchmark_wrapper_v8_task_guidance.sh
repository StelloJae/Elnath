#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

python3 - <<'PY' \
  "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" \
  "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh" \
  "$REPO_ROOT/benchmarks/public-corpus-v8-25.v1.json"
import json
import sys
from pathlib import Path

current = Path(sys.argv[1]).read_text().replace("\\`", "`")
baseline = Path(sys.argv[2]).read_text().replace("\\`", "`")
corpus = json.loads(Path(sys.argv[3]).read_text())

required_current = [
    "V8-JS-BUG-001 express mounted-app guidance:",
    "mounted child app calling `next('router')`",
    "Do not add a new `app.handle(..., err)` API",
    "real `next(err)` errors should still propagate normally",
    "V8-MIX-BUG-001 actions/toolkit command guidance:",
    "`packages/core/src/command.ts`",
    "`packages/core/__tests__/command.test.ts`",
    "Do not modify root `jest.config.js`",
    "npm test -- packages/core/__tests__/command.test.ts",
    "is_v8_mix_bug001_actions_toolkit_task",
    "V8-ADD-JS-001 yargs option parsing guidance:",
    "`lib/command.ts`",
    "`test/integration.mjs`",
    "`positionalMap`, `unparsed`, and `unknown-options-as-args`",
    "unparsed.push(`--${key}=${value}`)",
    "Do not edit `test/integration.mjs`",
    "test/fixtures/opt-assignment-and-positional-command-arg.js",
    "cmd exited with code 1",
    "changed only runtime rewrite/formatting surfaces",
    "npm run fix",
    "ensure the posttest check passes",
    "is_v8_add_js001_yargs_task",
    "V8-DEF-TS-003 MSW handler guidance:",
    "`src/core/utils/executeHandlers.ts`",
    "`src/core/handlers/RequestHandler.ts`",
    "Do not clone the request for every handler.",
    "expected 1/3/2/4 into 100+/200+/300+",
    "pnpm exec vitest src/core/utils/handleRequest.test.ts",
    "test/node/regressions/many-request-handlers-jsdom.test.ts",
    "Do not substitute full `pnpm test`",
    "is_v8_def_ts003_msw_task",
    'allowed_suffixes = (".go", ".ts", ".tsx", ".js", ".jsx", ".py")',
    "V8-PY-TH-001 pytest approx guidance:",
    "`src/_pytest/python_api.py`",
    "`testing/python/approx.py`",
    "Support `datetime.datetime` and `datetime.timedelta`",
    "Do not stop after production-only changes.",
    "datetime within tolerance, datetime outside tolerance, timedelta comparisons",
    "`pytest.raises(TypeError)` for unsupported `rel` / `nan_ok` arguments",
    "In `ApproxScalar.tolerance`, handle explicit `datetime.timedelta` absolute tolerance before numeric `< 0` tolerance checks",
    "comparison against a distinct actual datetime/timedelta value",
    "In no-change recovery, stop re-reading once `ApproxScalar`, the `approx()` factory, and the nearby `TestApprox` tests are identified",
    "is_v8_py_th001_pytest_task",
    "abs=timedelta(seconds=2)",
    "abs=datetime.timedelta(seconds=2)",
    "V8-GO-BUG-004 fsnotify inotify guidance:",
    "`backend_inotify.go`",
    "`IN_MOVE_SELF`, `IN_DELETE_SELF`",
    "Do not run `go test ./...` before a diff exists",
    "direct watch bookkeeping for self-move/self-delete events",
    "backend_inotify.go` and `backend_inotify_test.go` before any more exploration",
    "is_v8_go_bug004_fsnotify_task",
    "task_max_iterations",
    "printf '%s\\n' 28",
    "V8-GO-BUG-004 lacks the required fsnotify inotify behavior diff plus focused backend_inotify_test.go regression",
    "V8-ALT-MIX-001 semantic-release channel guidance:",
    "`lib/get-release-to-add.js`",
    "`test/get-release-to-add.test.js`",
    "Reuse the existing `isSameChannel` helper",
    "raw `includes(branch.channel || null)`",
    "channels: [false]",
    "is_v8_alt_mix001_semantic_release_task",
    "V8-ALT-MIX-001 lacks the required channel comparison diff plus focused release-add regression",
    "V8-PY-BF-001 Flask context guidance:",
    "`src/flask/app.py` around `_got_first_request`",
    "`tests/test_basic.py`",
    "Do not stop after a helper-only refactor",
    "is_v8_py_bf001_flask_context_task",
    "V8-PY-BF-001 lacks the required Flask context behavior diff plus focused tests/test_basic.py regression",
    "V8-GO-BF-004 GORM context cancellation guidance:",
    "`callbacks/query.go`",
    "`tests/query_test.go`",
    "db.Statement.Context.Err()",
    "errors.Is(err, context.Canceled)",
    "logger/slog_test.go",
    "is_v8_go_bf004_gorm_context_task",
    "undici `#3736` leaked error event on response body",
    "util.destroy(this.res.on('error', noop), this.reason)",
    "async (t) =>",
    "Do not add `res.once('end', this.removeAbortListener)`",
    "node --test test/client-request.js",
    "V8-GO-BUG-003 cobra command error guidance:",
    "`Command.Traverse` around the `ParseFlags(flags)` error path",
    "c.FlagErrorFunc()(c, err)",
    "is_v8_go_bug003_cobra_task",
    "V8-PY-BUG-001 requests option propagation guidance:",
    "`Session.merge_environment_settings`",
    "`Session.verify = False` is not overwritten by `REQUESTS_CA_BUNDLE`",
    "is_v8_py_bug001_requests_task",
    "No-change recovery discipline:",
    "If you are about to say you will apply a patch, apply it now.",
    "Keep the existing production diff intact initially",
    "patch 'src/_pytest/python_api.py' narrowly in 'ApproxScalar.tolerance'",
    "Make rel/nan_ok tests compare against a distinct actual value",
    "finish only if both 'src/_pytest/python_api.py' and 'testing/python/approx.py' are changed",
    "V8-PY-TH-001 lacks the required pytest approx behavior diff plus focused datetime/timedelta regression coverage pair",
]
missing_current = [snippet for snippet in required_current if snippet not in current]
if missing_current:
    raise SystemExit("current wrapper missing v8 task guidance: " + ", ".join(missing_current))

for forbidden in [
    "V8-JS-BUG-001 express mounted-app guidance:",
    "V8-PY-TH-001 pytest approx guidance:",
    "V8-GO-BUG-004 fsnotify inotify guidance:",
    "V8-GO-BUG-003 cobra command error guidance:",
    "V8-PY-BUG-001 requests option propagation guidance:",
    "V8-ALT-MIX-001 semantic-release channel guidance:",
    "V8-PY-BF-001 Flask context guidance:",
]:
    if forbidden in baseline:
        raise SystemExit("baseline wrapper should not receive current-side guidance: " + forbidden)

tasks = {task["id"]: task for task in corpus["tasks"]}
js_prompt = tasks["V8-JS-BUG-001"]["prompt"]
py_task = tasks["V8-PY-TH-001"]
mix_bug_task = tasks["V8-MIX-BUG-001"]

if "V8-GO-BF-003" in tasks:
    raise SystemExit("V8-GO-BF-003 should be removed from the repaired v8 corpus")

if "next('router')" not in js_prompt:
    raise SystemExit("V8-JS-BUG-001 corpus prompt must name next('router')")
if "parent router continues to the next matching middleware" not in js_prompt:
    raise SystemExit("V8-JS-BUG-001 corpus prompt must name parent router fallthrough")
if "real error" not in js_prompt:
    raise SystemExit("V8-JS-BUG-001 corpus prompt must distinguish sentinel from real errors")
if "datetime and timedelta support to pytest.approx" not in py_task["prompt"]:
    raise SystemExit("V8-PY-TH-001 corpus prompt must name pytest.approx datetime/timedelta support")
if py_task["repo_ref"] != "84ae27e4710af45cc307f8c0c25259e917090219":
    raise SystemExit("V8-PY-TH-001 must pin the pre-datetime-support pytest parent commit")
if py_task["verification_command"] != "python -m pytest -o minversion=0 testing/python/approx.py -q":
    raise SystemExit("V8-PY-TH-001 must use the focused approx verification command")
if mix_bug_task["verification_command"] != "npm test -- packages/core/__tests__/command.test.ts":
    raise SystemExit("V8-MIX-BUG-001 must use the focused core command test")
PY

echo "PASS: v8 task guidance and corpus prompts stay focused"
