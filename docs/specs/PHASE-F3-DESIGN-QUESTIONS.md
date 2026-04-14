# Phase F-3 Design Questions (Pre-spec)

**Status:** BRAINSTORMING (다음 세션에서 결정 → spec 작성)
**Predecessor:** Phase F-2.5 (Lesson Redaction) DONE
**Goal:** F-2 에서 SingleWorkflow 한정으로 걸었던 learning hook 을 Team / Ralph / Autopilot 까지 확장

---

## 세션 시작 체크리스트

다음 세션 assistant 가 먼저 수행:

1. 메모리 read
   - `project_elnath.md`
   - `project_elnath_next_action.md` (F-2.5 DONE + 다음 F-3)
   - `project_elnath_next_session.md`
2. `git log -5 --oneline` — 커밋 4개 확인 (`13f44ef`, `ca08670`, `04a9e3b`, `542ca00`)
3. `git status` — 미커밋 잔여분 (Telegram 재설계, Gate retry) 정리 여부 판단
4. 참고 코드 parallel read:
   - `internal/orchestrator/team.go`
   - `internal/orchestrator/ralph.go`
   - `internal/orchestrator/autopilot.go`
   - `internal/orchestrator/single.go` (F-2 적용 reference)
   - `internal/orchestrator/types.go` (WorkflowInput/Result 구조)
   - `internal/learning/agent_extractor.go` (rule 4개)
   - `internal/agent/agent.go` (RunResult + ToolStats 현황)

---

## Q1: Team Workflow — Lesson 생성 단위

Team 은 여러 sub-agent 를 병렬/순차 실행. 각 sub-agent 가 자체 RunResult (ToolStats, FinishReason, Iterations) 생성.

### 옵션

**A. Sub-agent 별 lesson**
- 각 sub-agent 의 RunResult 마다 ExtractAgent 호출
- Lesson.Source = `"agent:team:<sub-agent-name>"`
- 장점: 세분화된 signal. 어느 sub-agent 가 문제인지 식별 가능
- 단점: lessons.jsonl 증가율 N배. team 1 run = N lesson 가능성. F-1 rotation 에 부담

**B. Team-level 집계 1 lesson**
- 모든 sub-agent ToolStats 를 flatten 해서 합산 → ExtractAgent 1회
- Lesson.Source = `"agent:team"`
- 장점: 증가율 균일, 운영 단순
- 단점: 개별 sub-agent 문제 신호 소실

**C. Coordination-only lesson**
- Sub-agent 자체는 lesson 생성 skip
- Team level 에서는 sub-agent 간 조율/충돌 signal 만 추출 (예: 1 성공 + 1 실패)
- 장점: Single + Team 각각이 자기 영역만 담당, 중복 없음
- 단점: 새 rule 필요 (coordination signal), 구현 복잡

### 사전 추천: **B** (team-level 집계)

근거:
- 증가율 예측 가능. F-1 rotation 기본값 (KeepLast:5000) 유효성 유지
- 기존 4 rule 을 그대로 재사용 가능
- 세분화는 Lesson.Source 에 `"team:<name>"` 형태로 메타만 붙이면 충분 (나중에 필요 시 filter)

**하지만** team 이 SingleWorkflow 를 내부 호출하면 sub-agent 쪽에서 Single hook 이 먼저 돌아 중복 생성 가능. Team 이 자기 Learning 을 주입할 때는 sub-agent 쪽 Learning=nil 로 덮는 guard 필요.

### 사용자 결정
- [ ] A — sub-agent 별
- [ ] B — team-level 집계 (추천)
- [ ] C — coordination-only
- [ ] 기타:

---

## Q2: Ralph Workflow — 반복 lesson 전략

Ralph 는 동일 task 를 최대 N회 반복 (실패 시 재시도). 각 iter 가 agent.Run 1회.

### 옵션

**A. 매 iter 마다 lesson**
- iter 1 실패 → lesson, iter 2 실패 → lesson, ...
- 장점: 각 시도의 signal 보존
- 단점: 같은 실패 반복 시 동일 lesson 이 여러 건 (ID 해시는 같으므로 dedupe 되지만 저장 횟수 증가)

**B. 최종 1회 lesson**
- ralph 전체 완료 후 최종 RunResult (마지막 iter 의 것) 로 ExtractAgent
- 장점: 단순, rotation 부담 없음
- 단점: 중간 iter 의 signal (예: 3번 째 재시도에서 다른 tool 패턴 발견) 소실

**C. 집계 후 1 lesson**
- 모든 iter 의 ToolStats 합산, Iterations 총합, FinishReason 은 최종 값
- 추가로 `RetryCount` 를 AgentResultInfo 에 노출 → 새 Rule E: "ralph retry ≥ 3 → instability lesson"
- 장점: 반복 사실 자체를 signal 로 활용
- 단점: 새 rule + AgentResultInfo 확장 필요

### 사전 추천: **C** (집계 + 신규 Rule E)

근거:
- Ralph 의 본질 (반복) 이 자체 signal. 이걸 버리면 Ralph 학습 가치 큰 폭 감소
- Rule E 는 구현 10줄 수준 (`if RetryCount >= 3 { caution +0.02 }`)
- ToolStats 합산은 F-3 Q1 와 같은 merge 로직 재사용

### 사용자 결정
- [ ] A — 매 iter 마다
- [ ] B — 최종 1회
- [ ] C — 집계 + 신규 Rule E (추천)
- [ ] 기타:

---

## Q3: Autopilot Workflow — Pipeline 단계별 vs 최종

Autopilot 은 planner → executor → verifier 파이프라인. 각 단계가 별도 agent.Run.

### 옵션

**A. 단계별 lesson**
- planner 단계 끝에 lesson, executor 단계 끝에 lesson, verifier 단계 끝에 lesson
- Lesson.Source = `"agent:autopilot:planner"` 등
- 장점: 어느 단계에서 문제였는지 명시
- 단점: 1 pipeline = 최대 3 lessons. Team 옵션 A 와 같은 volume 문제

**B. Pipeline 최종 1 lesson**
- Verifier 통과 후 전체 pipeline ToolStats 집계 → 1 lesson
- 장점: Team 옵션 B 와 일관. 운영 단순
- 단점: planner 가 비효율적이었음에도 verifier 통과하면 signal 소실

**C. 단계 실패 시만 lesson**
- 정상 통과는 lesson 없음 (또는 verifier 통과한 최종 1회만)
- 중도 실패한 단계에서만 lesson 생성
- 장점: 성공 케이스는 볼륨 0. 실패 패턴만 축적
- 단점: positive reinforcement (Rule C) 를 놓침. 성공 패턴 학습 기회 상실

### 사전 추천: **B** (최종 1회, Team 과 일관)

근거:
- Team 과 design parity 유지 → 사용자가 Source 로 구분만 하면 됨
- Pipeline 은 성공/실패가 verifier 에서 판명. 중간 단계의 tool stats 는 합산으로 충분히 signal 됨
- 단계별 세분화는 lessons 운영 복잡도만 증가 (stats by-source 에 3단계 구분 노출 혼란)

### 사용자 결정
- [ ] A — 단계별
- [ ] B — 최종 1회 (추천)
- [ ] C — 실패 시만
- [ ] 기타:

---

## Q4: Lesson.Source 네이밍 체계

F-2 에서 agent source 는 `"agent"` 단일값. F-3 에서 workflow 세분화 필요.

### 옵션

**A. Flat string**
- `"agent:single"` / `"agent:team"` / `"agent:ralph"` / `"agent:autopilot"`
- research 는 기존 `"research"` 유지 (또는 `"research:topic"` 로 확장)
- 장점: 단순 string, filter 쉬움 (`--source agent:team`)
- 단점: 계층 구조가 string 에 묻힘

**B. 구조화 필드 분리**
- Lesson 에 `Workflow string` 필드 추가, `Source` 는 카테고리 (`agent` / `research`) 유지
- `elnath lessons stats --by-workflow` / `--by-source` 각각
- 장점: 명시적 계층
- 단점: Lesson struct 확장 = JSONL 스키마 변경. 기존 lesson 하위호환 필요

**C. 현상 유지 (agent 단일)**
- F-3 에서도 모든 workflow 가 `"agent"` 공통
- 장점: 변경 최소, 기존 stats 출력 호환
- 단점: 분석 불가

### 사전 추천: **A** (flat string, `"agent:<workflow>"`)

근거:
- 기존 Lesson 스키마 변경 없음 (JSONL 하위호환)
- filter 는 `strings.HasPrefix(l.Source, "agent:")` 또는 `"agent:team"` 정확 매치 양쪽 가능
- F-1 의 `lessons stats` 가 `ByTopic`/`ByConfidence` 집계 중. `ByConfidence` 같은 패턴으로 `BySource` 쉽게 추가 가능
- 추후 `elnath lessons stats --by-source` flag 는 F-3 과 같이 묶어서 추가

### 사용자 결정
- [ ] A — `"agent:<workflow>"` (추천)
- [ ] B — 구조화 (Workflow 필드 추가)
- [ ] C — 현상 유지
- [ ] 기타:

---

## 부가 사항

### Scope 추정 (옵션 B/C/B/A 선택 시)

- `internal/orchestrator/team.go` — applyLearning hook (단, SingleWorkflow 호출 시 Learning=nil 덮어쓰기 guard)
- `internal/orchestrator/ralph.go` — applyLearning hook, RetryCount 누적
- `internal/orchestrator/autopilot.go` — applyLearning hook, 단계 ToolStats 합산
- `internal/learning/agent_extractor.go` — Rule E (ralph instability) 추가, AgentResultInfo.RetryCount 필드
- `internal/orchestrator/{single,team,ralph,autopilot}.go` — Source 값을 `"agent:<name>"` 로 지정
- `cmd/elnath/runtime.go` / `cmd_daemon.go` — Team/Ralph/Autopilot workflow 에도 Learning 주입
- 테스트: 각 workflow 당 2-3 케이스 + duplicate guard 검증

추정 LOC: ~600-800 (Ralph/Autopilot 의 내부 구조가 single 보다 복잡). Phase 1-2 분리 권장:

- **Phase 1**: AgentResultInfo 확장 (RetryCount) + Rule E + ToolStats merge 헬퍼
- **Phase 2**: Team/Ralph/Autopilot workflow hook + wiring + tests

### 미커밋 잔여분 처리

현재 작업트리에 Telegram 재설계 + Gate retry 관련 변경이 미커밋. F-3 는 orchestrator 와 cmd/elnath 를 둘 다 건드리므로 충돌 위험. 다음 세션 시작 시:

- `git diff --stat` 로 잔여분 범위 확인
- F 시리즈와 무관한 lineage 면 별도 커밋 또는 stash 권장
- F-3 작업 전 작업트리 정리 (세션 단위 분리)

### 중복 추출 방지

Team 이 내부적으로 SingleWorkflow 를 호출하는 경우 :
- Team 의 wrapper 가 Learning 을 주입하고, sub-agent SingleWorkflow 호출 시 Learning=nil 로 override
- 또는 SingleWorkflow 가 context 에 "parent_workflow" 메타를 보고 skip
- 결정: **Team wrapper 에서 Learning=nil 주입** (더 명시적, test 용이)

### Research 경로는 건드리지 않음

Research 는 E-3 에서 별도 path. F-3 에서 rebuild 하지 말 것. 회귀 방지를 위해 E-3 관련 테스트 전부 pass 유지.

---

## 다음 세션 흐름

1. 위 Q1-Q4 에 사용자가 ✓ 체크 (또는 "기타" 로 답변)
2. 결정 내용으로 `docs/specs/PHASE-F3-MULTI-WORKFLOW-LEARNING.md` 작성
3. OpenCode 프롬프트 (2-phase) 작성
4. 실행 → 독립 검증 → code-reviewer → fix → 커밋

결정 전에 추가 조사가 필요하면 parallel read 로 team.go / ralph.go / autopilot.go 먼저 훑고 구체 사례 들어서 재제안.
