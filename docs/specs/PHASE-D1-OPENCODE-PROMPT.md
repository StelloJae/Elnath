# OpenCode Delegation Prompt: Phase D-1 Secret Governance + Audit Foundation

3 phase로 나뉜다. 각 phase 완료 후 `go test -race` + `go vet` 검증.

---

## Phase 1: internal/audit/ + internal/secret/ (detector + rules)

```
elnath 프로젝트 (`/Users/stello/elnath/`, feat/telegram-redesign 브랜치)에서 Phase D-1 작업을 시작한다.

목표: `internal/audit/` 패키지 (JSONL audit trail)와 `internal/secret/` 패키지 (secret detector + redactor)를 신설한다.

### 작업 1: internal/audit/event.go

```go
package audit

import "time"

type EventType string

const (
    EventSecretDetected    EventType = "secret_detected"
    EventSecretRedacted    EventType = "secret_redacted"
    EventInjectionBlocked  EventType = "injection_blocked"
    EventPermissionDenied  EventType = "permission_denied"
    EventPermissionGranted EventType = "permission_granted"
    EventSkillExecuted     EventType = "skill_executed"
)

type Event struct {
    Timestamp time.Time `json:"timestamp"`
    Type      EventType `json:"type"`
    SessionID string    `json:"session_id,omitempty"`
    ToolName  string    `json:"tool_name,omitempty"`
    RuleID    string    `json:"rule_id,omitempty"`
    Detail    string    `json:"detail,omitempty"`
}
```

### 작업 2: internal/audit/trail.go

JSONL append-only 파일에 보안 이벤트를 기록한다.

```go
package audit

import (
    "encoding/json"
    "fmt"
    "os"
    "sync"
    "time"
)

type Trail struct {
    mu   sync.Mutex
    file *os.File
    enc  *json.Encoder
}
```

함수:

1. `NewTrail(path string) (*Trail, error)`
   - `os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)`
   - 파일 못 열면 에러 반환
   - `json.NewEncoder(file)` 저장
   - `&Trail{file: file, enc: enc}` 반환

2. `(t *Trail) Log(event Event) error`
   - `t.mu.Lock()` / `defer t.mu.Unlock()`
   - `event.Timestamp`가 zero value이면 `time.Now().UTC()` 설정
   - `t.enc.Encode(event)` — JSON 1줄 + 개행
   - 에러 반환

3. `(t *Trail) Close() error`
   - `t.mu.Lock()` / `defer t.mu.Unlock()`
   - `t.file.Close()`

nil receiver 체크: Log와 Close에서 `t == nil || t.file == nil` → nil 반환 (no-op).

### 작업 3: internal/audit/trail_test.go

테이블 기반 테스트:
- NewTrail: temp dir에 파일 생성 → 에러 없음, 파일 존재 확인
- Log: Event 1개 기록 → 파일 읽어서 JSON 1줄 확인, Timestamp 필드 존재
- Log: Event 2개 → 2줄 확인
- Log: Timestamp zero → 자동 설정 (비어있지 않음)
- Log: Timestamp 미리 설정 → 그대로 유지
- Close: Close 후 Log → 에러 (closed file)
- 동시성: `t.Run("concurrent", func(t *testing.T) { ... })` 에서 10 goroutine, 각 10개 이벤트 → `go test -race` 통과, 파일에 100줄

### 작업 4: internal/secret/rules.go

20개 regex 규칙 정의. 모든 regex는 package-level에서 컴파일.

```go
package secret

import "regexp"

type Rule struct {
    ID      string
    Name    string
    Pattern *regexp.Regexp
}

var defaultRules = func() []Rule {
    type raw struct {
        id, name, pattern string
    }
    defs := []raw{
        {"anthropic-api-key", "Anthropic API Key", `sk-ant-api\d{2}-[\w-]{80,}`},
        {"openai-api-key", "OpenAI API Key", `sk-[a-zA-Z0-9]{20,}`},
        {"openai-project-key", "OpenAI Project Key", `sk-proj-[a-zA-Z0-9]{20,}`},
        {"aws-access-key", "AWS Access Key", `AKIA[0-9A-Z]{16}`},
        {"aws-secret-key", "AWS Secret Key", `(?i)aws_secret_access_key\s*[=:]\s*[A-Za-z0-9/+=]{40}`},
        {"gcp-api-key", "GCP API Key", `AIza[0-9A-Za-z\-_]{35}`},
        {"gcp-service-account", "GCP Service Account", `"type"\s*:\s*"service_account"`},
        {"github-token", "GitHub Token", `gh[pousr]_[A-Za-z0-9_]{36,}`},
        {"github-fine-grained", "GitHub Fine-Grained Token", `github_pat_[A-Za-z0-9_]{22,}`},
        {"gitlab-token", "GitLab Token", `glpat-[A-Za-z0-9\-]{20,}`},
        {"slack-token", "Slack Token", `xox[baprs]-[A-Za-z0-9\-]{10,}`},
        {"slack-webhook", "Slack Webhook", `hooks\.slack\.com/services/T[A-Z0-9]+/B[A-Z0-9]+/[A-Za-z0-9]+`},
        {"stripe-key", "Stripe Key", `[sr]k_(live|test)_[A-Za-z0-9]{20,}`},
        {"telegram-bot-token", "Telegram Bot Token", `\d{8,10}:[A-Za-z0-9_-]{35}`},
        {"jwt-token", "JWT Token", `eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`},
        {"private-key-pem", "PEM Private Key", `-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`},
        {"generic-password", "Generic Password Assignment", `(?i)(?:password|passwd|pwd)\s*[=:]\s*["'][^"']{8,}["']`},
        {"generic-secret", "Generic Secret Assignment", `(?i)(?:secret|token|api_key|apikey)\s*[=:]\s*["'][^"']{8,}["']`},
        {"connection-string", "Database Connection String", `(?i)(?:postgres|mysql|mongodb|redis)://[^\s]+:[^\s]+@`},
        {"env-file-secret", ".env File Secret", `(?i)^[A-Z_]*(KEY|SECRET|TOKEN|PASSWORD|CREDENTIAL)[A-Z_]*\s*=\s*\S{8,}`},
    }
    rules := make([]Rule, len(defs))
    for i, d := range defs {
        rules[i] = Rule{
            ID:      d.id,
            Name:    d.name,
            Pattern: regexp.MustCompile(d.pattern),
        }
    }
    return rules
}()
```

주의: `env-file-secret` 패턴의 `^`는 multiline에서 각 줄 시작을 매치해야 한다. `regexp.MustCompile("(?m)" + pattern)` 으로 처리하거나, Scan에서 줄 단위로 처리. **Scan에서 content 전체에 대해 실행하므로 `(?m)` 플래그를 env-file-secret 패턴에 추가해야 한다:** `(?im)^[A-Z_]*(KEY|SECRET|TOKEN|PASSWORD|CREDENTIAL)[A-Z_]*\s*=\s*\S{8,}`

### 작업 5: internal/secret/detector.go

```go
package secret

import (
    "sort"
    "strings"
)

type Finding struct {
    RuleID string
    Match  string
    Start  int
    End    int
}

type Detector struct {
    rules []Rule
}

func NewDetector() *Detector {
    return &Detector{rules: defaultRules}
}
```

메서드:

1. `(d *Detector) Scan(content string) []Finding`
   - 빈 content → nil
   - 각 rule에 대해 `rule.Pattern.FindAllStringIndex(content, -1)` 실행
   - 각 match를 Finding으로 수집 (RuleID, Match=content[start:end], Start, End)
   - findings를 Start 기준 정렬
   - 겹치는 findings 제거: 같은 범위가 여러 rule에 매치되면 ID 알파벳순 첫 번째만 유지. 범위가 완전히 포함되면 긴 것만 유지.
   - 결과 반환

2. `(d *Detector) Redact(content string, findings []Finding) string`
   - findings 없으면 → content 그대로
   - findings를 Start 기준 역순 정렬 (byte offset이 큰 것부터)
   - `result := content` (string은 immutable이므로 builder나 []byte 사용)
   - 각 finding: `result` 에서 `[Start:End]` 구간을 `[REDACTED:<RuleID>]` 로 교체
   - 결과 반환

3. `(d *Detector) ScanAndRedact(content string) (string, []Finding)`
   - `findings := d.Scan(content)`
   - `redacted := d.Redact(content, findings)`
   - return redacted, findings

### 작업 6: internal/secret/detector_test.go

**Scan 테스트** (각 규칙당 1개 이상):

```go
// 테스트 데이터 예시
tests := []struct {
    name    string
    content string
    wantIDs []string // 기대하는 rule IDs
}{
    {"anthropic key", "key: sk-ant-api03-" + strings.Repeat("a", 80), []string{"anthropic-api-key"}},
    {"openai key", "sk-abcdefghijklmnopqrstuvwxyz1234", []string{"openai-api-key"}},
    {"openai project", "sk-proj-abcdefghijklmnopqrstuvwxyz", []string{"openai-project-key"}},
    {"aws access", "AKIAIOSFODNN7EXAMPLE", []string{"aws-access-key"}},
    {"aws secret", "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", []string{"aws-secret-key"}},
    {"gcp api", "AIzaSyD-abcdefghijklmnopqrstuvwxyz12345", []string{"gcp-api-key"}},
    {"gcp service account", `"type": "service_account"`, []string{"gcp-service-account"}},
    {"github token", "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef1234ab", []string{"github-token"}},
    {"github fine-grained", "github_pat_ABCDEFGHIJKLMNOPQRSTUV1234", []string{"github-fine-grained"}},
    {"gitlab token", "glpat-ABCDEFGHIJKLMNOPQRSTUabcd", []string{"gitlab-token"}},
    {"slack token", "xoxb-123456789012-abcdef", []string{"slack-token"}},
    {"slack webhook", "https://hooks.slack.com/services/T12345678/B12345678/abcdefghijklmnop", []string{"slack-webhook"}},
    {"stripe key", "sk_live_abcdefghijklmnopqrstuvwxyz", []string{"stripe-key"}},
    {"telegram bot", "1234567890:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefgh", []string{"telegram-bot-token"}},
    {"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", []string{"jwt-token"}},
    {"pem key", "-----BEGIN RSA PRIVATE KEY-----", []string{"private-key-pem"}},
    {"generic password", `password = "mysecretpass123"`, []string{"generic-password"}},
    {"generic secret", `api_key = "sk_test_very_long_secret"`, []string{"generic-secret"}},
    {"connection string", "postgres://admin:secretpass@db.example.com:5432/mydb", []string{"connection-string"}},
    {"env secret", "API_SECRET_KEY=abcdef1234567890", []string{"env-file-secret"}},
    {"no secrets", "func main() { fmt.Println(\"hello\") }", nil},
    {"multiple secrets", "AKIAIOSFODNN7EXAMPLE and ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef1234ab", []string{"aws-access-key", "github-token"}},
}
```

**Redact 테스트:**
- 1개 secret → 해당 부분이 `[REDACTED:rule-id]` 로 치환
- 2개 secret → 둘 다 치환, 나머지 텍스트 유지
- 0개 finding → 원본 그대로

**ScanAndRedact 테스트:**
- secret 포함 → redacted + findings 모두 반환
- 빈 content → "", nil

### 검증

```bash
go test -race ./internal/audit/... ./internal/secret/...
go vet ./internal/audit/... ./internal/secret/...
```

모두 통과해야 한다.

주의: `stripe-key` 패턴 `[sr]k_(live|test)_...`과 `openai-api-key` 패턴 `sk-...`이 겹칠 수 있다. `sk_live_...` 는 stripe, `sk-...`은 openai. 구분은 `_` vs `-` 로 된다. 테스트에서 이 edge case 확인.
```

---

## Phase 2: SecretScanHook + runtime 통합

```
Phase D-1 Phase 2. Phase 1에서 internal/audit/ 와 internal/secret/ (detector + rules) 가 완성됐다.

### 참고할 기존 코드

agent.Hook 인터페이스 (internal/agent/hooks.go):
```go
type Hook interface {
    PreToolUse(ctx context.Context, toolName string, params json.RawMessage) (HookResult, error)
    PostToolUse(ctx context.Context, toolName string, params json.RawMessage, result *tools.Result) error
}

type HookResult struct {
    Action  HookAction  // HookAllow or HookDeny
    Message string
}
```

HookRegistry (internal/agent/hooks.go):
- `reg.Add(hook)` — hook 추가
- `reg.RunPostToolUse(ctx, toolName, params, result)` — 모든 hook 순차 실행

executor에서 호출 (internal/agent/executor.go:178-181):
```go
results[call.index] = toolExecResult{id: call.call.ID, output: result.Output, isError: result.IsError}
if a.hooks != nil {
    if hookErr := a.hooks.RunPostToolUse(childCtx, call.call.Name, call.call.Input, result); hookErr != nil {
        a.logger.Warn("post-tool hook error", "tool", call.call.Name, "error", hookErr)
    }
}
```

**중요**: executor.go 의 현재 구조를 보면, `result.Output`이 `toolExecResult`에 복사된 **후에** PostToolUse가 실행된다 (line 177 vs 178). 이것은 문제다 — hook에서 result.Output을 redact해도 이미 복사된 값에는 반영되지 않는다.

**해결 방안 2가지:**

방안 A: executor.go 수정 — PostToolUse를 result 복사 **전에** 실행하도록 순서 변경.
```go
// Hook first, then copy result
if a.hooks != nil {
    if hookErr := a.hooks.RunPostToolUse(childCtx, call.call.Name, call.call.Input, result); hookErr != nil {
        a.logger.Warn("post-tool hook error", "tool", call.call.Name, "error", hookErr)
    }
}
results[call.index] = toolExecResult{id: call.call.ID, output: result.Output, isError: result.IsError}
```

방안 B: executor.go 수정 — PostToolUse 후 result를 다시 복사.
```go
results[call.index] = toolExecResult{id: call.call.ID, output: result.Output, isError: result.IsError}
if a.hooks != nil {
    if hookErr := a.hooks.RunPostToolUse(childCtx, call.call.Name, call.call.Input, result); hookErr != nil {
        a.logger.Warn("post-tool hook error", "tool", call.call.Name, "error", hookErr)
    }
    // Re-copy in case hook modified result
    results[call.index].output = result.Output
}
```

**방안 A를 채택한다.** 더 깔끔하고, hook이 result를 변형할 수 있다는 것이 자연스러운 계약이다.

### 작업 1: internal/secret/hook.go

```go
package secret

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"

    "github.com/stello/elnath/internal/agent"
    "github.com/stello/elnath/internal/audit"
    "github.com/stello/elnath/internal/tools"
)

type SecretScanHook struct {
    detector *Detector
    trail    *audit.Trail
}

func NewSecretScanHook(detector *Detector, trail *audit.Trail) *SecretScanHook {
    return &SecretScanHook{detector: detector, trail: trail}
}
```

메서드:

1. `PreToolUse(_ context.Context, _ string, _ json.RawMessage) (agent.HookResult, error)`
   - 항상 `agent.HookResult{Action: agent.HookAllow}, nil` 반환

2. `PostToolUse(_ context.Context, toolName string, _ json.RawMessage, result *tools.Result) error`
   - `result == nil || result.Output == ""` → nil
   - `d.detector == nil` → nil
   - `redacted, findings := d.detector.ScanAndRedact(result.Output)`
   - `len(findings) == 0` → nil
   - `result.Output = redacted` — in-place 수정
   - 각 finding에 대해:
     - `slog.Warn("secret redacted in tool output", "tool", toolName, "rule", finding.RuleID, "chars", finding.End-finding.Start)`
     - trail이 nil이 아니면:
       ```go
       d.trail.Log(audit.Event{
           Type:     audit.EventSecretRedacted,
           ToolName: toolName,
           RuleID:   finding.RuleID,
           Detail:   fmt.Sprintf("redacted %d chars", finding.End-finding.Start),
       })
       ```
   - nil 반환 (hook 에러로 tool 실행을 중단하지 않음)

### 작업 2: internal/secret/hook_test.go

테스트:
- PostToolUse: `result.Output`에 Anthropic key 포함 → 호출 후 `result.Output`에 `[REDACTED:anthropic-api-key]` 포함
- PostToolUse: secret 없는 output → `result.Output` 변경 없음
- PostToolUse: nil result → panic 없음, nil 반환
- PostToolUse: 빈 output → nil 반환
- PostToolUse: audit trail에 이벤트 기록 확인 (trail 파일 읽어서 JSON 파싱)
- PostToolUse: trail nil → 에러 없이 동작 (audit 안 함)
- PreToolUse: 항상 HookAllow

### 작업 3: internal/agent/executor.go — hook 순서 수정

**line 177-182 변경:**

기존:
```go
results[call.index] = toolExecResult{id: call.call.ID, output: result.Output, isError: result.IsError}
if a.hooks != nil {
    if hookErr := a.hooks.RunPostToolUse(childCtx, call.call.Name, call.call.Input, result); hookErr != nil {
        a.logger.Warn("post-tool hook error", "tool", call.call.Name, "error", hookErr)
    }
}
```

변경:
```go
if a.hooks != nil {
    if hookErr := a.hooks.RunPostToolUse(childCtx, call.call.Name, call.call.Input, result); hookErr != nil {
        a.logger.Warn("post-tool hook error", "tool", call.call.Name, "error", hookErr)
    }
}
results[call.index] = toolExecResult{id: call.call.ID, output: result.Output, isError: result.IsError}
```

PostToolUse hook이 result를 수정할 수 있도록, hook 실행을 result 복사 전에 배치한다.

### 작업 4: cmd/elnath/runtime.go — Secret hook + audit trail 등록

`buildExecutionRuntime` 함수에서, hook registry 생성 부분 근처에 추가:

1. import 추가:
```go
"github.com/stello/elnath/internal/audit"
"github.com/stello/elnath/internal/secret"
```

2. audit trail 생성:
```go
auditPath := filepath.Join(cfg.DataDir, "audit.jsonl")
auditTrail, err := audit.NewTrail(auditPath)
if err != nil {
    app.Logger.Warn("audit trail unavailable", "error", err)
}
```

3. secret hook 생성 및 등록 (hooks 변수가 이미 존재할 것이다):
```go
if auditTrail != nil {
    secretHook := secret.NewSecretScanHook(secret.NewDetector(), auditTrail)
    hooks.Add(secretHook)
}
```

4. executionRuntime struct에 `auditTrail *audit.Trail` 필드 추가. struct 초기화에 `auditTrail: auditTrail` 추가.

5. OnStop에 Close 등록:
```go
if auditTrail != nil {
    hooks.AddOnStop(func(_ context.Context) error {
        return auditTrail.Close()
    })
}
```

주의: `hooks` 변수가 어떻게 생성되는지 확인할 것. `buildHookRegistry(cfg.Hooks)` 호출 결과일 수 있다. 그 반환값에 `.Add(secretHook)` 호출.

만약 `buildHookRegistry`가 `*agent.HookRegistry`를 반환한다면 직접 `.Add()` 호출. `[]*CommandHook` 같은 다른 타입이라면 확인 필요.

runtime.go를 읽어서 정확한 hook 변수 타입과 생성 위치를 확인한 뒤 통합할 것.

### 검증

```bash
go test -race ./internal/secret/... ./internal/audit/... ./internal/agent/... ./cmd/elnath/...
go vet ./...
make build
```

모두 통과해야 한다.

executor.go 변경은 기존 hook 테스트 (internal/agent/hooks_test.go)에도 영향 없어야 한다. 기존 테스트가 여전히 통과하는지 확인.
```

---

## Phase 3: 통합 검증 + edge case

```
Phase D-1 Phase 3. Phase 1-2에서 secret detector, audit trail, hook, executor 수정, runtime 통합이 완료됐다.

### 작업 1: 통합 테스트 확인

전체 빌드 + 테스트:
```bash
go test -race ./...
go vet ./...
make build
```

모든 패키지 테스트가 통과해야 한다.

### 작업 2: edge case 확인

`internal/secret/detector_test.go`에 edge case 테스트 추가:

1. **stripe vs openai 구분**: `sk_live_abcdef...` (stripe) vs `sk-abcdef...` (openai). 각각 올바른 rule ID로 매치되는지 확인.

2. **겹치는 매치**: content에 `sk-proj-abcdefghijklmnopqrstuvwxyz` → openai-api-key와 openai-project-key 둘 다 매치 가능. 더 구체적인 `openai-project-key`가 우선해야 함 (longer match wins).

3. **큰 content**: 100KB 문자열 (secret 없음) → Scan이 빠르게 완료 (`testing.Short()` 아니면 실행).

4. **redact 후 content 길이 변화**: redact 결과가 올바른 텍스트인지 확인 (앞뒤 텍스트 보존).

5. **multiline .env**: 여러 줄에 걸친 .env 파일 내용에서 env-file-secret 규칙이 각 줄 매치.

### 작업 3: audit.jsonl 기록 확인

`cmd/elnath/runtime_test.go`에 테스트 추가 (가능하면):
- buildExecutionRuntime 호출 후 auditTrail이 non-nil인지 확인
- 또는 기존 테스트에서 audit.jsonl 파일이 생성되는지 확인

이 테스트가 어려우면 (runtime 의존성이 많아서) skip 가능.

### 전체 최종 검증

```bash
go test -race ./...
go vet ./...
make build
```

전부 통과 확인.
```
