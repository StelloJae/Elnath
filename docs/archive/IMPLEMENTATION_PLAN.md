> **Archived**: This document is from the initial planning phase and may not reflect current implementation.

# Elnath v0.1 — Implementation Plan

> 작성일: 2026-04-07
> 최종 합의: 2026-04-07 (Ralplan — Planner/Architect/Critic 3-agent consensus)
> 기반: ULTRAPLAN_PROMPT.md + Stella 코드베이스 4개 영역 분석
> ADR: [ADR-001-v01-architecture.md](ADR-001-v01-architecture.md)
> 목표: 5개 스모크 테스트 전부 통과, 단일 바이너리 빌드
> 전략: **Option B** — ST-3 축소 + Phase 0 인프라 강화 + Anthropic-first, 14일

---

## 0. 아키텍처 확정

### 디렉토리 구조 (최종 — Architect AD-1 반영)

```
elnath/
├── cmd/elnath/
│   ├── main.go                    # 엔트리포인트, 시그널, 패닉 복구
│   └── commands.go                # 커스텀 CLI 디스패처 (Cobra 미사용)
├── internal/
│   ├── core/                      # App 라이프사이클 (app.go만)
│   │   ├── app.go                 # App 구조체, Init, Close
│   │   ├── logger.go              # slog 구조화 로깅 [P0-9]
│   │   ├── errors.go              # sentinel errors + 래핑 헬퍼 [P0-10]
│   │   └── db.go                  # SQLite DB 매니저 (WAL, busy_timeout) [P0-11]
│   ├── agent/                     # 에이전틱 루프 (AD-1: core에서 분리)
│   │   └── agent.go               # message→LLM→tools→repeat + LLM API 재시도 [CF-1]
│   ├── daemon/                    # 백그라운드 모드 (AD-1: core에서 분리)
│   │   ├── daemon.go              # daemon 모드 + launchd plist
│   │   ├── queue.go               # 배치 작업 큐 (SQLite) + stale task 복구
│   │   └── ipc.go                 # Unix domain socket + JSON-line [GAP-5]
│   ├── llm/                       # LLM API 추상화
│   │   ├── provider.go            # Provider 인터페이스 (AD-2: stream callback)
│   │   ├── message.go             # Message, Response, StreamEvent, ToolCall 타입
│   │   ├── toolconv.go            # Anthropic/OpenAI tool_use ↔ ToolCall 변환 [GAP-3]
│   │   ├── anthropic.go           # Claude API (Chat + SSE Stream + tool_use)
│   │   ├── openai.go              # OpenAI API (AD-3: Chat-only, tool_use v0.2)
│   │   ├── registry.go            # Provider 레지스트리 + 라우팅
│   │   ├── keypool.go             # 멀티키 로테이션 + 쿨다운
│   │   └── usage.go               # 사용량/비용 추적 (SQLite)
│   ├── tools/                     # 도구 실행 레이어
│   │   ├── tool.go                # Tool 인터페이스 + Result 타입
│   │   ├── registry.go            # 도구 레지스트리 + JSON Schema 디스패치
│   │   ├── schema.go              # JSON Schema 빌더 유틸 [GAP-2]
│   │   ├── bash.go                # Shell 실행 (timeout, sandbox)
│   │   ├── file.go                # Read, Write, Edit, Glob, Grep
│   │   ├── git.go                 # status, diff, commit, log, branch
│   │   └── web.go                 # HTTP fetch, 검색
│   ├── conversation/              # 대화 관리
│   │   ├── manager.go             # 세션 관리, 턴 기록
│   │   ├── intent.go              # LLM 기반 의도 분류
│   │   ├── context.go             # 컨텍스트 윈도우 관리 + 압축
│   │   └── history.go             # SQLite 대화 이력 + FTS5
│   ├── orchestrator/              # 워크플로우 오케스트레이션
│   │   ├── router.go              # 의도→워크플로우 자동 매핑
│   │   ├── single.go              # 단일 에이전트 (기본)
│   │   ├── team.go                # goroutine 병렬 에이전트
│   │   ├── autopilot.go           # plan→code→test→verify 파이프라인
│   │   ├── ralph.go               # 검증 반복 루프
│   │   └── types.go               # WorkflowResult, WorkflowConfig
│   ├── wiki/                      # Native LLM Wiki
│   │   ├── store.go               # 마크다운 파일 CRUD + frontmatter
│   │   ├── index.go               # SQLite FTS5 인덱싱 + 동기화
│   │   ├── search.go              # 하이브리드 검색 (FTS5 + 경로 매칭)
│   │   ├── ingest.go              # 소스 자동 수집 (git, 대화, 파일)
│   │   ├── lint.go                # 건강 점검 (모순, stale, orphan)
│   │   ├── schema.go              # 페이지 타입 + frontmatter 파싱
│   │   └── tool.go                # WikiRead/WikiWrite/WikiSearch 도구
│   ├── research/                  # Autoresearch 엔진
│   │   ├── loop.go                # 가설→실험→평가 반복
│   │   ├── hypothesis.go          # LLM 기반 가설 생성
│   │   └── experiment.go          # 자동 실험 + 결과 수집
│   ├── self/                      # Self 모델 (v0.1 기본만)
│   │   ├── state.go               # SelfState 로드/저장
│   │   ├── identity.go            # 이름, 미션, 성격
│   │   └── persona.go             # 페르소나 파라미터 + 자동 조정
│   └── config/                    # 설정 관리
│       ├── config.go              # YAML + env 오버라이드
│       └── defaults.go            # 기본값
├── wiki/                          # 기본 위키 데이터 (시드)
│   ├── index.md
│   └── log.md
├── go.mod
├── go.sum
├── Makefile
├── README.md
└── CLAUDE.md
```

### 핵심 인터페이스 (확정)

```go
// --- LLM Provider (AD-2: stream callback 패턴) ---
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    Stream(ctx context.Context, req ChatRequest, cb func(StreamEvent)) error
    Name() string
    Models() []ModelInfo
}

type ChatRequest struct {
    Model       string
    Messages    []Message
    Tools       []ToolDef
    MaxTokens   int
    Temperature float64
    System      string
}

type ChatResponse struct {
    Content     string
    ToolCalls   []ToolCall
    Usage       Usage
    StopReason  string
}

// --- Tool ---
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage
    Execute(ctx context.Context, params json.RawMessage) (*Result, error)
}

type Result struct {
    Output  string
    IsError bool
}

// --- Orchestrator ---
type Workflow interface {
    Name() string
    Run(ctx context.Context, input WorkflowInput) (*WorkflowResult, error)
}

type WorkflowInput struct {
    Message     string
    Session     *Session
    WikiContext []wiki.Page
    Tools       []Tool
    Provider    Provider
}

// --- Wiki ---
type Page struct {
    Path       string
    Title      string
    Type       string    // entity | concept | source | analysis | map
    Content    string
    Tags       []string
    Created    time.Time
    Updated    time.Time
    TTL        string
    Confidence string
}
```

---

## 1. Phase별 상세 태스크 리스트

### Phase 0: 프로젝트 스캐폴딩 (Day 1)

| ID | 태스크 | 파일 | 핵심 함수/타입 | LOC | 의존 |
|----|--------|------|----------------|-----|------|
| P0-1 | go mod init + 디렉토리 생성 | `go.mod`, dirs | — | 10 | — |
| P0-2 | Makefile (build, test, lint, run) | `Makefile` | build, test, lint, run | 40 | P0-1 |
| P0-3 | CLAUDE.md 초안 | `CLAUDE.md` | — | 50 | P0-1 |
| P0-4 | 설정 로더 | `internal/config/config.go` | `Load(path) (*Config, error)` | 120 | P0-1 |
| P0-5 | 기본값 정의 | `internal/config/defaults.go` | `DefaultConfig() *Config` | 50 | P0-4 |
| P0-6 | CLI 엔트리포인트 | `cmd/elnath/main.go` | `main()`, signal handling | 80 | P0-1 |
| P0-7 | 커맨드 디스패처 | `cmd/elnath/commands.go` | `executeCommand()`, `commandRegistry()` | 100 | P0-6 |
| P0-8 | App 구조체 기본 | `internal/core/app.go` | `App{}`, `New()`, `Close()` | 80 | P0-4 |

**Phase 0 합계: ~530 LOC**
**검증: `make build` → 단일 바이너리, `elnath version` 동작**

---

### Phase 1: LLM Provider + Tool Executor (Day 2-3)

| ID | 태스크 | 파일 | 핵심 함수/타입 | LOC | 의존 |
|----|--------|------|----------------|-----|------|
| P1-1 | Provider 인터페이스 | `internal/llm/provider.go` | `Provider`, `StreamEvent`, `ChatRequest`, `ChatResponse` | 80 | P0 |
| P1-2 | Message 타입 정의 | `internal/llm/message.go` | `Message`, `ToolCall`, `ToolDef`, `Usage`, `ModelInfo` | 100 | P1-1 |
| P1-3 | Anthropic 구현 | `internal/llm/anthropic.go` | `AnthropicProvider.Chat()`, `.Stream()`, SSE 파싱 | 300 | P1-1,2 |
| P1-4 | OpenAI 구현 | `internal/llm/openai.go` | `OpenAIProvider.Chat()`, `.Stream()`, SSE 파싱 | 280 | P1-1,2 |
| P1-5 | 로컬 모델 (Ollama) | `internal/llm/local.go` | `OllamaProvider.Chat()`, `.Stream()` | 120 | P1-1,2 |
| P1-6 | Provider 레지스트리 | `internal/llm/registry.go` | `Registry.Get()`, `.Invoke()`, fallback | 120 | P1-1 |
| P1-7 | KeyPool | `internal/llm/keypool.go` | `KeyPool.Next()`, cooldown, rotation | 150 | P1-6 |
| P1-8 | 사용량 추적 | `internal/llm/usage.go` | `UsageTracker.Record()`, `.Stats()`, SQLite | 180 | P1-6 |
| P1-9 | Tool 인터페이스 | `internal/tools/tool.go` | `Tool`, `Result`, `ToolCall` | 60 | P0 |
| P1-10 | Tool 레지스트리 | `internal/tools/registry.go` | `Registry.Execute()`, JSON Schema dispatch | 80 | P1-9 |
| P1-11 | Bash 도구 | `internal/tools/bash.go` | `BashTool.Execute()`, timeout, working dir | 200 | P1-9 |
| P1-12 | File 도구 | `internal/tools/file.go` | `ReadTool`, `WriteTool`, `EditTool`, `GlobTool`, `GrepTool` | 350 | P1-9 |
| P1-13 | Git 도구 | `internal/tools/git.go` | `GitTool.Execute()`, status/diff/commit/log | 150 | P1-9 |
| P1-14 | Web 도구 | `internal/tools/web.go` | `WebFetchTool`, `WebSearchTool` | 150 | P1-9 |
| P1-15 | 에이전틱 루프 | `internal/core/agent.go` | `Agent.Run()`: message→LLM→tools→repeat | 400 | P1-3,6,10 |
| P1-16 | CLI `run` 커맨드 연결 | `cmd/elnath/commands.go` 수정 | `runChat()` | 50 | P1-15,P0-7 |

**Phase 1 합계: ~2,770 LOC**
**검증: `elnath run` → 대화하면서 파일을 읽고 수정할 수 있다**

#### P1-15 Agent.Run() 상세 설계

```go
func (a *Agent) Run(ctx context.Context, input string) (*AgentResult, error) {
    messages := a.buildMessages(input)  // system + history + user

    for iteration := 0; iteration < a.maxIterations; iteration++ {
        resp, err := a.provider.Chat(ctx, ChatRequest{
            Model:    a.model,
            Messages: messages,
            Tools:    a.toolDefs(),
            System:   a.systemPrompt,
        })
        if err != nil { return nil, err }

        // 도구 호출 없으면 최종 응답
        if len(resp.ToolCalls) == 0 {
            return &AgentResult{Content: resp.Content, Usage: totalUsage}, nil
        }

        // 도구 실행
        messages = append(messages, assistantMessage(resp))
        for _, call := range resp.ToolCalls {
            result, err := a.tools.Execute(ctx, call.Name, call.Input)
            messages = append(messages, toolResultMessage(call.ID, result))
        }
    }
    return nil, ErrMaxIterations
}
```

---

### Phase 2: 워크플로우 오케스트레이션 (Day 4-6)

| ID | 태스크 | 파일 | 핵심 함수/타입 | LOC | 의존 |
|----|--------|------|----------------|-----|------|
| P2-1 | 대화 매니저 | `internal/conversation/manager.go` | `Manager.NewSession()`, `.SendMessage()`, `.GetHistory()` | 250 | P1-15 |
| P2-2 | 의도 분류 | `internal/conversation/intent.go` | `ClassifyIntent()` → question/simple_task/complex_task/project/research/unclear/chat | 200 | P1-3 |
| P2-3 | 컨텍스트 관리 | `internal/conversation/context.go` | `ContextWindow.Fit()`, token counting, 압축 | 250 | P2-1 |
| P2-4 | 대화 이력 (SQLite) | `internal/conversation/history.go` | `HistoryStore.Save()`, `.Load()`, `.Search()`, FTS5 | 250 | P2-1 |
| P2-5 | 워크플로우 타입 | `internal/orchestrator/types.go` | `Workflow`, `WorkflowInput`, `WorkflowResult`, `WorkflowConfig` | 80 | P1 |
| P2-6 | 의도→워크플로우 라우터 | `internal/orchestrator/router.go` | `Router.Route(intent, context) Workflow` | 250 | P2-2,5 |
| P2-7 | Single 워크플로우 | `internal/orchestrator/single.go` | `SingleWorkflow.Run()`: Agent.Run() 1회 | 150 | P1-15,P2-5 |
| P2-8 | Team 워크플로우 | `internal/orchestrator/team.go` | `TeamWorkflow.Run()`: goroutine 병렬, 결과 합산 | 350 | P1-15,P2-5 |
| P2-9 | Autopilot 워크플로우 | `internal/orchestrator/autopilot.go` | `AutopilotWorkflow.Run()`: plan→code→test→verify 단계 | 300 | P2-7,8 |
| P2-10 | Ralph 워크플로우 | `internal/orchestrator/ralph.go` | `RalphWorkflow.Run()`: 최대 N회 반복 + 검증 조건 | 250 | P2-7 |
| P2-11 | CLI 통합 (대화 세션) | `cmd/elnath/commands.go` 수정 | `runInteractive()`: readline + orchestrator | 100 | P2-1,6 |

**Phase 2 합계: ~2,430 LOC**
**검증: ST-1 통과 — "새 Go REST API 프로젝트 만들어줘" → autopilot 자동 선택 → plan→code→test→완료**

#### P2-6 Router 상세 설계

```go
func (r *Router) Route(intent Intent, ctx *ConversationContext) Workflow {
    // 1차: 의도 기반 직접 매핑
    switch intent {
    case IntentQuestion:
        if r.wikiAvailable() {
            return r.workflows["search"]
        }
        return r.workflows["single"]
    case IntentSimpleTask:
        return r.workflows["single"]
    case IntentComplexTask:
        return r.workflows["team"]
    case IntentProject:
        return r.workflows["autopilot"]
    case IntentVerifyCritical:
        return r.workflows["ralph"]
    case IntentResearch:
        return r.workflows["research"]
    case IntentUnclear:
        return r.workflows["clarify"]  // single + clarification prompt
    case IntentChat:
        return r.workflows["single"]   // no tools
    }

    // 2차: 휴리스틱 (파일 수, 독립성 판단)
    if ctx.EstimatedFiles > 3 {
        return r.workflows["team"]
    }
    return r.workflows["single"]
}
```

#### P2-8 Team 워크플로우 상세 설계

```go
func (t *TeamWorkflow) Run(ctx context.Context, input WorkflowInput) (*WorkflowResult, error) {
    // 1. LLM에게 작업 분할 요청
    subtasks, err := t.planSubtasks(ctx, input)

    // 2. goroutine 병렬 실행
    results := make(chan SubtaskResult, len(subtasks))
    var wg sync.WaitGroup
    for _, st := range subtasks {
        wg.Add(1)
        go func(task Subtask) {
            defer wg.Done()
            agent := NewAgent(t.provider, t.tools, task.SystemPrompt)
            res, err := agent.Run(ctx, task.Input)
            results <- SubtaskResult{Task: task, Result: res, Err: err}
        }(st)
    }

    // 3. 결과 수집 + 합산
    go func() { wg.Wait(); close(results) }()
    var all []SubtaskResult
    for r := range results {
        all = append(all, r)
    }

    // 4. LLM에게 결과 종합 요청
    return t.synthesize(ctx, input, all)
}
```

---

### Phase 3: Native Wiki (Day 7-9)

| ID | 태스크 | 파일 | 핵심 함수/타입 | LOC | 의존 |
|----|--------|------|----------------|-----|------|
| P3-1 | 페이지 스키마 + frontmatter | `internal/wiki/schema.go` | `Page`, `PageType`, `ParseFrontmatter()`, `RenderFrontmatter()` | 120 | P0 |
| P3-2 | 마크다운 CRUD | `internal/wiki/store.go` | `Store.Create()`, `.Read()`, `.Update()`, `.Delete()`, `.List()` | 300 | P3-1 |
| P3-3 | SQLite FTS5 인덱스 | `internal/wiki/index.go` | `Index.Rebuild()`, `.Upsert()`, `.Remove()`, FTS5 테이블 | 350 | P3-2 |
| P3-4 | 하이브리드 검색 | `internal/wiki/search.go` | `Search(query, opts) []SearchResult`, FTS5 + 경로 매칭 + 태그 필터 | 250 | P3-3 |
| P3-5 | 소스 수집 (Ingest) | `internal/wiki/ingest.go` | `IngestGitLog()`, `IngestConversation()`, `IngestFile()` | 350 | P3-2,P1-3 |
| P3-6 | 건강 점검 (Lint) | `internal/wiki/lint.go` | `Lint() []Issue`, stale 감지, orphan 감지, 모순 플래그 | 250 | P3-2,4 |
| P3-7 | Wiki 도구 (에이전트용) | `internal/wiki/tool.go` | `WikiReadTool`, `WikiWriteTool`, `WikiSearchTool` | 180 | P3-2,4,P1-9 |
| P3-8 | index.md 자동 갱신 | `internal/wiki/store.go` 확장 | `Store.rebuildIndex()` | 100 | P3-2 |
| P3-9 | log.md append-only 기록 | `internal/wiki/store.go` 확장 | `Store.appendLog()` | 60 | P3-2 |
| P3-10 | 시드 위키 데이터 | `wiki/index.md`, `wiki/log.md` | — | 30 | P3-2 |

**Phase 3 합계: ~1,990 LOC**
**검증: ST-2 통과 — "어제 Stella에서 뭐 바꿨어?" → wiki search → 정확한 답변**

#### P3-3 FTS5 스키마

```sql
CREATE TABLE wiki_pages (
    path        TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    type        TEXT NOT NULL,
    content     TEXT NOT NULL,
    tags        TEXT,          -- JSON array
    confidence  TEXT,
    ttl         TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE VIRTUAL TABLE wiki_fts USING fts5(
    title, content, tags,
    content='wiki_pages',
    content_rowid='rowid',
    tokenize='unicode61 remove_diacritics 2'
);

-- 트리거: INSERT/UPDATE/DELETE 시 FTS 동기화
CREATE TRIGGER wiki_pages_ai AFTER INSERT ON wiki_pages BEGIN
    INSERT INTO wiki_fts(rowid, title, content, tags)
    VALUES (new.rowid, new.title, new.content, new.tags);
END;
-- UPDATE, DELETE 트리거도 동일 패턴
```

#### P3-5 Ingest 파이프라인

```go
func (s *Store) IngestGitLog(ctx context.Context, repoPath string, since time.Time) error {
    // 1. git log --since=... --format="%H|%s|%an|%ai" 실행
    // 2. 각 커밋을 LLM에게 요약 요청
    // 3. 관련 wiki 페이지 식별 (index.md 기반)
    // 4. 페이지 갱신 또는 신규 생성
    // 5. log.md에 변경 기록 append
}

func (s *Store) IngestConversation(ctx context.Context, turns []Turn) error {
    // 1. LLM에게 대화에서 기록할 만한 사실 추출 요청
    // 2. 각 사실을 기존 페이지와 대조
    // 3. 갱신/생성 + index/log 갱신
}
```

---

### Phase 4: 자율 운영 + Autoresearch (Day 10-12)

| ID | 태스크 | 파일 | 핵심 함수/타입 | LOC | 의존 |
|----|--------|------|----------------|-----|------|
| P4-1 | Daemon 모드 | `internal/core/daemon.go` | `Daemon.Start()`, `.Stop()`, launchd plist 생성 | 250 | P0-8 |
| P4-2 | 배치 작업 큐 | `internal/core/queue.go` | `Queue.Enqueue()`, `.Next()`, `.MarkDone()`, SQLite | 300 | P4-1 |
| P4-3 | CLI `daemon` 커맨드 | `cmd/elnath/commands.go` 확장 | `runDaemon()`, `submitTask()` | 80 | P4-1,2 |
| P4-4 | 가설 생성 | `internal/research/hypothesis.go` | `GenerateHypotheses(ctx, wiki, topic) []Hypothesis` | 250 | P1-3,P3-4 |
| P4-5 | 실험 실행 | `internal/research/experiment.go` | `RunExperiment(ctx, hyp, tools) *ExperimentResult` | 350 | P1-15,P4-4 |
| P4-6 | Research 루프 | `internal/research/loop.go` | `Loop.Run()`: hypothesize→experiment→evaluate→wiki write | 400 | P4-4,5,P3-7 |
| P4-7 | Research 워크플로우 등록 | `internal/orchestrator/` 확장 | `ResearchWorkflow.Run()` | 100 | P4-6,P2-5 |

**Phase 4 합계: ~1,730 LOC**
**검증: ST-3 통과 (밤새 배치 작업 자율 실행), ST-5 통과 (autoresearch 루프)**

#### P4-6 Research Loop 상세

```go
func (l *Loop) Run(ctx context.Context, topic string, maxRounds int) (*ResearchResult, error) {
    var results []RoundResult

    for round := 0; round < maxRounds; round++ {
        // 1. Wiki에서 현재 지식 로드
        knowledge, _ := l.wiki.Search(ctx, topic, SearchOpts{Limit: 10})

        // 2. 가설 생성 (이전 라운드 결과 포함)
        hypotheses, _ := l.GenerateHypotheses(ctx, knowledge, results)

        // 3. 가설별 실험 실행
        for _, hyp := range hypotheses {
            exp, _ := l.RunExperiment(ctx, hyp)
            results = append(results, RoundResult{
                Round:      round,
                Hypothesis: hyp,
                Result:     exp,
            })
        }

        // 4. 결과를 Wiki에 기록
        l.wiki.IngestResearchResults(ctx, results)

        // 5. 종료 조건 체크 (목표 달성 또는 수렴)
        if l.shouldStop(results) { break }
    }

    return &ResearchResult{Rounds: results, Summary: l.summarize(results)}, nil
}
```

---

### Phase 5: 통합 + 품질 (Day 13-15)

| ID | 태스크 | 파일 | 핵심 함수/타입 | LOC | 의존 |
|----|--------|------|----------------|-----|------|
| P5-1 | Self State | `internal/self/state.go` | `SelfState{}`, `Load()`, `Save()`, atomic write | 200 | P0-8 |
| P5-2 | Identity | `internal/self/identity.go` | `Identity{Name, Mission, Vibe}` | 80 | P5-1 |
| P5-3 | Persona | `internal/self/persona.go` | `Persona`, `Adjust(lessons)`, clamping | 150 | P5-1 |
| P5-4 | System prompt 생성 | `internal/core/agent.go` 확장 | `buildSystemPrompt(self, wiki)` | 100 | P5-1,P3 |
| P5-5 | ST-4 통합 테스트 | 코드 수정 + 테스트 | 자동 워크플로우 + wiki 기록 E2E | 150 | P2,P3 |
| P5-6 | 에러 처리 강화 | 전체 | 사용자 친화적 에러 메시지, 로깅 | 200 | 전체 |
| P5-7 | Graceful shutdown | `internal/core/app.go` 수정 | context 전파, 리소스 정리 | 100 | P0-8 |
| P5-8 | README.md | `README.md` | 설치, 사용법, 아키텍처 | 150 | 전체 |
| P5-9 | 전체 스모크 테스트 스크립트 | `scripts/smoke_test.sh` | ST-1~5 자동 실행 | 100 | 전체 |

**Phase 5 합계: ~1,230 LOC**
**검증: 5개 스모크 테스트 전부 통과**

---

### 총 LOC 요약

| Phase | 프로덕션 LOC | 테스트 LOC (추정) | 합계 |
|-------|-------------|------------------|------|
| Phase 0 | 530 | 100 | 630 |
| Phase 1 | 2,770 | 1,400 | 4,170 |
| Phase 2 | 2,430 | 1,200 | 3,630 |
| Phase 3 | 1,990 | 1,000 | 2,990 |
| Phase 4 | 1,730 | 850 | 2,580 |
| Phase 5 | 1,230 | 600 | 1,830 |
| **합계** | **10,680** | **5,150** | **15,830** |

---

## 2. 의존성 그래프

```
Phase 0 (스캐폴딩)
  │
  ├── P0-1 (go mod init)
  │     ├── P0-2 (Makefile)
  │     ├── P0-3 (CLAUDE.md)
  │     ├── P0-4 (config) ──── P0-5 (defaults)
  │     ├── P0-6 (main.go) ── P0-7 (commands.go)
  │     └── P0-8 (App struct) ← P0-4
  │
Phase 1 (LLM + Tools) ← Phase 0 전체
  │
  ├── P1-1 (Provider iface) ← P0
  │     ├── P1-2 (Message types)
  │     │     ├── P1-3 (Anthropic) ★ 크리티컬 패스
  │     │     ├── P1-4 (OpenAI)
  │     │     └── P1-5 (Ollama)
  │     ├── P1-6 (Registry) ← P1-1
  │     │     ├── P1-7 (KeyPool)
  │     │     └── P1-8 (Usage)
  │     │
  │     └── [병렬 가능 ↓]
  │
  ├── P1-9 (Tool iface) ← P0
  │     ├── P1-10 (Tool Registry)
  │     ├── P1-11 (Bash) ─┐
  │     ├── P1-12 (File) ─┤ 모두 독립
  │     ├── P1-13 (Git)  ─┤
  │     └── P1-14 (Web)  ─┘
  │
  └── P1-15 (Agent Loop) ← P1-3, P1-6, P1-10  ★ Phase 1 핵심
        └── P1-16 (CLI 연결)
  
Phase 2 (오케스트레이션) ← P1-15
  │
  ├── P2-1 (Manager) ← P1-15
  │     ├── P2-2 (Intent) ← P1-3
  │     ├── P2-3 (Context)
  │     └── P2-4 (History SQLite)
  │
  ├── P2-5 (Types) ← P1
  │     ├── P2-6 (Router) ← P2-2, P2-5
  │     ├── P2-7 (Single) ← P1-15, P2-5
  │     ├── P2-8 (Team) ← P1-15, P2-5
  │     ├── P2-9 (Autopilot) ← P2-7, P2-8
  │     └── P2-10 (Ralph) ← P2-7
  │
  └── P2-11 (CLI 통합) ← P2-1, P2-6

Phase 3 (Wiki) ← Phase 1 (P1-3, P1-9)
  │
  ├── P3-1 (Schema) ← P0
  │     └── P3-2 (CRUD) ← P3-1
  │           ├── P3-3 (FTS5) ← P3-2
  │           │     └── P3-4 (Search) ← P3-3
  │           ├── P3-5 (Ingest) ← P3-2, P1-3
  │           ├── P3-6 (Lint) ← P3-2, P3-4
  │           ├── P3-8 (index rebuild) ← P3-2
  │           └── P3-9 (log append) ← P3-2
  │
  └── P3-7 (Wiki Tool) ← P3-2, P3-4, P1-9

Phase 4 (자율 운영) ← Phase 2 + Phase 3
  │
  ├── P4-1 (Daemon) ← P0-8
  │     ├── P4-2 (Queue) ← P4-1
  │     └── P4-3 (CLI daemon) ← P4-1, P4-2
  │
  └── P4-4 (Hypothesis) ← P1-3, P3-4
        ├── P4-5 (Experiment) ← P1-15, P4-4
        ├── P4-6 (Loop) ← P4-4, P4-5, P3-7
        └── P4-7 (Research WF) ← P4-6, P2-5

Phase 5 (통합) ← Phase 0-4 전체
  │
  ├── P5-1~3 (Self) ← P0-8
  ├── P5-4 (System prompt) ← P5-1, P3
  ├── P5-5 (ST-4 test) ← P2, P3
  ├── P5-6~7 (에러/shutdown) ← 전체
  └── P5-8~9 (README/smoke) ← 전체
```

### 크리티컬 패스

```
P0-1 → P0-4 → P0-8 → P1-1 → P1-2 → P1-3 → P1-6 → P1-15 → P2-1 → P2-6 → P2-11
                                                                          ↑
                                                 P1-9 → P1-10 → P1-11..14 ┘
```

**병목: P1-3 (Anthropic 구현) + P1-15 (에이전틱 루프)** — 이 두 개가 모든 후속 Phase의 게이트.

---

## 3. 병렬화 가능 태스크

### Phase 0 내부
```
P0-2 (Makefile) ║ P0-3 (CLAUDE.md)     — 동시 가능
P0-4 (config)   ║ P0-6 (main.go)        — 동시 가능 (의존성 없음)
```

### Phase 1 내부 (최대 병렬화 가능)
```
[LLM 트랙]                    ║  [Tools 트랙]
P1-1 (Provider iface)         ║  P1-9 (Tool iface)
  → P1-2 (Message types)      ║    → P1-10 (Registry)
  → P1-3 (Anthropic) ──┐      ║    → P1-11 (Bash) ─┐
  → P1-4 (OpenAI) ─────┤      ║    → P1-12 (File) ─┤ 4개 동시
  → P1-5 (Ollama) ─────┤      ║    → P1-13 (Git) ──┤
  → P1-6 (Registry)    │      ║    → P1-14 (Web) ──┘
  → P1-7 (KeyPool) ────┤      ║
  → P1-8 (Usage) ──────┘      ║
                               ║
  ────────── 합류 ──────────────
           P1-15 (Agent Loop)
```

**Phase 1에서 LLM 트랙과 Tools 트랙은 완전 독립 → 2명이 동시 작업 가능.**
또한 P1-3, P1-4, P1-5는 서로 독립 → 3개 provider 동시 구현 가능.
P1-11~14는 서로 독립 → 4개 tool 동시 구현 가능.

### Phase 2 내부
```
P2-1~4 (Conversation 트랙)  ║  P2-5~10 (Orchestrator 트랙)
  → P2-1 (Manager)          ║    → P2-5 (Types)
  → P2-2 (Intent)           ║    → P2-7 (Single) ─┐
  → P2-3 (Context)          ║    → P2-8 (Team) ───┤ 3개 동시
  → P2-4 (History)          ║    → P2-10 (Ralph) ──┘
                             ║    → P2-9 (Autopilot) ← 7,8 필요
```

### Phase 2 ║ Phase 3 (교차 병렬)
```
Phase 3 (Wiki)는 Phase 1만 의존 → Phase 2와 동시 진행 가능!

Day 4: P2-1~4 (Conversation) ║ P3-1~2 (Schema + CRUD)
Day 5: P2-5~8 (Workflows)    ║ P3-3~4 (FTS5 + Search)
Day 6: P2-9~11 (통합)        ║ P3-5~9 (Ingest + Lint)
```

**이 병렬화로 Day 4-6에서 Phase 2+3 동시 완료 가능 → 전체 일정 3일 단축.**

### Phase 4 내부
```
P4-1~3 (Daemon 트랙) ║ P4-4~6 (Research 트랙)
```

---

## 4. 리스크 분석

### HIGH 리스크

| # | 리스크 | 영향 | 완화 전략 |
|---|--------|------|-----------|
| R1 | **Anthropic SSE 스트리밍 파싱 복잡도** | P1-3이 지연되면 전체 크리티컬 패스 밀림 | Stella의 `provider_anthropic.go` (163 LOC)를 참고. 먼저 non-streaming Chat()만 구현하고, Stream()은 나중에 추가. |
| R2 | **Tool use 프로토콜 정확성** | LLM이 도구를 호출하는 JSON 형식이 provider마다 다름 (Anthropic vs OpenAI) | provider별 tool_use 변환 레이어 작성. Anthropic의 `tool_use` content block과 OpenAI의 `function_call`을 통일된 `ToolCall` 타입으로 매핑. |
| R3 | **의도 분류 정확도** | 워크플로우 자동 선택의 핵심. 오분류 시 UX 파괴. | 초기엔 LLM 기반 분류 + 유저 override ("이건 team으로 해줘"). 분류 프롬프트를 별도 파일로 관리하여 빠르게 튜닝. |
| R4 | **Team 워크플로우 동시성 버그** | goroutine 간 파일 충돌, context 취소 미전파 | 각 agent에 독립 작업 디렉토리 할당. context.WithCancel로 하위 goroutine 정리. 공유 자원(wiki)에 mutex. |

### MEDIUM 리스크

| # | 리스크 | 영향 | 완화 전략 |
|---|--------|------|-----------|
| R5 | **SQLite 동시 접근** | daemon 모드에서 여러 goroutine이 동시 쓰기 시 lock contention | WAL 모드 + busy_timeout=5000. 쓰기는 단일 goroutine으로 직렬화. |
| R6 | **FTS5 인덱스 동기화** | 마크다운 파일과 SQLite FTS가 불일치 | Rebuild() 함수로 전체 재인덱싱. 주기적 lint에서 불일치 감지. |
| R7 | **컨텍스트 윈도우 오버플로** | 긴 대화에서 토큰 초과 | 토큰 카운터 (tiktoken-go) + 자동 압축 (오래된 메시지 요약). Stella LCM 패턴 참고. |
| R8 | **Autoresearch 비용 폭주** | 루프가 무한 반복 시 API 비용 급증 | maxRounds 제한 (기본 5), 라운드당 비용 추적, 총 비용 캡 (기본 $5). |

### LOW 리스크

| # | 리스크 | 영향 | 완화 전략 |
|---|--------|------|-----------|
| R9 | Ollama 호환성 | 로컬 모델 tool use 미지원 가능 | v0.1에서는 Anthropic/OpenAI 우선. Ollama는 tool use 없는 대화 모드만 지원. |
| R10 | launchd plist 생성 | macOS 전용, Linux에서 systemd 필요 | v0.1은 macOS만. systemd는 v0.2. |

---

## 5. 테스트 전략

### Phase 0 테스트
```
config/config_test.go:
  - TestLoad_YAML — YAML 파일에서 설정 로드
  - TestLoad_EnvOverride — env 변수가 YAML 값을 덮어쓰기
  - TestLoad_MissingFile — 파일 없을 때 기본값 사용
  - TestDefaults — 기본값 정확성

make build → 바이너리 생성 확인
elnath version → 버전 출력 확인
```

### Phase 1 테스트

```
llm/anthropic_test.go:
  - TestChat_BasicResponse — 모킹된 HTTP 서버로 Chat() 호출
  - TestChat_ToolUse — tool_use content block 파싱
  - TestStream_SSE — SSE 이벤트 스트림 파싱
  - TestStream_Error — 에러 응답 처리

llm/openai_test.go:
  - TestChat_FunctionCall — function_call 파싱
  - TestStream_Chunks — 청크 스트림 파싱

llm/keypool_test.go:
  - TestNext_RoundRobin — 라운드 로빈 순서
  - TestCooldown_429 — rate limit 시 1시간 쿨다운
  - TestCooldown_AllKeys — 전체 키 쿨다운 시 빈 반환

tools/bash_test.go:
  - TestExecute_SimpleCommand — echo hello
  - TestExecute_Timeout — 타임아웃 동작
  - TestExecute_WorkingDir — 작업 디렉토리 유지

tools/file_test.go:
  - TestRead — 파일 읽기
  - TestWrite — 파일 쓰기
  - TestEdit — 문자열 교체
  - TestGlob — 패턴 매칭
  - TestGrep — 정규식 검색

core/agent_test.go:  ★ 핵심 테스트
  - TestRun_SimpleChat — 도구 없는 대화
  - TestRun_SingleToolCall — 한 번 도구 호출 후 응답
  - TestRun_MultipleToolCalls — 여러 도구 연쇄 호출
  - TestRun_MaxIterations — 반복 제한 동작
  - TestRun_ToolError — 도구 에러 시 LLM에 에러 전달
```

**Phase 1 검증 기준: `elnath run`에서 "이 디렉토리의 Go 파일 목록 보여줘" → glob → 결과 표시**

### Phase 2 테스트

```
conversation/intent_test.go:
  - TestClassify_Question — "~뭐야?" → question
  - TestClassify_SimpleTask — "이 파일 수정해줘" → simple_task
  - TestClassify_ComplexTask — "이 10개 파일 리팩터링해줘" → complex_task
  - TestClassify_Project — "새 프로젝트 만들어줘" → project

orchestrator/router_test.go:
  - TestRoute_QuestionToSearch — question → search workflow
  - TestRoute_ComplexToTeam — complex_task → team workflow
  - TestRoute_ProjectToAutopilot — project → autopilot workflow

orchestrator/team_test.go:
  - TestRun_ParallelExecution — 3개 서브태스크 병렬 실행
  - TestRun_PartialFailure — 1개 실패 시 나머지 결과 반환
  - TestRun_ContextCancel — context 취소 시 모든 goroutine 종료

orchestrator/ralph_test.go:
  - TestRun_FirstAttemptSuccess — 1회 만에 통과
  - TestRun_RetryUntilPass — 3회 시도 후 통과
  - TestRun_MaxRetries — 최대 횟수 초과 시 실패 보고
```

**Phase 2 검증 기준: ST-1 — "새 Go REST API 프로젝트 만들어줘" → autopilot 자동 선택 → 프로젝트 생성**

### Phase 3 테스트

```
wiki/store_test.go:
  - TestCreate — 페이지 생성 + frontmatter
  - TestRead — 페이지 읽기 + frontmatter 파싱
  - TestUpdate — 페이지 수정 + updated 갱신
  - TestDelete — 페이지 삭제 + index 갱신
  - TestList — 전체 페이지 목록

wiki/index_test.go:
  - TestRebuild — 전체 재인덱싱
  - TestUpsert — 단일 페이지 인덱스 갱신
  - TestSearch_FTS — 한글 + 영문 검색

wiki/lint_test.go:
  - TestLint_StaleDetection — 30일 이상 미갱신 감지
  - TestLint_OrphanDetection — index에 없는 페이지 감지
```

**Phase 3 검증 기준: ST-2 — "어제 Stella에서 뭐 바꿨어?" → wiki search → 정확한 답변**

### Phase 4 테스트

```
core/queue_test.go:
  - TestEnqueue — 작업 추가
  - TestNext — FIFO 순서
  - TestMarkDone — 완료 처리
  - TestPersistence — 재시작 후 큐 복구

research/loop_test.go:
  - TestRun_SingleRound — 1라운드 완료
  - TestRun_ConvergenceStop — 수렴 시 조기 종료
  - TestRun_CostCap — 비용 캡 도달 시 중단
```

**Phase 4 검증 기준: ST-3 (배치 작업 자율 실행), ST-5 (autoresearch 루프)**

### Phase 5: 스모크 테스트 전체 자동화

```bash
#!/bin/bash
# scripts/smoke_test.sh

set -e

echo "=== ST-1: End-to-end 프로젝트 생성 ==="
echo '새 Go REST API 프로젝트를 /tmp/test-api에 만들어줘' | elnath run --non-interactive
test -f /tmp/test-api/main.go && echo "PASS" || echo "FAIL"

echo "=== ST-2: 지식 검색 답변 ==="
# Wiki에 테스트 데이터 삽입 후 검색
elnath wiki search "Stella 변경 내역" | grep -q "commit" && echo "PASS" || echo "FAIL"

echo "=== ST-3: 자율 배치 작업 ==="
elnath daemon submit --task "fix-lint /tmp/test-api" --wait
elnath daemon status | grep -q "done" && echo "PASS" || echo "FAIL"

echo "=== ST-4: 자동 워크플로우 + Wiki 기록 ==="
echo 'Stella 테스트 커버리지 현황 정리해줘' | elnath run --non-interactive
elnath wiki search "테스트 커버리지" | grep -q "result" && echo "PASS" || echo "FAIL"

echo "=== ST-5: Autoresearch 루프 ==="
elnath research --topic "Go HTTP 성능 최적화" --max-rounds 2 --wiki-dir /tmp/test-wiki
test -f /tmp/test-wiki/log.md && echo "PASS" || echo "FAIL"
```

---

## 6. Stella 코드 참고 매핑

| Elnath 모듈 | Stella 참고 | 참고 수준 | 핵심 차용 포인트 |
|-------------|-------------|-----------|-----------------|
| `internal/llm/provider.go` | `modelgateway/provider.go` + `contracts.go` | **설계 참고** | 2-method 인터페이스 (Name + Invoke → Name + Chat). StreamProvider optional 패턴. |
| `internal/llm/anthropic.go` | `modelgateway/provider_anthropic.go` (163 LOC) | **SSE 파싱 로직 참고** | SSE content_block_delta 파싱, message_delta stop 감지. |
| `internal/llm/openai.go` | `modelgateway/provider_openai.go` (147 LOC) | **SSE 파싱 로직 참고** | data: {...} 라인 파싱, [DONE] 센티널 감지. |
| `internal/llm/keypool.go` | `modelgateway/keypool.go` (225 LOC) | **직접 참고** | RoundRobin + 에러별 쿨다운 (429→1h, 402→24h, 5xx→5m). |
| `internal/llm/usage.go` | `modelgateway/usage_tracker.go` (220 LOC) | **스키마 참고** | SQLite 스키마, 집계 쿼리, provider별 비용. |
| `internal/llm/registry.go` | `modelgateway/registry.go` (359 LOC) | **패턴 참고** | map[string]Provider 디스패치. Elnath는 단순화 (cost-tier 제거). |
| `internal/tools/*` | *Stella에 해당 없음* | **신규 작성** | Claude Code의 도구 체계 참고. Stella는 트레이딩 전용이라 범용 도구 없음. |
| `internal/core/agent.go` | *Stella에 해당 없음* | **신규 작성** | Claude Code의 에이전틱 루프 패턴. message→LLM→tools→repeat. |
| `internal/core/app.go` | `app/app.go` (1,397 LOC) | **라이프사이클 참고** | App struct, New(), Close() 패턴. Elnath는 대폭 단순화 (150+ 필드 → ~20 필드). |
| `cmd/elnath/commands.go` | `cmd/stella/commands.go` (2,049 LOC) | **CLI 디스패처 참고** | commandRegistry() map[string]commandRunner 패턴. Cobra 미사용. |
| `cmd/elnath/main.go` | `cmd/stella/main.go` (114 LOC) | **시그널 핸들링 참고** | signal.NotifyContext + recoverPanic + disableCoreDump. |
| `internal/config/config.go` | `config/config.go` (703 LOC) + `provider.go` (65 LOC) | **Provider 패턴 참고** | YAML + env 오버라이드 우선순위. Provider 인터페이스. |
| `internal/conversation/history.go` | `self/lcm/store.go` (1,662 LOC) | **SQLite + FTS5 참고** | 스키마, WAL 모드, FTS5 가상 테이블. Elnath는 단순화 (계층적 요약 v0.2). |
| `internal/conversation/context.go` | `self/lcm/` + `runtime/context_compiler.go` | **패턴 참고** | 토큰 카운팅, 컨텍스트 피팅, 오래된 메시지 압축. |
| `internal/orchestrator/router.go` | `swarm/orchestrator.go` (149 LOC) | **도메인 라우팅 참고** | ClassifyDomain() → intent 분류. payload에 _execution_mode 주입. |
| `internal/orchestrator/team.go` | *Stella에 직접 해당 없음* | **goroutine 패턴만 참고** | Stella의 동시성 패턴 (mutex, context 전파, graceful cancel). |
| `internal/orchestrator/types.go` | `workflow/store.go` (169 LOC) | **Store 인터페이스 참고** | Enqueue/Next/MarkDone 패턴. HandleResult → WorkflowResult. |
| `internal/wiki/store.go` | `memory/domain_store.go` (646 LOC) | **CRUD + atomic write 참고** | JSON 파일 기반 → 마크다운 파일 기반으로 변환. atomic rename 패턴. |
| `internal/wiki/index.go` | `self/lcm/schema.go` (116 LOC) | **FTS5 스키마 참고** | FTS5 가상 테이블 생성, 동기화 트리거. |
| `internal/wiki/ingest.go` | `memory/pipeline.go` (620 LOC) | **Extract→Judge→Execute 참고** | LLM 기반 사실 추출 → 판단 → 적용. Elnath는 wiki 페이지 단위로 변환. |
| `internal/wiki/lint.go` | *Stella에 해당 없음* | **신규 작성** | Karpathy 원칙 기반 lint: stale, orphan, contradiction 감지. |
| `internal/wiki/schema.go` | `memory/domain_store.go`의 Belief 타입 | **라이프사이클 참고** | hypothesis→established 패턴 → draft→published→stable. TTL, confidence. |
| `internal/research/loop.go` | `autoresearch/loops/research/` | **루프 구조 참고** | Stella의 hypothesis→experiment→evaluate 루프. Elnath는 범용화. |
| `internal/self/state.go` | `self/types.go` (281 LOC) | **타입 구조 참고** | SelfSnapshot → SelfState 단순화. Identity, Persona, RuntimeState. |
| `internal/self/persona.go` | `self/persona_tuner.go` (70 LOC) | **직접 참고** | Adjust() 로직, clamping 범위, 최소 샘플 수. |
| `internal/core/daemon.go` | Stella launchd 패턴 | **설정 참고** | plist 생성, KeepAlive=true, stdout/stderr 로그 경로. |
| `internal/core/queue.go` | `memory/queue.go` (254 LOC) | **SQLite 큐 참고** | Enqueue/Drain 패턴, dead letter, 트랜잭션 기반 읽기. |

### 참고 수준 범례
- **직접 참고**: 로직을 거의 그대로 가져오되, Elnath 스타일로 정리
- **SSE 파싱 로직 참고**: HTTP/SSE 파싱 코드만 참고
- **스키마 참고**: SQLite 테이블 구조만 참고
- **패턴 참고**: 설계 패턴과 인터페이스 구조만 참고, 구현은 새로 작성
- **라이프사이클 참고**: 상태 전이 로직만 참고
- **설계 참고**: 인터페이스 설계 철학만 참고
- **신규 작성**: Stella에 해당하는 코드가 없어 처음부터 작성

---

## 7. 최적 일정 (병렬화 적용)

Phase 2와 3을 병렬 진행하면 원래 15일 → 12일로 단축 가능.

```
Day 1:   Phase 0 (스캐폴딩)
Day 2-3: Phase 1 (LLM + Tools) — LLM 트랙 ║ Tools 트랙 병렬
Day 4-6: Phase 2 (오케스트레이션) ║ Phase 3 (Wiki) 병렬
Day 7-9: Phase 4 (자율 운영 + Autoresearch)
Day 10-12: Phase 5 (통합 + 품질 + 스모크 테스트)
```

### 일별 상세

| Day | 오전 (4h) | 오후 (4h) | 검증 |
|-----|-----------|-----------|------|
| 1 | P0-1~5 (mod, config, defaults) | P0-6~8 (CLI, App) | `make build`, `elnath version` |
| 2 | P1-1~2 (Provider iface, types) | P1-3 (Anthropic) ★ | Anthropic Chat() 동작 |
| 3 | P1-4~5 (OpenAI, Ollama) ║ P1-11~14 (Tools 4개) | P1-6~8 (Registry, KeyPool, Usage) + P1-15 (Agent Loop) | `elnath run` 대화 |
| 4 | P2-1~2 (Manager, Intent) ║ P3-1~2 (Schema, CRUD) | P2-3~4 (Context, History) ║ P3-3 (FTS5) | 의도 분류 테스트 |
| 5 | P2-5~7 (Types, Router, Single) ║ P3-4~5 (Search, Ingest) | P2-8 (Team) ║ P3-6 (Lint) | team 워크플로우 테스트 |
| 6 | P2-9~10 (Autopilot, Ralph) ║ P3-7~9 (Wiki Tool, index, log) | P2-11 (CLI 통합) ║ P3-10 (시드) | **ST-1 + ST-2** |
| 7 | P4-1~2 (Daemon, Queue) | P4-3 (CLI daemon) | daemon 모드 동작 |
| 8 | P4-4~5 (Hypothesis, Experiment) | P4-6 (Research Loop) | 단일 연구 라운드 |
| 9 | P4-7 (Research WF) | ST-3 + ST-5 검증 | **ST-3 + ST-5** |
| 10 | P5-1~3 (Self) | P5-4 (System prompt) | self 모델 로드/저장 |
| 11 | P5-5 (ST-4) + P5-6 (에러 처리) | P5-7 (Shutdown) | **ST-4** |
| 12 | P5-8 (README) | P5-9 (전체 스모크) + 최종 품질 | **ST-1~5 전부 통과** |

---

## 8. 구현 시작 체크리스트

Phase 0 시작 전 확인 사항:

- [ ] Go 1.22+ 설치 확인
- [ ] `/Users/stello/elnath/` 디렉토리 존재 확인
- [ ] Anthropic API 키 확보 (`ELNATH_ANTHROPIC_API_KEY`)
- [ ] OpenAI API 키 확보 (`ELNATH_OPENAI_API_KEY`)
- [ ] SQLite CGo 사용 여부 결정 (modernc.org/sqlite vs mattn/go-sqlite3)
  - **권장: modernc.org/sqlite** (pure Go, CGo 없이 크로스 컴파일 가능)
- [ ] 기존 LLM Wiki 경로 결정:
  - 기본: `~/.elnath/wiki/`
  - 기존 wiki 임포트: `/Users/stello/llm_memory/Claude Valut/wiki/` → 복사
- [ ] Stella 바이너리와 충돌 없는지 확인 (둘 다 launchd 사용)

---

## 9. Elnath 고유 용어 제안

| 개념 | Elnath 용어 | 설명 |
|------|------------|------|
| 워크플로우 모드 | **Mode** | single, team, autopilot, persist (ralph), discover (research) |
| 워크플로우 자동 선택 | **Autoroute** | 의도 파악 후 최적 모드 자동 선택 |
| Wiki 페이지 | **Page** | 마크다운 파일 단위 |
| Wiki 전체 | **Vault** | Obsidian 용어 차용 (기존 "Claude Vault"와 일관성) |
| 소스 수집 | **Ingest** | 외부 소스 → vault로 흡수 |
| 건강 점검 | **Audit** | stale, orphan, contradiction 감지 |
| 가설 연구 | **Discover** | hypothesis→experiment→evaluate |
| 반복 검증 | **Persist** | ralph 대체: 목표 달성까지 반복 |
| 자기 모델 | **Self** | identity + persona + state (Stella와 동일) |
| 에이전틱 루프 | **Loop** | message→LLM→tools→repeat |

---

## Appendix: 핵심 3rd-party 의존성

| 패키지 | 용도 | 이유 |
|--------|------|------|
| `modernc.org/sqlite` | SQLite (FTS5 포함) | Pure Go, CGo 불필요, 크로스 컴파일 |
| `gopkg.in/yaml.v3` | YAML 설정 파싱 | 표준 |
| `github.com/chzyer/readline` | CLI 입력 (히스토리, 자동완성) | 경량, 순수 Go |
| `golang.org/x/term` | 터미널 감지 (interactive vs pipe) | 표준 라이브러리 확장 |

**의도적으로 제외:**
- Cobra — 커스텀 디스패처로 충분
- Viper — yaml.v3 + os.Getenv로 충분
- GORM — 직접 SQL이 더 명확
- Gin/Echo — HTTP 서버 v0.1 불필요 (daemon은 소켓/파일 큐)
- tiktoken-go — character-based 추정으로 v0.1 충분 (AD: Architect/Critic 합의)

---

## 10. Ralplan Consensus Addendum (2026-04-07)

> 이 섹션은 Planner→Architect→Critic 3-agent 합의의 결과로, 원래 계획의 특정 부분을 **supersede**합니다.
> 상세 ADR: [ADR-001-v01-architecture.md](ADR-001-v01-architecture.md)

### 10.1 채택 전략: Option B

| 항목 | 원래 계획 | Option B (합의) |
|------|----------|----------------|
| 일정 | 12일 | **14일** (+2일 현실적 버퍼) |
| OpenAI | 완전 구현 (280 LOC) | **Chat-only** (~150 LOC, tool_use v0.2) |
| Ollama | Phase 1에서 구현 | **Phase 5로 이동** (v0.2 가능) |
| ST-3 | 10개 이슈 밤새 7개+ 해결 | **3개 로컬 태스크 병렬 배치** |
| Phase 0 | 530 LOC | **710 LOC** (+로깅, 에러, DB 인프라) |
| DB | 단일 elnath.db | **2개**: elnath.db + wiki.db |
| Stream | `<-chan StreamEvent` | **callback `func(StreamEvent)`** |
| core 패키지 | agent+daemon 포함 | **agent/, daemon/ 분리** |

### 10.2 Architect 4개 조건 → 태스크 매핑

| 조건 | 영향 태스크 | 변경 내용 |
|------|------------|----------|
| AD-1: core 분할 | P0-8, P1-15, P4-1~3 | `core/agent.go` → `agent/agent.go`, `core/daemon.go` → `daemon/daemon.go` |
| AD-2: Stream callback | P1-1, P1-3, P1-4, P1-15 | `Stream(ctx, req) (<-chan, error)` → `Stream(ctx, req, cb func(StreamEvent)) error` |
| AD-3: OpenAI Chat-only | P1-4 | 280 LOC → 150 LOC (text-only, tool_use 미지원) |
| AD-4: DB 분리 | P0-11, P3-3, P2-4 | `elnath.db` (conversation+usage+queue) + `wiki.db` (pages+FTS5) |

### 10.3 Critic MAJOR fix → 태스크 매핑

| Fix | 영향 태스크 | 변경 내용 | LOC |
|-----|------------|----------|-----|
| CF-1: Agent Loop 재시도 | P1-15 | LLM API exponential backoff (max 3회, 429/5xx) | +30 |
| CF-2: Wiki 시드 데이터 | P3-10 | `entities/stella.md`에 커밋 히스토리 시드. ST-2 `grep -q "commit"` 통과 보장 | +20 |

### 10.4 Critic MINOR fix

| Fix | 영향 | LOC |
|-----|------|-----|
| Config validation (API key, wiki dir 권한) | P0-4 | +15 |
| `elnath wiki search`는 LLM 없이 FTS5만으로 동작 | P3-4 | +10 |
| `--non-interactive` 모드 정의 | P2-11 | +20 |
| Team per-request 비용 경고 로그 | P2-8 | +15 |
| Daemon startup stale task 복구 | P4-2 | +15 |

### 10.5 수정된 LOC 총합

| Phase | 원래 | 합의 후 | 변경 |
|-------|------|---------|------|
| Phase 0 | 530 | **710** | +180 (logger, errors, db) |
| Phase 1 | 2,770 | **3,100** | +330 (toolconv, schema, retry, Anthropic 확대) |
| Phase 2 | 2,430 | **2,465** | +35 (non-interactive, cost log) |
| Phase 3 | 1,990 | **2,020** | +30 (FTS5 fallback, seed) |
| Phase 4 | 1,730 | **1,900** | +170 (IPC, stale recovery, ST-3 축소) |
| Phase 5 | 1,230 | **1,550** | +320 (OpenAI 완성에서 이동분) |
| **합계** | **10,680** | **11,745** | **+1,065** |
| 테스트 | ~5,150 | **~6,200** | +1,050 |
| **총합** | **~15,830** | **~17,945** | **+2,115** |

### 10.6 수정된 일정 (15일, claw-code 반영)

```
Day 1-1.5:  Phase 0 (스캐폴딩 + 인프라)
Day 2-5:    Phase 1 (LLM + Tools + Session + Permission)
Day 6-8:    Phase 2 ║ Phase 3 (병렬)
Day 9-11:   Phase 4 (Daemon + Research)
Day 12-15:  Phase 5 (통합 + 스모크)
```

### 10.6.1 Phase별 실행 모드 가이드

> **새 세션에서 이 프로젝트를 이어받는 에이전트를 위한 지침.**
> 각 Phase에 최적화된 실행 모드를 사용하세요. autopilot으로 전체를 한 번에 돌리지 마세요 (19K LOC 프로젝트는 컨텍스트 윈도우를 초과합니다).

| Phase | 규모 | 실행 모드 | 병렬 구조 | 완료 기준 |
|-------|------|----------|----------|----------|
| **Phase 0** | 690 LOC, 11 tasks | **직접 실행** (에이전트 불필요) | P0-2∥P0-3, P0-4∥P0-6, P0-9∥P0-10∥P0-11 | `make build` + `elnath version` |
| **Phase 1** | 3,810 LOC, 18 tasks | **`/team`** (2-3 executor) | **LLM 트랙** (P1-1→P1-8) ∥ **Tools 트랙** (P1-9→P1-14) → 합류 P1-15 | `elnath run` 대화 + 파일 수정 |
| **Phase 2** | 2,665 LOC, 12 tasks | **`/team`** (2 executor) | **Conversation 트랙** (P2-1→P2-4) ∥ **Orchestrator 트랙** (P2-5→P2-10) | ST-1 통과 |
| **Phase 3** | 2,020 LOC, 10 tasks | **`/team`** (Phase 2와 병렬) | P3-1→P3-2→P3-3∥P3-5∥P3-6 → P3-7 | ST-2 통과 |
| **Phase 4** | 1,900 LOC, 7 tasks | **`/ralph`** (검증 루프) | Daemon 트랙 ∥ Research 트랙 | ST-3 + ST-5 통과 |
| **Phase 5** | 1,580 LOC, 11 tasks | **`/ralph`** (검증 루프) | 순차 (통합 테스트) | **ST-1~5 전부 통과** |

#### 실행 모드 상세

**직접 실행 (Phase 0):**
- 단순 스캐폴딩이므로 orchestration 오버헤드가 불필요
- 파일 생성 → 빌드 확인 → 다음 Phase로

**`/team` (Phase 1, 2, 3):**
```
/team "Elnath Phase 1 구현. IMPLEMENTATION_PLAN.md Section 1의 Phase 1 태스크 참고.
LLM 트랙 (P1-1~P1-8)과 Tools 트랙 (P1-9~P1-14)을 병렬로 실행.
P1-15 (Agent Loop)에서 합류. 프로젝트 경로: /Users/stello/elnath/"
```
- 2-3명의 executor가 독립 트랙을 병렬 처리
- 각 executor에게 구체적 태스크 ID와 파일 경로를 할당
- 합류 지점에서 통합 테스트 실행

**`/ralph` (Phase 4, 5):**
```
/ralph "Elnath Phase 4 구현. IMPLEMENTATION_PLAN.md Section 1의 Phase 4 태스크 참고.
ST-3과 ST-5가 통과할 때까지 반복. 프로젝트 경로: /Users/stello/elnath/"
```
- 구현 → 테스트 → 실패 시 수정 → 재테스트 반복
- 스모크 테스트 통과가 종료 조건
- 최대 10회 반복

#### Phase 간 전환 규칙

1. **Phase N의 완료 기준을 충족해야 Phase N+1 시작** (검증 없이 넘어가지 말 것)
2. **Phase 2와 3은 동시 시작 가능** (의존성 없음, 단 P2-6 Router의 wiki는 optional dependency)
3. **새 세션 시작 시**: `IMPLEMENTATION_PLAN.md`의 Section 11.3 일정과 이 가이드를 먼저 읽을 것
4. **각 Phase 완료 후**: git commit으로 진행 상황 저장 (Phase별 1-2 커밋)

#### 참고 문서 우선순위

새 세션에서 Elnath 작업을 이어받을 때 읽어야 할 파일 순서:
1. `/Users/stello/elnath/IMPLEMENTATION_PLAN.md` — 전체 계획 + 이 실행 가이드
2. `/Users/stello/elnath/CLAW_CODE_ANALYSIS.md` — Claude Code/claw-code 내부 구조 (Agent Loop pseudocode 포함)
3. `/Users/stello/elnath/ADR-001-v01-architecture.md` — 아키텍처 결정 기록
4. `/Users/stello/elnath/ULTRAPLAN_PROMPT.md` — 원래 스펙 + 스모크 테스트 정의

### 10.7 수정된 ST-3 정의

**원래:**
```
유저: "이 10개 버그 이슈 밤새 처리해줘"
→ team 워크플로우 → 밤새 자율 실행 → 아침에 7개+ 해결
```

**합의 후:**
```
유저: tasks.txt에 3개 태스크 작성
  "fix lint errors in /tmp/test-api"
  "add missing error handling in /tmp/test-api/handler.go"
  "write unit test for /tmp/test-api/server.go"
Elnath daemon: 3개 작업 병렬 실행 → 3/3 완료
검증: elnath daemon submit --tasks tasks.txt --wait && elnath daemon status | grep -q "3/3"
```

### 10.8 확정된 아키텍처 결정

| 결정 | 값 | 근거 |
|------|----|----|
| Daemon IPC | Unix domain socket + JSON-line | 반응성, 구현 단순성. fallback: SQLite queue 직접 쓰기 |
| 토큰 카운팅 | `len([]rune(text)) / 2` (character-based) | tiktoken-go 불필요. API 응답 `usage` 필드로 사후 보정 |
| Wiki 시드 전략 | 테스트 전용 시드 데이터 (3-5개 페이지) | 기존 wiki 임포트는 v0.1 후 별도 태스크 |
| 프롬프트 관리 | `internal/prompts/` 패키지에 Go const | v0.2에서 파일/embed 전환. 흩어짐만 방지 |
| FTS5 fallback | `hasFTS` boolean + LIKE fallback | Stella lcm/schema.go:102-106 패턴 차용 |
| DB 파일 위치 | `~/.elnath/data/elnath.db` + `~/.elnath/data/wiki.db` | Wiki 읽기/쓰기 분리로 lock contention 감소 |
| Wiki 디렉토리 | `~/.elnath/wiki/` + `--wiki-dir` 플래그 + `ELNATH_WIKI_DIR` env | 오버라이드 가능 |
| 에러 전략 | Phase 0에서 sentinel errors + `fmt.Errorf("%w")` | Phase 5 미루기 금지 |
| 로깅 | `log/slog` (Go 표준) | 외부 의존성 없음, JSON/text 전환 가능 |

### 10.9 합의 기록

| 역할 | 판정 | 핵심 기여 |
|------|------|----------|
| **Planner** | Option B 권장 | 3대 갭(tool_use 과소추정, Phase 2-3 충돌, ST-3 50%), 7개 누락 태스크, LOC 재추정 |
| **Architect** | APPROVE (4 conditions) | core 분할, stream callback, OpenAI Chat-only, DB 분리, Option C steelman |
| **Critic** | APPROVE-WITH-RESERVATIONS | Agent Loop 재시도(CF-1), Wiki 시드(CF-2), 5 MINOR gaps |
| **최종** | **ACCEPTED** | Option B + AD 1-4 + CF 1-2 + MINOR 5건 전부 반영 |

---

## 11. claw-code 분석 Reconciliation (2026-04-07)

> claw-code (Rust+Python, ~48K LOC, 172K stars) 심층 분석 후 발견된 8개 설계 변경.
> 상세: [CLAW_CODE_ANALYSIS.md](CLAW_CODE_ANALYSIS.md)

### 11.1 태스크 변경

| 영향 태스크 | 변경 내용 | LOC 영향 |
|------------|----------|---------|
| P0-11 (DB 매니저) | 세션 저장은 **JSONL** 파일로 (SQLite 아님). DB는 wiki+usage+queue만. | -20 |
| P1-1 (Provider iface) | `ApiClient` + `ToolExecutor` 두 인터페이스로 분리. Agent Loop가 제네릭 구조. | +30 |
| P1-2 (Message types) | `ContentBlock` 타입 추가: `Text`, `ToolUse{id,name,input}`, `ToolResult{tool_use_id,output,is_error}`. 메시지 = blocks 배열. | +50 |
| P1-11 (Bash 도구) | regex 대신 **`mvdan.cc/sh/v3` AST 파서** 사용. 인용부호 안 오탐 방지. | +80 |
| P1-15 (Agent Loop) | claw-code `ConversationRuntime.run_turn()` 패턴 참고. **메시지 배열이 유일한 상태**. hidden state 없음. | +100 |
| **NEW P1-17** | **세션 JSONL 영속성** `internal/agent/session.go`: `Session.Save()`, `.Load()`, `.Fork()`. 줄 단위 append, atomic. | +200 |
| **NEW P1-18** | **퍼미션 엔진** `internal/agent/permission.go`: 4모드 (Default, AcceptEdits, Plan, Bypass). 6단계 resolution. | +250 |
| **NEW P2-12** | **컨텍스트 압축** `internal/agent/compact.go`: 3단계 (micro→auto→snip). Circuit breaker 최대 3회. | +200 |
| P5-4 (System prompt) | **프롬프트 캐시 경계** 적용. 정적 섹션과 동적 섹션 분리 (`__DYNAMIC_BOUNDARY__`). API 비용 절감. | +30 |

### 11.2 수정된 LOC

| Phase | 합의 후 | claw-code 반영 | 차이 |
|-------|---------|---------------|------|
| Phase 0 | 710 | 690 | -20 (JSONL로 세션 분리) |
| Phase 1 | 3,100 | **3,810** | +710 (session, permission, AST, message blocks) |
| Phase 2 | 2,465 | **2,665** | +200 (compaction) |
| Phase 3 | 2,020 | 2,020 | 0 |
| Phase 4 | 1,900 | 1,900 | 0 |
| Phase 5 | 1,550 | 1,580 | +30 (prompt cache boundary) |
| **합계** | **11,745** | **12,665** | **+920** |
| 테스트 | ~6,200 | ~6,700 | +500 |
| **총합** | **~17,945** | **~19,365** | **+1,420** |

### 11.3 일정 영향

Phase 1이 +710 LOC (session + permission + AST bash)로 가장 큰 영향.
**Day 2-4 → Day 2-5** (1일 추가). 총 일정 14일 → **15일**.

```
Day 1-1.5:  Phase 0
Day 2-5:    Phase 1 (LLM + Tools + Session + Permission)  ← +1일
Day 6-8:    Phase 2 ║ Phase 3 (병렬)
Day 9-11:   Phase 4
Day 12-15:  Phase 5
```

### 11.4 새 의존성

| 패키지 | 용도 | 이유 |
|--------|------|------|
| `mvdan.cc/sh/v3` | Bash AST 파싱 | tree-sitter의 Go 대안. 셸 구문 정확히 이해. |

---

## 12. Post-v0.1 Feature Roadmap (20개)

> 경쟁 분석 (claw-code 172K stars, OMC 25K stars, Claude Code CLI)을 기반으로 도출.
> Elnath의 진짜 경쟁자는 claw-code (같은 "독립 오픈소스 구현" 카테고리).
> 차별점: claw-code에 없는 Native Wiki + 범용 비서 + Autoresearch + Go 접근성.

### v0.1 포함 권장 (핵심 차별화)

| # | 기능 | 설명 | 영향도 |
|---|------|------|--------|
| 6 | **Wiki RAG** | 매 LLM 호출 전 관련 Wiki 페이지를 자동 검색 → system prompt에 주입. "시간이 갈수록 똑똑해지는 AI"의 실체. | 매우 높음 |
| 10 | **Cross-Project Intelligence** | Wiki가 프로젝트 경계를 넘어 지식 연결. Stella ↔ Orbis ↔ Elnath 간 패턴 매칭. | 높음 |
| 12 | **Git-native Wiki** | Wiki 디렉토리를 git으로 관리. 자동 커밋, diff, blame, revert. ~50 LOC. | 중간 |
| 14 | **Conversation Search** | 전체 대화 이력 FTS5 풀텍스트 검색. 과거 대화를 자산으로. | 중간 |
| 15 | **Interactive Onboarding** | 첫 실행 시 프로젝트 스캔 → Wiki 자동 시드. "설치 → 5분 → WOW" 경험. | 매우 높음 |
| 16 | **Streaming Cost Indicator** | 생성 중 실시간 토큰 수/비용 표시. 투명성 = 신뢰. | 중간 |
| 17 | **Auto-Documentation** | 코드 변경 → 관련 Wiki 페이지 자동 갱신. 살아있는 Wiki의 핵심. | 높음 |

### v0.2 (핵심 생태계)

| # | 기능 | 설명 | 영향도 |
|---|------|------|--------|
| 1 | **MCP Client** | Model Context Protocol 지원. 수백 개 기존 MCP 서버 즉시 활용. 생태계 진입 필수. | 매우 높음 |
| 2 | **Hooks 시스템** | PreToolUse/PostToolUse/OnMessage 이벤트 훅. 커뮤니티 확장의 전제 조건. | 높음 |
| 3 | **HTTP/WS API** (`elnath serve`) | REST/WebSocket API. 웹 프론트엔드, VS Code extension, 모바일 앱 연결 가능. | 중간 |
| 4 | **Session Import** | Claude Code `.claude/sessions/` → Elnath 변환. CLAUDE.md/auto-memory → Wiki 마이그레이션. | 중간 |
| 5 | **Cost Dashboard + Model Routing** | `elnath usage` 일별/주별 비용. 간단한 질문 → 저렴 모델, 복잡한 코딩 → 고급 모델 자동 라우팅. | 중간-높음 |
| 7 | **Conversation Fork** | Git-style 대화 분기. checkpoint, switch, merge. 접근법 A vs B 동시 탐색. | 높음 |
| 8 | **Natural Language Cron** | "매일 9시에 헬스체크하고 Telegram으로 알려줘." daemon의 진짜 가치. | 높음 |
| 11 | **Multi-Model Consensus** | 동일 질문을 Claude+GPT+Gemini에 동시 요청. 교집합 하이라이트. model-agnostic 킬러 유스케이스. | 중간-높음 |
| 13 | **Smart File Watcher** | daemon이 파일 변경 감지 → 선제적 제안. "handler.go 바꿨는데 테스트는 안 바꿨습니다." | 중간-높음 |

### v0.3 (장기 생태계)

| # | 기능 | 설명 | 영향도 |
|---|------|------|--------|
| 9 | **Skill Capture** | 반복 패턴을 자동 감지 → 재사용 가능한 skill로 Wiki에 저장. 학습하는 AI. | 중간-높음 |
| 18 | **Persona Switch** | `--persona researcher/coder/writer/reviewer`. Self 모델의 실용적 표면화. | 중간 |
| 19 | **Sandboxed Execution** | Docker/nsjail 격리 실행. 기업 유저 보안 요구. | 중간 |
| 20 | **Skill Marketplace** | 커뮤니티 스킬 공유 레지스트리. GitHub 기반, git clone으로 설치. 네트워크 효과. | 높음 |
