# Phase F-2.5: Lesson Topic / Text Redaction

**Status:** SPEC READY
**Predecessor:** Phase F-2 (Agent-task Lesson Extraction) DONE
**Successor:** Phase F-3 (Multi-workflow Learning Integration)
**Branch:** `feat/telegram-redesign`
**Type:** Security hardening — blocks F-3 expansion

---

## 1. Goal

F-2 에서 `firstMessageSnippet(input.Message, 80)` 가 user 입력 첫 80자를 lesson `Topic` 에 저장하게 만들었다. 사용자가 prompt 에 API key / token / password 를 paste 하면 그 앞부분이 `lessons.jsonl` 에 영구 저장된다 (0o600 이지만 backup/sync 대상이면 유출 확대).

F-3 로 Team/Ralph/Autopilot 까지 확장하면 모든 workflow 가 같은 경로를 탄다. **F-3 전에 막아야** 노출면이 최소.

이번 phase 는 `internal/learning/Store` 에 optional redactor 를 붙여 **저장 직전에 secret 패턴을 `[REDACTED:rule-id]` 로 치환**한다. `internal/secret/Detector` 의 `ScanAndRedact` 를 그대로 활용.

**Defense-in-depth 의도:**
Topic 레벨 (single.go) 에서 막지 않고 Store 레벨에서 막는다. 이유:
- research 경로 (E-3) 도 자동 커버
- 미래 새 extractor 가 추가돼도 자동 적용
- 1개 지점에서 enforce → 누락 위험 없음

**Out of scope:**
- Agent prompt 자체의 redaction (prompt node 레벨은 별도 문제 — session JSONL 에 원본이 남음). 이번엔 lesson 저장만.
- 사용자 정의 규칙 추가 (default rules 그대로 사용)
- Redaction audit logging (secret hook 은 별도 존재)
- Retrospective scan (기존 lessons.jsonl 정화 — 사용자가 `lessons clear` 로 수동 처리)

## 2. Architecture

```
┌──────────────────────────────────┐
│ ExtractAgent / research extractor│
└─────────┬────────────────────────┘
          │ Lesson{Topic, Text, Source, PersonaDelta}
          ▼
┌──────────────────────────────────┐
│ learning.Store.Append            │
│   (new) if redactor != nil       │
│       lesson.Topic = redact      │
│       lesson.Text  = redact      │
│       lesson.Source = redact     │
└─────────┬────────────────────────┘
          │ JSONL encode
          ▼
      lessons.jsonl
```

**설계 결정:**

1. **Function-type interface** — `learning.Redactor func(string) string`. 가벼운 함수형 인터페이스. DI 편함.
2. **Option pattern** — `NewStore(path, opts ...StoreOption)`. `WithRedactor(Redactor)`. 기존 `NewStore(path)` 시그니처 **유지** (backwards compat: variadic 이라 기존 호출자 깨지지 않음).
3. **Redact order** — Scan 결과가 offset 기반이므로 Topic/Text/Source 각각 독립 호출. Detector 의 ScanAndRedact 가 ordered replace 수행.
4. **Nil redactor = no-op** — 테스트나 benchmark 에서 nil 주입 가능. 기존 동작 보장.
5. **ID 재계산 순서** — `deriveID(lesson.Text)` 는 Append 의 나중 단계에서 수행되는데, redact 를 **먼저** 수행해야 ID 가 redacted text 의 hash 가 된다. 이렇게 해야 같은 secret 이 들어오더라도 ID 는 동일 (redacted 결과가 같으므로).

## 3. Deliverables

### 3.1 Modified: `internal/learning/store.go`

기존 `type Store struct` 에 필드 추가:

```go
type Store struct {
    mu       sync.Mutex
    path     string
    redactor Redactor // optional
}

// Redactor sanitizes strings before persistence. Returning the input unchanged
// is valid. Nil Redactors are treated as identity.
type Redactor func(string) string

// StoreOption configures a Store at construction time.
type StoreOption func(*Store)

// WithRedactor attaches a redactor that runs over Lesson.Topic, Text, and Source
// at Append time.
func WithRedactor(r Redactor) StoreOption {
    return func(s *Store) { s.redactor = r }
}
```

생성자 확장 (**기존 시그니처 유지**, variadic option 추가):

```go
func NewStore(path string, opts ...StoreOption) *Store {
    s := &Store{path: path}
    for _, opt := range opts {
        opt(s)
    }
    return s
}
```

`Append` 수정 — 기존 로직 초입에 redact 단계 추가, **ID 유도 이전에** 수행:

```go
func (s *Store) Append(lesson Lesson) error {
    if s == nil || s.path == "" {
        return nil
    }

    s.mu.Lock()
    defer s.mu.Unlock()

    if s.redactor != nil {
        lesson.Text = s.redactor(lesson.Text)
        lesson.Topic = s.redactor(lesson.Topic)
        lesson.Source = s.redactor(lesson.Source)
    }

    if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
        return fmt.Errorf("learning store: create dir: %w", err)
    }
    if lesson.Created.IsZero() {
        lesson.Created = time.Now().UTC()
    }
    if lesson.ID == "" {
        lesson.ID = deriveID(lesson.Text)
    }
    // ... existing file open / encode / close logic unchanged ...
}
```

**중요:** `Rotate` 의 archive append 경로에는 redactor 적용하지 않음. 이유: archive 로 이동되는 lesson 은 이미 **저장 시점에 redact 된 상태**. 재적용 불필요.

### 3.2 New: `internal/secret/redactor.go` (또는 `detector.go` 에 추가)

`secret.Detector` 에 `learning.Redactor` 시그니처와 호환되는 메서드 추가:

```go
// RedactString returns content with detected secrets replaced by [REDACTED:rule-id].
// Equivalent to calling ScanAndRedact and discarding findings.
func (d *Detector) RedactString(content string) string {
    if d == nil {
        return content
    }
    redacted, _ := d.ScanAndRedact(content)
    return redacted
}
```

**주의:** 기존 `Redact(content, findings)` 는 2-arg. 유지. `RedactString` 은 새 이름으로 충돌 피함.

### 3.3 Modified: `cmd/elnath/runtime.go`

`learning.NewStore` 호출 지점에 redactor 주입.

```go
// (기존) learningStore := learning.NewStore(learningPath)

detector := secret.NewDetector()
learningStore := learning.NewStore(
    learningPath,
    learning.WithRedactor(detector.RedactString),
)
```

detector 는 runtime 당 1개 인스턴스면 충분 (Scan 은 stateless regex). `secret` 패키지 import 추가.

### 3.4 Modified: `cmd/elnath/cmd_daemon.go`

`runtime.go` 와 동일 패턴. 같은 `detector` 를 만들거나 runtime helper 를 공유. 단순하게 각 지점에서 `secret.NewDetector()` 호출해도 부담 없음 (default rules 정적).

### 3.5 Modified: `internal/learning/store_test.go`

신규 서브 테스트:

1. `TestStoreAppend_WithRedactor`
   - 가짜 redactor: `func(s string) string { return strings.ReplaceAll(s, "SECRET", "[X]") }`
   - `Lesson{Text:"contains SECRET token", Topic:"SECRET-topic", Source:"SECRET-source"}` append
   - List → 모든 필드에 "SECRET" 없고 "[X]" 있음. ID 는 redacted Text 의 hash

2. `TestStoreAppend_WithRedactor_IDConsistency`
   - 같은 원본 Text 를 다른 세션에서 append → 같은 ID (redacted 이후 hash 이므로 안정적)

3. `TestStoreAppend_NilRedactor`
   - `NewStore(path)` (옵션 없음) → 기존 동작 그대로, "SECRET" 문자열 그대로 저장

4. `TestStoreAppend_RedactorNotAppliedToArchive`
   - redactor 있는 store 에 1개 append → redact 된 상태 저장
   - Rotate(KeepLast:0) → archive 로 이동
   - archive 파일 읽어 같은 redact 된 content 유지 (재적용 없음)

5. 동시성 보강: 기존 concurrent test 와 동일하게 redactor 주입한 버전 한 번 더 → race 통과

### 3.6 Modified: `internal/secret/detector_test.go`

`TestDetectorRedactString` 1 케이스 추가:
- 빈 문자열 → 빈 문자열
- secret 없는 문자열 → 그대로
- AWS key 포함 문자열 → `[REDACTED:aws-access-key]` 치환

### 3.7 New/Modified: 통합 smoke test

`cmd/elnath/runtime_test.go` 에 1 케이스:

`TestExecutionRuntimeSingleWorkflowRedactsTopic`
- Runtime 을 생성, learning store 에 redactor 주입 확인
- Mock provider 가 "Here is key AKIAIOSFODNN7EXAMPLE please use" 같은 input 으로 agent 호출
- Rule C (efficient stop) trigger 되도록 설정
- `lessons.jsonl` 읽기 → Topic 에 "AKIAIOSFODNN7EXAMPLE" 없고 `[REDACTED:aws-access-key]` 존재

(실제 rule 이름은 `internal/secret/rules.go` 의 defaultRules 확인 후 맞추기)

## 4. File Summary

### Modified (5)

| File | 변경 |
|------|------|
| `internal/learning/store.go` | `Redactor` type, `StoreOption`, `WithRedactor`, `NewStore` variadic, Append redact 단계 |
| `internal/learning/store_test.go` | 5 신규 서브 테스트 |
| `internal/secret/detector.go` | `RedactString(string) string` 메서드 |
| `internal/secret/detector_test.go` | `TestDetectorRedactString` |
| `cmd/elnath/runtime.go` | `secret.NewDetector()` + `learning.WithRedactor` 주입 |
| `cmd/elnath/cmd_daemon.go` | 동일 패턴 |
| `cmd/elnath/runtime_test.go` | e2e smoke 1 케이스 |

Total LOC 추정: ~150 (core) + ~150 (tests) ≈ **~300 LOC**.

## 5. Acceptance Criteria

- [ ] `go test -race ./internal/learning/... ./internal/secret/... ./cmd/elnath/...` 통과
- [ ] `go vet ./...` 경고 없음
- [ ] `make build` 성공
- [ ] 기존 `learning.NewStore(path)` 호출자 회귀 없음 (variadic option 덕분)
- [ ] Redactor 주입 후 Append 한 lesson 의 Topic/Text/Source 에 default rules 매칭 secret 없음
- [ ] Nil redactor 는 기존 동작과 동일
- [ ] Rotate 후 archive 파일이 재-redact 되지 않음 (원래 redact 된 상태 유지)
- [ ] runtime 과 daemon 둘 다 redactor 주입
- [ ] research 경로도 자동 커버 (store 레벨 적용 확인)

## 6. Risk

| Risk | Mitigation |
|------|-----------|
| False positive redaction (정상 문자열을 secret 으로 오인) | `defaultRules` 는 보수적. 사용자가 문제 발견 시 `lessons clear --id X` 로 정리 가능 |
| Default rules 가 놓치는 secret | 이번 phase 는 default 만. 사용자 정의 규칙은 추후 (secret 패키지 확장) |
| Redactor 가 Append hot path 에 오버헤드 | 정규식 스캔 짧은 문자열 (Topic 80자, Text 200자) → μs 단위. 무시 가능 |
| ID 충돌 (다른 secret 이 같은 redact 결과) | SHA256 8자 prefix 는 여전히 32bit 엔트로피. 충돌 확률 기존과 동일 |
| Rotate archive 에 이미 저장된 과거 lesson 의 secret | Scope 외. 사용자가 수동으로 `awk` / `jq` 로 정화 가능. `elnath lessons rotate` 가 재처리 옵션은 별도 phase |
| 새 rule 추가 시 이미 저장된 lesson 은 redact 안 됨 | Retrospective redact 는 out of scope. 기존 lesson 정리는 사용자 책임 |

## 7. Future Work

- **Retrospective redaction:** `elnath lessons rescan` 커맨드로 기존 전체 lesson 재-redact
- **User-defined rules:** `config.yaml` 에 secret pattern 추가 가능
- **Session JSONL redaction:** 본 phase 는 lesson 만. 대화 세션 JSONL 도 secret 포함 가능 (별도 phase)
- **Audit trail:** 몇 개 secret 이 redact 됐는지 `audit.Event` 로 기록

---

## Appendix A. 예시

**Input lesson (agent extractor 가 생성):**
```json
{
  "text": "Efficient completion on fix bug in AKIAIOSFODNN7EXAMPLE integration: 12/50 iterations",
  "topic": "fix bug in AKIAIOSFODNN7EXAMPLE integration",
  "source": "agent",
  "confidence": "high"
}
```

**After redactor (Append 시):**
```json
{
  "text": "Efficient completion on fix bug in [REDACTED:aws-access-key] integration: 12/50 iterations",
  "topic": "fix bug in [REDACTED:aws-access-key] integration",
  "source": "agent",
  "confidence": "high",
  "id": "<hash of redacted text>"
}
```
