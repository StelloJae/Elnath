# Phase C-2 Worker 2: Layer 1 — create_skill Tool + Guidance Node

## 역할
에이전트가 대화 중 반복 패턴을 감지하여 skill 생성을 제안할 수 있도록, `create_skill` 도구와 system prompt 가이드를 구현한다.

**신규 파일만 생성한다.** 기존 파일 수정 금지 (W1이 담당).

## 선행 조건
W1의 Tasks 1-5 완료 필요 (Skill struct 확장, Creator, Tracker, interfaces.go).

## 선행 지식

### Tool 인터페이스 (반드시 7개 메서드 구현)
```go
// internal/tools/tool.go
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage
    Execute(ctx context.Context, params json.RawMessage) (*Result, error)
    IsConcurrencySafe(params json.RawMessage) bool
    Reversible() bool
    Scope(params json.RawMessage) ToolScope
    ShouldCancelSiblingsOnError() bool
}

type Result struct {
    Output  string
    IsError bool
}
func ErrorResult(msg string) *Result
func SuccessResult(output string) *Result
```

### Creator API (W1이 생성)
```go
// internal/skill/creator.go
func NewCreator(store *wiki.Store, tracker *Tracker, registry *Registry) *Creator
func (c *Creator) Create(CreateParams) (*Skill, error)
func (c *Creator) Delete(name string) error

type CreateParams struct {
    Name, Description, Trigger string
    RequiredTools []string
    Model, Prompt, Status, Source string
    SourceSessions []string
}
```

### Registry API (기존)
```go
func (r *Registry) List() []*Skill
func (r *Registry) Get(name string) (*Skill, bool)
```

### Prompt Node 인터페이스
```go
// internal/prompt/ 패턴
type Node interface {
    Name() string
    Priority() int
    Render(ctx context.Context, state *RenderState) (string, error)
}

type RenderState struct {
    BenchmarkMode bool
    // ... other fields
}
```

## 작업

### Task 1: create_skill 도구 구현
**파일:** `internal/tools/skill_tool.go`, `internal/tools/skill_tool_test.go` (신규)

```go
type SkillTool struct {
    creator  *skill.Creator
    registry *skill.Registry
}
func NewSkillTool(creator *skill.Creator, registry *skill.Registry) *SkillTool
```

**Schema:**
```json
{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["create", "list", "delete"]},
    "name": {"type": "string"},
    "description": {"type": "string"},
    "trigger": {"type": "string"},
    "required_tools": {"type": "array", "items": {"type": "string"}},
    "prompt": {"type": "string"}
  },
  "required": ["action"]
}
```

**Execute 분기:**
- `"create"`: name+prompt 필수 검증 → `creator.Create(params)`, source="hint", status="active"
- `"list"`: `registry.List()` → 포맷 텍스트
- `"delete"`: name 필수 검증 → `creator.Delete(name)`

**Metadata:**
- `IsConcurrencySafe`: true
- `Reversible`: true
- `ShouldCancelSiblingsOnError`: false
- `Scope`: list→Read only, create/delete→Read+Write

**테스트:** Create/List/Delete 각각, 빈 name 에러, 빈 prompt 에러.

### Task 2: SkillGuidanceNode 구현
**파일:** `internal/prompt/skill_guidance_node.go`, `internal/prompt/skill_guidance_node_test.go` (신규)

Priority 64 (SkillCatalogNode 65 바로 아래). Name: "skill_guidance".

Render 출력:
```
You have a create_skill tool. Use it when:
- You notice a repeated pattern across sessions
- The user says "make this a skill" or similar
- A multi-step workflow could be reusable

When suggesting a skill, briefly explain what it would do before creating it.
Do not suggest skills for one-time tasks.
```

BenchmarkMode → 빈 문자열.

**테스트:** Render 비어있지 않음, BenchmarkMode에서 빈 문자열, Priority==64, Name=="skill_guidance".

## 완료 기준
- [ ] `go test -race ./internal/tools/ -run TestSkillTool` PASS
- [ ] `go test -race ./internal/prompt/ -run TestSkillGuidanceNode` PASS
- [ ] `go build ./internal/tools/... ./internal/prompt/...` 성공
- [ ] `go vet ./internal/tools/ ./internal/prompt/` 경고 없음

## 참고 문서
- Spec: `docs/specs/PHASE-C2-SKILL-EMERGENCE.md` §10 (W2 상세)
- Impl plan: `docs/specs/PHASE-C2-IMPL-PLAN.md` (Tasks 8-9)
- 기존 도구 구현 참고: `internal/tools/file.go` (WriteTool 패턴)
