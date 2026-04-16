# Local Outcomes Ralph Refactor

**Status:** SPEC READY
**Predecessor:** Phase C-2 (Skill Emergence MVP) DONE
**Successor:** Agent Profile
**Ref:** Managed Agents Outcomes 패턴, Elnath Superiority Design

---

## 1. Goal

현재 RalphWorkflow의 binary PASS/FAIL 검증을 3-state rubric 기반 독립 평가로 개선한다.
Managed Agents Outcomes 패턴을 로컬에서 구현하여, 자율 검증 품질을 구조적으로 향상시킨다.

## 2. 변경 범위

`internal/orchestrator/ralph.go` **한 파일만 수정**. 테스트 파일 포함 총 2 파일.

## 3. Design

### 3.1 3-State Verdict

```go
type VerifyVerdict int
const (
    VerdictPass          VerifyVerdict = iota // 완료, 반환
    VerdictNeedsRevision                      // retry + 피드백
    VerdictFail                               // 즉시 중단
)
```

verify() 반환 변경: `(bool, string, UsageStats, error)` → `(VerifyVerdict, string, UsageStats, error)`

Run() 루프 분기:
- `VerdictPass` → break, 성공 반환 (기존 동일)
- `VerdictNeedsRevision` → 피드백 append, retry (기존 FAIL 동작과 동일)
- `VerdictFail` → 즉시 error 반환, retry 낭비 방지

### 3.2 Evidence Window 확장

변경 전 → 변경 후:
- `maxToolResults`: 4 → 8
- `maxToolChars`: 1200 → 2000
- `maxAssistantChars`: 4000 → 6000

Grader가 더 넓은 context로 판정. 토큰 증가 ~3-4K, 전체 대비 미미.

### 3.3 Rubric 기반 Prompt

```
You are an independent quality reviewer evaluating task completion.

Original task: %s

Execution evidence:
%s

Evaluate against these criteria:
1. CORRECTNESS: Does the output correctly address the task?
2. COMPLETENESS: Are all parts of the task addressed?
3. VERIFICATION: Did the agent verify its work (tests, commands)?

Respond with exactly one of:
  PASS — all criteria satisfied
  NEEDS_REVISION: <specific feedback> — direction is right but needs fixes
  FAIL: <reason> — fundamentally wrong approach, retrying won't help

Your response must start with PASS, NEEDS_REVISION, or FAIL.
```

### 3.4 Verdict 파싱

```go
upper := strings.ToUpper(verdict)
switch {
case strings.HasPrefix(upper, "PASS"):
    return VerdictPass, "", usage, nil
case strings.HasPrefix(upper, "NEEDS_REVISION"):
    return VerdictNeedsRevision, extractFeedback(verdict), usage, nil
case strings.HasPrefix(upper, "FAIL"):
    return VerdictFail, extractFeedback(verdict), usage, nil
default:
    return VerdictNeedsRevision, verdict, usage, nil
}
```

Default는 NEEDS_REVISION (보수적 — 판정 불분명하면 retry).

### 3.5 Learning FinishReason 확장

기존: `"ralph_cap_exceeded"` (retry 소진)
추가: `"ralph_fail"` (grader FAIL 판정)

Run()에서 VerdictFail 시:
```go
if input.Learning != nil {
    info.FinishReason = "ralph_fail"
    applyAgentLearning(...)
}
return nil, fmt.Errorf("ralph workflow: verifier rejected task as fundamentally incorrect: %s", feedback)
```

### 3.6 Grader 구성

- Model: working agent와 동일 (변경 없음)
- Tools: working agent tools 전달 (변경 없음)
- MaxIterations: 3 (변경 없음)

## 4. 변경하지 않는 것

- Run() for loop 구조
- buildRecoveryPrompt()
- sanitizeRetryMessages()
- Grader model/tools 선택 로직
- WorkflowInput/WorkflowResult 타입

## 5. Acceptance Criteria

- [ ] VerdictPass → 기존과 동일하게 성공 반환
- [ ] VerdictNeedsRevision → retry + 피드백 (기존 FAIL 동작)
- [ ] VerdictFail → 즉시 error, retry 안 함
- [ ] Default (파싱 실패) → NEEDS_REVISION 처리
- [ ] Evidence window: 8 tool results, 2000 chars each
- [ ] FinishReason "ralph_fail" 기록
- [ ] 기존 테스트 regression 없음
- [ ] `go test -race ./internal/orchestrator/... -run TestRalph` PASS

## 6. Risk

| Risk | Mitigation |
|------|-----------|
| FAIL 판정 과다 → 작업 중단 빈도 증가 | Default를 NEEDS_REVISION으로 보수적 처리 |
| Rubric이 특정 도메인에 부적합 | 3개 criteria가 범용적, config로 확장 가능 |
| Evidence 확장으로 토큰 증가 | ~3-4K 추가, 전체 대비 2-3% |
