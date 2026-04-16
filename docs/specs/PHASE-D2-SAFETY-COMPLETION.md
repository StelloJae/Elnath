# Phase D-2: Safety Completion — Injection Scanner + Content Boundary

**Status:** SPEC READY
**Predecessor:** D-1 (Secret Governance) DONE
**Successor:** LB1 Greenfield path

---

## 1. Goal

D-1에서 구축한 secret redaction + audit 위에, prompt injection scanner를 모든 입출력 표면에 적용하고 Telegram 출력에서 secret leakage를 방지한다.

## 2. 현재 상태

| 표면 | Secret 스캔 | Injection 스캔 | 변경 |
|------|------------|---------------|------|
| Context files (CLAUDE.md) | ❌ | ✅ ScanContent | 유지 |
| Tool output (bash 등) | ✅ SecretScanHook | ❌ | **injection 추가** |
| Wiki RAG 결과 | ❌ | ❌ | **injection 추가** |
| MCP tool 결과 | ✅ SecretScanHook | ❌ | **injection 추가** |
| Telegram 출력 | ❌ | ❌ | **secret redaction 추가** |
| Telegram 입력 | ❌ | ❌ | 단일 유저, 생략 |

## 3. Design

### 3.1 Tool Output Injection Scan

SecretScanHook의 `PostToolUse()`에 injection scan 추가. 이미 모든 tool output이 이 hook을 통과하므로 hook 등록 변경 없음.

```go
// internal/secret/hook.go — PostToolUse 내부
func (h *SecretScanHook) PostToolUse(ctx context.Context, toolName string, params json.RawMessage, result *tools.Result) error {
    // 기존: secret redaction
    redacted, findings := h.detector.ScanAndRedact(result.Output)
    // ... audit logging ...

    // 추가: injection scan
    cleaned, blocked := prompt.ScanContent(redacted, "tool:"+toolName)
    if blocked {
        result.Output = cleaned
        // audit log: injection blocked
    }

    return nil
}
```

MCP tool 결과도 동일 경로를 통과하므로 자동 커버.

### 3.2 Wiki RAG Injection Scan

`internal/wiki/rag.go`에서 RAG 결과를 반환하기 전에 ScanContent 적용.

```go
// rag.go — search 결과 각 page content에 ScanContent 적용
for i, result := range results {
    cleaned, blocked := prompt.ScanContent(result.Content, "wiki:"+result.Path)
    if blocked {
        results[i].Content = cleaned
    }
}
```

### 3.3 Telegram Output Secret Redaction

`internal/telegram/sink.go`에서 메시지를 Telegram으로 보내기 전에 secret redaction.

```go
// sink.go — send 직전
func (s *Sink) redactSecrets(text string) string {
    redacted, _ := s.detector.ScanAndRedact(text)
    return redacted
}
```

Detector는 Sink 생성 시 주입. `cmd/elnath/cmd_telegram.go`에서 NewSink에 전달.

### 3.4 Audit 확장

기존 `audit.Trail`에 injection 이벤트 추가:

```go
type EventType string
const (
    EventSecretRedacted    EventType = "secret_redacted"    // 기존
    EventInjectionBlocked  EventType = "injection_blocked"  // 추가
)
```

## 4. 변경 파일

| 파일 | 변경 | LOC |
|------|------|-----|
| `internal/secret/hook.go` | PostToolUse에 injection scan 추가 | ~10 |
| `internal/secret/hook_test.go` | injection 차단 테스트 추가 | ~30 |
| `internal/wiki/rag.go` | search 결과 ScanContent 적용 | ~10 |
| `internal/wiki/rag_test.go` | injection 차단 테스트 추가 | ~20 |
| `internal/telegram/sink.go` | send 전 secret redaction | ~15 |
| `internal/telegram/sink_test.go` | redaction 테스트 추가 | ~20 |
| `cmd/elnath/cmd_telegram.go` | Detector를 Sink에 주입 | ~5 |
| `internal/audit/event.go` | EventInjectionBlocked 상수 추가 | ~2 |

**총 ~110 LOC** (테스트 포함). 신규 파일 없음, 기존 파일 수정만.

## 5. Acceptance Criteria

- [ ] Tool output에 "ignore all instructions" 포함 시 [BLOCKED] 처리
- [ ] Wiki RAG 결과에 injection 포함 시 [BLOCKED] 처리
- [ ] MCP tool 결과도 동일 경로로 injection 차단 (SecretScanHook 통과)
- [ ] Telegram 출력에 API key 포함 시 [REDACTED] 처리
- [ ] Audit trail에 injection_blocked 이벤트 기록
- [ ] 기존 secret redaction regression 없음
- [ ] `go test -race ./internal/secret/... ./internal/wiki/... ./internal/telegram/...` PASS

## 6. Out of Scope

- Telegram 입력 injection scan (단일 유저, 위험 낮음)
- LLM 기반 injection 탐지 (regex 한계 인정, 나중에 업그레이드)
- Multi-turn manipulation 탐지 (대화 흐름 분석 필요, 현재 범위 아님)
- 추가 regex 패턴 (필요 시 rules.go에 한 줄 추가로 해결)
