# Phase C-1: Skill System via Wiki

**Status:** SPEC READY  
**Predecessor:** Phase B-1 (LB1 Full Inclusion Graph) DONE  
**Successor:** Phase D (Safety & Governance)  
**Branch:** `feat/telegram-redesign`  
**Ref:** Superiority Design v2.2 §7 "Session C-1 — B2 Skill System via Wiki"

---

## 1. Goal

Wiki 페이지를 skill 정의로 사용하는 시스템을 구축한다. Slash command로 호출하면 tool-restricted agent가 skill prompt를 실행한다. SkillCatalogNode stub을 실제 구현으로 교체한다.

## 2. Architecture Overview

```
User Input: "/pr-review 42"
    │
    ▼
┌─────────────────────┐
│ Slash Command Parser │ (runtime.go / shell.go)
│ name="pr-review"     │
│ args={"pr_number":42}│
└─────────┬───────────┘
          ▼
┌─────────────────────┐
│ skill.Registry       │
│ .Get("pr-review")    │
│ .Execute(ctx, ...)   │
└─────────┬───────────┘
          ▼
┌─────────────────────┐
│ Filtered Registry    │ (only required_tools)
│ + New Agent          │ (skill.Model override)
│ + Rendered Prompt    │ (args interpolated)
└─────────┬───────────┘
          ▼
┌─────────────────────┐
│ Agent.Run()          │ (standard agent loop)
│ output → session     │
└─────────────────────┘
```

**핵심 결정: Forked Subagent = Filtered Registry + 기존 Agent**

Superiority Design의 SF5 (Forked Subagent)는 별도 타입이 아니라, 기존 `agent.New()`에 filtered `tools.Registry`를 주입하는 방식으로 구현한다. 이렇게 하면:
- 새 타입 없이 tool restriction 달성
- 기존 Agent의 retry, budget pressure, streaming 모두 재사용
- Model override는 `agent.WithModel()` 로 처리

## 3. Wiki Skill Page Format

wiki.Page의 `Extra map[string]any`를 활용한다. 기존 frontmatter 파싱이 unknown key를 Extra에 자동 저장하므로 스키마 변경 없음.

```markdown
---
title: "PR Review"
type: analysis
tags: [skill]
name: pr-review
description: "Review PR with security + quality focus"
trigger: "/pr-review <pr_number>"
required_tools: [bash, read_file, write_file]
model: ""
---

You are reviewing PR #{pr_number}.

1. Run `gh pr diff {pr_number}` to get the diff
2. Identify changed files
3. For each file, check: security issues, code quality, test coverage
4. Write structured feedback with SEVERITY levels (CRITICAL/HIGH/MEDIUM/LOW)
5. Provide actionable suggestions
```

**Convention:**
- `tags` 에 `"skill"` 포함 필수 — Registry가 이 tag로 필터링
- `name` — slash command 이름 (영문 소문자 + 하이픈)
- `trigger` — 사용법 표시용 (파싱에는 사용 안 함)
- `required_tools` — string 배열. 빈 배열이면 모든 tool 허용
- `model` — 빈 문자열이면 기본 모델 사용
- Body의 `{arg_name}` — 실행 시 실제 값으로 치환

## 4. Deliverables

### 4.1 New Package: `internal/skill/`

#### `internal/skill/skill.go`

```go
package skill

type Skill struct {
    Name          string
    Description   string
    Trigger       string
    RequiredTools []string
    Model         string
    Prompt        string   // markdown body (template with {arg} placeholders)
}
```

`Skill`은 wiki.Page에서 파싱된 immutable value object. Extra 필드에서 추출한다.

```go
// FromPage extracts a Skill from a wiki Page.
// Returns nil if the page is not a skill (missing "skill" tag or "name" extra).
func FromPage(page *wiki.Page) *Skill
```

로직:
1. `page.Tags`에 `"skill"` 없으면 → nil
2. `page.Extra["name"]` 없거나 빈 문자열이면 → nil
3. `page.Extra["required_tools"]` → `[]string` 변환 (type assertion: `[]any` → loop → `string`)
4. `page.Extra["model"]` → string (없으면 "")
5. `page.Extra["description"]` → string
6. `page.Extra["trigger"]` → string
7. `page.Content` → Prompt

```go
// RenderPrompt replaces {key} placeholders in the skill prompt with args values.
func (s *Skill) RenderPrompt(args map[string]string) string
```

단순 `strings.ReplaceAll` 루프: 각 key에 대해 `{key}` → value 치환.

#### `internal/skill/skill_test.go`

- FromPage: 정상 wiki page → Skill 추출
- FromPage: "skill" tag 없음 → nil
- FromPage: "name" extra 없음 → nil
- FromPage: required_tools 타입 변환
- RenderPrompt: `{pr_number}` → "42" 치환
- RenderPrompt: 여러 placeholder 동시 치환
- RenderPrompt: 없는 placeholder → 그대로 유지

#### `internal/skill/registry.go`

```go
type Registry struct {
    skills map[string]*Skill // name → Skill
}

// NewRegistry creates an empty skill registry.
func NewRegistry() *Registry

// Load scans wiki Store for pages with "skill" tag and populates the registry.
func (r *Registry) Load(store *wiki.Store) error

// Get returns the skill by name.
func (r *Registry) Get(name string) (*Skill, bool)

// List returns all skills sorted by name.
func (r *Registry) List() []*Skill

// Names returns sorted skill names.
func (r *Registry) Names() []string
```

**Load 로직:**
1. `store.List()` 호출하여 모든 wiki page 가져옴
2. 각 page에 `FromPage()` 적용
3. nil이 아닌 결과를 `skills` map에 저장
4. 중복 name → 마지막 것이 이김 (slog.Warn 로그)

```go
// Execute runs a skill with the given arguments.
// It creates a tool-restricted agent and runs the skill prompt.
func (r *Registry) Execute(ctx context.Context, params ExecuteParams) (*ExecuteResult, error)

type ExecuteParams struct {
    SkillName string
    Args      map[string]string
    Provider  llm.Provider
    ToolReg   *tools.Registry  // full registry — will be filtered
    Model     string           // default model (skill.Model overrides if set)
    OnText    func(string)     // streaming callback
}

type ExecuteResult struct {
    Output   string
    Messages []llm.Message
    Usage    llm.UsageStats
}
```

**Execute 로직:**
1. `r.Get(name)` → 없으면 error
2. `skill.RenderPrompt(args)` → system prompt
3. `FilterRegistry(toolReg, skill.RequiredTools)` → filtered registry
4. model 결정: `skill.Model` 이 비어있으면 `params.Model` 사용
5. `agent.New(provider, filteredReg, agent.WithSystemPrompt(rendered), agent.WithModel(model), agent.WithMaxIterations(30))`
6. `agent.Run(ctx, []llm.Message{llm.NewUserMessage("Execute this skill.")}, onText)`
7. 결과에서 마지막 assistant message의 text를 Output으로 반환

```go
// FilterRegistry creates a new tools.Registry containing only the named tools.
// If allowList is empty, returns the original registry unchanged.
func FilterRegistry(full *tools.Registry, allowList []string) *tools.Registry
```

로직:
1. `allowList` 비어있으면 → `full` 그대로 반환
2. `tools.NewRegistry()` 생성
3. `allowList` 순회: `full.Get(name)` → found면 `filtered.Register(tool)` 
4. not found → slog.Warn + skip (에러 아님)
5. filtered 반환

#### `internal/skill/registry_test.go`

- Load: temp wiki dir에 skill page 2개 + non-skill page 1개 → skill 2개만 로드
- Get: 존재하는 skill → found
- Get: 없는 skill → not found
- List: 이름순 정렬
- FilterRegistry: allowList로 필터 → 허용된 tool만 포함
- FilterRegistry: 빈 allowList → 원본 반환
- Execute: mock provider + mock tools로 실행 검증 (agent가 올바른 system prompt와 filtered tools를 받는지)

### 4.2 Wiki Skill Examples

#### `wiki/skills/pr-review.md`

```markdown
---
title: "PR Review"
type: analysis
tags: [skill]
name: pr-review
description: "Review PR with security and quality focus"
trigger: "/pr-review <pr_number>"
required_tools: [bash, read_file]
model: ""
---

Review PR #{pr_number}. Procedure:

1. Run `gh pr diff {pr_number}` to see changes
2. For each changed file:
   - Security: check for injection, auth bypass, secret exposure
   - Quality: naming, complexity, error handling
   - Tests: verify test coverage for changes
3. Output format per file:
   - **File**: path
   - **CRITICAL/HIGH/MEDIUM/LOW**: issue description
   - **Suggestion**: fix recommendation
4. End with overall assessment: APPROVE, REQUEST_CHANGES, or COMMENT
```

#### `wiki/skills/refactor-tests.md`

```markdown
---
title: "Refactor Tests"
type: analysis
tags: [skill]
name: refactor-tests
description: "Refactor test suite for clarity and maintainability"
trigger: "/refactor-tests <package>"
required_tools: [bash, read_file, write_file]
model: ""
---

Refactor tests in package {package}. Procedure:

1. List test files: find files matching `*_test.go` or `*.test.ts` in {package}
2. For each test file:
   - Convert to table-driven tests where applicable
   - Extract shared setup into helpers
   - Improve assertion messages
   - Remove duplicated test logic
3. Run tests after each file to verify no regressions
4. Report: files changed, tests before/after count, any failures
```

#### `wiki/skills/audit-security.md`

```markdown
---
title: "Security Audit"
type: analysis
tags: [skill]
name: audit-security
description: "Audit codebase for security vulnerabilities"
trigger: "/audit-security"
required_tools: [bash, read_file]
model: ""
---

Perform a security audit of the current working directory. Procedure:

1. Identify the tech stack (language, frameworks, dependencies)
2. Check for common vulnerabilities:
   - Hardcoded secrets (API keys, passwords, tokens)
   - SQL injection (raw queries without parameterization)
   - XSS (unsanitized user input in HTML output)
   - Command injection (shell commands with user input)
   - Path traversal (file access with user-controlled paths)
   - Insecure dependencies (known CVEs)
3. For each finding:
   - **Severity**: CRITICAL / HIGH / MEDIUM / LOW
   - **Location**: file:line
   - **Description**: what's wrong
   - **Fix**: recommended remediation
4. Summary: total findings by severity, overall risk assessment
```

### 4.3 Modified Files

#### `internal/prompt/skill_catalog_node.go` — Stub → Real

현재 stub을 실제 구현으로 교체. Registry 참조를 받아 skill 목록을 렌더링한다.

```go
type SkillCatalogNode struct {
    priority int
    registry *skill.Registry  // NEW: nil이면 빈 문자열 (하위호환)
}

func NewSkillCatalogNode(priority int, registry *skill.Registry) *SkillCatalogNode
```

**Render 출력 (registry에 skill이 있을 때):**
```
Available skills (invoke via /name):

- /pr-review <pr_number> — Review PR with security and quality focus
- /refactor-tests <package> — Refactor test suite for clarity and maintainability
- /audit-security — Audit codebase for security vulnerabilities
```

**조건:**
- registry nil → ""
- registry.List() 비어있음 → ""
- BenchmarkMode → ""

**주의:** `NewSkillCatalogNode` 시그니처가 바뀌므로 runtime.go 등록도 함께 변경.

#### `cmd/elnath/runtime.go` — Skill Registry 통합

1. `executionRuntime` struct에 `skillReg *skill.Registry` 필드 추가
2. `buildExecutionRuntime`에서:
   - `skill.NewRegistry()` 생성
   - `wikiStore != nil` 이면 `skillReg.Load(wikiStore)` 호출 (에러 → slog.Warn, skip)
   - struct에 저장
3. SkillCatalogNode 등록 변경:
   ```go
   b.Register(prompt.NewSkillCatalogNode(65, rt.skillReg))
   ```
   → `buildExecutionRuntime` 내에서는 아직 `rt`가 없으므로, builder 생성 전에 skillReg를 만들고 직접 전달.
4. `runTask` 에 slash command 분기 추가 (아래 참조)

#### `cmd/elnath/runtime.go` — Slash Command Parsing in runTask

`runTask` 메서드 시작 부분에 slash command 감지 로직 추가:

```go
func (rt *executionRuntime) runTask(...) ([]llm.Message, string, error) {
    // Slash command interception
    if rt.skillReg != nil && strings.HasPrefix(userInput, "/") {
        result, handled, err := rt.trySkillExecution(ctx, sess, userInput, output)
        if handled {
            if err != nil {
                return messages, "", err
            }
            // Append skill interaction to session
            sess.Messages = append(sess.Messages, llm.NewUserMessage(userInput))
            sess.Messages = append(sess.Messages, llm.NewAssistantMessage(result.Output))
            return sess.Messages, result.Output, nil
        }
    }
    // ... existing runTask logic unchanged ...
}
```

새 private 메서드:

```go
func (rt *executionRuntime) trySkillExecution(
    ctx context.Context,
    sess *agent.Session,
    input string,
    output orchestrationOutput,
) (*skill.ExecuteResult, bool, error)
```

로직:
1. `strings.Fields(input)` → fields
2. `fields[0]` 에서 `/` 제거 → skillName
3. `rt.skillReg.Get(skillName)` → not found면 `return nil, false, nil` (handled=false, 일반 flow로 진행)
4. found면:
   - 나머지 fields를 positional args로 파싱 (trigger 문자열에서 `<arg_name>` 패턴 추출 → 순서대로 매핑)
   - `output.emitText("Executing skill: " + skillName + "\n")`
   - `rt.skillReg.Execute(ctx, ExecuteParams{...})` 호출
   - `return result, true, err`

**Args 파싱 예시:**
- trigger: `/pr-review <pr_number>` → arg names: `["pr_number"]`
- input: `/pr-review 42` → args: `{"pr_number": "42"}`
- 초과 args는 무시, 부족한 args는 빈 문자열

#### `internal/telegram/shell.go` — Skill Command Routing

`handleCommand`의 `default` 분기에서 skill registry 확인:

```go
default:
    if strings.HasPrefix(fields[0], "/") {
        // Try skill execution
        if s.skillReg != nil {
            skillName := strings.TrimPrefix(fields[0], "/")
            if sk, ok := s.skillReg.Get(skillName); ok {
                return s.executeSkill(ctx, sk, fields[1:], principal)
            }
        }
        return "Unknown command. Use /help.", nil
    }
```

Shell struct에 `skillReg *skill.Registry` 필드 추가. 생성자에서 주입.

`executeSkill` 메서드: Telegram에서는 task queue를 통해 실행 (기존 enqueueNewTask 패턴). Skill prompt를 task prompt로 변환하여 queue에 넣는다.

#### `internal/telegram/shell.go` — /help 업데이트

skill이 있으면 /help 출력에 추가:

```go
// After existing help text
if s.skillReg != nil {
    for _, sk := range s.skillReg.List() {
        helpText += fmt.Sprintf("• <code>/%s</code> — %s\n", sk.Name, sk.Description)
    }
}
```

### 4.4 New Files Summary

| File | LOC (est.) | 역할 |
|------|-----------|------|
| `internal/skill/skill.go` | ~60 | Skill struct + FromPage + RenderPrompt |
| `internal/skill/skill_test.go` | ~100 | Skill 파싱/렌더 테스트 |
| `internal/skill/registry.go` | ~120 | Registry + Load + Execute + FilterRegistry |
| `internal/skill/registry_test.go` | ~150 | Registry 로드/실행 테스트 |
| `wiki/skills/pr-review.md` | ~30 | PR 리뷰 skill |
| `wiki/skills/refactor-tests.md` | ~25 | 테스트 리팩토링 skill |
| `wiki/skills/audit-security.md` | ~30 | 보안 감사 skill |

### 4.5 Modified Files Summary

| File | 변경 내용 |
|------|----------|
| `internal/prompt/skill_catalog_node.go` | stub → real (registry 주입, 목록 렌더) |
| `internal/prompt/skill_catalog_node_test.go` | registry mock으로 테스트 갱신 |
| `cmd/elnath/runtime.go` | skillReg 생성/로드, SkillCatalogNode 생성자 변경, trySkillExecution 추가 |
| `internal/telegram/shell.go` | skillReg 필드 추가, handleCommand에 skill 분기, /help 갱신 |

## 5. Acceptance Criteria

- [ ] `go test -race ./internal/skill/...` — 모든 테스트 통과
- [ ] `go test -race ./internal/prompt/...` — SkillCatalogNode 테스트 통과
- [ ] `go test -race ./cmd/elnath/...` — runtime 테스트 통과
- [ ] `go vet ./...` — 경고 없음
- [ ] `make build` — 빌드 성공
- [ ] `wiki/skills/` 에 3개 skill 파일 존재
- [ ] CLI에서 `/pr-review 42` 입력 시 skill 실행 (tool-restricted agent)
- [ ] 존재하지 않는 `/foo` → 일반 flow로 fallback (에러 아님)
- [ ] SkillCatalogNode가 system prompt에 skill 목록 포함
- [ ] BenchmarkMode에서 SkillCatalogNode → 빈 문자열
- [ ] FilterRegistry: `required_tools: [bash, read_file]` → agent가 bash + read_file만 사용 가능

## 6. Out of Scope

- Skill 인자의 JSON Schema 검증 (args_schema) — 현재는 positional string으로 충분
- Skill 결과의 wiki 자동 저장 — Phase D에서 고려
- Skill 동적 reload (daemon 재시작 없이) — watcher 미구현. 재시작 시 reload
- Telegram 경로의 full agent execution — 현재는 task queue를 통한 간접 실행. 직접 실행은 daemon worker가 담당
- Skill permission per-user — 모든 principal이 모든 skill 사용 가능

## 7. Risk

| Risk | Mitigation |
|------|-----------|
| wiki.Store.List()가 수백 페이지 → 느린 로드 | skill은 startup 1회만 로드. "skill" tag 필터가 O(N) scan이지만 N이 작음 |
| FilterRegistry에 없는 tool name → 빈 registry | slog.Warn 로그. agent가 tool 0개면 즉시 종료 (tool call 없이 텍스트만 반환) |
| Skill prompt injection | skill은 wiki 소유자(=사용자)가 작성. 자기 자신을 공격할 이유 없음. 외부 소스 skill은 미지원 |
| Agent iteration cap | skill execution에 maxIterations=30 (기본 50보다 낮음). 무한 루프 방지 |
