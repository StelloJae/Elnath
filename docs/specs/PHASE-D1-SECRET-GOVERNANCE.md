# Phase D-1: Secret Governance + Audit Foundation

**Status:** SPEC READY  
**Predecessor:** Phase C-1 (Skill System) DONE  
**Successor:** Phase E (LB2 Magic Docs / LB5 Ambient Autonomy)  
**Branch:** `feat/telegram-redesign`  
**Ref:** Superiority Design v2.2 §Phase 4.2 — D2 Secret & Data Governance

---

## 1. Goal

Tool 출력에서 credential/secret을 탐지하여 redact한 뒤 LLM에 전달한다. 모든 보안 결정(secret 탐지, threat scan 차단, permission deny)을 구조화된 audit trail에 기록한다.

## 2. Architecture Overview

```
Tool.Execute() → Result{Output: "sk-ant-api03-xxxxx..."}
       │
       ▼
┌──────────────────┐
│ SecretScanHook   │ (PostToolUse hook)
│ .PostToolUse()   │
└──────┬───────────┘
       │ ScanContent(result.Output)
       ▼
┌──────────────────┐
│ SecretDetector    │
│ 20+ regex rules   │
│ + invisible char  │
└──────┬───────────┘
       │ findings → Redact(output, findings)
       ▼
┌──────────────────┐
│ result.Output =  │ "sk-ant-api03-[REDACTED]"
│ AuditTrail.Log() │ → audit.jsonl
└──────────────────┘
```

**핵심 결정:**
- **Hook 기반 통합**: 기존 `agent.Hook` 인터페이스를 구현하는 `SecretScanHook`을 만들어 `HookRegistry`에 등록. Agent 코드 변경 없이 PostToolUse에서 result.Output을 in-place redact.
- **In-place redaction**: `tools.Result.Output`을 직접 수정. Hook이 result 포인터를 받으므로 가능.
- **Audit = JSONL 파일**: 복잡한 DB 없이 `{dataDir}/audit.jsonl` 에 append-only 기록.

## 3. Deliverables

### 3.1 New Package: `internal/secret/`

#### `internal/secret/rules.go`

Gitleaks 패턴 기반 탐지 규칙 정의.

```go
package secret

import "regexp"

type Rule struct {
    ID      string         // "aws-access-key", "anthropic-api-key", etc.
    Name    string         // human-readable name
    Pattern *regexp.Regexp // compiled regex
}
```

**규칙 목록 (20개):**

| ID | Name | Pattern |
|----|------|---------|
| `anthropic-api-key` | Anthropic API Key | `sk-ant-api\d{2}-[\w-]{80,}` |
| `openai-api-key` | OpenAI API Key | `sk-[a-zA-Z0-9]{20,}` |
| `openai-project-key` | OpenAI Project Key | `sk-proj-[a-zA-Z0-9]{20,}` |
| `aws-access-key` | AWS Access Key | `AKIA[0-9A-Z]{16}` |
| `aws-secret-key` | AWS Secret Key | `(?i)aws_secret_access_key\s*[=:]\s*[A-Za-z0-9/+=]{40}` |
| `gcp-api-key` | GCP API Key | `AIza[0-9A-Za-z\-_]{35}` |
| `gcp-service-account` | GCP Service Account | `"type"\s*:\s*"service_account"` |
| `github-token` | GitHub Token | `gh[pousr]_[A-Za-z0-9_]{36,}` |
| `github-fine-grained` | GitHub Fine-Grained Token | `github_pat_[A-Za-z0-9_]{22,}` |
| `gitlab-token` | GitLab Token | `glpat-[A-Za-z0-9\-]{20,}` |
| `slack-token` | Slack Token | `xox[baprs]-[A-Za-z0-9\-]{10,}` |
| `slack-webhook` | Slack Webhook | `hooks\.slack\.com/services/T[A-Z0-9]+/B[A-Z0-9]+/[A-Za-z0-9]+` |
| `stripe-key` | Stripe Key | `[sr]k_(live\|test)_[A-Za-z0-9]{20,}` |
| `telegram-bot-token` | Telegram Bot Token | `\d{8,10}:[A-Za-z0-9_-]{35}` |
| `jwt-token` | JWT Token | `eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}` |
| `private-key-pem` | PEM Private Key | `-----BEGIN (?:RSA\|EC\|DSA\|OPENSSH)? ?PRIVATE KEY-----` |
| `generic-password` | Generic Password Assignment | `(?i)(?:password\|passwd\|pwd)\s*[=:]\s*["'][^"']{8,}["']` |
| `generic-secret` | Generic Secret Assignment | `(?i)(?:secret\|token\|api_key\|apikey)\s*[=:]\s*["'][^"']{8,}["']` |
| `connection-string` | Database Connection String | `(?i)(?:postgres\|mysql\|mongodb\|redis)://[^\s]+:[^\s]+@` |
| `env-file-secret` | .env File Secret | `(?i)^[A-Z_]*(KEY\|SECRET\|TOKEN\|PASSWORD\|CREDENTIAL)[A-Z_]*\s*=\s*\S{8,}` |

규칙은 package-level `var defaultRules []Rule`로 `init()`에서 `regexp.MustCompile`. 컴파일 실패 → panic (개발 시점에 잡힘).

#### `internal/secret/detector.go`

```go
// Finding represents a detected secret in content.
type Finding struct {
    RuleID string
    Match  string // the matched substring
    Start  int    // byte offset
    End    int    // byte offset
}

// Detector scans content for secrets using compiled regex rules.
type Detector struct {
    rules []Rule
}

// NewDetector creates a Detector with the default rule set.
func NewDetector() *Detector

// Scan checks content against all rules and returns findings.
func (d *Detector) Scan(content string) []Finding

// Redact replaces detected secrets in content with [REDACTED:<rule_id>].
func (d *Detector) Redact(content string, findings []Finding) string

// ScanAndRedact is a convenience that scans then redacts.
func (d *Detector) ScanAndRedact(content string) (redacted string, findings []Finding)
```

**Scan 로직:**
1. 각 rule의 Pattern으로 `FindAllStringIndex(content, -1)` 실행
2. 각 match를 Finding으로 수집
3. 겹치는 범위는 더 긴 match 우선 (sort by length desc, dedupe overlaps)

**Redact 로직:**
1. findings를 Start 기준 역순 정렬 (뒤에서부터 치환해야 offset 안 깨짐)
2. 각 finding: `content[start:end]` → `[REDACTED:<rule_id>]`

**ScanAndRedact:** `findings := d.Scan(content)` → `redacted := d.Redact(content, findings)` → return both.

#### `internal/secret/detector_test.go`

테이블 기반 테스트:

**Scan 테스트:**
- Anthropic key: `"sk-ant-api03-" + 80자 alphanumeric` → findings 1개, ruleID="anthropic-api-key"
- OpenAI key: `"sk-abcdefghijklmnopqrstuvwxyz"` → findings 1개
- AWS key: `"AKIAIOSFODNN7EXAMPLE"` → findings 1개
- GitHub token: `"ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef1234"` → findings 1개
- PEM key: `"-----BEGIN RSA PRIVATE KEY-----"` → findings 1개
- JWT: `"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123def456"` → findings 1개
- 정상 코드 (secret 없음) → findings 0개
- 복합: 한 content에 2개 secret → findings 2개

**Redact 테스트:**
- `"key=sk-ant-api03-xxx..."` → `"key=[REDACTED:anthropic-api-key]"`
- 2개 secret → 둘 다 redact
- secret 없음 → 원본 그대로

**ScanAndRedact 테스트:**
- secret 포함 content → redacted string + findings 반환
- 빈 content → "", nil

### 3.2 New Package: `internal/audit/`

#### `internal/audit/event.go`

```go
package audit

import "time"

// EventType classifies audit events.
type EventType string

const (
    EventSecretDetected    EventType = "secret_detected"
    EventSecretRedacted    EventType = "secret_redacted"
    EventInjectionBlocked  EventType = "injection_blocked"
    EventPermissionDenied  EventType = "permission_denied"
    EventPermissionGranted EventType = "permission_granted"
    EventSkillExecuted     EventType = "skill_executed"
)

// Event is a single audit record.
type Event struct {
    Timestamp time.Time `json:"timestamp"`
    Type      EventType `json:"type"`
    SessionID string    `json:"session_id,omitempty"`
    ToolName  string    `json:"tool_name,omitempty"`
    RuleID    string    `json:"rule_id,omitempty"`
    Detail    string    `json:"detail,omitempty"`
}
```

#### `internal/audit/trail.go`

```go
// Trail appends security events to a JSONL file.
type Trail struct {
    mu   sync.Mutex
    file *os.File
    enc  *json.Encoder
}

// NewTrail opens (or creates) the audit file at path.
func NewTrail(path string) (*Trail, error)

// Log writes an event. Safe for concurrent use.
func (t *Trail) Log(event Event) error

// Close flushes and closes the file.
func (t *Trail) Close() error
```

**NewTrail:**
- `os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)` — 0o600 (owner-only)
- `json.NewEncoder(file)` 저장

**Log:**
- `t.mu.Lock()` / `defer Unlock()`
- `event.Timestamp`가 zero면 `time.Now().UTC()` 설정
- `t.enc.Encode(event)` — JSONL (줄바꿈 자동)

#### `internal/audit/trail_test.go`

- NewTrail: temp file 생성 → 파일 존재 확인
- Log: 이벤트 기록 → 파일에 JSON 1줄 확인
- Log: 2개 이벤트 → 2줄
- Log: Timestamp 없으면 자동 설정
- Close: 파일 닫힘 확인
- 동시성: 10 goroutine에서 각 10개 이벤트 → 100줄 정확히 기록 (race test)

### 3.3 New Hook: `internal/secret/hook.go`

기존 `agent.Hook` 인터페이스를 구현하는 PostToolUse hook.

```go
// SecretScanHook scans tool outputs for secrets and redacts them.
type SecretScanHook struct {
    detector *Detector
    trail    *audit.Trail  // nil이면 audit 안 함
    session  string        // current session ID for audit events
}

func NewSecretScanHook(detector *Detector, trail *audit.Trail, sessionID string) *SecretScanHook

// PreToolUse always allows — secret scanning is post-execution.
func (h *SecretScanHook) PreToolUse(_ context.Context, _ string, _ json.RawMessage) (agent.HookResult, error) {
    return agent.HookResult{Action: agent.HookAllow}, nil
}

// PostToolUse scans result.Output, redacts secrets, and logs findings.
func (h *SecretScanHook) PostToolUse(_ context.Context, toolName string, _ json.RawMessage, result *tools.Result) error
```

**PostToolUse 로직:**
1. `result == nil || result.Output == ""` → return nil
2. `h.detector.ScanAndRedact(result.Output)` → redacted, findings
3. findings 비어있으면 → return nil
4. `result.Output = redacted` — in-place 수정
5. 각 finding에 대해 audit 이벤트 기록:
   ```go
   h.trail.Log(audit.Event{
       Type:      audit.EventSecretRedacted,
       SessionID: h.session,
       ToolName:  toolName,
       RuleID:    finding.RuleID,
       Detail:    fmt.Sprintf("redacted %d chars", finding.End-finding.Start),
   })
   ```
6. `slog.Warn("secret redacted in tool output", "tool", toolName, "rule", finding.RuleID)`

#### `internal/secret/hook_test.go`

- PostToolUse: secret 포함 output → redacted
- PostToolUse: secret 없음 → output 변경 없음
- PostToolUse: nil result → no panic
- PostToolUse: audit trail에 이벤트 기록 확인
- PreToolUse: 항상 Allow

### 3.4 Modified Files

#### `cmd/elnath/runtime.go` — Hook 등록

`buildExecutionRuntime`에서:

1. Audit trail 생성:
```go
auditPath := filepath.Join(cfg.DataDir, "audit.jsonl")
auditTrail, err := audit.NewTrail(auditPath)
if err != nil {
    app.Logger.Warn("audit trail unavailable", "error", err)
}
```

2. Secret detector + hook 생성 및 HookRegistry에 등록:
```go
detector := secret.NewDetector()
if auditTrail != nil {
    secretHook := secret.NewSecretScanHook(detector, auditTrail, "")
    hooks.Add(secretHook)
}
```

3. `executionRuntime`에 `auditTrail *audit.Trail` 필드 추가.

4. `runTask`에서 session ID를 secret hook에 전달하는 방법:
   - SecretScanHook에 `SetSessionID(id string)` 메서드 추가
   - `runTask` 시작부에서 `secretHook.SetSessionID(sess.ID)` 호출
   - 또는 hook 생성 시 session 미지정 → PostToolUse에서 빈 session 기록 (간단. session은 audit에서 선택적)

**간단한 접근:** session ID 없이 시작. audit 이벤트에 session이 빈 문자열이어도 rule ID + tool name으로 충분히 유용.

5. OnStop hook에 audit trail Close 등록:
```go
if auditTrail != nil {
    hooks.AddOnStop(func(_ context.Context) error {
        return auditTrail.Close()
    })
}
```

#### `cmd/elnath/runtime.go` — threat_scan audit 통합 (선택)

기존 `internal/prompt/threat_scan.go`의 `ScanContent`에서 차단 시 audit 기록. 하지만 threat_scan은 prompt 패키지에 있고 audit 의존성을 추가하면 순환 참조 위험. **Phase D-1에서는 생략. 향후 콜백 패턴으로 해결.**

## 4. File Summary

### New Files (8)

| File | LOC (est.) | 역할 |
|------|-----------|------|
| `internal/secret/rules.go` | ~120 | 20개 regex 규칙 정의 |
| `internal/secret/detector.go` | ~100 | Scan + Redact + ScanAndRedact |
| `internal/secret/detector_test.go` | ~180 | 규칙별 탐지 + redact 테스트 |
| `internal/secret/hook.go` | ~60 | agent.Hook 구현 (PostToolUse redact) |
| `internal/secret/hook_test.go` | ~80 | hook 동작 테스트 |
| `internal/audit/event.go` | ~30 | Event struct + EventType constants |
| `internal/audit/trail.go` | ~60 | JSONL append-only audit file |
| `internal/audit/trail_test.go` | ~100 | 기록/동시성/close 테스트 |

### Modified Files (1)

| File | 변경 내용 |
|------|----------|
| `cmd/elnath/runtime.go` | audit trail 생성, secret hook 등록, OnStop close |

## 5. Acceptance Criteria

- [ ] `go test -race ./internal/secret/...` — 모든 테스트 통과
- [ ] `go test -race ./internal/audit/...` — 모든 테스트 통과
- [ ] `go test -race ./cmd/elnath/...` — runtime 테스트 통과
- [ ] `go vet ./...` — 경고 없음
- [ ] `make build` — 빌드 성공
- [ ] Tool output에 Anthropic API key → `[REDACTED:anthropic-api-key]` 로 치환
- [ ] `{dataDir}/audit.jsonl`에 redact 이벤트 기록
- [ ] Secret 없는 tool output → 변경 없음 (성능 영향 최소)
- [ ] 20개 규칙 각각 테스트 커버리지

## 6. Out of Scope

- SHA256 content hashing / ETag (Superiority Design의 full TeamMem 패턴. 현재는 regex scan만으로 충분)
- Symlink validation (PathGuard가 이미 write-deny 모델로 보호)
- Audit → wiki 자동 ingest (Phase E+)
- threat_scan.go의 audit 통합 (순환 참조 회피 필요. 콜백 패턴으로 향후 해결)
- Permission granted/denied audit (permission.go 수정 필요. 별도 Phase)

## 7. Risk

| Risk | Mitigation |
|------|-----------|
| Regex false positive (e.g., `sk-` 로 시작하는 일반 문자열) | 규칙별 최소 길이 제한. `openai-api-key`는 20자 이상만 매치 |
| Tool output이 큰 경우 성능 | 20개 regex는 Go의 RE2 엔진으로 빠름. 100KB output도 <1ms |
| JSONL 파일 무한 성장 | Phase E에서 rotation/cleanup 추가. 현재는 수동 관리 |
| Hook 에러가 tool 실행 차단 | PostToolUse hook은 에러 시 slog.Warn만. 차단하지 않음 (executor.go:179) |
