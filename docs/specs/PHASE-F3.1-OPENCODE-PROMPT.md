# Phase F-3.1 — OpenCode Prompt (Learning Extractor)

## Context

Elnath 는 Go 로 만든 자율 AI 비서 daemon (`/Users/stello/elnath/`, 브랜치 `feat/telegram-redesign`). `internal/learning/` 패키지가 agent 실행 결과에서 규칙 기반 lesson 을 뽑아 JSONL 로 저장하고, `internal/self/` 가 그 lesson 의 PersonaDelta 로 daemon 의 행동 param 을 조정한다. 이게 Elnath 자율 학습 loop 의 심장이다.

Phase F-2 에서 `SingleWorkflow` 한정으로 이 extractor 를 붙였고, Phase F-3 에서 Team/Ralph/Autopilot workflow 까지 확장하는 중이다. 이 prompt 는 그 중 **extractor 자체만** 손본다. 실제 workflow 배선은 Phase F-3.2 에서 한다.

상세 spec: `docs/specs/PHASE-F3-MULTI-WORKFLOW-LEARNING.md` §2.

## Scope (이 phase 에서 할 일)

파일 2 개만 건드린다:
- `internal/learning/agent_extractor.go` — 확장
- `internal/learning/agent_extractor_test.go` — 신규 또는 확장 (존재하지 않으면 생성)

## Task

### 1. `AgentResultInfo` 구조체에 두 필드 추가

```go
type AgentResultInfo struct {
    // ... 기존 필드 (Topic, FinishReason, Iterations, MaxIterations,
    //              OutputTokens, InputTokens, TotalCost, ToolStats) 전부 보존
    RetryCount int    // NEW
    Workflow   string // NEW
}
```

- `RetryCount`: ralph wrapper 가 `attempt - 1` 을 세팅. single/team/autopilot 호출에서는 0.
- `Workflow`: `"single" | "team" | "ralph" | "autopilot" | ""`. 빈 문자열은 기존 호출자 호환용.

### 2. Rule E 추가 (retry instability)

```go
const agentRalphRetryThreshold = 3
```

`ExtractAgent` 함수 끝쪽에 아래 규칙을 추가한다:

- 조건: `info.RetryCount >= agentRalphRetryThreshold`
- Text 형식: `"Task retried N times on TOPIC; review decomposability."` (N = RetryCount, TOPIC = info.Topic 또는 fallback "agent-task"). `truncate(..., maxLessonTextLen)` 적용.
- Topic: 기존 패턴과 동일 (trimmed info.Topic, 비었으면 "agent-task")
- Source: `sourceFor(info.Workflow)` (아래 §3 참고)
- Confidence: `"medium"`
- PersonaDelta: `[]self.Lesson{{Param: "caution", Delta: 0.02}}`
- Created: `now`

### 3. `sourceFor` 헬퍼 + 기존 4 rule 통합

```go
func sourceFor(workflow string) string {
    if workflow == "" {
        return "agent" // backward compat: Phase F-2 이전 저장된 lesson 과 동일 값
    }
    return "agent:" + workflow
}
```

기존 Rule A (tool error ≥ 3), Rule B (budget_exceeded stall), Rule C (efficient completion), Rule D (verbose output) 의 `Source: "agent"` 하드코딩을 전부 `Source: sourceFor(info.Workflow)` 로 교체한다. 5 개 rule 전부 동일 헬퍼 경유.

### 4. `MergeAgentToolStats` 헬퍼

```go
// MergeAgentToolStats sums Calls/Errors/TotalTime per tool Name across the
// provided slices. Entries with Calls == 0 after merging are dropped.
// Order of the returned slice is sorted by tool Name ascending for
// deterministic downstream behaviour.
func MergeAgentToolStats(slices ...[]AgentToolStat) []AgentToolStat {
    // 구현
}
```

사용처: F-3.2 에서 Team/Ralph/Autopilot wrapper 가 sub-run 의 ToolStats 를 이걸로 합친다.

정렬은 결정성(deterministic) 확보를 위해 Name ascending. 테스트가 map iteration 순서 의존성을 피할 수 있다.

## Constraints

- **기존 4 rule 의 임계값, Confidence, PersonaDelta 불변**: `agentToolFailureThreshold=3`, `agentVerboseOutputTokens=50_000`, `agentEfficientIterationPct=0.3` 등 상수 건드리지 말 것.
- **Rule E 는 새 상수 `agentRalphRetryThreshold = 3` 신설**.
- **Lesson 스키마 불변**: `learning.Lesson` 필드에 추가/삭제 금지. Source 문자열 값만 바뀐다.
- **기존 저장된 lesson 파일은 건드리지 않음**: extractor 는 읽기 전용, Append only. 이건 실제로 확인만 하고 별도 변경 없음 (코드가 이미 그렇게 돼 있음).
- **Workflow 값 validation 불필요**: extractor 는 caller trust. Orchestrator layer 에서만 세팅.

## Tests (반드시 작성)

### `TestExtractAgent_RalphRetry`

Table-driven:

| Case | RetryCount | Workflow | 기대 |
|------|-----------|----------|------|
| no retry | 0 | "ralph" | Rule E lesson 없음 |
| below threshold | 2 | "ralph" | Rule E lesson 없음 |
| at threshold | 3 | "ralph" | Rule E lesson 1 개, Source="agent:ralph", PersonaDelta caution +0.02 |
| high retry | 5 | "ralph" | Rule E lesson 1 개, text 에 "retried 5 times" 포함 |

### `TestExtractAgent_SourceSuffix`

각 rule 이 workflow 에 따라 올바른 Source 를 내는지.

| Workflow | Rule 발화 조건 | 기대 Source |
|----------|--------------|-------------|
| `""` | Rule A (tool error ≥ 3) | `"agent"` |
| `"single"` | Rule A | `"agent:single"` |
| `"team"` | Rule B (budget_exceeded) | `"agent:team"` |
| `"ralph"` | Rule C (efficient completion) | `"agent:ralph"` |
| `"autopilot"` | Rule D (verbose output) | `"agent:autopilot"` |
| `"ralph"` | Rule E (RetryCount=3) | `"agent:ralph"` |

### `TestMergeAgentToolStats`

- 빈 입력 → 빈 결과
- 단일 슬라이스 → Calls>0 만 통과, Name 정렬
- 겹치는 Name → Calls/Errors/TotalTime 모두 합산
- Calls=0 entry → 결과에서 제외
- 2 슬라이스 가 서로 다른 Name → union, 각각 보존

## Verification gates

```bash
cd /Users/stello/elnath
go vet ./internal/learning/...
go test -race ./internal/learning/...
```

두 명령 모두 exit 0 이어야 완료. 실패 시 수정 반복.

## Scope limit (절대 건드리지 말 것)

- `internal/orchestrator/**` — F-3.2 영역
- `cmd/elnath/**` — F-3.2 영역
- `internal/self/**` — Lesson 스키마 이미 확정
- `internal/learning/store.go` — F-2.5 에서 완료, 이번 변경 없음
- `internal/learning/research_extractor.go` — E-3 경로, F-3 무관
- 그 외 working tree 의 미커밋 변경들 — 건드리지 말 것

## 완료 보고 형식

작업 종료 시 다음을 보고:
1. 수정한 파일 목록 (파일 경로)
2. `go test -race ./internal/learning/...` 최종 출력의 PASS 요약
3. `go vet ./internal/learning/...` 결과
4. 예상 commit message (spec §8 의 F-3.1 템플릿 참고)

커밋은 하지 마라. 사용자(stello)가 직접 commit 한다.
