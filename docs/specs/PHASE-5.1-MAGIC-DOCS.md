# Phase 5.1: Magic Docs

**Status**: Draft (Red Team Revised)
**Date**: 2026-04-16
**Scope**: 3 sessions (~3 days)
**Predecessor**: Phase 5.0 Typed Event Bus (PR #7, merged)
**Successors**: 5.2 Ambient Autonomy, 5.3 Self-Improvement Substrate

## 1. Problem Statement

Elnath의 에이전트는 research, tool 사용, skill 실행, 대화 등 다양한 활동을 수행하면서 가치 있는 지식을 생성하지만, 이 지식은 세션이 끝나면 conversation JSONL에 매몰된다. 사용자가 수동으로 wiki에 정리하지 않는 한, 에이전트가 발견한 인사이트, 연구 결론, 패턴 등은 재활용되지 못한다.

Magic Docs는 Phase 5.0의 Typed Event Bus를 활용하여 에이전트 활동을 자동으로 관찰하고, wiki에 남길 가치가 있는 지식을 추출하여 wiki 페이지로 자동 생성/갱신하는 시스템이다.

### 문서화 대상

모든 에이전트 활동을 커버하되, 우선순위를 둔다:

| 우선순위 | 대상 | 예시 |
|----------|------|------|
| P0 | Research 결과 | 가설 검증, 실험 결론, 발견된 패턴 |
| P0 | Skill 실행 산출물 | 스킬이 생성한 분석, 코드 패턴 |
| P1 | 대화에서 도출된 지식 | 에이전트가 답변 중 발견한 인사이트 |
| P2 | Tool 사용 패턴 | 의미 있는 도구 사용 맥락 기록 |

## 2. Design Decisions

| 결정 | 선택 | 근거 |
|------|------|------|
| 트리거 방식 | 하이브리드 (실시간 축적 + 비동기 추출) | 실시간은 축적만 (non-blocking), LLM 추출은 비동기 goroutine |
| 페이지 분류 | 기존 PageType + `source: magic-docs` 메타데이터 | wiki를 이분화하지 않고 통합. 소유권은 메타데이터로 추적 |
| 충돌 해결 | Channel 기반 직렬화 (단일 goroutine) | Go 관용적. mutex/merge 불필요. 쓰기 빈도가 낮아 병목 없음 |
| 소유권 권한 | `source: magic-docs` + `source_session` 이중 검증 | 자동 생성 페이지만 갱신, 사용자 페이지 절대 불가침 |
| 품질 기준 | 규칙 필터 + LLM extract-or-skip 단일 호출 | 규칙으로 노이즈 제거, LLM으로 가치 판단 + 추출 동시 수행 |
| 이벤트 영속화 | 메모리 전용 (JSONL 체크포인트 없음) | task-level 추출에서 crash recovery 불필요. slog로 추출 활동 기록 |
| Bus 범위 | task-level (현행 유지) | 의미 있는 이벤트 시퀀스가 task 내 완결. 턴 간 coherence는 wiki가 담당 |
| 기존 KnowledgeExtractor 관계 | 공존 + 유틸리티 재사용 | 입력 소스가 다름 (대화 vs 이벤트). slugify, JSON 파싱 재사용. 통합은 5.3+ |

## 3. Architecture

### 3.1 Component Overview

```
                         ┌─────────────────────────────────────────┐
                         │          internal/magicdocs/            │
                         │                                         │
Bus ──Subscribe──▶ AccumulatorObserver                            │
                    │  • 메모리 버퍼 ([]event.Event)               │
                    │  • 트리거 감지 (isTrigger)                   │
                    │                                              │
                    ▼ (트리거 시 channel 전송)                      │
               extractCh chan ExtractionRequest (버퍼 16)          │
                    │                                              │
                    ▼                                              │
               Extractor goroutine (단일)                          │
                    │  1. RuleFilter — 노이즈 제거                 │
                    │  2. LLM extract-or-skip                      │
                    │  3. WikiWriter — 소유권 확인 후 쓰기          │
                    │                                              │
                    └──────────────────────────────────────────────┘
                         │                    │
                         ▼                    ▼
                    wiki.Store          llm.Provider
```

### 3.2 Dependency Injection

```go
type Config struct {
    Enabled   bool
    Store     *wiki.Store
    Provider  llm.Provider
    Model     string         // Provider가 지원하는 모델명 (설정에서 주입)
    Logger    *slog.Logger
    SessionID string
}

func New(cfg Config) *MagicDocs
func (m *MagicDocs) Observer() event.Observer  // Bus에 등록할 Observer 반환
func (m *MagicDocs) Start(ctx context.Context) // Extractor goroutine 시작
func (m *MagicDocs) Close(ctx context.Context) error // graceful shutdown
```

### 3.3 Package Structure

```
internal/magicdocs/
├── magicdocs.go      // MagicDocs 생성자, Start, Close, Config
├── observer.go       // AccumulatorObserver (event.Observer 구현)
├── filter.go         // RuleFilter (이벤트 분류)
├── extractor.go      // Extractor goroutine (LLM 호출 + 오케스트레이션)
├── writer.go         // WikiWriter (소유권 기반 wiki 쓰기)
├── prompt.go         // LLM 프롬프트 템플릿
├── types.go          // ExtractionRequest, ExtractionResult, PageAction
├── observer_test.go
├── filter_test.go
├── extractor_test.go
└── writer_test.go
```

### 3.4 Package Dependency Direction

```
event (leaf — 의존성 없음)
  ↑
magicdocs → event, wiki, llm
  ↑
cmd/elnath (wiring)
```

`wiki`와 `magicdocs`는 서로 모름. 순환 없음.

## 4. AccumulatorObserver

### 4.1 구현

```go
type AccumulatorObserver struct {
    mu        sync.Mutex
    buffer    []event.Event
    extractCh chan ExtractionRequest
    sessionID string
    logger    *slog.Logger
}

func (a *AccumulatorObserver) OnEvent(e event.Event) {
    a.mu.Lock()
    defer a.mu.Unlock()

    a.buffer = append(a.buffer, e)

    if isTrigger(e) {
        snapshot := make([]event.Event, len(a.buffer))
        copy(snapshot, a.buffer)
        a.buffer = a.buffer[:0]

        select {
        case a.extractCh <- ExtractionRequest{
            Events:    snapshot,
            SessionID: a.sessionID,
            Trigger:   e.EventType(),
            Timestamp: e.Timestamp(),
        }:
        default:
            a.logger.Warn("magic-docs extraction channel full, dropping request",
                "trigger", e.EventType(),
                "buffered_events", len(snapshot),
            )
        }
    }
}
```

- `OnEvent`는 Bus의 non-blocking Observer 계약을 준수
- channel send는 `select/default`로 non-blocking — 가득 차면 drop + 경고 로그
- channel 버퍼 16 — task 완료 시 1건이므로 사실상 drop 불발생

### 4.2 Trigger Conditions

```go
func isTrigger(e event.Event) bool {
    switch ev := e.(type) {
    case event.AgentFinishEvent:
        return true
    case event.ResearchProgressEvent:
        return ev.Phase == "conclusion" || ev.Phase == "synthesis"
    case event.SkillExecuteEvent:
        return ev.Status == "done"
    case event.DaemonTaskEvent:
        return ev.Status == "done"
    default:
        return false
    }
}
```

트리거 발생 시 현재까지 축적된 이벤트 전체를 snapshot으로 전송하고 버퍼를 리셋한다.

### 4.3 ExtractionRequest

```go
type ExtractionRequest struct {
    Events    []event.Event
    SessionID string
    Trigger   string    // 트리거 이벤트 타입 (로깅용)
    Timestamp time.Time // 트리거 시점
}
```

## 5. Rule Filter

### 5.1 분류 체계

```go
type classification int

const (
    drop    classification = iota
    pass
    context
)

func classify(e event.Event) classification {
    switch ev := e.(type) {
    // DROP: 기계적 스트리밍 이벤트
    case event.TextDeltaEvent:
        return drop
    case event.ToolUseStartEvent:
        return drop
    case event.ToolUseDeltaEvent:
        return drop
    case event.StreamDoneEvent:
        return drop
    case event.StreamErrorEvent:
        return drop
    case event.IterationStartEvent:
        return drop

    // PASS: 핵심 지식 이벤트
    case event.ResearchProgressEvent:
        if ev.Phase == "conclusion" || ev.Phase == "synthesis" {
            return pass
        }
        return context
    case event.HypothesisEvent:
        return pass
    case event.AgentFinishEvent:
        return pass
    case event.SkillExecuteEvent:
        if ev.Status == "done" {
            return pass
        }
        return context
    case event.DaemonTaskEvent:
        if ev.Status == "done" {
            return pass
        }
        return context

    // CONTEXT: 맥락 제공 이벤트
    case event.ToolUseDoneEvent:
        return context
    case event.ToolProgressEvent:
        return context
    case event.CompressionEvent:
        return context
    case event.WorkflowProgressEvent:
        return context
    case event.UsageProgressEvent:
        return context
    case event.SessionResumeEvent:
        return context
    case event.ClassifiedErrorEvent:
        return context

    // 미지 이벤트: 안전하게 DROP + 경고
    default:
        return drop
    }
}
```

### 5.2 Filter 함수

```go
type FilterResult struct {
    Signal  []event.Event // LLM에 전달할 핵심 이벤트
    Context []event.Event // LLM에 맥락으로 전달
}

func Filter(events []event.Event, logger *slog.Logger) FilterResult {
    var result FilterResult
    for _, e := range events {
        switch classify(e) {
        case drop:
            continue
        case pass:
            result.Signal = append(result.Signal, e)
        case context:
            result.Context = append(result.Context, e)
        }
    }
    return result
}
```

Signal이 0개이면 LLM 호출 자체를 skip한다.

## 6. Extractor

### 6.1 Goroutine Lifecycle

```go
type Extractor struct {
    provider llm.Provider
    model    string
    writer   *WikiWriter
    logger   *slog.Logger
}

func (x *Extractor) Run(ctx context.Context, ch <-chan ExtractionRequest) {
    for req := range ch {
        select {
        case <-ctx.Done():
            return
        default:
        }

        filtered := Filter(req.Events, x.logger)
        if len(filtered.Signal) == 0 {
            x.logger.Debug("magic-docs skip: no signal events",
                "trigger", req.Trigger,
                "total_events", len(req.Events),
            )
            continue
        }

        x.extractAndWrite(ctx, req, filtered)
    }
}
```

- `ch`가 닫히면(`Close()`) range loop이 자연 종료
- `ctx.Done()`으로 강제 종료 가능 (timeout)
- 단일 goroutine — 모든 wiki 쓰기가 직렬화됨

### 6.2 LLM Extract-or-Skip

```go
func (x *Extractor) extractAndWrite(ctx context.Context, req ExtractionRequest, f FilterResult) {
    callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()

    prompt := buildPrompt(req, f)
    resp, _, err := x.provider.Chat(callCtx, prompt)
    if err != nil {
        x.logger.Error("magic-docs LLM call failed",
            "trigger", req.Trigger,
            "error", err,
        )
        return
    }

    result, err := parseExtractionResult(resp)
    if err != nil {
        x.logger.Error("magic-docs parse failed",
            "trigger", req.Trigger,
            "error", err,
        )
        return
    }

    if len(result.Pages) == 0 {
        x.logger.Debug("magic-docs: LLM determined nothing worth keeping",
            "trigger", req.Trigger,
        )
        return
    }

    created, updated := x.writer.Apply(ctx, result.Pages, req.SessionID, req.Trigger)

    x.logger.Info("magic-docs extraction complete",
        "trigger", req.Trigger,
        "signal_events", len(f.Signal),
        "pages_created", created,
        "pages_updated", updated,
    )
}
```

### 6.3 LLM Prompt

System prompt:

```
You are a knowledge extraction agent for Elnath's wiki. Given a batch of
agent activity events, extract wiki-worthy knowledge.

Return JSON (no markdown fences): {"pages": [...]} or {"pages": []} if nothing worth keeping.

Each page object:
{
  "action": "create" | "update",
  "path": "<type>/<slug>.md",
  "title": "Page Title",
  "type": "entity" | "concept" | "source" | "analysis" | "map",
  "content": "Markdown body (no frontmatter)",
  "confidence": "high" | "medium" | "low",
  "tags": ["tag1", "tag2"]
}

Rules:
- Only extract NOVEL knowledge: facts, insights, patterns, conclusions
- Do NOT extract: raw tool output, mechanical progress, debugging noise, trivial observations
- For "update": path must point to an existing auto-generated page
- Prefer "analysis" type for research findings, "concept" for discovered patterns
- Write content in Korean (matching the wiki's language)
- Be concise: 100-500 words per page
```

User prompt는 signal 이벤트와 context 이벤트를 구조화된 텍스트로 직렬화한다:

```
## Signal Events (핵심)
[1] research_progress (conclusion): "Go 에러 래핑에서 sentinel 에러를 사용하면..."
[2] hypothesis (validated): "errors.Is() 체인이 3단계 이상이면 성능 저하"

## Context Events (맥락)
[1] tool_use_done: read_file internal/agent/agent.go
[2] tool_progress: grep "errors.Is" — 14 matches
[3] workflow_progress: intent=research, workflow=deep_research
```

### 6.4 Response Parsing

`wiki/extract.go`의 검증된 파싱 인프라를 재사용한다:

```go
func parseExtractionResult(raw string) (*ExtractionResult, error) {
    // 1. extractFirstJSONObject(raw) — 마크다운 펜스 제거 + 첫 JSON 객체 추출
    // 2. json.Unmarshal → ExtractionResult
    // 3. 각 PageAction 검증:
    //    - action ∈ {"create", "update"}
    //    - path가 store.absPath()로 유효한지 (path traversal 차단)
    //    - confidence ∈ {"high", "medium", "low"}
    //    - type ∈ {"entity", "concept", "source", "analysis", "map"}
    //    - 잘못된 항목은 skip + log warning (나머지는 정상 처리)
}
```

재사용 대상 (`wiki/extract.go`):
- `extractFirstJSONObject()` — JSON 추출
- `slugify()` — 경로 slug 생성
- JSON fence 제거 로직

## 7. WikiWriter

### 7.1 소유권 기반 권한 모델

```go
type WikiWriter struct {
    store  *wiki.Store
    logger *slog.Logger
}

func (w *WikiWriter) Apply(ctx context.Context, actions []PageAction, sessionID, trigger string) (created, updated int) {
    for _, a := range actions {
        var err error
        switch a.Action {
        case "create":
            err = w.createPage(a, sessionID, trigger)
            if err == nil {
                created++
            }
        case "update":
            wasUpdate, e := w.updateOwnedPage(a, sessionID, trigger)
            err = e
            if err == nil {
                if wasUpdate {
                    updated++
                } else {
                    created++ // fallback to create
                }
            }
        }
        if err != nil {
            w.logger.Error("magic-docs wiki write failed",
                "action", a.Action,
                "path", a.Path,
                "error", err,
            )
        }
    }
    return
}
```

### 7.2 소유권 검증

```go
func isOwnedByMagicDocs(page *wiki.Page) bool {
    source, _ := page.Extra["source"].(string)
    _, hasSession := page.Extra["source_session"]
    return source == "magic-docs" && hasSession
}
```

이중 검증: `source == "magic-docs"` AND `source_session`이 존재해야 소유권 인정.

### 7.3 Create

```go
func (w *WikiWriter) createPage(a PageAction, sessionID, trigger string) error {
    page := &wiki.Page{
        Path:       a.Path,
        Title:      a.Title,
        Type:       wiki.PageType(a.Type),
        Content:    a.Content,
        Confidence: a.Confidence,
        Tags:       a.Tags,
        Extra: map[string]any{
            "source":        "magic-docs",
            "source_session": sessionID,
            "source_event":  trigger,
        },
    }
    return w.store.Create(page)
}
```

### 7.4 Update (소유권 확인)

```go
func (w *WikiWriter) updateOwnedPage(a PageAction, sessionID, trigger string) (wasUpdate bool, err error) {
    existing, err := w.store.Read(a.Path)
    if err != nil {
        // 페이지 없음 → create로 fallback
        return false, w.createPage(a, sessionID, trigger)
    }

    if !isOwnedByMagicDocs(existing) {
        // 사용자 페이지 → 절대 수정 안 함. 새 linked 페이지 생성.
        return false, w.createLinkedPage(a, existing, sessionID, trigger)
    }

    // 소유권 확인됨 → Upsert로 갱신
    existing.Content = a.Content
    existing.Confidence = a.Confidence
    existing.Tags = a.Tags
    existing.Extra["source_session"] = sessionID
    existing.Extra["source_event"] = trigger
    return true, w.store.Upsert(existing)
}
```

### 7.5 Linked Page (사용자 페이지와 관련된 자동 발견)

```go
func (w *WikiWriter) createLinkedPage(a PageAction, target *wiki.Page, sessionID, trigger string) error {
    // 경로 충돌 방지: {type}/{slug}-auto-{short-hash}.md
    hash := shortHash(sessionID + a.Path)
    dir := filepath.Dir(a.Path)
    base := strings.TrimSuffix(filepath.Base(a.Path), ".md")
    linkedPath := filepath.Join(dir, base+"-auto-"+hash+".md")

    page := &wiki.Page{
        Path:       linkedPath,
        Title:      a.Title,
        Type:       wiki.PageType(a.Type),
        Content:    fmt.Sprintf("Related: [%s](%s)\n\n%s", target.Title, target.Path, a.Content),
        Confidence: a.Confidence,
        Tags:       a.Tags,
        Extra: map[string]any{
            "source":         "magic-docs",
            "source_session": sessionID,
            "source_event":   trigger,
            "related_to":     target.Path,
        },
    }
    return w.store.Create(page)
}
```

## 8. Integration

### 8.1 Runtime Wiring

`cmd/elnath/runtime.go`에서 Bus 생성 시 MagicDocs를 연결:

```go
// runtime.go — runTask 또는 적절한 wiring 지점

if cfg.MagicDocs.Enabled {
    md := magicdocs.New(magicdocs.Config{
        Enabled:   true,
        Store:     wikiStore,
        Provider:  provider,
        Model:     cfg.MagicDocs.Model,
        Logger:    logger.With("component", "magic-docs"),
        SessionID: sessionID,
    })
    bus.Subscribe(md.Observer())
    md.Start(ctx)
    app.RegisterCloser("magic-docs", md.Close)
}
```

### 8.2 Configuration

`internal/config/config.go`에 `LLMExtractionConfig` 패턴을 따라 추가:

```go
type MagicDocsConfig struct {
    Enabled bool   `yaml:"enabled" env:"ELNATH_MAGIC_DOCS_ENABLED"`
    Model   string `yaml:"model"   env:"ELNATH_MAGIC_DOCS_MODEL"`
}
```

기본값: `Enabled: false` (opt-in), `Model`: Provider 기본 모델.

### 8.3 Graceful Shutdown

```go
func (m *MagicDocs) Close(ctx context.Context) error {
    close(m.extractCh)  // Extractor goroutine의 range loop 종료 신호

    done := make(chan struct{})
    go func() {
        m.wg.Wait()  // Extractor goroutine 종료 대기
        close(done)
    }()

    select {
    case <-done:
        return nil
    case <-ctx.Done():
        m.cancel()  // 강제 종료 — 진행 중 LLM 호출 취소
        return ctx.Err()
    }
}
```

## 9. Error Handling

### 9.1 핵심 원칙: 관찰 실패 ≠ 실행 중단

Phase 5.0과 동일. Magic Docs의 어떤 실패도 메인 에이전트 루프에 영향 없음.

### 9.2 실패 지점별 처리

| 실패 지점 | 처리 |
|-----------|------|
| Observer.OnEvent panic | Bus의 `defer recover()`가 격리 (기존 메커니즘) |
| extraction channel full | non-blocking drop + `logger.Warn`. 버퍼 16이라 사실상 발생 불가 |
| LLM 호출 실패 (네트워크, rate limit) | `logger.Error` + skip. 재시도 없음 (best-effort) |
| LLM 응답 파싱 실패 (잘못된 JSON) | `logger.Error` + skip |
| 개별 PageAction 검증 실패 | 해당 항목만 skip, 나머지 정상 처리 |
| wiki.Store 쓰기 실패 | `logger.Error` + 해당 페이지만 skip |
| 소유권 검사 중 Store.Read 실패 | create로 fallback (안전한 방향) |
| Extractor goroutine panic | `defer recover()` + `logger.Error` |

### 9.3 Observability

모든 추출 활동을 `slog.Logger`로 구조화 로깅:

```go
// 추출 성공
logger.Info("magic-docs extraction complete",
    "trigger", "research_progress",
    "signal_events", 3,
    "pages_created", 1,
    "pages_updated", 1,
)

// 추출 skip (가치 없음)
logger.Debug("magic-docs skip: LLM determined nothing worth keeping",
    "trigger", "agent_finish",
)

// 실패
logger.Error("magic-docs LLM call failed",
    "trigger", "research_progress",
    "error", "context deadline exceeded",
)
```

Phase 5.3 Self-Improvement는 이 로그 + wiki 페이지 메타데이터(`source`, `source_session`, timestamps)로 추출 효과를 분석한다.

## 10. Testing Strategy

### 10.1 단위 테스트

| 대상 | 테스트 |
|------|--------|
| `isTrigger` | 17개 이벤트 타입 각각에 대한 트리거 여부 테이블 테스트 |
| `classify` | 17개 이벤트 타입의 DROP/PASS/CONTEXT 분류 테이블 테스트. 미지 타입 → DROP 검증 |
| `Filter` | signal 0개 → 빈 결과. 혼합 이벤트 → 올바른 분리 |
| `AccumulatorObserver` | 이벤트 축적 → 트리거 시 channel 전송 → 버퍼 리셋 |
| `AccumulatorObserver` | channel full 시 drop (blocking 하지 않음) |
| `isOwnedByMagicDocs` | source + source_session 이중 검증 |
| `WikiWriter.createPage` | frontmatter에 source 메타데이터 주입 검증 |
| `WikiWriter.updateOwnedPage` | 소유 페이지 → 갱신. 비소유 → linked page 생성 |
| `WikiWriter.updateOwnedPage` | 존재하지 않는 페이지 → create fallback |
| `parseExtractionResult` | 정상 JSON, 마크다운 펜스, 잘못된 action, path traversal 시도 |

### 10.2 통합 테스트

- **End-to-end**: Bus에 이벤트 시퀀스 발행 → Observer 축적 → Extractor 처리 → wiki.Store에 페이지 생성 검증 (LLM mock)
- **소유권 시나리오**: 사용자 페이지 존재 시 linked page 생성 + `related_to` 메타데이터 검증
- **Graceful shutdown**: Close 호출 → 진행 중 추출 완료 후 goroutine 종료 검증
- **Race detection**: `go test -race` — Observer 동시 접근, channel 동시 send

### 10.3 기존 테스트 도우미 활용

- `event.RecorderSink` + `EventsOfType[T]` — 이벤트 발행 검증
- `event.NopSink{}` — 이벤트 불필요한 테스트

## 11. Scope Boundaries

### In Scope

- `internal/magicdocs/` 패키지 신규 생성 (7개 파일 + 테스트)
- AccumulatorObserver, RuleFilter, Extractor, WikiWriter
- LLM extract-or-skip 프롬프트
- 소유권 기반 권한 모델 (`source: magic-docs` + `source_session`)
- `runtime.go` wiring + `config.go` MagicDocsConfig
- `app.RegisterCloser` lifecycle 관리
- `wiki/extract.go` 유틸리티 재사용 (slugify, JSON 파싱)
- 단위 + 통합 테스트
- slog 기반 추출 활동 로깅

### Out of Scope

- JSONL 이벤트 체크포인트 / event serde 레이어 (불필요로 판단)
- Bus lifecycle 변경 (task-level이 올바른 설계)
- 기존 `wiki.KnowledgeExtractor` 수정/폐기 (5.3+에서 통합 검토)
- wiki lint 확장 (auto/human 통합 제안) — Phase 5.3+
- Stage 1 규칙 자동 튜닝 — Phase 5.3 Self-Improvement
- `Unsubscribe()` — YAGNI 유지
- 새 이벤트 타입 추가 (MagicDocsWriteEvent 등)
- 비동기/채널 기반 Bus 전달

## 12. Red Team Review Log

2026-04-16 red team 리뷰 수행. 15건 지적, 최종 반영 결과:

| ID | 등급 | 지적 | 해결 |
|----|------|------|------|
| RT-01 | CRITICAL | event.Event JSONL 직렬화 불가능 (unexported fields, no JSON tags, no type discriminator) | **제거**: JSONL 체크포인트를 scope에서 제외. 메모리 전용 + slog 로깅으로 대체. task-level 추출에서 crash recovery 불필요. |
| RT-02 | CRITICAL | Extractor goroutine 누수 (shutdown 미정의) | **반영**: §8.3 Graceful Shutdown — `Close(ctx)`, `app.RegisterCloser()`, context 전파, `sync.WaitGroup` |
| RT-03 | HIGH | Bus가 task 단위 (session 아닌) | **제거**: task-level이 올바른 설계. 의미 있는 이벤트 시퀀스가 task 내 완결. wiki가 턴 간 coherence 담당. |
| RT-04 | HIGH | ClassifiedErrorEvent 필터 누락 | **반영**: §5.1에서 CONTEXT로 분류. 미지 이벤트 → DROP + log |
| RT-05 | HIGH | ToolProgressEvent 필터 누락 | **반영**: §5.1에서 CONTEXT로 분류 |
| RT-06 | HIGH | wiki.Store TOCTOU race | **하향 (MEDIUM)**: 추출이 task 완료 후 실행되므로 실제 race window 극소. `store.Upsert()` 활용. |
| RT-07 | HIGH | 기존 wiki.KnowledgeExtractor와 중복 | **반영**: §2에서 공존 + 유틸리티 재사용 결정. 입력 소스가 다름 (대화 vs 이벤트). source 메타데이터로 분리. 통합은 5.3+. |
| RT-08 | HIGH | LLM JSON 응답 파싱 미정의 | **반영**: §6.4에서 extract.go 파싱 재사용 + 검증 추가 (path traversal, action/confidence 값) |
| RT-09 | MEDIUM | Backpressure drop 시 복구 없음 | **제거**: JSONL 제거로 해소. 버퍼 16 + drop + log warning. 실발생 불가 수준. |
| RT-10 | MEDIUM | context.Context 전파 없음 | **반영**: §6.1 ctx 전파, §6.2 per-call timeout 30s, §8.3 cancel |
| RT-11 | MEDIUM | Linked page 경로 패턴 미정의 | **반영**: §7.5 `{type}/{slug}-auto-{short-hash}.md` + `Extra["related_to"]` |
| RT-12 | MEDIUM | Checkpoint flush 타이밍 문제 | **제거**: JSONL 제거로 해소 |
| RT-13 | MEDIUM | 소유권 단일 검증 (source만) | **반영**: §7.2 이중 검증 — `source == "magic-docs" && source_session != nil` |
| RT-14 | MEDIUM | Config 불완전 (Enabled, Logger 누락) | **반영**: §3.2 Config에 Enabled, Logger 포함. CheckpointDir 제거. §8.2 MagicDocsConfig 패턴 |
| RT-15 | MEDIUM | 중복 추출 방지 없음 | **제거/연기**: WikiWriter update 경로가 자연 처리. wiki lint로 보완 가능. 명시적 dedup은 YAGNI. |
