> **Archived**: This document is from the initial planning phase and may not reflect current implementation.

# Elnath v0.1 — Ultraplan Prompt

이 문서는 Elnath v0.1의 구현 계획을 수립하기 위한 self-contained 프롬프트입니다.
별도 세션에서 이 문서만으로 full implementation plan을 생성할 수 있어야 합니다.

---

## 1. 프로젝트 정의

### Elnath란?

**자율 AI 비서 플랫폼** — Claude Code급 코드 실행 품질을 가지면서, 범용 비서 능력(자연어 대화, 자율 작업, 지식 관리)을 겸비한 **독립 실행 Go daemon**.

기존 도구와의 포지셔닝:

| | 코딩 품질 | 범용 비서 | 지식 관리 | CLI 독립 |
|---|---|---|---|---|
| Claude Code | 최고 | 없음 | 없음 | N/A (자체가 CLI) |
| OpenClaw/Hermes | 보통 | 있음 | 기초적 | 독립 |
| **Elnath** | **최고** | **있음** | **Native Wiki** | **독립** |

### 핵심 차별점

1. **Native LLM Wiki** — Karpathy-style 구조화된 지식 베이스를 네이티브로 내장. 마크다운 파일(Obsidian 호환) + SQLite FTS5 인덱스. 에이전트가 작업할수록 더 똑똑해짐.

2. **자동 워크플로우 선택** — 유저가 슬래시 커맨드를 외울 필요 없음. 자연어로 의도를 말하면 Elnath가 맥락을 파악하여 최적 워크플로우(single/team/autopilot/ralph)를 자동 선택.

3. **독립 실행** — Claude Code CLI, Codex CLI 등 어떤 도구에도 종속되지 않음. 직접 LLM API를 호출하는 독립 Go daemon.

### 한 줄 피치
> "Claude Code처럼 뛰어나게 해내면서, 비서처럼 모든 것을 하고, 시간이 갈수록 더 똑똑해지는 AI."

---

## 2. 기술 결정 (확정)

| 항목 | 결정 | 이유 |
|------|------|------|
| 언어 | **Go** | 제작자의 주력 언어, Stella 코드 재사용, daemon/동시성 강점 |
| LLM | **Model-agnostic** | Anthropic, OpenAI, 로컬 모델 전부. OAuth 포함 |
| Wiki 저장 | **마크다운 + SQLite FTS5** | 마크다운 = Obsidian 호환 + git 추적. SQLite = 빠른 검색 |
| 배포 | **로컬 우선** (macOS/Linux) | 단일 바이너리, launchd/systemd |
| 실행 형태 | **CLI + daemon** | 대화형 CLI + 백그라운드 daemon 모드 |

### Stella에서 재사용 가능한 코드 (참고용, 직접 복사가 아닌 설계 참고)

Stella(`/Users/stello/stella/`)는 같은 제작자의 Go 프로젝트입니다. 아키텍처 패턴을 참고하되, Elnath는 독립 프로젝트로 처음부터 깨끗하게 작성합니다.

참고할 패턴:
- `internal/modelgateway/` — LLM API 추상화 (keypool, routing, pricing, config)
- `internal/self/` — Self 모델 (persona_tuner, conversation, operator_profile, embeddings)
- `internal/memory/` — 메모리 파이프라인 (belief_advisor, domain_store, llm_adapter, pipeline)
- `internal/workflow/` — 태스크 엔진 (engine, file_store, lifecycle)
- `internal/agents/` — 에이전트 수명주기 관리

---

## 3. v0.1 범위 (Acceptance Criteria)

### 5개 스모크 테스트 — 모두 통과해야 출시 가능

**ST-1. End-to-end 프로젝트 생성**
```
유저: "새 Go REST API 프로젝트 만들어줘"
Elnath: 
  → 의도 파악 (프로젝트 생성)
  → autopilot 워크플로우 자동 선택
  → plan 생성 → 코드 작성 → 테스트 작성 → 실행 → 완료
  → 결과를 wiki에 기록
```

**ST-2. 지식 검색 답변**
```
유저: "어제 Stella에서 뭐 바꿨어?"
Elnath:
  → wiki search 워크플로우 자동 선택
  → Wiki에서 검색 (FTS5 + 마크다운 스캔)
  → 정확한 답변 반환 (커밋, 변경 내역 등)
```

**ST-3. 자율 배치 작업**
```
유저: "이 10개 버그 이슈 밤새 처리해줘"
Elnath:
  → team 워크플로우 자동 선택 (병렬)
  → 밤새 자율 실행
  → 아침에 7개+ 해결, 결과 보고
```

**ST-4. 자동 워크플로우 선택 + Wiki 기록**
```
유저: "Stella의 테스트 커버리지를 80%로 올려줘"
Elnath:
  → Wiki에서 현재 상태 확인 (READ)
  → team 워크플로우 자동 선택 (3+ 파일, 독립적)
  → 실행 완료
  → 결과를 Wiki에 기록 (WRITE)
```

**ST-5. Autoresearch 루프**
```
유저: "Stella scalping 전략 개선 방법 찾아줘"
Elnath:
  → autoresearch 워크플로우 자동 선택
  → 현재 백테스트 결과 분석 (Wiki READ)
  → 가설 생성 + 실험 설계
  → 자동 실험 실행 + 결과 평가
  → 개선안을 Wiki에 기록
```

### Non-Goals (v0.1에서 하지 않는 것)

- 트레이딩 기능 (Stella 영역)
- 멀티 플랫폼 UI (Telegram, Discord, 웹 = v0.2+)
- Self Override 아키텍처 (v0.2+)
- Sub-system 연동 — Stella/Ludus 연동 (v0.2+)
- Meta-autoresearch — 자기 워크플로우 최적화 (v0.2)
- 특정 LLM/CLI 벤더 종속
- GitHub 이슈 자동 수정 전용 기능 (Ludus 영역)

---

## 4. 아키텍처 (제안, 플래닝 세션에서 정제)

```
elnath/
├── cmd/elnath/                  # CLI + daemon 엔트리포인트
│   └── main.go
├── internal/
│   ├── core/                    # App 구조체, 라이프사이클, Close()
│   ├── llm/                     # LLM API 추상화 (model-agnostic)
│   │   ├── provider.go          # Provider 인터페이스
│   │   ├── anthropic.go         # Claude API + OAuth
│   │   ├── openai.go            # OpenAI / Codex API
│   │   └── local.go             # 로컬 모델 (ollama 등)
│   ├── tools/                   # 도구 실행 레이어
│   │   ├── executor.go          # Tool 인터페이스 + 디스패처
│   │   ├── bash.go              # Shell 실행 (sandbox 옵션)
│   │   ├── file.go              # 파일 읽기/쓰기/검색/glob
│   │   ├── git.go               # Git 작업
│   │   └── web.go               # 웹 fetch/검색
│   ├── orchestrator/            # 워크플로우 오케스트레이션
│   │   ├── router.go            # 맥락 기반 자동 워크플로우 선택
│   │   ├── single.go            # 단일 에이전트 실행
│   │   ├── team.go              # 병렬 에이전트 팀
│   │   ├── autopilot.go         # End-to-end 자동 파이프라인
│   │   ├── ralph.go             # 검증까지 반복 루프
│   │   └── types.go             # 공유 타입
│   ├── wiki/                    # Native LLM Wiki (핵심 차별점)
│   │   ├── store.go             # 마크다운 파일 CRUD
│   │   ├── index.go             # SQLite FTS5 인덱싱
│   │   ├── ingest.go            # 소스 자동 수집 (git, 대화, 파일)
│   │   ├── lint.go              # 건강 검진 (모순, stale, orphan)
│   │   ├── search.go            # 하이브리드 검색
│   │   └── schema.go            # 페이지 타입 정의 (entity, concept, source)
│   ├── research/                # Autoresearch 엔진
│   │   ├── loop.go              # 가설→실험→평가 루프
│   │   ├── hypothesis.go        # LLM 기반 가설 생성
│   │   └── experiment.go        # 자동 실험 실행 + 결과 수집
│   ├── self/                    # Self 모델
│   │   ├── state.go             # SelfState 관리
│   │   ├── identity.go          # 정체성, 미션
│   │   ├── persona.go           # 페르소나 파라미터
│   │   └── beliefs.go           # 신념 수명주기
│   ├── conversation/            # 대화 관리
│   │   ├── intent.go            # 의도 파악 + 워크플로우 라우팅
│   │   ├── context.go           # 컨텍스트 윈도우 관리
│   │   ├── history.go           # 대화 이력 (SQLite)
│   │   └── clarify.go           # 불명확 시 질문 생성
│   └── config/                  # 설정 관리
│       ├── config.go            # YAML/env 설정 로더
│       └── defaults.go          # 기본값
├── wiki/                        # 기본 위키 데이터
│   ├── index.md
│   └── log.md
├── go.mod
├── go.sum
├── Makefile
├── README.md
└── CLAUDE.md
```

### 워크플로우 자동 선택 로직 (router.go)

```
유저 입력 → LLM 의도 분류
│
├── intent: question        → wiki search (답변만)
├── intent: simple_task     → single agent (파일 1-2개)
├── intent: complex_task    → team (3+ 파일, 병렬 가능)
├── intent: project         → autopilot (plan→code→test→verify)
├── intent: verify_critical → ralph (반복 검증 루프)
├── intent: research        → autoresearch loop
├── intent: unclear         → clarify (질문 생성)
└── intent: conversation    → direct reply (도구 불필요)
```

### LLM Provider 인터페이스

```go
type Provider interface {
    Chat(ctx context.Context, messages []Message, opts ...Option) (Response, error)
    Stream(ctx context.Context, messages []Message, opts ...Option) (<-chan StreamEvent, error)
    Name() string
    Models() []string
}
```

### Tool 인터페이스

```go
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage  // JSON Schema for parameters
    Execute(ctx context.Context, params json.RawMessage) (Result, error)
}
```

---

## 5. 구현 순서 (제안, 플래닝 세션에서 정제)

### Phase 0: 프로젝트 스캐폴딩 (Day 1)
- go mod init, 디렉토리 구조, Makefile, CLAUDE.md
- 기본 CLI (cobra 또는 자체 구현)
- 설정 로더 (config.go)

### Phase 1: LLM Provider + Tool Executor (Day 2-3)
- Provider 인터페이스 + Anthropic 구현 (최우선)
- OpenAI 구현
- Tool 인터페이스 + bash, file, git 도구
- 기본 대화 루프 (CLI에서 입력 → LLM → 도구 사용 → 출력)
- **검증: 대화하면서 파일을 읽고 수정할 수 있다**

### Phase 2: 워크플로우 오케스트레이션 (Day 4-6)
- Single agent 워크플로우 (기본)
- Intent router (LLM 기반 의도 분류)
- Team 워크플로우 (goroutine 기반 병렬 실행)
- Autopilot 워크플로우 (plan→code→test→verify)
- Ralph 워크플로우 (반복 검증)
- **검증: ST-1 통과 (end-to-end 프로젝트 생성)**

### Phase 3: Native Wiki (Day 7-9)
- 마크다운 파일 CRUD (store.go)
- SQLite FTS5 인덱스 (index.go)
- 검색 (keyword + FTS5 하이브리드)
- Ingest (git log → wiki, 대화 결과 → wiki)
- Lint (stale 감지, cross-reference 검증)
- **검증: ST-2 통과 (지식 검색 답변)**

### Phase 4: 자율 운영 + Autoresearch (Day 10-12)
- Daemon 모드 (백그라운드 실행)
- 배치 작업 큐 + 자율 실행
- Autoresearch 루프 (hypothesis → experiment → evaluate)
- **검증: ST-3 (배치 작업), ST-5 (autoresearch) 통과**

### Phase 5: 통합 + 품질 (Day 13-15)
- ST-4 통과 (자동 워크플로우 + wiki 기록)
- 에러 처리, 로깅, graceful shutdown
- Self 모델 기본 구현 (identity, persona)
- 전체 스모크 테스트 5개 통과
- README, 문서화

---

## 6. 품질 기준

- **Bellman 프로젝트와 대등한 완성도가 아니면 출시 불가**
- 모든 public 함수에 테스트
- go vet, staticcheck 통과
- 단일 바이너리 빌드 (`go build`)
- README에 30초 데모 GIF 또는 스크린캐스트
- 깔끔한 에러 메시지 (유저 친화적)

---

## 7. 경쟁 분석 (참고)

### Bellman (Yeachan-Heo)의 생태계
- oh-my-claudecode (25K stars): Claude Code 멀티 에이전트 하네스
- oh-my-codex (17K stars): Codex 멀티 에이전트 하네스
- clawhip: 이벤트 라우팅 인프라
- claw-code (174K stars, ultraworkers org): Rust Claude Code 구현

**Bellman의 한계 (Elnath의 기회):**
- CLI 종속 — 하네스는 Claude Code/Codex CLI 없이 작동 불가
- 수동 워크플로우 선택 — 유저가 /team, /autopilot 등을 직접 선택해야 함
- 지식 관리 없음 — 세션 간 학습/기억 메커니즘 없음

### OpenClaw / Hermes
- 범용 비서 (멀티 플랫폼, 자율 작업)
- 실행 품질이 "보통" — 코딩 특화 최적화 없음
- 메모리가 평면적 (구조화된 wiki 아님)

---

## 8. 제작자 프로필

- 한국계 캐나다인 개발자
- Go 주력 (Stella 5만줄+ Go 프로젝트 단독 개발)
- 기존 프로젝트: Stella (DeFi 트레이딩 에이전트, 라이브), Orbis (L1 블록체인), Ludus (GitHub autofix)
- LLM Wiki 운영 중 (/Users/stello/llm_memory/Claude Valut/, 30+ 페이지)
- 목표: Bellman 수준의 업계 임팩트

---

## 9. 플래닝 세션에 요청하는 것

이 spec을 기반으로 **Elnath v0.1의 상세 구현 계획**을 작성해주세요:

1. **Phase별 상세 태스크 리스트** — 각 태스크에 파일 경로, 핵심 함수 시그니처, 예상 줄 수
2. **의존성 그래프** — 어떤 태스크가 어떤 태스크에 의존하는지
3. **병렬화 가능 태스크 식별** — 동시에 진행 가능한 것들
4. **리스크 분석** — 기술적으로 어려운 부분, 미지의 영역
5. **테스트 전략** — 각 Phase에서 무엇을 어떻게 검증하는지
6. **Stella 코드 참고 매핑** — Elnath의 어떤 모듈이 Stella의 어떤 패턴을 참고하는지

프로젝트 경로: `/Users/stello/elnath/`

---

## Appendix A: Stella 코드 패턴 (참고용)

웹 세션에서 로컬 파일을 읽을 수 없으므로 핵심 인터페이스를 여기에 포함합니다.
Elnath는 이 패턴을 **참고**하되, 독립적으로 깨끗하게 재작성합니다.

### A1. LLM Gateway 타입 (modelgateway/contracts.go)

```go
type StreamChunk struct {
    Content      string `json:"content"`
    Done         bool   `json:"done"`
    InputTokens  int    `json:"input_tokens,omitempty"`
    OutputTokens int    `json:"output_tokens,omitempty"`
}

type StreamCallback func(chunk StreamChunk)

type InvocationPlan struct {
    Provider       string
    ModelID        string
    NeedsModel     bool
    MaxCostUSD     float64
    MaxLatencyMS   int
    Reason         string
    APIKeyOverride string
}

type InvocationRequest struct {
    Task    domain.TaskEnvelope
    Context domain.CompiledContext
    Plan    InvocationPlan
}

type InvocationResult struct {
    Provider     string
    ModelID      string
    Confidence   float64
    Summary      string
    Evidence     []string
    InputTokens  int
    OutputTokens int
    CostUSD      float64
}
```

### A2. Workflow Engine (workflow/)

```go
// Store 인터페이스 — 태스크 큐 + 상태 추적
type Store interface {
    Enqueue(task domain.TaskEnvelope)
    UpsertRecord(record TaskRecord)
    Next() (domain.TaskEnvelope, bool)
    MarkRunning(taskID string)
    MarkDone(taskID string, result HandleResult)
    ListRecords() []TaskRecord
    Stats() Stats
}

// TaskHandler — 각 워크플로우가 구현
type TaskHandler interface {
    Handle(ctx context.Context, task domain.TaskEnvelope) (HandleResult, error)
}

type HandleResult struct {
    TaskID     string
    Action     string
    Confidence float64
    Strategy   string
    NextTasks  []domain.TaskEnvelope  // 다음 태스크 체이닝
}
```

### A3. Memory Pipeline (memory/)

```go
// LLM 호출 인터페이스
type LLMCaller interface {
    Call(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// Extract 단계 출력
type CandidateFact struct {
    Text     string `json:"text"`
    Category string `json:"category"`
    Type     string `json:"type"` // "belief", "lesson", "procedure"
}

// Judge 단계 출력
type MemoryAction struct {
    Event    string `json:"event"` // "ADD", "UPDATE", "DELETE", "NONE"
    ID       string `json:"id"`
    Text     string `json:"text"`
    Category string `json:"category,omitempty"`
}

// 신념 모델
type Belief struct {
    ID            string  `json:"id"`
    Text          string  `json:"text"`
    Confidence    float64 `json:"confidence"`
    Status        string  `json:"status"` // hypothesis → forming → established
    Source        string  `json:"source"` // experience | operator | inference
    Domain        string  `json:"domain"`
    EvidenceCount int     `json:"evidence_count"`
}

// 교훈 모델
type Lesson struct {
    ID           string   `json:"id"`
    WhatHappened string   `json:"what_happened"`
    LessonText   string   `json:"lesson"`
    Impact       string   `json:"impact"` // positive | negative | neutral
    Confidence   float64  `json:"confidence"`
}
```

### A4. Self 모델 (self/)

```go
type stateFile struct {
    Identity     Identity                  `json:"identity"`
    Constitution []domain.ConstitutionRule `json:"constitution"`
    Persona      Persona                  `json:"persona"`
    State        RuntimeState             `json:"state"`
}

// Persona — PersonaTuner가 자율 조정
// Tone, Directness, Confidence, CautionBias, AggroBias 등

// Service — SQLite DB 기반 (Close() 필수)
// DB는 대화 이력, 임베딩, LCM 트리 등을 저장
```

---

## Appendix B: 기존 LLM Wiki 구조 (참고용)

현재 `/Users/stello/llm_memory/Claude Valut/`에 30개 페이지가 있습니다.
Elnath의 Native Wiki는 이 구조를 **네이티브로 내장**합니다.

```
wiki/
├── index.md              # 전체 페이지 카탈로그
├── log.md                # append-only 변경 기록
├── entities/             # 프로젝트, 조직, 인물
│   ├── stella.md
│   ├── orbis.md
│   ├── ludus.md
│   └── elnath.md
├── concepts/             # 개념, 이론, 프레임워크
│   ├── 자율-에이전트-아키텍처.md
│   ├── DeFi-트레이딩-전략.md
│   ├── fail-closed-설계-철학.md
│   └── self-override-아키텍처.md
├── sources/              # 소스 문서 요약
│   ├── stella-readme.md
│   ├── stella-architecture.md
│   └── ... (17개)
├── analyses/             # 분석, 질의 결과
│   └── stella-제작-동기와-비전.md
└── maps/                 # 비교표, 타임라인
    └── stella-orbis-프로젝트-비교.md
```

**페이지 frontmatter 형식:**
```yaml
---
title: "페이지 제목"
type: entity | concept | source | analysis | map
created: 2026-04-06
updated: 2026-04-06
ttl: permanent | 30d | snapshot
confidence: high | mixed | low
tags: [태그1, 태그2]
sources: [관련/소스/경로]
---
```

**Karpathy 원칙:**
- 새 소스 1개 ingestion 시 10-15개 페이지를 동시에 갱신
- index.md를 먼저 읽어 관련 페이지를 식별
- log.md에 모든 변경을 append-only로 기록
- 정기 lint: 모순, stale claims, orphan pages 탐지

---

## Appendix C: 네이밍 규칙

- **Bellman의 프로젝트명 사용 금지**: "OMC", "OMX", "oh-my-claudecode", "oh-my-codex" 등 Bellman 프로젝트 고유 이름을 Elnath 코드/문서에서 사용하지 않음
- **Generic 워크플로우 이름은 OK**: "autopilot", "ralph", "team", "single" 등은 일반 용어로 사용 가능
- **Elnath 고유 용어 체계**: 독자적 네이밍을 수립할 것 (플래닝 시 제안 환영)
