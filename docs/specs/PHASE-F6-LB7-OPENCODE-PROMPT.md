# Phase F-6 LB7 — OpenCode 구현 지시서 (Fault Injection Framework)

> **진실 원천**: `docs/specs/PHASE-F6-LB7-FAULT-INJECTION.md`
> **작업 디렉토리**: `/Users/stello/elnath` (브랜치: `feat/telegram-redesign`)
> **이 파일의 역할**: OpenCode 에게 전달하는 구현 지시서. 코드 작성 전 spec 전문을 반드시 읽을 것.

---

## 1. Context

### 프로젝트 개요

Elnath 는 순수 Go CLI 자율 AI 비서 daemon이다 (`go1.25+`, CGo 없음, `log/slog`, JSONL session persistence, `modernc.org/sqlite`). LLM은 Anthropic 주력, 실행 경로는 agent loop → tool batch → streamWithRetry → daemon runner.

### Phase F-6 LB7 목적

Elnath의 **복구 경로를 실제 production 코드에서 검증**한다.

- **Tool 오류**: bash transient fail, file read perm-denied, web timeout
- **LLM 오류**: HTTP 429 burst, malformed JSON, provider timeout
- **IPC 오류**: Unix socket 지연, packet drop, backpressure, worker panic

3개 카테고리 × 10개 시나리오를 정의하고, production 바이너리에 fault hook을 내장한다. **env + config + daemon 경고** 3중 가드로 실수 활성화를 완전 차단한다.

### 전제 조건

- **F-5 Phase 2** (`c97d24f` 이후): `internal/llm` Provider interface, `AnthropicProvider`, `CodexOAuthProvider` 안정화됨.
- **LB6**: credential 흐름 정리 완료, `golang.org/x/term` 의존성이 이미 `go.mod`에 있음.
- `internal/agent/agent.go`: `streamWithRetry`, `executeApprovedToolCalls` 존재. `WithHooks` 옵션 패턴 있음.
- `internal/daemon/runner.go`: task dispatch goroutine + `recover()` 블록 이미 존재.

### 결정 사항 (변경 금지)

| ID | 결정 |
|----|------|
| Q7 | Daemon-integrated, env-gated (외부 harness 아님) |
| Q8 | Tool / LLM / IPC 3개 카테고리만 (filesystem, network, time-skew는 defer) |
| Q9 | 10개 시나리오 |
| Q10 | Per-scenario threshold (카테고리별 상이) |
| Q11 | 3중 가드 (env + config.enabled + daemon 5초 경고) |
| Q12 | JSONL + Markdown 리포트 |
| S5 | `WithToolExecutor(tools.Executor)` 옵션으로 agent에 주입 |
| S6 | Embedded Go struct — YAML 동적 로드 없음 |

---

## 2. Scope — 신규/수정 파일 목록

### 신규 파일

```
internal/fault/faulttype/types.go            (leaf package, ~60 LOC)
internal/fault/scenario.go                   (~40 LOC, faulttype 재-export + registry 지원)
internal/fault/registry.go                   (~40 LOC)
internal/fault/injector.go                   (~70 LOC)
internal/fault/guard.go                      (~55 LOC)
internal/fault/hook_tool.go                  (~55 LOC)
internal/fault/hook_llm.go                   (~55 LOC)
internal/fault/hook_ipc.go                   (~60 LOC)
internal/fault/reporter.go                   (~80 LOC)
internal/fault/scenarios/builtin.go          (~90 LOC)
internal/fault/injector_test.go
internal/fault/guard_test.go
internal/fault/registry_test.go
internal/fault/hook_tool_test.go
internal/fault/hook_llm_test.go
internal/fault/hook_ipc_test.go
internal/fault/reporter_test.go
internal/fault/scenarios/scenarios_test.go
internal/fault/e2e_test.go                   (//go:build fault_e2e)
cmd/elnath/cmd_chaos.go                      (~80 LOC)
cmd/elnath/cmd_chaos_test.go
```

### 수정 파일

```
internal/config/config.go     (+20 LOC: FaultInjectionConfig 추가)
internal/agent/agent.go       (+15 LOC: tools.Executor interface + WithToolExecutor 옵션)
internal/daemon/runner.go     (+8 LOC: CheckGuards 호출 + WithFaultInjector 옵션)
cmd/elnath/commands.go        (+2 LOC: "chaos" dispatcher 등록)
cmd/elnath/cmd_daemon.go      (+5 LOC: CheckGuards 호출)
internal/config/config_test.go (기존 파일 수정)
```

spec §11 LOC table 참조: production ~682 LOC, test ~270 LOC.

---

## 3. Task — 파일별 구현 지시

### 3.1 `internal/fault/faulttype/types.go` (신규, leaf package)

**순환 import 방지를 위한 leaf package**다. 이 패키지는 어떤 내부 패키지도 import하지 않는다 (stdlib `"time"` 만 허용).

```go
package faulttype

import "time"

type Category string

const (
    CategoryTool Category = "tool"
    CategoryLLM  Category = "llm"
    CategoryIPC  Category = "ipc"
)

type FaultType string

const (
    FaultTransientError FaultType = "transient_error"
    FaultPermDenied     FaultType = "perm_denied"
    FaultTimeout        FaultType = "timeout"
    FaultMalformedJSON  FaultType = "malformed_json"
    FaultHTTP429Burst   FaultType = "http_429_burst"
    FaultSlowConn       FaultType = "slow_conn"
    FaultPacketDrop     FaultType = "packet_drop"
    FaultBackpressure   FaultType = "backpressure"
    FaultWorkerPanic    FaultType = "worker_panic"
)

type Threshold struct {
    RecoveryRate        float64
    MaxRuns             int
    MaxRecoveryAttempts int
}

type Scenario struct {
    Name          string
    Category      Category
    FaultType     FaultType
    Description   string
    FaultRate     float64
    FaultDuration time.Duration
    Threshold     Threshold
    TargetTool    string
    BurstLimit    int
}
```

**절대 추측 금지**: 필드 이름, 타입 상수 문자열값은 이 지시서에 있는 그대로 사용한다.

---

### 3.2 `internal/fault/scenario.go` (신규)

`faulttype` 타입들을 `fault` 패키지로 재-export하는 alias 파일. `fault.Scenario`, `fault.FaultType` 등으로 외부에서 접근 가능하게 한다.

```go
package fault

import "github.com/stello/elnath/internal/fault/faulttype"

// Type aliases so callers can use fault.Scenario without importing faulttype.
type (
    Scenario  = faulttype.Scenario
    Threshold = faulttype.Threshold
    Category  = faulttype.Category
    FaultType = faulttype.FaultType
)

const (
    CategoryTool = faulttype.CategoryTool
    CategoryLLM  = faulttype.CategoryLLM
    CategoryIPC  = faulttype.CategoryIPC

    FaultTransientError = faulttype.FaultTransientError
    FaultPermDenied     = faulttype.FaultPermDenied
    FaultTimeout        = faulttype.FaultTimeout
    FaultMalformedJSON  = faulttype.FaultMalformedJSON
    FaultHTTP429Burst   = faulttype.FaultHTTP429Burst
    FaultSlowConn       = faulttype.FaultSlowConn
    FaultPacketDrop     = faulttype.FaultPacketDrop
    FaultBackpressure   = faulttype.FaultBackpressure
    FaultWorkerPanic    = faulttype.FaultWorkerPanic
)
```

---

### 3.3 `internal/fault/registry.go` (신규)

```go
package fault

import (
    "fmt"
    "github.com/stello/elnath/internal/fault/faulttype"
)

type ScenarioRegistry struct {
    scenarios map[string]*faulttype.Scenario
    ordered   []*faulttype.Scenario
}

// NewRegistry는 외부에서 주입한 scenarios slice로 레지스트리를 구성한다.
// 시그니처: NewRegistry(scenarios []*faulttype.Scenario) *ScenarioRegistry
// (builtinScenarios를 내부에서 호출하지 않음 — 순환 import 방지)
func NewRegistry(scenarios []*faulttype.Scenario) *ScenarioRegistry { ... }

func (r *ScenarioRegistry) Register(s *faulttype.Scenario) { /* 중복 시 panic */ }
func (r *ScenarioRegistry) Get(name string) (*faulttype.Scenario, bool) { ... }
func (r *ScenarioRegistry) All() []*faulttype.Scenario { return r.ordered }
```

**주의**: `builtinScenarios()` 를 registry.go 내에서 호출하지 않는다. 호출자(`cmd_chaos.go`, daemon init)가 `scenarios.All()` 결과를 `NewRegistry(scenarios.All())` 형태로 주입한다.

---

### 3.4 `internal/fault/injector.go` (신규)

**Critical invariant — zero overhead**: `Active()` 가 false일 때 모든 hook 경로는 즉시 반환, allocation 0.

```go
package fault

import (
    "context"
    "fmt"
    "math/rand"
    "os"
    "sync/atomic"
    "time"
    "github.com/stello/elnath/internal/fault/faulttype"
)

type Injector interface {
    Active() bool
    ShouldFault(s *faulttype.Scenario) bool
    InjectFault(ctx context.Context, s *faulttype.Scenario) error
}

type ScenarioInjector struct {
    scenario   *faulttype.Scenario
    rng        *rand.Rand
    active     atomic.Bool
    burstCount atomic.Int64  // FaultHTTP429Burst 전용
    burstLimit int           // s.BurstLimit에서 복사
}

func NewScenarioInjector(s *faulttype.Scenario, seed int64) *ScenarioInjector {
    inj := &ScenarioInjector{
        scenario:   s,
        rng:        rand.New(rand.NewSource(seed)),
        burstLimit: s.BurstLimit,
    }
    inj.active.Store(true)
    return inj
}

func (i *ScenarioInjector) Active() bool { return i.active.Load() }

// ShouldFault: FaultHTTP429Burst는 burstCount < burstLimit일 때만 true.
// 나머지는 FaultRate 확률.
func (i *ScenarioInjector) ShouldFault(s *faulttype.Scenario) bool {
    if !i.Active() { return false }
    if s.FaultType == faulttype.FaultHTTP429Burst {
        n := i.burstCount.Add(1)
        return n <= int64(i.burstLimit)
    }
    return i.rng.Float64() < s.FaultRate
}

// ResetForRun: 각 독립 run 시작 시 호출. burstCount를 0으로 리셋.
func (i *ScenarioInjector) ResetForRun() { i.burstCount.Store(0) }

func (i *ScenarioInjector) InjectFault(ctx context.Context, s *faulttype.Scenario) error {
    switch s.FaultType {
    case faulttype.FaultTransientError:
        return fmt.Errorf("fault: injected transient error (%s)", s.Name)
    case faulttype.FaultPermDenied:
        return fmt.Errorf("fault: injected permission denied (%s): %w", s.Name, os.ErrPermission)
    case faulttype.FaultTimeout:
        dur := s.FaultDuration
        if dur == 0 { dur = 30 * time.Second }
        select {
        case <-time.After(dur):
        case <-ctx.Done():
        }
        return context.DeadlineExceeded
    case faulttype.FaultMalformedJSON:
        return &MalformedJSONError{Scenario: s.Name}
    case faulttype.FaultHTTP429Burst:
        return &HTTP429Error{Scenario: s.Name, RetryAfter: 1 * time.Second}
    default:
        return fmt.Errorf("fault: unknown fault type %q in scenario %q", s.FaultType, s.Name)
    }
}

// NoopInjector: fault injection 비활성화 시 사용. Active() == false, zero-cost.
type NoopInjector struct{}

func (NoopInjector) Active() bool                                       { return false }
func (NoopInjector) ShouldFault(_ *faulttype.Scenario) bool             { return false }
func (NoopInjector) InjectFault(_ context.Context, _ *faulttype.Scenario) error { return nil }

// Sentinel error types for typed assertions in tests.
type MalformedJSONError struct{ Scenario string }
func (e *MalformedJSONError) Error() string { return "fault: injected malformed JSON in " + e.Scenario }

type HTTP429Error struct {
    Scenario   string
    RetryAfter time.Duration
}
func (e *HTTP429Error) Error() string { return "fault: injected HTTP 429 in " + e.Scenario }
```

---

### 3.5 `internal/fault/guard.go` (신규)

**3중 가드 핵심 파일**. 실수 활성화를 막는 가장 중요한 코드다.

```go
package fault

import (
    "fmt"
    "os"
    "os/signal"
    "syscall"
    "time"
    "golang.org/x/term"
)

const (
    envFaultProfile = "ELNATH_FAULT_PROFILE"
    guardWaitSecs   = 5
)

type GuardConfig struct {
    Enabled bool
}

// CheckGuards는 3중 가드를 통과하면 활성 scenario 이름을 반환한다.
// env가 비어 있으면 즉시 ("", nil) 반환 — zero-overhead fast path.
// 에러는 설정 불일치 또는 사용자 SIGINT 취소 시에만 반환.
func CheckGuards(cfg GuardConfig) (scenarioName string, err error) {
    profile := os.Getenv(envFaultProfile)
    if profile == "" {
        return "", nil  // fast path: single os.Getenv, no allocation
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

func printDaemonWarning(profile string) {
    isTTY := term.IsTerminal(int(os.Stderr.Fd()))
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

`golang.org/x/term` 은 LB6 passphrase prompt가 이미 사용하므로 go.mod에 추가 불필요.

---

### 3.6 `internal/fault/hook_tool.go` (신규)

`tools.Executor` interface (agent.go에 추가할 1-method interface)를 구현한다.

```go
package fault

import (
    "context"
    "encoding/json"
    "github.com/stello/elnath/internal/fault/faulttype"
    "github.com/stello/elnath/internal/tools"
)

// ToolFaultHook wraps a tools.Registry and implements tools.Executor.
// injector.Active() == false 이면 inner.Execute() 로 직접 위임 (zero overhead).
type ToolFaultHook struct {
    inner    *tools.Registry
    injector Injector
    scenario *faulttype.Scenario
}

func NewToolFaultHook(reg *tools.Registry, inj Injector, s *faulttype.Scenario) *ToolFaultHook {
    return &ToolFaultHook{inner: reg, injector: inj, scenario: s}
}

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

// Registry returns the underlying registry for metadata-only queries
// (tool listing, permission checks) that must never be faulted.
func (h *ToolFaultHook) Registry() *tools.Registry { return h.inner }
```

`internal/agent/agent.go` 에 추가할 내용:

```go
// tools.Executor는 agent가 tool 실행을 위임하는 1-method interface.
// 기본 구현은 *tools.Registry. fault mode에서는 ToolFaultHook.
type Executor interface {
    Execute(ctx context.Context, name string, input json.RawMessage) (tools.ToolResult, error)
}

// WithToolExecutor는 agent의 기본 tools.Registry Executor를 교체한다.
// fault injection이 비활성화된 기본 경로에서는 호출되지 않으므로 zero overhead.
func WithToolExecutor(exec tools.Executor) Option { ... }
```

`executeApprovedToolCalls` 내부에서 `a.tools.Execute` → `a.executor.Execute` 로 변경. 기본값은 `a.tools` (기존 Registry).

---

### 3.7 `internal/fault/hook_llm.go` (신규)

```go
package fault

import (
    "context"
    "fmt"
    "github.com/stello/elnath/internal/fault/faulttype"
    "github.com/stello/elnath/internal/llm"
)

// LLMFaultHook wraps llm.Provider and implements llm.Provider.
// agent.New(provider, ...) 의 provider 자리에 그대로 전달 가능.
type LLMFaultHook struct {
    inner    llm.Provider
    injector Injector
    scenario *faulttype.Scenario
}

func NewLLMFaultHook(p llm.Provider, inj Injector, s *faulttype.Scenario) *LLMFaultHook {
    return &LLMFaultHook{inner: p, injector: inj, scenario: s}
}

func (h *LLMFaultHook) Name() string              { return h.inner.Name() }
func (h *LLMFaultHook) Models() []llm.ModelInfo   { return h.inner.Models() }

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

**Compile-time assertion** (테스트에 추가): `var _ llm.Provider = (*LLMFaultHook)(nil)`

---

### 3.8 `internal/fault/hook_ipc.go` (신규)

```go
package fault

import (
    "net"
    "time"
    "github.com/stello/elnath/internal/fault/faulttype"
)

// IPCFaultConn wraps net.Conn and injects latency / drops on Write.
type IPCFaultConn struct {
    net.Conn
    injector Injector
    scenario *faulttype.Scenario
}

func NewIPCFaultConn(c net.Conn, inj Injector, s *faulttype.Scenario) *IPCFaultConn {
    return &IPCFaultConn{Conn: c, injector: inj, scenario: s}
}

func (c *IPCFaultConn) Write(b []byte) (int, error) {
    if c.injector.Active() && c.injector.ShouldFault(c.scenario) {
        switch c.scenario.FaultType {
        case faulttype.FaultSlowConn:
            // FaultDuration <= 5s (scenarios/builtin.go validator에서 검증됨)
            time.Sleep(c.scenario.FaultDuration)
        case faulttype.FaultPacketDrop:
            // Data silently discarded. Write 성공으로 보고.
            return len(b), nil
        case faulttype.FaultBackpressure:
            // FaultDuration <= 5s (동일한 cap 규칙)
            time.Sleep(c.scenario.FaultDuration)
        case faulttype.FaultWorkerPanic:
            // panic은 daemon/runner.go 레벨에서 처리. conn level에서는 pass-through.
        }
    }
    return c.Conn.Write(b)
}
```

`FaultWorkerPanic` 처리: `internal/daemon/runner.go` 에 `WithFaultInjector(inj fault.Injector)` 옵션 추가. task dispatch goroutine 내에서 `inj.ShouldFault(s)` true 이면 `panic("fault: injected worker panic")` 호출. 기존 `recover()` 블록이 이를 잡는다. fault hook goroutine에도 별도 `defer recover()` 추가.

**IPC sleep cap 검증**: `scenarios/builtin.go` 의 `All()` 내에서 혹은 `NewRegistry` 수신 시 `FaultSlowConn`/`FaultBackpressure` 시나리오의 `FaultDuration <= 5*time.Second` 를 assert하는 validator 실행.

---

### 3.9 `internal/fault/reporter.go` (신규)

**Recovery attempt 정의** (이 정의를 코드 주석에 그대로 복사):
> Fault를 수신한 후 agent loop의 top-level iteration 1회 = recovery attempt 1회. `streamWithRetry` 내부 재시도는 count하지 않는다. 시나리오 #10의 daemon `recover()` block 자체는 count하지 않으며, 그 다음 task re-submission이 1회다.

```go
package fault

import (
    "encoding/json"
    "fmt"
    "io"
    "time"
)

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

// MDReporter reads a JSONL runs file and renders a Markdown report.
// Layout:
//   # Fault Injection Report — <date>
//   ## Summary table (scenario | runs | pass | fail | pass-rate | status)
//   ## Failed runs (top-5 with error_detail)
//   ## Recommendations (scenarios below threshold)
type MDReporter struct {
    runFile string
    out     io.Writer
}

func NewMDReporter(runFile string, out io.Writer) *MDReporter {
    return &MDReporter{runFile: runFile, out: out}
}

func (r *MDReporter) Render() error { ... }
```

Report 파일 위치: `~/.elnath/data/fault/<run-id>/runs.jsonl` 및 `report.md`.
`os.MkdirAll(path, 0700)` 으로 디렉토리 생성.

---

### 3.10 `internal/fault/scenarios/builtin.go` (신규)

**이 파일은 `faulttype` 패키지만 import한다.** `internal/fault` 를 import하면 순환 import 발생.

```go
package scenarios

import (
    "time"
    "github.com/stello/elnath/internal/fault/faulttype"
)

// All returns all 10 built-in fault scenarios in canonical order.
// 순서 변경 금지 — scenario #8 은 baseline fail 가능성이 문서화된 특수 케이스.
func All() []*faulttype.Scenario {
    ss := []*faulttype.Scenario{
        toolBashTransientFail(),
        toolFileReadPermDenied(),
        toolWebTimeout(),
        llmAnthropic429Burst(),
        llmCodexMalformedJSON(),
        llmProviderTimeout(),
        ipcSocketSlow(),
        ipcSocketDrop(),       // #8: baseline fail 허용 — §13 참조
        ipcQueueBackpressure(),
        ipcWorkerPanicRecover(),
    }
    // IPC sleep cap validator: FaultSlowConn/FaultBackpressure FaultDuration <= 5s 강제
    for _, s := range ss {
        if (s.FaultType == faulttype.FaultSlowConn || s.FaultType == faulttype.FaultBackpressure) &&
            s.FaultDuration > 5*time.Second {
            panic("fault: scenario " + s.Name + " FaultDuration exceeds 5s cap")
        }
    }
    return ss
}
```

**10개 시나리오 전부 정의 — 이름, 수치, threshold를 정확히 아래 table대로 구현 (추측 금지)**:

| # | Name | Category | FaultType | FaultRate | FaultDuration | BurstLimit | RecoveryRate | MaxRuns | MaxRecoveryAttempts |
|---|------|----------|-----------|-----------|---------------|-----------|-------------|---------|---------------------|
| 1 | `tool-bash-transient-fail` | tool | transient_error | 0.20 | — | 0 | 0.95 | 20 | 3 |
| 2 | `tool-file-read-perm-denied` | tool | perm_denied | 0.10 | — | 0 | 0.90 | 20 | 2 |
| 3 | `tool-web-timeout` | tool | timeout | 0.10 | — | 0 | 0.90 | 20 | 3 |
| 4 | `llm-anthropic-429-burst` | llm | http_429_burst | 1.00 | — | **3** | 0.95 | 15 | 5 |
| 5 | `llm-codex-malformed-json` | llm | malformed_json | 0.15 | — | 0 | 0.85 | 20 | 3 |
| 6 | `llm-provider-timeout` | llm | timeout | 0.30 | — | 0 | 0.80 | 15 | 3 |
| 7 | `ipc-socket-slow` | ipc | slow_conn | 1.00 | **50ms** | 0 | 0.98 | 20 | 1 |
| 8 | `ipc-socket-drop` | ipc | packet_drop | 0.05 | — | 0 | 0.90 | 20 | 3 |
| 9 | `ipc-queue-backpressure` | ipc | backpressure | 1.00 | **500ms** | 0 | 0.90 | 15 | 2 |
| 10 | `ipc-worker-panic-recover` | ipc | worker_panic | 0.10 | — | 0 | 0.95 | 20 | 1 |

시나리오 #1 의 `TargetTool = "bash"`, #2 의 `TargetTool = "read_file"` (또는 해당 tool 이름을 codebase에서 grep해서 정확히 맞출 것).

**시나리오 #8 baseline 주석**: 코드 주석에 "daemon에 retransmit 로직이 없으면 baseline fail 가능 — spec §13 참조. 이것 자체가 측정 목적이다."를 명시.

---

### 3.11 `internal/config/config.go` 수정

`Config` struct에 추가:

```go
FaultInjection FaultInjectionConfig `yaml:"fault_injection"`
```

신규 struct:

```go
// FaultInjectionConfig controls the fault injection framework.
// 기본값은 off — env var 없으면 production daemon에 영향 0.
type FaultInjectionConfig struct {
    Enabled   bool   `yaml:"enabled"`
    OutputDir string `yaml:"output_dir"`
}
```

`DefaultConfig()` 에 `FaultInjection: FaultInjectionConfig{Enabled: false}` 명시 추가 (zero value와 동일하나 가독성).

---

### 3.12 `cmd/elnath/cmd_chaos.go` (신규)

```go
package main

func runChaos(rt *Runtime, args []string) error {
    if len(args) == 0 {
        return printChaosHelp(rt.Out)
    }
    switch args[0] {
    case "run":    return runChaosRun(rt, args[1:])
    case "list":   return runChaosList(rt, args[1:])
    case "report": return runChaosReport(rt, args[1:])
    case "help", "--help", "-h":
        return printChaosHelp(rt.Out)
    default:
        return fmt.Errorf("unknown subcommand %q (try: run, list, report)", args[0])
    }
}
```

**Flag 규약**:
- `run <scenario-name>`: 단일 시나리오 실행
- `run --all`: 전체 10개 순차 실행 (병렬 아님)
- `run --runs N`: 시나리오당 실행 횟수 override (default: scenario.Threshold.MaxRuns)
- `run --out <dir>`: output 디렉토리 override
- `run --config-enable`: `FaultInjection.Enabled=true` 를 런타임에서 강제 (테스트/smoke 용)
- `list`: 시나리오 이름 + 카테고리 + 설명 + threshold 표
- `report <run-id>`: Markdown report를 stdout에 출력
- `report latest`: 가장 최근 run

**Guard 호출**: `runChaosRun` 실행 전 반드시 `fault.CheckGuards(rt.cfg.FaultInjection)` 호출. 에러 시 즉시 return error.

**Dispatcher 등록**: `cmd/elnath/commands.go` 에 `"chaos": runChaos` 추가 (+1~2 line).

---

### 3.13 Guard 호출 위치 (2+ callsite 필수)

**`internal/daemon/runner.go`**: Runner 초기화 최상단:
```go
scenarioName, err := fault.CheckGuards(fault.GuardConfig{Enabled: cfg.FaultInjection.Enabled})
if err != nil {
    return fmt.Errorf("daemon runner: %w", err)
}
// scenarioName이 비어 있지 않으면 fault injector 세팅
```

**`cmd/elnath/cmd_daemon.go`**: daemon 실행 경로 최상단에서 동일한 `fault.CheckGuards` 호출. 에러 시 `os.Exit(1)` 또는 error return.

`grep -rn "fault.CheckGuards"` 결과에 **최소 2개 callsite** 가 있어야 verification gate 통과.

---

## 4. Tests Required

### 4.1 Unit tests

**`internal/fault/injector_test.go`**:
- `NoopInjector.Active()` → false; `ShouldFault()` → false; `InjectFault()` → nil
- `ScenarioInjector.ShouldFault()` — seed 고정, FaultRate=0.0 → 0/N, FaultRate=1.0 → N/N
- `InjectFault(FaultTransientError)` → error non-nil, 시나리오 이름 포함
- `InjectFault(FaultTimeout)` — context already cancelled → 즉시 반환
- `InjectFault(FaultMalformedJSON)` → `*MalformedJSONError` type assertion 성공
- `InjectFault(FaultHTTP429Burst)` → `*HTTP429Error` type assertion 성공
- **burst counter**: `FaultHTTP429Burst`, burstLimit=3 → 첫 3호출 fault, 4번째는 pass; `ResetForRun()` 후 다시 3회 fault

**`internal/fault/guard_test.go`**:
- env `ELNATH_FAULT_PROFILE=""`, Enabled=false → `("", nil)` (fast path)
- env set, Enabled=false → error 반환
- env set, Enabled=true → printDaemonWarning 호출 (stderr capture), `waitWithInterrupt` mock으로 즉시 통과 → scenarioName 반환

**`internal/fault/registry_test.go`**:
- `NewRegistry(scenarios.All())` → 10개 시나리오 로드
- `Get("tool-bash-transient-fail")` → 올바른 시나리오 반환
- `Get("nonexistent")` → `(nil, false)`
- 중복 이름 등록 → panic (defer/recover로 검증)

**`internal/fault/hook_tool_test.go`**:
- `injector.Active()==false` → inner registry 직접 호출
- `Active()==true`, `ShouldFault()==false` → inner 호출
- `Active()==true`, `ShouldFault()==true` → inner 호출 안 함, error 반환
- `TargetTool="bash"` 설정, 다른 tool 이름으로 호출 → inner 호출 (fault skip)

**`internal/fault/hook_llm_test.go`**:
- Compile-time assertion: `var _ llm.Provider = (*LLMFaultHook)(nil)`
- `ShouldFault()==true` → `Stream()` 에서 inner 호출 전 error 반환
- `ShouldFault()==false` → inner `Stream()` 위임 (mock provider 사용)

**`internal/fault/hook_ipc_test.go`**:
- `FaultSlowConn` — Write 소요 시간 ≥ scenario.FaultDuration (time.Since 측정)
- `FaultPacketDrop` — Write returns `len(b), nil`, inner Write 미호출 (mock conn)
- `injector.Active()==false` → inner net.Conn.Write 위임, 추가 latency 없음

**`internal/fault/reporter_test.go`**:
- `JSONLReporter.Record()` — 출력 JSON unmarshal 가능, 모든 필드 일치
- `MDReporter.Render()` — 전부 pass, 전부 fail, 혼합 RunRecord에 대해 Markdown 비어 있지 않음, "PASS"/"FAIL" 문자열 포함

### 4.2 Scenario / integration tests

**`internal/fault/scenarios/scenarios_test.go`**:
- `All()` 정확히 10개 반환
- 각 시나리오 `Threshold.MaxRuns > 0`, `Threshold.RecoveryRate > 0`
- 이름 슬러그 `[a-z0-9-]+` regexp 검증
- `FaultRate ∈ [0.0, 1.0]` 범위 검증
- `RecoveryRate > FaultRate * 0.5` (회복 가능성 검증)

**`internal/fault/e2e_test.go`** (`//go:build fault_e2e`):
- `tool-bash-transient-fail`: mock agent + ToolFaultHook, 20 runs, ≥ 19 pass
- 실행 시간 ≤ 120초 (mock 환경 기준)
- 일반 CI에서는 skip — `go test -tags fault_e2e` 로만 실행

### 4.3 Config test

**`internal/config/config_test.go`** (기존 파일 수정):
- `DefaultConfig()` → `FaultInjection.Enabled == false`
- YAML `fault_injection:\n  enabled: true\n  output_dir: /tmp/fault` → 정상 파싱

### 4.4 CLI test

**`cmd/elnath/cmd_chaos_test.go`** (신규):
- `runChaos([]string{})` → help 출력 (non-empty)
- `runChaos([]string{"list"})` → 10행 이상 출력
- `runChaos([]string{"unknown"})` → error
- `runChaos([]string{"run", "nonexistent-scenario"})` → error (scenario not found)

---

## 5. Verification Gates

구현 완료 후 모든 커맨드를 순서대로 실행하고 결과를 보고한다.

```bash
cd /Users/stello/elnath

# 1. Build & vet
go vet ./internal/fault/... ./cmd/elnath/cmd_chaos.go ./internal/daemon/... ./internal/agent/... ./internal/config/...
go build ./...

# 2. Unit + integration tests (race detector 필수)
go test -race ./internal/fault/... ./internal/daemon/... ./cmd/elnath/... ./internal/config/... ./internal/agent/...

# 3. Guard callsite 확인 (최소 2개)
grep -rn "fault.CheckGuards" /Users/stello/elnath/internal/daemon/ /Users/stello/elnath/cmd/elnath/cmd_daemon.go /Users/stello/elnath/cmd/elnath/cmd_chaos.go
# 기대: 2+ callsites

# 4. NoopInjector 기본값 확인
grep -r "NoopInjector" /Users/stello/elnath/internal/agent/ /Users/stello/elnath/internal/daemon/
# 기대: 기본 초기화 경로에 NoopInjector 사용

# 5. 디버그 코드 없음 확인
grep -rn "fmt\.Print\b\|log\.Print\b" /Users/stello/elnath/internal/fault/ | grep -v "_test.go"
# 기대: 없음 (slog만 허용)

# 6. 순환 import 없음 확인
go list -json ./internal/fault/... | grep -A5 '"Imports"'
# faulttype 패키지의 Imports에 internal/fault가 없어야 함
# scenarios 패키지의 Imports에 internal/fault가 없어야 함 (faulttype만 있어야)
```

전부 exit 0 + 기존 테스트 regression 0.

---

## 6. Smoke Test

구현 완료 후 다음 smoke test를 실행하고 출력을 보고한다:

```bash
# 전제: config에 fault_injection.enabled: true 설정
export ELNATH_FAULT_PROFILE=tool-bash-transient-fail

cd /Users/stello/elnath

# 1. 시나리오 목록 확인 (10행)
./elnath chaos list
# 기대: 10개 시나리오 표

# 2. 단일 시나리오 실행 (5회)
./elnath chaos run tool-bash-transient-fail --runs 5
# 기대: 5초 경고 후 실행, 결과 스트리밍, JSONL 기록

# 3. 리포트 출력
cat ~/.elnath/data/fault/<run-id>/report.md
# 또는:
./elnath chaos report latest
# 기대: Markdown 출력, "PASS" 또는 "FAIL" 포함, scenario 이름 포함
```

---

## 7. Commit Message Template

spec §8 그대로 사용:

```
feat: phase F-6 LB7 fault injection framework

카테고리별 fault 주입으로 production 코드 경로의 회복 능력 검증.
3중 가드 (env + config + daemon 5초 경고) 로 실수 활성화 방지.

- internal/fault: Scenario, Registry, Injector, NoopInjector,
  ToolFaultHook, LLMFaultHook, IPCFaultConn, JSONLReporter, MDReporter,
  guard (CheckGuards + daemon stderr warning)
- internal/fault/faulttype: leaf package, pure types (순환 import 방지)
- internal/fault/scenarios: 10 built-in scenarios
  (tool×3, llm×3, ipc×4) with per-scenario PASS thresholds
- internal/config: FaultInjectionConfig (enabled + output_dir)
- internal/agent: WithToolExecutor option + tools.Executor interface
  (minimal change, no behaviour change when fault inactive)
- internal/daemon: CheckGuards callsite + WithFaultInjector option
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

커밋은 하지 마라. stello가 직접 commit.

---

## 8. Self-Review Checklist

구현 완료 전 아래 항목을 순서대로 확인하고, 각 항목에 PASS/FAIL을 표시해서 보고한다.

- [ ] **burst counter**: `ScenarioInjector` 에 `burstCount atomic.Int64` + `burstLimit int` 필드 존재하고, `FaultHTTP429Burst` 에서만 counter 사용
- [ ] **ResetForRun()**: 메서드 존재하고 `burstCount.Store(0)` 호출
- [ ] **zero-overhead when disabled**: `Active() == false` 경로에서 allocation 없음. `ToolFaultHook.Execute`, `LLMFaultHook.Stream`, `IPCFaultConn.Write` 모두 첫 줄에서 `Active()` 체크 후 즉시 위임
- [ ] **recovery attempt 정의**: `reporter.go` 주석에 "top-level iteration 1회 = 1 attempt, streamWithRetry 내부 재시도 count 안 함" 명시
- [ ] **3 guard callsites**: `grep fault.CheckGuards` 결과에 daemon/runner.go, cmd_daemon.go, cmd_chaos.go 3곳 모두 존재 (최소 2곳)
- [ ] **순환 import 회피 증명**: `go list -json ./internal/fault/faulttype/...` 의 Imports에 `internal/fault` 없음; `go list -json ./internal/fault/scenarios/...` 의 Imports에 `internal/fault` 없고 `internal/fault/faulttype` 만 있음
- [ ] **시나리오 10개 전부 정의**: `scenarios_test.go` 에서 `len(All()) == 10` 확인
- [ ] **threshold 숫자 정확**: 위 table 의 RecoveryRate, MaxRuns, MaxRecoveryAttempts, FaultRate, FaultDuration, BurstLimit 값이 코드와 일치
- [ ] **IPC sleep cap**: `scenarios/builtin.go` 의 `All()` 내에 `FaultSlowConn`/`FaultBackpressure` FaultDuration ≤ 5s validator 존재
- [ ] **report JSONL + MD 둘 다 기록**: 각 run 후 `runs.jsonl` append + 최종 `report.md` 생성
- [ ] **시나리오 #8 baseline 주석**: `ipcSocketDrop()` 함수 or `All()` 내에 "baseline fail 가능 — daemon retransmit 없으면 예상된 동작" 주석 존재
- [ ] **`NewRegistry` 시그니처**: `NewRegistry(scenarios []*faulttype.Scenario)` — 내부에서 `scenarios.All()` 호출 안 함
- [ ] **path traversal 방어**: report output 디렉토리 생성 시 `run-id` 가 UUID v4 형식이고 경로 조작 문자 (`..`, `/`) 포함 여부 검증
- [ ] **daemon log "fault injection ACTIVE"**: daemon이 fault mode로 시작할 때 slog `WARN` level 이상으로 "fault injection ACTIVE" 메시지 기록
- [ ] **디버그 코드 없음**: `fmt.Println`, `fmt.Printf`, `log.Print` 가 `internal/fault/` production 코드에 없음 (slog만)
- [ ] **`--force-weak` 우회 없음**: 3중 가드를 한 번에 넘기는 플래그가 없음. `--config-enable` 은 테스트 smoke 용으로만 허용
- [ ] **go test -race 통과**: race detector 활성화 상태로 전체 테스트 통과

---

## 9. Scope Boundaries

### 이번 구현에 포함 (In scope)

- `internal/fault/faulttype/` (leaf)
- `internal/fault/` (scenario, registry, injector, guard, hook_tool, hook_llm, hook_ipc, reporter)
- `internal/fault/scenarios/builtin.go`
- `internal/config/config.go` FaultInjectionConfig 추가
- `internal/agent/agent.go` WithToolExecutor + tools.Executor (+15 LOC, minimal)
- `internal/daemon/runner.go` CheckGuards + WithFaultInjector
- `cmd/elnath/cmd_chaos.go`
- `cmd/elnath/commands.go` (+1~2 line)
- Unit + integration + e2e (build tag) 테스트

### 이번 구현에서 제외 (Defer — 건드리지 말 것)

1. **Filesystem fault 카테고리** — `os.Open`/`os.WriteFile` fault. Q8 defer.
2. **Network fault 카테고리** — TCP connection drop, DNS failure. Q8 defer.
3. **Time-skew fault 카테고리** — 시계 이상. Q8 defer.
4. **CI 자동 실행** — `make chaos-e2e` target. 별도 enhancement.
5. **Distributed fault** — 멀티 인스턴스. 단일 daemon만.
6. **Fault 이력 대시보드** — 웹 UI / Telegram 보고. CLI report만.
7. **Scenario 커스텀 YAML 로드** — builtin Go struct만. S6 defer.
8. **chaos 실행 중 daemon state 스냅샷** — 회복 여부만 측정.
9. **per-tool granularity 설정** — `TargetTool` 은 시나리오 struct 필드로만, YAML 동적 per-tool 설정 없음.

---

## 10. Production Safety Reminders

**이 항목은 절대 타협하지 않는다:**

1. **env 없으면 코드 미진입**: `fault.CheckGuards()` 첫 줄에서 `os.Getenv(envFaultProfile) == ""` 이면 즉시 return. 단 1회 os.Getenv, allocation 0.

2. **`--force-weak` 금지**: 3중 가드를 단번에 bypass하는 플래그를 만들지 않는다. guard를 강제로 통과시키는 코드 경로는 없다.

3. **daemon log 큼직한 경고**: daemon이 fault mode로 시작할 때 slog.Warn(`"fault injection ACTIVE"`, `"scenario"`, scenarioName) 을 daemon 시작 로그에 반드시 기록. daemon runner의 일반 info 로그와 시각적으로 구별되어야 한다.

4. **production config 기본값 false**: `DefaultConfig()` 에서 `FaultInjection.Enabled = false` 명시. YAML에서 `fault_injection:` 섹션 자체가 없을 때도 `Enabled == false` 보장.

5. **test 파일에만 guard bypass**: `guard_test.go` 에서만 `waitWithInterrupt` 를 mock으로 교체. production 코드에 test hook 남기지 않는다.

---

## 완료 보고 형식

작업 종료 시 다음 형식으로 보고:

1. 생성/수정 파일 목록 (절대 경로)
2. `go test -race ./internal/fault/... ./internal/daemon/... ./cmd/elnath/...` 결과 (신규 테스트 개수 포함)
3. `go vet` + `go build ./...` 결과
4. `grep -rn "fault.CheckGuards"` 결과 (2+ callsites 확인)
5. Self-review checklist 전체 PASS/FAIL 목록
6. Smoke test 출력 (`./elnath chaos list` 10행 이상, `./elnath chaos report latest` Markdown 포함)

커밋은 하지 마라. stello가 직접 commit.
