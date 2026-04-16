# Phase C-2: Skill Emergence System MVP

**Status:** SPEC READY
**Predecessor:** Phase C-1 (Skill System via Wiki) DONE
**Successor:** Phase C-3 (Skill Layer 2 Dual-Analyst)
**Branch:** TBD
**Ref:** Superiority Design v2.2 §Phase 4.1 B2 + §Phase 5.3 B6

---

## 1. Goal

Elnath의 Wiki-native Skill System(C-1)을 확장하여:
1. 유저가 CLI와 자연어로 skill을 생성/관리할 수 있게 한다
2. 에이전트가 대화 중 패턴을 감지하여 skill 생성을 제안한다 (Layer 1)
3. Draft skill을 prevalence 기반으로 자동 승격한다 (Layer 3)
4. Hermes/Claude Code 대비 superiority 차별점: **skill emergence** (reactive → proactive)

**Scope 분리**: Layer 2 (Post-session Dual-Analyst, Trace2Skill 방식)는 Phase C-3으로 분리.
Dog-food 데이터 축적 후 더 정확하게 설계하기 위함.

## 2. Background

### 2.1 기존 C-1 구현 (완료)
- `internal/skill/skill.go` — Skill struct, FromPage, RenderPrompt
- `internal/skill/registry.go` — Registry, Load, Execute, FilterRegistry
- `internal/prompt/skill_catalog_node.go` — system prompt 주입
- `wiki/skills/` — 3개 seed skills (pr-review, refactor-tests, audit-security)
- Runtime slash command dispatch + Telegram integration

### 2.2 경쟁 분석
- **Hermes**: `skill_manage(action='create')` 수동 생성, `SKILLS_GUIDANCE` 유지보수 유도. Skill emergence 없음.
- **Claude Code**: `~/.claude/skills/` 자동 로드, `skillUsageTracking.ts` 사용 빈도 기록. Auto-generation 없음.
- **Trace2Skill (ETH+Alibaba, 2026)**: trajectory → parallel analyst → prevalence-weighted consolidation. 병렬이 순차보다 20x 빠르고 +6.83pp 높음.

### 2.3 Managed Agents 설계 패턴 차용 (이전 세션 분석)
- **Local Outcomes**: 별도 context window의 grader agent → Layer 2 analyst에 적용
- **Skills Progressive Disclosure**: intent 기반 동적 skill 로딩
- **Agent Presets**: 선언적 agent 구성 (skill execution에 활용 가능)

## 3. Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                    Skill Lifecycle                        │
│                                                          │
│  ┌──────────┐    ┌──────────┐    ┌──────────────────┐   │
│  │  Manual   │    │ Layer 1  │    │     Layer 2      │   │
│  │  CRUD     │    │ Hint     │    │  Dual-Analyst    │   │
│  │ (CLI/NL)  │    │(realtime)│    │  (post-session)  │   │
│  └────┬─────┘    └────┬─────┘    └────────┬─────────┘   │
│       │               │                    │              │
│       │ active        │ active/recorded    │ draft        │
│       ▼               ▼                    ▼              │
│  ┌────────────────────────────────────────────────┐      │
│  │           wiki/skills/*.md                      │      │
│  │  (status: active | draft)                       │      │
│  │  + Extra: source, source_sessions, prevalence   │      │
│  └────────────────────┬───────────────────────────┘      │
│                       │                                   │
│                       │ read                              │
│                       ▼                                   │
│  ┌──────────────────────────────────────────────┐        │
│  │         skill.Registry (existing C-1)         │        │
│  │  Load → Get → Execute (unchanged)             │        │
│  └──────────────────────────────────────────────┘        │
│                                                          │
│  ┌──────────────────────────────────────────────┐        │
│  │         Layer 3: Consolidator                 │        │
│  │  scheduled task → read drafts → prevalence    │        │
│  │  check → promote → Telegram notify            │        │
│  └──────────────────────────────────────────────┘        │
│                                                          │
│  ┌──────────────────────────────────────────────┐        │
│  │         Tracker (cross-cutting)               │        │
│  │  usage log → pattern log → JSONL append-only  │        │
│  └──────────────────────────────────────────────┘        │
└─────────────────────────────────────────────────────────┘
```

## 4. 3-Layer Emergence Model

### Layer 1: Real-time Hint (대화 중)
- `create_skill` 도구 + system prompt 가이드
- LLM 자체의 패턴 인식으로 반복 작업 감지
- "이 패턴을 skill로 만들까요?" 자연 제안
- 유저 확인 → 즉시 생성 (status: active, source: hint)
- 유저 무시 → tracker에 패턴 기록만

### Layer 2: Post-session Dual-Analyst (세션 후)
- Trace2Skill 방식: 세션 JSONL을 trajectory로 분석
- **Success Analyst**: 성공 패턴 추출 (single-pass, 별도 context)
- **Error Analyst**: 실패 원인 multi-turn 분석, root cause 검증 후 패치 제안 (별도 context)
- Managed Agents Local Outcomes 패턴: analyst는 원본 세션과 **별도 message array**에서 실행
- Quality gating: 검증되지 않은 trajectory 제외
- 출력: draft skill wiki pages (status: draft, source: analyst)

### Layer 3: Periodic Consolidation (daemon scheduled task)
- Prevalence-weighted merge: 독립 2+ 세션에서 같은 패턴 → systematic property
- Draft → active 승격 (5+ 세션 축적 후, 2+ prevalence)
- Telegram 알림: "새 skill `/name` 생성됨 (N개 세션에서 패턴 감지)"
- Config로 threshold 조정 가능

## 5. Design Decisions

### 5.1 Skill Status via Frontmatter
`status: active|draft`를 wiki page Extra 필드에 저장. Registry.Load()에서 draft 필터링. 
별도 저장소나 DB 불필요 — wiki가 single source of truth.

### 5.2 create_skill Tool > Hook-based Pattern Detection
복잡한 hook 인프라 대신 LLM의 자체 패턴 인식 활용. Hermes의 `skill_manage` 접근법과 동일.
구현이 간단하고, 세션 컨텍스트를 LLM이 이미 갖고 있어 정확도가 높음.

### 5.3 JSONL Tracker > DB Extension
개인 비서 규모(연간 ~3000 엔트리)에서 JSONL append-only가 충분. 
DB 스키마 변경 없이 독립 파일로 관리.

### 5.4 5-session Promotion Threshold (architect 검증 후 하향)
10/3은 개인 비서에 과도. 기본값 5세션 + 2 prevalence로 하향.
Config로 조정 가능 (`skill_emergence.min_sessions`, `skill_emergence.min_prevalence`).

### 5.5 Domain Tagging (Future-Ready)
현재는 범용. conversation.Intent 기반 자동 태깅 경로만 열어둠.
SkillCatalogNode의 intent 필터는 나중에 1줄 추가로 구현 가능.

## 6. Worker Decomposition (병렬 작업)

**Phase C-2 = W1 + W2 + W4 (Layer 2는 C-3으로 분리)**

W1이 0.5 세션 선행, W2/W4가 병렬 진행. 기존 파일 수정은 W1만 담당.

### W1: Foundation (Skill CRUD + Creator + Tracker + Integration)
**기존 파일 수정:**
- `internal/skill/skill.go` — Status, Source 필드 추가 (~5 LOC)
- `internal/skill/registry.go` — Load()에 draft 필터 (~3 LOC) + Add() hot-reload 경로 확인
- `cmd/elnath/commands.go` — skill 서브커맨드 등록
- `cmd/elnath/runtime.go` — Creator/Tracker 생성, create_skill tool 등록, skill-promote 핸들러
- `internal/scheduler/task.go` — "skill-promote" 타입 추가
- `internal/telegram/shell.go` — /skill-list, /skill-create, 승격 알림

**신규 파일:**
- `internal/skill/creator.go` (~120 LOC) — Create/Update/Delete/Promote via wiki.Store + Registry.Add() hot-reload
- `internal/skill/tracker.go` (~100 LOC) — JSONL usage/pattern logging
- `internal/skill/interfaces.go` (~30 LOC) — Analyst, Consolidator interfaces
- `cmd/elnath/cmd_skill.go` (~150 LOC) — CLI commands

**중요**: Creator는 optional `*Registry`를 받아 Promote() 시 registry.Add() 호출 (hot-reload).

### W2: Layer 1 (create_skill Tool + Guidance)
**신규 파일만:**
- `internal/tools/skill_tool.go` (~150-180 LOC) — create_skill tool (7-method Tool interface)
- `internal/tools/skill_tool_test.go`
- `internal/prompt/skill_guidance_node.go` (~40 LOC) — system prompt guidance
- `internal/prompt/skill_guidance_node_test.go`

### W4: Layer 3 (Consolidation + Promotion)
**신규 파일만:**
- `internal/skill/consolidator.go` (~180 LOC) — prevalence-weighted merge
- `internal/skill/consolidator_test.go`
- `internal/skill/promotion.go` (~100 LOC) — draft → active lifecycle
- `internal/skill/promotion_test.go`

### [Phase C-3] W3: Layer 2 (Post-session Dual-Analyst) — 별도 phase
**신규 파일만:**
- `internal/skill/analyst.go` (~200 LOC) — Success/Error analyst (forked subagent, 별도 context)
- `internal/skill/analyst_test.go`
- `internal/skill/extractor.go` (~150 LOC) — agent.LoadSessionMessages() API 사용 (raw JSONL 파싱 금지)
- `internal/skill/extractor_test.go`
- AnalysisGate 포함: 최소 10 tool call + 성공 완료 세션만 분석 (ComplexityGate 패턴 참조)

## 7. Data Structures

### 7.1 Skill (extended)
```go
type Skill struct {
    Name          string
    Description   string
    Trigger       string
    RequiredTools []string
    Model         string
    Prompt        string
    Status        string   // "active" | "draft"
    Source        string   // "user" | "hint" | "analyst" | "promoted"
}
```

### 7.2 Creator
```go
type Creator struct {
    store    *wiki.Store
    tracker  *Tracker
    registry *Registry // optional, for hot-reload on Promote()
}

type CreateParams struct {
    Name           string
    Description    string
    Trigger        string
    RequiredTools  []string
    Model          string
    Prompt         string
    Status         string
    Source         string
    SourceSessions []string
}
```

### 7.3 Tracker
```go
type UsageRecord struct {
    SkillName string    `json:"skill_name"`
    SessionID string    `json:"session_id"`
    Timestamp time.Time `json:"timestamp"`
    Success   bool      `json:"success"`
}

type PatternRecord struct {
    ID           string    `json:"id"`
    Description  string    `json:"description"`
    SessionIDs   []string  `json:"session_ids"`
    ToolSequence []string  `json:"tool_sequence"`
    FirstSeen    time.Time `json:"first_seen"`
    LastSeen     time.Time `json:"last_seen"`
    DraftSkill   string    `json:"draft_skill,omitempty"`
}
```

### 7.4 Interfaces (W2/W3/W4 contract)
```go
type Analyst interface {
    Analyze(ctx context.Context, sessions []SessionTrajectory) ([]SkillPatch, error)
}

type Consolidator interface {
    Consolidate(ctx context.Context, drafts []*Skill, patterns []PatternRecord) (*ConsolidationResult, error)
}

type SessionTrajectory struct {
    SessionID string
    Messages  []llm.Message
    Success   bool
    Intent    string
}

type SkillPatch struct {
    Action         string
    Params         CreateParams
    Evidence       []string
    Confidence     float64
    PatchRationale string
}

type ConsolidationResult struct {
    Promoted []string
    Merged   []string
    Rejected []string
}
```

## 8. CLI Commands

```
elnath skill list              — active skills 목록
elnath skill list --all        — draft 포함
elnath skill show <name>       — skill 상세
elnath skill create <name>     — 대화형 생성
elnath skill edit <name>       — $EDITOR로 wiki page 편집
elnath skill delete <name>     — 삭제 (확인)
elnath skill stats             — 사용 통계
```

## 9. Acceptance Criteria

### W1 (Foundation)
- [ ] `elnath skill list/create/show/edit/delete` 동작
- [ ] Tracker가 usage/pattern JSONL에 append
- [ ] Registry.Load()가 draft skill 필터링
- [ ] runtime.go에 create_skill tool 등록
- [ ] skill-promote scheduled task type 동작

### W2 (Layer 1)
- [ ] create_skill tool이 wiki page 생성
- [ ] SkillGuidanceNode가 system prompt에 skill 생성 가이드 포함
- [ ] 유저가 "이거 skill로 만들어줘" → agent가 create_skill tool 호출

### W3 (Layer 2) — Phase C-3 scope
- [ ] agent.LoadSessionMessages() API로 세션 로드 (raw JSONL 파싱 금지)
- [ ] Success/Error analyst가 별도 context에서 분석 (Local Outcomes 패턴)
- [ ] AnalysisGate: 최소 10 tool call + 성공 완료 세션만 분석
- [ ] 분석 결과가 draft skill wiki page로 저장
- [ ] Quality gating: 검증 안 된 trajectory 제외

### W4 (Layer 3)
- [ ] Draft skills에서 prevalence 계산 (set 비교, 순서 무시)
- [ ] 2+ prevalence, 5+ total sessions → active 승격
- [ ] Creator.Promote() → Registry.Add() hot-reload
- [ ] 승격 시 Telegram 알림
- [ ] 90일 초과 + prevalence 미달 draft 자동 정리
- [ ] Config로 threshold 조정 가능

## 10. W2 상세: create_skill Tool

### 10.1 Tool Schema (JSON Schema for LLM)
```json
{
  "name": "create_skill",
  "description": "Create, list, or delete a wiki-native skill",
  "input_schema": {
    "type": "object",
    "properties": {
      "action": {"type": "string", "enum": ["create", "list", "delete"]},
      "name": {"type": "string", "description": "Skill name (lowercase, hyphens)"},
      "description": {"type": "string"},
      "trigger": {"type": "string", "description": "e.g. /deploy-check <env>"},
      "required_tools": {"type": "array", "items": {"type": "string"}},
      "prompt": {"type": "string", "description": "Skill prompt with {arg} placeholders"}
    },
    "required": ["action"]
  }
}
```

### 10.2 Execute 분기
- `action: "create"` → `creator.Create(params)`, source: "hint", status: "active" → tracker 기록
- `action: "list"` → `registry.List()` → 포맷 텍스트 반환
- `action: "delete"` → `creator.Delete(name)` 호출

### 10.3 Tool Metadata
- `IsConcurrencySafe()`: true (wiki.Store가 파일 단위 독립)
- `Reversible()`: true (delete로 되돌리기 가능)
- `Scope()`: write (wiki 디렉토리에 파일 생성)

### 10.4 SkillGuidanceNode (Priority 64)
SkillCatalogNode(65) 바로 아래. Render 출력:
```
You have a create_skill tool. Use it when:
- You notice a repeated pattern across sessions
- The user says "make this a skill" or similar
- A multi-step workflow could be reusable

When suggesting a skill, briefly explain what it would do before creating it.
Do not suggest skills for one-time tasks.
```
BenchmarkMode → 빈 문자열.

## 11. W4 상세: Consolidator + Promotion

### 11.1 ConsolidatorConfig
```go
type ConsolidatorConfig struct {
    MinSessions   int           // default 5
    MinPrevalence int           // default 2
    MaxDraftAge   time.Duration // default 90 days
}
```

### 11.2 Consolidator.Run() 로직
1. Wiki에서 모든 draft skill 로드 (status: draft인 page)
2. Tracker에서 PatternRecord 로드
3. 각 draft에 대해: source_sessions와 pattern의 tool sequence를 **set 비교** (순서 무시)
4. 독립 세션 수 카운트 → prevalence
5. `prevalence >= MinPrevalence AND total >= MinSessions` → `creator.Promote(name)` → `registry.Add(skill)` hot-reload
6. `draft age > MaxDraftAge AND prevalence < MinPrevalence` → `creator.Delete(name)` (90일 정리)
7. ConsolidationResult 반환

### 11.3 Scheduler 등록
```yaml
# scheduled_tasks.yaml
- name: skill-promote
  type: skill-promote
  prompt: ""
  interval: 24h
  run_on_start: false
  enabled: true
```

### 11.4 Promotion 알림
```go
type NotifyFunc func(ctx context.Context, message string) error

func FormatPromotionMessage(skill *Skill, prevalence int, sessions int) string
// 예: "새 skill /deploy-check 활성화됨 (5개 세션에서 패턴 감지). /skill-list로 확인."
```

## 12. Out of Scope

- Skill 인자의 JSON Schema 검증 (positional string 충분)
- Per-user skill permission
- Skill marketplace / 공유
- Domain-specific analyst (범용으로 시작)
- Cross-project skill sharing
- LLM 기반 패턴 유사도 (초기는 tool set 비교, 나중에 진화)

## 13. Risk

| Risk | Mitigation |
|------|-----------|
| Draft skill 축적 | consolidator 90일 자동 정리 |
| Analyst 저품질 (C-3) | AnalysisGate + prevalence threshold |
| create_skill tool 남용 | 하루 최대 5개 제한 (config) |
| Tracker JSONL 성장 | 연간 rotation (skill-usage-2026.jsonl) |
| Registry hot-reload 경합 | Creator가 같은 패키지 내 Registry.Add() 호출, mutex 보호 |
| 5/2 threshold가 false positive 유발 | dog-food에서 관찰 후 조정, config로 변경 가능 |

## 14. References

- Trace2Skill (arxiv.org/html/2603.25158v1) — 3-stage trajectory analysis pipeline
- Anthropic Managed Agents — Local Outcomes, Skills Progressive Disclosure 패턴
- Elnath Reference Landscape (wiki) — Hermes/Claude Code skill system 비교 분석
- Architect 검증 결과 (2026-04-16) — 10개 항목 검토, 7개 권고사항 반영
