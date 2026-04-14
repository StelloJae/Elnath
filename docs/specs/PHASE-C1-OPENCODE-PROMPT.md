# OpenCode Delegation Prompt: Phase C-1 Skill System via Wiki

3 phase로 나뉜다. 각 phase 완료 후 `go test -race` + `go vet` 검증.

---

## Phase 1: internal/skill/ 패키지 + wiki skill 파일

```
elnath 프로젝트 (`/Users/stello/elnath/`, feat/telegram-redesign 브랜치)에서 Phase C-1 작업을 시작한다.

목표: `internal/skill/` 패키지를 신설하고, wiki에 3개 예시 skill 파일을 생성한다.

### 참고할 기존 코드

wiki 페이지 구조 (internal/wiki/schema.go):
```go
type Page struct {
    Path       string
    Title      string
    Type       PageType
    Content    string    // markdown body without frontmatter
    Tags       []string
    Created    time.Time
    Updated    time.Time
    TTL        string
    Confidence string
    Extra      map[string]any  // custom frontmatter fields go here
}
```

wiki 프론트매터에서 known key(title, type, tags, created, updated, ttl, confidence) 외의 모든 필드는 `Extra`에 자동 저장된다. 따라서 skill 관련 필드(name, description, trigger, required_tools, model)는 Extra에서 꺼내면 된다.

wiki store API (internal/wiki/store.go):
- `store.List() ([]*Page, error)` — 모든 페이지 반환
- `store.Read(path string) (*Page, error)` — 단일 페이지 읽기
- `store.Create(page *Page) error` — 새 페이지 생성

agent API (internal/agent/agent.go):
- `agent.New(provider, reg, opts...)` — agent 생성
- `agent.WithSystemPrompt(prompt)` — system prompt 설정
- `agent.WithModel(model)` — 모델 오버라이드
- `agent.WithMaxIterations(n)` — iteration 제한

tools registry (internal/tools/registry.go):
- `tools.NewRegistry()` — 빈 레지스트리
- `reg.Register(tool)` — 도구 추가
- `reg.Get(name) (Tool, bool)` — 이름으로 조회
- `reg.Names() []string` — 정렬된 이름 목록

### 작업 1: internal/skill/skill.go

```go
package skill

import "github.com/stello/elnath/internal/wiki"

// Skill represents a wiki-defined skill.
type Skill struct {
    Name          string
    Description   string
    Trigger       string
    RequiredTools []string
    Model         string
    Prompt        string
}
```

함수 2개:

1. `FromPage(page *wiki.Page) *Skill`
   - page.Tags에 "skill" 없으면 → nil
   - page.Extra["name"]이 빈 문자열이거나 없으면 → nil
   - Extra에서 description, trigger, model을 string으로 추출 (없으면 "")
   - Extra["required_tools"]를 []string으로 변환:
     - type이 []any이면 loop해서 각 element를 fmt.Sprintf("%v", v)로 변환
     - type이 []string이면 그대로
     - 그 외 → nil
   - page.Content → Prompt
   - Skill struct 반환

2. `(s *Skill) RenderPrompt(args map[string]string) string`
   - s.Prompt을 복사
   - args의 각 key/value에 대해 strings.ReplaceAll(result, "{"+key+"}", value) 적용
   - 결과 반환

### 작업 2: internal/skill/skill_test.go

테이블 기반 테스트:
- FromPage: 정상 page (tags=["skill"], Extra에 name/description/trigger/required_tools) → Skill 반환, 필드값 일치
- FromPage: "skill" tag 없음 → nil
- FromPage: Extra["name"] 없음 → nil
- FromPage: Extra["name"]="" → nil
- FromPage: required_tools가 []any{"bash", "read_file"} → RequiredTools=["bash", "read_file"]
- FromPage: required_tools 없음 → RequiredTools=nil
- RenderPrompt: `{pr_number}` → "42"
- RenderPrompt: 여러 placeholder → 모두 치환
- RenderPrompt: 없는 placeholder → 그대로 유지

### 작업 3: internal/skill/registry.go

```go
package skill

import (
    "context"
    "fmt"
    "log/slog"
    "sort"

    "github.com/stello/elnath/internal/agent"
    "github.com/stello/elnath/internal/llm"
    "github.com/stello/elnath/internal/tools"
    "github.com/stello/elnath/internal/wiki"
)

type Registry struct {
    skills map[string]*Skill
}

func NewRegistry() *Registry {
    return &Registry{skills: make(map[string]*Skill)}
}
```

메서드:

1. `(r *Registry) Load(store *wiki.Store) error`
   - store.List() 호출
   - 각 page에 FromPage() 적용
   - nil이 아니면 r.skills[skill.Name] = skill (중복 시 slog.Warn + 덮어쓰기)
   - store.List() 에러 → 에러 반환

2. `(r *Registry) Get(name string) (*Skill, bool)`
   - 단순 map 조회

3. `(r *Registry) List() []*Skill`
   - map values를 이름순 정렬해서 반환

4. `(r *Registry) Names() []string`
   - skills map의 key를 정렬해서 반환

5. `FilterRegistry(full *tools.Registry, allowList []string) *tools.Registry`
   - allowList 비어있으면 → full 반환
   - 새 tools.NewRegistry() 생성
   - allowList 순회: full.Get(name) → found면 filtered.Register(tool), not found면 slog.Warn
   - filtered 반환

6. `(r *Registry) Execute(ctx context.Context, params ExecuteParams) (*ExecuteResult, error)`

```go
type ExecuteParams struct {
    SkillName string
    Args      map[string]string
    Provider  llm.Provider
    ToolReg   *tools.Registry
    Model     string
    OnText    func(string)
}

type ExecuteResult struct {
    Output   string
    Messages []llm.Message
    Usage    llm.UsageStats
}
```

Execute 로직:
   - r.Get(params.SkillName) → not found면 에러
   - rendered := skill.RenderPrompt(params.Args)
   - filteredReg := FilterRegistry(params.ToolReg, skill.RequiredTools)
   - model 결정: skill.Model이 비어있지 않으면 skill.Model, 아니면 params.Model
   - ag := agent.New(params.Provider, filteredReg, agent.WithSystemPrompt(rendered), agent.WithModel(model), agent.WithMaxIterations(30))
   - result, err := ag.Run(ctx, []llm.Message{llm.NewUserMessage("Execute this skill.")}, params.OnText)
   - err → 에러 반환
   - output 추출: result.Messages에서 마지막 assistant 메시지의 텍스트 추출 (없으면 "")
   - ExecuteResult{Output: output, Messages: result.Messages, Usage: result.Usage} 반환

### 작업 4: internal/skill/registry_test.go

- Load: t.TempDir()에 wiki store 생성, skill page 2개 + non-skill page 1개 작성 → Load() → len(registry.List()) == 2
- Get: 존재하는 skill → found=true
- Get: 없는 skill → found=false
- List: 이름 알파벳순 정렬 확인
- Names: 이름 알파벳순 정렬 확인
- FilterRegistry: full registry에 3개 tool, allowList=["bash", "read_file"] → filtered에 2개만
- FilterRegistry: 빈 allowList → full 반환 (포인터 동일)
- FilterRegistry: allowList에 없는 tool name → slog.Warn, skip

Execute 테스트는 LLM provider mock이 필요하므로, 빌드만 되면 충분하다. 통합 테스트는 Phase 3에서.

### 작업 5: wiki skill 파일 3개

`/Users/stello/elnath/wiki/skills/` 디렉토리에 아래 3파일 생성. 프론트매터 형식은 wiki.ParseFrontmatter이 파싱할 수 있어야 한다 (`---` 구분자, YAML).

**wiki/skills/pr-review.md:**
```markdown
---
title: "PR Review"
type: analysis
tags: [skill]
name: pr-review
description: "Review PR with security and quality focus"
trigger: "/pr-review <pr_number>"
required_tools: [bash, read_file]
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

**wiki/skills/refactor-tests.md:**
```markdown
---
title: "Refactor Tests"
type: analysis
tags: [skill]
name: refactor-tests
description: "Refactor test suite for clarity and maintainability"
trigger: "/refactor-tests <package>"
required_tools: [bash, read_file, write_file]
---

Refactor tests in package {package}. Procedure:

1. List test files in {package}
2. For each test file:
   - Convert to table-driven tests where applicable
   - Extract shared setup into helpers
   - Improve assertion messages
   - Remove duplicated test logic
3. Run tests after each file to verify no regressions
4. Report: files changed, tests before/after count, any failures
```

**wiki/skills/audit-security.md:**
```markdown
---
title: "Security Audit"
type: analysis
tags: [skill]
name: audit-security
description: "Audit codebase for security vulnerabilities"
trigger: "/audit-security"
required_tools: [bash, read_file]
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

### 검증

```bash
go test -race ./internal/skill/...
go vet ./internal/skill/...
```

모두 통과해야 한다.
```

---

## Phase 2: SkillCatalogNode 실제 구현 + runtime 통합

```
Phase C-1 Phase 2. Phase 1에서 `internal/skill/` 패키지와 wiki skill 파일이 완성됐다.

### 작업 1: internal/prompt/skill_catalog_node.go — stub → real

현재 stub 코드를 실제 구현으로 교체한다.

**변경점:**
- import에 `"github.com/stello/elnath/internal/skill"` 추가
- struct에 `registry *skill.Registry` 필드 추가
- 생성자 시그니처 변경: `NewSkillCatalogNode(priority int, registry *skill.Registry) *SkillCatalogNode`
- Render 구현:

```go
func (n *SkillCatalogNode) Render(_ context.Context, state *RenderState) (string, error) {
    if n == nil || n.registry == nil {
        return "", nil
    }
    if state != nil && state.BenchmarkMode {
        return "", nil
    }
    skills := n.registry.List()
    if len(skills) == 0 {
        return "", nil
    }
    var b strings.Builder
    b.WriteString("Available skills (invoke via /name):\n")
    for _, sk := range skills {
        fmt.Fprintf(&b, "\n- /%s", sk.Name)
        if sk.Trigger != "" {
            // trigger에서 /name 부분 제거하고 args만 추출
            parts := strings.SplitN(sk.Trigger, " ", 2)
            if len(parts) > 1 {
                b.WriteString(" ")
                b.WriteString(parts[1])
            }
        }
        if sk.Description != "" {
            b.WriteString(" — ")
            b.WriteString(sk.Description)
        }
    }
    return b.String(), nil
}
```

### 작업 2: internal/prompt/skill_catalog_node_test.go — 갱신

기존 테스트가 `NewSkillCatalogNode(65)` 호출하고 있을 것이다. 시그니처가 `NewSkillCatalogNode(65, registry)` 로 바뀌었으니 수정.

테스트 추가:
- nil registry → ""
- 빈 registry → ""
- skill 2개 있는 registry → 출력에 "/pr-review" + "/audit-security" 포함
- BenchmarkMode → ""

registry mock 생성 방법: `skill.NewRegistry()` 후 수동으로 skill 추가. 하지만 Registry.skills가 private이므로, 테스트용 helper가 필요하다.

방법 1: `internal/skill/` 에 `func (r *Registry) Add(s *Skill)` 메서드를 추가 (테스트에서도 유용)
방법 2: wiki Store를 만들어서 Load로 추가

**방법 1 추천.** Registry에 `Add(s *Skill)` 메서드 추가:
```go
func (r *Registry) Add(s *Skill) {
    if s != nil && s.Name != "" {
        r.skills[s.Name] = s
    }
}
```

이 메서드는 테스트에서도 유용하고, 향후 프로그래밍적 skill 등록에도 사용할 수 있다.

### 작업 3: cmd/elnath/runtime.go — Skill Registry 생성 + 등록

`buildExecutionRuntime` 함수에서:

1. skill registry 생성 및 로드:
```go
skillReg := skill.NewRegistry()
if wikiStore != nil {
    if err := skillReg.Load(wikiStore); err != nil {
        app.Logger.Warn("skill registry load failed", "error", err)
    }
}
```

2. `executionRuntime` struct에 `skillReg *skill.Registry` 필드 추가.

3. SkillCatalogNode 등록 변경:
```go
// 기존: b.Register(prompt.NewSkillCatalogNode(65))
// 변경:
b.Register(prompt.NewSkillCatalogNode(65, skillReg))
```

4. struct 초기화에 `skillReg: skillReg` 추가.

### 작업 4: cmd/elnath/runtime.go — trySkillExecution 메서드

`runTask` 메서드 시작 부분에 slash command 인터셉트 추가:

```go
func (rt *executionRuntime) runTask(
    ctx context.Context,
    sess *agent.Session,
    messages []llm.Message,
    userInput string,
    output orchestrationOutput,
) ([]llm.Message, string, error) {
    // Skill slash command interception
    if rt.skillReg != nil && strings.HasPrefix(userInput, "/") {
        result, handled, err := rt.trySkillExecution(ctx, sess, messages, userInput, output)
        if handled {
            return result, "", err
        }
    }
    // ... rest of existing runTask unchanged ...
```

trySkillExecution 구현:

```go
func (rt *executionRuntime) trySkillExecution(
    ctx context.Context,
    sess *agent.Session,
    messages []llm.Message,
    input string,
    output orchestrationOutput,
) ([]llm.Message, bool, error) {
    fields := strings.Fields(input)
    if len(fields) == 0 {
        return nil, false, nil
    }
    skillName := strings.TrimPrefix(fields[0], "/")
    sk, ok := rt.skillReg.Get(skillName)
    if !ok {
        return nil, false, nil // not a skill — fall through to normal flow
    }

    // Parse positional args from trigger pattern
    args := parseSkillArgs(sk.Trigger, fields[1:])

    rt.app.Logger.Info("executing skill", "name", skillName, "args", args)
    output.emitText(fmt.Sprintf("Executing skill: %s\n", skillName))

    result, err := rt.skillReg.Execute(ctx, skill.ExecuteParams{
        SkillName: skillName,
        Args:      args,
        Provider:  rt.provider,
        ToolReg:   rt.reg,
        Model:     rt.wfCfg.Model,
        OnText:    output.emitText,
    })
    if err != nil {
        return nil, true, fmt.Errorf("skill %q: %w", skillName, err)
    }

    updated := append(messages, llm.NewUserMessage(input))
    updated = append(updated, llm.NewAssistantMessage(result.Output))
    sess.Messages = updated
    return updated, true, nil
}
```

`parseSkillArgs` helper:

```go
// parseSkillArgs extracts named args from the trigger pattern and maps positional values.
// trigger: "/pr-review <pr_number>" → argNames: ["pr_number"]
// values: ["42"] → {"pr_number": "42"}
func parseSkillArgs(trigger string, values []string) map[string]string {
    args := make(map[string]string)
    // Extract <arg_name> patterns from trigger
    parts := strings.Fields(trigger)
    idx := 0
    for _, part := range parts {
        if strings.HasPrefix(part, "<") && strings.HasSuffix(part, ">") {
            name := strings.TrimPrefix(strings.TrimSuffix(part, ">"), "<")
            if idx < len(values) {
                args[name] = values[idx]
                idx++
            }
        }
    }
    return args
}
```

### 작업 5: cmd/elnath/runtime_test.go — 컴파일 수정

`NewSkillCatalogNode` 시그니처가 바뀌었으므로, runtime_test.go에서 호출하는 부분이 있다면 수정. 보통 runtime_test.go는 buildExecutionRuntime을 간접 호출하므로 문제없을 수 있지만, 확인 필요.

### 검증

```bash
go test -race ./internal/skill/... ./internal/prompt/... ./cmd/elnath/...
go vet ./...
make build
```

모두 통과해야 한다.
```

---

## Phase 3: Telegram 통합 + 전체 검증

```
Phase C-1 Phase 3. Phase 1-2에서 skill 패키지, SkillCatalogNode, runtime 통합이 완성됐다.

### 작업 1: internal/telegram/shell.go — skillReg 필드 추가

Shell struct에 `skillReg *skill.Registry` 필드를 추가한다.

`internal/telegram/shell.go`를 읽어서 Shell struct와 NewShell (또는 생성자) 를 확인한 뒤:
1. struct에 `skillReg *skill.Registry` 추가
2. 생성자 파라미터에 `skillReg *skill.Registry` 추가

Shell 생성자를 호출하는 곳을 grep해서 모두 수정 (아마 cmd/elnath/cmd_daemon.go 또는 internal/telegram/ 내부).

### 작업 2: internal/telegram/shell.go — handleCommand에 skill 분기

`handleCommand` 메서드의 `default` 케이스를 수정:

기존:
```go
default:
    if strings.HasPrefix(fields[0], "/") {
        return "Unknown command. Use /help.", nil
    }
    return s.enqueueNewTask(ctx, text, principal)
```

변경:
```go
default:
    if strings.HasPrefix(fields[0], "/") {
        if s.skillReg != nil {
            skillName := strings.TrimPrefix(fields[0], "/")
            if _, ok := s.skillReg.Get(skillName); ok {
                // Execute skill via task queue
                skillPrompt := fmt.Sprintf("[Skill: %s] %s", skillName, text)
                return s.enqueueNewTask(ctx, skillPrompt, principal)
            }
        }
        return "Unknown command. Use /help.", nil
    }
    return s.enqueueNewTask(ctx, text, principal)
```

Telegram 경로에서는 skill을 직접 실행하지 않고, daemon의 task queue로 넘긴다. daemon worker가 runTask를 호출하면 거기서 skill interception이 동작한다.

### 작업 3: internal/telegram/shell.go — /help 갱신

/help 출력에 skill 목록 추가. `case "/help":` 블록 수정:

```go
case "/help":
    help := "📖 <b>Commands</b>\n" +
        "• <code>/status</code> — task status\n" +
        "• <code>/submit &lt;msg&gt;</code> — new task\n" +
        "• <code>/approvals</code> — pending approvals\n" +
        "• <code>/approve &lt;id&gt;</code> — approve\n" +
        "• <code>/deny &lt;id&gt;</code> — deny\n" +
        "• <code>/followup &lt;sid&gt; &lt;msg&gt;</code> — follow-up\n" +
        "• <i>or just type a message</i>"
    if s.skillReg != nil {
        skills := s.skillReg.List()
        if len(skills) > 0 {
            help += "\n\n🛠 <b>Skills</b>"
            for _, sk := range skills {
                help += fmt.Sprintf("\n• <code>/%s</code> — %s", sk.Name, sk.Description)
            }
        }
    }
    return help, nil
```

### 작업 4: Shell 생성자 호출부 수정

Shell 생성자에 skillReg 파라미터가 추가됐으니, 호출하는 코드를 찾아 수정한다.

가능한 위치:
- `cmd/elnath/cmd_daemon.go`
- `internal/telegram/` 내부

grep으로 `NewShell` 또는 Shell 생성자를 찾아서 skillReg 전달.

daemon에서 skillReg를 Shell에 전달하려면, daemon이 skillReg를 갖고 있어야 한다. `cmd/elnath/cmd_daemon.go`에서 buildExecutionRuntime의 결과에서 skillReg를 가져와 Shell에 전달.

방법: executionRuntime에 이미 skillReg가 있으므로, Shell 생성 시 rt.skillReg를 넘기면 된다.

### 작업 5: 기존 테스트 수정

`internal/telegram/shell_test.go` 에서 Shell 생성자 호출이 있다면 skillReg 파라미터 추가 (nil 전달 가능).

### 전체 검증

```bash
go test -race ./internal/skill/... ./internal/prompt/... ./internal/telegram/... ./cmd/elnath/...
go vet ./...
make build
```

모두 통과해야 한다.

최종 확인: `make build` 후 CLAUDE.md가 있는 디렉토리에서 `elnath run` 실행 →
1. system prompt에 "Available skills" 섹션 포함되는지 확인
2. `/pr-review 42` 입력 → "Executing skill: pr-review" 출력 후 agent 실행
3. `/nonexistent` 입력 → 일반 workflow로 fallback (에러 아님)
```
