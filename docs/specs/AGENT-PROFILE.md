# Agent Profile — Wiki-native Declarative Agent Configuration

**Status:** SPEC READY
**Predecessor:** Local Outcomes Ralph Refactor DONE
**Successor:** Phase C-3 (dog-food 후) 또는 Phase D-2

---

## 1. Goal

Agent 구성(model, tools, max_iterations, system prompt)을 wiki 페이지로 선언적으로 정의한다.
이름으로 참조하여 Skill execution, Team workflow, Ralph grader 등에서 재사용한다.

## 2. 변경 범위

**신규 파일:**
- `internal/profile/profile.go` (~80 LOC) — Profile struct, FromPage, LoadAll
- `internal/profile/profile_test.go` (~120 LOC)
- `wiki/profiles/code-reviewer.md` — seed profile
- `wiki/profiles/researcher.md` — seed profile
- `wiki/skills/deep-interview.md` — seed skill (bonus)

**수정 파일:**
- `cmd/elnath/runtime.go` — profile 로드, executionRuntime에 저장
- `cmd/elnath/commands.go` — "profile" 명령 등록
- `cmd/elnath/cmd_skill.go` — cmdProfile 함수 추가 (list/show)

## 3. Design

### 3.1 Wiki Page 포맷

```markdown
---
title: "Code Reviewer"
tags: [profile]
name: code-reviewer
model: ""
tools: [read_file, grep, glob, bash]
max_iterations: 20
---

You are a strict code reviewer. Focus on correctness, security, and maintainability.
Do not suggest style-only changes.
```

- `tags: [profile]` — LoadAll이 이 tag로 필터
- `name` — 참조 키 (영문 소문자 + 하이픈)
- `model` — 빈 문자열이면 caller의 기본 model 사용
- `tools` — 허용 도구 목록. 빈 배열이면 모든 도구 허용
- `max_iterations` — agent 반복 제한. 0이면 caller 기본값
- Body → 추가 system prompt (agent에 prepend)

### 3.2 Profile Package

```go
package profile

type Profile struct {
    Name          string
    Model         string
    Tools         []string
    MaxIterations int
    SystemExtra   string  // wiki page body
}

// FromPage extracts a Profile from a wiki Page.
// Returns nil if page is not a profile (no "profile" tag or no "name" extra).
func FromPage(page *wiki.Page) *Profile

// LoadAll scans wiki Store for profile pages and returns name→Profile map.
func LoadAll(store *wiki.Store) (map[string]*Profile, error)
```

Skill의 `FromPage` 패턴과 동일. Extra에서 name/model/tools/max_iterations 파싱.

### 3.3 Seed Profiles

**wiki/profiles/code-reviewer.md:**
```markdown
---
title: "Code Reviewer"
tags: [profile]
name: code-reviewer
model: ""
tools: [read_file, grep, glob, bash]
max_iterations: 20
---

You are a strict code reviewer. Evaluate code for correctness, security vulnerabilities,
and maintainability issues. Provide specific, actionable feedback with severity levels
(CRITICAL/HIGH/MEDIUM/LOW). Do not suggest style-only changes.
```

**wiki/profiles/researcher.md:**
```markdown
---
title: "Researcher"
tags: [profile]
name: researcher
model: ""
tools: [bash, read_file, write_file, web_fetch, wiki_search, wiki_read, wiki_write]
max_iterations: 50
---

You are a thorough researcher. Explore topics systematically, verify claims with multiple
sources, and synthesize findings into structured wiki pages. Cite sources explicitly.
```

### 3.4 Deep Interview Seed Skill

**wiki/skills/deep-interview.md:**
```markdown
---
title: "Deep Interview"
type: analysis
tags: [skill]
name: deep-interview
description: "Clarify ambiguous requests before executing"
trigger: "/deep-interview"
required_tools: []
model: ""
---

Before executing any task, clarify the user's intent through structured questions.

Procedure:
1. Identify what is ambiguous or underspecified in the request
2. Ask 3-5 focused clarifying questions (all in one message if in Telegram)
3. Wait for user responses
4. Summarize the refined requirements
5. Confirm with user before proceeding
6. Execute the clarified task

If the request is already clear and specific, skip the interview and execute directly.
Do not interview for simple, unambiguous tasks.
```

### 3.5 CLI Commands

```
elnath profile list           — 등록된 profile 목록
elnath profile show <name>    — profile 상세 (model, tools, prompt)
```

Create/edit는 wiki 파일 직접 편집 (`elnath profile edit <name>` → $EDITOR).

### 3.6 Runtime Integration

`buildExecutionRuntime()`에서 profile 로드:
```go
profiles, err := profile.LoadAll(wikiStore)
```

executionRuntime struct에 `profiles map[string]*profile.Profile` 저장.

현재는 로드만 하고, 실제 소비자 연결(Team, Skill, Ralph)은 해당 workflow 수정 시점에서.

## 4. 소비자 연결 계획 (이번 scope 아님)

| 소비자 | 연결 시점 | 방식 |
|--------|----------|------|
| Skill execution | skill wiki에 `profile: code-reviewer` 추가 시 | registry.Execute()에서 profile 조회 → tools/model override |
| Team workflow | Phase 5.1 | subtask config에 profile 지정 |
| Ralph grader | 필요 시 | grader profile 분리 |

## 5. Acceptance Criteria

- [ ] `profile.FromPage()` — profile tag 있는 page에서 Profile 추출
- [ ] `profile.FromPage()` — profile tag 없으면 nil
- [ ] `profile.LoadAll()` — wiki에서 모든 profile 로드
- [ ] `elnath profile list` — seed profiles 표시
- [ ] `elnath profile show code-reviewer` — 상세 출력
- [ ] wiki/profiles/ 에 2개 seed profile 존재
- [ ] wiki/skills/deep-interview.md 존재
- [ ] `go test -race ./internal/profile/...` PASS
- [ ] `make build` 성공

## 6. Out of Scope

- Profile 동적 reload (daemon 재시작 필요 — skill과 동일)
- Profile permission guard (agent가 profile 수정 방지 — Phase 6 I2)
- Profile ↔ Skill 자동 매핑 (수동 참조로 충분)
- Profile versioning
