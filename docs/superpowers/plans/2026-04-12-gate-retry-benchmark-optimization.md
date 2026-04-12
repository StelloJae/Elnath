# Gate Retry: Benchmark Optimization Spec v2

**Status**: READY FOR IMPLEMENTATION
**Phase**: 3.2 retry prep
**Branch**: `feat/telegram-redesign`
**Estimated scope**: ~15 files, 1-2 sessions
**Supersedes**: v1 (증상 치료 spec, 이 문서가 대체)

## Research Evidence

이 spec은 4개 코드베이스 비교 연구에 기반합니다:
- Claude Code 외부 빌드 (source map 역설계, `/Users/stello/claude-code-src/`)
- Claude Code ant 빌드 (same source, `USER_TYPE === 'ant'` 분기)
- Hermes agent (`~/.hermes/hermes-agent/`)
- claw-code Rust 포팅 (`/Users/stello/claw-code/`)

## Problem

Phase 3.2 Gate 3회 실행, 전패:
- Brownfield: 17% vs baseline 50%
- Bugfix: 44% vs baseline 100%
- Duration: 164s mean vs baseline 27s (6x)
- Go tasks: 2/12 (17%)

## Root Cause Analysis (evidence-based)

| # | 근본 원인 | 증거 | 영향도 |
|---|----------|------|--------|
| RC1 | Tool description에 행동 유도가 전혀 없음 | Elnath: "Read the contents of a file." / Claude Code: 150+ words with "read only relevant portions", size caps, steering | **Critical** |
| RC2 | Read-dedup / loop blocking 부재 | Hermes: mtime dedup + 4회 하드 블록. Elnath: 없음. 같은 파일 무한 재읽기 | **Critical** |
| RC3 | Tool result size cap 부재 | Claude Code: 50K/tool, 200K/msg. Elnath: 무제한. Context 폭증 | **High** |
| RC4 | Budget pressure 부재 | Hermes: 70%/90% 소진 시 메시지 주입. Elnath: 모델이 남은 budget을 모름 | **High** |
| RC5 | 시스템 프롬프트에 코딩 품질 제약 부재 | Claude Code ant: false-claims, verification, 25-word limit. Elnath: generic guidance만 | **High** |
| RC6 | BenchmarkMode에서 불필요한 노드 로드 | WikiRAG, Persona, SessionSummary가 벤치마크에 무의미한 토큰 소비 | **Medium** |
| RC7 | Routing이 ralph/team으로 빠짐 | ExistingCode+VerificationHint → ralph의 5-attempt retry overhead | **Medium** |

## Implementation Plan

우선순위 순. 각 FD는 독립적이므로 순서 변경 가능하지만, P0를 먼저 구현.

---

### P0-1: Tool Description 전면 재작성

**파일**: `internal/tools/*.go` (각 tool의 Description/Schema 반환 메서드)

현재 Elnath의 tool description은 기능 한 줄입니다. Claude Code와 Hermes의 패턴을 참고해서 **행동 유도를 description에 직접 삽입**합니다.

#### read_file

현재: `"Read the contents of a file with optional line range."`

변경:
```
Read a file from the local filesystem. Use this instead of cat/head/tail via bash.

Usage:
- Read up to 2000 lines by default. For files over 500 lines, use offset and limit to read in chunks.
- When you already know which part you need, only read that part — do not read entire large files.
- Results include line numbers (cat -n format).
- This tool can read images and PDFs.
```

#### edit_file

현재: `"Replace an exact string in a file with new content."`

변경:
```
Replace an exact string in a file with new content.

Usage:
- You MUST read the file with read_file before editing. This tool will fail if you haven't read the file first.
- The old_string must be unique in the file. Provide more surrounding context if needed.
- Use the smallest old_string that's clearly unique — usually 2-4 adjacent lines is sufficient. Avoid including 10+ lines of context when less uniquely identifies the target.
- Prefer editing existing files over creating new ones.
- Do not add comments, docstrings, or type annotations to code you didn't change.
```

#### write_file

현재: `"Create or overwrite a file with the given content."`

변경:
```
Create or overwrite a file. Use read_file first if the file already exists.

Usage:
- Prefer edit_file for modifying existing files — it only sends the diff.
- Do not create files unless absolutely necessary for the task.
```

#### bash

현재: `"Execute a shell command in the working directory."`

변경:
```
Execute a shell command in the working directory.

IMPORTANT: Do NOT use bash for tasks that have a dedicated tool:
- File search: use glob (not find or ls)
- Content search: use grep (not grep or rg)
- Read files: use read_file (not cat/head/tail)
- Edit files: use edit_file (not sed/awk)
- Write files: use write_file (not echo/cat heredoc)

Using dedicated tools is faster and lets the user review your work more easily.
```

#### glob

현재: `"List files matching a glob pattern."`

변경:
```
Fast file pattern matching. Use this instead of find or ls via bash.
Supports patterns like "**/*.go" or "src/**/*.ts".
Returns matching file paths sorted by modification time.
```

#### grep

현재: `"Search for a regex pattern in files."`

변경:
```
Search file contents with a regex pattern. Use this instead of grep or rg via bash.
Supports full regex syntax. Filter by file type or glob pattern.
```

**구현 위치**: 각 tool의 `Schema()` 또는 `Description()` 메서드. 정확한 위치는 `internal/tools/` 내 각 파일에서 `Description` 필드를 반환하는 곳.

**테스트**: `internal/tools/schema_test.go` 에 각 tool description이 핵심 키워드를 포함하는지 검증 (예: read_file description에 "read_file before editing" 포함 여부는 아니고, edit_file에 "MUST read" 포함 여부).

---

### P0-2: Read-Dedup + Consecutive Loop Blocking

**신규 파일**: `internal/tools/read_tracker.go`
**수정 파일**: `internal/tools/file_read.go`, `internal/tools/grep.go`, `internal/agent/agent.go`

Hermes의 패턴을 Go로 포팅합니다. **read_file뿐 아니라 grep(search)에도 동일 패턴 적용** (Hermes의 search_files loop blocking, file_tools.py:670-706).

#### ReadTracker 구조체

```go
type ReadTracker struct {
    mu          sync.Mutex
    seen        map[readKey]readEntry  // (path, offset, limit) → mtime
    consecutive map[readKey]int        // 연속 같은 읽기 횟수
    lastReadKey *readKey               // 마지막 read_file 호출의 key
}

type readKey struct {
    Tool   string // "read" or "grep"
    Path   string
    Offset int
    Limit  int
    Query  string // grep pattern (empty for read)
}

type readEntry struct {
    ModTime time.Time
    Hash    uint64  // 선택: content hash for extra safety
}
```

#### 동작

1. **Dedup**: `read_file` 또는 `grep` 호출 시 `(tool, path, offset, limit, query)`를 key로 mtime 확인. 파일이 변경되지 않았으면:
   - 전체 내용 대신 `"[File unchanged since last read at line X-Y. Use edit_file to make changes, or read a different section.]"` 반환
   - 토큰 절약 + 모델에게 다음 행동 유도

2. **Consecutive block**: 같은 key로 연속 호출 시 카운터 증가.
   - 3회: 경고 메시지 append: `"[WARNING: You have read this exact file region 3 times. Consider making your edit or reading a different file.]"`
   - 4회: 하드 블록: `"[BLOCKED: You have read this exact region 4 times consecutively. The content has not changed. Proceed with editing or move to a different file.]"`

3. **리셋**: read_file/grep/glob 이외의 tool 호출 시 consecutive 카운터 리셋 (Hermes 패턴).

#### 통합

- `ReadTracker`는 `agent.Agent` 생성 시 주입, session 수명으로 관리
- `read_file` tool의 `Execute()` 시작부에서 tracker 체크 (tool="read")
- `grep` tool의 `Execute()` 시작부에서 tracker 체크 (tool="grep", query=pattern)
- Agent의 `executeTools()` 에서 tool 실행 후 tracker에 tool name 알림 (consecutive 리셋용)

**테스트**:
- `TestReadTrackerDedup` — 같은 파일 두 번 읽기 시 stub 반환
- `TestReadTrackerConsecutiveBlock` — 4회 연속 시 하드 블록
- `TestReadTrackerResetOnOtherTool` — edit_file 호출 후 카운터 리셋
- `TestReadTrackerAllowsAfterModification` — 파일 수정 후 재읽기 허용 (mtime 변경)
- `TestReadTrackerGrepDedup` — 같은 grep pattern+path 두 번 시 stub 반환
- `TestReadTrackerGrepConsecutiveBlock` — grep 4회 연속 하드 블록

---

### P0-3: Dedup Reset on Context Compression

**수정 파일**: `internal/tools/read_tracker.go`, `internal/agent/agent.go`

Hermes 패턴 (file_tools.py:483-503): context compression이 발생하면 원본 tool result가 요약으로 대체되므로, dedup 캐시를 클리어해야 합니다. 그렇지 않으면 모델이 압축 후 파일을 다시 읽으려 할 때 "[File unchanged]" stub을 받게 되어 실제 내용을 복구할 수 없습니다.

#### 동작

- `ReadTracker.ResetDedup()` 메서드 추가 — `seen` map 전체 클리어, `consecutive` 카운터 유지
- Agent의 context compression 로직 (`compressContext()` 또는 동등) 실행 직후 `tracker.ResetDedup()` 호출

**테스트**:
- `TestReadTrackerResetDedup` — ResetDedup 후 같은 파일 재읽기가 full content 반환

---

### P0-4: Post-Write Timestamp Refresh

**수정 파일**: `internal/tools/read_tracker.go`, `internal/tools/file_write.go`, `internal/tools/file_edit.go`

Hermes 패턴 (J4): 모델이 write_file/edit_file로 파일을 수정한 직후, ReadTracker의 해당 파일 mtime을 갱신합니다. 이렇게 하지 않으면 자기가 방금 쓴 파일을 읽을 때 "unchanged" stub이 반환되는 false staleness가 발생합니다.

#### 동작

- `ReadTracker.RefreshPath(path string)` 메서드 추가 — 해당 path의 모든 entry를 `seen`에서 삭제 (다음 read가 full content 반환하도록)
- `write_file`과 `edit_file`의 `Execute()` 성공 후 `tracker.RefreshPath(path)` 호출

**테스트**:
- `TestReadTrackerRefreshAfterWrite` — read → write → read 시 두 번째 read가 full content 반환
- `TestReadTrackerRefreshAfterEdit` — read → edit → read 시 두 번째 read가 full content 반환

---

### P1-1: Tool Result Size Cap

**수정 파일**: `internal/agent/agent.go`

#### 동작

Tool result가 반환될 때:
- 단일 tool result > **50,000 chars**: 처음 2000 chars + `"\n\n[Output truncated. %d total characters. Read specific sections with offset/limit for details.]"` 로 교체
- 한 turn의 전체 tool results 합계 > **200,000 chars**: 가장 큰 result부터 truncation 적용

#### 구현

`executeTools()` 반환 후, `messages`에 append하기 전에 `truncateToolResults(results []llm.Message, perToolLimit, totalLimit int)` 함수 적용.

**테스트**:
- `TestToolResultTruncation` — 60K result → 2K preview + notice
- `TestToolResultTotalCap` — 3개 tool × 80K = 240K → 200K 이하로 truncation

---

### P1-2: Budget Pressure Injection

**수정 파일**: `internal/agent/agent.go`

#### 동작

Agent loop의 각 iteration 시작 시, 남은 iteration budget을 확인:
- **70% 소진** (예: 50 중 35): user role 메시지 주입:
  `"[BUDGET: Iteration 35/50. 15 iterations remaining. Start consolidating your work and prepare your final answer.]"`
- **90% 소진** (예: 50 중 45): user role 메시지 주입:
  `"[BUDGET WARNING: Only 5 iterations remaining. Provide your final response NOW. Do not start new explorations.]"`

#### 구현

Main loop (`for iter := 0; iter < a.maxIterations; iter++`) 안에서, API call 직전에:
```go
if pct := float64(iter) / float64(a.maxIterations); pct >= 0.9 {
    messages = append(messages, llm.NewUserMessage(fmt.Sprintf(
        "[BUDGET WARNING: Only %d iterations remaining. Provide your final response NOW.]",
        a.maxIterations-iter)))
} else if pct >= 0.7 {
    messages = append(messages, llm.NewUserMessage(fmt.Sprintf(
        "[BUDGET: Iteration %d/%d. %d remaining. Start consolidating your work.]",
        iter, a.maxIterations, a.maxIterations-iter)))
}
```

**테스트**:
- `TestBudgetPressureAt70Percent` — 70% 시점에 consolidation 메시지 존재 확인
- `TestBudgetPressureAt90Percent` — 90% 시점에 WARNING 메시지 존재 확인
- `TestNoBudgetPressureBelow70` — 69% 이하에서는 주입 없음

---

### P1-3: Ack-Continuation Detection

**수정 파일**: `internal/agent/agent.go`

Hermes 패턴 (run_agent.py:1564-1633): 모델이 tool call 없이 "I'll look into...", "Let me check..." 같은 계획만 말하는 응답을 하면, 실제 실행을 강제합니다.

#### 동작

Main loop에서 LLM 응답 처리 시:
- tool call이 없고 (`len(response.ToolCalls) == 0`)
- stop reason이 `end_turn`이고
- 응답 텍스트가 ack 패턴과 매치되면 (아래 heuristic)
- user role 메시지 주입: `"[System: Continue now. Execute the required tool calls. Do not describe what you plan to do — do it.]"`
- 최대 2회 재시도. 3회째는 그냥 종료.

#### Ack Heuristic

```go
var ackPatterns = []string{
    "I'll ", "I will ", "Let me ", "I'm going to ",
    "I need to ", "First, I'll ", "I should ",
}

func isAckOnly(text string) bool {
    text = strings.TrimSpace(text)
    if len(text) > 500 { return false }  // 긴 응답은 실제 답변일 수 있음
    for _, p := range ackPatterns {
        if strings.HasPrefix(text, p) { return true }
    }
    return false
}
```

**테스트**:
- `TestAckContinuationDetected` — "I'll look into the file" → 강제 continue 메시지 주입
- `TestAckContinuationMaxRetries` — 3회 연속 ack → 종료 (무한 루프 방지)
- `TestLongResponseNotAck` — 500자 이상 응답은 ack로 판정 안 함

---

### P2-1: System Prompt 코딩 품질 제약 강화

**수정 파일**: `internal/prompt/brownfield_node.go`, `internal/prompt/node.go`

Claude Code ant 빌드의 좋은 아이디어를 Elnath 자체 구현으로 가져옵니다. 이건 복사가 아니라, 외부 Claude Code에도 없는 기능을 Elnath에 넣어서 surpass하는 것입니다.

#### RenderState 확장

`node.go`의 `RenderState`에 추가:
```go
BenchmarkMode bool
TaskLanguage  string  // "go", "typescript", ""
```

#### BrownfieldNode 강화

`brownfield_node.go`의 `Render()` 출력을 다음으로 교체:

```
# Execution Discipline

## Core
- Make the smallest correct change. Do not refactor, add features, or improve code beyond what was asked.
- Read the file before editing. Inspect existing patterns and reuse them.
- Run the repo test suite before finishing. All existing tests MUST still pass.
- Keep text between tool calls brief (under 30 words). Lead with the action, not the reasoning.

## Verification (ant P2)
- Before reporting a task complete, verify it actually works: run the test, execute the script, check the output.
- If you can't verify (no test exists, can't run the code), say so explicitly rather than claiming success.

## Accuracy (ant P4 — bidirectional)
- Report outcomes faithfully. If tests fail, say so with the relevant output. If you did not run a verification step, say that rather than implying it succeeded.
- Never claim "all tests pass" when output shows failures. Never suppress or simplify failing checks to manufacture a green result. Never characterize incomplete or broken work as done.
- Equally, when a check did pass or a task is complete, state it plainly. Do not hedge confirmed results with unnecessary disclaimers or re-verify things you already checked. The goal is an accurate report, not a defensive one.

## Comments (ant P1)
- Default to writing no comments. Only add one when the WHY is non-obvious: a hidden constraint, a subtle invariant, a workaround for a specific bug.
- Don't explain WHAT the code does — well-named identifiers already do that. Don't reference the current task or callers.

## Collaboration (ant P3)
- If you notice the request is based on a misconception, or spot a bug adjacent to what was asked, say so.
```

TaskLanguage == "go" 일 때 추가:
```
Go-specific:
- Run `go test ./...` FIRST to establish baseline before any edit.
- Preserve existing API surface — do not rename exported types or functions.
- Prefer the smallest diff: one function change > new file.
- Use existing error handling patterns from adjacent code.
```

TaskLanguage == "typescript" 일 때 추가:
```
TypeScript-specific:
- Check existing test command (npm test / pnpm test) FIRST.
- Follow existing import style (relative vs alias).
- Do not add new dependencies unless the task explicitly requires them.
```

**테스트**:
- `TestBrownfieldNodeContainsVerificationDiscipline` — "Report outcomes faithfully" 포함
- `TestBrownfieldNodeGoGuidance` — TaskLanguage="go" 시 "go test" 포함
- `TestBrownfieldNodeTSGuidance` — TaskLanguage="typescript" 시 "npm test" 포함
- `TestBrownfieldNodeBenchmarkSkipsWikiNodes` — BenchmarkMode=true 시 WikiRAG 등 빈 문자열

---

### P2-2: BenchmarkMode Prompt Skip + Routing

**수정 파일**: `internal/prompt/wiki_rag_node.go`, `internal/prompt/persona_node.go`, `internal/prompt/session_summary_node.go`, `internal/prompt/project_context_node.go`, `internal/orchestrator/router.go`, `cmd/elnath/runtime.go`

(v1 spec의 FD1-2, FD4, FD6 통합)

- 각 노드의 `Render()` 시작부: `if state.BenchmarkMode { return "", nil }`
- `RoutingContext.BenchmarkMode bool` 추가, `routeName()`에서 true면 항상 `"single"` 반환
- `runtime.go`에서 `ELNATH_BENCHMARK_MODE=1` → `BenchmarkMode=true` wiring
- `ELNATH_TASK_LANGUAGE` → `RenderState.TaskLanguage` wiring

---

### P2-3: MaxIterations 환경변수 제어

**수정 파일**: `internal/orchestrator/types.go`, `internal/orchestrator/single.go`, `cmd/elnath/runtime.go`

- `WorkflowConfig.MaxIterations int` 추가
- `single.go`의 `Run()`에서 `MaxIterations > 0`이면 `agent.WithMaxIterations(cfg.MaxIterations)` 적용
- `runtime.go`에서 `ELNATH_MAX_ITERATIONS` env → `WorkflowConfig.MaxIterations` 파싱

---

### P3-1: Wrapper 환경변수 주입

**수정 파일**: `scripts/run_current_benchmark_wrapper.sh`

`run_elnath()` 함수 내 export:
```bash
export ELNATH_BENCHMARK_MODE=1
export ELNATH_MAX_ITERATIONS=20
export ELNATH_TASK_LANGUAGE="$TASK_LANGUAGE"
```

---

## File Change Summary

| 우선순위 | 파일 | 변경 |
|---------|------|------|
| P0-1 | `internal/tools/file_read.go` | Description 재작성 |
| P0-1 | `internal/tools/file_write.go` | Description 재작성 |
| P0-1 | `internal/tools/file_edit.go` | Description 재작성 + "smallest old_string" 힌트 |
| P0-1 | `internal/tools/bash.go` | Description 재작성 |
| P0-1 | `internal/tools/glob.go` | Description 재작성 |
| P0-1 | `internal/tools/grep.go` | Description 재작성 |
| P0-2 | `internal/tools/read_tracker.go` | **신규** — ReadTracker dedup + consecutive block (read + grep) |
| P0-2 | `internal/tools/read_tracker_test.go` | **신규** — 6 test cases (read 4 + grep 2) |
| P0-2 | `internal/tools/file_read.go` | ReadTracker 통합 |
| P0-2 | `internal/tools/grep.go` | ReadTracker 통합 (search loop blocking) |
| P0-2 | `internal/agent/agent.go` | ReadTracker 주입 + tool 실행 후 tracker 알림 |
| P0-3 | `internal/tools/read_tracker.go` | +ResetDedup() 메서드 |
| P0-3 | `internal/agent/agent.go` | compression 후 tracker.ResetDedup() 호출 |
| P0-4 | `internal/tools/read_tracker.go` | +RefreshPath() 메서드 |
| P0-4 | `internal/tools/file_write.go` | 성공 후 tracker.RefreshPath() |
| P0-4 | `internal/tools/file_edit.go` | 성공 후 tracker.RefreshPath() |
| P1-1 | `internal/agent/agent.go` | truncateToolResults 함수 + 통합 |
| P1-2 | `internal/agent/agent.go` | Budget pressure injection |
| P1-3 | `internal/agent/agent.go` | Ack-continuation detection + 2회 retry |
| P2-1 | `internal/prompt/node.go` | +BenchmarkMode, +TaskLanguage |
| P2-1 | `internal/prompt/brownfield_node.go` | 전면 재작성 (ant P1-P4 반영) |
| P2-2 | `internal/prompt/wiki_rag_node.go` | BenchmarkMode guard |
| P2-2 | `internal/prompt/persona_node.go` | BenchmarkMode guard |
| P2-2 | `internal/prompt/session_summary_node.go` | BenchmarkMode guard |
| P2-2 | `internal/prompt/project_context_node.go` | BenchmarkMode guard |
| P2-2 | `internal/orchestrator/router.go` | +BenchmarkMode → single |
| P2-3 | `internal/orchestrator/types.go` | +MaxIterations |
| P2-3 | `internal/orchestrator/single.go` | Wire MaxIterations |
| P2-3 | `cmd/elnath/runtime.go` | Env var wiring |
| P3-1 | `scripts/run_current_benchmark_wrapper.sh` | Export env vars |
| Tests | 각 패키지 test 파일 | 신규 test cases |

Production ~22 files + test ~6 files.

## Acceptance Criteria

1. `go test -race ./...` 19/19 패키지 통과
2. `go vet ./...` 깨끗
3. Tool description에 행동 유도 포함 (unit test) — edit_file에 "smallest old_string" 포함
4. ReadTracker: read dedup + grep dedup + 4회 하드 블록 동작 (unit test)
5. ReadTracker: ResetDedup 후 full content 재반환 (unit test)
6. ReadTracker: RefreshPath 후 write/edit된 파일 재읽기 허용 (unit test)
7. Tool result 50K cap 적용 (unit test)
8. Budget pressure 70%/90% 주입 (unit test)
9. Ack-continuation: tool 없는 ack 응답 → 강제 continue, 3회 후 종료 (unit test)
10. BrownfieldNode에 ant P1-P4 (accuracy 양방향, comment, collaboration, verification) 포함 (unit test)
11. BrownfieldNode에 Go/TS 언어별 guidance 포함 (unit test)
12. BenchmarkMode=true → WikiRAG/Persona/SessionSummary/ProjectContext 미렌더링 (unit test)
13. BenchmarkMode=true → routing 항상 single (unit test)

## NOT in scope

- Corpus 확장 (7→25 tasks) — 별도 세션, Gate retry 후 통계 안정성 개선
- Streaming parallel tool execution — 아키텍처 변경 규모가 크고 LBB3가 이미 read 병렬화 기반을 깔아둠. 향후 세션.
- Context compaction 고도화 — Hermes의 2-tier 방식 도입은 별도 세션
- read_file의 read-before-edit 강제 (edit_file에서 직전 read 여부 체크) — P0-2의 ReadTracker가 간접적으로 커버하지만, tool-level hard enforcement는 별도
- Tool argument type coercion, dynamic schema patching, deterministic call IDs — P1 후보였으나 벤치마크 성능 대비 구현 비용 높음. 별도 세션.
- Structured error classifier (13 categories), orphaned tool result sanitizer — P2 후보. 에러 복구보다 탐색 효율이 현재 병목.

## Implementation Order (OpenCode delegation용)

```
Phase 1 (P0): Tool descriptions + ReadTracker (read+grep dedup, loop block, ResetDedup, RefreshPath)
  Files: internal/tools/*.go, internal/agent/agent.go
  → go test -race ./internal/tools/... ./internal/agent/...

Phase 2 (P1): Tool result cap + Budget pressure + Ack-continuation detection
  Files: internal/agent/agent.go
  → go test -race ./internal/agent/...

Phase 3 (P2): BrownfieldNode (ant P1-P4) + BenchmarkMode + Routing + MaxIterations
  Files: internal/prompt/*.go, internal/orchestrator/*.go, cmd/elnath/runtime.go
  → go test -race ./internal/prompt/... ./internal/orchestrator/... ./cmd/elnath/...

Phase 4 (P3): Wrapper env vars
  Files: scripts/run_current_benchmark_wrapper.sh
  → bash -n scripts/run_current_benchmark_wrapper.sh

Final: go test -race ./... (19/19), go vet ./...
```
