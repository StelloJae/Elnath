# Phase F-1: Lessons Operational Tooling

**Status:** SPEC READY
**Predecessor:** Phase E-3 (B6 Self-Improvement) DONE
**Successor:** Phase F-2 (TBD — Cron, LB2 Magic Docs, or agent-task lesson extraction)
**Branch:** `feat/telegram-redesign`
**Ref:** Phase E-3 §7 Future Work — "CLI: `elnath lessons list`, `elnath lessons clear <id>`"

---

## 1. Goal

Phase E-3 에서 도입된 `lessons.jsonl` 은 현재 **읽기/쓰기만 가능**하고 **운영 수단이 전무**하다. 이 phase 에서는 일상적인 점검·정리·회고를 위한 CLI 도구와 저장소 관리 기능을 추가한다.

구체적으로:

1. `elnath lessons list [--topic T] [--confidence C] [--since D] [--limit N]` — 저장된 lesson 조회
2. `elnath lessons show <id>` — 특정 lesson 전체(persona delta 포함) 상세 출력
3. `elnath lessons clear [--id X ...] [--topic T] [--before D] [--all]` — 선택 삭제 또는 전체 삭제
4. `elnath lessons rotate [--keep N] [--max-bytes B]` — 오래된 lesson 을 archive 로 이동
5. `elnath lessons stats` — topic / confidence / 최근 30일 카운트 요약
6. `learning.Store` 에 Delete / Clear / Rotate / ListFiltered 메서드 추가
7. daemon 기동 시 자동 rotation (기본 5000 entries / 1MB 초과 시 archive)

**Out of scope:**
- LLM-based lesson editing/summarization
- Remote lesson sync
- Wiki export (Phase F-later)
- GUI
- Agent-task 기반 lesson 추출 (E-4 후보)

## 2. Architecture

```
┌─────────────────────────────────────────┐
│ cmd/elnath/cmd_lessons.go (new)         │
│  - parses flags, dispatches subcommands │
└─────────┬───────────────────────────────┘
          │ uses
          ▼
┌─────────────────────────────────────────┐
│ internal/learning/store.go (extended)   │
│  existing: Append / List / Recent       │
│  NEW:      Delete / Clear / Rotate      │
│            ListFiltered / Stats         │
└─────────┬───────────────────────────────┘
          │ backed by
          ▼
┌─────────────────────────────────────────┐
│ {dataDir}/lessons.jsonl       (active)  │
│ {dataDir}/lessons.archive.jsonl (old)   │
└─────────────────────────────────────────┘
```

daemon 기동 경로:

```
cmd_daemon.go (기존) → learning.NewStore(path)
  + (new) store.AutoRotateIfNeeded(RotateOpts{KeepLast: 5000, MaxBytes: 1<<20})
```

**설계 원칙:**

1. **Atomic rewrite** — Delete/Clear/Rotate 는 write-to-tempfile + `os.Rename` 패턴으로 원자성 확보. 중간 크래시 시 원본 유지.
2. **Archive append-only** — rotation 은 오래된 entry 를 `lessons.archive.jsonl` 에 append, 기존 archive 는 절대 덮어쓰지 않음. 복구 가능.
3. **Filter composition** — Topic / Confidence / Since / Before 는 AND 조건. `ListFiltered(Filter)` 하나로 모든 조회 경로 통합.
4. **ID 기반 안전장치** — `clear --id` 는 8-char hex prefix 매칭 (Lesson.ID 는 이미 SHA256 8자). 부분 매칭 시 ambiguous 에러.
5. **CLI 출력 포맷** — human-readable 기본, `--json` 플래그로 JSONL 원문 재출력 (파이프/script 친화).

## 3. Deliverables

### 3.1 Modified: `internal/learning/store.go`

기존 `Store` 구조 유지, 아래 메서드/타입 추가.

```go
// Filter narrows a lesson query. Zero-value fields are ignored.
type Filter struct {
    Topic      string    // substring match on Lesson.Topic (case-insensitive)
    Confidence string    // exact match: "high" | "medium" | "low"
    Since      time.Time // inclusive lower bound on Lesson.Created
    Before     time.Time // exclusive upper bound on Lesson.Created
    IDs        []string  // if non-empty, only lessons whose ID starts with one of these prefixes
    Limit      int       // if > 0, caps result length (applied after sorting)
    Reverse    bool      // newest first when true (default: insertion order)
}

// ListFiltered returns lessons matching all non-zero filter fields.
// Sorting rules: insertion order unless Reverse is true.
func (s *Store) ListFiltered(f Filter) ([]Lesson, error)

// Delete removes lessons whose IDs start with any of idPrefixes.
// Returns the number of lessons removed. An unmatched prefix is not an error.
// Uses atomic tempfile + rename; original remains intact if an error occurs.
func (s *Store) Delete(idPrefixes ...string) (int, error)

// DeleteMatching removes every lesson that matches f. Returns the count.
// At least one filter field must be non-zero; use Clear for a full wipe.
func (s *Store) DeleteMatching(f Filter) (int, error)

// Clear truncates the active lessons file. Archive is untouched.
// Returns count removed.
func (s *Store) Clear() (int, error)

// RotateOpts controls rotation. At least one bound must be positive.
type RotateOpts struct {
    KeepLast int   // keep the newest N lessons in the active file
    MaxBytes int64 // if active file exceeds this, rotate oldest until under the limit
}

// Rotate moves oldest active lessons into the archive file until the opts bounds
// are satisfied. No-op when the active file already fits. Returns count moved.
// Archive path: {activePath} with ".jsonl" → ".archive.jsonl".
func (s *Store) Rotate(opts RotateOpts) (int, error)

// AutoRotateIfNeeded is a convenience wrapper intended for daemon startup:
// performs rotation only when opts bounds are exceeded, logs nothing on no-op.
func (s *Store) AutoRotateIfNeeded(opts RotateOpts) (int, error)

// Stats holds a summary of the active file (archive not included).
type Stats struct {
    Total       int
    ByTopic     map[string]int
    ByConfidence map[string]int
    OldestAt    time.Time
    NewestAt    time.Time
    FileBytes   int64
}

// Summary reads the active file and returns aggregate stats.
func (s *Store) Summary() (Stats, error)
```

**구현 노트:**

- `archivePath()` 헬퍼: `strings.TrimSuffix(s.path, ".jsonl") + ".archive.jsonl"`. `.jsonl` 확장자 없으면 `".archive"` 접미사.
- Delete / DeleteMatching / Clear / Rotate 는 전부 `s.mu.Lock()` 아래에서 수행.
- Atomic rewrite 공통 헬퍼 `rewriteLocked(func(keep func(Lesson)) error) error`:
  1. `dir := filepath.Dir(s.path)` 아래 tempfile 생성 (`os.CreateTemp(dir, ".lessons-*.tmp")`).
  2. 원본 라인 단위로 순회하며 callback 이 `keep` 를 부른 lesson 만 temp 에 기록.
  3. 성공 시 `os.Rename(temp, s.path)`. 실패 시 temp 삭제.
  4. 권한 0o600 유지.
- Rotate 의 archive 쪽은 O_APPEND 로 append 후 active 쪽을 rewriteLocked 로 줄인다. 둘 다 같은 mutex 보호.
- ListFiltered 의 Topic 매칭: `strings.Contains(strings.ToLower(l.Topic), strings.ToLower(f.Topic))`.
- `DeleteMatching` 은 `Filter.IsZero()` 인 경우 `errors.New("learning store: DeleteMatching requires at least one filter")` 반환.

### 3.2 Modified: `internal/learning/store_test.go`

기존 테스트 유지, 다음 테이블 기반 테스트 추가:

1. **ListFiltered**
   - 10개 append (topic 2종, confidence 3종, 다양한 Created 시각)
   - topic 필터 → 해당만
   - confidence=medium → 해당만
   - since=now-5s → 최근 5s
   - limit=3 → 3개
   - Reverse=true → 최신순

2. **Delete**
   - 5개 append → Delete(prefix[:8]) → 해당만 제거, 나머지 4개 그대로
   - Delete 2개 prefix → 2개 제거
   - 존재하지 않는 prefix → count=0, 파일 그대로
   - Delete 후 race-safe (동시 Append 와 병행해도 panic 없음)

3. **DeleteMatching**
   - topic 필터 → 해당만 제거, 반환 count 일치
   - empty Filter → 에러 반환, 파일 unchanged

4. **Clear**
   - 3개 append → Clear → List() = nil, count=3
   - archive 존재 → archive 는 unchanged

5. **Rotate**
   - 10개 append + `RotateOpts{KeepLast: 3}` → active 에 3개, archive 에 7개
   - 20개 append + `MaxBytes: 500` → active 크기 500 이하로 내려갈 때까지 oldest 이동
   - `AutoRotateIfNeeded{KeepLast:100}` on 10개 active → no-op, count=0
   - archive 에 이미 파일 있으면 뒤에 append

6. **Summary**
   - 3 topic / 2 confidence 분포 → ByTopic / ByConfidence 정확
   - OldestAt/NewestAt Created 범위
   - 빈 store → Total=0, 빈 map

### 3.3 New: `cmd/elnath/cmd_lessons.go`

```go
package main

import (
    "context"
    "encoding/json"
    "errors"
    "flag"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "time"

    "github.com/stello/elnath/internal/config"
    "github.com/stello/elnath/internal/learning"
)

func cmdLessons(ctx context.Context, args []string) error {
    if len(args) == 0 {
        return printLessonsUsage()
    }

    cfgPath := extractConfigFlag(os.Args)
    if cfgPath == "" {
        cfgPath = config.DefaultConfigPath()
    }
    cfg, err := config.Load(cfgPath)
    if err != nil {
        return fmt.Errorf("load config: %w", err)
    }
    store := learning.NewStore(filepath.Join(cfg.DataDir, "lessons.jsonl"))

    switch args[0] {
    case "list":
        return lessonsList(store, args[1:])
    case "show":
        return lessonsShow(store, args[1:])
    case "clear":
        return lessonsClear(store, args[1:])
    case "rotate":
        return lessonsRotate(store, args[1:])
    case "stats":
        return lessonsStats(store, args[1:])
    case "help", "-h", "--help":
        return printLessonsUsage()
    default:
        return fmt.Errorf("unknown lessons subcommand: %s (try: elnath lessons help)", args[0])
    }
}
```

**Subcommand 상세:**

#### `list`

```
Flags:
  --topic T        substring match
  --confidence C   high|medium|low
  --since D        RFC3339 OR "7d"/"24h" relative (subtract from now)
  --before D       RFC3339 OR relative
  --limit N        max rows (default 50)
  --newest         sort newest-first (default: insertion order)
  --json           JSONL output (one Lesson per line)
```

출력 (human):

```
2026-04-13  [high] elnath-v2  a1b2c3d4
  On elnath-v2: streaming summary reduces user confusion by 40%.

2026-04-12  [medium] ml-strategies  9f8e7d6c
  Topic ml-strategies requires more evidence before conclusions.
```

빈 결과 시 `No lessons found.` 출력하고 exit 0.

#### `show <id>`

- args 첫 번째가 id prefix (>=4자, 권장 8자).
- ListFiltered 로 검색 → 1개 아니면 에러 ("no lesson matched" / "ambiguous prefix: N matches").
- 1개 match 시 전체 필드 dump (text / topic / source / confidence / created / persona delta 리스트).

```
ID:         a1b2c3d4
Created:    2026-04-13T09:12:44Z
Topic:      elnath-v2
Confidence: high
Source:     elnath-v2

Text:
  On elnath-v2: streaming summary reduces user confusion by 40%.

Persona delta:
  persistence  +0.020
```

#### `clear`

```
Flags (적어도 하나 필요):
  --id ID          한 번에 한 개, 반복 가능 (--id a1b2 --id 9f8e)
  --topic T        substring match
  --confidence C
  --before D
  --all            전체 삭제 (다른 필터와 배타)
  --dry-run        실제 삭제 안 함, 매칭 카운트만 출력
  -y               확인 프롬프트 skip

확인 프롬프트 (--all 인 경우, -y 없으면):
  "Delete ALL N lessons? Archive is untouched. [y/N] "
```

- `--all` + 다른 필터 → 에러.
- 아무 필터 없음 → 에러 ("specify --id, --topic, --confidence, --before, or --all").
- dry-run 은 `DeleteMatching` 을 호출하지 않고 `ListFiltered` 만 수행.
- 성공 시 `Deleted N lesson(s).` 출력.

#### `rotate`

```
Flags (적어도 하나 필요):
  --keep N         active 에 최근 N 만 유지
  --max-bytes B    active 파일 크기 상한 (숫자 또는 "1MB"/"512KB")
```

둘 다 지정 시 `RotateOpts{KeepLast: N, MaxBytes: B}` 로 양쪽 동시 적용. count + archive 경로 출력:

```
Rotated 120 lesson(s). Active: /Users/x/.elnath/lessons.jsonl (43 entries, 6.2 KB). Archive: /Users/x/.elnath/lessons.archive.jsonl (total 120 entries).
```

#### `stats`

- `Summary()` 호출.
- 출력:

```
Active file: /Users/x/.elnath/lessons.jsonl (12.4 KB)
Total: 163 lessons (oldest 2026-03-28, newest 2026-04-13)

By confidence:
  high    98
  medium  41
  low     24

By topic (top 10):
  elnath-v2      34
  ml-strategies  22
  ...
```

- `--json` 시 `Stats` JSON encode.
- archive 크기도 같이 출력 (별도 줄). archive 열어서 wc 만 수행 (라인 수).

**공통 헬퍼:**

- `parseTimeFlag(string) (time.Time, error)`:
  - `"7d"` / `"24h"` / `"30m"` → `time.Now().UTC().Add(-dur)`
  - RFC3339 → 그대로 파싱
  - 빈 문자열 → zero time (ignore)
- `parseBytesFlag(string) (int64, error)`:
  - `"1MB"`/`"512KB"`/`"1024"` 지원.
- flag 파싱은 `flag.NewFlagSet("lessons-list", flag.ContinueOnError)` 형태.

### 3.4 Modified: `cmd/elnath/commands.go`

```go
func commandRegistry() map[string]commandRunner {
    return map[string]commandRunner{
        ...
        "lessons": cmdLessons,  // NEW
    }
}
```

help 텍스트 (`onboarding.T(..., "cli.help")`) 에 `lessons` 라인 추가 필요 여부 확인 — 있으면 업데이트, 없으면 별도 처리 불필요.

### 3.5 Modified: `cmd/elnath/cmd_daemon.go`

learning store 생성 후 auto-rotate 호출:

```go
learningPath := filepath.Join(cfg.DataDir, "lessons.jsonl")
learningStore := learning.NewStore(learningPath)
if n, err := learningStore.AutoRotateIfNeeded(learning.RotateOpts{
    KeepLast: 5000,
    MaxBytes: 1 << 20, // 1 MiB
}); err != nil {
    app.Logger.Warn("learning: auto-rotate failed", "error", err)
} else if n > 0 {
    app.Logger.Info("learning: auto-rotated lessons", "moved", n)
}
```

`cmd/elnath/runtime.go` 도 같은 store 를 생성하지만, rotate 는 daemon 기동 경로에서만 수행 (interactive `elnath run` 은 매 실행 rotate 하면 오히려 노이즈).

### 3.6 New: `cmd/elnath/cmd_lessons_test.go`

- `cmdLessons` end-to-end 테스트 (tempdir 을 DataDir 로 설정한 테스트용 config):
  - 3개 lesson 을 pre-populate
  - `list` 출력 contains 3개 line
  - `list --topic X --json` → JSONL 1줄
  - `show <id8>` → 전체 필드 포함
  - `clear --topic X -y` → 해당만 제거
  - `stats` → `Total: 3` 이전/이후 카운트 일치
  - `rotate --keep 1` → active 1개, archive 2개
- flag 파싱 단위 테스트 (`parseTimeFlag`, `parseBytesFlag`) 별도 케이스.

### 3.7 Modified (optional): help catalog

`internal/onboarding/` 의 locale json 에 `cli.help` 업데이트. 다국어 번역은 en 만 먼저 반영하고 ko 는 기존 패턴 따라 추후.

## 4. File Summary

### New files (2)

| File | LOC (est.) | 역할 |
|------|-----------|------|
| `cmd/elnath/cmd_lessons.go` | ~400 | CLI subcommands (list/show/clear/rotate/stats) |
| `cmd/elnath/cmd_lessons_test.go` | ~250 | subcommand e2e + flag helper tests |

### Modified files (4)

| File | 변경 내용 |
|------|---------|
| `internal/learning/store.go` | Filter/Delete/DeleteMatching/Clear/Rotate/AutoRotateIfNeeded/Summary + rewriteLocked helper |
| `internal/learning/store_test.go` | 신규 메서드 테이블 테스트 5종 |
| `cmd/elnath/commands.go` | registry 에 `"lessons": cmdLessons` |
| `cmd/elnath/cmd_daemon.go` | daemon 기동 시 `AutoRotateIfNeeded` 호출 |

Total new LOC 추정: ~400 (cmd) + ~250 (cmd test) + ~300 (store add) + ~250 (store tests) ≈ 1200 LOC.

## 5. Acceptance Criteria

- [ ] `go test -race ./internal/learning/... ./cmd/elnath/...` 통과
- [ ] `go vet ./...` 경고 없음
- [ ] `make build` 성공
- [ ] `elnath lessons list` / `show` / `clear` / `rotate` / `stats` 각 subcommand 정상 동작
- [ ] `clear --all -y` 실행 후 `lessons.jsonl` 비어 있음, archive 건드리지 않음
- [ ] `rotate --keep 3` 실행 후 active 최근 3개, 나머지 `lessons.archive.jsonl` 로 이동
- [ ] daemon 기동 로그에 entry 수 > 5000 또는 1MB 초과 시 `auto-rotated lessons` 메시지 출력 (또는 no-op 로그 없음)
- [ ] `--json` 출력이 각 1줄 JSON 으로 파싱 가능
- [ ] 중복 삭제/부분 매칭 시 ambiguous 에러 정확히 발생
- [ ] atomic rewrite: 강제 종료 시나리오 (unit test 에서 tempfile close 전 fsync 후 rename 이 한 번만 호출됨 검증)

## 6. Risk

| Risk | Mitigation |
|------|-----------|
| Rewrite 중 크래시로 active 소실 | tempfile → rename. rename 이전에 실패하면 원본 그대로. 재실행만 하면 됨 |
| archive 와 active 동시 쓰기 race | `s.mu` 가 둘 다 커버. 같은 프로세스 내 안전 |
| 잘못된 `--before` 파싱으로 전체 삭제 | parseTimeFlag 가 실패면 에러. zero time 은 Filter 에서 무시되므로 "부주의한 0 bound" 방지를 위해 명시적으로 비어있으면 필드 set X |
| --all 오타로 데이터 소실 | `-y` 없으면 `[y/N]` 프롬프트. 입력 tty 아니면 기본 거부 (stdin not tty → N 로 처리) |
| rotate 반복으로 archive 무한 성장 | 본 phase 에서는 archive prune 없음. 사용자가 수동 `rm lessons.archive.jsonl` 가능. 추후 `--archive-max-bytes` 옵션 고려 |
| ID prefix 충돌 (8자 SHA256) | 50k entries 에서도 충돌 확률 낮지만 Store 가 ambiguous detection 후 에러 발생. 사용자가 프롬프트 길이를 늘리면 해결 |
| 기존 lessons.jsonl 스키마 변경 | 이번에는 Lesson struct 변경 없음. ID/Created 이미 존재 |
| CLI test 에서 `os.Stdin` 프롬프트 블로킹 | `-y` 플래그로 회피. clear --all 테스트는 반드시 `-y` 또는 dry-run |

## 7. Future Work (Phase F-2+)

- `lessons export --format markdown` (wiki `wiki/self/lessons.md` 로 export)
- `lessons import <file>` (다른 elnath instance 와 수동 공유)
- Archive prune (age 또는 size 기준)
- `lessons replay` — 기존 lesson 을 다시 persona 에 적용 (실험적, selfState 복구 시)
- `lessons diff` — 두 시점 snapshot 비교
- TUI interactive browser (bubbletea) — 장기 후보

---

## Appendix A. UX Mockup

```
$ elnath lessons stats
Active: /Users/stello/.elnath/lessons.jsonl (14.2 KB)
Archive: /Users/stello/.elnath/lessons.archive.jsonl (128 entries, 68 KB)

Total (active): 187 lessons
Range: 2026-04-02T08:11Z → 2026-04-13T22:44Z

By confidence:
  high    102
  medium   52
  low      33

By topic (top 5):
  elnath-v2       44
  ml-strategies   31
  ops             18
  gate-retry      12
  telegram         9
```

```
$ elnath lessons list --confidence high --since 3d --newest --limit 3
2026-04-13  [high] elnath-v2       a1b2c3d4
  On elnath-v2: streaming summary reduces user confusion by 40%.

2026-04-12  [high] telegram        0f1e2d3c
  On telegram: PathGuard correctly rejects write to ~/.ssh (supported).

2026-04-11  [high] ml-strategies   7b8c9d0e
  On ml-strategies: 0.72 volatility gate correlates with profitability (supported).
```

```
$ elnath lessons clear --topic ml-strategies --dry-run
Would delete 31 lesson(s) (topic substring="ml-strategies"). Run without --dry-run to apply.
```

```
$ elnath lessons rotate --keep 100
Rotated 87 lesson(s).
Active:  /Users/stello/.elnath/lessons.jsonl (100 entries, 7.1 KB)
Archive: /Users/stello/.elnath/lessons.archive.jsonl (215 entries, 18.4 KB)
```
