# Phase F-6 LB7 — Fault Injection Framework

**Predecessor:** Phase F-6 LB6 Auth/Credential Portability (spec complete)
**Status:** SPEC (decisions Q7-Q12 locked — `PHASE-F6-DECISIONS.md`)
**Scope:** ~700 LOC (fault package + agent/LLM/IPC hooks + CLI + config)
**Branch:** `feat/telegram-redesign`

---

## 0. Goal

Elnath 의 **복구 경로를 실제 production 코드에서 검증**한다. Tool 실행 실패, LLM API 오류, IPC 소켓 이상 등 현실에서 발생하는 결함을 주입하고, 에이전트 루프가 의도대로 회복하는지 확인한다.

**Why**: Elnath 는 LLM 호출, OS tool 실행, Unix socket 데몬 세 가지 비결정적 외부 의존성을 가진다. `retryMaxAttempts=3`, `ralph` 재시도 루프, `daemon/runner.go` 패닉 회복 등의 방어 코드는 있으나, 이를 체계적으로 검증하는 harness 가 없다. 회복 로직이 개정될 때마다 인수 테스트를 수작업 실행해야 하고, "어떤 fault 에서 어떤 임계값을 넘기면 사용자가 영향을 받는가"에 대한 측정치가 없다.

**Why now**: F-5 Provider Patch 가 LLM 추상화를 안정화했고, LB6 가 credential 흐름을 정리한다. 두 변경이 합쳐지면 fault injection 포인트가 명확하게 정의된다. 또한 F7 onboarding / F8 locale 구현 전에 신뢰성 baseline 을 확보해두면, 이후 기능이 회복 경로를 실수로 깨뜨렸을 때 즉시 검출할 수 있다.

**Why daemon-integrated, not external harness (Q7=B)**: 별도 test double / mock server 방식은 실제 코드 경로를 우회한다. `internal/fault` 패키지를 production 바이너리에 내장하되, env + config 의 이중 잠금과 daemon 시작 시 5초 경고로 실수 활성화를 방지한다.

---

## 1. Decisions (F-6 Q7-Q12 확정)

| ID | Question | Answer | Rationale |
|----|----------|--------|-----------|
| Q7 | Framework 통합 방식 | **B** — Daemon-integrated, env-gated | 실제 production 코드 경로 검증. kill switch 3중. zero overhead when disabled. |
| Q8 | Fault 카테고리 | **1, 2, 3 만** — Tool / LLM / IPC | filesystem(4), network(5), time-skew(6) 는 별도 phase defer. |
| Q9 | Corpus 크기 | **B** — 10 시나리오 | 3 카테고리 × ~3 시나리오. 1 cycle ~30분. 집중적이고 유지 가능한 수준. |
| Q10 | PASS/FAIL 기준 | **B** — Per-scenario threshold | 카테고리별 회복 특성 상이. Tool 20% fail → recovery ≥ 95%, IPC drop 5% → recovery ≥ 90%. |
| Q11 | Production guard | **B** — 3중 가드 | env `ELNATH_FAULT_PROFILE=<name>` + config `fault_injection.enabled: true` + daemon 시작 시 stderr 빨간 경고 + 5초 wait. |
| Q12 | Reporting | **C** — JSONL + Markdown | JSONL = CI/scorecard 기계 처리, Markdown = PR/사람 리뷰. |

---

## 2. Architecture

```
┌───────────────────────────────────────────────────────────────────┐
│  FAULT INJECTION FRAMEWORK  (disabled by default, 3-guard gate)   │
│                                                                   │
│  ┌──────────────────────────────────────────────────────────────┐ │
│  │  internal/fault/                                             │ │
│  │    registry.go   — ScenarioRegistry  (load / get / list)     │ │
│  │    injector.go   — Injector interface + NoopInjector         │ │
│  │    scenario.go   — Scenario struct + threshold types         │ │
│  │    hook_tool.go  — ToolFaultHook  (wraps tools.Registry)     │ │
│  │    hook_llm.go   — LLMFaultHook   (wraps llm.Provider)       │ │
│  │    hook_ipc.go   — IPCFaultHook   (wraps daemon conn)        │ │
│  │    reporter.go   — RunRecord + JSONLReporter + MDReporter    │ │
│  │    guard.go      — 3-guard gate + daemon stderr warning      │ │
│  │    scenarios/    — 10 embedded scenario definitions (Go)     │ │
│  └──────────────────────────────────────────────────────────────┘ │
│                                                                   │
│  Injection points:                                                │
│                                                                   │
│  [Tool path]                                                      │
│  agent.executeToolBatch()                                         │
│    └─► tools.Registry.Execute()  ←─── ToolFaultHook wraps here   │
│                                                                   │
│  [LLM path]                                                       │
│  agent.streamWithRetry()                                          │
│    └─► llm.Provider.Stream()     ←─── LLMFaultHook wraps here    │
│         (AnthropicProvider / CodexOAuthProvider)                  │
│                                                                   │
│  [IPC path]                                                       │
│  daemon/runner.go task dispatch                                   │
│    └─► net.Conn (unix socket)    ←─── IPCFaultHook wraps here    │
│                                                                   │
│  Guard gate (3 layers):                                           │
│  1. env ELNATH_FAULT_PROFILE set?                                 │
│  2. config.fault_injection.enabled == true?                       │
│  3. daemon start: stderr WARN + 5-second countdown (SIGINT to abort) │
└───────────────────────────────────────────────────────────────────┘

CLI surface:
  elnath chaos run   <scenario>        → run 1 scenario, stream results
  elnath chaos run   --all             → run all 10, stream + summary
  elnath chaos list                    → print scenario catalog
  elnath chaos report <run-id|latest>  → render markdown from JSONL
```

**Zero overhead when disabled**: every hook path checks a single `atomic.Bool` (`fault.Injector.Active()`) before any logic. When false, the hook is a direct pass-through with no allocation.

---

## 3. Implementation

**Phase 1** (core package + ToolFaultHook + 3 tool scenarios + guard): ~320 LOC
**Phase 2** (LLMFaultHook + IPCFaultHook + reporter + CLI + remaining scenarios): ~380 LOC

### 3.1 `internal/fault/scenario.go` (NEW, ~60 LOC)

Scenario 는 embedded Go struct 로 정의된다. YAML 파싱 의존성 없음 — 빌드 시 정적 포함.

```go
package fault

import "time"

// Category classifies which subsystem a fault targets.
type Category string

const (
    CategoryTool Category = "tool"
    CategoryLLM  Category = "llm"
    CategoryIPC  Category = "ipc"
)

// FaultType describes the failure mode injected.
type FaultType string

const (
    FaultTransientError   FaultType = "transient_error"    // error returned N% of calls
    FaultPermDenied       FaultType = "perm_denied"        // os.ErrPermission
    FaultTimeout          FaultType = "timeout"            // context.DeadlineExceeded after delay
    FaultMalformedJSON    FaultType = "malformed_json"     // garbled response body
    FaultHTTP429Burst     FaultType = "http_429_burst"     // consecutive 429 responses
    FaultSlowConn         FaultType = "slow_conn"          // added latency per write
    FaultPacketDrop       FaultType = "packet_drop"        // drop N% of writes
    FaultBackpressure     FaultType = "backpressure"       // block until queue drains
    FaultWorkerPanic      FaultType = "worker_panic"       // panic() in worker goroutine
)

// Threshold defines PASS criteria for a scenario.
type Threshold struct {
    // RecoveryRate is the minimum fraction of runs that must end with
    // successful task completion (not just error handling).
    RecoveryRate float64
    // MaxRuns is the number of independent runs to execute.
    MaxRuns int
    // MaxRecoveryAttempts is the upper bound of retries allowed per run
    // before the run is considered a FAIL (even if eventually recovered).
    MaxRecoveryAttempts int
}

// Scenario is a single fault injection test case.
type Scenario struct {
    Name        string    // unique slug, used in env var and CLI
    Category    Category
    FaultType   FaultType
    Description string    // human-readable
    // FaultRate is the probability [0,1] a faultable call is disrupted.
    FaultRate   float64
    // FaultDuration applies to timeout and slow_conn faults.
    FaultDuration time.Duration
    Threshold   Threshold
    // TargetTool restricts ToolFaultHook to a specific tool name.
    // Empty = all tools in the category.
    TargetTool  string
    // BurstLimit is used only with FaultHTTP429Burst. The injector faults
    // the first BurstLimit calls per run, then passes through normally.
    // Zero means no limit (use FaultRate instead).
    BurstLimit  int
}
```

### 3.2 `internal/fault/registry.go` (NEW, ~40 LOC)

```go
package fault

import "fmt"

// ScenarioRegistry holds all registered fault scenarios.
type ScenarioRegistry struct {
    scenarios map[string]*Scenario
    ordered   []*Scenario // insertion order for list
}

// NewRegistry returns a registry pre-loaded with all built-in scenarios.
func NewRegistry() *ScenarioRegistry {
    r := &ScenarioRegistry{scenarios: make(map[string]*Scenario)}
    for _, s := range builtinScenarios() {
        r.Register(s)
    }
    return r
}

// Register adds a scenario. Panics on duplicate names (caught at init time).
func (r *ScenarioRegistry) Register(s *Scenario) {
    if _, exists := r.scenarios[s.Name]; exists {
        panic(fmt.Sprintf("fault: duplicate scenario name %q", s.Name))
    }
    r.scenarios[s.Name] = s
    r.ordered = append(r.ordered, s)
}

// Get returns a scenario by name, or false if not found.
func (r *ScenarioRegistry) Get(name string) (*Scenario, bool) {
    s, ok := r.scenarios[name]
    return s, ok
}

// All returns all scenarios in registration order.
func (r *ScenarioRegistry) All() []*Scenario { return r.ordered }
```

`builtinScenarios()` 는 `internal/fault/scenarios/` 패키지에 정의된 10개 시나리오 슬라이스를 반환한다 (§4 참조).

### 3.3 `internal/fault/injector.go` (NEW, ~50 LOC)

Injector 는 훅 레이어가 호출하는 핵심 인터페이스다.

```go
package fault

import (
    "context"
    "math/rand"
    "sync/atomic"
    "time"
)

// Injector decides whether and how to disrupt a call.
type Injector interface {
    // Active reports whether fault injection is enabled.
    // Hook wrappers check this first; if false they skip all logic.
    Active() bool
    // ShouldFault returns true with probability s.FaultRate.
    ShouldFault(s *Scenario) bool
    // InjectFault applies the fault described by s and returns the
    // error (or nil for side-effect-only faults like slow_conn).
    InjectFault(ctx context.Context, s *Scenario) error
}

// activeFlag is an internal atomic guard.
type activeFlag struct{ v atomic.Bool }

// ScenarioInjector is the production implementation of Injector.
type ScenarioInjector struct {
    scenario   *Scenario
    rng        *rand.Rand
    active     activeFlag
    burstCount atomic.Int64 // incremented per ShouldFault call
    burstLimit int          // for FaultHTTP429Burst: fault only when burstCount.Load() < burstLimit
}

// NewScenarioInjector creates an active injector for the given scenario.
func NewScenarioInjector(s *Scenario, seed int64) *ScenarioInjector {
    inj := &ScenarioInjector{
        scenario:   s,
        rng:        rand.New(rand.NewSource(seed)),
        burstLimit: s.BurstLimit,
    }
    inj.active.v.Store(true)
    return inj
}

func (i *ScenarioInjector) Active() bool { return i.active.v.Load() }

// ShouldFault returns true when the injector decides to inject a fault.
// For FaultHTTP429Burst: faults only on the first burstLimit calls per run.
// For all other types: faults with probability s.FaultRate.
func (i *ScenarioInjector) ShouldFault(s *Scenario) bool {
    if !i.Active() {
        return false
    }
    if s.FaultType == FaultHTTP429Burst {
        n := i.burstCount.Add(1)
        return n <= int64(i.burstLimit)
    }
    return i.rng.Float64() < s.FaultRate
}

// ResetForRun resets per-run counters (e.g. burstCount). Call at the start
// of each independent chaos run so burst scenarios begin from zero.
func (i *ScenarioInjector) ResetForRun() {
    i.burstCount.Store(0)
}

func (i *ScenarioInjector) InjectFault(ctx context.Context, s *Scenario) error {
    switch s.FaultType {
    case FaultTransientError:
        return fmt.Errorf("fault: injected transient error (%s)", s.Name)
    case FaultPermDenied:
        return fmt.Errorf("fault: injected permission denied (%s): %w", s.Name, os.ErrPermission)
    case FaultTimeout:
        dur := s.FaultDuration
        if dur == 0 {
            dur = 30 * time.Second
        }
        select {
        case <-time.After(dur):
        case <-ctx.Done():
        }
        return context.DeadlineExceeded
    case FaultMalformedJSON:
        return &MalformedJSONError{Scenario: s.Name}
    case FaultHTTP429Burst:
        return &HTTP429Error{Scenario: s.Name, RetryAfter: 1 * time.Second}
    default:
        return fmt.Errorf("fault: unknown fault type %q in scenario %q", s.FaultType, s.Name)
    }
}

// NoopInjector is returned when fault injection is disabled.
// All methods are zero-cost; Active() always returns false.
type NoopInjector struct{}

func (NoopInjector) Active() bool                                  { return false }
func (NoopInjector) ShouldFault(_ *Scenario) bool                  { return false }
func (NoopInjector) InjectFault(_ context.Context, _ *Scenario) error { return nil }

// Sentinel errors for typed assertions in tests.
type MalformedJSONError struct{ Scenario string }
func (e *MalformedJSONError) Error() string { return "fault: injected malformed JSON in " + e.Scenario }

type HTTP429Error struct {
    Scenario   string
    RetryAfter time.Duration
}
func (e *HTTP429Error) Error() string { return "fault: injected HTTP 429 in " + e.Scenario }
```

### 3.4 `internal/fault/guard.go` (NEW, ~45 LOC)

3중 가드 — 실수 활성화 방지의 핵심.

```go
package fault

import (
    "fmt"
    "os"
    "os/signal"
    "syscall"
    "time"
)

const (
    envFaultProfile = "ELNATH_FAULT_PROFILE"
    guardWaitSecs   = 5
)

// GuardConfig mirrors the relevant portion of config.FaultInjectionConfig.
type GuardConfig struct {
    Enabled bool
}

// CheckGuards returns the active scenario name if all three guards pass,
// or ("", nil) if fault injection is inactive (normal operation).
// Returns an error only if the configuration is inconsistent.
//
// Guards:
//  1. ELNATH_FAULT_PROFILE env var must be set to a non-empty scenario name.
//  2. config.fault_injection.enabled must be true.
//  3. Stderr warning + guardWaitSecs-second countdown (SIGINT to abort).
func CheckGuards(cfg GuardConfig) (scenarioName string, err error) {
    profile := os.Getenv(envFaultProfile)
    if profile == "" {
        return "", nil // fault injection inactive — fast path
    }
    if !cfg.Enabled {
        return "", fmt.Errorf(
            "fault: ELNATH_FAULT_PROFILE=%q but fault_injection.enabled=false in config — refusing to start",
            profile)
    }
    printDaemonWarning(profile)
    if interrupted := waitWithInterrupt(guardWaitSecs); interrupted {
        return "", fmt.Errorf("fault: startup aborted by user (SIGINT during fault warning countdown)")
    }
    return profile, nil
}

// printDaemonWarning writes a highly visible warning to stderr.
// Uses ANSI red for TTY, plain text otherwise.
func printDaemonWarning(profile string) {
    isTTY := isTerminal(os.Stderr)
    if isTTY {
        fmt.Fprintf(os.Stderr, "\033[1;31m")
    }
    fmt.Fprintf(os.Stderr,
        "\n⚠  FAULT INJECTION ACTIVE: scenario=%q\n"+
        "   This daemon will deliberately corrupt operations.\n"+
        "   NOT for production use. Starting in %d seconds. Ctrl-C to abort.\n\n",
        profile, guardWaitSecs)
    if isTTY {
        fmt.Fprintf(os.Stderr, "\033[0m")
    }
}

func waitWithInterrupt(secs int) (interrupted bool) {
    sigs := make(chan os.Signal, 1)
    signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
    defer signal.Stop(sigs)

    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()
    remaining := secs
    for {
        select {
        case <-sigs:
            return true
        case <-ticker.C:
            remaining--
            if remaining <= 0 {
                return false
            }
        }
    }
}
```

`isTerminal` は `golang.org/x/term` の `term.IsTerminal(int(f.Fd()))` を使う。LB6 passphrase prompt が同じ依存性を使うため go.mod への追加不要。

### 3.5 `internal/fault/hook_tool.go` (NEW, ~55 LOC)

Agent の tool execution path に挟む wrapper。`tools.Registry` の `Execute` を intercept する。

```go
package fault

import (
    "context"

    "github.com/stello/elnath/internal/tools"
)

// ToolFaultHook wraps a tools.Registry and injects faults at Execute time.
// When injector.Active() == false the wrapper is a transparent pass-through.
type ToolFaultHook struct {
    inner    *tools.Registry
    injector Injector
    scenario *Scenario
}

// NewToolFaultHook returns a ToolFaultHook. If injector.Active() == false
// the hook is a no-op wrapper with zero overhead.
func NewToolFaultHook(reg *tools.Registry, inj Injector, s *Scenario) *ToolFaultHook {
    return &ToolFaultHook{inner: reg, injector: inj, scenario: s}
}

// Execute wraps tools.Registry execution with probabilistic fault injection.
// The fault fires before the real execution so the agent's error-handling
// path is exercised, not just the tool's error path.
func (h *ToolFaultHook) Execute(ctx context.Context, name string, input json.RawMessage) (tools.ToolResult, error) {
    if h.injector.Active() {
        targeted := h.scenario.TargetTool == "" || h.scenario.TargetTool == name
        if targeted && h.injector.ShouldFault(h.scenario) {
            err := h.injector.InjectFault(ctx, h.scenario)
            return tools.ToolResult{}, err
        }
    }
    return h.inner.Execute(ctx, name, input)
}

// Registry returns the underlying registry for metadata queries
// (tool listing, permission checks, etc.) that must not be faulted.
func (h *ToolFaultHook) Registry() *tools.Registry { return h.inner }
```

`agent.Agent` はすでに `tools.Registry` ポインタを `WithHooks` / `New` で受け取る。fault mode では `ToolFaultHook` が同じインターフェースを実装するよう `tools.Registry` の `Execute` メソッドを interface 化するか、`ToolFaultHook` を `Agent` に `WithToolExecutor(...)` オプションで注入する。

実装判断 (S5): `WithToolExecutor(exec tools.Executor)` オプションを `internal/agent/agent.go` に追加。`tools.Executor` は `Execute(ctx, name, input) (ToolResult, error)` のみの 1-method interface。デフォルトは `a.tools` (実際の Registry)。`ToolFaultHook` がこれを実装する。既存コードへの影響は `agent.go` の `executeApprovedToolCalls` 内で `a.tools.Execute` → `a.executor.Execute` に切り替えるだけ。

### 3.6 `internal/fault/hook_llm.go` (NEW, ~55 LOC)

`llm.Provider` を wrap する fault 層。

```go
package fault

import (
    "context"

    "github.com/stello/elnath/internal/llm"
)

// LLMFaultHook wraps an llm.Provider and injects faults at Stream time.
type LLMFaultHook struct {
    inner    llm.Provider
    injector Injector
    scenario *Scenario
}

func NewLLMFaultHook(p llm.Provider, inj Injector, s *Scenario) *LLMFaultHook {
    return &LLMFaultHook{inner: p, injector: inj, scenario: s}
}

// Name delegates to the underlying provider.
func (h *LLMFaultHook) Name() string { return h.inner.Name() }

// Models delegates to the underlying provider.
func (h *LLMFaultHook) Models() []llm.ModelInfo { return h.inner.Models() }

// Stream injects a fault before calling the underlying Stream.
// For FaultMalformedJSON the underlying Stream is called but the streaming
// callback receives a garbled delta to exercise the agent's JSON parse
// error path.
func (h *LLMFaultHook) Stream(ctx context.Context, req llm.Request, cb func(llm.StreamEvent)) error {
    if h.injector.Active() && h.injector.ShouldFault(h.scenario) {
        err := h.injector.InjectFault(ctx, h.scenario)
        if err != nil {
            return fmt.Errorf("llm fault hook (%s): %w", h.scenario.Name, err)
        }
    }
    return h.inner.Stream(ctx, req, cb)
}
```

注入 방식: `agent.New(provider, reg, ...)` 의 `provider` 자리에 `LLMFaultHook` 을 넘긴다. `llm.Provider` 인터페이스를 implement 하기 때문에 agent 코드 변경 불필요.

### 3.7 `internal/fault/hook_ipc.go` (NEW, ~60 LOC)

Daemon Unix socket 연결에 대한 fault 주입. `net.Conn` 을 wrap 한다.

```go
package fault

import (
    "context"
    "net"
    "time"
)

// IPCFaultConn wraps net.Conn and injects latency or drops on Write.
type IPCFaultConn struct {
    net.Conn
    injector Injector
    scenario *Scenario
}

func NewIPCFaultConn(c net.Conn, inj Injector, s *Scenario) *IPCFaultConn {
    return &IPCFaultConn{Conn: c, injector: inj, scenario: s}
}

func (c *IPCFaultConn) Write(b []byte) (int, error) {
    if c.injector.Active() && c.injector.ShouldFault(c.scenario) {
        switch c.scenario.FaultType {
        case FaultSlowConn:
            // FaultDuration 상한: scenarios 정의에서 최대 5s 로 cap. net.Conn 은
            // context 비수신이므로 sleep 은 순수 timer 지만, scenarios.All() 생성
            // 시점에 FaultDuration <= 5*time.Second 검증 (scenarios/builtin.go
            // validator 또는 NewRegistry 수신 slice 검증에서 assert).
            time.Sleep(c.scenario.FaultDuration)
        case FaultPacketDrop:
            // Pretend write succeeded but discard data.
            return len(b), nil
        case FaultBackpressure:
            // FaultDuration 상한: 위 FaultSlowConn 과 동일한 5s cap 규칙 적용.
            time.Sleep(c.scenario.FaultDuration)
        case FaultWorkerPanic:
            // The panic scenario is handled at daemon runner level, not conn.
            // Fall through to normal write.
        }
    }
    return c.Conn.Write(b)
}
```

`FaultWorkerPanic` (scenario `ipc-worker-panic-recover`): daemon runner 의 goroutine 이 panic 했다가 recover 하는 경로를 검증한다. `daemon/runner.go` 의 `recover()` 블록이 이미 존재한다. fault hook 은 `daemon.Runner` 에 `WithFaultInjector(inj Injector)` 옵션을 추가하고, task dispatch goroutine 내에서 `inj.ShouldFault(s)` 가 true 이면 `panic("fault: injected worker panic")` 을 호출한다.

### 3.8 `internal/fault/reporter.go` (NEW, ~70 LOC)

```go
package fault

import (
    "encoding/json"
    "io"
    "time"
)

// **Recovery attempt 정의**: Fault 를 받은 시점 이후 agent loop 의 top-level
// iteration 1회 = recovery attempt 1회. `streamWithRetry` 내부 재시도는 별도
// count 안 함. 시나리오 #10 의 경우 daemon `recover()` block 자체는 count 안 함
// (그 다음 task re-submission 이 1회).
// `RunRecord.RecoveryAttempts` 는 이 정의에 따라 측정된 값이다.

// RunRecord captures the outcome of a single fault injection run.
type RunRecord struct {
    Timestamp        time.Time `json:"timestamp"`
    Scenario         string    `json:"scenario"`
    FaultType        FaultType `json:"fault_type"`
    RunID            string    `json:"run_id"`           // UUID v4
    Outcome          string    `json:"outcome"`          // "pass" | "fail" | "error"
    DurationMS       int64     `json:"duration_ms"`
    RecoveryAttempts int       `json:"recovery_attempts"`
    ErrorDetail      string    `json:"error_detail,omitempty"`
}

// JSONLReporter writes one RunRecord per line to an io.Writer (JSONL).
type JSONLReporter struct{ w io.Writer }

func NewJSONLReporter(w io.Writer) *JSONLReporter { return &JSONLReporter{w: w} }

func (r *JSONLReporter) Record(rec RunRecord) error {
    b, err := json.Marshal(rec)
    if err != nil {
        return fmt.Errorf("fault reporter: marshal: %w", err)
    }
    _, err = fmt.Fprintf(r.w, "%s\n", b)
    return err
}

// MDReporter reads a JSONL file and emits a Markdown summary.
type MDReporter struct {
    runFile string
    out     io.Writer
}

func NewMDReporter(runFile string, out io.Writer) *MDReporter {
    return &MDReporter{runFile: runFile, out: out}
}

// Render reads all RunRecords from runFile and writes a Markdown report.
// Layout:
//   # Fault Injection Report — <date>
//   ## Summary table  (scenario | runs | pass | fail | pass-rate | status)
//   ## Failed runs     (top-5 failures with error_detail)
//   ## Recommendations (auto-generated: scenarios below threshold)
func (r *MDReporter) Render() error { ... }
```

Report 파일 위치: `~/.elnath/data/fault/<run-id>/runs.jsonl` 및 `report.md`. CLI `elnath chaos report latest` 는 가장 최근 run-id 디렉토리를 찾아 `report.md` 를 stdout 에 출력한다.

### 3.9 `internal/fault/scenarios/builtin.go` (NEW, ~80 LOC)

10개 시나리오 전부 이 파일에 정의. §4 에 상세 카탈로그 기재.

**순환 import 방지**: `scenarios/builtin.go` 가 `internal/fault` 를 import 하고, `fault.NewRegistry()` 가 `scenarios.All()` 을 호출하면 순환 import 가 발생한다. 이를 막기 위해 아래 구조를 채택한다.

- `Scenario`, `Threshold`, `FaultType`, `Category` 등 순수 타입 정의를 `internal/fault/faulttype/` 하위 leaf 패키지로 이동. 이 패키지는 어떤 내부 패키지도 import 하지 않는다 (upstream 의존성 0).
- `scenarios/builtin.go` 와 `internal/fault` 둘 다 `internal/fault/faulttype` 만 import.
- `fault.NewRegistry` 시그니처를 `NewRegistry(scenarios []*faulttype.Scenario) *ScenarioRegistry` 로 변경. 호출자 (`cmd_chaos.go` / daemon init) 가 `scenarios.All()` 결과를 주입한다.
- 이 sub-package 는 `internal/fault/faulttype` 만 import 한다.

```go
package scenarios

import (
    "time"
    "github.com/stello/elnath/internal/fault/faulttype"
)

// All returns all built-in fault scenarios in canonical order.
func All() []*faulttype.Scenario {
    return []*fault.Scenario{
        toolBashTransientFail(),
        toolFileReadPermDenied(),
        toolWebTimeout(),
        llmAnthropic429Burst(),
        llmCodexMalformedJSON(),
        llmProviderTimeout(),
        ipcSocketSlow(),
        ipcSocketDrop(),
        ipcQueueBackpressure(),
        ipcWorkerPanicRecover(),
    }
}
```

각 함수는 대응 시나리오의 `*fault.Scenario` 를 반환. `fault.NewRegistry()` 내 `builtinScenarios()` 가 `scenarios.All()` 을 호출한다.

### 3.10 `internal/config/config.go` 변경 (MODIFY, +20 LOC)

`Config` struct 에 `FaultInjection FaultInjectionConfig` 필드 추가.

```go
// FaultInjectionConfig controls the fault injection framework.
// All fields default to off / zero — production daemons are unaffected
// unless both enabled=true AND the ELNATH_FAULT_PROFILE env var are set.
type FaultInjectionConfig struct {
    // Enabled must be true to allow fault injection. Even when true, the
    // ELNATH_FAULT_PROFILE env var is still required (guard layer 1).
    Enabled bool `yaml:"enabled"`
    // OutputDir overrides the default run output directory.
    // Default: ~/.elnath/data/fault/
    OutputDir string `yaml:"output_dir"`
}
```

`Config` struct 내:

```go
FaultInjection FaultInjectionConfig `yaml:"fault_injection"`
```

기존 `DefaultConfig()` 는 `FaultInjection: FaultInjectionConfig{Enabled: false}` 를 반환한다 (명시적 기본값 — zero value 와 동일하나 코드 가독성을 위해 명시).

### 3.11 `cmd/elnath/cmd_chaos.go` (NEW, ~80 LOC)

```go
package main

func runChaos(rt *Runtime, args []string) error {
    if len(args) == 0 {
        return printChaosHelp(rt.Out)
    }
    switch args[0] {
    case "run":
        return runChaosRun(rt, args[1:])
    case "list":
        return runChaosList(rt, args[1:])
    case "report":
        return runChaosReport(rt, args[1:])
    case "help", "--help", "-h":
        return printChaosHelp(rt.Out)
    default:
        return fmt.Errorf("unknown subcommand %q (try: run, list, report)", args[0])
    }
}
```

Flag 규약:
- `run <scenario-name>` : 단일 시나리오 실행.
- `run --all` : 전체 10개 순차 실행 (병렬 아님 — 결과 해석 용이).
- `run --runs N` : 시나리오당 실행 횟수 (default: scenario.Threshold.MaxRuns).
- `run --out <dir>` : output 디렉토리 override.
- `list` : 시나리오 이름 + 카테고리 + 설명 + threshold 표 출력.
- `report <run-id>` : 해당 run 의 Markdown report 를 stdout 에 출력.
- `report latest` : 가장 최근 run.

Dispatcher 등록: `cmd/elnath/commands.go` 에 `"chaos": runChaos` 추가.

### 3.12 `cmd/elnath/cmd_chaos.go` — Guard integration

`runChaosRun` 은 실행 전 `fault.CheckGuards(cfg.FaultInjection)` 를 호출한다. Guard 통과 시 반환된 `scenarioName` 으로 `registry.Get(scenarioName)` 를 호출해 시나리오를 찾는다. CLI `--scenario` 플래그와 env 값이 다른 경우 CLI 를 우선한다 (사용자 명시).

### 3.13 Daemon entrypoint — Guard integration

사용자가 `elnath daemon` 을 직접 실행할 때도 guard 를 우회할 수 없도록, daemon 초기화 최상단에서 `fault.CheckGuards()` 를 호출한다.

- `internal/daemon/runner.go` (또는 daemon entrypoint 함수) 의 초기화 최상단에 `fault.CheckGuards(cfg.FaultInjection)` 호출 추가. `ELNATH_FAULT_PROFILE` env 가 비어 있으면 즉시 반환 (zero-overhead fast path).
- `cmd/elnath/cmd_daemon.go` 의 daemon 실행 경로에서도 동일하게 `fault.CheckGuards(cfg.FaultInjection)` 호출. daemon runner 와 CLI entrypoint 양쪽에서 호출하면, 어느 경로로 진입하더라도 guard 를 통과한다.
- 두 callsite 모두 `CheckGuards` 가 에러를 반환하면 즉시 `os.Exit(1)` (또는 error return).

이 변경은 `internal/daemon/runner.go` 및 `cmd/elnath/cmd_daemon.go` 를 scope 에 추가한다 (§6 In scope 갱신).

---

## 4. Scenario Catalog (10개)

| # | Name | Category | Fault Type | Fault Rate | Recovery Target | Max Runs | Max Recovery Attempts | Notes |
|---|------|----------|-----------|-----------|----------------|---------|----------------------|-------|
| 1 | `tool-bash-transient-fail` | tool | transient_error | 20% | ≥ 95% | 20 | 3 | |
| 2 | `tool-file-read-perm-denied` | tool | perm_denied | 10% | ≥ 90% | 20 | 2 | |
| 3 | `tool-web-timeout` | tool | timeout | 10% | ≥ 90% | 20 | 3 | |
| 4 | `llm-anthropic-429-burst` | llm | http_429_burst | 100% (first 3 calls) | ≥ 95% | 15 | 5 | BurstLimit: 3 |
| 5 | `llm-codex-malformed-json` | llm | malformed_json | 15% | ≥ 85% | 20 | 3 | |
| 6 | `llm-provider-timeout` | llm | timeout | 30% | ≥ 80% | 15 | 3 | |
| 7 | `ipc-socket-slow` | ipc | slow_conn | 100% (50ms/write) | ≥ 98% | 20 | 1 | |
| 8 | `ipc-socket-drop` | ipc | packet_drop | 5% | ≥ 90% | 20 | 3 | 현재 daemon retransmit 없으면 baseline fail — §13 참조 |
| 9 | `ipc-queue-backpressure` | ipc | backpressure | 100% (500ms/write) | ≥ 90% | 15 | 2 | |
| 10 | `ipc-worker-panic-recover` | ipc | worker_panic | 10% | ≥ 95% | 20 | 1 | |

### 4.1 상세 설명

**1. `tool-bash-transient-fail`**
- 설명: bash tool 호출의 20% 에서 `transient_error` 를 반환. agent 의 `streamWithRetry` 및 tool error message → LLM 재계획 경로를 검증한다.
- 회복 기준: LLM 이 tool error 를 message 에 수신하고 재시도 또는 대안 plan 을 생성해 최종 작업 완료.
- PASS 조건: 20회 독립 실행 중 ≥ 19회 (95%) 에서 agent RunResult.FinishReason == "stop" (정상 완료).

**2. `tool-file-read-perm-denied`**
- 설명: file read tool 의 10% 호출에서 `os.ErrPermission` wrap 에러 반환. agent 의 "permission denied" 오류 처리 경로와 다음 액션 선택 능력 검증.
- 회복 기준: agent 가 오류를 인식하고 대안 path 를 시도하거나 graceful finish.
- PASS 조건: 20회 중 ≥ 18회 (90%).

**3. `tool-web-timeout`**
- 설명: web search / fetch tool 의 10% 에서 `context.DeadlineExceeded` 반환 (30초 대기 없이 즉시). agent 의 timeout 처리와 search 재시도 plan 검증.
- PASS 조건: 20회 중 ≥ 18회 (90%).

**4. `llm-anthropic-429-burst`**
- 설명: 각 실행 run 의 첫 3회 `Stream()` 호출에서 `HTTP429Error` 반환. `agent.streamWithRetry` 의 `retryMaxAttempts=3` 과 지수 백오프 경로 검증. 3번 이후는 정상 응답.
- FaultRate=100% 이지만 burst 횟수 제한 있음 (injector 내부 카운터로 구현).
- PASS 조건: 15회 중 ≥ 14회 (95%) — 4번째 시도에서 성공해야.

**5. `llm-codex-malformed-json`**
- 설명: Codex provider Stream 의 15% 에서 `MalformedJSONError` 반환. LLM response parsing 오류 → 재시도 경로 검증. codex 스트리밍의 JSON 파싱 실패 처리.
- PASS 조건: 20회 중 ≥ 17회 (85%) — malformed JSON recovery 는 best-effort.

**6. `llm-provider-timeout`**
- 설명: `Stream()` 호출의 30% 에서 30초 지연 후 `context.DeadlineExceeded`. agent context timeout 설정 및 retry 경로의 total-time overhead 검증.
- PASS 조건: 15회 중 ≥ 12회 (80%) — timeout 이 많아 overall latency 증가 허용.

**7. `ipc-socket-slow`**
- 설명: 모든 Unix socket Write 에 50ms 추가 latency. daemon task dispatch 가 지연에도 정상 작동하는지 검증. 기능 회복이 목표가 아닌 "정상 완료" 검증.
- PASS 조건: 20회 중 ≥ 19회 (98%) — latency 가 있어도 기능은 완전해야.

**8. `ipc-socket-drop`**
- 설명: Unix socket Write 의 5% 를 silently drop (write 성공 반환이나 데이터 버림). daemon 의 task 재전송 또는 timeout-and-retry 경로 검증.
- PASS 조건: 20회 중 ≥ 18회 (90%).
- **참고**: 이 시나리오는 daemon 에 현재 retransmit 로직이 없다면 baseline fail. LB7 는 "견고성 측정 도구" 이므로 baseline fail 자체가 가치 있다 (이후 daemon 개선의 근거). threshold 30% 완화 또는 "graceful degradation 검증" 으로 reframe 여부는 §13 참조.

**9. `ipc-queue-backpressure`**
- 설명: 모든 Write 에 500ms block (대기열 포화 시뮬레이션). daemon 의 queue pressure 처리와 sender-side timeout 검증.
- PASS 조건: 15회 중 ≥ 13회 (90%) — 각 run 이 최소 몇 초 걸림.

**10. `ipc-worker-panic-recover`**
- 설명: daemon runner 의 task dispatch goroutine 에서 10% 확률로 `panic("fault: injected worker panic")` 발생. `daemon/runner.go` 의 `recover()` 블록이 panic 을 잡고 task 를 failed 상태로 기록한 후 worker pool 이 계속 동작하는지 검증.
- PASS 조건: 20회 중 ≥ 19회 (95%) — panic 후 daemon 자체는 살아 있어야.

---

## 5. Tests

### 5.1 Unit tests

**`internal/fault/injector_test.go`**:
- `NoopInjector.Active()` → false, `ShouldFault()` → false, `InjectFault()` → nil.
- `ScenarioInjector.ShouldFault()` — 고정 seed, FaultRate=0.0 → 0/N, FaultRate=1.0 → N/N.
- `InjectFault(FaultTransientError)` → 에러 non-nil, 에러 메시지 시나리오 이름 포함.
- `InjectFault(FaultTimeout)` — context 이미 cancelled 상태로 전달 → 즉시 반환.
- `InjectFault(FaultMalformedJSON)` → `*MalformedJSONError` type assertion 성공.
- `InjectFault(FaultHTTP429Burst)` → `*HTTP429Error` type assertion 성공.

**`internal/fault/guard_test.go`**:
- env `ELNATH_FAULT_PROFILE=""`, cfg.Enabled=false → `("", nil)` 반환 (fast path).
- env `ELNATH_FAULT_PROFILE="some-scenario"`, cfg.Enabled=false → 에러 반환.
- env set + cfg.Enabled=true → `printDaemonWarning` 호출 (stderr capture), `waitWithInterrupt` mock 으로 즉시 통과.

**`internal/fault/registry_test.go`**:
- `NewRegistry()` → 10개 시나리오 로드.
- `Get("tool-bash-transient-fail")` → 올바른 시나리오 반환.
- `Get("nonexistent")` → `(nil, false)`.
- 중복 이름 등록 → panic (defer/recover 로 검증).

**`internal/fault/hook_tool_test.go`**:
- `injector.Active()==false` → `ToolFaultHook.Execute()` 가 inner registry 를 그대로 호출.
- `injector.Active()==true`, `ShouldFault()==false` → inner 호출.
- `injector.Active()==true`, `ShouldFault()==true` → inner 호출 안 함, 에러 반환.
- `TargetTool="bash"` 설정, 다른 tool 이름으로 호출 → inner 호출 (fault skip).

**`internal/fault/hook_llm_test.go`**:
- `LLMFaultHook` 이 `llm.Provider` interface 를 구현하는지 compile-time assertion.
- `ShouldFault()==true` → `Stream()` 에서 inner provider 호출 전 에러 반환.
- `ShouldFault()==false` → inner `Stream()` 위임 확인 (mock provider 사용).

**`internal/fault/hook_ipc_test.go`**:
- `FaultSlowConn` — Write 소요 시간 ≥ scenario.FaultDuration.
- `FaultPacketDrop` — Write returns len(b), nil 이나 inner Write 미호출 (mock conn).
- `injector.Active()==false` → 내부 net.Conn.Write 위임, 추가 latency 없음.

**`internal/fault/reporter_test.go`**:
- `JSONLReporter.Record()` — 출력에서 JSON unmarshal 가능, 필드 일치.
- `MDReporter.Render()` — 모든 pass, 모든 fail, 혼합 RunRecord 세트에 대해 Markdown 출력 non-empty, "PASS"/"FAIL" 문자열 포함.

### 5.2 Scenario run test (integration)

**`internal/fault/scenarios/scenarios_test.go`**:
- `All()` 가 정확히 10개 반환.
- 각 시나리오의 `Threshold.MaxRuns > 0`, `Threshold.RecoveryRate > 0`.
- 이름 슬러그가 `[a-z0-9-]+` 패턴에 맞는지 regexp 검증.
- `FaultRate ∈ [0, 1]` 범위 검증.

**`internal/fault/e2e_test.go`** (빌드 태그 `//go:build fault_e2e`):
- 전체 시나리오 실행은 일반 CI 에서 skip. `go test -tags fault_e2e` 로만 실행.
- `tool-bash-transient-fail` 단일 시나리오: mock agent + ToolFaultHook, 20 runs, ≥ 19 pass 검증.
- 실행 시간 ≤ 120초 (mock 환경 기준).

### 5.3 Config test

**`internal/config/config_test.go`** (기존 파일 수정):
- `DefaultConfig()` → `FaultInjection.Enabled == false`.
- YAML `fault_injection:\n  enabled: true\n  output_dir: /tmp/fault` → 정상 파싱.

### 5.4 CLI test

**`cmd/elnath/cmd_chaos_test.go`** (NEW):
- `runChaos([]string{})` → help 출력.
- `runChaos([]string{"list"})` → 10행 이상 출력.
- `runChaos([]string{"unknown"})` → 에러.
- `runChaos([]string{"run", "nonexistent-scenario"})` → 에러 (scenario not found).

---

## 6. Scope Boundaries

**In scope** (이 spec):
- `internal/fault/` 신규 패키지 전체 (scenario, registry, injector, guard, hook_tool, hook_llm, hook_ipc, reporter)
- `internal/fault/scenarios/builtin.go` — 10개 built-in 시나리오
- `internal/config/config.go` — `FaultInjectionConfig` 필드 추가
- `internal/agent/agent.go` — `WithToolExecutor` 옵션 + `tools.Executor` interface 추가 (최소 변경)
- `cmd/elnath/cmd_chaos.go` — `elnath chaos` CLI
- `cmd/elnath/commands.go` — dispatcher 등록 (+1 line)
- Unit + integration + e2e (빌드 태그) 테스트
- `PHASE-F6-LB7-OPENCODE-PROMPT.md` (별도 작성)

**Out of scope** — defer:

1. **Filesystem fault 카테고리** (Q8 결정 — 4번 defer). `os.Open` / `os.WriteFile` 에 fault 주입. 별도 phase.
2. **Network fault 카테고리** (Q8 결정 — 5번 defer). TCP connection drop / DNS failure 시뮬레이션. 별도 phase.
3. **Time-skew fault 카테고리** (Q8 결정 — 6번 defer). 시계 이상 시뮬레이션. 별도 phase.
4. **CI 자동 실행** (`--all` 을 make target 에 등록). CI 는 unit test 만. e2e 는 dog-food.
5. **Distributed fault 주입** (멀티 인스턴스 Elnath). 단일 daemon 만.
6. **Fault 이력 대시보드** (웹 UI / Telegram 보고). CLI report 만.
7. **Scenario 커스텀 YAML 로드**. builtin Go struct 만. 동적 로드는 별도 enhancement.
8. **chaos 실행 중 daemon state 스냅샷**. 회복 여부만 측정 (내부 state dump 아님).

---

## 7. Verification Gates

### 7.1 Build / Type check

```bash
cd /Users/stello/elnath
go vet ./internal/fault/... ./internal/config/... ./internal/agent/... ./cmd/elnath/...
go build ./...
```

### 7.2 Unit tests

```bash
go test -race ./internal/fault/... ./internal/config/... ./internal/agent/... ./cmd/elnath/...
```

### 7.3 Fault E2E (선택 — dog-food)

```bash
# 3중 가드 활성화
export ELNATH_FAULT_PROFILE=tool-bash-transient-fail

# config 에 fault_injection.enabled: true 설정 후:
./elnath chaos run tool-bash-transient-fail --runs 5
# expected: 5/5 pass (또는 4/5), report 출력

./elnath chaos list
# expected: 10행 시나리오 표

./elnath chaos report latest
# expected: Markdown 출력, "PASS" 포함
```

### 7.4 Code hygiene

```bash
# Production guard 확인: env 없이는 fault 코드 실행 안 됨
grep -rn "fault.CheckGuards" /Users/stello/elnath/internal/daemon/ /Users/stello/elnath/cmd/elnath/cmd_daemon.go
# expected: 2+ callsites (daemon runner 초기화 + cmd_daemon.go daemon 실행 경로)
grep -r "ELNATH_FAULT_PROFILE" /Users/stello/elnath/cmd/ /Users/stello/elnath/internal/daemon/
# expected: guard.go 정의 + cmd_chaos.go 참조 포함

# NoopInjector 기본값 확인
grep -r "NoopInjector" /Users/stello/elnath/internal/agent/ /Users/stello/elnath/internal/daemon/
# expected: 기본 초기화 경로에 NoopInjector 사용

# 디버그 코드 없음 확인
grep -rn "fmt\.Print\b\|log\.Print\b" /Users/stello/elnath/internal/fault/ | grep -v "_test.go"
# expected: 없음 (slog 만 허용)
```

---

## 8. Commit Message Template

```
feat: phase F-6 LB7 fault injection framework

카테고리별 fault 주입으로 production 코드 경로의 회복 능력 검증.
3중 가드 (env + config + daemon 5초 경고) 로 실수 활성화 방지.

- internal/fault: Scenario, Registry, Injector, NoopInjector,
  ToolFaultHook, LLMFaultHook, IPCFaultConn, JSONLReporter, MDReporter,
  guard (CheckGuards + daemon stderr warning)
- internal/fault/scenarios: 10 built-in scenarios
  (tool×3, llm×3, ipc×4) with per-scenario PASS thresholds
- internal/config: FaultInjectionConfig (enabled + output_dir)
- internal/agent: WithToolExecutor option + tools.Executor interface
  (minimal change, no behaviour change when fault inactive)
- cmd elnath chaos {run,list,report}
  - run: single scenario or --all, --runs N override
  - list: scenario catalog table
  - report: JSONL → Markdown render
- Unit + integration tests; e2e behind fault_e2e build tag

Deferred (별도 phase):
- Filesystem / network / time-skew fault categories (Q8)
- CI auto-run of fault e2e suite
- Dynamic scenario YAML loading
```

---

## 9. OpenCode Prompt

`docs/specs/PHASE-F6-LB7-OPENCODE-PROMPT.md` (별도 작성 예정).

내부 구조 (집계):
- §1 Context (메모리 + 본 spec + decisions 요약)
- §2 생성/수정 파일 목록 (절대 경로)
- §3 파일별 상세 구현 지시
- §4 테스트 요구
- §5 Verification gate 명령
- §6 Commit message 템플릿
- §7 자가 리뷰 체크리스트

---

## 10. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|-----------|
| production daemon 에서 실수로 fault 활성화 | CRITICAL | 3중 가드: env var 없으면 코드 미진입 (fast path return). `fault.CheckGuards()` 는 env 없으면 단 1회 `os.Getenv` 후 즉시 반환. 오버헤드 0. |
| daemon 시작 5초 경고 중 사용자가 놓침 | HIGH | stderr 에 ANSI 빨간색 + 이모지, 명확한 "Ctrl-C to abort" 메시지. plist / systemd 자동 시작 경로에서는 SIGTERM 이 5초 내 도달하면 즉시 중단. |
| FaultPacketDrop 이 data loss 처럼 보여 혼란 | MED | RunRecord 의 `outcome` 과 `error_detail` 에 "fault: packet drop injected" 명시. report 에 "fault-injected drop (not real)" 각주. |
| 10 시나리오 × MaxRuns 실행 시간 (~30분) 이 CI timeout 초과 | HIGH | e2e 는 `//go:build fault_e2e` 빌드 태그로 일반 CI 에서 skip. CI 는 unit test 만. |
| ipc-worker-panic-recover 에서 daemon 이 실제로 crash | HIGH | `panic` 은 항상 `recover()` 블록 안에서만 발생. `daemon/runner.go` 의 기존 recover 블록이 이미 catch. 추가 defer recover 를 fault hook goroutine 에도 감쌈. |
| `tools.Executor` interface 도입이 기존 tool 통계 집계 깨뜨림 | MED | `ToolFaultHook.Execute` 는 inner 호출 전에 fault error 를 반환할 때도 `toolStatsMu` 를 통해 error count 를 기록. 기존 `toolStatAcc.errors++` 경로와 동일. |
| Scenario FaultRate 설정 오류로 항상 fail | MED | `scenarios_test.go` 에서 `FaultRate ∈ [0, 1]` 범위 + `RecoveryRate > FaultRate * 0.5` (회복 가능성이 있는 threshold) 검증. |
| JSONL report 디렉토리 권한 문제 | LOW | `~/.elnath/data/fault/<run-id>/` 생성 시 `os.MkdirAll(path, 0700)`. 실패 시 `--out /tmp/...` 로 override 가능. |

---

## 11. Estimated LOC Breakdown

| File | NEW / MODIFY | Est LOC |
|------|-------------|---------|
| `internal/fault/scenario.go` | NEW | 60 |
| `internal/fault/registry.go` | NEW | 40 |
| `internal/fault/injector.go` | NEW | 70 |
| `internal/fault/guard.go` | NEW | 55 |
| `internal/fault/hook_tool.go` | NEW | 55 |
| `internal/fault/hook_llm.go` | NEW | 55 |
| `internal/fault/hook_ipc.go` | NEW | 60 |
| `internal/fault/reporter.go` | NEW | 80 |
| `internal/fault/scenarios/builtin.go` | NEW | 90 |
| `cmd/elnath/cmd_chaos.go` | NEW | 80 |
| `cmd/elnath/commands.go` | MODIFY | +2 |
| `internal/config/config.go` | MODIFY | +20 |
| `internal/agent/agent.go` | MODIFY | +15 (WithToolExecutor + Executor interface) |
| Tests (8 files, _test.go) | NEW | ~270 |

**Production 소계**: ~682 LOC
**Test 소계**: ~270 LOC
**Total**: ~952 LOC

추정 근거: LB7 는 3개 hook 레이어 × 각 50-60 LOC + guard (55) + reporter (80) + scenarios (90) + CLI (80) + config/agent 수정 (~37). 초기 DECISIONS 추정치 700 LOC 보다 약간 높은 이유는 guard 의 signal 처리와 reporter 의 Markdown 렌더링이 예상보다 세부 경로가 많기 때문. 필요 시 `hook_ipc.go` 의 backpressure/panic 시나리오를 `hook_tool.go` 에 통합해 1 파일 절감 (-40 LOC) 가능하나 분리가 유지보수에 유리.

---

## 12. Next After This Spec

1. 사용자 리뷰 → 수정 반영
2. F7 / F8 spec 병렬 작성 (LB6/LB7 이미 완료)
3. opencode prompt 4개 작성 (LB6/LB7/F7/F8)
4. opencode 4 세션 병렬 위임
5. LB7 구현 완료 후 `elnath chaos run --all` dog-food 실행 → scorecard 갱신

---

## 13. Spec-Stage Decisions

아래는 Q7-Q12 에서 명시되지 않았으나 spec 작성 시 내부적으로 결정한 사항. 사용자 확정이 없으므로 향후 검토 가능.

| ID | Question | Decision | Rationale | Revisit? |
|----|----------|----------|-----------|----------|
| S5 | Tool hook 주입 방식 | `WithToolExecutor(tools.Executor)` 옵션 추가, `tools.Executor` 1-method interface | agent 코드 최소 변경. 기존 Registry 메서드 시그니처 불변. | 낮음 — 패턴이 LB6 의 `WithCodexOAuthTimeout` 과 일관됨. |
| S6 | Scenario 포맷 | Embedded Go struct (`scenarios/builtin.go`) | 의존성 0, 컴파일 시 정적 검증. YAML 동적 로드는 defer. | 중간 — 사용자 커스텀 시나리오 요구 시 YAML 지원 추가 필요. |
| S7 | 429 burst 구현 | Injector 내부 per-run 카운터 (첫 3회 강제 주입) | `FaultRate=100%` 를 제한된 횟수에만 적용. `rng` 기반 확률과 다른 패턴을 injector 가 `FaultType==FaultHTTP429Burst` 분기로 처리. | 낮음 — 충분히 실용적. |
| S8 | Run ID 형식 | `time.Now().Format("20060102T150405")` + 4자 hex suffix | UUID 라이브러리 추가 없이 충분한 고유성. 디렉토리 이름이 사람이 읽기 쉬움. | 낮음. |
| S9 | Daemon 경고 TTY 감지 | `golang.org/x/term.IsTerminal` | LB6 passphrase TTY 감지와 동일 라이브러리. 신규 의존성 없음. | 낮음. |
| S10 | 시나리오 #8 (`ipc-socket-drop`) threshold | 현재 90% 로 설정되어 있으나, daemon 에 retransmit 로직이 없으면 baseline fail 예상. **현재 daemon 코드 상태 확인 후 threshold 확정**: retransmit 없으면 30% 로 완화하거나 시나리오를 "graceful degradation 검증" 으로 reframe. | LB7 는 현재 상태 측정 도구이므로 baseline fail 자체가 가치. threshold 는 daemon 코드 반영 후 결정. | 높음 — 구현 전 확인 필요. |
| S10 | `elnath chaos run --all` 실행 순서 | `All()` 반환 순서 (카테고리 그룹 순, tool → llm → ipc) | 사람이 읽을 때 카테고리별로 묶여서 리포트 가독성 높음. | 낮음. |
