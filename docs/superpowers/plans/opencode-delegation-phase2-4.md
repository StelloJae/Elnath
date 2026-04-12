# Gate Retry Implementation — Phases 2-4

Read the full spec first:
```
cat docs/superpowers/plans/2026-04-12-gate-retry-benchmark-optimization.md
```

## What's Already Done (Phase 1 + partial Phase 2)

Phase 1 is COMPLETE and tested:
- All 6 tool descriptions rewritten in `internal/tools/file.go` and `internal/tools/bash.go`
- `internal/tools/read_tracker.go` — full ReadTracker (dedup, consecutive block, ResetDedup, RefreshPath)
- `internal/tools/read_tracker_test.go` — 9 test cases, all passing
- Wired into agent, executor, commands, registry

Phase 2 PRODUCTION CODE is done but TESTS ARE MISSING:
- `internal/agent/agent.go` already contains:
  - Budget pressure injection (lines ~120-130 in the main loop)
  - Ack-continuation detection with isAckOnly() (lines ~148-155)
  - truncateToolResults() function (end of file)
- You need to ADD TESTS for these three features

## Your Tasks

### Task 1: Phase 2 Tests

Add tests to `internal/agent/agent_test.go` (or a new file `internal/agent/budget_test.go`):

1. **TestBudgetPressureAt70Percent** — Create agent with maxIterations=10. Mock provider to return tool calls for 7 iterations, then no tool calls. Verify that messages contain "[BUDGET:" after iteration 7.

2. **TestBudgetPressureAt90Percent** — Same setup, 9 iterations. Verify "[BUDGET WARNING:" present.

3. **TestNoBudgetPressureBelow70** — 5 iterations. Verify no budget messages.

4. **TestAckContinuationDetected** — Mock provider returns text "I'll look into the file" with no tool calls on first call, then returns a proper response. Verify "[System: Continue now" message was injected.

5. **TestAckContinuationMaxRetries** — Mock provider returns ack text 3 times. Verify loop exits after 2 retries (total 3 ack responses).

6. **TestLongResponseNotAck** — Mock provider returns 600-char text with no tool calls. Verify no continuation injection.

7. **TestToolResultTruncation** — Create a message with a ToolResultBlock containing 60K chars. Call truncateToolResults(). Verify result is truncated to ~2K + notice.

8. **TestToolResultTotalCap** — Create a message with 3 ToolResultBlocks × 80K chars each. Call truncateToolResults(). Verify total is under 200K.

Look at existing tests in `internal/agent/agent_test.go` and `executor_test.go` for patterns on how to mock the provider. The provider interface is `llm.Provider` — check how existing tests create mock providers.

Run after: `go test -race ./internal/agent/...`

### Task 2: Phase 3 — BrownfieldNode + BenchmarkMode + Routing + MaxIterations

Read each file before editing. Follow patterns already in the code.

#### 2a. RenderState extension
Read `internal/prompt/node.go`. Add two fields to `RenderState`:
```go
BenchmarkMode bool
TaskLanguage  string
```

#### 2b. BrownfieldNode rewrite
Read `internal/prompt/brownfield_node.go`. Replace the `Render()` method's output with the EXACT text from the spec under "P2-1: System Prompt 코딩 품질 제약 강화 > BrownfieldNode 강화". The spec has the complete replacement text including:
- Core discipline section
- Verification (ant P2)
- Accuracy (ant P4 — bidirectional)
- Comments (ant P1)
- Collaboration (ant P3)
- Go-specific section (when TaskLanguage == "go")
- TypeScript-specific section (when TaskLanguage == "typescript")

#### 2c. BenchmarkMode guards
Read each of these files and add `if state.BenchmarkMode { return "", nil }` at the top of their `Render()` method:
- `internal/prompt/wiki_rag_node.go`
- `internal/prompt/persona_node.go`
- `internal/prompt/session_summary_node.go`
- `internal/prompt/project_context_node.go`

#### 2d. Routing
Read `internal/orchestrator/router.go`. Add `BenchmarkMode bool` to `RoutingContext`. In the `routeName()` function, if `BenchmarkMode` is true, return `"single"` immediately.

#### 2e. MaxIterations
Read `internal/orchestrator/types.go`. Add `MaxIterations int` to `WorkflowConfig`.
Read `internal/orchestrator/single.go`. In `Run()`, if `cfg.MaxIterations > 0`, pass `agent.WithMaxIterations(cfg.MaxIterations)`.

#### 2f. Runtime wiring
Read `cmd/elnath/runtime.go`. Wire these env vars:
- `ELNATH_BENCHMARK_MODE=1` → set BenchmarkMode=true in both RoutingContext and RenderState
- `ELNATH_TASK_LANGUAGE` → set TaskLanguage in RenderState
- `ELNATH_MAX_ITERATIONS` → parse int, set WorkflowConfig.MaxIterations

#### 2g. Phase 3 Tests
Add tests for:
- BrownfieldNode contains "Report outcomes faithfully" and "Do not hedge confirmed results"
- BrownfieldNode with TaskLanguage="go" contains "go test"
- BrownfieldNode with TaskLanguage="typescript" contains "npm test"
- BenchmarkMode=true → wiki_rag_node returns empty
- BenchmarkMode=true → routing returns "single"

Run after: `go test -race ./internal/prompt/... ./internal/orchestrator/... ./cmd/elnath/...`

### Task 3: Phase 4 — Wrapper Env Vars

Read `scripts/run_current_benchmark_wrapper.sh`. Find the `run_elnath()` function. Add these exports:
```bash
export ELNATH_BENCHMARK_MODE=1
export ELNATH_MAX_ITERATIONS=20
export ELNATH_TASK_LANGUAGE="$TASK_LANGUAGE"
```

Run after: `bash -n scripts/run_current_benchmark_wrapper.sh`

### Final Verification

```bash
go test -race ./...
go vet ./...
```

ALL 19+ packages must pass. Zero vet warnings.

## Rules
- Read each file BEFORE editing
- Match existing code patterns (constructors, test helpers, naming)
- Do NOT change exported function signatures unless the spec says to
- Do NOT add new dependencies
- Run tests after EACH task, fix failures before proceeding
