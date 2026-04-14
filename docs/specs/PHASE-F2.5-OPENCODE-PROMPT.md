# OpenCode Delegation Prompt: Phase F-2.5 Lesson Redaction

대상 spec: `docs/specs/PHASE-F2.5-TOPIC-REDACTION.md`

**단일 phase** (scope 작음, 2 step 불필요). 완료 후 `go test -race ./... && go vet ./... && make build` 게이트.

**원칙:**

- 기존 `learning.NewStore(path)` 시그니처는 **깨지 않는다**. variadic option 으로 확장만.
- `secret.Detector.Redact(content, findings)` (2-arg, 기존) 은 건드리지 않는다. `RedactString` 은 신규 이름.
- 테스트는 real file I/O + table-driven.
- research 경로 자동 커버 여부를 테스트로 증명 (store 레벨 적용이므로 자연히 따라옴).
- 커밋 스테이지는 지정한 파일만. Phase F-2 와 무관한 변경 섞지 말 것.

---

## 작업 블록

```
elnath 프로젝트 (`/Users/stello/elnath/`, feat/telegram-redesign 브랜치) 에서 Phase F-2.5 를 시작한다.

목표: `internal/learning/Store` 에 optional Redactor 를 붙여 lesson 저장 직전에 secret 을 `[REDACTED:rule-id]` 로 치환. `internal/secret/Detector` 가 이미 `ScanAndRedact(string) (string, []Finding)` 를 제공하므로 그 위에 thin adapter 만 얹는다.

defense-in-depth: agent path / research path 둘 다 같은 Store 를 쓰므로 store-level 적용 하나로 전 경로 커버.

### 사전 확인 (꼭 파일 read)

- `internal/learning/store.go` — 현재 NewStore/Append 구조
- `internal/learning/store_test.go` — 테스트 패턴
- `internal/secret/detector.go` — Detector, Scan, Redact, ScanAndRedact 시그니처
- `internal/secret/rules.go` — defaultRules 의 RuleID 이름 (테스트에서 정확한 이름 필요)
- `cmd/elnath/runtime.go` — learning.NewStore 호출 지점
- `cmd/elnath/cmd_daemon.go` — 동일
- F-2 에서 만든 `internal/orchestrator/single.go` — Topic 생성 경로 (이번엔 여기 건드리지 않지만 영향 이해용)

### 작업 1: internal/learning/store.go

변경점:

1. 타입 추가 (파일 상단, `Store` 근처):

```go
type Redactor func(string) string

type StoreOption func(*Store)

func WithRedactor(r Redactor) StoreOption {
    return func(s *Store) { s.redactor = r }
}
```

2. `Store` struct 에 필드:

```go
type Store struct {
    mu       sync.Mutex
    path     string
    redactor Redactor // optional
}
```

3. `NewStore` variadic 확장:

```go
func NewStore(path string, opts ...StoreOption) *Store {
    s := &Store{path: path}
    for _, opt := range opts {
        opt(s)
    }
    return s
}
```

기존 호출자 `learning.NewStore(path)` 는 그대로 동작.

4. `Append` 에 redact 단계 추가. **반드시 lock 잡은 뒤, ID derive 전**:

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

    // ... 기존 MkdirAll / Created / ID derive / OpenFile / Encode / Close 로직 그대로 ...
}
```

**주의:** Rotate 의 `appendArchiveLocked` 경로에는 redactor 적용 안 함 (이미 active 에서 redact 된 상태). 명시적으로 readAllLocked → archive append 는 bypass.

### 작업 2: internal/secret/detector.go 에 메서드 추가

기존 `Redact(content, findings)` 2-arg 은 건드리지 않는다. 아래 신규 메서드만 추가:

```go
// RedactString returns content with every detected secret replaced by
// [REDACTED:rule-id]. Empty or secret-free input is returned unchanged.
// Nil-safe.
func (d *Detector) RedactString(content string) string {
    if d == nil {
        return content
    }
    redacted, _ := d.ScanAndRedact(content)
    return redacted
}
```

### 작업 3: cmd/elnath/runtime.go

learning store 생성 지점을 찾아 (F-1 에서 이미 추가된 코드) redactor 주입:

변경 전:
```go
learningStore := learning.NewStore(learningPath)
```

변경 후:
```go
learningDetector := secret.NewDetector()
learningStore := learning.NewStore(
    learningPath,
    learning.WithRedactor(learningDetector.RedactString),
)
```

`secret` 패키지 import 추가. 기존 import 구문과 정렬.

### 작업 4: cmd/elnath/cmd_daemon.go

runtime.go 와 동일 패턴. 두 지점이 같은 pattern 으로 가도록. `secret.NewDetector()` 는 호출 당 새 인스턴스 허용 (defaultRules 는 정적 slice 라 오버헤드 없음).

### 작업 5: internal/learning/store_test.go — 5 케이스 추가

```go
func TestStoreAppend_WithRedactor(t *testing.T) {
    t.Run("topic text source all redacted", func(t *testing.T) {
        dir := t.TempDir()
        path := filepath.Join(dir, "lessons.jsonl")
        redactor := func(s string) string {
            return strings.ReplaceAll(s, "SECRET", "[X]")
        }
        store := learning.NewStore(path, learning.WithRedactor(redactor))

        err := store.Append(learning.Lesson{
            Text:   "contains SECRET token",
            Topic:  "SECRET-topic",
            Source: "SECRET-source",
        })
        if err != nil { t.Fatal(err) }

        got, err := store.List()
        if err != nil { t.Fatal(err) }
        if len(got) != 1 { t.Fatalf("want 1, got %d", len(got)) }
        if strings.Contains(got[0].Text, "SECRET") {
            t.Errorf("Text not redacted: %q", got[0].Text)
        }
        if strings.Contains(got[0].Topic, "SECRET") {
            t.Errorf("Topic not redacted: %q", got[0].Topic)
        }
        if strings.Contains(got[0].Source, "SECRET") {
            t.Errorf("Source not redacted: %q", got[0].Source)
        }
        if !strings.Contains(got[0].Text, "[X]") {
            t.Errorf("Text should contain [X], got %q", got[0].Text)
        }
    })

    t.Run("id derived from redacted text is stable", func(t *testing.T) {
        dir := t.TempDir()
        path := filepath.Join(dir, "lessons.jsonl")
        redactor := func(s string) string {
            return strings.ReplaceAll(s, "TOKEN-", "[X]-")
        }
        store := learning.NewStore(path, learning.WithRedactor(redactor))

        l1 := learning.Lesson{Text: "uses TOKEN-aaa"}
        l2 := learning.Lesson{Text: "uses TOKEN-bbb"}
        // 같은 redact 결과 아님. 다른 ID 기대.
        if err := store.Append(l1); err != nil { t.Fatal(err) }
        if err := store.Append(l2); err != nil { t.Fatal(err) }

        got, _ := store.List()
        if got[0].ID == got[1].ID {
            t.Errorf("expected different IDs, both = %s", got[0].ID)
        }
        if strings.Contains(got[0].Text, "TOKEN-") || strings.Contains(got[1].Text, "TOKEN-") {
            t.Errorf("TOKEN- remained in stored text")
        }
    })
}

func TestStoreAppend_NilRedactor(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "lessons.jsonl")
    store := learning.NewStore(path) // 옵션 없음

    err := store.Append(learning.Lesson{Text: "keeps SECRET literal"})
    if err != nil { t.Fatal(err) }

    got, _ := store.List()
    if !strings.Contains(got[0].Text, "SECRET") {
        t.Errorf("expected literal retained when no redactor")
    }
}

func TestStoreRotate_ArchiveDoesNotReRedact(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "lessons.jsonl")
    calls := 0
    redactor := func(s string) string {
        calls++
        return strings.ReplaceAll(s, "Z", "z")
    }
    store := learning.NewStore(path, learning.WithRedactor(redactor))

    for i := 0; i < 3; i++ {
        _ = store.Append(learning.Lesson{
            Text:    fmt.Sprintf("entry %d with Z", i),
            Created: time.Now().Add(time.Duration(i) * time.Second),
        })
    }
    callsAfterAppend := calls

    n, err := store.Rotate(learning.RotateOpts{KeepLast: 1})
    if err != nil { t.Fatal(err) }
    if n != 2 { t.Fatalf("rotate moved %d, want 2", n) }
    if calls != callsAfterAppend {
        t.Errorf("redactor called during rotate: before=%d after=%d", callsAfterAppend, calls)
    }
}

func TestStoreAppend_WithRedactor_Concurrent(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "lessons.jsonl")
    var mu sync.Mutex
    var calls int
    redactor := func(s string) string {
        mu.Lock()
        calls++
        mu.Unlock()
        return strings.ReplaceAll(s, "S", "s")
    }
    store := learning.NewStore(path, learning.WithRedactor(redactor))

    var wg sync.WaitGroup
    for g := 0; g < 10; g++ {
        wg.Add(1)
        go func(gID int) {
            defer wg.Done()
            for i := 0; i < 5; i++ {
                _ = store.Append(learning.Lesson{Text: fmt.Sprintf("g=%d i=%d S", gID, i)})
            }
        }(g)
    }
    wg.Wait()

    got, _ := store.List()
    if len(got) != 50 {
        t.Errorf("got %d lessons, want 50", len(got))
    }
    for _, l := range got {
        if strings.ContainsRune(l.Text, 'S') {
            t.Errorf("found upper S in %q", l.Text)
            break
        }
    }
}
```

기존 테스트가 `learning.NewStore(path)` 만 사용 중이라면 그대로 pass 해야 한다 (variadic).

### 작업 6: internal/secret/detector_test.go 에 추가

```go
func TestDetectorRedactString(t *testing.T) {
    d := secret.NewDetector()

    t.Run("empty", func(t *testing.T) {
        if got := d.RedactString(""); got != "" {
            t.Errorf("got %q", got)
        }
    })

    t.Run("no secret", func(t *testing.T) {
        in := "just a normal sentence"
        if got := d.RedactString(in); got != in {
            t.Errorf("got %q, want %q", got, in)
        }
    })

    t.Run("contains secret", func(t *testing.T) {
        // rules.go 의 defaultRules 에서 실제 trigger 되는 패턴 하나 선택.
        // 예: AWS access key / anthropic api key / openai api key.
        // rules.go 를 먼저 확인해서 정확한 regex 에 맞는 샘플 써야 한다.
        in := "key=AKIAIOSFODNN7EXAMPLE done"
        got := d.RedactString(in)
        if got == in {
            t.Fatalf("redaction did not trigger on %q", in)
        }
        if !strings.Contains(got, "[REDACTED:") {
            t.Errorf("expected [REDACTED:...] marker, got %q", got)
        }
    })

    t.Run("nil receiver safe", func(t *testing.T) {
        var d *secret.Detector
        in := "anything"
        if got := d.RedactString(in); got != in {
            t.Errorf("nil receiver should be identity, got %q", got)
        }
    })
}
```

**중요:** rules.go 를 먼저 읽고 실제 default rule 중 하나에 매칭되는 샘플을 고른다. "AKIAIOSFODNN7EXAMPLE" 이 안 걸리면 다른 패턴 사용.

### 작업 7: cmd/elnath/runtime_test.go — e2e 1 케이스

`TestExecutionRuntimeSingleWorkflowRedactsTopic`:

- runtime 빌드 (기존 테스트에서 사용하는 helper 재사용)
- runtime.learningStore 에 실제 redactor 주입됐는지 (또는 runtime 을 통한 end-to-end 경로):
  - 사용자 input 에 default rule 에 매치되는 secret 을 포함
  - Rule C 또는 A 가 trigger 되도록 mock agent.Run 설계
  - `lessons.jsonl` 읽어 Topic/Text 에 원본 secret 문자열 없고 `[REDACTED:...]` 포함

기존 `TestExecutionRuntimeSingleWorkflowPersistsAgentLessons` 패턴을 그대로 차용. 그 테스트의 fixture 를 확장해서 input message 에 secret 포함하게 하면 1-2줄 추가로 끝난다.

### 검증

```bash
cd /Users/stello/elnath
go test -race ./internal/learning/... ./internal/secret/... ./cmd/elnath/...
go vet ./...
make build
```

전부 통과 확인. 회귀 (`learning.NewStore(path)` 기존 호출자 포함 E-3/F-1/F-2 테스트) 없는지도 전체 실행으로 확인:

```bash
go test -race ./...
```

### 수동 smoke (선택)

```bash
rm -f ~/.elnath/data/lessons.jsonl
# elnath run 에서 secret 포함 input 으로 task 1-2 회 실행
./elnath run
# 종료 후
./elnath lessons list
# Topic 에 [REDACTED:...] 포함 확인
```

외부 credential 필요하면 skip OK. 대신 테스트로 증명.

### 커밋

단일 커밋:

```
feat: phase F-2.5 lesson redaction

- learning.Store 에 optional Redactor 주입 (WithRedactor option)
- secret.Detector.RedactString 추가 (1-arg adapter)
- runtime/daemon 에서 detector → store redactor 주입
- Topic/Text/Source 전부 Append 시점에 redact, archive 재처리 없음
- ID 는 redacted 결과 기반 → 재현성 유지
- 신규 테스트 7개 (store 5 + detector 1 + e2e 1)
```

push 하지 않음.

### 보고

- rules.go 에서 선택한 trigger 패턴 이름 (예: aws-access-key)
- 추가된 테스트 수
- 회귀 없음 확인
- spec 이탈 있으면 보고

### 주의

- variadic option 도입이 기존 호출자 타입 검사 통과하는지 반드시 확인. `NewStore(path)` 가 깨지면 E-3/F-1/F-2 테스트 수십 개가 깨진다.
- `RedactString` 는 nil-safe (nil receiver 허용). 이 조건 테스트로 검증.
- defense-in-depth 원칙: 이번에는 오직 **Store Append 한 지점** 에서만 redact. orchestrator/single.go 의 Topic 생성 지점은 건드리지 않는다.
```

---

## 완료 기준

- 전체 `go test -race ./...` green
- `learning.NewStore(path)` 기존 호출자 전부 호환
- `elnath lessons list` 출력에 `[REDACTED:...]` 가 실제 secret 을 가린다 (테스트로 증명)
- 단일 커밋, push 없음
- research / agent 경로 양쪽에 자동 적용됨을 테스트로 확인
