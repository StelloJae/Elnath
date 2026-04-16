# Phase 5.0: Typed Event Bus

**Status**: Draft (Red Team Revised)  
**Date**: 2026-04-15  
**Scope**: 2-3 sessions (~2-3 days)  
**Predecessor**: v0.6.0 (C-2 Skill Emergence + D-2 Safety)  
**Successors**: 5.1 Magic Docs, 5.2 Ambient Autonomy

## 1. Problem Statement

현재 Elnath의 이벤트 전달 체계는 두 가지 독립적 메커니즘으로 분산되어 있다:

1. **`onText func(string)`** — LLM 텍스트 델타를 소비자에게 전달하는 단일 콜백. Agent, Research Loop, Skill Registry, Orchestrator, Daemon 등 14곳 이상에서 사용. 텍스트만 전달 가능하고, 이벤트 타입 구분이 불가능하며, 소비자가 여러 개면 수동 fan-out 필요.

2. **`orchestrationOutput` + `daemon.ProgressEvent`** — `runtime.go`의 `emitText()`가 `onText` 스트림 안에 `daemon.ProgressEvent` JSON을 혼합 전달하는 이중 디스패치 구조. 도구 진행 상황, 워크플로우 전환, 토큰 사용량 등 구조화된 이벤트가 텍스트 문자열로 인코딩되어 `onText`를 통해 흐르고 있음.

3. **`HookRegistry`** — 5개 인터페이스(Hook, LLMHook, CompressionHook, IterationHook, ErrorObserver)로 에이전트 라이프사이클을 다루는 개입(intercept) 시스템.

이 구조의 문제:
- 소비자(터미널, 텔레그램, 세션 기록, 로깅)가 이벤트를 받으려면 각 메커니즘을 개별적으로 연동해야 함
- `onText`는 untyped string이라 이벤트 종류를 구분할 수 없음
- 5.1 Magic Docs와 5.2 Ambient Autonomy에서 research/skill/daemon 이벤트를 소비해야 하는데, 현재 구조로는 불가능

## 2. Design Decisions

| 결정 | 선택 | 근거 |
|------|------|------|
| 이벤트 범위 | C) 시스템 전역 | 5.1/5.2에서 즉시 필요, B→C 확장은 불필요한 중간 단계 |
| 소비자 패턴 | 인터페이스 기반 구독 | HookRegistry에서 검증된 패턴, 다중 소비자 자연 지원 |
| 이벤트 타입 구조 | 인터페이스 + 구체 타입 | 20+ 이벤트 확장에 적합, type switch로 깔끔한 분기 |
| HookRegistry 관계 | 공존 | Hook=개입(차단/수정), Event=관찰(읽기 전용), 본질적으로 다른 관심사 |
| 아키텍처 | Interface Sink | Go 관용적, 컴포넌트가 Bus를 모름, 테스트 용이 |

## 3. Architecture

### 3.1 Core Interfaces

```go
package event

type Event interface {
    EventType() string
    Timestamp() time.Time
}

type Sink interface {
    Emit(Event)
}

type Observer interface {
    OnEvent(Event)
}
```

- `Event` — 모든 이벤트의 공통 인터페이스. `EventType()`으로 식별, `Timestamp()`로 발생 시각.
- `Sink` — 이벤트 발행 인터페이스. 현재 `onText func(string)` 자리를 대체.
- `Observer` — 이벤트 수신 인터페이스. Bus에 등록되는 소비자.

### 3.2 Base Type

```go
type Base struct {
    ts        time.Time
    sessionID string
}

func (b Base) Timestamp() time.Time { return b.ts }
func (b Base) SessionID() string    { return b.sessionID }

func NewBase() Base                          { return Base{ts: time.Now()} }
func NewBaseWith(ts time.Time, sid string) Base { return Base{ts: ts, sessionID: sid} }
```

모든 구체 이벤트 타입이 embed하는 공통 필드.
- `sessionID`: daemon에서 여러 태스크가 동시 실행될 때 이벤트를 originating session에 연관시키기 위함 (5.1/5.2 호환성).
- `NewBaseWith()`: 테스트에서 시간과 세션을 주입할 수 있도록 함 (M3 수정).

### 3.3 Sink Implementations

**Bus** — 핵심 구현체. 여러 Observer에 순차 fan-out.

```go
type Bus struct {
    observers []Observer
    mu        sync.RWMutex
}

func (b *Bus) Emit(e Event)
func (b *Bus) Subscribe(o Observer)
```

- **copy-on-read 패턴**: `Emit()`은 RLock 아래에서 observer 슬라이스를 복사한 뒤 즉시 unlock하고, 복사본을 순회하며 호출. Observer.OnEvent() 안에서 Subscribe()를 호출해도 데드락 없음 (H1 수정).
- 순차 실행 (순서 보장, goroutine 없음)
- `sync.RWMutex`로 동시 Subscribe/Emit 안전
- Observer panic은 `defer recover()`로 격리 — 한 Observer 실패가 다른 Observer를 막지 않음
- **Observer 계약**: Observer.OnEvent()는 반드시 non-blocking이어야 함. 디스크 I/O나 네트워크 호출이 필요한 Observer는 내부적으로 버퍼링/goroutine 사용.
- `Unsubscribe()` 제거 (YAGNI, M1 수정). 필요 시 5.1+에서 추가.

**NopSink** — 아무것도 안 하는 구현체.

```go
type NopSink struct{}
func (NopSink) Emit(Event) {}
```

현재 `if onText != nil` 패턴을 대체. 테스트에서 이벤트가 필요 없을 때 사용.

**RecorderSink** — 테스트 전용. 발행된 이벤트를 기록.

```go
type RecorderSink struct {
    mu     sync.Mutex
    Events []Event
}
func (r *RecorderSink) Emit(e Event) {
    r.mu.Lock()
    r.Events = append(r.Events, e)
    r.mu.Unlock()
}
```

`sync.Mutex` 포함 — team workflow 등 goroutine 병렬 실행 환경에서도 안전 (H3 수정).

## 4. Event Catalog

### 4.1 LLM Stream (현재 StreamEvent 대체)

| 타입 | 페이로드 | 대체 대상 |
|------|---------|-----------|
| `TextDeltaEvent` | Content string | `onText(ev.Content)` |
| `ToolUseStartEvent` | ID, Name string | `StreamEvent{Type: EventToolUseStart}` |
| `ToolUseDeltaEvent` | ID, Input string | `StreamEvent{Type: EventToolUseDelta}` |
| `ToolUseDoneEvent` | ID, Name, Input string | `StreamEvent{Type: EventToolUseDone}` |
| `StreamDoneEvent` | Usage UsageStats | `StreamEvent{Type: EventDone}` |
| `StreamErrorEvent` | Err error | `StreamEvent{Type: EventError}` |

### 4.2 Agent Lifecycle (HookRegistry 관찰 부분)

| 타입 | 페이로드 | 출처 |
|------|---------|------|
| `IterationStartEvent` | Iteration, Max int | `IterationHook.OnIterationStart` |
| `CompressionEvent` | BeforeCount, AfterCount int | `CompressionHook.OnCompression` |
| `ClassifiedErrorEvent` | Classification string, Err error | `ErrorObserver.OnClassifiedError` |
| `AgentFinishEvent` | FinishReason, Usage | Agent.Run 종료 시 |

### 4.3 Progress (현재 daemon.ProgressEvent + orchestrationOutput 대체) [C1 수정]

현재 `daemon.EncodeProgressEvent()`로 JSON 인코딩되어 `onText` 스트림에 혼합 전달되는 구조화된 이벤트들을 독립적인 타입으로 분리한다.

| 타입 | 페이로드 | 대체 대상 |
|------|---------|-----------|
| `ToolProgressEvent` | ToolName, Preview string | `daemon.ToolProgressEvent()` → `EncodeProgressEvent()` → `onText()` (agent.go:565-568) |
| `WorkflowProgressEvent` | Intent, Workflow string | `daemon.WorkflowProgressEvent()` → `emitWorkflow()` (runtime.go:53-60) |
| `UsageProgressEvent` | Summary string | `daemon.UsageProgressEvent()` → `emitUsage()` (runtime.go:78-88) |

이로써 `orchestrationOutput.emitText()`의 이중 디스패치(ParseProgressEvent JSON 파싱 + raw text 전달)가 제거된다. 각 이벤트가 자기 타입으로 직접 Sink에 발행되므로 JSON 인코딩/디코딩이 불필요해진다.

### 4.4 Research (현재 emitf 대체)

| 타입 | 페이로드 | 대체 대상 |
|------|---------|-----------|
| `ResearchProgressEvent` | Phase string, Round int, Message string | `l.emitf(...)` |
| `HypothesisEvent` | HypothesisID, Statement, Status string | 가설 생성/검증 |

### 4.5 Skill

| 타입 | 페이로드 | 출처 |
|------|---------|------|
| `SkillExecuteEvent` | SkillName, Status string | skill.Registry.Execute |

### 4.6 Session

| 타입 | 페이로드 | 출처 |
|------|---------|------|
| `SessionResumeEvent` | ResumedSessionID, Surface string | session resume 시 |

### 4.7 Daemon

| 타입 | 페이로드 | 출처 |
|------|---------|------|
| `DaemonTaskEvent` | TaskID, Status string | daemon 작업 상태 변경 |

## 5. Data Flow

### 5.1 이벤트 흐름

```
cmd/elnath/runtime.go
  │ bus := event.NewBus()
  │ bus.Subscribe(terminalObserver)
  │ bus.Subscribe(sessionObserver)
  │ bus.Subscribe(telegramObserver)  // optional
  │
  ▼
orchestrator.WorkflowInput{ Sink: bus }
  │
  ▼
agent.Run(ctx, messages, sink)
  ├─ provider.Stream() 내부:
  │   sink.Emit(TextDeltaEvent{...})
  │   sink.Emit(ToolUseStartEvent{...})
  │
  ├─ agent loop 내부:
  │   sink.Emit(IterationStartEvent{...})
  │   sink.Emit(CompressionEvent{...})
  │
  ▼
research.Loop(ctx, ..., sink)
  │ sink.Emit(ResearchProgressEvent{...})
  │
  ▼
skill.Registry.Execute(ctx, ..., sink)
    sink.Emit(SkillExecuteEvent{...})
```

### 5.2 통합 원칙

1. **Sink은 아래로만 흐른다** — runtime → orchestrator → agent → research/skill. 현재 `onText` 전달 경로와 동일.
2. **HookRegistry는 건드리지 않는다** — Hook은 개입, Event Bus는 관찰. 독립 공존. Hook 실행 후 관찰용 이벤트를 자동 발행하는 어댑터는 선택적 추가 가능.
3. **기존 `llm.StreamEvent`는 provider 내부용으로 유지** — Agent의 stream 메서드에서 `llm.StreamEvent` → `event.*Event`로 변환하여 Sink에 발행. Provider 레이어는 event 패키지를 모름.

### 5.3 패키지 의존 방향

```
event (새 패키지, 의존성 없음 — leaf package)
  ↑
agent, research, skill, daemon (event.Sink 사용)
  ↑
cmd/elnath (event.Bus 생성, Observer 등록)
```

## 6. Error Handling

### 6.1 핵심 원칙: 관찰 실패 ≠ 실행 중단

Event Bus는 관찰 전용. Observer 실패가 에이전트 실행을 멈추면 안 된다.

### 6.2 설계

- `Observer.OnEvent(Event)`는 에러를 반환하지 않음
- Observer 내부에서 자체적으로 에러 처리 (로깅, 재시도, 무시)
- Bus.Emit()에서 각 Observer 호출을 `defer recover()`로 감싸 panic 격리
- recover된 panic은 내부 로깅

### 6.3 Hook과의 비교

| 항목 | Hook (기존) | Event Observer (신규) |
|------|------------|---------------------|
| 에러 반환 | O (실행 차단 가능) | X (반환값 없음) |
| panic 전파 | 전파됨 | recover로 격리 |
| 역할 | 개입/차단 | 관찰/알림 |

## 7. Migration Strategy

### 7.1 전환 매핑

| Before | After |
|--------|-------|
| `onText func(string)` 파라미터 | `sink event.Sink` 파라미터 |
| `if onText != nil { onText(s) }` | `sink.Emit(TextDeltaEvent{...})` |
| `OnText func(string)` in WorkflowInput | `Sink event.Sink` in WorkflowInput |
| 호출부에서 `nil` 전달 | `event.NopSink{}` 전달 |
| `emitf(format, args...)` | `sink.Emit(ResearchProgressEvent{...})` |
| `daemon.EncodeProgressEvent()` → `onText()` | `sink.Emit(ToolProgressEvent{...})` |
| `orchestrationOutput.emitText()` 이중 디스패치 | Observer에서 이벤트 타입별 분기 |

### 7.2 전체 onText 사용처 (14곳+) [H2 수정]

**Orchestrator 계층:**
- `orchestrator/types.go:28` — WorkflowInput.OnText 필드 정의
- `orchestrator/single.go:37` — Agent.Run()에 input.OnText 전달
- `orchestrator/team.go:52,61,74,305-313,343,346,348,392,393,421` — mutex-wrapped 콜백, subtask 진행 출력
- `orchestrator/autopilot.go:130-131,142,151-152,177,194-235` — 단계별 진행상황 출력
- `orchestrator/research.go:61,76-77` — Research 워크플로우에 전달

**Agent 계층:**
- `agent/agent.go:193` — Agent.Run() 시그니처
- `agent/agent.go:237,254,295,330,351,354,393,399-400,442,457-458` — 내부 전파 체인
- `agent/agent.go:544-549,559,565-568` — executeToolsWithStats, collectApprovedToolCalls, ProgressEvent 인코딩

**Daemon 계층:**
- `daemon/runner.go:14` — TaskRunner 인터페이스 정의
- `daemon/daemon.go:42-43,520,524,531-532` — AgentTaskRunner, 태스크 실행

**Research 계층:**
- `research/runner.go:110,131,146` — TaskRunner.Run(), 전파
- `research/experiment.go:32,45-47,98` — ExperimentRunner.WithOnText()
- `research/loop.go:33,54-56,159-160` — Loop.onText, emitf()

**Skill 계층:**
- `skill/registry.go:124,169` — ExecuteParams.OnText, Agent.Run() 전달

**Runtime 계층:**
- `cmd/elnath/runtime.go:585,647` — orchestrationOutput.emitText 연결

### 7.3 점진적 전환 전략 (컴파일 안전) [Red Team 추가]

Agent.Run() 시그니처를 한 번에 바꾸면 모든 호출부가 동시에 깨진다. 이를 방지하기 위해 2-phase 전환을 사용한다.

**Phase A: 병렬 시그니처** (컴파일 항상 통과)
1. `internal/event/` 패키지 생성 (인터페이스, 타입, Bus, NopSink, RecorderSink)
2. `SinkAdapter` 브릿지 함수 추가: `func SinkToOnText(sink Sink) func(string)` — Sink를 onText로 변환
3. `OnTextAdapter` 브릿지 함수 추가: `func OnTextToSink(fn func(string)) Sink` — onText를 Sink로 변환
4. Agent에 `RunWithSink(ctx, messages, sink)` 추가 (내부에서 기존 Run 호출 + 어댑터)
5. 새 코드는 `RunWithSink` 사용, 기존 코드는 `Run` 유지 — 양쪽 모두 컴파일됨

**Phase B: 전면 전환** (Phase A 검증 후)
6. 모든 호출부를 `RunWithSink`로 전환
7. 기존 `Run(ctx, messages, onText)` 제거, `RunWithSink`를 `Run`으로 rename
8. 어댑터 함수 제거
9. WorkflowInput.OnText → WorkflowInput.Sink
10. Research Loop: `emitf()` → `sink.Emit()`
11. Skill Registry: `OnText` → `Sink`
12. daemon.TaskRunner: `onText func(string)` → `sink event.Sink` [C2 수정]
13. orchestrationOutput → TerminalObserver (Bus의 Observer로 전환)
14. daemon.ProgressEvent 인코딩/디코딩 제거 (ToolProgressEvent 등이 직접 Sink에 발행)
15. 기존 테스트 전환 (NopSink 적용)
16. team.go의 mutex-wrapped onText 제거 (Bus가 thread-safe이므로 불필요)

## 8. Testing Strategy

### 8.1 event 패키지 단위 테스트

- Bus: Subscribe → Emit → Observer 호출 검증
- Bus: 여러 Observer 등록 시 전부 호출
- Bus: Observer panic 시 나머지 Observer 정상 실행
- Bus: goroutine 동시 Emit/Subscribe race 검증 (`go test -race`)
- NopSink: Emit 호출 시 에러 없음
- RecorderSink: Emit 후 Events 슬라이스에 기록

### 8.2 기존 코드 전환 회귀 테스트

- Agent, Research Loop, Skill Registry 기존 테스트가 NopSink으로 동작
- `emitf()` 제거 후 동일 내용이 `ResearchProgressEvent`로 발행되는지

### 8.3 통합 테스트

- runtime에서 Bus 생성 → Agent 실행 → Observer가 올바른 순서로 이벤트 수신
- 이벤트 타입별 type switch 분기 검증

### 8.4 테스트 도우미

```go
func EventsOfType[T Event](r *RecorderSink) []T
```

RecorderSink에서 특정 타입 이벤트만 필터링하는 제네릭 헬퍼.

## 9. Scope Boundaries

### In Scope

- `internal/event/` 패키지 신규 생성 (인터페이스, 타입, Bus, NopSink, RecorderSink)
- `onText func(string)` → `event.Sink` 전환 (14곳+ 전수 전환)
- `emitf()` → `event.Sink.Emit()` 전환
- `daemon.ProgressEvent` 인코딩/디코딩 → 타입화된 이벤트로 대체 (C1 수정)
- `daemon.TaskRunner` 인터페이스 시그니처 전환 (C2 수정)
- `orchestrationOutput` → TerminalObserver 전환
- 단위 + 통합 테스트
- 2-phase 점진적 마이그레이션 (컴파일 안전 보장)

### Out of Scope

- HookRegistry 수정 또는 통합
- LLMHook 관찰 이벤트 추가 (5.1에서 필요 시 추가, M2)
- 새 Observer 구현 (텔레그램 Observer 등은 5.1+에서)
- 이벤트 영속화 (JSONL 저장 등은 5.1+에서)
- 비동기/채널 기반 전달
- `Unsubscribe()` 메서드 (YAGNI, 필요 시 5.1+에서 추가)

## 10. Red Team Review Log

2026-04-15 red team 리뷰 수행. 아래 지적 사항을 본 스펙에 반영함.

| ID | 등급 | 지적 | 수정 내용 |
|----|------|------|-----------|
| C1 | CRITICAL | orchestrationOutput + daemon.ProgressEvent 레이어 누락 | §4.3 Progress 이벤트 카테고리 추가, §7.3 Phase B에 전환 단계 포함 |
| C2 | CRITICAL | daemon.TaskRunner 인터페이스 마이그레이션 누락 | §7.3 Phase B step 12에 포함 |
| H1 | HIGH | Bus.Emit() 데드락 위험 (Observer 내 Subscribe 호출 시) | §3.3 copy-on-read 패턴 + Observer non-blocking 계약 명시 |
| H2 | HIGH | onText 사용처 6곳 → 14곳+ 과소평가 | §7.2 전체 사용처 열거 |
| H3 | HIGH | RecorderSink 비thread-safe | §3.3 sync.Mutex 추가 |
| M1 | MEDIUM | Unsubscribe 사용처 없음 (YAGNI) | §3.3에서 제거, §9 Out of Scope로 이동 |
| M2 | MEDIUM | LLMHook 관찰 이벤트 미정의 | §9 Out of Scope에 기록 (5.1에서 필요 시 추가) |
| M3 | MEDIUM | time.Now() 하드코딩으로 테스트 불안정 | §3.2 NewBaseWith() 추가 |
| — | — | session/trace ID 없이 5.1 이벤트 필터링 불가 | §3.2 Base에 sessionID 필드 추가 |
| — | — | scope 1 session → 2-3 sessions | 헤더 Scope 수정 |
