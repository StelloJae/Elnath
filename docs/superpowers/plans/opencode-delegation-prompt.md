# Gate Retry Implementation — OpenCode Delegation

You are implementing the Elnath benchmark optimization spec. Read it first:

```
cat docs/superpowers/plans/2026-04-12-gate-retry-benchmark-optimization.md
```

## Repo Context

- Language: Go 1.25+, pure Go (no CGo), modernc.org/sqlite
- Branch: `feat/telegram-redesign` (clean, 19/19 tests pass)
- Build: `make build` or `go build ./cmd/elnath/`
- Test: `go test -race ./...` (19 packages)
- Lint: `go vet ./...`

## Execution Rules

1. **Read the spec file FIRST** before writing any code.
2. **Phase-by-phase execution**. Do NOT attempt all phases at once.
3. **Run tests after each phase**. Fix any failures before proceeding.
4. **Read each file before editing it**. Inspect existing patterns and match them.
5. **Do NOT create new packages**. All new files go in existing package directories.
6. **Do NOT change function signatures of exported types** unless the spec explicitly says to.
7. **Do NOT add dependencies**. Use only what's already in go.mod.

## Phase Execution Order

### Phase 1 (P0): Tool Descriptions + ReadTracker

1. Read `internal/tools/file_read.go`, `file_write.go`, `file_edit.go`, `bash.go`, `glob.go`, `grep.go`
2. Find each tool's Description field and replace with the spec's expanded text
3. Create `internal/tools/read_tracker.go` — ReadTracker struct with Dedup, ConsecutiveBlock, ResetDedup, RefreshPath
4. Create `internal/tools/read_tracker_test.go` — all 8+ test cases from spec
5. Integrate ReadTracker into `file_read.go` Execute() and `grep.go` Execute()
6. Integrate ReadTracker into `internal/agent/agent.go` — injection, tool name notification, compression reset
7. Integrate RefreshPath into `file_write.go` and `file_edit.go` Execute() success path
8. Run: `go test -race ./internal/tools/... ./internal/agent/...`
9. Fix any failures. All tests must pass before Phase 2.

### Phase 2 (P1): Tool Result Cap + Budget Pressure + Ack-Continuation

1. Read `internal/agent/agent.go`
2. Add `truncateToolResults()` function — 50K per tool, 200K per turn
3. Add budget pressure injection at 70% and 90% in the main loop
4. Add ack-continuation detection — isAckOnly heuristic + 2 retry max
5. Add tests for all three features
6. Run: `go test -race ./internal/agent/...`
7. Fix any failures before Phase 3.

### Phase 3 (P2): BrownfieldNode + BenchmarkMode + Routing + MaxIterations

1. Read `internal/prompt/node.go` — add BenchmarkMode and TaskLanguage to RenderState
2. Read `internal/prompt/brownfield_node.go` — replace Render() with the spec's expanded text (ant P1-P4)
3. Read each of: `wiki_rag_node.go`, `persona_node.go`, `session_summary_node.go`, `project_context_node.go` — add BenchmarkMode guard
4. Read `internal/orchestrator/router.go` — add BenchmarkMode → "single" routing
5. Read `internal/orchestrator/types.go` — add MaxIterations to WorkflowConfig
6. Read `internal/orchestrator/single.go` — wire MaxIterations
7. Read `cmd/elnath/runtime.go` — wire ELNATH_BENCHMARK_MODE, ELNATH_TASK_LANGUAGE, ELNATH_MAX_ITERATIONS env vars
8. Add tests for brownfield content, benchmark skip, routing, maxiterations
9. Run: `go test -race ./internal/prompt/... ./internal/orchestrator/... ./cmd/elnath/...`
10. Fix any failures before Phase 4.

### Phase 4 (P3): Wrapper Env Vars

1. Read `scripts/run_current_benchmark_wrapper.sh`
2. Add the three env var exports to the `run_elnath()` function
3. Run: `bash -n scripts/run_current_benchmark_wrapper.sh` (syntax check)

### Final Verification

```bash
go test -race ./...
go vet ./...
```

All 19 packages must pass. Zero vet warnings.

## Critical Reminders

- The spec has EXACT text for tool descriptions and brownfield prompts. Use those exactly, don't paraphrase.
- ReadTracker must be goroutine-safe (sync.Mutex).
- Budget pressure messages go in user role, NOT system role.
- Ack-continuation detection only fires when there are ZERO tool calls AND text matches ack patterns.
- BenchmarkMode guard is a simple `if state.BenchmarkMode { return "", nil }` at the top of Render().
- Tool result truncation happens AFTER tool execution, BEFORE appending to messages.
