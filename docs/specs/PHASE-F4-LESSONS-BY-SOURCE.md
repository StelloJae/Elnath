# Phase F-4 Lessons BySource Stats + List Filter

**Predecessor:** Phase F-3 (Multi-workflow Learning Integration) DONE
**Status:** SPEC (locked)
**Scope:** ~150 LOC across 2 files prod + 2 files test
**Branch:** `feat/telegram-redesign`

---

## 0. Goal

F-3.1 이 `Lesson.Source` 를 `"agent"` 단일값에서 `"agent:single"/"agent:team"/"agent:ralph"/"agent:autopilot"` 로 세분화했으나 운영 도구 (`lessons stats`, `lessons list`) 가 이 신호를 활용 못 함. F-4 가 stats 집계 + list filter 로 이 격차를 메운다.

**Why**: 학습 데이터 축적이 늘어나는 시점. 어느 workflow 가 lesson 을 많이 찍는지, ralph instability 가 어느 topic 에서 발생하는지 같은 분석이 CLI 한 줄로 돼야 한다.

---

## 1. Decisions

| ID | Question | Answer | Rationale |
|----|----------|--------|-----------|
| Q1 | Stats 집계 구조 | `Stats.BySource map[string]int` 신설 | 기존 `ByTopic/ByConfidence` 패턴 그대로 |
| Q2 | 출력 포맷 | "By source:" 섹션 전체 표시, Count 내림차순 | Source cardinality 작음 (≤ 7) → top-N 제한 불필요 |
| Q3 | `lessons list --source` 매칭 | **exact + trailing-colon prefix** 이중 규칙 | `--source agent:` 로 F-3 세분화 전체 선택, `--source agent` 로 legacy 만, `--source agent:team` 로 정확 매치 |
| Q4 | 빈 source 표시 | stats 에 빈 문자열 key 있으면 `"(empty)"` 라벨로 노출 | 이론상 없어야 하지만 데이터 방어 |

---

## 2. Implementation

### 2.1 `internal/learning/store.go`

**Stats 확장**:
```go
type Stats struct {
    Total        int
    ByTopic      map[string]int
    ByConfidence map[string]int
    BySource     map[string]int // NEW
    OldestAt     time.Time
    NewestAt     time.Time
    FileBytes    int64
}
```

`Summary()` 초기화/집계 양쪽에 `BySource` 추가:
```go
stats := Stats{
    ByTopic:      make(map[string]int),
    ByConfidence: make(map[string]int),
    BySource:     make(map[string]int),
}
// loop 내부
stats.BySource[lesson.Source]++
```

빈 Stats return path (`path == ""` nil store) 도 `BySource: make(...)` 채우기.

**Filter 확장**:
```go
type Filter struct {
    // 기존 필드 보존
    Source string // NEW
}
```

`ListFiltered` 의 필터 루프 내부에 `matchSource` 체크 추가. 매칭 규칙:
- `filter.Source == ""` → 매칭 통과 (필터 미적용)
- `strings.HasSuffix(filter.Source, ":")` → `strings.HasPrefix(lesson.Source, filter.Source)` (prefix 매치)
- 그 외 → `lesson.Source == filter.Source` (exact)

구현 헬퍼:
```go
func matchSource(lessonSource, filter string) bool {
    if filter == "" {
        return true
    }
    if strings.HasSuffix(filter, ":") {
        return strings.HasPrefix(lessonSource, filter)
    }
    return lessonSource == filter
}
```

### 2.2 `cmd/elnath/cmd_lessons.go`

**`lessonsList` 에 `--source` flag 추가**:
```go
source := fs.String("source", "", "")
// ...
lessons, err := store.ListFiltered(learning.Filter{
    // 기존 필드
    Source: *source,
})
```

**`lessonsStats` 출력에 "By source:" 섹션 추가**:
```go
fmt.Println()
fmt.Println("By source:")
sources := make([]sourceCount, 0, len(stats.BySource))
for src, count := range stats.BySource {
    label := src
    if label == "" {
        label = "(empty)"
    }
    sources = append(sources, sourceCount{Source: label, Count: count})
}
sort.Slice(sources, func(i, j int) bool {
    if sources[i].Count == sources[j].Count {
        return sources[i].Source < sources[j].Source
    }
    return sources[i].Count > sources[j].Count
})
for _, src := range sources {
    fmt.Printf("  %-18s %d\n", src.Source, src.Count)
}
```

`topicCount` 옆에 `sourceCount` struct 추가:
```go
type sourceCount struct {
    Source string
    Count  int
}
```

JSON 출력은 `Stats` struct 에 `BySource` 가 자동 embed 되므로 변경 불필요 (현재 `json:"archive_bytes"` 처럼 tag 만 확인; `BySource` 에 `json:"by_source"` tag 추가).

### 2.3 Help text 업데이트

`printLessonsUsage` 의 `list` 항목 설명은 그대로 (subcommand description 만 있어 flag 별도 문서 안 함). flag 사용법은 `--source <value>` 자체로 충분 self-documenting.

---

## 3. Tests

### 3.1 `internal/learning/store_test.go`

- `TestSummary_BySource`: 5 lesson 을 다양한 Source 값 (`"agent"`, `"agent:single"`, `"agent:team"`, `"agent:ralph"`, `"research"`) 으로 Append → `Summary().BySource` 가 정확히 집계.
- `TestListFiltered_SourceExact`: `Filter{Source: "agent:team"}` → `"agent:team"` 만 반환.
- `TestListFiltered_SourcePrefix`: `Filter{Source: "agent:"}` → `agent:*` 전부 반환, legacy `"agent"` 및 `"research"` 제외.
- `TestListFiltered_SourceLegacyOnly`: `Filter{Source: "agent"}` → `"agent"` 정확 매치 1 개만.
- `TestListFiltered_SourceEmpty`: `Filter{Source: ""}` → 필터 미적용.

### 3.2 `cmd/elnath/cmd_lessons_test.go`

- `TestLessonsList_SourceFlag`: `lessons list --source agent:ralph` → stdout 에 해당 source 만 출력.
- `TestLessonsStats_IncludesBySource`: stats 출력에 "By source:" 섹션 + 각 source count 존재.
- `TestLessonsStats_JSONIncludesBySource`: `--json` 출력이 `by_source` key 를 포함.

---

## 4. Scope boundaries

**In scope**:
- Stats.BySource + Summary 집계
- Filter.Source + matchSource 헬퍼
- `lessons list --source` flag
- `lessons stats` 출력 섹션
- JSON tag

**Out of scope**:
- 새 stats sub-aggregation (예: Source × Confidence 교차)
- `--source` 의 glob/regex 지원 (미래 필요 시)
- Lesson 스키마 변경
- Rotate/Clear 에 Source 필터 추가 (별도 phase 후보)

---

## 5. Verification gates

```bash
cd /Users/stello/elnath
go vet ./internal/learning/... ./cmd/elnath/...
go test -race ./internal/learning/... ./cmd/elnath/...
make build
```

---

## 6. Commit message template

```
feat: phase F-4 lessons by-source stats + list filter

Surface the F-3 workflow-scoped Source values in operational tooling.
Stats.BySource aggregates lesson counts per Source ("agent:single",
"agent:team", etc., plus legacy "agent"). `lessons list --source`
supports exact match ("agent:team") and trailing-colon prefix
("agent:") for selecting all workflow-scoped sources while excluding
legacy. `lessons stats` renders a "By source:" section sorted by count.
```

---

## 7. OpenCode prompt

See `docs/specs/PHASE-F4-OPENCODE-PROMPT.md`.
