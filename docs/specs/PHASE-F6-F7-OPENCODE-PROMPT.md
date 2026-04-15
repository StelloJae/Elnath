# Phase F-6 F7 — OpenCode 위임 Prompt (Onboarding UX β)

진실 원천: `docs/specs/PHASE-F6-F7-ONBOARDING-UX.md`
브랜치: `feat/telegram-redesign`
프로젝트 루트: `/Users/stello/elnath`

---

## 1. Context

### 1.1 Phase 목적

**Phase F-6 F7 은 "5분 안에 첫 task" 를 달성하는 Onboarding UX β 다.**

F-5 까지 내부 품질 (lesson extraction, circuit breaker, Anthropic provider refresh) 에 집중했다. F-6 F7 은 그 결과물이 외부에 노출되는 UX 를 정비한다. β 는 UX 친절성(onboarding accessibility)이며, 표준 a11y (스크린 리더, colour contrast 등 α) 는 별도 phase 다.

**이 phase 가 달성해야 할 것:**

- `elnath setup --quickstart`: Codex OAuth 자동 감지, 기본값 채움, demo 1회, 1분 이내 완료
- `internal/userfacingerr`: ELN-XXX 코드 14개, catalog, Wrap() helper, Top-N 10개 에러 경로 적용
- `elnath errors <code|list>`: 에러 코드 상세 조회 명령 신규
- 5개 명령 man-page style help 강화 (25-50 줄 + 예제 + See also)
- 각 명령 `--help`/`-h` 라우팅

### 1.2 Decisions (Q13-Q17 확정, 변경 불가)

| ID | 결정 내용 |
|----|---------|
| Q13 | `elnath setup --quickstart` minimal mode — 기존 setup 확장, 새 명령 표면 추가 없음 |
| Q14 | Local-only `~/.elnath/data/onboarding_metric.json` — 외부 전송 0 |
| Q15 | Top-N 10개 에러 경로 + ELN-XXX 코드 catalog |
| Q16 | man-page style help 강화 — 핵심 명령 5개, 25-50 줄 |
| Q17 | Setup 끝 demo 1개 — setup → demo 자연 flow |

### 1.3 선행 Phase 상태

- Phase F-5 commit: `a18d026` (LLM lesson extraction, circuit breaker)
- `internal/llm/codex_oauth.go` 에 `CodexOAuthAvailable()` 함수 존재 — **재정의 금지**
- `internal/onboarding/` 패키지 존재 (TUI model, i18n, setup wizard)
- `cmd/elnath/commands.go` 에 명령 dispatch 테이블 존재

---

## 2. Scope

### 2.1 신규 파일

| 파일 | 구분 |
|------|------|
| `internal/onboarding/quickstart.go` | NEW |
| `internal/onboarding/metric.go` | NEW |
| `internal/onboarding/demo.go` | NEW |
| `internal/userfacingerr/codes.go` | NEW |
| `internal/userfacingerr/catalog.go` | NEW |
| `internal/userfacingerr/wrap.go` | NEW |
| `cmd/elnath/cmd_errors.go` | NEW |
| `internal/onboarding/metric_test.go` | NEW |
| `internal/onboarding/quickstart_integration_test.go` | NEW |
| `internal/userfacingerr/wrap_test.go` | NEW |
| `internal/userfacingerr/catalog_test.go` | NEW |
| `internal/userfacingerr/regression_test.go` | NEW |
| `cmd/elnath/cmd_errors_test.go` | NEW |

### 2.2 수정 파일

| 파일 | 변경 규모 |
|------|---------|
| `cmd/elnath/cmd_setup.go` | +40 LOC (--quickstart 분기) |
| `cmd/elnath/commands.go` | +20 LOC (errors 등록, --help 라우팅) |
| `internal/onboarding/i18n.go` | +120 LOC (5개 help key 추가) |
| Top-N 에러 wrap 대상 파일 (~5개) | 각 1-3 LOC 교체 |

### 2.3 변경 금지

- `internal/llm/codex_oauth.go` — `CodexOAuthAvailable()` 정의 파일. 수정 금지
- `docs/specs/` 파일 — 수정 금지
- F-5 에서 확정된 Lesson struct, deriveID, FailCounter→Breaker wiring

---

## 3. Task

### 3.1 `cmd/elnath/cmd_setup.go` 확장

기존 `cmdSetup` 함수에 `--quickstart` 분기를 추가한다. 기존 full setup 경로는 플래그 없으면 100% 그대로 유지한다.

```go
func cmdSetup(ctx context.Context, args []string) error {
    cfgPath := extractConfigFlag(os.Args)
    if cfgPath == "" {
        cfgPath = config.DefaultConfigPath()
    }

    // NEW: quickstart mode — early return, never enters existing wizard body
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

    metric := onboarding.MetricRecord{
        SetupStartedAt:   started,
        SetupCompletedAt: time.Now(),
        Steps: onboarding.MetricSteps{
            Provider:  result.ProviderDetected,
            APIKey:    result.APIKey != "",  // bool 만 — 문자열 저장 금지
            SmokeTest: result.SmokeTestPassed,
            DemoTask:  false,
        },
    }

    // Demo task: provider 는 cmd layer 에서 빌드 후 RunDemoTask 에 주입 (역의존 방지)
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

    _ = onboarding.WriteMetric(metric) // best-effort, non-fatal

    if !demoRan {
        fmt.Println("\nSetup complete. Run 'elnath run' to start.")
    }
    return nil
}
```

`promptYN(prompt string, defaultYes bool) bool` 헬퍼도 이 파일에 추가한다. TTY 에서 `[Y/n]` (defaultYes=true) 또는 `[y/N]` (defaultYes=false) 를 출력. non-TTY (`!term.IsTerminal(int(os.Stdin.Fd()))`) 에서는 default 반환.

**중요**: `onboardingResultToConfig` 가 이미 존재하면 재사용. 없으면 `QuickstartResult.Result` 에서 필드 매핑하는 로컬 함수 추가.

---

### 3.2 `internal/onboarding/quickstart.go` (신규)

**Bubbletea TUI 없는 plain fmt+bufio 경로.** `--quickstart` 의 목적이 단계 최소화이므로 alt-screen 오버헤드를 쓰지 않는다.

**`llm.CodexOAuthAvailable()` 는 `internal/llm/codex_oauth.go` 의 기존 함수를 import 해서 사용. quickstart.go 에 동일 함수 재정의 절대 금지.**

```go
package onboarding

import (
    "bufio"
    "context"
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "github.com/stello/elnath/internal/llm"
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

    // Step 1: Provider detection — uses existing llm.CodexOAuthAvailable()
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

    // Step 2: Apply defaults (mirrors PathQuick logic)
    home, _ := os.UserHomeDir()
    base := filepath.Join(home, ".elnath")
    res.DataDir = filepath.Join(base, "data")
    res.WikiDir = filepath.Join(base, "wiki")
    res.PermissionMode = "default"
    res.Locale = En

    // Step 3: Smoke test (only when API key present)
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

// readLineOrEnv returns the environment variable value if set,
// otherwise reads a line from stdin.
func readLineOrEnv(envKey string) string {
    if v := os.Getenv(envKey); v != "" {
        return v
    }
    scanner := bufio.NewReader(os.Stdin)
    line, _ := scanner.ReadString('\n')
    return strings.TrimRight(line, "\r\n")
}
```

`ValidateAnthropicKey` 가 이미 `internal/onboarding/` 패키지에 있으면 그대로 사용. 없으면 `internal/llm` 의 ping 함수를 사용하거나, 없으면 단순히 `res.SmokeTestPassed = false` 로 스킵하고 위 spec 에서 제공한 시그니처만 맞추면 된다.

---

### 3.3 `internal/onboarding/demo.go` (신규)

**시그니처 고정: `RunDemoTask(ctx context.Context, provider llm.Provider, model string) error`**

내부에서 `buildProvider` 패턴이나 config 로드를 호출하지 말 것. provider 는 항상 caller (`cmd_setup.go`) 가 빌드해서 주입한다. 순환 import 방지.

```go
package onboarding

import (
    "context"
    "fmt"

    "github.com/stello/elnath/internal/llm"
)

// RunDemoTask submits a minimal "what is 2+2?" prompt via the injected provider
// and streams the response to stdout. Bypasses DB, wiki, and tools to stay
// lightweight. provider and model are injected by the caller (cmd layer).
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

`llm.Request`, `llm.Message`, `llm.ContentBlock`, `llm.StreamEvent`, `llm.EventText` 타입은 기존 `internal/llm/` 에서 확인해서 맞는 타입명을 쓸 것. `provider.Stream` 시그니처가 다르면 기존 코드베이스 패턴을 따를 것.

---

### 3.4 `internal/onboarding/metric.go` (신규)

**`MetricSteps.APIKey` 는 반드시 `bool` 타입. API key 문자열을 struct 에 넣으면 절대 안 된다.**

**파일 경로는 `config.DefaultDataDir()` 고정. `--config` 플래그나 `cfgPath` 디렉터리에 따라 달라지면 안 된다.**

```go
package onboarding

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "time"

    "github.com/stello/elnath/internal/config"
)

// MetricRecord captures the onboarding timeline for local diagnostics.
// Written once to config.DefaultDataDir()/onboarding_metric.json.
// Never transmitted externally (Q14=B).
type MetricRecord struct {
    SetupStartedAt   time.Time   `json:"setup_started_at"`
    SetupCompletedAt time.Time   `json:"setup_completed_at"`
    DurationSec      int         `json:"duration_sec"`
    Steps            MetricSteps `json:"steps"`
}

type MetricSteps struct {
    Provider  string `json:"provider"`    // "codex" | "anthropic" | ""
    APIKey    bool   `json:"api_key"`     // bool 만 — 문자열 저장 금지
    SmokeTest bool   `json:"smoke_test"`
    DemoTask  bool   `json:"demo_task"`
}

// WriteMetric persists the record to config.DefaultDataDir()/onboarding_metric.json.
// Overwrites any previous file — onboarding is a one-time event.
// Failure is non-fatal: caller ignores returned error with _.
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
    return os.WriteFile(path, data, 0o600) // 0600 — user-only
}
```

---

### 3.5 `internal/userfacingerr/` 패키지 (신규)

패키지를 새로 만든다. 파일 3개: `codes.go`, `catalog.go`, `wrap.go`.

#### `internal/userfacingerr/codes.go`

14개 코드. **번호는 10 단위 gap (001, 002 는 예외 — 이미 Q15 에서 할당됨). 같은 번호를 다른 의미로 재사용 금지.**

```go
package userfacingerr

// Code is a stable ELN-XXX error identifier.
// Codes are never reused after assignment. Gaps are reserved for future use.
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
    ELN070 Code = "ELN-070" // session file corrupted (future — no emission path yet)
    ELN080 Code = "ELN-080" // rate limited (429)
    ELN090 Code = "ELN-090" // OAuth token missing / absent
    ELN100 Code = "ELN-100" // wiki page not found
    ELN110 Code = "ELN-110" // daemon task timeout
    ELN120 Code = "ELN-120" // empty LLM response (future — defined but not yet emitted)
)
```

#### `internal/userfacingerr/catalog.go`

14개 엔트리. **ELN-070, ELN-120 은 catalog 에 정의만 — Wrap 주입 대상 아님.**

```go
package userfacingerr

// CatalogEntry describes an error code for "elnath errors <code>".
type CatalogEntry struct {
    Code     Code
    Title    string
    What     string
    Why      string
    HowToFix string
}

var catalog = []CatalogEntry{
    {ELN001, "Provider not configured",
        "Elnath could not find an LLM provider (Anthropic API key or Codex OAuth).",
        "No API key is set in config.yaml and no Codex OAuth token was found.",
        "Run 'elnath setup --quickstart' or set ELNATH_ANTHROPIC_API_KEY."},
    {ELN002, "OAuth token expired",
        "The Codex OAuth access token has expired and automatic refresh failed.",
        "The refresh token may have been revoked or the network is unavailable.",
        "Re-authenticate with 'codex auth' and retry."},
    {ELN010, "Wiki not initialized",
        "The wiki directory does not exist or has not been initialised.",
        "wiki_dir in config.yaml points to a non-existent path, or setup was skipped.",
        "Run 'elnath setup' and confirm the wiki directory, or create it manually."},
    {ELN020, "Permission denied",
        "Elnath's path guard blocked access to the requested file or directory.",
        "The target path is outside the allowed working directories.",
        "Check permission.allow in config.yaml or move the file inside the project root."},
    {ELN030, "Daemon socket unreachable",
        "The CLI could not connect to the Elnath daemon socket.",
        "The daemon is not running, or the socket_path in config.yaml is stale.",
        "Run 'elnath daemon start' or check 'elnath daemon status'."},
    {ELN040, "LLM request timeout",
        "The LLM provider did not respond within the configured timeout.",
        "High load on the provider, large prompt, or network latency.",
        "Retry. Increase anthropic.timeout_seconds in config.yaml if recurring."},
    {ELN050, "Tool execution failed",
        "A tool (bash, write, edit, etc.) returned a non-zero exit or error.",
        "The command failed, a file was locked, or the path was invalid.",
        "Check the error detail above. Re-run with a corrected command or path."},
    {ELN060, "Config invalid",
        "Elnath could not parse or validate config.yaml.",
        "A required field is missing, has an unexpected type, or the YAML is malformed.",
        "Run 'elnath setup' to regenerate config, or edit ~/.elnath/config.yaml manually."},
    {ELN070, "Session file corrupted",
        "A session JSONL file could not be parsed.",
        "The file was truncated (e.g. disk full) or written by an incompatible version.",
        "Delete the corrupted file from ~/.elnath/data/sessions/ and start a new session."},
    {ELN080, "Rate limited (429)",
        "The LLM provider rejected the request due to rate limiting.",
        "Too many requests in a short window, or quota exhausted.",
        "Wait a moment and retry. Check provider dashboard for quota status."},
    {ELN090, "OAuth token missing / absent",
        "The OAuth access token is missing or absent from the auth file.",
        "Authentication was not completed or the auth file was removed.",
        "Run 'elnath setup' to re-authenticate with Codex / Anthropic."},
    {ELN100, "Wiki page not found",
        "The requested wiki page does not exist.",
        "The path is incorrect, the page was deleted, or the wiki dir was changed.",
        "Run 'elnath wiki search <term>' to find the correct path."},
    {ELN110, "Daemon task timeout",
        "A task submitted to the daemon exceeded its execution time limit.",
        "The task is too large, the LLM is slow, or the daemon is overloaded.",
        "Increase daemon.task_timeout_seconds in config.yaml, or split the task."},
    {ELN120, "Empty LLM response",
        "The LLM stream completed without producing any text content.",
        "The model refused the prompt, or a content filter triggered.",
        "Retry with a rephrased prompt. Check for content policy restrictions."},
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

#### `internal/userfacingerr/wrap.go`

```go
package userfacingerr

import (
    "errors"
    "fmt"
)

// UserFacingError carries a stable ELN-XXX code and a user-readable hint.
// It wraps the original error for %w unwrapping.
type UserFacingError struct {
    code    Code
    context string
    wrapped error
}

// Wrap creates a UserFacingError. context is a short (≤30 char) caller label.
// err may be nil (hint-only case).
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

// Is reports whether target carries the same error code.
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

---

### 3.6 `cmd/elnath/cmd_errors.go` (신규)

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
    code := userfacingerr.Code(strings.ToUpper(raw))
    if !strings.HasPrefix(string(code), "ELN-") {
        code = userfacingerr.Code("ELN-" + string(code))
    }
    entry, ok := userfacingerr.Lookup(code)
    if !ok {
        return fmt.Errorf("unknown error code %q — run 'elnath errors list'", raw)
    }
    fmt.Printf("\n%s — %s\n\n", entry.Code, entry.Title)
    fmt.Printf("What:  %s\n", entry.What)
    fmt.Printf("Why:   %s\n", entry.Why)
    fmt.Printf("Fix:   %s\n\n", entry.HowToFix)
    return nil
}

func printErrorsHelp() error {
    fmt.Print(`Usage: elnath errors <code|list>

Look up an Elnath error code for details and suggested fixes.

Commands:
  list         List all known error codes with short titles
  <code>       Show full details for the given code

Arguments:
  code         The ELN-XXX code shown in an error message.
               You may omit the "ELN-" prefix (e.g. "elnath errors 001").

Examples:
  elnath errors list
  elnath errors ELN-001
  elnath errors 030

See also: elnath setup --quickstart, elnath daemon start
`)
    return nil
}
```

---

### 3.7 `cmd/elnath/commands.go` 수정

두 가지를 수정한다.

**① `"errors"` 명령 등록**: 기존 명령 dispatch 테이블 (map 또는 switch) 에 `"errors": cmdErrors` 를 추가한다.

**② `--help`/`-h` intercept**: 명령 dispatch 전에 인터셉트해서 `printCommandHelp(name)` 을 호출한다.

```go
func executeCommand(ctx context.Context, name string, args []string) error {
    // per-command help intercept — added before existing dispatch
    if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
        return printCommandHelp(name)
    }
    // existing dispatch (unchanged below)
    // ...
}

func printCommandHelp(name string) error {
    locale := loadLocale() // 기존 locale 로딩 패턴 사용
    key := "cmd." + name + ".help"
    text := onboarding.TOptional(locale, key)
    if text == "" {
        return cmdHelp(nil, nil) // fallback to global help
    }
    fmt.Println(text)
    return nil
}
```

`onboarding.TOptional(locale, key) string` 은 키 없으면 빈 문자열 반환하는 헬퍼다. 기존 `T()` 가 키 없으면 key 자체 반환하므로, `TOptional` 을 `internal/onboarding/i18n.go` 에 추가해야 한다. 구현: key 가 translations map 에 없으면 `""` 반환.

`loadLocale()` 이 없으면 기존 코드베이스에서 locale 을 얻는 방법을 grep 해서 맞는 패턴 사용.

---

### 3.8 `internal/onboarding/i18n.go` 확장

**5개 help key 를 En/Ko 로케일 모두에 추가한다. Ko 는 빈 문자열 placeholder 로 명시적 추가. 키 누락 시 `T()` 가 raw key 반환하는 것을 방지하기 위해서다.**

추가할 키: `cmd.run.help`, `cmd.setup.help`, `cmd.wiki.help`, `cmd.lessons.help`, `cmd.daemon.help`.

`TOptional` 헬퍼도 이 파일에 추가한다.

각 key 의 값은 man-page style 텍스트 (아래 영어 원문 사용):

**`cmd.run.help`**:
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

**`cmd.setup.help`**:
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
  --config <path> Use an alternative config file

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

**`cmd.wiki.help`**:
```
USAGE
  elnath wiki <subcommand> [args]

DESCRIPTION
  Manage Elnath's local knowledge base (wiki). The wiki stores
  Markdown pages with YAML frontmatter and is searchable via SQLite FTS5.
  Pages are linked to agent sessions for automatic context injection.

SUBCOMMANDS
  search <term>     Full-text search across all wiki pages
  get <path>        Show a specific page by path
  list              List all pages (title, path, updated)
  add <path>        Create a new page interactively
  delete <path>     Delete a page

EXAMPLES
  # Search for pages about authentication
  $ elnath wiki search authentication

  # View a specific page
  $ elnath wiki get /architecture/auth

  # List all pages
  $ elnath wiki list

  # Create a new page
  $ elnath wiki add /notes/my-note

SEE ALSO
  elnath run, elnath lessons
```

**`cmd.lessons.help`**:
```
USAGE
  elnath lessons <subcommand>

DESCRIPTION
  View and manage lessons Elnath has learned from past agent runs.
  Lessons influence future agent behaviour via persona parameters.
  LLM-extracted lessons (when enabled) are stored separately from
  manually curated ones.

SUBCOMMANDS
  list              List recent lessons (default: 20)
  stats             Show lesson store statistics and LLM extraction status
  delete <id>       Delete a lesson by ID

EXAMPLES
  # List recent lessons
  $ elnath lessons list

  # Show LLM extraction status and breaker state
  $ elnath lessons stats

  # Delete a specific lesson
  $ elnath lessons delete abc123

SEE ALSO
  elnath run, elnath wiki
```

**`cmd.daemon.help`**:
```
USAGE
  elnath daemon <subcommand> [flags]

DESCRIPTION
  Manage the Elnath background daemon. The daemon accepts task requests
  via a Unix socket and runs agent sessions asynchronously. Use it for
  long-running tasks or integration with external automation.

SUBCOMMANDS
  start             Start the daemon in the background
  stop              Stop a running daemon
  status            Show daemon status and active task count
  logs              Stream daemon logs to stdout
  task submit       Submit a task to the running daemon
  task list         List submitted tasks and their statuses
  task cancel <id>  Cancel a pending or running task

FLAGS
  --config <path>   Use an alternative config file
  --socket <path>   Override the Unix socket path

EXAMPLES
  # Start the daemon
  $ elnath daemon start

  # Check daemon status
  $ elnath daemon status

  # Submit a task
  $ elnath daemon task submit "summarise the latest PRs"

  # Stream daemon logs
  $ elnath daemon logs

  # Stop the daemon
  $ elnath daemon stop

SEE ALSO
  elnath run, elnath errors ELN-030
```

Ko 로케일에는 동일 key 를 `""` (빈 문자열) 로 추가한다. 예:
```go
Ko: map[string]string{
    // ...existing keys...
    "cmd.run.help":     "",
    "cmd.setup.help":   "",
    "cmd.wiki.help":    "",
    "cmd.lessons.help": "",
    "cmd.daemon.help":  "",
}
```

---

### 3.9 Top-N 에러 Wrap 주입 (10개 경로)

아래 10개 callsite 에서 기존 `fmt.Errorf(...)` 또는 직접 `errors.New(...)` 를 `userfacingerr.Wrap(ELNXXX, err, context)` 로 교체한다. **ELN-070, ELN-120 은 현재 emission path 없으므로 건드리지 않는다.**

| # | 파일 | 기존 에러 문자열 패턴 | Wrap 코드 |
|---|------|---------------------|----------|
| 1 | `cmd/elnath/commands.go` | `"no LLM provider configured"` | `ELN001` |
| 2 | `cmd/elnath/cmd_run.go` | `"load config: ..."` | `ELN060` |
| 3 | `internal/llm/codex_oauth.go` | `"codex: refresh failed"` | `ELN002` |
| 4 | `internal/llm/anthropic.go` | `"anthropic: rate limit (429)"` | `ELN080` |
| 5 | `internal/llm/anthropic.go` | HTTP timeout error | `ELN040` |
| 6 | `internal/daemon/daemon.go` | `"daemon: task timed out"` | `ELN110` |
| 7 | `cmd/elnath/cmd_wiki.go` | wiki 디렉터리 미초기화 에러 | `ELN010` |
| 8 | `internal/tools/pathguard.go` | `"write denied"` | `ELN020` |
| 9 | `cmd/elnath/cmd_run.go` | daemon 연결 실패 경로 | `ELN030` |
| 10 | `internal/wiki/store.go` | `"wiki store: page not found"` | `ELN100` |

각 파일을 grep 해서 정확한 에러 문자열과 라인을 찾은 뒤 교체한다. 경로명은 예시이므로 실제 파일명이 다르면 grep 으로 확인할 것.

---

## 4. Error Catalog 전체 (codes.go + catalog.go 참조)

| Code | Title | future? | Wrap 주입 |
|------|-------|---------|----------|
| ELN-001 | Provider not configured | — | 예 (#1) |
| ELN-002 | OAuth token expired | — | 예 (#3) |
| ELN-010 | Wiki not initialized | — | 예 (#7) |
| ELN-020 | Permission denied | — | 예 (#8) |
| ELN-030 | Daemon socket unreachable | — | 예 (#9) |
| ELN-040 | LLM request timeout | — | 예 (#5) |
| ELN-050 | Tool execution failed | — | catalog 만 (callsite 분산) |
| ELN-060 | Config invalid | — | 예 (#2) |
| **ELN-070** | Session file corrupted | **future** | **금지** |
| ELN-080 | Rate limited (429) | — | 예 (#4) |
| ELN-090 | OAuth token missing / absent | — | catalog 만 (ELN-002 와 구분) |
| ELN-100 | Wiki page not found | — | 예 (#10) |
| ELN-110 | Daemon task timeout | — | 예 (#6) |
| **ELN-120** | Empty LLM response | **future** | **금지** |

---

## 5. Tests Required

### 5.1 `internal/onboarding/metric_test.go`

- `WriteMetric` → 파일 생성, JSON round-trip (모든 필드 검증)
- `WriteMetric` 두 번 호출 → 파일 덮어쓰기 (append 아님, `ioutil.ReadFile` 로 확인)
- 디렉터리가 없을 때 → `os.MkdirAll` 로 생성되는지 확인
- 파일 권한 `0o600` 확인

### 5.2 `internal/userfacingerr/wrap_test.go`

- `Wrap(ELN001, err, "test")` → `.Error()` 에 `"ELN-001"` 포함
- `Wrap(ELN001, err, "test")` → `.Error()` 에 `HowToFix` 포함
- `errors.As` 로 `*UserFacingError` unwrap 가능
- `Wrap(ELN001, nil, "test")` → nil wrapped err 시 panic 없음
- unknown code (`"ELN-999"`) → fallback format (code + context, panic 없음)
- `e.Code()` → `ELN001` 반환

### 5.3 `internal/userfacingerr/catalog_test.go`

- `Lookup(ELN001)` → 찾음, 모든 필드 비어있지 않음
- `Lookup("ELN-999")` → not found, ok=false
- `All()` → `len == 14`
- 각 entry 의 Code, Title, What, Why, HowToFix 비어있지 않음 (table-driven test)

### 5.4 `cmd/elnath/cmd_errors_test.go`

- `cmdErrors(ctx, []string{"list"})` → 에러 없음, stdout 에 `"ELN-001"` 포함
- `cmdErrors(ctx, []string{"ELN-001"})` → 에러 없음, stdout 에 `"Provider not configured"` 포함
- `cmdErrors(ctx, []string{"001"})` → prefix 자동 보완, ELN-001 출력
- `cmdErrors(ctx, []string{"ELN-999"})` → 에러 반환

### 5.5 `internal/userfacingerr/regression_test.go`

- `fmt.Errorf("no LLM provider configured: ...")` 를 `Wrap(ELN001, ...)` 으로 감쌌을 때 `errors.Unwrap` 체인이 원본 메시지를 잃지 않는지 확인

### 5.6 `internal/onboarding/quickstart_integration_test.go` (`//go:build integration`)

- 임시 디렉터리에 가짜 `~/.codex/auth.json` 세팅 (유효 OAuth 포맷)
- `RunQuickstart` 실행 → `ProviderDetected == "codex"`, `APIKey == ""`
- `WriteMetric` 호출 → 파일 내용 검증

---

## 6. Verification Gates

작업 완료 후 아래 명령을 순서대로 실행하고 결과를 보고한다.

```bash
cd /Users/stello/elnath

# 1. Type check + vet
go vet ./internal/onboarding/... ./internal/userfacingerr/... ./cmd/elnath/...

# 2. Tests (race detector)
go test -race ./internal/onboarding/... ./internal/userfacingerr/... ./cmd/elnath/...

# 3. Full build
make build

# 4. Smoke: error catalog
./elnath errors list
# 기대: ELN-001 ~ ELN-120 14줄

./elnath errors ELN-001
# 기대: "Provider not configured" + What/Why/Fix 출력

./elnath errors 030
# 기대: "Daemon socket unreachable" (prefix 자동 보완)

# 5. Smoke: per-command help
./elnath run --help
# 기대: USAGE / DESCRIPTION / FLAGS / EXAMPLES / SEE ALSO 포함, ≥25줄

./elnath setup --help
# 기대: --quickstart 플래그 설명 포함

# 6. Smoke: quickstart (Codex OAuth 없는 환경)
./elnath setup --quickstart
# 기대: API key 프롬프트 → 입력 후 config 저장 → "Try a demo task?" → Y → "what is 2+2?" 응답 출력

# 7. Metric 확인
cat ~/.elnath/data/onboarding_metric.json
# 기대: setup_started_at, steps.api_key (bool), steps.demo_task 전부 존재

# 8. 기존 full setup 회귀 없음 확인
./elnath setup --help
# 플래그 없으면 기존 TUI wizard 진입 확인 (인터럽트로 종료)

# 9. 순환 import 없음
go build ./...
```

---

## 7. Commit Message Template

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

커밋은 하지 말 것. stello 가 직접 commit.

---

## 8. Self-Review Checklist

작업 제출 전 아래 항목을 직접 확인하고 체크한다.

- [ ] `CodexOAuthAvailable` 재정의 없음: `grep -rn "func CodexOAuthAvailable" /Users/stello/elnath` 결과가 `internal/llm/` 파일 1개만
- [ ] `RunDemoTask(ctx, provider, model)` 시그니처 일치: `grep -n "func RunDemoTask" /Users/stello/elnath/internal/onboarding/demo.go`
- [ ] `MetricRecord.APIKey` 는 `bool` 타입: `grep -n "APIKey" /Users/stello/elnath/internal/onboarding/metric.go` 에서 `bool` 확인
- [ ] ELN-070, ELN-120 는 `Wrap(` 호출 목록에 없음: `grep -rn "ELN070\|ELN120" /Users/stello/elnath/cmd /Users/stello/elnath/internal` 에서 `wrap.go`/`catalog.go`/`codes.go` 외 파일에 없음
- [ ] ELN-090 title 이 "OAuth token missing / absent" 인지: `grep -n "ELN090\|ELN-090" /Users/stello/elnath/internal/userfacingerr/catalog.go`
- [ ] 5개 help key Ko placeholder 존재: `grep -n "cmd.run.help\|cmd.setup.help\|cmd.wiki.help\|cmd.lessons.help\|cmd.daemon.help" /Users/stello/elnath/internal/onboarding/i18n.go` 에서 Ko map 에도 등장
- [ ] Metric 파일 권한 `0o600`: `WriteMetric` 내 `os.WriteFile(path, data, 0o600)` 확인
- [ ] `--help` intercept 동작: `./elnath run --help` → USAGE 섹션 출력
- [ ] `setup --quickstart` smoke 통과: exit 0, config 파일 생성됨
- [ ] demo 1 task 실행 성공: `what is 2+2?` 응답이 stdout 에 출력
- [ ] 기존 full setup 동작 회귀 없음: `--quickstart` 없으면 TUI wizard 진입
- [ ] Commit message 위 §7 템플릿과 일치

---

## 9. Scope Boundaries (defer 목록)

아래는 이 phase 스코프 밖이다. 구현하지 말 것.

- `elnath tutorial` 명령 (in-CLI tutorial) — Q16=A 로 defer
- `elnath docs` (브라우저 연동) — 외부 hosting 없음
- Opt-in telemetry (서버 전송) — Q14=B, local-only
- 모든 `fmt.Errorf` 표준 wrapping — Top-N 10개만
- `elnath examples list/run` — Q17=B, setup demo 1개만
- 표준 a11y (스크린 리더, WCAG colour contrast) — α phase
- LB6 portability, LB7 fault injection, F8 locale — 각 별도 spec
- `Lesson` struct 에 새 필드 추가, `deriveID` 변경 — F-5 확정

---

## 10. Critical Invariants (위반 시 즉시 수정 후 재제출)

**`CodexOAuthAvailable` 재정의 금지**: 이 함수는 `internal/llm/codex_oauth.go` 에 이미 존재한다. `quickstart.go` 에 동일 함수를 재정의하면 컴파일 에러 또는 동작 불일치가 발생한다. import 해서 사용할 것.

**`RunDemoTask(ctx, provider, model)` 시그니처 고정**: `cmd_setup.go` 의 `buildProvider(cfgResult)` 결과를 주입받는 구조다. `demo.go` 내부에서 config 로드나 provider 빌드를 직접 하면 `onboarding → cmd/elnath` 역의존이 생겨 순환 import 가 발생한다.

**`MetricRecord.APIKey` 는 `bool` 타입**: `api_key: true/false` 만 기록한다. 실제 API key 문자열을 struct 나 JSON 에 넣으면 개인정보 유출이다.

**ELN-070, ELN-120 은 `Wrap()` 주입 대상 아님**: catalog 정의만 하고 callsite 에서 호출하지 않는다. 현재 emission path 없음.

**ELN-090 title 은 "OAuth token missing / absent"**: rename 확정. "credentials missing" 등 다른 표현 금지.

**Ko 번역 placeholder 명시적 추가**: `""` 로라도 Ko map 에 키가 있어야 한다. 키가 없으면 `T()` 가 `"cmd.run.help"` raw key 를 반환하는 경우 발생 가능.

**Metric 경로 `config.DefaultDataDir()` 고정**: `cfgPath` 디렉터리, `--config` 플래그 경로에 따라 달라지면 안 된다. 항상 사용자 홈 기준 data dir.
