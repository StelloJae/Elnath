# Phase F-5 Design Questions (Pre-spec) — LLM-based Lesson Extraction

**Status:** BRAINSTORMING (사용자 결정 → spec 작성)
**Predecessor:** Phase F-4 (Lessons by-source stats + list filter) DONE
**Goal:** Rule-based extractor 9개 (Research R1-R3 + Agent A/B/C/D + Rule E) 가 놓치는 패턴을 LLM으로 보완

---

## 리서치 요약 (2026-04-14)

**내부 데이터**:
- `~/.elnath/data/lessons.jsonl` = **0 bytes** (프로덕션 lesson 한 번도 쌓인 적 없음)
- 실행 중이던 daemon (PID 70206, Sat Apr 11 16:50 시작) 이 F-2 시대 바이너리. F-3.1/F-3.2/F-4 커밋은 2026-04-14 08:52~09:22. 본 세션에서 `launchctl kickstart -k gui/$UID/com.elnath.daemon` 으로 재시작 (새 PID 22328).
- Daemon 로그: Apr 11-14 기간 동안 task 2-11 "completed" 찍혀 있으나 lesson 0. Rule A-D 가 **failure signal만** 잡도록 설계된 게 원인.

**Rule 9개 분석** (`internal/learning/{extractor,agent_extractor}.go`):
- Positive signal 1개: Rule C (`iter ≤ 30% × maxIter AND totalCalls > 0 AND FinishReason=stop` → persistence+0.01).
- 나머지 8개는 전부 실패 지표 (tool error ≥ 3, budget_exceeded, output_tokens ≥ 50K, retry ≥ 3, low-confidence ≥ 50%, TotalCost > $2).
- **LLM이 메울 gap 후보** (우선순위):
  1. 성공 패턴 학습 (가장 큰 gap — Rule C 사각지대 대부분)
  2. 에러 메시지 내용 기반 교훈 (Rule A는 횟수만, 내용은 무시)
  3. Tool 순서·조합 효과 (Rule은 개별 tool만 봄)
  4. Project-specific 관습 ("이 repo는 pnpm", "test는 `make test`" 등)
  5. Persona delta 근거 있는 값 (현재 0.01~0.03 하드코딩)
  6. 중복·모순 lesson reconciliation

**외부 레퍼런스** (document-specialist 리서치):
- **Claude Code `extractMemories` + `autoDream`** — 가장 직접 이식 가능. Per-turn forked agent, cursor tracking, existing-memory manifest injection, rule-vs-LLM mutual exclusion (`hasMemoryWritesSince`), maxTurns:5, typed schema (user/feedback/project/reference), `Why:` + `How to apply:` 필수, 24h/5-session autoDream consolidation.
- **Hermes `MemoryProvider`** — 3 hook (`on_session_end`, `on_pre_compress`, `on_delegation`). 성공/실패 trajectory 파일 분리.
- **openclaw** — RAG 전용 (embedding 색인). Delta threshold (100KB / 50 msgs) 는 참고 가능.
- **Anthropic cookbook `evaluator_optimizer`** — in-loop 품질 gate, post-run extraction 아님. F-5에 부적합.

**Cost 모델** (scientist 리서치, 577 sessions / 5.8 days):
- 현재 organic cadence: ~20-33 agent runs/day (p50 세션 ~2.8KB, p75 ~23KB, p95 ~122KB).
- Input 토큰 추정 (p75 / p95): full=7.7K/40.9K, compact=2K/6.2K.
- 월 비용 (50 runs/day, p75 input, 300 output tok):
  - Haiku+compact: **$5.70**, Haiku+full: **$14.25**
  - Sonnet+compact: **$17.10**, Sonnet+full: **$42.75**
- Base run 대비 extraction 비중: p50 세션에서 full 모드는 **94%** (거의 2배). p75+ 세션에서 compact 모드는 2-9%.
- **반드시 session-complexity gate 필요**: `num_messages ≥ 5` 또는 `has_tool_call == true` 에서만 fire.
- Stress case (Sonnet+full+200/day+p95): **$768/월** → 위험. full 모드 상한 (예: 20K tok truncate) 필요.

---

## 세션 시작 체크리스트

다음 세션 assistant 가 먼저 수행:

1. 메모리 read
   - `project_elnath.md`
   - `project_elnath_next_action.md`
   - `feedback_research_before_spec.md`
2. `git log -10 --oneline` — F-4 `8b659bc`, F-3.2 `9fbc296`, F-3.1 `e3a4de5`, F-2.5 `13f44ef` 확인
3. `git status` — working tree 정리 상태 확인
4. Daemon 재확인:
   - `launchctl list | grep elnath` → 현 PID
   - `./elnath lessons stats` → lesson 축적 상태 (리서치 중 재시작 → 며칠 후 재확인 필요)
5. 참고 코드 parallel read:
   - `internal/learning/agent_extractor.go` (rule 5개)
   - `internal/learning/extractor.go` (research rule 3개)
   - `internal/learning/store.go` (Append/Filter/List 인터페이스)
   - `cmd/elnath/runtime.go:217-247` (learningStore 주입부)
   - `cmd/elnath/cmd_daemon.go:116-177` (daemon 쪽 learningStore)
   - `internal/llm/` (provider 인터페이스 — Haiku/Sonnet 호출 경로)
   - `internal/orchestrator/learning.go` (F-3.2 공통 hook)
6. 레퍼런스 재확인 (선택):
   - Claude Code: `/Users/stello/claude-code-src/src/services/extractMemories/extractMemories.ts`
   - Claude Code: `/Users/stello/claude-code-src/src/services/extractMemories/autoDream.ts`
   - Hermes: `/Users/stello/.hermes/hermes-agent/agent/memory_provider.py`

---

## Q1: Trigger — LLM extraction 은 언제 fire 하는가?

### 옵션

**A. Per-run unconditional**
- 모든 agent run 끝에 LLM 호출 (session-complexity gate 없이)
- 장점: Claude Code 와 동일 패턴. 누락 없음.
- 단점: 짧은 chat/stub session에서 base run의 94% 비용. 현재 577 session 중 133개가 stub (<200B) — 23% wasted calls.

**B. Complexity-gated per-run**
- `num_messages ≥ 5 OR has_tool_call == true` 만족 시 LLM 호출
- 장점: cost 모델의 "반드시 필요" 결론 반영. stub/chat-only 세션 skip.
- 단점: gate 미세조정 필요 (4 messages + tool_call 은 통과? 10 messages + no tool은?). Edge case.

**C. Rule-gap-only**
- Rule A-E가 lesson 0개 생성했을 때만 LLM 호출 (Claude Code의 `hasMemoryWritesSince` mutual exclusion 강화판)
- 장점: 비용 최저. 성공 패턴 학습 gap 정확히 타깃.
- 단점: Rule이 잘못 fire해도 LLM 보완 없음. Rule 1개 + LLM 다수 인사이트 있는 경우 놓침.

### 사전 추천: **B** (complexity-gated per-run)

근거:
- Cost 모델의 hard constraint (p50에서 94%) 를 그대로 반영
- Claude Code 도 사실 `skipCriteria` 로 similar gate 적용 (`isSubagent`, `disabled`, remote mode 등)
- C (rule-gap-only) 는 리서치 후보 1번 ("성공 패턴 학습") 의 정공법이지만, 후보 2-6 (에러 내용, tool 순서, project-specific 관습 등) 은 rule이 fire해도 LLM이 보완해야 함. 상호배타보다 병행이 나음 → Q6에서 다시 논의
- Gate 기준은 초기 보수적으로 (`≥ 5 messages AND has_tool_call`), 실측 데이터 쌓이면 완화 가능

### 사용자 결정
- [ ] A — per-run unconditional
- [ ] B — complexity-gated (추천)
- [ ] C — rule-gap-only
- [ ] 기타:

---

## Q2: Input Mode — LLM에 무엇을 보내는가?

### 옵션

**A. Full transcript**
- 세션 JSONL 전체 → LLM prompt
- 장점: 모든 맥락 보존. Claude Code의 `createCacheSafeParams` 와 동일.
- 단점: p95 40.9K tok. Sonnet+full+200/day = $768/월 stress case. p50 비용 과다.

**B. Compact summary**
- 직전 10 messages + tool-call stats 헤더 (~2K tok p75)
- 장점: 비용 1/4. 최신 signal 중심.
- 단점: 장기 session context 소실. "초기에 사용자가 X 지시했는데 무시" 같은 패턴 놓침.

**C. Hybrid (tool-stats + last-N + selective context)**
- tool_stats 요약 + 직전 N messages + 사용자 명시적 피드백 turn (길이 무관 전체 포함)
- 장점: compact 의 싸면서 사용자 correction signal 보존.
- 단점: "사용자 명시 피드백" 감지 로직 필요 (keyword? heuristic?). 구현 복잡도 +.

### 사전 추천: **B** 로 시작, 실측 후 **C** 로 진화

근거:
- B 는 구현 최단 + 비용 예측 가능 ($14/월 baseline at Haiku+full — 실제로는 compact에서 $5.70).
- Cost 모델 recommendation 과 일치 (Balanced regime).
- Claude Code는 full 쓰지만 prompt cache 88% 효율에 기반 — Elnath는 cache 전략 미구현. Full 을 지금 도입하면 cache 없이 full price. B 로 시작해 cache hit rate 실측 후 full로 upgrade가 안전.
- C 의 "사용자 correction 감지" 는 F-6 또는 F-5.1 로 분리 (scope creep 방지).

### 사용자 결정
- [ ] A — full transcript
- [ ] B — compact summary (추천)
- [ ] C — hybrid
- [ ] 기타:

---

## Q3: Cursor Tracking — 증분 vs 전체

### 옵션

**A. Cursor (last-processed message UUID / line number per session)**
- 세션마다 cursor 저장, 새 메시지만 LLM에 전달
- 장점: Claude Code `lastMemoryMessageUuid` 와 동일. 다회 turn 세션에서 누적 비용 선형 방지.
- 단점: cursor state 저장소 필요 (`~/.elnath/data/lesson_cursors.json` 또는 DB). Lost cursor 시 재처리 정책 결정 필요.

**B. Full session replay**
- 매번 전체 transcript 처리
- 장점: 무상태. 구현 최단.
- 단점: 다회 turn 세션 재처리 N배 낭비. Q2 B 채택 시 "직전 10 msgs" 이 cursor 역할을 암묵적으로 함 → 중복 완화되나, 똑같은 10 msgs 를 매번 재평가.

**C. Session-end only**
- 세션 종료 (명시적 `stop` FinishReason) 시점에만 1회 추출, cursor 불필요
- 장점: Hermes `on_session_end` 패턴. 단순.
- 단점: 장기 session (telegram chat 등 종료 없음) 에서 lesson 영원히 추출 안 됨.

### 사전 추천: **A** (cursor)

근거:
- Q1-B 선택 시 session 당 여러 번 LLM fire 가능. cursor 없으면 동일 context 재평가 = 비용 낭비.
- Cursor 구현 ~20 LOC (session_id → last_line_number map 을 lessons.jsonl 옆에 append-only JSONL 로 저장하거나 elnath.db에 table 추가).
- Claude Code도 동일 패턴 채택, 프로덕션 검증됨.
- B는 비용 2-5배, C는 telegram shell 등 상시 open 세션 치명적.

### 사용자 결정
- [ ] A — cursor (추천)
- [ ] B — full replay
- [ ] C — session-end only
- [ ] 기타:

---

## Q4: Existing-lessons Manifest 주입

### 옵션

**A. Full manifest inject**
- Prompt에 기존 lesson list (ID + topic + text 요약) 주입, "update-not-duplicate" 지시
- 장점: Claude Code 표준. 중복 lesson 방지. LLM 이 기존 lesson과 상충하는 신호 발견 시 reconciliation 가능 (리서치 gap 후보 #6).
- 단점: Prompt 크기 증가. 현재 0 lesson이지만 수개월 후 수백개면 ~5-10K tok 추가.

**B. Topic-scoped manifest**
- 현재 run의 topic과 관련된 lesson만 (예: 같은 topic prefix) 주입
- 장점: prompt 크기 제한. 집중도 증가.
- 단점: topic scoping 로직 필요. cross-topic 패턴 놓침.

**C. No manifest**
- LLM 에 기존 lesson 알리지 않음. dedupe 는 `deriveID` SHA256 중복 제거에 의존.
- 장점: prompt 최소. SHA256 중복은 이미 `Store.Append` 에서 처리됨.
- 단점: 유사하지만 텍스트 다른 lesson (예: "bash failed 3x on go build" vs "bash errored 3 times building go") 중복 발생. 의미적 dedupe 불가.

### 사전 추천: **A** 로 시작, 수백개 넘으면 **B** 로 진화

근거:
- 현재 0 lesson → 한동안 manifest 가 1-20개 수준 → 비용 무시 가능.
- Claude Code는 A 패턴 + 주기적 `autoDream` consolidation 으로 prune → 수는 선형 증가 아님.
- Q9 에서 consolidation 결정되면 A 유지 가능성 높음.
- B는 F-5.2 또는 manifest size > N 시 자동 전환으로 follow-up.

### 사용자 결정
- [ ] A — full manifest (추천)
- [ ] B — topic-scoped
- [ ] C — no manifest
- [ ] 기타:

---

## Q5: Provider / Model 선택

### 옵션

**A. Haiku 4.5 고정**
- 모든 extraction call에 Haiku ($1/$5 per MTok)
- 장점: 최저 비용. Haiku+full 50/day = $14/월.
- 단점: 복잡 추론 약함 — cross-session 패턴 감지, persona delta 근거 제안 등에서 깊이 부족 가능.

**B. Sonnet 4.6 고정**
- 모든 call에 Sonnet ($3/$15) — 현재 elnath 기본 모델과 일치
- 장점: Elnath agent loop 와 동일 품질. "이 접근이 잘 먹혔다" 같은 판단 정확도 ↑.
- 단점: Sonnet+compact 50/day = $17/월, Sonnet+full 50/day = $42/월. 200/day 확장 시 위험.

**C. Mixed (complexity-tiered)**
- 기본 Haiku, Rule E retry≥3 케이스 또는 session_cost > $0.10 케이스에서만 Sonnet
- 장점: 비용/품질 균형. 일반 run은 싸고, 중요 run은 깊이.
- 단점: tier 규칙 정의 필요. cost attribution 로직 추가.

### 사전 추천: **A** (Haiku 고정) 로 시작

근거:
- Cost 모델 Balanced regime.
- Extraction task 자체는 "structured output 생성" 이라 Haiku 로도 충분 (Claude Code는 자체 fork 에서 parent와 같은 모델 사용하지만 maxTurns:5 제약).
- 품질 부족 실측되면 C로 전환 (Haiku 기본 + 복잡 case만 Sonnet). B 는 확장성 위험.
- 중요: F-5 spec 에 "Model switch는 config 1줄 수정" 으로 쉽게 바뀌도록 설계 (FlagLLMModel config key).

### 사용자 결정
- [ ] A — Haiku 4.5 (추천)
- [ ] B — Sonnet 4.6
- [ ] C — Mixed (tiered)
- [ ] 기타:

---

## Q6: Rule-vs-LLM 공존 모드

### 옵션

**A. Mutual exclusion (Claude Code 패턴)**
- Rule이 해당 run에서 lesson 생성했으면 LLM skip. Rule=0 이면 LLM fire.
- 장점: 구현 단순. 중복 방지 확실.
- 단점: Rule A (tool error 3회) 가 fire했지만 그 외 "이 tool 조합이 실패했다" 같은 LLM-only 인사이트 놓침.

**B. Parallel (둘 다 실행, Store 레벨에서 dedupe)**
- Rule lesson + LLM lesson 둘 다 Append. SHA256 ID 중복만 제거.
- 장점: 양쪽 signal 모두 보존. 리서치 gap 후보 2-6 (에러 내용, tool 순서 등) 이 Rule과 병행 포착.
- 단점: volume 증가. 유사하지만 다른 텍스트는 dedupe 안 됨.

**C. LLM-primary, Rule-fallback**
- 평소엔 LLM만. LLM fail/timeout 시 Rule fallback.
- 장점: LLM 품질 신뢰 시 깔끔. 중복 고민 없음.
- 단점: Rule의 결정적·무료 signal 버림. LLM 의존도 ↑ (provider outage 시 학습 멈춤).

### 사전 추천: **B** (parallel) + Q1-B gate 공유

근거:
- Rule은 매우 싸고 결정적 → 버릴 이유 없음. LLM은 gap 채우기.
- Q1-B (complexity-gated) 가 이미 fire 여부를 결정. Rule은 항상 fire, LLM은 gate 통과 시만 — Rule 0 + LLM N, Rule N + LLM M, 또는 Rule N + LLM 0 (LLM도 "추가 인사이트 없음" 판단) 모두 자연스럽게 수용.
- Claude Code의 mutual exclusion은 "성공한 경우에도 LLM이 무언가 쓴다" 철학 때문 (성공 케이스 Rule 0 → LLM 1). Elnath Rule이 실패 편향이라 동일 로직 적용 시 LLM이 성공 케이스 대부분 담당 → 사실상 B와 유사한 결과.
- LLM이 Rule과 유사한 lesson 생성해도 SHA256 dedupe로 자동 억제. 의미적 중복은 Q9 consolidation 담당.

### 사용자 결정
- [ ] A — mutual exclusion
- [ ] B — parallel + dedupe (추천)
- [ ] C — LLM-primary
- [ ] 기타:

---

## Q7: Output Schema — Lesson 구조 확장 여부

### 옵션

**A. 기존 Lesson struct 그대로 사용**
- LLM 출력을 `{Topic, Text, Source, Confidence, PersonaDelta}` 에 매핑
- 장점: JSONL 스키마 변경 없음. Store / Filter / Stats 전부 호환.
- 단점: "왜 이 lesson인가" 근거 (Claude Code의 `Why:` 필드) 저장 못 함. Debug 어려움.

**B. Lesson 확장 (`Rationale`, `Evidence` 필드 추가)**
- `Lesson` 에 `Rationale string` (Claude Code `Why:` 등가), `Evidence []string` (원문 인용, optional) 추가
- 장점: LLM 판단 근거 보존 → 나중에 "이 lesson 틀렸네" 감사 가능. Consolidation 시 상충 해소 재료.
- 단점: JSONL 스키마 변경. 기존 lesson 하위호환 처리 필요 (nil safe는 자동).

**C. 별도 타입 `LLMLesson` + interface union**
- Rule lesson은 기존 `Lesson`, LLM lesson은 `LLMLesson` 별도 struct (Rationale + Evidence + Confidence를 LLM 고유 방식)
- Store / Filter 가 둘 다 handle
- 장점: 출처별 schema 명확.
- 단점: 코드 복잡도 +. `lessons list`/`stats` 도 분기 필요. Consumer (LessonsNode prompt injection) 도 union 처리.

### 사전 추천: **B** (Lesson 확장)

근거:
- Rationale 필드는 Claude Code 의 핵심 학습 — `Why:` 가 없으면 시간 지남에 따라 lesson 무의미화 (feedback memory 와 같은 문제).
- JSONL 하위호환: 기존 lesson 에는 `Rationale = ""` 로 load, 신규는 포함. Write 시에만 `omitempty` 로 공간 절약.
- `Evidence []string` 은 optional. 초기엔 미사용, F-5.2에서 "quote 원문 transcript line" 추가 가능.
- C는 분기 비용이 이득보다 큼. Rule lesson 도 Rationale 넣으면 ("Tool X failed 3x, threshold reached") 동일 스키마 유지하면서 명확도 상승.

### 사용자 결정
- [ ] A — 기존 Lesson 유지
- [ ] B — Lesson 확장 (Rationale) (추천)
- [ ] C — 별도 LLMLesson 타입
- [ ] 기타:

---

## Q8: Persona Delta — LLM이 제안 vs 고정

### 옵션

**A. LLM이 delta 값 제안**
- LLM 출력에 `persona_delta: [{param: "caution", delta: 0.04}]` 포함
- 장점: 맥락 기반 가변 값. Rule의 하드코딩 0.01~0.03 한계 극복.
- 단점: LLM 이 절대값 지정 = hallucination 위험. 악성 prompt로 delta=100 주입 가능.

**B. LLM은 정성적 힌트, 코드가 수치 fix**
- LLM: `{param: "caution", direction: "increase", reason: "..."}` → 코드에서 +0.02 매핑
- 장점: 안전. 일관된 크기.
- 단점: Rule과 동일한 하드코딩 한계.

**C. LLM은 persona_delta 미생성, Rule만 담당**
- LLM lesson은 `PersonaDelta: nil` 고정. persona 는 rule 전용.
- 장점: 최단순. 보안 우려 zero.
- 단점: 리서치 gap 후보 #5 ("근거 있는 delta 값") 미해결.

### 사전 추천: **B** (정성적 힌트 + 코드 매핑)

근거:
- A 의 hallucination 위험 실제 — LLM이 "critical finding" 이라며 delta=0.5 줘버리면 persona 폭주.
- B 는 schema 에 `direction` (`increase` / `decrease` / `neutral`) 과 `magnitude` (`small` / `medium` / `large`) 등 enum으로 제약. 코드가 {small: ±0.01, medium: ±0.03, large: ±0.06} 매핑.
- C 는 리서치 gap 미해결이라 지금 선택할 이유 없음.
- `Evidence` (Q7-B) 와 묶이면 "왜 이 direction" 근거까지 감사 가능.

### 사용자 결정
- [ ] A — LLM 절대 delta
- [ ] B — LLM 정성 + 코드 매핑 (추천)
- [ ] C — LLM은 persona 미생성
- [ ] 기타:

---

## Q9: Consolidation Pass (Claude Code `autoDream` 등가)

### 옵션

**A. No consolidation (F-5 에서 생략)**
- Per-run extraction 만. `Store.Append` 의 SHA256 dedupe + F-1 rotation 이 유일한 축적 관리.
- 장점: scope 최소. F-5 LOC 추정 그대로 (~400-600).
- 단점: 유사 lesson 누적 → 몇 달 후 매니페스트 거대화. Q4-A 전제 무너짐.

**B. Periodic consolidation (24h + 5 sessions gate)**
- Claude Code `autoDream` 이식. daemon scheduler 에서 매일 1회 (혹은 조건 만족 시) lesson 전체 대상으로 LLM pass — merge / delete contradictions / 인덱스 정리.
- 장점: 매니페스트 건강 유지. 리서치 gap #6 (reconciliation) 해결.
- 단점: scope +200-300 LOC. autoDream prompt engineering 필요. Ralph/Autopilot 와 별개 스케줄 필요.

**C. On-demand consolidation (`elnath lessons consolidate` CLI)**
- 사용자 명시적 명령 시만 실행. 자동 트리거 없음.
- 장점: 중간 scope. 사용자 control.
- 단점: 실행되지 않음으로 누적 여전. B의 이점 대부분 소실.

### 사전 추천: **A** (F-5 에서 생략, F-5.2 로 분리)

근거:
- 현재 lesson 0 → 수개월간 수십 개 수준 → 매니페스트 문제 없음.
- F-5 핵심은 "성공 패턴 학습" (리서치 gap #1). Consolidation 은 수가 쌓인 후 가치 발현 — 지금 만들면 dead code.
- B 를 F-5.2 로 분리: F-5 출시 후 1-2개월 실데이터 축적 → Consolidation 필요성 검증 → 설계 근거 확보.
- C 는 중간 선택 같지만 "실행 안 되는 기능" 이라 F-5.2 때 B로 재설계하게 됨 → 지금 만들 이유 없음.

### 사용자 결정
- [ ] A — 생략, F-5.2 로 분리 (추천)
- [ ] B — Periodic 포함
- [ ] C — On-demand 포함
- [ ] 기타:

---

## Q10: 실패 처리 + 결정론 (fallback, mock, golden test)

### 옵션

**A. Strict fail-closed**
- LLM call 실패 (timeout / 400 / quota exceeded) 시 에러 로그 + 해당 run lesson 0. Rule 결과는 보존.
- 장점: 무효 lesson 저장 없음. Rule 의 결정적 baseline 항상 유효.
- 단점: outage 기간 LLM gap 학습 완전 0. 재시도 없음.

**B. Fail-silent + retry queue**
- LLM fail 시 run metadata 를 retry queue (`~/.elnath/data/lesson_retry.jsonl`) 에 저장. Scheduler 가 주기 (예: 1h) 마다 재시도.
- 장점: outage 회복 후 따라잡음. 운영 resilience.
- 단점: Queue 관리 복잡도 + (rotation, TTL, poison entry).

**C. Fail-fast + circuit breaker**
- N회 연속 fail 시 LLM extraction pause (예: 10분), 상태를 daemon log + `lessons stats` 에 노출. 회복 시 resume.
- 장점: Provider outage 감지 + 사용자 가시성. 쓰지 않는 call 로 비용 방어.
- 단점: pause 중 lesson 손실. 복구 정책 필요.

### 사전 추천: **A** (strict fail-closed) + **C** (circuit breaker) 결합

근거:
- A 단독: outage 길면 silent loss. C 단독: 짧은 fail 에서도 부작용.
- A + C: 기본은 strict fail-closed (한 번 fail = 해당 run 포기), 10분 내 5회 연속 fail → 10분 pause (daemon 로그 + `lessons stats` 에 "llm_extraction: paused" 표시). 재개 시 자동.
- B 의 retry queue 는 elnath가 "학습 지연 복구" 까지 보장하는 야심. 현재 goal (gap 채우기) 대비 복잡도 과다 → F-5.2 로 분리.
- 구현 ~50 LOC (fail counter + 타임스탬프 + sync/atomic).

**결정론 측면**:
- `internal/llm/` 에 이미 mock provider 패턴 있음 (research path 테스트). F-5 도 `LessonExtractor` 인터페이스 → mock 구현 → golden lesson JSON 비교.
- LLM 비결정성 → temperature=0 고정 + `seed` (Anthropic API 지원 시) → 여전히 byte-exact 보장 안 됨 → test 는 schema/field 존재 검증 + mock로 결정론 확보.
- Golden lesson fixture: `internal/learning/testdata/golden_lesson_*.json`.

### 사용자 결정
- [ ] A — fail-closed only
- [ ] A + C — fail-closed + circuit breaker (추천)
- [ ] B — retry queue
- [ ] 기타:

---

## 부가 사항

### Scope 추정

사전 추천 (B/B/A/A/A/B/B/B/A/A+C) 선택 시:

| 작업 | 예상 LOC |
|---|---|
| `internal/learning/llm_extractor.go` (신규) | ~200 |
| `internal/learning/cursor.go` (신규, session cursor store) | ~80 |
| `internal/learning/lesson.go` 확장 (`Rationale`, `Evidence`, `PersonaDirection`) | ~20 |
| `internal/learning/store.go` schema 하위호환 patch | ~10 |
| `internal/llm/` extractor 용 shared client 훅 | ~30 |
| `cmd/elnath/runtime.go` + `cmd_daemon.go` wiring + config flag | ~40 |
| Circuit breaker (`internal/learning/breaker.go`) | ~60 |
| Tests (`llm_extractor_test.go`, mock provider, golden fixtures, cursor test) | ~250 |
| `cmd_lessons.go` — rationale 컬럼 표시 (list/show) | ~20 |
| **합계** | **~710** |

F-5 는 **Phase 1/2 분리** 권장:

- **Phase 1**: Lesson 스키마 확장 (Rationale/Evidence/PersonaDirection) + mock extractor + config flag + parallel mode wiring. LLM call은 mock만, 실제 provider 호출은 Phase 2. 테스트 전부 통과 → 커밋 → **실제 저장되는 lesson schema 1차 축적**.
- **Phase 2**: Anthropic Haiku provider 연결 + cursor + circuit breaker + complexity gate. Phase 1 의 mock 을 real 로 치환. 프로덕션 rollout.

### 미해결 / 다음 세션 재검토

- **Session complexity gate 정확한 threshold**: Q1-B 에서 `≥ 5 messages AND has_tool_call` 제안. 실제 daemon 로그 기준 실측 필요 (Phase 1 후 며칠 dog-food → tune).
- **Cache strategy**: Claude Code 의 prompt cache sharing (88% hit) 을 Elnath `internal/llm/anthropic.go` 가 지원하는지 미확인. Phase 2 구현 시 provider 쪽 cache header 확인 필요.
- **Telegram shell 세션**: 상시 open 세션에서 cursor 처리. Q3-A 가정은 "세션 종료 있음" — telegram shell 은 종료 없음. Idle timeout (예: 30분 무메시지) 또는 message count (예: 20 msgs 마다) trigger 필요.
- **Research path 와의 관계**: 현재 F-5 는 agent path 만 대상. Research path (`extractor.go` R1-R3 rule) 는 별도 phase (F-5.3?) 또는 F-5 Phase 2 에 포함.
- **Subagent**: Elnath 에도 subagent concept 있으나 (`orchestrator/team.go`), Claude Code 의 "main agent only" 제약 도입 여부. 현재 F-3.2 는 team-level 1 lesson → subagent 별 LLM fire는 비용 배수.

### Research-Before-Spec 재검증

이 문서는 내부 데이터 (실 production 0 lessons, rule 9개 source 읽음) + 외부 3 레퍼런스 (Claude Code extractMemories/autoDream 소스, Hermes memory_provider.py, openclaw dist) + cost 모델 (577 sessions 샘플, pricing page 검증) 세 축에서 작성. 얕은 리서치 트랩 (URL 1-2개) 회피 확인.

단, 다음 세션 전에 **데이터 축적 대기**가 필요 — daemon 재시작 (2026-04-14 09:33) 후 며칠 dog-food → lesson 분포 실측 → Q1-B gate threshold 와 Q5 model 선택 재검증 권장.

### 다음 세션 흐름

1. 위 Q1-Q10 에 사용자가 ✓ 체크 (또는 "기타" 답변)
2. daemon 재시작 후 수일간 축적된 lesson 분포 확인 (`elnath lessons stats`, `lessons list --json`) → 가정 재검증
3. 결정 내용 + 실측 데이터로 `docs/specs/PHASE-F5-LLM-LESSON-EXTRACTION.md` 작성
4. Phase 1/2 OpenCode 프롬프트 분리 작성
5. Phase 1 실행 → code-reviewer → 커밋 → Phase 2 (LLM provider 연결)

결정 전에 추가 조사가 필요하면 parallel read 로 `internal/llm/anthropic.go` / `internal/orchestrator/learning.go` 먼저 훑고 구체 구현 제약 확인 후 재제안.
