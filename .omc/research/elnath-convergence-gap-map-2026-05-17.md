# Elnath Convergence Gap Map

Date: 2026-05-17 KST

Status: fresh gap map

Goal source:

- `/Users/stello/elnath/.omc/research/elnath-ultimate-goal-codex-claude-hermes-convergence-2026-05-17.md`

Primary conclusion:

> Elnath already has much of the control-surface substrate. The next gap is not
> "add more named tools." The next gap is making runtime supervision see and
> feed back real post-action evidence: actual file mutations, diagnostics after
> writes, user-visible progress, and session continuity.

Strategic order:

1. Product/runtime quality first.
2. Benchmark proof second.
3. Do not use v8 reruns as the roadmap.

## Current Repo State

Checked on 2026-05-17 after PR #254 merge and UX branch rebase start:

- Repo: `/Users/stello/elnath`
- Branch: `codex/user-input-operator-ux`
- origin/main: `0f432eb9555e37e0b16dad1350ac05bf09232d34`
- PR #254: merged, `https://github.com/StelloJae/Elnath/pull/254`
- Bubblewrap substrate for PR #254: PASS
- Seatbelt substrate for PR #254: PASS

Pre-existing dirty/untracked files observed before this artifact:

- modified: `.omc/research/elnath-final-autonomous-runtime-goal-2026-05-13.md`
- modified: `scripts/run_current_benchmark_wrapper.sh`
- modified: `scripts/test_benchmark_wrapper_v8_task_guidance.sh`
- modified: `scripts/test_current_benchmark_wrapper_completion_guards.sh`
- untracked: `.claude/`
- untracked: `docs/superpowers/plans/2026-04-30-elnath-local-managed-runtime.md`

This artifact is under `.omc/research` and may be ignored by git.

Current local branch adds:

- Telegram pending-question numeric fallback;
- Telegram native inline buttons for structured pending-question choices;
- Telegram callback-query answer enqueue and acknowledgement;
- Telegram HTTP client `reply_markup`, `answerCallbackQuery`, and
  `callback_query` parsing support.

Evidence artifact:

- `.omc/research/telegram-user-question-numeric-choice-ux-2026-05-17.md`

## Sources Inspected

### Elnath

Repo structure:

- `cmd/elnath`
- `internal/agent`
- `internal/tools`
- `internal/daemon`
- `internal/scheduler`
- `internal/worktree`
- `internal/skill`
- `internal/llm`
- `internal/providerproxy`
- `internal/agentic`
- `internal/conversation`
- `internal/orchestrator`

Specific files inspected or searched:

- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/commands.go`
- `cmd/elnath/runtime_status.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime_completion_retry.go`
- `cmd/elnath/cmd_provider.go`
- `cmd/elnath/cmd_proxy.go`
- `internal/agent/agent.go`
- `internal/agent/user_question_tool.go`
- `internal/tools/registry.go`
- `internal/tools/tool_search.go`
- `internal/tools/file.go`
- `internal/tools/code_symbols.go`
- `internal/tools/process_tools.go`
- `internal/skill/claude_compat.go`
- `internal/llm/provider.go`
- `internal/llm/openai.go`
- `internal/llm/responses_test.go`
- `internal/config/config_test.go`
- `docs/roadmap.md`
- `/Users/stello/llm_memory/Claude Valut/wiki/entities/elnath-progress-2026-05-15.md`

### Claude Code Source

Reference root:

- `/Users/stello/claude-code-src/src`

Specific surfaces inspected or mapped:

- `tools/ToolSearchTool/ToolSearchTool.ts`
- `tools/ToolSearchTool/prompt.ts`
- `tools/AskUserQuestionTool/AskUserQuestionTool.tsx`
- `tools/AskUserQuestionTool/prompt.ts`
- `tools/FileEditTool/FileEditTool.ts`
- `tools/FileEditTool/utils.ts`
- `tools/FileEditTool/UI.tsx`
- `tools/LSPTool/LSPTool.ts`
- `tools/LSPTool/formatters.ts`
- `components/LspRecommendation/LspRecommendationMenu.tsx`
- `services/lsp/*`
- `services/tools/*`
- `services/compact/*`
- `services/SessionMemory/*`
- `utils/permissions/*`
- `utils/hooks/fileChangedWatcher.ts`
- `utils/hooks/skillImprovement.ts`
- `types/hooks.ts`
- `components/StructuredDiff*`
- `components/permissions/*`
- task, cron, plan, worktree, todo tool directories

Use boundary:

- Flow and architecture reference only.
- No proprietary source, prompt, or error text copied.

### Hermes

Local reference root:

- `/Users/stello/.hermes/hermes-agent`

Local files inspected or mapped:

- `gateway/stream_consumer.py`
- `tools/clarify_gateway.py`
- `tools/managed_tool_gateway.py`
- `cron/scheduler.py`
- `cron/jobs.py`
- `tools/skills_tool.py`
- `tools/skills_guard.py`
- `tools/skill_usage.py`
- `agent/memory_manager.py`
- `agent/memory_provider.py`
- `tests/hermes_cli/test_session_handoff.py`
- `tests/gateway/test_stream_consumer*.py`
- `tests/tools/test_clarify_gateway.py`
- `tests/tools/test_skills_guard.py`
- `tests/tools/test_skill_usage.py`
- `tests/cron/test_cron_prompt_injection_skill.py`

Public Hermes v0.14.0 release inspected:

- `https://raw.githubusercontent.com/NousResearch/hermes-agent/main/RELEASE_v0.14.0.md`

High-value v0.14 references:

- OpenAI-compatible local proxy for OAuth providers.
- `x_search`.
- Microsoft Teams end-to-end stack.
- lazy installs / cold-start performance.
- `/handoff` live session transfer.
- native button UI for `clarify`.
- Discord channel history backfill.
- per-turn file-mutation verifier footer.
- LSP semantic diagnostics on every write.
- `computer_use` cua-driver backend for non-Anthropic providers.
- OpenRouter Pareto Code router.
- Codex app-server runtime with session reuse and retirement.
- trusted skills tap.
- API stream approval events.
- `tool_override` plugin surface.
- dangerous-command bypass closures and tool-error sanitization.
- `/subgoal` for active goal criteria.

### Codex

Official docs checked:

- `https://developers.openai.com/codex/cli`
- `https://developers.openai.com/codex/concepts/sandboxing`
- `https://developers.openai.com/codex/skills`
- `https://developers.openai.com/codex/app/features`

Codex reference points:

- Local coding agent, not model weights.
- Tool, shell, apply-patch, skill, tool-search, and reasoning-control surfaces.
- Sandbox and approval as separate but coupled controls.
- Skills use progressive disclosure.
- App supports local/worktree/cloud modes, built-in Git, terminal, PR flow.

## Current Elnath Strengths

### 1. Agent Loop

Evidence:

- `internal/agent/agent.go`

Current shape:

- message array is the primary state.
- provider streams events.
- tool calls are extracted from assistant messages.
- tools execute through registry/executor.
- tool results are fed back into the next model iteration.
- max iteration cap exists.
- ack-only continuation guard exists.
- provider retry/backoff exists.
- proactive context compaction exists.
- `RunResult` carries finish reason, usage, tool stats, reasoning effort, and loaded deferred tools.

Position:

- Strong foundation.
- Comparable to Codex/Claude style model-tool loop at a substrate level.
- Main gap is not loop existence. Main gap is richer post-action evidence injected into the loop.

### 2. Tool Registry and ToolSearch

Evidence:

- `internal/tools/registry.go`
- `internal/tools/tool_search.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/commands.go`

Current shape:

- tool registry has named tool dispatch.
- `ToolDefs()` exposes model-callable definitions.
- `tool_search` supports search and `select:` query.
- metadata includes category, surface, schema preview, deferred status, execution policy, concurrency, reversibility, and receipt.
- some tools can defer initial schema.

Position:

- Strong.
- This is already close to Claude Code ToolSearch and Codex skill/tool progressive disclosure intent.
- Remaining gap is not discovery. Remaining gap is "after tool use, what did the filesystem/runtime actually do?"

### 3. Control Surfaces

Evidence:

- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_command_execute_tool.go`
- `internal/daemon/task_tools.go`
- `internal/scheduler/task_tools.go`
- `internal/worktree/tools.go`
- `internal/tools/process_tools.go`
- `internal/agent/user_question_tool.go`

Current shape:

- discovery: `tool_search`
- task: `task_create`, `task_list`, `task_get`, `task_stop`, `task_output`, `task_monitor`, `task_update`
- user input: `ask_user_question`, `user_question_list`, `user_question_wait`, `user_question_answer`, `user_question_cancel`
- schedule: `schedule_create`, `schedule_list`, `schedule_delete`
- plan: `enter_plan_mode`, `exit_plan_mode`
- worktree: `enter_worktree`, `worktree_list`, `worktree_run`, `worktree_prune`, `exit_worktree`
- process: `process_start`, `process_monitor`, `process_wait`, `process_stop`
- skill: `skill_catalog`, `skill`, `create_skill`
- command: `command_catalog`, `runtime_command`
- scratchpad: `todo_write`
- code intelligence: `code_symbols`

Position:

- Broadly implemented.
- Product-boundary closeout is accurate for "named surfaces exist."
- But Claude/Hermes/Codex convergence requires more than naming tools. It requires native UX, automatic supervision, and feedback loops.

### 4. Skills and Compatibility

Evidence:

- `internal/skill/claude_compat.go`
- `internal/skill/catalog_tool.go`
- `internal/skill/invocation_tool.go`
- `internal/skill/creator.go`
- `internal/prompt/skill_catalog_node.go`
- `internal/prompt/skill_guidance_node.go`

Current shape:

- loads Claude-style `.claude/skills/**/SKILL.md`.
- loads Codex-style `.codex/skills`.
- loads `.agents/skills`.
- loads Claude commands as compatible command skills.
- maps Claude tool names to Elnath tool names.
- supports plugin-cache skill roots.
- has skill catalog, invocation, creation, trust/filtering tests.

Position:

- Strong compatibility layer.
- Next convergence point is Hermes-style skill usage feedback and safe skill improvement, not basic loading.

### 5. Provider / Model / Effort

Evidence:

- `internal/llm/provider.go`
- `internal/llm/responses_test.go`
- `cmd/elnath/commands.go`
- `cmd/elnath/cmd_provider.go`
- `cmd/elnath/runtime_provider.go`
- `cmd/elnath/runtime_status.go`
- `cmd/elnath/runtime_test.go`

Current shape:

- provider interface supports `Chat`, `Stream`, `Name`, `Models`.
- OpenAI Responses-compatible provider exists as `openai-responses`.
- config and env support `openai_responses` / generic responses keys.
- base URL, model, timeout, and reasoning effort are configurable.
- Codex OAuth can route into Responses provider path.
- `/provider`, `/model`, `/effort`, `/status` expose runtime state.
- auto effort routing exists and is tested.
- provider capabilities expose reasoning effort compatibility and fallback.

Position:

- Strong.
- Good enough for Kimi/Moonshot, MiniMax, OpenAI/Codex-style Responses endpoints if endpoint compatibility holds.
- Gap is cost/quality routing beyond simple effort routing: Hermes Pareto-style coding quality threshold, provider cost policy, and no-silent-fallback enforcement across all paths.

### 6. Process / Long-Running Work

Evidence:

- `internal/tools/process_tools.go`
- `cmd/elnath/cmd_explain.go`

Current shape:

- process manager supports start, monitor, wait, stop.
- default timeout: 10 minutes.
- max timeout: 60 minutes.
- bounded wait, tail limits, kill grace.
- literal `watch_text` support.

Position:

- Good substrate.
- Gap is user-visible async progress bridge: Codex event queue / Hermes gateway stream consumer style status surface.

### 7. Code Intelligence

Evidence:

- `internal/tools/code_symbols.go`
- `cmd/elnath/cmd_explain.go`

Current shape:

- Go-native symbol listing.
- workspace symbols.
- definition/references/hover.
- syntax diagnostics.
- edit-aware diagnostic deltas.

Position:

- Strong Go-native path.
- Gap is automatic post-write diagnostic routing and non-Go adapter strategy.

### 8. User Input

Evidence:

- `internal/agent/user_question_tool.go`
- `internal/learning/user_question_tools.go`
- `internal/learning/pending_questions.go`
- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_explain.go`

Current shape:

- `ask_user_question` emits structured question, options, timeout, request ID, session ID, answer command, receipt.
- pending question list/wait/answer/cancel paths exist.
- CLI answer path exists.
- Telegram/operator gateway answer path documented as product boundary replacement.

Position:

- Strong runtime path.
- Gap is native operator UX: Hermes-style buttons/text-capture and Claude Code-style integrated AskUserQuestion presentation.

## Major Gaps

### Gap 1: Per-Turn File Mutation Verifier

Status:

- Missing as a first-class automatic supervisor layer.

Evidence:

- `internal/tools/file.go` read/write/edit tools are direct filesystem tools.
- `write_file` detects identical full content.
- `edit_file` detects identical old/new strings and missing/duplicate matches.
- tools refresh read tracker after writes.
- no structured mutation receipt with before/after hash, line delta, operation, expected intent, unexpected churn.
- no per-turn footer summarizing actual file mutations injected into the next model turn.

Reference:

- Hermes v0.14 per-turn file-mutation verifier footer.
- Claude Code structured diff UX and file-change watcher references.
- Codex final report discipline and tool execution event model.

Why it matters:

- Elnath can know a tool returned success, but the model does not automatically get a compact verified disk-delta summary after the write.
- This is the key missing self-correction substrate.
- Without it, no-op/wrong-file/silent-overwrite cases rely too much on model behavior or later tests.

Recommended milestone:

- Implement mutation recorder for `write_file` and `edit_file`.
- Add structured mutation receipt fields:
  - `path`
  - `operation`
  - `changed`
  - `before_hash`
  - `after_hash`
  - `before_lines`
  - `after_lines`
  - `line_delta`
  - `failure_family`
- Accumulate per-run mutation summaries.
- Inject a concise footer after mutating tools before the next LLM iteration.
- Add completion gate signal when edit intent exists but mutation summary has no changed files.

Priority:

- P0.

### Gap 2: Automatic Diagnostics on Write

Status:

- Partially implemented.

Evidence:

- `code_symbols diagnostics` and `diagnostics_delta` exist.
- They are model-callable, not automatically triggered after every write.
- current product boundary excludes full multi-language LSP lifecycle.

Reference:

- Hermes v0.14 LSP semantic diagnostics on every write.
- Claude Code LSPTool and LspRecommendation UX.

Why it matters:

- Code intelligence exists, but the loop does not consistently force diagnostics into the next turn after mutation.
- Elnath can miss "I edited code, introduced syntax/semantic issue, then confidently final-answer" unless verification catches it later.

Recommended milestone:

- Start with Go automatic diagnostics after changed `.go` files.
- Use existing `code_symbols diagnostics_delta` logic or extracted internal helper.
- Feed diagnostic delta into mutation footer.
- For TypeScript/Python, add explicit policy first:
  - unsupported automatic diagnostics
  - available if local command/tool adapter configured
  - not full LSP parity claim

Priority:

- P0 after Gap 1 or bundled as a narrow extension if easy.

### Gap 3: User-Visible Async Progress Bridge

Status:

- Partially implemented.

Evidence:

- event sink exists.
- process tools can monitor/wait.
- daemon queue stores progress/summary.
- Telegram completion notifier exists.
- no Hermes-style streaming bridge from sync runtime callbacks to async platform delivery.

Reference:

- Hermes `gateway/stream_consumer.py`
- Codex app terminal/event queue model.
- Claude Code streaming tool progress and TUI surfaces.

Why it matters:

- Long autonomous tasks feel wedged even when alive.
- User frustration came from long runs without clear "why still going" signals.

Recommended milestone:

- Add runtime progress heartbeat/phase events around:
  - reference inspection
  - edit attempt
  - verification
  - retry
  - artifact write
- Surface through CLI first.
- Keep Telegram bridge thin.

Priority:

- P1.

### Gap 4: Native User Input UX

Status:

- Runtime path implemented, UX not complete.

Evidence:

- `ask_user_question` and pending question tools exist.
- CLI answer command exists.
- Product boundary says UI modal/native answer collection is outside current Go runtime boundary.

Reference:

- Hermes `tools/clarify_gateway.py` with pending entry, timeout, text fallback, native buttons.
- Claude Code `AskUserQuestionTool`.

Why it matters:

- A personal assistant must ask and resume naturally.
- Current runtime receipts are correct but not delightful.

Recommended milestone:

- Add Telegram inline button path for structured options.
- Preserve CLI answer path.
- Add duplicate final-send suppression and answer receipt.

Priority:

- P1.

### Gap 5: Session Handoff / Resume Recap

Status:

- Partially implemented through sessions, compaction, and history, but not Hermes-like handoff product.

Reference:

- Hermes `/handoff` live session transfer.
- Hermes session DB handoff state machine.
- Codex session/thread and worktree model.
- Claude Code remote session manager / session memory.

Why it matters:

- Elnath wants persistent personal assistant and swarm.
- That needs "pause here, resume there, pass to stronger model" semantics.

Recommended milestone:

- Add explicit session handoff state:
  - requested
  - claimed
  - running
  - completed
  - failed
- Add human-readable resume recap from receipts/tool stats/memory.
- Add stale/wedged session retirement reason.

Priority:

- P1.

### Gap 6: Skills Feedback and Self-Improvement

Status:

- Partially implemented.

Evidence:

- SKILL.md compatibility exists.
- skill invocation receipts exist.
- skill creation exists.

Reference:

- Codex skills progressive disclosure.
- Hermes skill usage, trusted taps, skill guards, skill improvement tests.
- Claude Code skill hooks.

Why it matters:

- Skills should not only run. They should become durable, trusted, pruned, improved, and measured.

Recommended milestone:

- Record skill use outcome:
  - selected skill
  - required tools
  - verification result
  - user-visible outcome
  - promotion candidate
- Add safe improvement proposal artifact, not automatic overwrite.

Priority:

- P2.

### Gap 7: Provider Cost / Quality Router

Status:

- Partially implemented.

Evidence:

- OpenAI Responses provider exists.
- base URL/model/effort/timeouts configurable.
- auto effort routing exists.
- provider status/check commands exist.

Reference:

- Hermes OpenRouter Pareto Code router and provider switching.
- Codex model/reasoning controls.

Why it matters:

- User wants automatic token/cost savings while preserving hard-task quality.
- Current auto effort is useful but simple.

Recommended milestone:

- Add explicit `routing policy` object:
  - task class
  - minimum quality tier
  - max cost tier
  - effort default
  - fallback allowed or forbidden
- Keep current simple heuristic as default.
- Expose `elnath provider route --explain` or extend existing provider status.

Priority:

- P2.

### Gap 8: Gateway / Delivery Router

Status:

- Partially implemented.

Evidence:

- Telegram/operator path exists.
- provider proxy exists.
- daemon status/queue exists.
- no broad Hermes-like multi-platform gateway abstraction as product target.

Reference:

- Hermes gateway/platform adapters, stream consumer, Teams/LINE/SimpleX expansions.

Why it matters:

- Elnath target is personal assistant across surfaces, but Telegram should remain thin until product UX stabilizes.

Recommended milestone:

- Define delivery-router interface:
  - message origin
  - home channel
  - delivery receipt
  - progress update
  - user answer
  - completion notification
- Do not add many platforms first.

Priority:

- P2.

### Gap 9: Sandbox / Approval Depth

Status:

- Implemented in Elnath terms, but not Codex-level OS sandbox parity.

Evidence:

- Elnath has permissions modes, command safety analysis, sandbox/net proxy work, tool scopes.
- Codex official docs emphasize OS-enforced sandbox + approval policy.

Reference:

- Codex sandboxing: workspace-write/default, approvals on-request, danger-full-access/never.
- Claude Code permissions and dangerous command patterns.
- Hermes dangerous-command bypass closures and tool-error sanitization.

Why it matters:

- Strong autonomy requires enforced boundaries, not only model instruction.

Recommended milestone:

- Keep current Elnath sandbox policy explicit.
- Add command error sanitization review.
- Add denial reason receipts.
- Do not claim Codex OS sandbox parity unless implemented/tested.

Priority:

- P2/P3 depending on risk lane.

### Gap 10: Benchmark/Public Proof

Status:

- Deferred.

Evidence:

- benchmark readiness is separate from product/runtime.
- existing memory warns not to overclaim v8 or superiority.

Reference:

- Elnath benchmark artifacts and memory.

Why it matters:

- Benchmark proof should validate runtime, not drive runtime design.

Recommended milestone:

- Resume only after P0/P1 product milestones improve the runtime.
- Start with small dogfood/control smoke, not full v8.

Priority:

- Later.

## Rank: Next Five Milestones

### 1. P0: Per-Turn File Mutation Verifier

Reason:

- Highest leverage.
- Directly addresses repeated "model said it changed something, evidence weak" pain.
- Enables better self-correction without benchmark loops.

Acceptance:

- write/edit tools emit structured mutation receipts.
- agent loop gathers mutation summaries for one turn.
- next model turn receives compact mutation footer.
- completion summary can mention verified changed files.
- tests cover:
  - write changes file and records hash/line delta
  - edit changes file and records hash/line delta
  - no-op write/edit is classified
  - mutation footer appears after mutating tool call
  - no mutation footer for read-only tools

Likely files:

- `internal/tools/file.go`
- new `internal/tools/mutation*.go` or `internal/agent/mutation*.go`
- `internal/agent/agent.go`
- `cmd/elnath/runtime_completion_observability.go`
- focused tests in `internal/tools` and `internal/agent` or `cmd/elnath`

### 2. P0: Automatic Go Diagnostics After Mutation

Reason:

- `code_symbols diagnostics_delta` already exists.
- Glue is missing.
- This makes Elnath catch errors more like Claude/Hermes.

Acceptance:

- changed `.go` file can produce diagnostic delta without model explicitly calling `code_symbols`.
- footer says whether diagnostics were skipped, clean, or new errors found.
- no full multi-language LSP parity claim.

Likely files:

- `internal/tools/code_symbols.go`
- new reusable diagnostic helper
- mutation footer integration
- focused tests with broken/fixed Go snippets

### 3. P1: Progress Bridge / Alive Status

Reason:

- Long work currently causes user anxiety.
- Product quality needs "what is happening now" visibility.

Acceptance:

- CLI/event sink exposes coarse phases.
- process wait and verification phases visible.
- no Telegram feature explosion.

Likely files:

- `internal/event`
- `internal/tools/process_tools.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_completion_observability.go`

### 4. P1: Native User Input UX

Reason:

- Runtime path exists; UX gap remains.
- Hermes `clarify` button flow is strong reference.

Acceptance:

- Telegram structured options can render as native buttons or explicit equivalent.
- answer receipts still bind to request/session.
- timeout/cancel path documented and tested.

Likely files:

- `internal/telegram`
- `internal/agent/user_question_tool.go`
- `internal/learning/user_question_tools.go`
- `cmd/elnath/cmd_task.go`

### 5. P1/P2: Session Handoff / Resume Recap

Reason:

- Personal assistant/swarm target needs continuity beyond one terminal.
- Hermes v0.14 `/handoff` is strong reference.

Acceptance:

- session handoff state exists.
- resume recap artifact generated.
- stale/wedged session can retire with reason.

Likely files:

- `internal/agent/session.go`
- `internal/conversation`
- `internal/daemon`
- `cmd/elnath`

## First Milestone Chosen

Chosen:

> P0: Per-Turn File Mutation Verifier

Why:

- Highest leverage product/runtime improvement.
- Directly improves Claude/Codex-like coding execution quality.
- Uses Hermes v0.14's strongest new coding reliability idea.
- Does not require benchmark reruns.
- Builds substrate for automatic diagnostics, self-correction, and trustworthy final claims.

Implementation style:

- Elnath-native Go.
- No proprietary source copying.
- Start with `write_file` and `edit_file`.
- Keep patch small.
- Add tests first where practical.

Non-goals for first milestone:

- no full multi-language LSP lifecycle.
- no benchmark run.
- no baseline.
- no Codex/Claude comparison.
- no Telegram UX changes.
- no broad self-healing promise.

## Post-PR251 Update

Date: 2026-05-17 KST

PR #251 shipped after the original gap map:

- delivery target routing for daemon tasks;
- bounded agentic activation command and loop;
- durable activation history;
- proposed task reference tracking;
- opt-in low-risk activation auto-enqueue;
- activation summaries through the delivery router;
- long-running tool progress heartbeats;
- compact agentic evidence CLI;
- session handoff state markers;
- agentic control-surface manifest visibility;
- truthful auto-enqueue autonomy status reporting.

Implication:

- Gap 3 user-visible async progress bridge is materially improved for tool
  progress and daemon delivery routing.
- Gap 5 session handoff / resume recap is improved by handoff recap, resume
  context, retired-session explicit resume, and session handoff state markers,
  but full Hermes-style atomic live transfer remains open.
- Gap 8 gateway / delivery router is improved by target-aware delivery routing,
  but broad multi-platform gateway parity remains open.
- Standing-goal activation is improved by bounded activation and opt-in
  low-risk auto-enqueue, but product dogfood with real controlled goals remains
  the next proof step.

Do not return to benchmark loops after PR #251. The next structural blocker
should be selected from product/runtime evidence. Current best candidates:

1. local controlled dogfood of activation auto-enqueue using temporary config
   and explicit evidence;
2. Hermes-style handoff claim/complete/fail lifecycle if cross-surface transfer
   is needed now;
3. operator-facing status polish for hidden runtime capabilities.

## Post-PR252 Update

Date: 2026-05-17 KST

PR #252 shipped after PR #251:

- `elnath agentic goal create`
- `elnath agentic goals`
- `elnath agentic signal create`
- `elnath agentic tasks`
- `Store.ListStandingGoals`
- `Store.ListAgenticTasks`
- fresh agentic DB optional approval-table handling for `agentic status` and
  task/lineage approval lookup.

Dogfood evidence:

- temporary config created one standing goal through CLI;
- manual signal CLI created one new goal signal;
- `agentic activate --once --json` triaged the signal and produced
  `proposed_task_ids=[1]`;
- `agentic task 1 --json` rendered the proposed task;
- `agentic tasks --status proposed --limit 5 --json` recovered task `#1`.

Implication:

- Gap 8 operator/control gateway improves materially: there is now a
  product-facing path for `standing goal -> manual signal -> activation ->
  proposed task -> task list`.
- Standing-goal activation is no longer only fixture/test backed for the
  manual operator path.
- Remaining gap: watcher/scheduler creation is not yet a CLI surface; it
  remains a separate product decision rather than a blocker for manual alpha
  dogfood.

Next best candidate:

1. Dogfood explicit `agentic enqueue` from the proposed task path using a
   temporary config and bounded flags.
2. If enqueue dogfood is clean, move to runtime/task lifecycle polish rather
   than benchmark.
3. If enqueue dogfood exposes missing daemon setup or operator status, patch
   that narrow product/runtime blocker.

## Post-Agentic-Approval-CLI Update

Date: 2026-05-17 KST

Dogfood after PR #252 exposed a concrete product/runtime gap:

- daemon execution reached `approval_required` for a high-risk `bash` call;
- `agentic status --json` and `agentic task 1 --json` showed the pending
  approval;
- terminal operator had no direct CLI to decide the request.

Patch on branch `codex/agentic-approval-cli` adds:

- `elnath agentic approvals [--limit n] [--json]`;
- `elnath agentic approve <approval-id> [--json]`;
- `elnath agentic deny <approval-id> [--json]`;
- help/i18n/test coverage for those surfaces.

Evidence artifact:

- `.omc/research/agentic-approval-cli-2026-05-17.md`

Implication:

- Gap 8 operator/control gateway is improved: pending approval attention can
  now be resolved by terminal CLI, not only by direct DB mutation or Telegram
  shell.
- A deeper gap remains: approved gateway requests are not yet continued or
  replayed through a bounded executor path.
- Another concrete daemon lifecycle gap remains: stopping the daemon while a
  worker is blocked can leave the queue task stuck as `running`.

Next best candidate:

1. graceful daemon shutdown / running-task retirement;
2. approved-request continuation or explicit approval-resume command;
3. daemon timeout config visibility/loader verification.

## Post-Daemon-Shutdown-Retirement Update

Date: 2026-05-17 KST

PR #253 shipped and merged:

- PR: `https://github.com/StelloJae/Elnath/pull/253`
- Merge commit: `66aa039f817e9a4be26a80c5bf01e82b50ed3887`

It adds daemon finalization protection:

- execution context cancellation no longer cancels the terminal queue write;
- running task stopped by daemon shutdown is classified as `task_canceled`;
- queue mark/delivery/envelope finalization use a short independent context.

Evidence artifact:

- `.omc/research/agentic-daemon-shutdown-retirement-2026-05-17.md`
- `.omc/research/pr253-agentic-approval-daemon-shutdown-closure-2026-05-17.md`

Implication:

- Gap 3 async progress/lifecycle bridge is improved: stopped daemon tasks should
  become operator-visible terminal failures rather than stuck `running` tasks.
- Gap 8 gateway/control operator loop is more durable after approval waits and
  stop requests.
- Approved-request continuation remains open.

Next best candidate:

1. approved-request continuation or explicit approval-resume command;
2. daemon timeout config visibility/loader verification;
3. fresh temp-DB operator-loop dogfood after merge/PR-ready verification.

## Post-Approval-Continuation Local Update

Date: 2026-05-17 KST

Local branch:

- `codex/approval-consumption`

Local commits after `origin/main`:

- `abc2cc6 feat(agentic): consume approved approvals once`
- `017f436 feat(agentic): wait for approved tool requests`

Artifacts:

- `.omc/research/approval-consumption-milestone-2026-05-17.md`
- `.omc/research/approval-live-wait-design-2026-05-17.md`

What changed:

- approved gateway requests can be consumed exactly once;
- consumed approval records link to the executing receipt;
- denied/different-actor approvals do not execute;
- optional live wait exists through `agentic.approval.wait_timeout_seconds`;
- default remains `0`, so existing non-blocking behavior is preserved;
- live wait approved path can continue the same tool request and execute it;
- denied and timeout paths stay receipt-backed and non-executing.

Why `approve --resume` was not the first implementation:

- current agentic task lineage has one queue-task linkage;
- re-enqueueing the same task would require broader queue/task lineage
  semantics;
- a fresh follow-up task would not naturally consume the original approval
  request without adding resume-envelope semantics;
- bounded live wait solves same-session approval continuation with smaller
  product/runtime risk.

Verification:

- `go test ./cmd/elnath -count=1` passed;
- `go test ./internal/agentic/... ./internal/daemon ./internal/config -count=1`
  passed;
- `go vet ./...` passed;
- focused approval-consumption and live-wait tests passed;
- `git diff --check` on touched milestone files passed.

Implication:

- The "approved-request continuation" gap is no longer the next product/runtime
  blocker for opt-in live sessions.
- Offline/after-the-fact `approve --resume` remains an intentional later design,
  not a blocker for the current convergence lane.

## Post-Gap-Map Reality Correction

Date: 2026-05-17 KST

After re-reading current `origin/main`, the original Gap 1 and Gap 2 entries are
now stale as "next" milestones. They were valid when the gap map was first
written, but current code already contains the mutation/diagnostic substrate:

- `internal/tools/mutation.go`
- `internal/tools/file.go`
- `internal/agent/mutation_footer.go`
- `internal/agent/agent.go`
- `cmd/elnath/runtime_completion_observability.go`

Evidence artifacts already present:

- `.omc/research/elnath-mutation-verifier-milestone-2026-05-17.md`
- `.omc/research/elnath-go-diagnostics-after-mutation-milestone-2026-05-17.md`
- `.omc/research/pr234-convergence-mutation-verifier-closure-2026-05-17.md`
- `.omc/research/pr239-completion-mutation-diagnostics-closure-2026-05-17.md`
- `.omc/research/pr240-structured-mutation-receipts-closure-2026-05-17.md`
- `.omc/research/pr241-composite-mutation-receipts-closure-2026-05-17.md`
- `.omc/research/pr242-non-go-diagnostic-policy-closure-2026-05-17.md`
- `.omc/research/pr243-python-diagnostic-adapter-closure-2026-05-17.md`

Current implementation facts:

- `write_file` and `edit_file` attach structured `FileMutation` receipts;
- mutation receipts include path, operation, changed flag, hash, line counts,
  line delta, diagnostic language/status/counts, and failure family;
- the agent loop appends a `[Filesystem mutation verifier]` footer after
  mutating tools before the next model turn;
- Go parser diagnostic deltas run automatically after `.go` mutations;
- Python syntax diagnostics and TypeScript/JavaScript conditional syntax
  diagnostics are represented through explicit adapter policies;
- completion observability can derive diagnostic receipts from structured
  mutation receipts and trusted mutation verifier text.

Updated next candidates:

1. PR-ready closeout for local approval continuation work on
   `codex/approval-consumption`.
2. Native user-input/operator UX: make `ask_user_question` and pending-answer
   flows feel like a first-class Hermes/Claude-style operator path, within the
   existing Go/Telegram boundary.
3. Session handoff/resume recap: improve continuity and stale-session recovery
   beyond substrate markers.
4. Async progress/alive-status polish after the approval continuation PR is
   either merged or parked.

## Post-Telegram-Operator-UX Local Update

Date: 2026-05-17 KST

Local branch:

- `codex/user-input-operator-ux`

Local commit:

- `3a6975f37aa65c9b94b66960b7be6e262b76ff15`
  `feat(telegram): improve pending question choice UX`

Artifact:

- `.omc/research/telegram-user-question-numeric-choice-ux-2026-05-17.md`

What changed:

- Telegram `/questions` renders structured choices as numbered text.
- `/answer <session> <request> <number>` normalizes to the selected option.
- Bound-chat plain text numeric answers normalize to the selected option when
  exactly one pending bound question exists.
- Daemon `user_question_answer` accepts numeric structured choices through the
  same validator-backed path.
- `elnath explain pending-questions` advertises numeric answer commands.
- Telegram `/questions` sends native inline buttons when the bot client
  supports button messages.
- Telegram callback queries with `uq:<request_id>:<choice_number>` enqueue the
  answer-resume task, acknowledge the callback, and send the same operator
  receipt.
- Telegram HTTP client now supports `reply_markup.inline_keyboard`,
  `answerCallbackQuery`, and `callback_query` parsing.

Reference impact:

- Gap 4 Native User Input UX is materially improved for Telegram structured
  choices.
- This adopts the Hermes `clarify` button/fallback product pattern in
  Elnath-native Go without copying source, prompts, or error strings.
- Claude Code AskUserQuestion parity remains broader than this slice; terminal
  modal UX and richer interrupt/redirect UX remain separate.

Verification:

- `go test ./internal/telegram -run 'TestShellQuestionsCommandSendsChoiceButtons|TestShellQuestionChoiceCallbackEnqueuesAnswer' -count=1`
  passed.
- `go test ./internal/telegram -run 'TestHTTPClient(SendMessageWithButtons|AnswerCallback|GetUpdatesParsesCallbackQuery)' -count=1`
  passed.
- `go test ./internal/telegram -count=1` passed.
- `go test ./cmd/elnath ./internal/telegram ./internal/daemon ./internal/learning -count=1`
  passed.
- `git diff --check` on touched UX/artifact files passed.

Remaining user-input UX gaps:

- Terminal-native modal/interactive choice UX is not implemented.
- Callback payloads are request/choice based; no signed/expiring callback token
  layer yet.
- Multi-select questions remain outside scope.
- Cross-surface answer routing beyond Telegram/CLI remains future gateway work.

Updated next candidates:

1. Park this local UX milestone until enough local work is batched for one PR,
   unless review pressure requires opening it.
2. Session handoff/resume recap: improve continuity and stale-session recovery
   beyond substrate markers.
3. Async progress/alive-status polish for local interactive runs.
4. Terminal-native user-input choice UX if operator friction remains higher
   than continuity friction.

## Post-Session-Handoff-Lifecycle Local Update

Date: 2026-05-17 KST

Local branch:

- `codex/user-input-operator-ux`

Artifact:

- `.omc/research/session-handoff-lifecycle-cli-2026-05-17.md`

What changed:

- `elnath task handoff <id>` now accepts `--state STATE`,
  `--surface SURFACE`, and `--reason TEXT`.
- Operators can mark handoff states such as `claimed`, `running`,
  `completed`, or `failed` from the CLI.
- Existing `--request SURFACE` behavior remains.
- The command rejects mixing `--request` and `--state`.
- The same recap output path shows the latest handoff state in plain text,
  JSON, and markdown.

Reference impact:

- Gap 5 Session Handoff / Resume Recap improves again: the state machine was
  already present in the session layer, and now has a direct operator CLI path.
- This moves toward Hermes `/handoff` lifecycle semantics without claiming
  live cross-process transfer parity.

Verification:

- `go test ./cmd/elnath -run TestCmdTaskHandoffWithQueueRecordsLifecycleState -count=1`
  failed before implementation with `unknown task handoff flag: --state`.
- `go test ./cmd/elnath -run 'TestCmdTaskHandoffWithQueue(RecordsLifecycleState|RequestRecordsHandoffState|PrintsResumeRecap|MarkdownOutput|SaveWritesMarkdown)|TestBuildTaskResumeHandoffContextIncludesCompactRecap' -count=1`
  passed.
- `go test ./cmd/elnath -count=1` passed.
- `go test ./internal/agent -run 'TestRecordHandoffAndLoadStatus|TestRecordHandoffRejectsUnknownState|TestLoadSessionSkipsResumeLines' -count=1`
  passed.

Remaining handoff gaps:

- No live runtime migration between processes yet.
- No signed remote claimant identity yet.
- Gateway surfaces other than CLI do not yet expose lifecycle state changes.

Updated next candidates:

1. progress/alive-status polish for long local/daemon work;
2. terminal-native user-input choice UX;
3. gateway exposure for handoff state if CLI behavior remains stable.

## Post-Task-Progress-CLI-Render Local Update

Date: 2026-05-17 KST

Local branch:

- `codex/user-input-operator-ux`

Artifact:

- `.omc/research/task-progress-cli-render-2026-05-17.md`

What changed:

- `elnath task monitor <id>` now renders structured progress envelopes as
  human-readable text in plain output.
- `elnath task output <id> --field progress` now does the same.
- `--json` remains raw and machine-readable.

Reference impact:

- Gap 3 Progress Bridge / Alive Status improves for CLI task observation.
- This does not add a new streaming TUI; it removes raw JSON leakage from
  existing progress observation surfaces.

Verification:

- `go test ./cmd/elnath -run 'TestCmdTask(MonitorWithQueueRendersStructuredProgress|OutputWithQueueRendersStructuredProgress)' -count=1`
  failed before implementation because raw progress JSON was printed.
- `go test ./cmd/elnath -run 'TestCmdTask(MonitorWithQueueRendersStructuredProgress|OutputWithQueueRendersStructuredProgress|MonitorWithQueueShowsSnapshot|OutputWithQueueReturnsTail)' -count=1`
  passed.
- `go test ./cmd/elnath -count=1` passed.
- `go test ./internal/daemon -run 'Test(DeliveryRouter_OnProgressParsesAndRoutes|TaskOutputToolReadsProgressField|TaskMonitorTool)' -count=1`
  passed.
- `git diff --check -- cmd/elnath/cmd_task.go cmd/elnath/cmd_task_test.go`
  passed.

Remaining progress/alive gaps:

- No full rich TUI or streaming progress timeline.
- Telegram/gateway progress formatting remains a separate surface.
- Long-running local command observation can still be improved with better
  phase/heartbeat summaries.

Updated next candidates:

1. terminal-native user-input choice UX;
2. gateway handoff lifecycle exposure;
3. PR-ready branch cleanup after enough UX work is batched.

## Post-Task-Answer-Choice-CLI Local Update

Date: 2026-05-17 KST

Local branch:

- `codex/user-input-operator-ux`

Artifact:

- `.omc/research/task-answer-choice-cli-2026-05-17.md`

What changed:

- `elnath task answer` now supports `--choice N`.
- `--choice N` feeds the existing validator-backed answer normalization path.
- `--answer TEXT` remains supported.
- using both `--answer` and `--choice` is rejected.
- `elnath explain pending-questions` now suggests `--choice N` for numbered
  structured choices.

Reference impact:

- Gap 4 Native User Input UX improves for terminal operators.
- This keeps Elnath's existing receipt-backed queue path while making structured
  choice intent explicit.

Verification:

- `go test ./cmd/elnath -run 'TestCmdTaskAnswerWithQueueAcceptsChoiceFlag|TestExplainPendingQuestionsTextShowsAnswerCommand' -count=1`
  failed before implementation because `--choice` was unknown and
  `explain pending-questions` still suggested `--answer 'N'`.
- `go test ./cmd/elnath -run 'TestCmdTaskAnswerWithQueue(AcceptsChoiceFlag|EnqueuesBoundAnswer|RejectsStaleRequest)|TestExplainPendingQuestionsTextShowsAnswerCommand' -count=1`
  passed.
- `go test ./cmd/elnath -count=1` passed.
- `go test ./internal/daemon -run 'TestUserQuestionAnswerTool' -count=1`
  passed.
- `git diff --check -- cmd/elnath/cmd_task.go cmd/elnath/cmd_task_test.go cmd/elnath/cmd_explain.go cmd/elnath/cmd_explain_test.go`
  passed.

Remaining terminal input gaps:

- This is still command-driven, not an interactive picker.
- Multi-select questions remain outside scope.

Updated next candidates:

1. open one draft PR for the batched local UX milestones;
2. continue with one more gateway/handoff UX slice only if PR #254 remains
   parked and branch size is still acceptable.

## Post-Telegram-Handoff-Command Local Update

Date: 2026-05-17 KST

Local branch:

- `codex/user-input-operator-ux`

Artifact:

- `.omc/research/telegram-handoff-command-2026-05-17.md`

What changed:

- Telegram shell now supports `/handoff <task_id>`.
- Telegram shell now supports `/handoff <task_id> <state> [reason]` for
  lifecycle states such as `claimed`, `running`, `completed`, and `failed`.
- `WithShellDataDir` gives Telegram shell access to session JSONL metadata.
- Daemon-embedded Telegram and standalone `elnath telegram shell` now pass
  `cfg.DataDir`.

Reference impact:

- Gap 5 Session Handoff / Resume Recap improves on the Telegram gateway.
- This is Hermes-style operator continuity without claiming live runtime
  migration.

Verification:

- `go test ./internal/telegram -run 'TestShellHandoffCommand(RendersTaskRecap|RecordsLifecycleState)' -count=1`
  failed before implementation because `WithShellDataDir` and `/handoff`
  command support did not exist.
- `go test ./internal/telegram -run 'TestShellHandoffCommand(RendersTaskRecap|RecordsLifecycleState)' -count=1`
  passed.
- `go test ./internal/telegram -count=1` passed.
- `go test ./cmd/elnath -run 'Test(CommandHelpers|CmdTelegram|CmdDaemon|Telegram|Daemon)' -count=1`
  passed.
- `git diff --check -- internal/telegram/shell.go internal/telegram/shell_test.go cmd/elnath/cmd_daemon.go cmd/elnath/cmd_telegram.go`
  passed.

Remaining handoff/gateway gaps:

- No live runtime migration.
- No remote claimant authentication.
- No automatic cross-surface handoff notification.

Updated next candidates:

1. broad local verification for this UX batch;
2. sequence against PR #254 because both branches add/update the same
   convergence gap map artifact;
3. open one draft PR for this coherent UX batch only after sequencing is clear.

## Operator-UX-Batch PR-Readiness Update

Date: 2026-05-17 KST

Local branch:

- `codex/user-input-operator-ux`

Head:

- `119807a docs(research): record operator ux batch readiness`

Artifact:

- `.omc/research/operator-ux-batch-pr-readiness-2026-05-17.md`

Batch scope:

- Telegram pending-question numeric fallback and inline buttons;
- terminal `elnath task answer --choice N`;
- CLI handoff lifecycle state recording;
- plain text task progress rendering;
- Telegram `/handoff` recap and state recording.

Verification:

- `go test ./cmd/elnath ./internal/telegram ./internal/daemon ./internal/learning ./internal/agent -count=1`
  passed.
- `go vet ./...` passed.
- `git diff --check origin/main..HEAD` passed.
- Post-PR254 rebase verification also passed:
  - `go test ./cmd/elnath ./internal/telegram ./internal/daemon ./internal/learning ./internal/agent -count=1`
  - `go vet ./...`
  - `git diff --check origin/main..HEAD`

Sequencing with PR #254:

- PR #254 merged as `0f432eb9555e37e0b16dad1350ac05bf09232d34`.
- Production files did not overlap this UX batch.
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md` overlapped as an
  artifact and was reconciled during the rebase.
- Do not create another tiny PR; this branch is now one coherent PR-sized
  local batch.

## Claim Boundary

Allowed after this gap map:

- Elnath has a current convergence gap map.
- The post-PR252 operator dogfood has identified and patched a missing terminal
  approval-decision surface.
- The daemon shutdown retirement patch is locally verified.
- Approved-request continuation has a local, tested opt-in live-wait milestone
  on `codex/approval-consumption`.
- Per-turn file mutation verifier and automatic diagnostics-after-mutation are
  no longer the next product blockers; current code already contains those
  substrates.
- Telegram structured pending questions now have a local, tested native button
  and numeric fallback milestone on `codex/user-input-operator-ux`.
- Handoff lifecycle states now have a local, tested CLI milestone on
  `codex/user-input-operator-ux`.
- Plain text task monitor/output progress now has a local, tested rendering
  polish milestone on `codex/user-input-operator-ux`.
- Terminal pending-question answers now have a local, tested `--choice N`
  milestone on `codex/user-input-operator-ux`.
- Telegram handoff recap/state now has a local, tested gateway milestone on
  `codex/user-input-operator-ux`.
- The operator UX branch is locally verified as one coherent PR-sized batch.
- The next highest-leverage product/runtime candidates are approval
  continuation closeout, remaining user-input/operator UX, session
  handoff/recap, and progress/alive-status polish.
- Benchmark remains downstream.

Forbidden:

- Elnath is complete as a daily-driver assistant.
- Elnath beats Codex or Claude Code.
- Elnath matches Hermes continuity/companion UX fully.
- Full v8 benchmark success.
- Full Codex/Claude/Hermes parity.

## Verification for This Artifact

Commands run:

- `test -f /Users/stello/elnath/.omc/research/elnath-convergence-gap-map-2026-05-17.md`
  passed.
- `wc -l /Users/stello/elnath/.omc/research/elnath-convergence-gap-map-2026-05-17.md`
  returned `1539`.
- `rg -n "Operator-UX-Batch|operator-ux-batch|119807a|0f432eb|go vet|PR #254|Claim Boundary" /Users/stello/elnath/.omc/research/elnath-convergence-gap-map-2026-05-17.md /Users/stello/elnath/.omc/research/operator-ux-batch-pr-readiness-2026-05-17.md`
  found the operator UX batch update, readiness artifact link, current head
  commit, PR #254 merge sequencing, `go vet` evidence, and claim boundary.
- `git diff --check -- .omc/research/operator-ux-batch-pr-readiness-2026-05-17.md .omc/research/elnath-convergence-gap-map-2026-05-17.md`
  passed.

## Post-Task-Monitor-Alive-Status Local Update

Date: 2026-05-17 KST

Local branch:

- `codex/progress-alive-status`

Artifact:

- `.omc/research/task-monitor-alive-status-cli-2026-05-17.md`

What changed:

- `elnath task monitor <id>` plain output now shows existing monitor timing
  evidence:
  - `Age`
  - `Running`
  - `Idle`
- JSON output remains unchanged.

Reference impact:

- Gap 3 Progress Bridge / Alive Status improves again.
- This follows the Claude Code/Hermes product pattern of keeping long-running
  work visibly alive without adding benchmark loops or changing task runtime
  semantics.

Verification:

- `go test ./cmd/elnath -run TestCmdTaskMonitorWithQueueShowsRunningAndIdleAge -count=1`
  failed before implementation because plain monitor output did not include
  alive/idle timing fields.
- `go test ./cmd/elnath -run 'TestCmdTaskMonitorWithQueue(ShowsRunningAndIdleAge|ShowsSnapshot|RendersStructuredProgress)' -count=1`
  passed.

Remaining progress/alive gaps:

- Telegram/gateway progress formatting remains separate.
- No full rich TUI or streaming progress timeline.
- No daemon timeout policy change.

Updated next candidates:

1. Telegram/gateway progress formatting;
2. session handoff automatic cross-surface notification;
3. richer terminal timeline only after operator demand justifies it.

## Post-Telegram-Progress-Text-Delivery Local Update

Date: 2026-05-17 KST

Local branch:

- `codex/progress-alive-status`

Artifact:

- `.omc/research/telegram-progress-text-delivery-2026-05-17.md`

What changed:

- Telegram progress delivery now renders generic daemon text progress events.
- `ProgressReporter` gained `ReportText`.
- Unhandled `daemon.TextProgressEvent` messages now become short escaped bullet
  lines instead of disappearing.
- Tool, stage, and summary progress routes remain unchanged.

Reference impact:

- Gap 3 Progress Bridge / Alive Status improves on the Telegram gateway.
- This moves toward Claude Code/Hermes-style visible long-running work without
  adding full remote keep-alive or timeline storage.

Verification:

- `go test ./internal/telegram -run TestSinkNotifyProgressRendersTextEvent -count=1`
  failed before implementation because no Telegram sent/edited message
  contained the generic text progress.
- `go test ./internal/telegram -run 'TestSinkNotifyProgressRendersTextEvent|TestSinkOnProgressRoutesToProgressReporter|TestSinkOnProgressSummaryRoutesToStream' -count=1`
  passed.

Remaining progress/alive gaps:

- Non-Telegram gateways may still need equivalent progress formatting.
- No full timeline store.
- No remote keep-alive protocol parity.

Updated next candidates:

1. focused verification for this combined progress/alive branch;
2. PR-ready branch cleanup if verification passes;
3. session handoff automatic cross-surface notification after this branch.

## Post-Telegram-Handoff-Notification Local Update

Date: 2026-05-17 KST

Branch:

- `codex/handoff-notification`

Artifact:

- `.omc/research/telegram-handoff-notification-2026-05-17.md`

What changed:

- Telegram streaming completion notifications now include
  `Handoff: /handoff <task_id>` when the completed task has a session ID.
- Telegram shell-polled completion notifications now include
  `handoff: /handoff <task_id>` when the completed task has a session ID.
- The handoff command guard is centralized in `telegramHandoffCommand`.

Reference impact:

- Closes the previously listed "automatic cross-surface handoff notification"
  gap at the operator-notification level.
- Does not claim live runtime migration, remote claimant authentication, or full
  Hermes handoff parity.

Verification:

- `go test ./internal/telegram -run 'TestSinkNotifyCompletionIncludesHandoffHint|TestShellNotifyCompletionsUpdatesBinder' -count=1`
  passed.
- `go test ./internal/telegram -count=1` passed.
- `go test ./cmd/elnath ./internal/telegram ./internal/daemon -count=1`
  passed.
- `git diff --check -- internal/telegram/sink.go internal/telegram/shell.go internal/telegram/sink_test.go internal/telegram/shell_test.go`
  passed.
- `go vet ./...` passed.

Remaining handoff/gateway gaps:

- No live runtime migration.
- No remote claimant authentication.
- Gateway handoff lifecycle exposure still remains a candidate if product need
  persists.

## Post-Session-Handoff-Transition-Guard Local Update

Date: 2026-05-17 KST

Branch:

- `codex/handoff-notification`

Artifact:

- `.omc/research/session-handoff-transition-guard-2026-05-17.md`

What changed:

- Session handoff writes now validate lifecycle transitions before appending
  JSONL handoff events.
- Terminal states `completed` / `failed` can be retried only by starting a new
  `requested` handoff.
- Stale active writes after terminal completion are rejected as invalid
  transitions.

Reference impact:

- Moves Elnath's handoff behavior closer to Hermes-style explicit lifecycle
  discipline while staying compatible with existing Elnath CLI/Telegram direct
  operator flows.
- Does not claim distributed atomic gateway claim or full Hermes handoff parity.

Verification:

- `go test ./internal/agent -run TestRecordHandoffRejectsInvalidTransition -count=1`
  failed before implementation because terminal-first handoff writes were
  accepted.
- `go test ./internal/agent -run 'TestRecordHandoff(AndLoadStatus|RejectsUnknownState|RejectsInvalidTransition)' -count=1`
  passed.
- `go test ./cmd/elnath ./internal/telegram ./internal/agent -run 'TestRecordHandoff|TestCmdTaskHandoffWithQueue|TestShellHandoffCommand' -count=1`
  passed.
- `go test ./cmd/elnath ./internal/telegram ./internal/daemon ./internal/agent -count=1`
  passed.
- `go vet ./...` passed.
- `git diff --check` passed.

Remaining handoff/gateway gaps:

- No distributed atomic gateway claim lock.
- No remote claimant authentication.
- No live runtime migration.

## Post-Task-Pending-Handoffs-Surfaces Local Update

Date: 2026-05-17 KST

Branch:

- `codex/handoff-pending`

Artifact:

- `.omc/research/task-pending-handoffs-cli-2026-05-17.md`

What changed:

- Added `elnath task handoffs`.
- Added `elnath task handoffs --json`.
- Added Telegram `/handoffs`.
- Pending handoff list includes only latest handoff state `requested`.
- Claimed/running/completed/failed handoffs are excluded.
- Each pending row includes the operator claim command:
  `elnath task handoff <id> --state claimed --surface cli`.
- Telegram pending rows include `/handoff <id> claimed`.

Reference impact:

- Moves Gap 5 / Gap 8 closer to Hermes-style `list_pending_handoffs` flow.
- Gives Elnath CLI and Telegram operators a discover -> claim path instead of
  requiring the task ID to already be known.
- Does not claim distributed atomic gateway claim, remote claimant auth, live
  runtime migration, or full Hermes parity.

Verification:

- `go test ./cmd/elnath -run 'TestCmdTaskHandoffsWithQueue' -count=1`
  failed before implementation because `cmdTaskHandoffsWithQueue` did not
  exist.
- `go test ./cmd/elnath -run 'TestCmdTaskHandoffsWithQueue' -count=1`
  passed.
- `go test ./internal/telegram -run TestShellHandoffsCommandListsPendingOnly -count=1`
  passed.
- `go test ./cmd/elnath ./internal/agent -run 'TestCmdTaskHandoff|TestCmdTaskHandoffs|TestRecordHandoff' -count=1`
  passed.
- `go test ./cmd/elnath ./internal/telegram ./internal/agent ./internal/daemon -count=1`
  passed.
- `go vet ./...` passed.
- `git diff --check` passed.

Remaining handoff/gateway gaps:

- No distributed atomic gateway claim lock.
- No remote claimant authentication.
- No automatic gateway watcher loop.
- No live runtime migration.

## Post-Skill-Usage-Outcome-Receipts Local Update

Date: 2026-05-18 KST

Branch:

- `codex/skill-usage-outcomes`

Artifact:

- `.omc/research/skill-usage-outcome-receipts-2026-05-18.md`

What changed:

- Skill usage records now carry outcome-oriented fields:
  `required_tools`, `verification_result`, `user_outcome`,
  `promotion_candidate`, and `improvement_proposal_path`.
- `skill_catalog action=usage` now exposes aggregated required tools,
  verification result counts, promotion candidates, latest user outcome, and
  latest improvement proposal path.
- Model-callable and slash skill executions now record required tools and
  explicit `verification_result:not_run` outcome receipts.
- `create_skill action=propose_improvement` now writes a review artifact for a
  proposed skill improvement instead of editing `SKILL.md`.

Reference impact:

- Advances Gap 6 from basic usage visibility toward Hermes-style sidecar
  telemetry and Claude-style improvement detection, while staying safer than
  automatic rewrite behavior.
- Does not claim full Hermes curator lifecycle, automatic pruning, automatic
  promotion, or full Claude Code skill-improvement hook parity.

Verification:

- `go test ./internal/skill -run 'TestTracker|TestInvocationToolRecordsSkillUsage|TestCatalogToolReportsUsageStats' -count=1`
  passed.
- `go test ./internal/skill ./internal/tools -run 'TestTracker|TestInvocationToolRecordsSkillUsage|TestCatalogToolReportsUsageStats|TestSkillToolProposeImprovement|TestSkillToolScope' -count=1`
  passed.
- `go test ./cmd/elnath ./internal/skill ./internal/tools -count=1`
  passed.
- `go vet ./...` passed.
- `git diff --check` passed.

Remaining skill feedback gaps:

- No reviewed apply path for improvement proposals yet.
- No automatic skill lifecycle curator.
- No skill-specific verifier ownership model beyond explicit `not_run`
  classification in usage receipts.

## Post-Skill-Improvement-Proposal-Apply Local Update

Date: 2026-05-18 KST

Branch:

- `codex/skill-improvement-apply`

Artifact:

- `.omc/research/skill-improvement-proposal-apply-2026-05-18.md`

What changed:

- Added `Tracker.ReadImprovementProposal(path)` with proposal-dir confinement.
- Added parsing for Elnath-generated skill improvement proposal markdown.
- Added `Creator.ApplyImprovementProposal(path)` for wiki-native skills.
- Added `create_skill action=apply_improvement`.
- Applying requires `approved:true`.
- Applying appends an explicit applied-improvement note instead of asking an
  LLM to rewrite the whole skill file.

Reference impact:

- Closes the immediate "proposal exists but cannot be applied through a bounded
  path" gap after PR #259.
- Moves Elnath closer to Claude Code-style skill improvement flow while keeping
  Elnath's safer receipt/review posture.
- Does not claim automatic skill improvement or full Hermes curator parity.

Verification:

- `go test ./internal/skill ./internal/tools -run 'TestTracker(Read|Write)Improvement|TestCreatorApplyImprovementProposal|TestSkillTool(ProposeImprovement|ApplyImprovement|Scope)' -count=1`
  passed.
- `go test ./cmd/elnath ./internal/skill ./internal/tools -count=1`
  passed.
- `go vet ./...` passed.
- `git diff --check` passed.

Remaining skill feedback gaps:

- No CLI-specific proposal list/show/apply UX yet.
- Apply path is mechanical append, not natural skill rewrite.
- No automatic skill lifecycle curator.

## Post-Skill-Proposals-CLI-Review-UX Local Update

Date: 2026-05-18 KST

Branch:

- `codex/skill-proposals-cli`

Artifact:

- `.omc/research/skill-proposals-cli-review-ux-2026-05-18.md`

What changed:

- Added `elnath skill proposals list`.
- Added `elnath skill proposals list --json`.
- Added `elnath skill proposals show <proposal-file>`.
- Added `elnath skill proposals show <proposal-file> --json`.
- Added `elnath skill proposals apply <proposal-file>`.
- Added `elnath skill proposals apply <proposal-file> --yes`.
- Added tracker proposal listing sorted newest-first.
- Apply without `--yes` asks for confirmation and cancels safely on `n`.

Reference impact:

- Closes the immediate CLI/operator review UX gap after PR #260.
- Moves Elnath closer to a practical skill self-improvement loop:
  observe -> propose -> review -> approved apply.
- Does not claim automatic skill improvement, natural rewrite parity, or full
  Hermes curator lifecycle.

Verification:

- `go test ./internal/skill ./cmd/elnath -run 'TestTrackerListImprovementProposals|TestCmdSkillProposals' -count=1`
  passed.
- `go test ./cmd/elnath ./internal/skill ./internal/tools -count=1`
  passed.
- `go vet ./...` passed.
- `git diff --check` passed.

Remaining skill feedback gaps:

- Apply path is mechanical append, not natural skill rewrite.
- No automatic lifecycle curator.
- No Telegram/operator proposal review surface.

## Post-PR261 Runtime Progress / Alive Status Milestone Update

Date: 2026-05-18 KST

Branch:

- `codex/runtime-progress-status`

Artifact:

- `.omc/research/runtime-progress-alive-status-2026-05-18.md`

What changed:

- Added typed `event.RuntimeProgressEvent`.
- Added daemon progress kind `runtime`.
- Added runtime phase progress forwarding to CLI, daemon progress observers,
  and legacy CLI callbacks.
- Added phase events for prompt build, workflow run, completion check,
  session persistence, completion retry, and verification retry.
- Added parsed `progress_event` metadata to `task_monitor` tool output and
  `elnath task monitor --json` output.

Reference impact:

- Moves Elnath closer to Hermes-style "the run is alive" visibility without
  adding a separate streaming subsystem.
- Moves Elnath closer to Claude Code-style progress grouping at the substrate
  level while not claiming TUI parity.

Verification:

- `go test ./internal/event ./internal/daemon ./cmd/elnath -run 'TestRuntimeProgressEvent|TestOnTextToSinkRuntimeProgressEncodesJSON|TestProgressObserverDispatchesRepresentativeEventTypesAndIgnoresUnknown|TestLegacyCallbackObserverDispatchesRepresentativeEventTypesAndIgnoresUnknown|TestExecutionRuntimeRunTaskEmitsRuntimePhaseProgress|TestDeliveryRouter_OnProgressParsesAndRoutes|TestCmdDaemonStatusRendersStructuredProgressEnvelope' -count=1`
  passed.
- `go test ./internal/event ./internal/daemon ./cmd/elnath -run 'TestRuntimeProgressEvent|TestOnTextToSinkRuntimeProgressEncodesJSON|TestProgressObserverDispatchesRepresentativeEventTypesAndIgnoresUnknown|TestLegacyCallbackObserverDispatchesRepresentativeEventTypesAndIgnoresUnknown|TestExecutionRuntimeRunTaskEmitsRuntimePhaseProgress|TestDeliveryRouter_OnProgressParsesAndRoutes|TestCmdDaemonStatusRendersStructuredProgressEnvelope|TestTaskMonitorToolReturnsParsedProgressEvent|TestCmdTaskMonitorWithQueueJSONIncludesParsedProgressEvent' -count=1`
  passed.
- `go test ./cmd/elnath ./internal/event ./internal/daemon -count=1`
  passed.
- `go vet ./...` passed.
- `git diff --check` passed.

Remaining progress/UX gaps:

- Runtime progress is phase-level, not a rich timeline.
- Telegram uses existing progress delivery; no new native transcript UI.
- No Claude Code-style condensed terminal transcript grouping yet.

## Post-PR261 Skill Curator CLI Status / Install Milestone Update

Date: 2026-05-18 KST

Branch:

- `codex/runtime-progress-status`

Artifact:

- `.omc/research/skill-curator-cli-status-install-2026-05-18.md`

What changed:

- Added `elnath skill curator status`.
- Added `elnath skill curator status --json`.
- Added `elnath skill curator install`.
- Added `elnath skill curator install --interval DURATION`.
- Added `elnath skill curator install --run-on-start`.
- Added `elnath skill curator install --json`.
- Curator status reports schedule, draft count, proposal count, usage-tracked
  skill count, total usage count, and static scheduler runtime semantics.
- Curator install writes a recurring static scheduled task with type
  `skill-promote`.

Reference impact:

- Makes the existing skill consolidator / `skill-promote` substrate visible as
  a product surface.
- Moves Elnath closer to Hermes-style self-improving skill lifecycle while
  preserving explicit operator installation and schedule restart semantics.
- Does not claim automatic skill rewrite quality or full Hermes curator parity.

Verification:

- `go test ./cmd/elnath -run TestCmdSkillCuratorStatusAndInstall -count=1`
  passed.
- `go test ./cmd/elnath -run 'TestCmdSkill(Curator|Proposals)' -count=1`
  passed.
- `go test ./cmd/elnath ./internal/skill ./internal/scheduler -count=1`
  passed.
- `go vet ./...` passed.
- `git diff --check` passed.

Remaining skill feedback gaps:

- Installed curator schedules take effect after daemon restart; no hot reload.
- Curator still uses existing draft promotion/cleanup thresholds only.
- Proposal application remains review/approval driven.

## Post-PR261 Terminal User Question Interactive Answer Milestone Update

Date: 2026-05-18 KST

Branch:

- `codex/runtime-progress-status`

Artifact:

- `.omc/research/terminal-user-question-interactive-answer-2026-05-18.md`

What changed:

- Added `elnath task answer --interactive`.
- Added interactive pending-question lookup by optional `--session` and
  `--request`.
- Added terminal question display with numbered options.
- Interactive answers reuse the existing `user_question_answer` validation and
  queue-backed resume path.

Reference impact:

- Moves Elnath closer to Claude Code-style interactive choice UX for
  AskUserQuestion flows.
- Moves Elnath closer to Hermes-style clarify fallback while staying
  CLI-native.
- Does not claim full TUI modal parity or multi-select support.

Verification:

- `go test ./cmd/elnath -run TestCmdTaskAnswerWithQueueInteractiveChoice -count=1`
  passed.
- `go test ./cmd/elnath -run 'TestCmdTaskAnswerWithQueue(InteractiveChoice|AcceptsChoiceFlag|EnqueuesBoundAnswer|RejectsStaleRequest|RejectsTimedOutRequest)' -count=1`
  passed.
- `go test ./cmd/elnath -count=1` passed.
- `go vet ./...` passed.
- `git diff --check` passed.

Remaining user-input UX gaps:

- Interactive prompt is simple stdin/stdout, not a full TUI modal.
- Multi-select questions remain outside scope.
- No fuzzy search/filtering for many pending questions.

## Runtime / Operator UX Batch Readiness Update

Date: 2026-05-18 KST

Branch:

- `codex/runtime-progress-status`

Artifact:

- `.omc/research/runtime-operator-ux-batch-readiness-2026-05-18.md`

Batch commits:

- `9aa818d` `feat(runtime): surface runtime progress phases`
- `6d6ba9d` `feat(skill): expose curator schedule status`
- `d3e40b8` `feat(task): add interactive question answers`

Branch-level verification:

- `go test ./cmd/elnath ./internal/event ./internal/daemon ./internal/skill ./internal/scheduler -count=1`
  passed.
- `go vet ./...` passed.
- `git diff --check` passed.

Batch status:

- PR-sized and locally verified.
- No benchmark lane was run.
- No baseline/comparison was run.
- Product/runtime completion moved forward through runtime visibility,
  skill-curator operator surfacing, and terminal user-input UX.

## Code Intelligence Status CLI Milestone Update

Date: 2026-05-18 KST

Branch:

- `codex/code-intelligence-status`

Artifact:

- `.omc/research/code-intelligence-status-cli-2026-05-18.md`

What changed:

- Added `elnath explain code-intelligence`.
- Added `--json`, `--path`, and `--max-results` flags.
- The command exposes the code-intelligence product boundary, replacement path,
  diagnostic adapters, and live Go diagnostics from the existing `code_symbols`
  diagnostic implementation.
- When diagnostics are present, the command surfaces top repair hints with
  suggested model-callable tools and a bounded stop condition.
- The command includes a read-only receipt proving diagnostics were checked.

Reference impact:

- Moves Elnath closer to Hermes-style inspectable LSP/code-intelligence status
  while preserving Elnath's current product boundary.
- Moves Elnath closer to Claude Code's diagnostic-channel discipline by making
  diagnostics visible as a separate status/receipt surface.
- Does not claim full multi-language LSP parity, IDE-grade diagnostic parity, or
  benchmark impact.

Verification:

- `go test ./cmd/elnath -run 'TestCmdExplainCodeIntelligenceJSONRunsGoDiagnostics|TestExplainCodeIntelligenceText' -count=1`
  passed.
- `go test ./cmd/elnath -run 'Test(CmdExplainCodeIntelligenceJSONRunsGoDiagnostics|ExplainCodeIntelligenceText|ExplainControlSurfaces|ControlSurfaceManifestMatchesToolSearchRouting)' -count=1`
  passed.
- `go run ./cmd/elnath explain code-intelligence --json --path cmd/elnath --max-results 5`
  passed and returned `go_diagnostics.status=success`, `count=0`, and a
  read-only `explain_code_intelligence` receipt.

Remaining code-intelligence gaps:

- No full multi-language LSP lifecycle.
- Non-Go diagnostics remain syntax adapter policies, not always-on semantic
  language-server diagnostics.

## Runtime Diagnostic Repair Hints Milestone Update

Date: 2026-05-18 KST

Branch:

- `codex/diagnostic-repair-hints`

Artifact:

- `.omc/research/diagnostic-repair-hints-runtime-2026-05-18.md`

What changed:

- Added `diagnostic_repair_hints` to completion summaries.
- Extracts top introduced diagnostic details from:
  - `code_symbols diagnostics_delta`;
  - filesystem mutation verifier footer `new_diag_N=...`;
  - structured file mutation receipts.
- Retry guidance for `new_diagnostics_found` now includes concrete
  `file:line:column source error` hints when available.
- Learning outcome records and agentic completion gate summaries now persist
  these hints.

Reference impact:

- Moves Elnath closer to Claude Code's diagnostic-tracking loop: capture new
  diagnostics relative to edited files, then feed concise detail back into the
  correction path.
- Moves Elnath closer to Hermes' compact LSP reporter pattern by keeping
  diagnostic summaries bounded and line-oriented.
- Keeps Elnath-native receipt and retry boundaries: hints are structured,
  capped, and stop on `diagnostic_delta_clean_or_no_new_diagnostics`.

Verification:

- `go test ./cmd/elnath -run 'TestCompletionContractSummaryDetects(NewDiagnosticDelta|MutationVerifierNewDiagnostics|StructuredMutationNewDiagnostics)|TestCompletionRetryPromptGuidesNewDiagnosticDelta|TestRecordOutcomePersistsCompletionObservability|TestCompletionGateContextProviderConsumesRuntimeSummary|TestCompletionGateReceiptSummaryIncludesRuntimeContext' -count=1`
  passed.
- `go test ./internal/agentic/completion -run TestCompletionGate_ReceiptSummaryIncludesOptionalCompletionContext -count=1`
  passed.
- `go test ./cmd/elnath -count=1` passed.
- `go test ./internal/learning ./internal/agentic/completion -count=1`
  passed.
- `go vet ./...` passed.
- `git diff --check` passed.

Remaining code-intelligence gaps:

- No full multi-language LSP lifecycle.
- Non-Go diagnostics remain syntax adapter policies, not always-on semantic
  language-server diagnostics.
- Retry remains bounded; no broad silent self-healing claim.
