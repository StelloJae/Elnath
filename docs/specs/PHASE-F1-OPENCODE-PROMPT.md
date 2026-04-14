# OpenCode Delegation Prompt: Phase F-1 Lessons Operational Tooling

대상 spec: `docs/specs/PHASE-F1-LESSONS-TOOLING.md`

2 phase 구성. 각 phase 완료 후 반드시 `go test -race` + `go vet` + `make build` 통과 확인. 실패 시 같은 phase 에서 fix, 다음 phase 로 넘어가지 말 것.

**중요 원칙 (이전 phase 와 동일):**

- 기존 메서드 시그니처 변경 금지 (Append/List/Recent 유지).
- 테스트 파일은 반드시 `t.Run` table-driven.
- stub/mock 없이 실제 파일 I/O 로 검증. tempdir 은 `t.TempDir()`.
- 민감 데이터 하드코딩 금지.
- 커밋은 phase 별로 squash 해서 1개만. 메시지 규약: `feat: phase F-1 lessons tooling (phase 1/2 — store API)`.

---

## Phase 1: `internal/learning/store.go` 확장 + 테스트

```
elnath 프로젝트 (`/Users/stello/elnath/`, feat/telegram-redesign 브랜치) 에서 Phase F-1 Phase 1 을 시작한다.

목표: `internal/learning/store.go` 에 운영용 메서드를 추가하고, `store_test.go` 에 race 통과하는 table-driven 테스트를 붙인다.

### 사전 확인

- 기존 타입 `Store` / `Lesson` / `deriveID` 는 변경 금지.
- 기존 메서드 `Append` / `List` / `Recent` 도 시그니처 유지.
- `internal/self/persona.go` 의 `self.Lesson` 은 PersonaDelta 에 계속 사용.
- 파일 권한은 0o600 유지.

### 작업 1: `internal/learning/store.go` 에 추가

1.1 `Filter` 타입

```go
type Filter struct {
    Topic      string
    Confidence string
    Since      time.Time
    Before     time.Time
    IDs        []string
    Limit      int
    Reverse    bool
}

func (f Filter) isZero() bool {
    return f.Topic == "" && f.Confidence == "" &&
        f.Since.IsZero() && f.Before.IsZero() && len(f.IDs) == 0
}

func (f Filter) match(l Lesson) bool {
    if f.Topic != "" {
        if !strings.Contains(strings.ToLower(l.Topic), strings.ToLower(f.Topic)) {
            return false
        }
    }
    if f.Confidence != "" && !strings.EqualFold(l.Confidence, f.Confidence) {
        return false
    }
    if !f.Since.IsZero() && l.Created.Before(f.Since) {
        return false
    }
    if !f.Before.IsZero() && !l.Created.Before(f.Before) {
        return false
    }
    if len(f.IDs) > 0 {
        hit := false
        for _, p := range f.IDs {
            if p != "" && strings.HasPrefix(l.ID, p) {
                hit = true
                break
            }
        }
        if !hit {
            return false
        }
    }
    return true
}
```

1.2 메서드 추가

```go
func (s *Store) ListFiltered(f Filter) ([]Lesson, error) {
    lessons, err := s.List()
    if err != nil {
        return nil, err
    }
    out := lessons[:0:0] // new backing array
    for _, l := range lessons {
        if f.match(l) {
            out = append(out, l)
        }
    }
    if f.Reverse {
        sort.Slice(out, func(i, j int) bool {
            return out[i].Created.After(out[j].Created)
        })
    }
    if f.Limit > 0 && f.Limit < len(out) {
        out = out[:f.Limit]
    }
    return out, nil
}

func (s *Store) Delete(idPrefixes ...string) (int, error) {
    valid := make([]string, 0, len(idPrefixes))
    for _, p := range idPrefixes {
        if p = strings.TrimSpace(p); p != "" {
            valid = append(valid, p)
        }
    }
    if len(valid) == 0 {
        return 0, nil
    }

    s.mu.Lock()
    defer s.mu.Unlock()

    removed := 0
    err := s.rewriteLocked(func(l Lesson, keep func(Lesson)) {
        for _, p := range valid {
            if strings.HasPrefix(l.ID, p) {
                removed++
                return
            }
        }
        keep(l)
    })
    if err != nil {
        return 0, err
    }
    return removed, nil
}

func (s *Store) DeleteMatching(f Filter) (int, error) {
    if f.isZero() {
        return 0, errors.New("learning store: DeleteMatching requires at least one filter")
    }

    s.mu.Lock()
    defer s.mu.Unlock()

    removed := 0
    err := s.rewriteLocked(func(l Lesson, keep func(Lesson)) {
        if f.match(l) {
            removed++
            return
        }
        keep(l)
    })
    if err != nil {
        return 0, err
    }
    return removed, nil
}

func (s *Store) Clear() (int, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    lessons, err := s.readAllLocked()
    if err != nil {
        return 0, err
    }
    if len(lessons) == 0 {
        return 0, nil
    }

    // truncate by opening with O_TRUNC
    f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
    if err != nil {
        return 0, fmt.Errorf("learning store: clear: %w", err)
    }
    if cerr := f.Close(); cerr != nil {
        return 0, fmt.Errorf("learning store: clear close: %w", cerr)
    }
    return len(lessons), nil
}

type RotateOpts struct {
    KeepLast int
    MaxBytes int64
}

func (o RotateOpts) hasBound() bool {
    return o.KeepLast > 0 || o.MaxBytes > 0
}

func (s *Store) Rotate(opts RotateOpts) (int, error) {
    if !opts.hasBound() {
        return 0, errors.New("learning store: Rotate requires KeepLast or MaxBytes")
    }

    s.mu.Lock()
    defer s.mu.Unlock()

    lessons, err := s.readAllLocked()
    if err != nil {
        return 0, err
    }
    if len(lessons) == 0 {
        return 0, nil
    }

    // insertion order preserved in lessons. oldest first.
    keepFromIdx := 0
    if opts.KeepLast > 0 && opts.KeepLast < len(lessons) {
        keepFromIdx = len(lessons) - opts.KeepLast
    }

    if opts.MaxBytes > 0 {
        fi, statErr := os.Stat(s.path)
        if statErr == nil && fi.Size() > opts.MaxBytes {
            approxAvg := fi.Size() / int64(len(lessons))
            if approxAvg <= 0 {
                approxAvg = 1
            }
            targetEntries := opts.MaxBytes / approxAvg
            if targetEntries < 0 {
                targetEntries = 0
            }
            idxByBytes := len(lessons) - int(targetEntries)
            if idxByBytes > keepFromIdx {
                keepFromIdx = idxByBytes
            }
            if keepFromIdx >= len(lessons) {
                keepFromIdx = len(lessons) - 1
            }
            if keepFromIdx < 0 {
                keepFromIdx = 0
            }
        }
    }

    if keepFromIdx <= 0 {
        return 0, nil
    }

    toArchive := lessons[:keepFromIdx]
    toKeep := lessons[keepFromIdx:]

    if err := s.appendArchiveLocked(toArchive); err != nil {
        return 0, err
    }

    if err := s.writeAllLocked(toKeep); err != nil {
        return 0, err
    }
    return len(toArchive), nil
}

func (s *Store) AutoRotateIfNeeded(opts RotateOpts) (int, error) {
    if !opts.hasBound() {
        return 0, nil
    }
    fi, err := os.Stat(s.path)
    if err != nil {
        if os.IsNotExist(err) {
            return 0, nil
        }
        return 0, fmt.Errorf("learning store: stat: %w", err)
    }
    exceedBytes := opts.MaxBytes > 0 && fi.Size() > opts.MaxBytes

    if !exceedBytes && opts.KeepLast > 0 {
        // cheap line count: scan file
        lessons, err := s.List()
        if err != nil {
            return 0, err
        }
        if len(lessons) <= opts.KeepLast {
            return 0, nil
        }
    } else if !exceedBytes {
        return 0, nil
    }
    return s.Rotate(opts)
}

type Stats struct {
    Total        int
    ByTopic      map[string]int
    ByConfidence map[string]int
    OldestAt     time.Time
    NewestAt     time.Time
    FileBytes    int64
}

func (s *Store) Summary() (Stats, error) {
    lessons, err := s.List()
    if err != nil {
        return Stats{}, err
    }
    st := Stats{
        ByTopic:      make(map[string]int),
        ByConfidence: make(map[string]int),
    }
    if fi, statErr := os.Stat(s.path); statErr == nil {
        st.FileBytes = fi.Size()
    }
    for _, l := range lessons {
        st.Total++
        st.ByTopic[l.Topic]++
        st.ByConfidence[l.Confidence]++
        if st.OldestAt.IsZero() || l.Created.Before(st.OldestAt) {
            st.OldestAt = l.Created
        }
        if l.Created.After(st.NewestAt) {
            st.NewestAt = l.Created
        }
    }
    return st, nil
}
```

1.3 private 헬퍼 추가

```go
func (s *Store) archivePath() string {
    if strings.HasSuffix(s.path, ".jsonl") {
        return strings.TrimSuffix(s.path, ".jsonl") + ".archive.jsonl"
    }
    return s.path + ".archive"
}

// readAllLocked assumes caller holds s.mu. Mirrors List() logic without re-locking.
func (s *Store) readAllLocked() ([]Lesson, error) {
    if s == nil || s.path == "" {
        return nil, nil
    }
    file, err := os.Open(s.path)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, fmt.Errorf("learning store: open: %w", err)
    }
    defer file.Close()

    var lessons []Lesson
    scanner := bufio.NewScanner(file)
    scanner.Buffer(make([]byte, 64*1024), 1024*1024)
    for scanner.Scan() {
        var l Lesson
        if err := json.Unmarshal(scanner.Bytes(), &l); err != nil {
            return nil, fmt.Errorf("learning store: decode: %w", err)
        }
        lessons = append(lessons, l)
    }
    if err := scanner.Err(); err != nil {
        return nil, fmt.Errorf("learning store: scan: %w", err)
    }
    return lessons, nil
}

func (s *Store) writeAllLocked(lessons []Lesson) error {
    dir := filepath.Dir(s.path)
    tmp, err := os.CreateTemp(dir, ".lessons-*.tmp")
    if err != nil {
        return fmt.Errorf("learning store: create tempfile: %w", err)
    }
    tmpPath := tmp.Name()
    cleanup := func() { _ = os.Remove(tmpPath) }

    if err := os.Chmod(tmpPath, 0o600); err != nil {
        tmp.Close()
        cleanup()
        return fmt.Errorf("learning store: chmod tempfile: %w", err)
    }

    enc := json.NewEncoder(tmp)
    for _, l := range lessons {
        if err := enc.Encode(l); err != nil {
            tmp.Close()
            cleanup()
            return fmt.Errorf("learning store: encode: %w", err)
        }
    }
    if err := tmp.Sync(); err != nil {
        tmp.Close()
        cleanup()
        return fmt.Errorf("learning store: sync tempfile: %w", err)
    }
    if err := tmp.Close(); err != nil {
        cleanup()
        return fmt.Errorf("learning store: close tempfile: %w", err)
    }
    if err := os.Rename(tmpPath, s.path); err != nil {
        cleanup()
        return fmt.Errorf("learning store: rename tempfile: %w", err)
    }
    return nil
}

func (s *Store) rewriteLocked(visit func(l Lesson, keep func(Lesson))) error {
    lessons, err := s.readAllLocked()
    if err != nil {
        return err
    }
    if len(lessons) == 0 {
        return nil
    }
    kept := make([]Lesson, 0, len(lessons))
    keepFn := func(l Lesson) { kept = append(kept, l) }
    for _, l := range lessons {
        visit(l, keepFn)
    }
    if len(kept) == len(lessons) {
        return nil // nothing changed, avoid rewriting
    }
    return s.writeAllLocked(kept)
}

func (s *Store) appendArchiveLocked(lessons []Lesson) error {
    if len(lessons) == 0 {
        return nil
    }
    arcPath := s.archivePath()
    if err := os.MkdirAll(filepath.Dir(arcPath), 0o755); err != nil {
        return fmt.Errorf("learning store: archive dir: %w", err)
    }
    f, err := os.OpenFile(arcPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
    if err != nil {
        return fmt.Errorf("learning store: open archive: %w", err)
    }
    enc := json.NewEncoder(f)
    for _, l := range lessons {
        if err := enc.Encode(l); err != nil {
            f.Close()
            return fmt.Errorf("learning store: encode archive: %w", err)
        }
    }
    if err := f.Close(); err != nil {
        return fmt.Errorf("learning store: close archive: %w", err)
    }
    return nil
}
```

1.4 import 에 필요한 것 추가: `errors`, `strings`, 기존 `bufio` / `encoding/json` / `fmt` / `os` / `path/filepath` / `sort` / `sync` / `time` 유지.

### 작업 2: `internal/learning/store_test.go` 확장

아래 하위 테스트를 추가한다 (기존 테스트 함수는 그대로). `t.Run` 으로 묶어 table-driven.

1. `TestStore_ListFiltered`
   - 5개 lesson append (topic a/b/c, confidence high/medium/low, Created 시각을 `time.Sleep` 아닌 명시적 now+N 사용 위해 `Lesson.Created` 를 직접 set 해서 append)
   - Filter{Topic:"a"} → 해당만, len 일치
   - Filter{Confidence:"high"} → 해당만
   - Filter{Since: t3} → t3 이후만
   - Filter{Before: t3} → t3 미만 (t3 정확 시각은 제외)
   - Filter{Limit: 2} → 2개
   - Filter{Reverse: true} → 최신순 정렬 확인
   - Filter{IDs: []string{prefix4}} → prefix 매칭

2. `TestStore_Delete`
   - 3개 append → Delete(p1, p2) → 2개 제거, List 1개
   - 없는 prefix → 0, 파일 unchanged
   - 빈 slice / whitespace-only → 0, 에러 없음

3. `TestStore_DeleteMatching`
   - 3개 append (topic a,a,b)
   - DeleteMatching{Topic:"a"} → count=2, 남은 1개 topic=b
   - DeleteMatching{} (zero) → 에러, 파일 unchanged

4. `TestStore_Clear`
   - 3개 append → Clear() → count=3, List=nil
   - archive 파일 미리 생성 → Clear 후 archive 그대로 (크기 비교)
   - 빈 파일에 Clear → 0, 에러 없음

5. `TestStore_Rotate`
   - 5개 append → Rotate{KeepLast:2} → archive 3개, active 2개 (순서 유지: active 는 마지막 2개)
   - Rotate{KeepLast:10} → no-op, 0
   - MaxBytes 초과 케이스: 10개 append 후 `RotateOpts{MaxBytes: <현재크기 절반>}` → archive 로 이동, active 파일 크기 축소 확인
   - opts.hasBound=false → 에러
   - Rotate 2회 연속 호출 → 두 번째는 archive 에 append (archive 총 entry 수 증가 확인)

6. `TestStore_AutoRotateIfNeeded`
   - 빈 store → 0, 에러 없음
   - 5개 entry, KeepLast:10 → no-op, 0
   - 5개 entry, KeepLast:2 → 3 rotated

7. `TestStore_Summary`
   - 0개 → Total=0, map 빈
   - 3개 (topic a,b,b, confidence high,medium,high) → Total=3, ByTopic[a]=1,[b]=2, ByConfidence[high]=2,[medium]=1
   - Oldest/Newest Created 범위 확인
   - FileBytes > 0 (파일 존재 시)

8. race 보강: 기존 concurrent 테스트 옆에 "concurrent Rotate + Append" 추가 — 10 goroutine append 하는 동안 main 에서 3번 Rotate 호출. panic 없음, 최종 active+archive 합계가 원본 + 50 와 일치.

### 검증

```bash
cd /Users/stello/elnath
go test -race ./internal/learning/...
go vet ./internal/learning/...
```

전부 통과 후 phase 종료. race flag 필수.

### 보고

- 추가된 메서드 이름 리스트
- 테스트 케이스 수
- 발견된 edge case 가 있으면 메모
```

---

## Phase 2: CLI subcommands + daemon auto-rotate + e2e tests

```
Phase F-1 Phase 2 시작. Phase 1 에서 store API 가 완성됐다는 가정.

목표: `elnath lessons` 서브커맨드를 구현하고, daemon 기동 시 auto-rotate 를 호출하도록 한다.

### 작업 1: `cmd/elnath/cmd_lessons.go` 신규 작성

spec 의 3.3 절을 그대로 따른다. 특히:

- `cmdLessons` 메인 dispatcher
- 서브커맨드: `list`, `show`, `clear`, `rotate`, `stats`, `help`
- 각 서브커맨드는 독립 `flag.NewFlagSet(name, flag.ContinueOnError)` 로 파싱
- 공통 헬퍼 두 개:

```go
func parseTimeFlag(raw string) (time.Time, error) {
    raw = strings.TrimSpace(raw)
    if raw == "" {
        return time.Time{}, nil
    }
    if d, err := time.ParseDuration(raw); err == nil && d > 0 {
        return time.Now().UTC().Add(-d), nil
    }
    if t, err := time.Parse(time.RFC3339, raw); err == nil {
        return t.UTC(), nil
    }
    // accept shorthand "Nd"
    if strings.HasSuffix(raw, "d") {
        if n, err := strconv.Atoi(strings.TrimSuffix(raw, "d")); err == nil && n > 0 {
            return time.Now().UTC().Add(-time.Duration(n) * 24 * time.Hour), nil
        }
    }
    return time.Time{}, fmt.Errorf("invalid time %q (expected RFC3339 or duration like 7d/24h)", raw)
}

func parseBytesFlag(raw string) (int64, error) {
    raw = strings.TrimSpace(strings.ToUpper(raw))
    if raw == "" {
        return 0, nil
    }
    mul := int64(1)
    switch {
    case strings.HasSuffix(raw, "KB"):
        mul = 1024
        raw = strings.TrimSuffix(raw, "KB")
    case strings.HasSuffix(raw, "MB"):
        mul = 1024 * 1024
        raw = strings.TrimSuffix(raw, "MB")
    case strings.HasSuffix(raw, "GB"):
        mul = 1024 * 1024 * 1024
        raw = strings.TrimSuffix(raw, "GB")
    }
    n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
    if err != nil || n < 0 {
        return 0, fmt.Errorf("invalid byte size %q", raw)
    }
    return n * mul, nil
}
```

- `list` 기본 limit=50, `--newest` 가 있으면 Reverse=true.
- `show` 는 ListFiltered(Filter{IDs:[prefix]}) 결과로 판단:
  - 0개 → `fmt.Errorf("no lesson matched prefix %q", prefix)`
  - ≥2개 → `fmt.Errorf("ambiguous prefix %q: %d matches", prefix, n)`
- `clear`:
  - `--id` 는 repeatable. `flag.Var` 로 커스텀 slice 타입 구현:
    ```go
    type stringList []string
    func (s *stringList) String() string { return strings.Join(*s, ",") }
    func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }
    ```
  - `--all` 과 다른 필터 배타
  - 필터가 모두 비어있고 `--all` 도 없으면 에러
  - 확인 프롬프트: `--all && !yesFlag && isTTY(os.Stdin)` 조건. 비 TTY 면 `-y` 없을 때 거부.
  - dry-run 은 `ListFiltered` 로 count 만 표시.
- `rotate`:
  - `--keep` int, `--max-bytes` string (parseBytesFlag)
  - 둘 다 0 이면 에러.
  - 결과 출력에 active/archive 경로, 각 크기/엔트리 수.
- `stats`:
  - Active 경로/크기/Total/Range.
  - By confidence 는 고정 순서 [high, medium, low].
  - By topic 은 빈도 상위 10개, 동률은 topic 이름 사전순.
  - archive 라인 수: `countArchiveLines(store)` 같은 헬퍼. 단순 `bufio.Scanner` 로 `.archive.jsonl` 파일 읽으며 증가.
  - `--json` 시 `Stats` JSON 출력 + 추가 필드 `{"archive_bytes": N, "archive_lines": N}`.

- TTY 판별:
  ```go
  func isTTY(f *os.File) bool {
      fi, err := f.Stat()
      if err != nil {
          return false
      }
      return (fi.Mode() & os.ModeCharDevice) != 0
  }
  ```

### 작업 2: `cmd/elnath/commands.go` 수정

```go
func commandRegistry() map[string]commandRunner {
    return map[string]commandRunner{
        "version":  cmdVersion,
        "help":     cmdHelp,
        "run":      cmdRun,
        "setup":    cmdSetup,
        "daemon":   cmdDaemon,
        "research": cmdResearch,
        "telegram": cmdTelegram,
        "wiki":     cmdWiki,
        "search":   cmdSearch,
        "eval":     cmdEval,
        "task":     cmdTask,
        "lessons":  cmdLessons, // NEW
    }
}
```

### 작업 3: `cmd/elnath/cmd_daemon.go` 수정

learningStore 생성 직후 AutoRotateIfNeeded 호출. spec 3.5 절 그대로:

```go
learningPath := filepath.Join(cfg.DataDir, "lessons.jsonl")
learningStore := learning.NewStore(learningPath)
if n, err := learningStore.AutoRotateIfNeeded(learning.RotateOpts{
    KeepLast: 5000,
    MaxBytes: 1 << 20,
}); err != nil {
    app.Logger.Warn("learning: auto-rotate failed", "error", err)
} else if n > 0 {
    app.Logger.Info("learning: auto-rotated lessons", "moved", n)
}
```

- `app.Logger` 는 이미 존재하는 *slog.Logger. 필드 이름 틀리면 실제 파일 read 해서 교정.
- `runtime.go` 에는 추가 X.

### 작업 4: `cmd/elnath/cmd_lessons_test.go` 신규

end-to-end 테스트. 패턴은 기존 `cmd_research_test.go` / `cmd_task_test.go` 참고.

공통 fixture:

```go
func newLessonsFixture(t *testing.T) (*learning.Store, string) {
    t.Helper()
    dir := t.TempDir()
    path := filepath.Join(dir, "lessons.jsonl")
    store := learning.NewStore(path)
    // pre-populate 3 lessons with different topics/confidence
    // use store.Append to let ID/Created auto-set
    ...
    return store, dir
}
```

CLI 진입은 `cmdLessons(ctx, args)` 직접 호출. 그러나 `cmdLessons` 가 config.Load 를 하므로, 임시 config yaml 을 tempdir 에 만들고 `os.Args` 를 조작해서 `--config` 전달하는 패턴 필요. 기존 `command_helpers_test.go` 에 비슷한 헬퍼 있으면 재사용.

또는 더 간단하게: `cmd_lessons.go` 의 dispatcher 를 얇게 두고, 각 subcommand 함수가 `*learning.Store` 를 받는 내부 구현 `lessonsList(store, args)` 를 export 가능한 형태로 유지. 테스트는 `lessonsList(store, ...)` 를 직접 호출해서 stdout 캡처.

stdout 캡처 헬퍼:

```go
func captureStdout(t *testing.T, fn func()) string {
    t.Helper()
    old := os.Stdout
    r, w, _ := os.Pipe()
    os.Stdout = w
    fn()
    w.Close()
    os.Stdout = old
    b, _ := io.ReadAll(r)
    return string(b)
}
```

**테스트 케이스:**

1. `TestLessonsList_Human` — 3개 중 확인용 문자열 3줄 포함
2. `TestLessonsList_JSONFlag` — 줄마다 `json.Unmarshal` 성공
3. `TestLessonsList_TopicFilter` — topic 필터로 1줄만 남음
4. `TestLessonsShow_Unique` — prefix 8자 → 전체 detail 출력 (persona delta 포함)
5. `TestLessonsShow_Ambiguous` — prefix 1자 (여러개 match) → 에러 `ambiguous`
6. `TestLessonsShow_NotFound` → 에러 `no lesson matched`
7. `TestLessonsClear_Topic_DryRun` — dry-run 은 파일 unchanged, 출력에 "Would delete N"
8. `TestLessonsClear_Topic_Apply` — `-y --topic X` → 해당만 제거, 나머지 유지
9. `TestLessonsClear_All_WithoutYes_Errors` — 비 TTY 환경에서 `-y` 없으면 거부
10. `TestLessonsRotate_KeepLast` — `--keep 1` → active 1, archive 2
11. `TestLessonsRotate_MaxBytes` — `--max-bytes 80` 적용 후 active 크기 감소
12. `TestLessonsRotate_NoBound` → 에러
13. `TestLessonsStats_Human` — 출력에 "Total:", "By confidence:", "By topic" 포함
14. `TestLessonsStats_JSON` — json.Unmarshal 성공, Total 일치
15. `TestParseTimeFlag` — table: RFC3339 / "24h" / "7d" / invalid
16. `TestParseBytesFlag` — table: "1024" / "512KB" / "1MB" / invalid

### 검증

각 파일 저장 직후 build 먼저:

```bash
cd /Users/stello/elnath
go build ./...
```

전체 테스트:

```bash
go test -race ./internal/learning/... ./cmd/elnath/...
go vet ./...
make build
```

전부 통과 후 수동 smoke:

```bash
./elnath lessons help
./elnath lessons list --limit 5
./elnath lessons stats
```

(실제 lessons.jsonl 이 없으면 "No lessons found." / "Total: 0" 이 떠야 함. 에러 나면 버그.)

### 커밋

`feat: phase F-1 lessons tooling (phase 2/2 — CLI + auto-rotate)` 로 단일 커밋. 체크포인트는 squash.

### 보고

- 새 파일 이름 / LOC
- 테스트 케이스 수 + pass 여부
- spec 에서 벗어난 부분 있으면 사유와 함께 기록
- 수동 smoke 결과 (stdout 복붙)
```

---

## 작업 중 막히면

- spec 3.1 의 메서드 시그니처 기준. 필요하면 spec 과 다른 이름 제안 전에 반드시 보고.
- research runner 쪽은 이번 phase 에서 건드리지 않는다. 만약 test dependency 가 필요해지면 그 사실만 보고.
- 테스트가 flaky 하면 `time.Now()` 대신 Lesson.Created 를 명시적으로 set 해서 ordering 안정화.
- ID prefix 매칭에서 충돌이 우연히 발생하면 입력 len 을 늘려 재시도. SHA256 8자에서 3개 fixture 가 겹칠 확률은 0 에 가깝다.

## 완료 기준

- Phase 1 + Phase 2 모두 `go test -race ./... && go vet ./... && make build` 성공
- spec §5 Acceptance Criteria 의 체크박스 항목이 전부 green
- `elnath lessons list/show/clear/rotate/stats` 가 실제로 실행됨 (수동 smoke)
- 기존 테스트 (learning/prompt/research/cmd/elnath) 회귀 없음
