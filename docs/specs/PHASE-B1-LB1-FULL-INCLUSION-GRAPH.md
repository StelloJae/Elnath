# Phase B-1: LB1 Full Inclusion Graph

**Status:** SPEC READY  
**Predecessor:** Phase A (LB4 + LB3) DONE  
**Successor:** Phase C-1 (Skill System)  
**Branch:** `feat/telegram-redesign`  
**Ref:** Superiority Design v2.2 §6 "Session 2.1 — LBB1 Full Inclusion Graph"

---

## 1. Goal

`internal/prompt/` 패키지의 Inclusion Graph를 완성한다. 현재 9개 노드가 존재하며, 4개 신규 노드 + 1개 인프라(threat scan) + RenderState 확장 + runtime 등록을 추가해 full graph를 달성한다.

## 2. Current State

### 등록된 노드 (runtime.go:182-190)

| # | Node | Priority | 역할 |
|---|------|----------|------|
| 1 | IdentityNode | 100 | name, mission, vibe, persona params |
| 2 | PersonaNode | 90 | PersonaExtra (preset guidance) |
| 3 | ToolCatalogNode | 80 | 사용 가능 tool 목록 |
| 4 | ModelGuidanceNode | 70 | provider별 LLM 가이드 |
| 5 | DynamicBoundaryNode | 999 | `__DYNAMIC_BOUNDARY__` marker |
| 6 | WikiRAGNode | 60 | wiki FTS5 검색 결과 |
| 7 | ProjectContextNode | 50 | git branch/remote + likely files |
| 8 | BrownfieldNode | 40 | 실행 규율 (코딩 원칙) |
| 9 | SessionSummaryNode | 30 | 최근 N개 user 메시지 |

### 빠진 노드

| Node | Priority | 역할 |
|------|----------|------|
| **ContextFilesNode** | 95 | CLAUDE.md / AGENTS.md / .elnath/project.yaml auto-discovery + injection |
| **SelfStateNode** | 85 | 운영 상태: session ID, message count, mode, timestamp |
| **SkillCatalogNode** | 65 | **stub** — Phase C에서 구현. 빈 문자열 반환 |
| **MemoryContextNode** | 55 | 이전 세션 요약, 학습된 선호도, cross-session context |

### 빠진 인프라

| Component | 역할 |
|-----------|------|
| **threat_scan.go** | Hermes 패턴 포트: invisible unicode + regex injection 탐지 |

## 3. Deliverables

### 3.1 New Files

#### `internal/prompt/threat_scan.go`

Hermes `_scan_context_content` 패턴을 Go로 포팅한다.

```go
// ScanContent checks content for prompt injection patterns.
// Returns cleaned content or a blocking message if threats detected.
func ScanContent(content, filename string) (string, bool)
```

**2-layer detection:**

1. **Invisible unicode** — Zero-width space (U+200B, 200C, 200D), ZWNBSP (U+2060), BOM (U+FEFF), BiDi overrides (U+202A-E)
2. **Regex patterns** (case-insensitive):

| Pattern | ID |
|---------|----|
| `ignore\s+(?:\w+\s+)*(?:previous\|all\|above\|prior)\s+(?:\w+\s+)*instructions` | prompt_injection |
| `do\s+not\s+tell\s+the\s+user` | deception_hide |
| `system\s+prompt\s+override` | sys_prompt_override |
| `disregard\s+(your\|all\|any)\s+(instructions\|rules\|guidelines)` | disregard_rules |
| `act\s+as\s+(if\|though)\s+you\s+(have\s+no\|don't\s+have)\s+(restrictions\|limits\|rules)` | bypass_restrictions |
| `<!--[^>]*(?:ignore\|override\|system\|secret\|hidden)[^>]*-->` | html_comment_injection |
| `<\s*div\s+style\s*=\s*["'].*display\s*:\s*none` | hidden_div |
| `curl\s+[^\n]*\$\{?\w*(KEY\|TOKEN\|SECRET\|PASSWORD\|CREDENTIAL\|API)` | exfil_curl |
| `cat\s+[^\n]*(\.env\|credentials\|\.netrc\|\.pgpass)` | read_secrets |

**Behavior:**
- ANY match → `return "[BLOCKED: <filename> contained potential prompt injection (<pattern_ids>). Content not loaded.]", true`
- No match → `return content, false`
- `slog.Warn` 으로 차단 이벤트 로그

#### `internal/prompt/threat_scan_test.go`

테이블 기반 테스트:
- 정상 CLAUDE.md → 통과
- invisible unicode 포함 → 차단
- 각 regex 패턴 → 차단
- 빈 content → 통과
- 복합 패턴 (unicode + regex) → 차단, 모든 pattern ID 포함

#### `internal/prompt/context_files_node.go`

CLAUDE.md auto-discovery + threat scan 적용.

```go
type ContextFilesNode struct {
    priority int
}

func NewContextFilesNode(priority int) *ContextFilesNode
```

**Discovery 로직:**
1. `state.WorkDir` 에서 시작
2. Git root까지 상위 디렉토리 탐색 (최대 10레벨)
3. 각 레벨에서 다음 파일 검색 (first-match per filename):
   - `.elnath/project.yaml`
   - `CLAUDE.md` / `claude.md`
   - `AGENTS.md` / `agents.md`
4. 각 파일 내용을 `ScanContent()` 로 검증
5. 차단되지 않은 content를 `<<context_files>>...<</context_files>>` 으로 감싸 반환
6. 각 파일은 `--- <filename> ---` 구분자로 분리

**조건:**
- `state.WorkDir == ""` → 빈 문자열
- `state.BenchmarkMode` → 빈 문자열 (벤치마크에서 불필요)
- 발견된 파일 없음 → 빈 문자열
- 파일 읽기 실패 → skip (에러 아님, slog.Debug)
- 단일 파일 최대 8KB, 전체 최대 24KB (초과 시 truncate + `[truncated]` 표시)

#### `internal/prompt/context_files_node_test.go`

- temp dir에 CLAUDE.md 생성 → Render 결과에 포함 확인
- nested dir에서 parent의 CLAUDE.md 발견 확인
- injection 포함 CLAUDE.md → `[BLOCKED: ...]` 메시지 확인
- 파일 없음 → 빈 문자열
- BenchmarkMode → 빈 문자열
- 8KB 초과 → truncate 확인

#### `internal/prompt/self_state_node.go`

운영 자기인식 렌더링. IdentityNode(정적 정체성)와 구분되는 동적 상태.

```go
type SelfStateNode struct {
    priority int
}

func NewSelfStateNode(priority int) *SelfStateNode
```

**렌더링 내용:**
```
Operational state:
- Session: <session_id>
- Messages in conversation: <count>
- Mode: <daemon|interactive>
- Working directory: <workdir>
- Current time: <RFC3339>
```

**조건:**
- `state == nil` → 빈 문자열
- `state.SessionID == ""` → `Session: (new)` 표시

#### `internal/prompt/self_state_node_test.go`

- 정상 RenderState → 모든 필드 출력 확인
- 빈 SessionID → "(new)" 표시
- nil state → 빈 문자열
- DaemonMode true/false → "daemon"/"interactive"

#### `internal/prompt/memory_context_node.go`

이전 세션 context 주입. 현재는 wiki에서 session 관련 page를 가져오는 방식.

```go
type MemoryContextNode struct {
    priority   int
    maxEntries int
    maxChars   int
}

func NewMemoryContextNode(priority, maxEntries, maxChars int) *MemoryContextNode
```

**렌더링 로직:**
1. `state.WikiIdx` 가 nil이면 빈 문자열
2. Wiki에서 `tag:memory` 또는 `tag:session-summary` 페이지 검색
3. 최근 `maxEntries` 개 (기본 5) 를 시간순 정렬
4. 전체 `maxChars` (기본 1200) 이내로 truncate
5. `<<memory_context>>...<</memory_context>>` 으로 감싸 반환

**조건:**
- `state.BenchmarkMode` → 빈 문자열
- wiki 없음 → 빈 문자열
- 메모리 페이지 없음 → 빈 문자열

#### `internal/prompt/memory_context_node_test.go`

- mock WikiIdx로 memory page 반환 → 렌더 확인
- maxEntries 제한 동작
- maxChars truncate 동작
- wiki nil → 빈 문자열
- BenchmarkMode → 빈 문자열

#### `internal/prompt/skill_catalog_node.go`

Phase C 의존 stub. SkillCatalog이 구현되면 여기에 연결.

```go
type SkillCatalogNode struct {
    priority int
}

func NewSkillCatalogNode(priority int) *SkillCatalogNode
```

**Render:** 항상 빈 문자열 반환. Phase C에서 skill registry를 받아 catalog을 렌더링하도록 확장.

#### `internal/prompt/skill_catalog_node_test.go`

- Render → 빈 문자열
- Name() → "skill_catalog"
- Priority() → 설정값

### 3.2 Modified Files

#### `internal/prompt/node.go` — RenderState 확장

```go
type RenderState struct {
    // ... existing fields ...
    DaemonMode   bool   // true if running as daemon
    MessageCount int    // number of messages in current conversation
}
```

2개 필드 추가. 기존 필드는 변경 없음.

#### `cmd/elnath/runtime.go` — 노드 등록 + RenderState 채우기

**등록 (line ~182):**

```go
b := prompt.NewBuilder()
b.Register(prompt.NewIdentityNode(100))
b.Register(prompt.NewContextFilesNode(95))    // NEW
b.Register(prompt.NewPersonaNode(90))
b.Register(prompt.NewSelfStateNode(85))       // NEW
b.Register(prompt.NewToolCatalogNode(80))
b.Register(prompt.NewModelGuidanceNode(70))
b.Register(prompt.NewSkillCatalogNode(65))    // NEW (stub)
b.Register(prompt.NewDynamicBoundaryNode())
b.Register(prompt.NewWikiRAGNode(60, 3))
b.Register(prompt.NewMemoryContextNode(55, 5, 1200)) // NEW
b.Register(prompt.NewProjectContextNode(50))
b.Register(prompt.NewBrownfieldNode(40))
b.Register(prompt.NewSessionSummaryNode(30, 5, 800))
```

**RenderState 채우기 (line ~279):**

```go
renderState := &prompt.RenderState{
    // ... existing fields ...
    DaemonMode:   rt.daemonMode,    // NEW: executionRuntime에 필드 추가
    MessageCount: len(prepared),     // NEW
}
```

`executionRuntime` struct에 `daemonMode bool` 필드 추가. `newExecutionRuntime` 파라미터에 추가.

#### `cmd/elnath/cmd_run.go` — daemonMode=false 전달

`newExecutionRuntime` 호출 시 `daemonMode: false`.

#### `cmd/elnath/cmd_daemon.go` — daemonMode=true 전달

`newExecutionRuntime` 호출 시 `daemonMode: true`.

## 4. Priority Map (Complete)

Token budget pressure 시 낮은 priority부터 drop.

| Priority | Node | Drop 안전성 |
|----------|------|-------------|
| 999 | DynamicBoundary | NEVER drop |
| 100 | Identity | NEVER drop (priority ≥ 999 체크) |
| 95 | ContextFiles | safe — 없어도 기본 동작 |
| 90 | Persona | safe |
| 85 | SelfState | safe |
| 80 | ToolCatalog | risky — tool 목록 모르면 성능 저하 |
| 70 | ModelGuidance | safe |
| 65 | SkillCatalog | safe (stub) |
| 60 | WikiRAG | safe |
| 55 | MemoryContext | safe — 첫 번째 drop 후보군 |
| 50 | ProjectContext | safe |
| 40 | Brownfield | safe — 하지만 품질 저하 |
| 30 | SessionSummary | **첫 번째 drop** |

**주의:** `applyBudget`은 `priority >= 999`를 보호한다. Identity(100)는 보호 대상이 아니므로, 극단적 budget pressure에서 drop될 수 있다. 이 동작이 의도적이라면 유지. Identity도 보호하려면 priority를 999로 올리거나 builder에서 name-based 보호 로직을 추가.

## 5. Acceptance Criteria

- [ ] `go test -race ./internal/prompt/...` — 모든 테스트 통과
- [ ] `go vet ./...` — 경고 없음
- [ ] `make build` — 빌드 성공
- [ ] CLAUDE.md가 있는 repo에서 `elnath run` 실행 시 system prompt에 CLAUDE.md 내용 포함
- [ ] injection 패턴이 포함된 CLAUDE.md → `[BLOCKED: ...]` 메시지
- [ ] 13개 노드 등록 확인 (기존 9 + 신규 4)
- [ ] token budget 0일 때 모든 노드 렌더링
- [ ] token budget 제한 시 priority 낮은 노드부터 drop
- [ ] SkillCatalogNode.Render → 항상 빈 문자열
- [ ] BenchmarkMode에서 ContextFiles, MemoryContext 스킵

## 6. Out of Scope

- SkillCatalog 실제 구현 (Phase C)
- MemoryContext의 wiki tag 기반 필터링이 현재 wiki.Index API에 없으면, 단순 keyword 검색 `"session summary"` 으로 대체. Phase C에서 tag 시스템 추가 시 업그레이드.
- ProjectContextNode의 CLAUDE.md 검색 로직 — ContextFilesNode가 전담. ProjectContextNode은 기존대로 git + file hints만.
- ThreatScan의 cron-specific 패턴 (authorized_keys, sudoers, rm -rf /) — context file 전용 패턴만 포팅.

## 7. Risk

| Risk | Mitigation |
|------|-----------|
| ContextFilesNode의 상위 디렉토리 탐색이 느림 | 최대 10레벨 + early exit (git root 발견 시 중단) |
| 8KB file cap이 큰 CLAUDE.md를 잘라냄 | 경고 로그 + `[truncated]` 표시. 사용자가 인지 가능 |
| threat_scan false positive | regex가 보수적 (Hermes 검증 완료 패턴). slog.Warn으로 차단 이벤트 기록 |
| MemoryContext wiki 검색이 노이즈 반환 | maxEntries=5 + maxChars=1200 제한. 향후 tag 필터링으로 정밀화 |
