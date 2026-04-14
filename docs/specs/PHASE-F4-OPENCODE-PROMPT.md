# Phase F-4 — OpenCode Prompt (Lessons BySource Stats + List Filter)

## Context

Elnath 는 Go 로 만든 자율 AI 비서 daemon (`/Users/stello/elnath/`, 브랜치 `feat/telegram-redesign`). Phase F-3 에서 `Lesson.Source` 값이 `"agent"` 단일에서 workflow-scoped 로 세분화됐다 (`agent:single`, `agent:team`, `agent:ralph`, `agent:autopilot`; legacy `"agent"` 는 하위호환). F-4 는 이 세분화를 CLI 운영 도구에 노출한다.

상세 spec: `docs/specs/PHASE-F4-LESSONS-BY-SOURCE.md`.

## Scope

파일 4개 건드린다:
- `internal/learning/store.go` — `Stats.BySource` 집계 + `Filter.Source` 매칭
- `internal/learning/store_test.go` — Summary/ListFiltered source 테스트
- `cmd/elnath/cmd_lessons.go` — `lessons list --source` + `lessons stats` 출력 섹션
- `cmd/elnath/cmd_lessons_test.go` — CLI 테스트

## Task

### 1. `internal/learning/store.go`

**`Stats` struct 확장**:
```go
type Stats struct {
    Total        int
    ByTopic      map[string]int            `json:"by_topic"`        // 기존 태그 있으면 유지
    ByConfidence map[string]int            `json:"by_confidence"`
    BySource     map[string]int            `json:"by_source"` // NEW
    OldestAt     time.Time                 // 기존 유지
    NewestAt     time.Time
    FileBytes    int64
}
```

json tag 는 기존 필드에 있는 스타일 그대로 따라라 (있으면 유지, 없으면 BySource 에도 생략 — Go default snake_case 없음). 기존 코드가 `json:"archive_bytes"` 처럼 struct embed 에서 수동 tag 사용하니 BySource 에도 `json:"by_source"` 명시.

**`Summary()` 수정**:
- 초기화 두 곳 모두 `BySource: make(map[string]int)` 추가 (nil path + 정상 path).
- lesson 루프 내부에 `stats.BySource[lesson.Source]++` 한 줄 추가.

**`Filter` 에 `Source string` 필드 추가**.

**`ListFiltered` 매칭**:
기존 Topic/Confidence/Since/Before 매칭 옆에 source 매칭 추가. 헬퍼 함수로 분리:

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

사용:
```go
if !matchSource(lesson.Source, filter.Source) {
    continue
}
```

### 2. `cmd/elnath/cmd_lessons.go`

**`lessonsList` 에 `--source` flag 추가** (기존 `--topic`, `--confidence` 패턴 그대로):
```go
source := fs.String("source", "", "")
// ...
lessons, err := store.ListFiltered(learning.Filter{
    Topic:      *topic,
    Confidence: *confidence,
    Source:     *source,
    Since:      since,
    Before:     before,
    Limit:      *limit,
    Reverse:    *newest,
})
```

**`lessonsStats` 출력에 "By source:" 섹션 추가**. "By topic (top 10):" 섹션 바로 위 또는 아래에 배치. 위쪽 (By confidence 다음) 이 자연스러움.

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

`topicCount` struct 정의 근처 (파일 하단) 에 `sourceCount` 추가:
```go
type sourceCount struct {
    Source string
    Count  int
}
```

**By topic 은 top 10 제한이 있지만 By source 는 제한 없음** — Source cardinality 는 7 이하 예상 (5 workflow × 1-2 variant + legacy + research).

### 3. JSON 출력

`lessonsStats` 의 JSON 경로는 `Stats` 필드를 embed 로 뱉으므로 `BySource` 는 자동 포함. json tag `"by_source"` 만 확인하면 됨.

## Constraints

- 기존 `Stats.ByTopic` / `Stats.ByConfidence` 집계 로직 불변. 새 맵만 추가.
- `Filter.Topic` / `Filter.Confidence` / `Filter.Since` / `Filter.Before` 매칭 로직 불변.
- Lesson 스키마 변경 금지. JSONL 파일 하위호환 필수.
- `lessons list` 의 다른 flag (`--topic`, `--confidence`, `--since`, `--before`, `--limit`, `--newest`, `--json`) 동작 불변.

## Tests

### `internal/learning/store_test.go`

- `TestSummary_BySource`: Append 5+ lessons with Sources `"agent"`, `"agent:single"`, `"agent:team"`, `"agent:ralph"`, `"research"` → Summary().BySource 각 값이 정확히 1 (또는 테스트에서 준 횟수).
- `TestListFiltered_SourceExact`: `Filter{Source: "agent:team"}` → `"agent:team"` lesson 만 반환.
- `TestListFiltered_SourcePrefix`: `Filter{Source: "agent:"}` → `"agent:single"`, `"agent:team"`, `"agent:ralph"` 반환, legacy `"agent"` 와 `"research"` 제외.
- `TestListFiltered_SourceLegacyExact`: `Filter{Source: "agent"}` → 정확히 `"agent"` 인 lesson 만 (legacy).
- `TestListFiltered_SourceEmpty`: `Filter{Source: ""}` → 필터 적용 안 됨 (모든 lesson).

### `cmd/elnath/cmd_lessons_test.go`

기존 테스트 파일 스타일 그대로 사용. store 를 temp dir 에 만들고 CLI 진입점을 직접 호출하는 패턴일 것. 다음 3 케이스 추가:

- `TestLessonsList_SourceFlag`: lessons.jsonl 에 여러 Source 섞고 `lessons list --source agent:ralph` 호출 → stdout 에 해당 source 만.
- `TestLessonsStats_IncludesBySource`: `lessons stats` 출력에 "By source:" 포함 + 각 source line 존재.
- `TestLessonsStats_JSONIncludesBySource`: `lessons stats --json` 출력 파싱 → `by_source` key 존재 + 맵.

## Verification gates

```bash
cd /Users/stello/elnath
go vet ./internal/learning/... ./cmd/elnath/...
go test -race ./internal/learning/... ./cmd/elnath/...
make build
```

전부 exit 0 이어야 완료.

## Scope limits

- `internal/orchestrator/**` 건드리지 말 것 — F-3 종료
- `internal/learning/agent_extractor.go` 건드리지 말 것 — F-3.1 에서 완료, 여기 source 생성 로직은 이미 올바름
- 다른 cmd/elnath/* 파일 (cmd_daemon.go, runtime.go 등) 건드리지 말 것
- Lesson JSONL 파일 migration 하지 말 것
- 새 stats aggregation 축 (예: Source × Confidence cross-tab) 추가 금지 — scope out

## 완료 보고 형식

작업 종료 시:
1. 수정/추가 파일 목록
2. `go test -race ./internal/learning/... ./cmd/elnath/...` PASS 요약
3. `go vet` + `make build` 결과
4. 예상 commit message (spec §6 템플릿 참고)

커밋은 하지 마라. stello 가 직접 commit.
