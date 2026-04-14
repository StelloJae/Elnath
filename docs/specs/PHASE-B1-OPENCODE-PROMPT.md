# OpenCode Delegation Prompt: Phase B-1 LB1 Full Inclusion Graph

아래 프롬프트를 OpenCode에 복사하여 실행한다. 3 phase로 나뉘어 있으며, 각 phase 완료 후 `go test -race ./internal/prompt/...` 와 `go vet ./...` 를 실행하여 검증한 뒤 다음 phase로 넘어간다.

---

## Phase 1: threat_scan.go + ContextFilesNode + SkillCatalogNode (stub)

```
elnath 프로젝트 (`/Users/stello/elnath/`, feat/telegram-redesign 브랜치)에서 Phase B-1 작업을 시작한다.

목표: internal/prompt/ 에 신규 노드 2개 + 인프라 1개를 추가한다.

### 기존 패턴 참고

모든 노드는 이 인터페이스를 구현한다 (internal/prompt/node.go):

```go
type Node interface {
    Name() string
    Priority() int
    Render(ctx context.Context, state *RenderState) (string, error)
}
```

생성자는 `New<NodeType>(priority int, ...추가파라미터) *<NodeType>` 패턴. nil receiver와 nil state를 graceful하게 처리. identity_node.go를 참고.

### 작업 1: internal/prompt/threat_scan.go + threat_scan_test.go

Hermes의 context file injection 탐지를 Go로 포팅한다.

exported 함수:
```go
func ScanContent(content, filename string) (cleaned string, blocked bool)
```

2-layer 탐지:

Layer 1 — invisible unicode 탐지:
- U+200B (zero-width space)
- U+200C (zero-width non-joiner)
- U+200D (zero-width joiner)
- U+2060 (word joiner)
- U+FEFF (BOM)
- U+202A ~ U+202E (BiDi overrides)

content에 위 문자가 하나라도 있으면 blocked.

Layer 2 — regex 패턴 (case-insensitive):
1. `ignore\s+(?:\w+\s+)*(?:previous|all|above|prior)\s+(?:\w+\s+)*instructions` → "prompt_injection"
2. `do\s+not\s+tell\s+the\s+user` → "deception_hide"
3. `system\s+prompt\s+override` → "sys_prompt_override"
4. `disregard\s+(your|all|any)\s+(instructions|rules|guidelines)` → "disregard_rules"
5. `act\s+as\s+(if|though)\s+you\s+(have\s+no|don't\s+have)\s+(restrictions|limits|rules)` → "bypass_restrictions"
6. `<!--[^>]*(?:ignore|override|system|secret|hidden)[^>]*-->` → "html_comment_injection"
7. `<\s*div\s+style\s*=\s*["'].*display\s*:\s*none` → "hidden_div"
8. `curl\s+[^\n]*\$\{?\w*(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API)` → "exfil_curl"
9. `cat\s+[^\n]*(\.env|credentials|\.netrc|\.pgpass)` → "read_secrets"

ANY match → return `"[BLOCKED: <filename> contained potential prompt injection (<matched_pattern_ids>). Content not loaded.]", true`
No match → return `content, false`

매치 시 `slog.Warn("threat_scan: blocked content", "filename", filename, "patterns", matchedIDs)` 로 로그.

regex는 init() 또는 package-level var로 한 번만 컴파일 (regexp.MustCompile).

테스트 (threat_scan_test.go):
- 정상 markdown content → 통과 (blocked=false)
- 빈 content → 통과
- invisible unicode (U+200B) 포함 → blocked, "[BLOCKED: ...]" 메시지
- 각 regex 패턴 개별 테스트 (9개) → 각각 blocked
- 복합 패턴 (unicode + regex) → blocked, 패턴 ID 모두 포함
- 대소문자 혼합 ("Ignore ALL Prior Instructions") → blocked

### 작업 2: internal/prompt/context_files_node.go + context_files_node_test.go

CLAUDE.md / AGENTS.md / .elnath/project.yaml auto-discovery.

```go
type ContextFilesNode struct {
    priority int
}
func NewContextFilesNode(priority int) *ContextFilesNode
func (n *ContextFilesNode) Name() string          // "context_files"
func (n *ContextFilesNode) Priority() int
func (n *ContextFilesNode) Render(ctx context.Context, state *RenderState) (string, error)
```

Render 로직:
1. state.WorkDir 이 빈 문자열이면 → ""
2. state.BenchmarkMode 이면 → ""
3. state.WorkDir에서 시작, 상위 디렉토리를 최대 10레벨 탐색
4. git root 발견 시 (.git 디렉토리 존재) 해당 레벨까지만 (그 위로 안 감)
5. 각 레벨에서 다음 파일 검색 (first-match per filename group):
   - `.elnath/project.yaml`
   - `CLAUDE.md` 또는 `claude.md`
   - `AGENTS.md` 또는 `agents.md`
6. 발견된 각 파일을 os.ReadFile로 읽기 (실패 → slog.Debug + skip)
7. 단일 파일 8KB 초과 → 8KB에서 자르고 "\n[truncated]" 추가
8. ScanContent(content, filename)으로 threat scan
9. blocked → 해당 파일만 "[BLOCKED: ...]"으로 대체 (다른 파일은 유지)
10. 전체 24KB 초과 → 마지막 파일부터 제거
11. 출력 포맷:
```
<<context_files>>
--- CLAUDE.md ---
<file content>

--- AGENTS.md ---
<file content>
<</context_files>>
```
12. 발견된 파일이 0개면 → ""

테스트:
- t.TempDir()에 CLAUDE.md 생성 → Render 출력에 "<<context_files>>" + 내용 포함
- 하위 디렉토리에서 parent의 CLAUDE.md 발견
- .git이 있는 디렉토리에서 멈추는지 확인 (상위로 안 감)
- injection 포함 CLAUDE.md → "[BLOCKED: ...]" 포함, 나머지 파일은 정상
- 파일 없는 디렉토리 → ""
- BenchmarkMode → ""
- 8KB 초과 파일 → "[truncated]" 포함

### 작업 3: internal/prompt/skill_catalog_node.go + skill_catalog_node_test.go

Phase C stub.

```go
type SkillCatalogNode struct {
    priority int
}
func NewSkillCatalogNode(priority int) *SkillCatalogNode
func (n *SkillCatalogNode) Name() string          // "skill_catalog"
func (n *SkillCatalogNode) Priority() int
func (n *SkillCatalogNode) Render(_ context.Context, _ *RenderState) (string, error)
```

Render: 항상 `"", nil` 반환.

테스트:
- Render → ("", nil)
- Name() → "skill_catalog"
- Priority() → 설정값

### 검증

모든 파일 작성 후:
```bash
go test -race ./internal/prompt/...
go vet ./internal/prompt/...
```

모두 통과해야 한다.
```

---

## Phase 2: SelfStateNode + MemoryContextNode + RenderState 확장

```
Phase B-1 Phase 2. Phase 1에서 threat_scan, ContextFilesNode, SkillCatalogNode가 완성됐다.

### 작업 1: internal/prompt/node.go — RenderState 확장

RenderState struct에 2개 필드 추가:

```go
type RenderState struct {
    // ... 기존 필드 유지 ...
    DaemonMode   bool
    MessageCount int
}
```

기존 필드는 절대 변경하지 않는다. 끝에 추가만.

### 작업 2: internal/prompt/self_state_node.go + self_state_node_test.go

운영 자기인식 (dynamic state). IdentityNode(정적 정체성)와 구분.

```go
type SelfStateNode struct {
    priority int
}
func NewSelfStateNode(priority int) *SelfStateNode
func (n *SelfStateNode) Name() string          // "self_state"
func (n *SelfStateNode) Priority() int
func (n *SelfStateNode) Render(_ context.Context, state *RenderState) (string, error)
```

Render 출력:
```
Operational state:
- Session: <state.SessionID 또는 "(new)">
- Messages in conversation: <state.MessageCount>
- Mode: <"daemon" if state.DaemonMode else "interactive">
- Working directory: <state.WorkDir 또는 "(none)">
- Current time: <time.Now().UTC().Format(time.RFC3339)>
```

nil state → ""
nil receiver → ""

테스트:
- 정상 state → 모든 필드 출력 확인 (strings.Contains)
- SessionID="" → "(new)" 포함
- DaemonMode=true → "daemon" 포함
- DaemonMode=false → "interactive" 포함
- nil state → ""
- time.RFC3339 포맷 포함 확인 (regex 매칭)

### 작업 3: internal/prompt/memory_context_node.go + memory_context_node_test.go

이전 세션 context 주입.

```go
type MemoryContextNode struct {
    priority   int
    maxEntries int
    maxChars   int
}
func NewMemoryContextNode(priority, maxEntries, maxChars int) *MemoryContextNode
func (n *MemoryContextNode) Name() string          // "memory_context"
func (n *MemoryContextNode) Priority() int
func (n *MemoryContextNode) Render(ctx context.Context, state *RenderState) (string, error)
```

Render 로직:
1. BenchmarkMode → ""
2. state.WikiIdx == nil → ""
3. WikiIdx.Search(ctx, "session summary memory context") 호출 (wiki.Index의 Search 메서드 사용)
4. 결과를 maxEntries개로 제한
5. 각 결과의 Snippet/Content를 모아 maxChars 이내로 truncate
6. 출력:
```
<<memory_context>>
[page title 1]
snippet content...

[page title 2]
snippet content...
<</memory_context>>
```
7. 검색 결과 0개 → ""

주의: wiki.Index 인터페이스의 실제 Search 메서드 시그니처를 확인하고 맞춰라. `internal/wiki/index.go` 를 읽어볼 것.

테스트:
- WikiIdx가 결과 반환 → 렌더 확인 (mock이 필요하면 인터페이스 확인 후 작성)
- maxEntries 제한 동작
- maxChars truncate
- WikiIdx nil → ""
- BenchmarkMode → ""
- 검색 결과 0건 → ""

### 검증

```bash
go test -race ./internal/prompt/...
go vet ./internal/prompt/...
```
```

---

## Phase 3: runtime.go 통합 + 전체 검증

```
Phase B-1 Phase 3. Phase 1-2에서 모든 신규 노드가 완성됐다. 이제 runtime에 통합한다.

### 작업 1: cmd/elnath/runtime.go — executionRuntime 확장

1. `executionRuntime` struct에 `daemonMode bool` 필드 추가.

2. `newExecutionRuntime` 함수 파라미터에 `daemonMode bool` 추가.
   해당 값을 struct에 저장.

3. 노드 등록 변경 (기존 등록 라인 교체):

```go
b := prompt.NewBuilder()
b.Register(prompt.NewIdentityNode(100))
b.Register(prompt.NewContextFilesNode(95))
b.Register(prompt.NewPersonaNode(90))
b.Register(prompt.NewSelfStateNode(85))
b.Register(prompt.NewToolCatalogNode(80))
b.Register(prompt.NewModelGuidanceNode(70))
b.Register(prompt.NewSkillCatalogNode(65))
b.Register(prompt.NewDynamicBoundaryNode())
b.Register(prompt.NewWikiRAGNode(60, 3))
b.Register(prompt.NewMemoryContextNode(55, 5, 1200))
b.Register(prompt.NewProjectContextNode(50))
b.Register(prompt.NewBrownfieldNode(40))
b.Register(prompt.NewSessionSummaryNode(30, 5, 800))
```

4. RenderState 생성 부분에 새 필드 추가:

```go
renderState := &prompt.RenderState{
    // ... 기존 필드 유지 ...
    DaemonMode:   rt.daemonMode,
    MessageCount: len(prepared),
}
```

### 작업 2: cmd/elnath/cmd_run.go — daemonMode=false

`newExecutionRuntime` 호출 시 `daemonMode: false` 전달. 기존 호출 패턴을 확인하고 파라미터 추가.

### 작업 3: cmd/elnath/cmd_daemon.go — daemonMode=true

`newExecutionRuntime` 호출 시 `daemonMode: true` 전달.

### 작업 4: 기존 테스트 수정

`cmd/elnath/runtime_test.go` 에 `newExecutionRuntime` 호출이 있다면 `daemonMode` 파라미터 추가.

### 전체 검증

```bash
go test -race ./internal/prompt/...
go test -race ./cmd/elnath/...
go vet ./...
make build
```

모두 통과해야 한다.

최종 확인: `make build` 후 생성된 바이너리로 CLAUDE.md가 있는 디렉토리에서 `elnath run` 실행하여 system prompt에 CLAUDE.md 내용이 포함되는지 확인.
```
