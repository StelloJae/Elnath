# Phase F-6 F7 — Onboarding & UX Accessibility (β)

**Predecessor:** Phase F-5 LLM Lesson Extraction DONE (`a18d026`)
**Status:** SPEC (decisions Q13-Q17 locked — `PHASE-F6-DECISIONS.md`)
**Scope:** ~400 LOC production + ~180 LOC tests
**Branch:** `feat/telegram-redesign`

---

## 0. Goal

5분 안에 첫 task. 친절한 에러 메시지. 자세한 help.

"β" 는 UX 친절성 (onboarding accessibility) 을 뜻한다. α 는 표준 접근성 (스크린 리더, 색상 contrast 등)으로 별도 phase.

**Why now**: Elnath 가 v0.4.0 에 도달해 기능 폭이 넓어졌으나 신규 사용자 진입 장벽 (provider 설정, 에러 해석, help 빈약) 이 채워지지 않았다. F-5 까지 내부 품질 (lesson extraction, provider refresh) 에 집중했고, F7 은 그 결과물이 외부에 노출되는 UX 를 정비한다.

**Why this scope**: Q13=A (setup 확장, 새 명령 불필요), Q14=B (local-only metric), Q15=B+C일부 (Top-N 에러 + ELN-XXX 코드), Q16=A (man-page style help), Q17=B (setup 끝 demo). 넷 모두 기존 코드를 소폭 확장하는 경로 — 신규 패키지 1개 (`userfacingerr`), 신규 파일 2-3개, 기존 파일 4-5개 수정.

---

## 1. Decisions (F-6 Q13-Q17 확정)

| ID | Question | Answer | Rationale |
|----|----------|--------|-----------|
| Q13 | 첫 사용자 path | **A** — `elnath setup --quickstart` minimal mode | 기존 setup 확장. 새 명령 표면 0 추가. WelcomeModel 의 `PathQuick` 경로와 자연 연동. |
| Q14 | 5분 metric | **B** — Local-only `~/.elnath/onboarding_metric.json` | 외부 전송 0. privacy 우선. 디버그 협조 시 사용자 자발 공유 가능. |
| Q15 | Error 친절화 | **B+C 일부** — Top-N 개선 + ELN-XXX 코드 | 완전 표준화 리팩터 비용 회피. ROI 높은 10-15 경로만. catalog 확장 여지. |
| Q16 | Help 시스템 | **A** — man-page style 강화 | 핵심 명령 5개 (`run`, `setup`, `wiki`, `lessons`, `daemon`) 에 25-50 라인 help + 예제 3-5개 + See also. |
| Q17 | 첫 example | **B** — Setup 끝 demo 1개 | setup → demo 자연 flow. 5분 metric 마지막 step. 별도 명령 학습 불요. |

---

## 2. Architecture

```
cmd/elnath/cmd_setup.go
│
├─ --quickstart 플래그 감지
│   └─ onboarding.RunQuickstart(cfgPath, version) 호출
│       ├─ Codex OAuth auto-detect → provider 선택 skip
│       ├─ APIKey step (Anthropic 또는 skip)
│       ├─ 기본값 채움: DataDir/WikiDir/PermissionMode (PathQuick 경로 재사용)
│       ├─ SmokeTest (API key 있을 때만)
│       └─ Demo prompt [Y/n]
│           └─ onboarding.RunDemoTask(ctx, provider, model) → "what is 2+2?" → print result
│
├─ metric writer (Q14)
│   internal/onboarding/metric.go
│   WriteMetric(MetricRecord) → ~/.elnath/onboarding_metric.json
│   MetricRecord { SetupStartedAt, SetupCompletedAt, DurationSec, Steps }
│
└─ 일반 setup (플래그 없음): 기존 동작 그대로

internal/userfacingerr/ (신규 패키지, Q15)
│
├─ codes.go      ELN-XXX 상수 + Code type
├─ catalog.go    []CatalogEntry{Code, Title, What, Why, HowToFix}
├─ wrap.go       Wrap(code, err, context) → *UserFacingError
│                Format() → "ELN-XXX: <title>\n<what>\nHint: <how_to_fix>"
└─ (기존 user-facing 에러 경로 10-15 곳에서 Wrap 호출)

cmd/elnath/cmd_errors.go (신규, Q15 catalog 조회)
│
└─ "elnath errors <code>" → CatalogEntry 상세 출력
   "elnath errors list"   → 전체 목록

Help 강화 (Q16)
└─ internal/onboarding/i18n.go
    "cli.help"                   → 전체 help 확장 (현재 ~20줄 → ~50줄)
    "cmd.run.help"    (신규)     → run 명령 상세 25-50줄
    "cmd.setup.help"  (신규)     → setup 명령 상세
    "cmd.wiki.help"   (신규)     → wiki 명령 상세
    "cmd.lessons.help"(신규)     → lessons 명령 상세
    "cmd.daemon.help" (신규)     → daemon 명령 상세
    cmd/elnath/commands.go 에서 --help / -h 플래그 라우팅
```

### Quickstart flow (Q13 세부)

```
elnath setup --quickstart
│
[1] Codex OAuth detect (CodexOAuthAvailable())
│   Yes → provider = codex, API key step skip
│   No  → API key step (짧은 single-prompt, no TUI)
│
[2] 기본값 채움 (silent)
│   DataDir = ~/.elnath/data
│   WikiDir = ~/.elnath/wiki
│   PermissionMode = "default"
│
[3] 설정 저장 (config.WriteFromResult)
│
[4] SmokeTest (API key 있으면 1회 ping)
│   성공 → "Elnath is ready!"
│   실패 → "Connection failed — check your key. Config saved."
│
[5] metric 기록 (onboarding_metric.json)
│
[6] Demo [Y/n]
    Y → RunDemoTask: "what is 2+2?" → agent 1회 호출 → 결과 출력
    N → "Run 'elnath run' to start."
```

---

## 3. Implementation

### 3.1 `cmd/elnath/cmd_setup.go` 확장 (MODIFY, ~+40 LOC)

`--quickstart` 플래그 감지 로직 추가. 기존 `cmdSetup` 함수 분기:

```go
func cmdSetup(ctx context.Context, args []string) error {
    cfgPath := extractConfigFlag(os.Args)
    if cfgPath == "" {
        cfgPath = config.DefaultConfigPath()
    }

    // NEW: quickstart mode
    if hasFlag(os.Args, "--quickstart") {
        return cmdSetupQuickstart(ctx, cfgPath)
    }

    // existing full setup wizard (unchanged)
    // ...
}

func cmdSetupQuickstart(ctx context.Context, cfgPath string) error {
    started := time.Now()

    result, err := onboarding.RunQuickstart(cfgPath, version)
    if err != nil {
        return userfacingerr.Wrap(userfacingerr.ELN001, err, "setup quickstart")
    }

    cfgResult := onboardingResultToConfig(result)
    if err := config.WriteFromResult(cfgPath, cfgResult); err != nil {
        return userfacingerr.Wrap(userfacingerr.ELN060, err, "write config after quickstart")
    }

    // Write onboarding metric (Q14).
    metric := onboarding.MetricRecord{
        SetupStartedAt:    started,
        SetupCompletedAt:  time.Now(),
        Steps: onboarding.MetricSteps{
            Provider:  result.ProviderDetected,
            APIKey:    result.APIKey != "",
            SmokeTest: result.SmokeTestPassed,
            DemoTask:  false, // updated after demo
        },
    }

    // Demo task (Q17).
    // provider and model are built here (in cmd layer) and injected into RunDemoTask
    // to keep onboarding → cmd/elnath dependency direction clean.
    demoRan := false
    if promptYN("Try a demo task? [Y/n] ", true) {
        provider, model, provErr := buildProvider(cfgResult)
        if provErr != nil {
            fmt.Fprintf(os.Stderr, "Demo skipped (no provider): %v\n", provErr)
        } else if err := onboarding.RunDemoTask(ctx, provider, model); err != nil {
            fmt.Fprintf(os.Stderr, "Demo task failed (that's ok): %v\n", err)
        } else {
            demoRan = true
        }
    }
    metric.Steps.DemoTask = demoRan
    metric.SetupCompletedAt = time.Now()
    metric.DurationSec = int(metric.SetupCompletedAt.Sub(metric.SetupStartedAt).Seconds())

    _ = onboarding.WriteMetric(metric) // best-effort, non-fatal; writes to config.DefaultDataDir()

    if !demoRan {
        fmt.Println("\nSetup complete. Run 'elnath run' to start.")
    }
    return nil
}
```

`promptYN(prompt string, defaultYes bool) bool` 헬퍼: TTY 에서 `[Y/n]` (defaultYes=true) 또는 `[y/N]` (defaultYes=false) 프롬프트. non-TTY 에서는 default 반환.

### 3.2 `internal/onboarding/quickstart.go` (NEW, ~70 LOC)

`onboarding.RunQuickstart` 구현. 기존 TUI `Run()` 와 달리 non-TUI (plain fmt 출력 + bufio.Scanner) 경로. 이유: `--quickstart` 의 목적이 "단계 최소화" 이므로 Bubbletea alt-screen 은 오버헤드. 기존 `PathQuick` 로직 (APIKey → 기본값) 을 재사용해 단순화.

> 기존 `llm.CodexOAuthAvailable` 재사용 — import cycle 없음 (`onboarding → llm` 단방향)

```go
package onboarding

import (
    "github.com/stello/elnath/internal/llm"
    // ... other imports
)

// QuickstartResult extends Result with quickstart-specific metadata.
type QuickstartResult struct {
    Result
    ProviderDetected string // "codex" | "anthropic" | ""
    SmokeTestPassed  bool
}

// RunQuickstart executes the minimal onboarding path non-interactively (no TUI).
// Provider auto-detection skips the API key prompt when Codex OAuth is available.
func RunQuickstart(cfgPath, version string) (*QuickstartResult, error) {
    res := &QuickstartResult{}

    // Step 1: Provider detection.
    if llm.CodexOAuthAvailable() {
        res.ProviderDetected = "codex"
        fmt.Println("Codex OAuth detected — skipping API key setup.")
    } else {
        fmt.Print("Enter your Anthropic API key (press Enter to skip): ")
        key := readLineOrEnv("ELNATH_ANTHROPIC_API_KEY")
        res.APIKey = strings.TrimSpace(key)
        if res.APIKey != "" {
            res.ProviderDetected = "anthropic"
        }
    }

    // Step 2: Apply defaults (mirrors PathQuick in model.go afterAPIKey).
    home, _ := os.UserHomeDir()
    base := filepath.Join(home, ".elnath")
    res.DataDir = filepath.Join(base, "data")
    res.WikiDir = filepath.Join(base, "wiki")
    res.PermissionMode = "default"
    res.Locale = En

    // Step 3: Smoke test.
    if res.APIKey != "" {
        vr := ValidateAnthropicKey(context.Background(), res.APIKey)
        res.SmokeTestPassed = vr.Valid
        if vr.Valid {
            fmt.Println("Connection test passed.")
        } else {
            fmt.Printf("Connection test failed (%v) — config saved anyway.\n", vr.Error)
        }
    }

    return res, nil
}
```

`readLineOrEnv(envKey string) string` : 환경변수 있으면 반환, 없으면 `bufio.NewReader(os.Stdin).ReadString('\n')`.

### 3.3 `internal/onboarding/metric.go` (NEW, ~50 LOC)

```go
package onboarding

import (
    "encoding/json"
    "os"
    "path/filepath"
    "time"
)

// MetricRecord captures the onboarding timeline for local diagnostics.
// Written once to ~/.elnath/onboarding_metric.json. Never transmitted externally.
type MetricRecord struct {
    SetupStartedAt   time.Time    `json:"setup_started_at"`
    SetupCompletedAt time.Time    `json:"setup_completed_at"`
    DurationSec      int          `json:"duration_sec"`
    Steps            MetricSteps  `json:"steps"`
}

type MetricSteps struct {
    Provider  string `json:"provider"`   // "codex" | "anthropic" | ""
    APIKey    bool   `json:"api_key"`
    SmokeTest bool   `json:"smoke_test"`
    DemoTask  bool   `json:"demo_task"`
}

// WriteMetric persists the record to config.DefaultDataDir()/onboarding_metric.json.
// Overwrites any previous file — onboarding is a one-time event.
// Failure is non-fatal: caller should log and continue.
func WriteMetric(rec MetricRecord) error {
    dir := config.DefaultDataDir()
    if err := os.MkdirAll(dir, 0o700); err != nil {
        return fmt.Errorf("onboarding metric: mkdir: %w", err)
    }
    data, err := json.MarshalIndent(rec, "", "  ")
    if err != nil {
        return fmt.Errorf("onboarding metric: marshal: %w", err)
    }
    path := filepath.Join(dir, "onboarding_metric.json")
    return os.WriteFile(path, data, 0o600)
}
```

파일 위치: `config.DefaultDataDir()` 기반 (`~/.elnath/data/onboarding_metric.json`). `cfgPath` 디렉터리 사용 금지 — config 이 프로젝트별 경로에 있어도 metric 은 항상 사용자 홈 기준 data dir 에 기록.

### 3.4 `internal/onboarding/demo.go` (NEW, ~40 LOC)

`cmd/elnath/cmd_setup.go` 의 `--quickstart` 분기에서 `buildProvider(cfg)` 로 provider 를 생성한 뒤 `RunDemoTask(ctx, provider, model)` 를 호출한다. provider 주입 방식으로 `onboarding → cmd/elnath` 역의존 없이 순환 import 회피.

```go
package onboarding

import (
    "github.com/stello/elnath/internal/llm"
    // ... other imports
)

// RunDemoTask submits a minimal "what is 2+2?" task via the given provider
// and streams the response to stdout. Uses a single-turn non-interactive agent.
// The task bypasses the full cmdRun setup (no DB, no wiki, no tools) to stay
// lightweight and fast.
func RunDemoTask(ctx context.Context, provider llm.Provider, model string) error {
    fmt.Printf("\nDemo: asking %q — \"what is 2+2?\"\n\n", model)

    req := llm.Request{
        Model: model,
        Messages: []llm.Message{
            {Role: "user", Content: []llm.ContentBlock{
                {Type: "text", Text: "what is 2+2?"},
            }},
        },
        MaxTokens: 64,
    }

    return provider.Stream(ctx, req, func(ev llm.StreamEvent) {
        if ev.Type == llm.EventText {
            fmt.Print(ev.Text)
        }
    })
}
```

### 3.5 `internal/userfacingerr/` (NEW 패키지)

#### `internal/userfacingerr/codes.go` (~40 LOC)

```go
package userfacingerr

// Code is a stable ELN-XXX error identifier. Codes are never reused after assignment.
type Code string

const (
    ELN001 Code = "ELN-001" // provider not configured
    ELN002 Code = "ELN-002" // OAuth token expired
    ELN010 Code = "ELN-010" // wiki not initialized
    ELN020 Code = "ELN-020" // permission denied (path guard)
    ELN030 Code = "ELN-030" // daemon socket unreachable
    ELN040 Code = "ELN-040" // LLM timeout
    ELN050 Code = "ELN-050" // tool execution failed
    ELN060 Code = "ELN-060" // config invalid
    ELN070 Code = "ELN-070" // session file corrupted
    ELN080 Code = "ELN-080" // rate limited (429)
    ELN090 Code = "ELN-090" // OAuth token missing / absent
    ELN100 Code = "ELN-100" // wiki page not found
    ELN110 Code = "ELN-110" // daemon task timeout
    ELN120 Code = "ELN-120" // no LLM response (empty stream)
)
```

#### `internal/userfacingerr/catalog.go` (~80 LOC)

```go
package userfacingerr

// CatalogEntry describes an error code for display via "elnath errors <code>".
type CatalogEntry struct {
    Code     Code
    Title    string
    What     string // one sentence: what happened
    Why      string // one sentence: common root cause
    HowToFix string // actionable step(s)
}

var catalog = []CatalogEntry{
    {
        Code:     ELN001,
        Title:    "Provider not configured",
        What:     "Elnath could not find an LLM provider (Anthropic API key or Codex OAuth).",
        Why:      "No API key is set in config.yaml and no Codex OAuth token was found.",
        HowToFix: "Run 'elnath setup --quickstart' or set ELNATH_ANTHROPIC_API_KEY.",
    },
    {
        Code:     ELN002,
        Title:    "OAuth token expired",
        What:     "The Codex OAuth access token has expired and automatic refresh failed.",
        Why:      "The refresh token may have been revoked or the network is unavailable.",
        HowToFix: "Re-authenticate with 'codex auth' and retry.",
    },
    {
        Code:     ELN010,
        Title:    "Wiki not initialized",
        What:     "The wiki directory does not exist or has not been initialised.",
        Why:      "wiki_dir in config.yaml points to a non-existent path, or setup was skipped.",
        HowToFix: "Run 'elnath setup' and confirm the wiki directory, or create it manually.",
    },
    {
        Code:     ELN020,
        Title:    "Permission denied",
        What:     "Elnath's path guard blocked access to the requested file or directory.",
        Why:      "The target path is outside the allowed working directories.",
        HowToFix: "Check permission.allow in config.yaml or move the file inside the project root.",
    },
    {
        Code:     ELN030,
        Title:    "Daemon socket unreachable",
        What:     "The CLI could not connect to the Elnath daemon socket.",
        Why:      "The daemon is not running, or the socket_path in config.yaml is stale.",
        HowToFix: "Run 'elnath daemon start' or check 'elnath daemon status'.",
    },
    {
        Code:     ELN040,
        Title:    "LLM request timeout",
        What:     "The LLM provider did not respond within the configured timeout.",
        Why:      "High load on the provider, large prompt, or network latency.",
        HowToFix: "Retry. Increase anthropic.timeout_seconds in config.yaml if recurring.",
    },
    {
        Code:     ELN050,
        Title:    "Tool execution failed",
        What:     "A tool (bash, write, edit, etc.) returned a non-zero exit or error.",
        Why:      "The command failed, a file was locked, or the path was invalid.",
        HowToFix: "Check the error detail above. Re-run with a corrected command or path.",
    },
    {
        Code:     ELN060,
        Title:    "Config invalid",
        What:     "Elnath could not parse or validate config.yaml.",
        Why:      "A required field is missing, has an unexpected type, or the YAML is malformed.",
        HowToFix: "Run 'elnath setup' to regenerate config, or edit ~/.elnath/config.yaml manually.",
    },
    {
        Code:     ELN070,
        Title:    "Session file corrupted", // future — no emission path yet
        What:     "A session JSONL file could not be parsed.",
        Why:      "The file was truncated (e.g. disk full) or written by an incompatible version.",
        HowToFix: "Delete the corrupted file from ~/.elnath/data/sessions/ and start a new session.",
    },
    {
        Code:     ELN080,
        Title:    "Rate limited (429)",
        What:     "The LLM provider rejected the request due to rate limiting.",
        Why:      "Too many requests in a short window, or quota exhausted.",
        HowToFix: "Wait a moment and retry. Check provider dashboard for quota status.",
    },
    {
        Code:     ELN090,
        Title:    "OAuth token missing / absent",
        What:     "The OAuth access token is missing or absent from the auth file.",
        Why:      "Authentication was not completed or the auth file was removed.",
        HowToFix: "Run 'elnath setup' to re-authenticate with Codex / Anthropic.",
    },
    {
        Code:     ELN100,
        Title:    "Wiki page not found",
        What:     "The requested wiki page does not exist.",
        Why:      "The path is incorrect, the page was deleted, or the wiki dir was changed.",
        HowToFix: "Run 'elnath wiki search <term>' to find the correct path.",
    },
    {
        Code:     ELN110,
        Title:    "Daemon task timeout",
        What:     "A task submitted to the daemon exceeded its execution time limit.",
        Why:      "The task is too large, the LLM is slow, or the daemon is overloaded.",
        HowToFix: "Increase daemon.task_timeout_seconds in config.yaml, or split the task.",
    },
    {
        Code:     ELN120,
        Title:    "Empty LLM response", // future — defined but not yet emitted
        What:     "The LLM stream completed without producing any text content.",
        Why:      "The model refused the prompt, or a content filter triggered.",
        HowToFix: "Retry with a rephrased prompt. Check for content policy restrictions.",
    },
}

// Lookup returns the CatalogEntry for a given code, or false if not found.
func Lookup(code Code) (CatalogEntry, bool) {
    for _, e := range catalog {
        if e.Code == code {
            return e, true
        }
    }
    return CatalogEntry{}, false
}

// All returns a copy of the full catalog.
func All() []CatalogEntry {
    return append([]CatalogEntry(nil), catalog...)
}
```

#### `internal/userfacingerr/wrap.go` (~40 LOC)

```go
package userfacingerr

import (
    "errors"
    "fmt"
)

// UserFacingError is an error that carries a stable ELN-XXX code and a
// user-readable hint. It wraps the original error for %w unwrapping.
type UserFacingError struct {
    code    Code
    context string
    wrapped error
}

// Wrap creates a UserFacingError. context is a short (≤30 char) caller label,
// e.g., "load config" or "daemon connect". err may be nil.
func Wrap(code Code, err error, context string) *UserFacingError {
    return &UserFacingError{code: code, context: context, wrapped: err}
}

func (e *UserFacingError) Error() string {
    entry, ok := Lookup(e.code)
    if !ok {
        if e.wrapped != nil {
            return fmt.Sprintf("%s: %s: %v", e.code, e.context, e.wrapped)
        }
        return fmt.Sprintf("%s: %s", e.code, e.context)
    }
    if e.wrapped != nil {
        return fmt.Sprintf("%s %s: %v\nHint: %s", e.code, entry.Title, e.wrapped, entry.HowToFix)
    }
    return fmt.Sprintf("%s %s\nHint: %s", e.code, entry.Title, entry.HowToFix)
}

func (e *UserFacingError) Unwrap() error { return e.wrapped }

// Is reports whether target is the same error code.
func (e *UserFacingError) Is(target error) bool {
    var t *UserFacingError
    if errors.As(target, &t) {
        return e.code == t.code
    }
    return false
}

// Code returns the stable ELN-XXX identifier.
func (e *UserFacingError) Code() Code { return e.code }
```

### 3.6 `cmd/elnath/cmd_errors.go` (NEW, ~50 LOC)

```go
package main

import (
    "context"
    "fmt"
    "strings"

    "github.com/stello/elnath/internal/userfacingerr"
)

func cmdErrors(_ context.Context, args []string) error {
    if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
        return printErrorsHelp()
    }
    switch args[0] {
    case "list":
        return cmdErrorsList()
    default:
        return cmdErrorsLookup(args[0])
    }
}

func cmdErrorsList() error {
    fmt.Println("Elnath Error Catalog")
    fmt.Println(strings.Repeat("─", 50))
    for _, e := range userfacingerr.All() {
        fmt.Printf("  %-10s  %s\n", e.Code, e.Title)
    }
    fmt.Println()
    fmt.Println("Run 'elnath errors <code>' for details.")
    return nil
}

func cmdErrorsLookup(raw string) error {
    // Accept both "ELN-001" and "001" for convenience.
    code := userfacingerr.Code(strings.ToUpper(raw))
    if !strings.HasPrefix(string(code), "ELN-") {
        code = userfacingerr.Code("ELN-" + string(code))
    }
    entry, ok := userfacingerr.Lookup(code)
    if !ok {
        return fmt.Errorf("unknown error code %q — run 'elnath errors list'", raw)
    }
    fmt.Printf("\n%s — %s\n\n", entry.Code, entry.Title)
    fmt.Printf("What:     %s\n", entry.What)
    fmt.Printf("Why:      %s\n", entry.Why)
    fmt.Printf("Fix:      %s\n\n", entry.HowToFix)
    return nil
}

func printErrorsHelp() error {
    fmt.Print(`Usage: elnath errors <code|list>

Look up an Elnath error code for details and suggested fixes.

Commands:
  list         List all known error codes with short titles
  <code>       Show full details for the given code

Arguments:
  code         The ELN-XXX code shown in an error message. You may
               omit the "ELN-" prefix (e.g. "elnath errors 001").

Examples:
  elnath errors list
  elnath errors ELN-001
  elnath errors 030

See also: elnath setup --quickstart, elnath daemon start
`)
    return nil
}
```

`commands.go` 에 `"errors": cmdErrors` 추가.

### 3.7 Help 강화 — `internal/onboarding/i18n.go` 확장 (MODIFY, ~+120 LOC)

현재 `cli.help` 키 (~20줄) 를 ~50줄로 확장하고, 명령별 상세 help 키 5개 추가.

**확장할 i18n 키 (En 로케일, Ko 로케일도 동일 구조)**:

| Key | 목적 |
|-----|------|
| `cmd.run.help` | run 상세 (25-40줄, 예제 4개) |
| `cmd.setup.help` | setup 상세 (25줄, 예제 3개) |
| `cmd.wiki.help` | wiki 상세 (30줄, 예제 4개) |
| `cmd.lessons.help` | lessons 상세 (25줄, 예제 3개) |
| `cmd.daemon.help` | daemon 상세 (35줄, 예제 5개) |

각 help 텍스트 구조 (man-page style):

```
USAGE
  elnath <cmd> [flags]

DESCRIPTION
  <2-4줄 설명>

FLAGS
  --flag    설명

EXAMPLES
  # 예제 설명 1
  $ elnath <cmd> ...

  # 예제 설명 2
  $ elnath <cmd> ...

SEE ALSO
  elnath help, elnath errors list
```

**`cmd.run.help` 예시**:

```
USAGE
  elnath run [flags]

DESCRIPTION
  Start an interactive chat session with your configured LLM provider.
  Elnath maintains a persistent message history and can use tools (bash,
  file read/write, wiki, web fetch) to complete tasks autonomously.

  If no config exists, setup runs automatically on first launch.

FLAGS
  --non-interactive    Skip TUI onboarding; use env vars and defaults
  --principal <id>     Override the principal identity for this session
  --project-id <id>    Tag this session with a project identifier
  --config <path>      Use an alternative config file

EXAMPLES
  # Start interactive chat
  $ elnath run

  # Non-interactive (CI / scripted)
  $ ELNATH_ANTHROPIC_API_KEY=sk-ant-... elnath run --non-interactive

  # Use a project-scoped config
  $ elnath run --config ~/projects/myapp/.elnath/config.yaml

  # Run with explicit principal
  $ elnath run --principal alice

SEE ALSO
  elnath setup, elnath daemon, elnath wiki
```

**`cmd.setup.help` 예시**:

```
USAGE
  elnath setup [--quickstart]

DESCRIPTION
  Launch the interactive setup wizard to configure your LLM provider,
  data directories, permission mode, and MCP servers. Running setup
  again reconfigures an existing installation (current values shown
  as defaults). Your existing config is backed up before overwriting.

FLAGS
  --quickstart    Minimal fast path: auto-detects Codex OAuth, applies
                  defaults, and runs a demo task. No TUI. (~1 min)

EXAMPLES
  # Full interactive wizard
  $ elnath setup

  # Minimal 1-minute path (recommended for first-time users)
  $ elnath setup --quickstart

  # Reconfigure with a specific config file
  $ elnath setup --config ~/projects/myapp/.elnath/config.yaml

SEE ALSO
  elnath run, elnath errors list
```

### 3.8 `cmd/elnath/commands.go` 에서 `--help` 라우팅 (MODIFY, ~+20 LOC)

현재 `cmdHelp` 는 `cli.help` 키 하나를 출력. 명령별 `--help` / `-h` 플래그를 각 commandRunner 에서 처리하도록 수정:

```go
func executeCommand(ctx context.Context, name string, args []string) error {
    // NEW: per-command help before dispatch
    if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
        return printCommandHelp(name)
    }
    // existing dispatch ...
}

func printCommandHelp(name string) error {
    locale := loadLocale()
    key := "cmd." + name + ".help"
    text := onboarding.TOptional(locale, key) // returns "" if key missing
    if text == "" {
        // fallback to global help
        return cmdHelp(nil, nil)
    }
    fmt.Println(text)
    return nil
}
```

`onboarding.TOptional(locale, key) string` 신규 헬퍼: 키 없으면 빈 문자열 반환 (현재 `T` 는 키 없으면 패닉하지 않고 key 자체 반환 — 구현 확인 필요).

---

## 4. Error Catalog (ELN-XXX)

전체 14개 코드. §3.5 의 `catalog.go` 에 구현됨.

| Code | Title | 주요 발생 경로 |
|------|-------|------------|
| ELN-001 | Provider not configured | `buildProvider()` → `"no LLM provider configured"` |
| ELN-002 | OAuth token expired | `codex_oauth.go:155` → `"codex: refresh failed (re-run codex auth)"` |
| ELN-010 | Wiki not initialized | `wiki/store.go:21` → `"wiki store: create dir"` / wiki 명령 진입 시 |
| ELN-020 | Permission denied | `tools/pathguard.go:64` → `"write denied: ... is under protected path"` |
| ELN-030 | Daemon socket unreachable | `daemon/daemon.go:141` → `"daemon: listen"` / 클라이언트 연결 실패 |
| ELN-040 | LLM timeout | `anthropic.go:109` → http timeout, `codex_oauth.go:181` |
| ELN-050 | Tool execution failed | bash tool exit ≠ 0, write tool OS error |
| ELN-060 | Config invalid | `config.Load()` YAML parse error, missing required field |
| ELN-070 | Session file corrupted | JSONL parse error on session load *(future — no emission path yet)* |
| ELN-080 | Rate limited (429) | `anthropic.go:120` → `"anthropic: rate limit (429)"` |
| ELN-090 | OAuth token missing / absent | `codex_oauth.go:522` → `"codex: no access token in auth file"` |
| ELN-100 | Wiki page not found | `wiki/store.go:79` → `"wiki store: page not found"` |
| ELN-110 | Daemon task timeout | `daemon/daemon.go:529` → `"daemon: task timed out"` |
| ELN-120 | Empty LLM response | stream 완료 후 content block 0개 *(future — defined but not yet emitted)* |

**Top-N user-facing 경로에 Wrap 주입 (Q15 B 부분)**:

실제로 TTY 에 도달하는 에러 경로 중 사용자 혼란이 높은 8개 (ELN-070/ELN-120 은 현재 emission path 없어 제외):

1. `commands.go` 의 `"no LLM provider configured"` → `userfacingerr.Wrap(ELN001, ...)`
2. `cmd_run.go` 의 `"load config: ..."` → `userfacingerr.Wrap(ELN060, ...)`
3. `codex_oauth.go` 의 `"codex: refresh failed (re-run codex auth)"` → `userfacingerr.Wrap(ELN002, ...)`
4. `anthropic.go` 의 `"anthropic: rate limit (429)"` → `userfacingerr.Wrap(ELN080, ...)`
5. `daemon/daemon.go` 의 `"daemon: task timed out"` → `userfacingerr.Wrap(ELN110, ...)`
6. `cmd_wiki.go` 에서 wiki 디렉터리 미초기화 에러 → `userfacingerr.Wrap(ELN010, ...)`
7. `tools/pathguard.go` 의 `"write denied"` → `userfacingerr.Wrap(ELN020, ...)`
8. `cmd_run.go` 의 daemon 연결 실패 경로 → `userfacingerr.Wrap(ELN030, ...)`
9. `anthropic.go` HTTP timeout → `userfacingerr.Wrap(ELN040, ...)`
10. `wiki/store.go` page not found → `userfacingerr.Wrap(ELN100, ...)`

> ELN-070 ("Session file corrupted") 및 ELN-120 ("Empty LLM response") 는 현재 wrap 할 callsite 없음. catalog 에 등록만 하고 향후 emission path 추가 시 Wrap 적용.

각 wrap 지점에서 기존 `fmt.Errorf("...")` 를 `userfacingerr.Wrap(ELNXXX, err, context)` 로 교체. 에러가 최종적으로 `main.go` 또는 `executeCommand` 의 `fmt.Fprintln(os.Stderr, err)` 에 도달할 때 `UserFacingError.Error()` 의 hint 라인이 함께 출력됨.

---

## 5. Tests

### 5.1 Unit tests

**`internal/onboarding/metric_test.go`** (~30 LOC):
- `WriteMetric` → 파일 생성, JSON round-trip (필드 전부 검증).
- `WriteMetric` 두 번 호출 → 파일 덮어쓰기 (append 아님).
- `cfgPath` 의 디렉터리가 없을 때 → `os.MkdirAll` 로 생성.

**`internal/onboarding/quickstart_test.go`** (~40 LOC):
- `llm.CodexOAuthAvailable()` : 유효한 `auth.json` → true; 파일 없음 → false; `auth_mode != "chatgpt"` → false. (테스트는 `internal/llm` 패키지에 위치)
- `RunQuickstart` 는 TTY 인터랙션 테스트 불필요 (integration test 로 위임). `llm.CodexOAuthAvailable` 의 unit test 는 `internal/llm` 패키지에서 이미 커버 가정.

**`internal/userfacingerr/wrap_test.go`** (~50 LOC):
- `Wrap(ELN001, err, "test")` → `.Error()` 에 `"ELN-001"` 포함.
- `Wrap(ELN001, err, "test")` → `.Error()` 에 `HowToFix` 포함.
- `errors.As` 로 `*UserFacingError` unwrap 가능.
- `Wrap(ELN001, nil, "test")` → nil wrapped err 시 panic 없음.
- unknown code → fallback format (code + context).

**`internal/userfacingerr/catalog_test.go`** (~30 LOC):
- `Lookup(ELN001)` → 찾음, 모든 필드 비어있지 않음.
- `Lookup("ELN-999")` → not found.
- `All()` → len == 14 (catalog 개수).
- 각 entry 의 Code, Title, What, Why, HowToFix 비어있지 않음 (table test).

**`cmd/elnath/cmd_errors_test.go`** (~30 LOC):
- `cmdErrors(ctx, []string{"list"})` → 에러 없음, stdout 에 "ELN-001" 포함.
- `cmdErrors(ctx, []string{"ELN-001"})` → 에러 없음, stdout 에 "Provider not configured" 포함.
- `cmdErrors(ctx, []string{"001"})` → 코드 prefix 자동 보완.
- `cmdErrors(ctx, []string{"ELN-999"})` → 에러 반환.

### 5.2 Setup flow golden test

**`internal/onboarding/quickstart_integration_test.go`** (~30 LOC, `//go:build integration` tag):
- 임시 디렉터리에 가짜 `~/.codex/auth.json` (유효 OAuth) 세팅.
- `RunQuickstart` 실행 → `ProviderDetected == "codex"`, `APIKey == ""`.
- `WriteMetric` 호출 → 파일 내용 검증.

### 5.3 Error wrapping regression test

**`internal/userfacingerr/regression_test.go`** (~20 LOC):
- 실제 발생 가능한 에러 (`fmt.Errorf("no LLM provider configured: ...")`) 를 wrap 하고 `UserFacingError.Error()` 가 원본 메시지를 잃지 않는지 확인 (`errors.Unwrap` 체인).

---

## 6. Scope Boundaries

**In scope (이 spec)**:
- `cmd/elnath/cmd_setup.go` — `--quickstart` 플래그 + `cmdSetupQuickstart`
- `internal/onboarding/quickstart.go` — `RunQuickstart` (uses `llm.CodexOAuthAvailable`)
- `internal/onboarding/metric.go` — `MetricRecord`, `WriteMetric`
- `internal/onboarding/demo.go` — `RunDemoTask`
- `internal/userfacingerr/` — 신규 패키지 (codes, catalog, wrap)
- `cmd/elnath/cmd_errors.go` — `elnath errors <code|list>`
- `cmd/elnath/commands.go` — `"errors"` 등록, `--help` 라우팅
- `internal/onboarding/i18n.go` — 5개 명령 상세 help 키 추가
- Unit + integration tests

**Out of scope — defer**:
- **In-CLI tutorial** (`elnath tutorial`) — Q16=A 로 defer. help 강화로 우선.
- **Web docs** (`elnath docs` → 브라우저) — 외부 hosting 불필요. defer.
- **Opt-in telemetry** — Q14=B 로 local-only. 서버 인프라 없음.
- **모든 `fmt.Errorf` 표준 wrapping** — Q15=B, top-N 10개만. 전체 300+ 경로 는 별도 phase.
- **Examples registry** (`elnath examples list/run`) — Q17=B 로 defer. setup demo 1개.
- **표준 a11y** (스크린 리더, WCAG colour contrast) — F7 는 β (UX). α 는 별도 phase.
- **LB6 portability, LB7 fault injection, F8 locale** — 각 별도 spec.

---

## 7. Verification Gates

### 7.1 Build & Type check

```bash
cd /Users/stello/elnath
go vet ./internal/userfacingerr/... ./internal/onboarding/... ./cmd/elnath/...
go build ./cmd/elnath/...
```

### 7.2 Tests

```bash
go test -race ./internal/userfacingerr/... ./internal/onboarding/... ./cmd/elnath/...
```

기대 결과: 0 failures, no race conditions.

### 7.3 Manual smoke

```bash
# Error catalog
./elnath errors list
# expected: 14 lines with ELN-001 through ELN-120

./elnath errors ELN-001
# expected: "Provider not configured" + What/Why/Fix 출력

./elnath errors 030
# expected: "Daemon socket unreachable" (prefix 자동 보완)

# Per-command help
./elnath run --help
# expected: USAGE / DESCRIPTION / FLAGS / EXAMPLES / SEE ALSO 포함, ≥25 줄

./elnath setup --help
# expected: --quickstart 플래그 설명 포함

# Quickstart (Codex OAuth 없는 환경에서)
./elnath setup --quickstart
# expected: API key 프롬프트 → 입력 후 config 저장 → "Try a demo task?" → Y → "what is 2+2?" 응답 출력

# Metric 확인
cat ~/.elnath/onboarding_metric.json
# expected: setup_started_at, setup_completed_at, duration_sec, steps 전부 존재
```

### 7.4 Code hygiene

```bash
# userfacingerr import 없는 user-facing 에러 경로 잔존 여부 확인 (샘플)
grep -n "no LLM provider configured" /Users/stello/elnath/cmd/elnath/commands.go
# expected: userfacingerr.Wrap 로 교체됨

# 순환 import 없음
go build ./...
```

---

## 8. Commit Message Template

```
feat: phase F-6 F7 onboarding UX β

5분 첫 task 도달, 친절한 에러 코드, man-page style help 강화.

- elnath setup --quickstart: Codex OAuth auto-detect, default 채움,
  demo task [Y/n], onboarding_metric.json 기록 (local-only, 외부 전송 0)
- internal/userfacingerr: ELN-XXX 코드 14개, catalog (what/why/fix),
  Wrap() helper. Top-N 10개 user-facing 에러 경로 Wrap 적용.
- elnath errors <code|list>: 에러 코드 상세 조회 명령 신규
- Help 강화: run/setup/wiki/lessons/daemon 5개 명령
  25-50줄 man-page style + 예제 3-5개 + See also
- 각 명령 --help / -h 플래그 라우팅 (executeCommand 분기)

Deferred:
- In-CLI tutorial, web docs, opt-in telemetry
- 모든 fmt.Errorf 표준 wrapping (top-N 10개만)
- Standard a11y (α phase)
```

---

## 9. OpenCode Prompt Pointer

`docs/specs/PHASE-F6-F7-OPENCODE-PROMPT.md` (별도 작성).

구조:
- §1 Context (메모리 + 본 spec + decisions Q13-Q17 요약)
- §2 파일 생성/수정 목록 (절대 경로)
- §3 구현 지시 (파일별, §3.1-3.8 그대로)
- §4 Error catalog 전체 주입 (codes.go, catalog.go 내용)
- §5 테스트 요구
- §6 Verification gate 명령
- §7 Commit message 템플릿
- §8 자가 리뷰 체크리스트 (순환 import 없음, metric 외부 전송 0, --quickstart 기존 setup 동작 유지)

---

## 10. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|-----------|
| `RunDemoTask` 가 실제 LLM 호출을 해서 과금 발생 | LOW | `MaxTokens: 64` 고정. "what is 2+2?" = 단답형. Codex OAuth 시 토큰 비용 0. |
| `--quickstart` 가 기존 setup 흐름 override 시 기존 rerun 모드 회귀 | MED | `--quickstart` 분기는 기존 `cmdSetup` 바디에 진입하지 않음 (early return 패턴). 기존 full setup 은 플래그 없으면 기존 경로 100% 그대로. |
| `onboarding_metric.json` 에 민감 정보 (API key) 포함 위험 | HIGH | `MetricSteps.APIKey` 는 `bool` (존재 여부만). 실제 key string 은 절대 기록 안 함. 코드 리뷰 시 명시적 확인 필요. |
| ELN-XXX 코드 안정성 — 코드 번호 변경 시 사용자 혼란 | MED | `codes.go` 주석에 "Codes are never reused after assignment" 명시. 코드는 숫자 gaps 허용 (중간 삽입 지양). |
| 5분 metric 의 `DurationSec` 이 wallclock 기반이라 시스템 부하에 따라 가변 | LOW | 설계 목표 검증 도구일 뿐. 절대 임계값 없음. |
| `userfacingerr.Wrap` 호출 지점에서 `nil` err 전달 시 Hint 만 노출돼 원인 불명 | MED | `Wrap` 의 `wrapped` 가 nil 이면 포맷에서 `%v` 파트 생략. Callsite 에서 항상 실제 err 전달 권장 (lint 규칙 추가 고려). |
| `cmd_errors.go` 의 prefix 자동 보완("001" → "ELN-001") 이 미래 코드 네임스페이스와 충돌 | LOW | prefix 를 "ELN-" 로 고정. 다른 도구 코드와 동일 숫자 사용 가능성 낮음. |
| Help 텍스트가 i18n.go 에 하드코딩되어 Ko 번역 누락 | MED | En 먼저, Ko 는 best-effort (F7 scope). 번역 누락 키는 `T()` 가 key 자체 반환 → 영어 fallback. Ko i18n 번역 누락 시 `T()` 가 raw key 반환 방지: 빈 문자열 placeholder 삽입. |

---

## 11. Estimated LOC Breakdown

| File | NEW/MODIFY | Est LOC |
|------|-----------|---------|
| `cmd/elnath/cmd_setup.go` | MODIFY | +40 |
| `internal/onboarding/quickstart.go` | NEW | 90 |
| `internal/onboarding/metric.go` | NEW | 50 |
| `internal/onboarding/demo.go` | NEW | 60 |
| `internal/userfacingerr/codes.go` | NEW | 40 |
| `internal/userfacingerr/catalog.go` | NEW | 80 |
| `internal/userfacingerr/wrap.go` | NEW | 40 |
| `cmd/elnath/cmd_errors.go` | NEW | 50 |
| `cmd/elnath/commands.go` | MODIFY | +20 |
| `internal/onboarding/i18n.go` | MODIFY | +120 |
| Tests (6 files) | NEW | ~180 |

**Production 소계**: ~590 LOC (수정 포함)
**신규 production LOC**: ~410
**Test 소계**: ~180
**Total**: ~590 LOC

DECISIONS.md 의 F7 추정 ~400 LOC 과 근접 (신규 production 기준).

---

## 12. Next After This Spec

1. 사용자 리뷰 → 수정 반영
2. LB7 / F8 spec 작성 (2개 남음)
3. OpenCode prompt 4개 작성 (LB6 / LB7 / F7 / F8)
4. OpenCode 4 세션 병렬 위임 (sub-feature 독립성 확인 — F7 은 `cmd/elnath/`, `internal/onboarding/`, `internal/userfacingerr/` 만 접촉, 다른 sub-feature 와 공유 파일 없음)

---

## 13. Spec-Stage Decisions

사용자 확정 사항 없음 — Q13-Q17 이미 `PHASE-F6-DECISIONS.md` 에서 확정. 아래는 spec 작성 중 내부적으로 결정한 기본값:

| ID | Question | Decision | Rationale |
|----|----------|----------|-----------|
| S1 | `RunDemoTask` 시그니처 | `RunDemoTask(ctx, provider, model)` — provider 주입 | `internal/onboarding` → `cmd/elnath` 역의존 회피. 순환 import 없음. |
| S2 | `onboarding_metric.json` 위치 | `cfgPath` 디렉터리 (기본 `~/.elnath/`) | config.yaml 과 동일 위치 → 사용자가 찾기 쉬움. |
| S3 | `TOptional` 신규 헬퍼 | key 없으면 `""` 반환 (기존 `T` 는 key 자체 반환) | `printCommandHelp` 가 unknown 명령에서 fallback 처리 필요. |
| S4 | Error catalog 개수 | **14개** (ELN-001 ~ ELN-120, 10씩 증가) | 번호 gaps 허용으로 카테고리 확장 여지. 향후 ELN-031~039 는 daemon 연관 코드. |
| S5 | `promptYN` TTY 감지 | `isatty.IsTerminal(os.Stdin.Fd())` (기존 `cmd_run.go` 패턴) | 일관성. non-TTY 는 default 반환 (CI 친화). |
