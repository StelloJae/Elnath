# Claude Code Ant vs External: Exhaustive Audit

**Date**: 2026-04-12
**Source**: `/Users/stello/claude-code-src/src/` (npm source map 역설계)
**Method**: 120+ files 전수 조사, 3 parallel agents, 모든 `process.env.USER_TYPE === 'ant'` 분기 식별

## Architecture

Claude Code는 3중 레이어로 내부/외부를 분리:
1. **Build-time DCE**: `process.env.USER_TYPE === 'ant'`가 bundler에 의해 상수로 치환되고, 외부 빌드에서는 ant-only 코드가 물리적으로 제거됨
2. **Build-time feature flags**: `feature('FLAG_NAME')` from `bun:bundle` — 85개 unique flags
3. **Runtime flags**: GrowthBook (별도 SDK key — ant/external 완전히 다른 프로젝트)

## HIGH Impact Findings (Elnath 벤치마크에 직접 영향)

### PROMPT 차이 (시스템 프롬프트에서 LLM이 받는 텍스트)

#### P1. Comment Discipline (prompts.ts:205-212) — ANT ONLY
```
Default to writing no comments. Only add one when the WHY is non-obvious: a hidden constraint, a subtle invariant, a workaround for a specific bug, behavior that would surprise a reader. If removing the comment wouldn't confuse a future reader, don't write it.

Don't explain WHAT the code does, since well-named identifiers already do that. Don't reference the current task, fix, or callers ("used by X", "added for the Y flow", "handles the case from issue #123"), since those belong in the PR description and rot as the codebase evolves.

Don't remove existing comments unless you're removing the code they describe or you know they're wrong. A comment that looks pointless to you may encode a constraint or a lesson from a past bug that isn't visible in the current diff.
```

#### P2. Verification Before Completion (prompts.ts:210-211) — ANT ONLY
```
Before reporting a task complete, verify it actually works: run the test, execute the script, check the output. Minimum complexity means no gold-plating, not skipping the finish line. If you can't verify (no test exists, can't run the code), say so explicitly rather than claiming success.
```

#### P3. Collaborator Mode (prompts.ts:225-228) — ANT ONLY
```
If you notice the user's request is based on a misconception, or spot a bug adjacent to what they asked about, say so. You're a collaborator, not just an executor—users benefit from your judgment, not just your compliance.
```

#### P4. False-Claims Prevention (prompts.ts:238-241) — ANT ONLY
```
Report outcomes faithfully: if tests fail, say so with the relevant output; if you did not run a verification step, say that rather than implying it succeeded. Never claim "all tests pass" when output shows failures, never suppress or simplify failing checks (tests, lints, type errors) to manufacture a green result, and never characterize incomplete or broken work as done. Equally, when a check did pass or a task is complete, state it plainly — do not hedge confirmed results with unnecessary disclaimers, downgrade finished work to "partial," or re-verify things you already checked. The goal is an accurate report, not a defensive one.
```

#### P5. Output Style (prompts.ts:404-414 vs 416-427) — COMPLETELY DIFFERENT TEXT

**Ant** gets "Communicating with the user" (~300 words):
```
When sending user-facing text, you're writing for a person, not logging to a console. Assume users can't see most tool calls or thinking - only your text output. Before your first tool call, briefly state what you're about to do. While working, give short updates at key moments...

When making updates, assume the person has stepped away and lost the thread... Write so they can pick back up cold: use complete, grammatically correct sentences without unexplained jargon. Expand technical terms...

Write user-facing text in flowing prose while eschewing fragments, excessive em dashes, symbols and notation... Avoid semantic backtracking: structure each sentence so a person can read it linearly...

What's most important is the reader understanding your output without mental overhead or follow-ups...
```

**External** gets "Output efficiency" (~100 words):
```
IMPORTANT: Go straight to the point. Try the simplest approach first without going in circles. Do not overdo it. Be extra concise.

Keep your text output brief and direct. Lead with the answer or action, not the reasoning. Skip filler words, preamble, and unnecessary transitions...
```

#### P6. "Short and concise" 제거 (prompts.ts:433-435) — ANT ONLY
Ant: 이 줄을 받지 않음
External: `"Your responses should be short and concise."`

#### P7. 25-Word Length Anchors (prompts.ts:529-534) — ANT ONLY
```
Length limits: keep text between tool calls to ≤25 words. Keep final responses to ≤100 words unless the task requires more detail.
```

#### P8. FileEditTool Minimal Uniqueness (FileEditTool/prompt.ts:17-18) — ANT ONLY
```
Use the smallest old_string that's clearly unique — usually 2-4 adjacent lines is sufficient. Avoid including 10+ lines of context when less uniquely identifies the target.
```

#### P9. Ant Model Override Suffix (prompts.ts:136-140) — ANT ONLY
GrowthBook `tengu_ant_model_override`에서 `defaultSystemPromptSuffix`를 읽어 시스템 프롬프트에 임의 텍스트 추가 가능.

#### P10. EnterPlanModeTool (EnterPlanModeTool/prompt.ts:167) — COMPLETELY DIFFERENT
Ant: "Use this tool when a task has genuine ambiguity." 더 공격적, "just do it" 마인드.
External: "Prefer using EnterPlanMode for implementation tasks." 더 신중, 거의 모든 multi-file 작업에 plan mode 권장.

#### P11. BashTool Git Instructions (BashTool/prompt.ts:56-76) — DIFFERENT
Ant: 짧은 버전 — `/commit`, `/commit-push-pr` skill 사용 안내
External: 80줄 전체 git/PR 인라인 가이드

#### P12. /issue, /share 추천 (prompts.ts:243-246) — ANT ONLY (Elnath 무관)

#### P13. Verification Agent Mandate (prompts.ts:392-394) — ANT ONLY (GrowthBook A/B)
`tengu_hive_evidence` flag가 true이면: "independent adversarial verification must happen before you report completion"

### API/서버 차이

#### A1. CLI Internal Beta Header (betas.ts:30, 243-248)
Ant: `cli-internal-2026-02-09` 헤더 전송 → 서버가 다르게 동작 가능
External: 빈 문자열

#### A2. Token Efficient Tools (betas.ts:338-343) — ANT ONLY
~4.5% 출력 토큰 절약하는 JSON tool_use 포맷 beta.

#### A3. Numeric Effort Override (claude.ts:457-464) — ANT ONLY
`anthropic_internal.effort_override`로 숫자형 effort 전송 가능 (e.g., 75).

#### A4. 1-Hour Prompt Cache (claude.ts:409) — ANT ALWAYS ELIGIBLE
Ant: 항상 1시간 prompt cache TTL 자격
External: Claude AI 구독자 + overage 아닌 경우만

#### A5. Research Field (claude.ts: 여러 곳) — ANT ONLY
API 응답의 `research` 필드를 캡처하여 메시지에 첨부. 내부 메타데이터.

#### A6. Connector Text Summarization (betas.ts:291) — ANT ONLY
서버측 텍스트 요약 beta.

### MODEL 차이

#### M1. Default Model (model.ts:180) — CRITICAL
Ant: Opus 4.6 [1m] (GrowthBook 설정 가능)
External Max/TeamPremium: Opus 4.6
External Pro/PAYG: **Sonnet 4.6** (이게 가장 큰 차이)

#### M2. Ant-Only Models (antModels.ts)
GrowthBook `tengu_ant_model_override`로 내부 코드네임 모델 접근 가능.

#### M3. Explore Agent Model (exploreAgent.ts:78)
Ant: `'inherit'` (부모 모델, e.g., Opus)
External: `'haiku'`

#### M4. Default Effort (effort.ts:282)
Ant: GrowthBook 설정으로 모델별 기본 effort 커스터마이즈 가능
External: 하드코딩된 기본값

#### M5. Max Output Tokens (context.ts:156)
Ant: 내부 모델은 커스텀 `defaultMaxTokens` + `upperMaxTokensLimit`
External: 하드코딩된 값

### TOOL BEHAVIOR 차이

#### T1. REPL Tool (tools.ts:17-19, REPLTool/constants.ts:27) — ANT ONLY
Ant 빌드에서만 REPL tool 존재. Read/Write/Edit/Glob/Grep/Bash를 JS VM으로 wrapping.

#### T2. Nested Agents (constants/tools.ts:41)
Ant: Agent 안에서 Agent 호출 가능 (재귀)
External: Agent 안에서 Agent 호출 불가

#### T3. Agent Isolation: Remote (AgentTool/loadAgentsDir.ts:94)
Ant: `['worktree', 'remote']`
External: `['worktree']` only

#### T4. Agent Swarms Always On (agentSwarmsEnabled.ts:26)
Ant: 항상 활성화
External: opt-in 필요 (env var 또는 --agent-teams flag)

### PERMISSION 차이

#### X1. Auto Mode Permissions Template (yoloClassifier.ts:66-72)
Ant: `permissions_anthropic.txt` (내부용 template)
External: `permissions_external.txt`

#### X2. Bash Safe Env Vars (bashPermissions.ts:447-497)
Ant: 30+ 추가 env vars (KUBECONFIG, DOCKER_HOST, AWS_PROFILE, GH_TOKEN 등) 자동 승인
External: 기본 ~35개만

#### X3. Read-Only gh CLI (readOnlyValidation.ts:1211)
Ant: gh 명령어 read-only 자동 승인
External: 프롬프트 필요

#### X4. Computer Use (computerUse/gates.ts:40)
Ant: 구독 무관 항상 가능
External: Max/Pro만

#### X5. Plan Mode V2 Interview (planModeV2.ts:52)
Ant: 항상 활성
External: GrowthBook 실험 게이트

### CONFIG 차이

#### C1. GrowthBook — 다른 SDK Key (keys.ts:6)
Ant: `sdk-xRVcrliHIlrg4og4`
External: `sdk-zAZezfDKGoZuXXKe`
완전히 다른 feature flag 프로젝트.

#### C2. GrowthBook Flag Override (growthbook.ts:172-191)
Ant: `CLAUDE_INTERNAL_FC_OVERRIDES` env var로 모든 flag 강제 가능
External: 불가

#### C3. 25+ Internal-Only Commands (commands.ts:343)
backfillSessions, breakCache, bughunter, commit, commitPushPr, ctx_viz, goodClaude, issue, initVerifiers, mockLimits, bridgeKick, version, ultraplan, subscribePr, resetLimits, onboarding, share, summary, teleport, antTrace, perfIssue, env, oauthRefresh, debugToolCall, agentsPlatform, autofixPr

### UI/TELEMETRY (Elnath 무관, 완전성을 위해)

~90개 callsite: debug logging, telemetry sampling, Chrome extension, bridge infrastructure, OSC terminal, release notes, npm cache cleanup, update channels, startup profiling 등. 벤치마크 성능에 영향 없음.

## Elnath에 적용 가능한 것 요약

| 우선순위 | 적용 대상 | 원본 출처 |
|---------|----------|----------|
| **MUST** | P1-P7 프롬프트 원문을 Elnath에 자체 구현 | prompts.ts ant sections |
| **MUST** | P8 FileEditTool minimal uniqueness | FileEditTool/prompt.ts |
| **SHOULD** | P5 Output style (ant 버전의 "Communicating with the user") | prompts.ts |
| **SHOULD** | P10 Plan mode: 더 공격적 "just do it" 스타일 | EnterPlanModeTool/prompt.ts |
| **NICE** | A2 Token efficient tools (자체적으로 tool description 최적화) | betas.ts |
| **불가** | A1 cli-internal beta header (서버측) | - |
| **불가** | M1 default Opus (API key tier 의존) | - |
| **불가** | T1 REPL tool (전체 다른 아키텍처) | - |
