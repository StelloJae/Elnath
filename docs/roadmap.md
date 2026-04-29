# Elnath Roadmap

Updated: 2026-04-30

This is the tracked roadmap for Elnath. It consolidates the existing 6-month roadmap direction with the new **Agentic Runtime Control Plane** workstream.

The central implementation principle is:

> Do not replace the existing agent/tool/workflow execution engine. Add `internal/agentic/*` as a standing-goal-driven control-plane above it.

## 1. Executive Summary

Elnath already has a strong execution substrate: agent loop, tool registry, daemon queue, workflow router, fixed workflows, planner/subagent execution, verification loop, research loop, static scheduling, approval store, audit trail, outcomes, and wiki memory.

As of 2026-04-30, the durable agentic foundation is implemented through PR8: `internal/agentic/{schema,store,types,test}.go` exists, runtime startup initializes the agentic schema, daemon queue tasks are linked to durable `agentic_tasks` envelopes, scheduler/ambient/manual submit surfaces can record observe-only `goal_signals`, explicit triage can convert or link signals into `agentic_tasks` without execution, a standalone policy evaluator can persist durable `policy_decisions`, provenance-aware approval requests can link agentic tasks, policy decisions, actor/action/risk/reason metadata, explicit agentic tool calls can pass through a context-gated ToolGateway that records policy decisions and tool action receipts, and Ralph verifier runs can be persisted with criteria/evidence refs when explicit agentic verification context is configured. The roadmap now starts from that foundation instead of treating it as future work.

Hermes Agent moved from the v0.8/v0.9 baseline to v0.10/v0.11 and an active post-v0.11 `main` branch. The Elnath takeaway is not to copy Hermes wholesale. The useful deltas are: ToolGateway-style execution routing, hardline-vs-approval policy separation, plugin/hook lifecycle points, webhook/cron signal hardening, bounded delegation, receipt-backed tool results, and verification-gated memory.

The next roadmap step is not "more workflows." The next step is to make Elnath a true agentic runtime:

```text
standing goal
→ signal
→ task graph
→ autonomy policy
→ actor orchestration
→ tool gateway
→ approval gate
→ execution
→ verification
→ receipt ledger
→ memory update
→ follow-up scheduler
```

## 2. Current Agentic Readiness: 80/100

Elnath is currently a strong workflow runner and tool-using agent platform with a durable agentic control-plane schema, observe-only daemon task envelope linkage, an observe-only signal ledger bridge, explicit signal-to-agentic-task triage, standalone autonomy policy decision records, provenance-aware approval request storage/bridge foundations, a context-gated ToolGateway for explicit agentic tool calls, and durable verifier-run persistence for explicit agentic verification context. It is not yet a complete standing-goal-driven autonomous system because triaged tasks are not automatically enqueued or executed, the gateway is not globally enabled for legacy tool execution, verifier results do not gate task completion, and memory/follow-ups are not yet forced through the agentic runtime.

Implemented foundations:

| Foundation | File | Current role |
|---|---|---|
| Agent loop | `internal/agent/agent.go` | Message → LLM → tools → repeat execution loop. |
| Tool registry | `internal/tools/registry.go` | Registers and dispatches executable tools. |
| Daemon queue | `internal/daemon/queue.go` | Durable FIFO execution queue. |
| Workflow router | `internal/orchestrator/router.go` | Routes classified intent to workflow. |
| Fixed pipeline | `internal/orchestrator/autopilot.go` | Hardcoded plan → code → test → verify pipeline. |
| Planner/subagents | `internal/orchestrator/team.go` | LLM planner creates prompt-scoped subagents. |
| Verify/retry loop | `internal/orchestrator/ralph.go` | Execute → verify → retry workflow. |
| Research loop | `internal/research/loop.go` | Hypothesis → experiment → evaluate loop. |
| Static scheduler | `internal/scheduler/scheduler.go` | Static scheduled task enqueue. |
| Ambient scheduler | `internal/ambient/scheduler.go` | Wiki boot-task scheduling. |
| Approval store | `internal/daemon/approval_store.go` | Tool approval request storage with optional task/policy/action/risk/reason provenance. |
| Audit trail | `internal/audit/trail.go` | JSONL security/permission audit events. |
| Permission path | `internal/agent/permission.go` | Tool permission modes. |
| Outcome handling | `internal/learning/outcome.go` | Workflow outcome recording. |
| Wiki memory path | `internal/wiki/index.go` | Wiki page index and FTS memory substrate. |
| Agentic schema | `internal/agentic/schema.go` | Creates durable goals/signals/tasks/actors/policy/receipts/verification/memory/follow-up tables. |
| Agentic store | `internal/agentic/store.go` | Typed create/read APIs for agentic control-plane records. |
| Agentic startup init | `cmd/elnath/runtime.go` | Initializes agentic schema alongside conversation schema. |
| Agentic daemon envelope | `internal/agentic/runtime/envelope.go` | Links existing daemon queue tasks to durable `agentic_tasks` records and mirrors coarse lifecycle state. |
| Agentic signal bridge | `internal/agentic/signals/bridge.go` | Records scheduler, ambient, and manual-submit observations as observe-only `goal_signals` with watcher cursors. |
| Agentic signal triage | `internal/agentic/triage/triage.go` | Explicitly converts or links `goal_signals` into durable `agentic_tasks` without daemon queue enqueue. |
| Agentic policy evaluator | `internal/agentic/policy/policy.go` | Standalone evaluator that records durable policy decisions without runtime enforcement. |
| Agentic approval bridge | `internal/agentic/approvals/bridge.go` | Creates provenance-aware approval requests from `approval_required` policy decisions without runtime enforcement. |
| Agentic ToolGateway | `internal/agentic/tools/gateway.go` | Context-gated gateway for explicit agentic tool calls; records policy decisions and tool action receipts, and fails closed for denied or approval-required actions. |
| Agentic tool context | `internal/tools/agentic_context.go` | Carries task, actor, and tool-call identity plus result finalization hooks for explicit agentic tool calls. |
| Agentic verifier runs | `internal/agentic/verification/recorder.go` | Persists Ralph verifier criteria, evidence refs, verdict, and redacted reason when explicit agentic verification context is configured. |
| Hook surface | `internal/agent/hooks.go` | Pre/post tool and LLM lifecycle hooks; useful base for receipt-aware gateway hooks. |

## 3. Workflow vs Agentic Gap

Workflow-like today:

- `autopilot` is a fixed pipeline, not a dynamic control-plane.
- `router` selects a workflow from intent and heuristics, not from standing goals.
- `scheduler` and `ambient` start static prompts, not signal-driven tasks.
- `team` subagents are prompt-scoped goroutines, not durable actors.

Agentic foundation already present:

- `agent.Run` lets the model choose tool calls.
- `team` performs LLM task decomposition.
- `ralph` has a verification/retry pattern.
- `research` has a hypothesis/experiment/evaluate loop.

Gap to close:

- Add durable goals, signals, task graph, policy decisions, action receipts, verification records, memory-update provenance, and follow-up scheduling.
- Make mutating autonomous action impossible without policy, approval if needed, receipt, and verification lineage.

## 4. Implementation Status

Schema/store foundation implemented:

```text
standing_goals
signal_watchers
goal_signals
agentic_tasks
task_edges
agent_actors
policy_decisions
tool_action_receipts
verification_runs
memory_updates
followups
```

Still missing as runtime behavior:

- Global/default runtime wiring that sends all required agentic tool execution through the gateway.
- End-to-end runtime wiring that injects task/action/risk/policy provenance into every enforced approval path.
- Receipt enforcement before task completion.
- Verification gate before `Queue.MarkDone` for required agentic tasks.
- Verified-only memory/outcome/wiki update policy.
- Follow-up scheduler with dedupe/cooldown/fanout limits.
- Durable actor runtime for planner/executor/verifier/critic/memory/scheduler roles.

Non-claims after PR1, PR2, PR3, PR4, PR5, PR6, PR7, and PR8:

- No autonomous runtime behavior is enabled.
- Signals are not enqueued into daemon work.
- ToolGateway is active only for explicit agentic tool calls, not for all legacy daemon/tool execution.
- Policy decisions gate only the context-gated ToolGateway path; normal tool execution remains unchanged.
- Approval provenance storage and bridge foundations exist; approval-required gateway calls fail closed and create/reuse approvals, but there is no synchronous approval wait or retry-after-approval UX yet.
- Tool action receipts are recorded through the context-gated gateway, but receipt-based task completion gates are not active.
- Verifier runs can be persisted when explicit agentic verification context is configured, but no verifier gate is active.
- No memory gate is active.
- No follow-up scheduler is active.

## 5. Target Architecture

Target module structure:

```text
internal/agentic/
  goals/
  signals/
  triage/
  tasks/
  policy/
  actors/
  tools/
  approvals/
  receipts/
  verification/
  memory/
  followup/
  runtime/
```

Responsibilities:

- `goals`: standing goal CRUD, autonomy level, risk budget, success criteria.
- `signals`: watcher definitions, signal ledger, dedupe/fingerprint.
- `triage`: signal → task proposal.
- `tasks`: task graph, parent/child edges, dependency status.
- `policy`: auto/approval/observe/deny decision engine.
- `actors`: durable planner/executor/verifier/critic/memory/scheduler actor state.
- `tools`: ToolGateway wrapping the existing `tools.Executor`.
- `approvals`: bridge existing approval store to task/action/policy provenance.
- `receipts`: tool action receipts and task receipt summaries.
- `verification`: verification criteria, evidence refs, verdicts.
- `memory`: verified-only memory update policy.
- `followup`: one-shot and recurring continuation scheduling.
- `runtime`: daemon task envelope around the existing runtime.

## 6. Required Schema Changes

Implemented:

- `internal/agentic/schema.go` creates the initial agentic tables.
- `internal/agentic/types.go` defines typed records and status constants.
- `internal/agentic/store.go` provides initial create/read APIs and receipt completion.
- `internal/agentic/triage/triage.go` provides explicit signal-to-task triage without execution.
- `internal/agentic/tools/gateway.go` provides a context-gated ToolGateway for explicit agentic tool calls.
- `internal/agentic/verification/recorder.go` provides durable verifier-run recording with criteria/evidence refs and reason redaction/truncation.
- `cmd/elnath/runtime.go` initializes the agentic schema during runtime startup.

Current tables:

```text
standing_goals(id, title, description, status, priority, autonomy_level, risk_budget, created_at, updated_at)
signal_watchers(id, goal_id, source, config_json, enabled, interval_s, last_cursor, created_at, updated_at)
goal_signals(id, goal_id, watcher_id, source, type, payload_json, fingerprint, severity, status, observed_at, dedupe_key)
agentic_tasks(id, goal_id, signal_id, parent_id, queue_task_id, title, prompt, status, priority, risk_level, autonomy_decision, approval_request_id, verification_status, created_at, updated_at, due_at)
task_edges(parent_id, child_id, edge_type, created_at)
agent_actors(id, task_id, role, state_json, inbox_json, outbox_json, tool_allowlist_json, budget_json, status, created_at, updated_at)
policy_decisions(id, task_id, actor_id, action_kind, tool_name, risk_level, decision, reason, policy_version, created_at)
tool_action_receipts(id, task_id, actor_id, policy_decision_id, approval_request_id, tool_name, input_hash, output_hash, output_summary, status, reversible, started_at, completed_at, tool_call_id, raw_output_hash, visible_output_hash, failure_reason, hook_provenance_json)
verification_runs(id, task_id, verifier_actor_id, criteria_json, evidence_refs_json, verdict, reason, created_at)
memory_updates(id, task_id, receipt_id, verification_run_id, target, operation, payload_hash, status, created_at)
followups(id, task_id, goal_id, trigger_at, reason, status, created_task_id, created_at)
```

Existing schema extensions:

- `internal/daemon/queue.go`: add nullable `agentic_task_id`, `goal_id`, or link first via `agentic_tasks.queue_task_id`.
- `internal/daemon/approval_store.go`: add `task_id`, `policy_decision_id`, `risk_level`, `reason`, `expires_at`, `decided_by`.
- `internal/learning/outcome.go`: add optional `task_id`, `verification_run_id`, `receipt_id`.

Hermes-inspired schema hardening to add before full autonomy:

- Keep policy decision values centered on the implemented `auto_allowed`, `approval_required`, `hardline_denied`, `observe_only`, and `escalated` constants.
- Add receipt metadata for `pre_hook_ids`, `post_hook_ids`, and `transform_hook_ids` if hook identity must be queryable beyond the current `hook_provenance_json`.
- Add watcher fields for `cursor_kind`, `rate_limit`, `cooldown_s`, `max_fanout`, and `last_error`.
- Add task fields or side table for `retry_count`, `max_retries`, `failure_reason`, and `escalation_reason`.
- Add actor budget fields for `max_spawn_depth`, `max_tool_calls`, `max_runtime_s`, and `allowed_toolsets`.

## 7. Required Runtime Changes

Current daemon path:

```text
task_queue payload
→ ParseTaskPayload
→ load/create session
→ runTask
→ workflow result
→ Queue.MarkDone
```

Target path:

```text
task_queue payload
→ AgenticRuntime.Run(queue_task_id)
→ load/create agentic_task
→ policy preflight
→ actor orchestration
→ receipt-enforced tool execution
→ verification run
→ memory/follow-up processing
→ Queue.MarkDone only after required gates pass
```

Runtime files:

- Add `internal/agentic/runtime/runtime.go`.
- Add `internal/agentic/runtime/envelope.go`.
- Modify `cmd/elnath/runtime.go`, especially `newDaemonTaskRunner` and `runTask`.
- Keep existing workflows compatible.

## 8. Required Agent / Tool / Approval / Receipt / Memory / Scheduler Changes

Agent:

- Keep current workflows initially.
- Record actor state around existing planner/executor/verifier roles.
- Later promote `team` subagents into durable `agent_actors`.

Tool:

- `internal/agentic/tools/gateway.go` now wraps explicit agentic tool calls.
- The gateway records policy decisions, creates `tool_action_receipts`, and fails closed for denied or approval-required decisions.
- Future work must decide how broader agentic runtime paths opt into the gateway without changing legacy tool execution unexpectedly.

Approval:

- Extend `approval_requests` with task/action/risk/policy context.
- Telegram/CLI approval surfaces must show why the action needs approval.

Receipt:

- Mutating action cannot be considered complete without a receipt.
- Task completion should depend on required receipts.

Verification:

- Persist verifier criteria, evidence refs, verdict, and reason.
- Later, required verifier failure should block agentic task completion. PR8 should persist verifier runs first without changing task completion behavior.

Memory:

- Trusted memory update requires verifier pass or explicit user memory command.
- Outcome records should include task/verification/receipt provenance.

Scheduler:

- Keep static scheduler and ambient scheduler.
- Add follow-up scheduler that reads `followups` and creates future signals/tasks under budget and cooldown.

Hermes catch-up imports:

- From Hermes v0.10 Tool Gateway: prefer a single gateway path for backend routing, policy decision, execution, and receipt writing. Elnath should not copy vendor-managed credentials; it should copy the gateway boundary.
- From Hermes v0.11 plugins/hooks: keep hooks inside the gateway/runtime boundary. Hooks may observe or block; hooks that transform results must be receipt-visible.
- From Hermes approval hardening: split `hardline_denied` from `approval_required`. Hardline actions are never allowed by bypass, daemon, scheduler, or approval timeout.
- From Hermes cron/webhook: require idempotency, rate limit, HMAC or trusted local source, per-job workdir, and `wakeAgent=false`-style no-op gates for watchers.
- From Hermes delegation: preserve bounded actor depth and inherited/intersected toolsets. Child actors must not gain tools that parents did not have.
- From Hermes memory/session search: improve searchability later, but do not let unverified results become trusted memory.

## 9. MVP Scope

MVP Agentic Runtime Control Plane:

- At least one standing goal can be registered and inspected.
- Scheduler/ambient/manual signals are stored in `goal_signals`.
- Signals can create `agentic_tasks`.
- Read-only tools are auto-allowed.
- Mutating tools require approval by default: write/edit/bash/git/wiki_write and comparable MCP mutators.
- Every tool call creates a receipt.
- Task completion requires one verifier run.
- Memory/outcome writes require verifier pass or explicit user memory action.
- Follow-up supports one-shot scheduled tasks.

## 10. Ultimate Scope

Ultimate Agentic Runtime Control Plane:

- Goal-specific watcher/cursor/risk budget/autonomy level.
- Signal dedupe and severity scoring.
- Full task graph with dependencies, retries, child tasks, and escalation.
- Durable role actors: planner, executor, verifier, critic, memory, scheduler.
- ToolGateway required for every tool execution.
- Approval escalation with task/action/risk provenance.
- Independent verifier with evidence references.
- Verified-only trusted memory.
- Rollback/recovery strategy.
- Per-goal budget, cooldown, max fanout, and max depth.
- Operator CLI/Telegram visibility for goals, signals, tasks, actors, approvals, receipts, verification, and follow-ups.

## 11. PR-by-PR Roadmap

### Phase 1: Agentic control-plane foundation

#### PR1: `feat(agentic): add schema and store`

Status: shipped foundation; continue only with small hardening follow-ups.

- Purpose: Add durable control-plane tables and typed store APIs.
- Current related files: `internal/daemon/queue.go`, `internal/core/db.go`, `cmd/elnath/runtime.go`.
- Files already modified: `cmd/elnath/runtime.go`.
- Files already added: `internal/agentic/schema.go`, `internal/agentic/store.go`, `internal/agentic/types.go`, `internal/agentic/store_test.go`.
- Core implementation: Idempotent schema init for goals, signals, tasks, actors, policy, receipts, verification, memory, followups.
- Dependency: none.
- Completion criteria: Startup initializes schema without affecting existing queue/conversation/wiki tables.
- Test criteria: `GOFLAGS='-tags=sqlite_fts5' go test ./internal/agentic -count=1`; schema idempotency tests; existing daemon/conversation tests still pass.
- Agentic capability: Elnath gets durable control-plane state.
- Remaining hardening: add richer policy decision constants and watcher/task budget fields before enabling autonomous continuation.

#### PR2: `feat(agentic): wrap daemon task execution`

Status: shipped observe-only daemon task envelope; do not expand into policy/tool/verification gates without later PRs.

- Purpose: Link existing daemon work to `agentic_tasks`.
- Current related files: `cmd/elnath/runtime.go`, `internal/daemon/task_payload.go`, `internal/daemon/queue.go`.
- Files modified: `cmd/elnath/cmd_daemon.go`, `internal/daemon/daemon.go`, `internal/daemon/envelope.go`.
- Files added: `internal/agentic/runtime/envelope.go`, `internal/agentic/runtime/envelope_test.go`.
- Core implementation: daemon workers create/load an agentic task envelope before calling existing task execution, mirror coarse lifecycle status, and reconcile stale running envelopes on startup.
- Dependency: PR1.
- Completion criteria: Existing task submissions still complete; each daemon task has an agentic task record.
- Test criteria: daemon envelope tests prove queue task ↔ agentic task link, existing success/failure behavior preservation, observable degraded envelope failures, stale running reconcile, and no autonomous side effects.
- Agentic capability: Existing workflows become traceable inside agentic runtime.
- Hermes update: this remains observe-only. PR2 does not change tool permission behavior; it records task envelope and lifecycle only.

### Phase 2: Signal and task graph

#### PR3: `feat(signals): add signal ledger and watcher bridge`

Status: shipped observe-only signal ledger and watcher bridge; signal-to-task conversion is handled by shipped PR4.

- Purpose: Persist observations before they become work.
- Current related files: `internal/scheduler/scheduler.go`, `internal/ambient/scheduler.go`, `internal/event/*`.
- Files modified: `cmd/elnath/cmd_daemon.go`, `internal/scheduler/scheduler.go`, `internal/ambient/scheduler.go`, `internal/daemon/daemon.go`, `internal/agentic/schema.go`, `internal/agentic/store.go`.
- Files added: `internal/agentic/signals/bridge.go`, `internal/agentic/signals/bridge_test.go`.
- Core implementation: Convert scheduler/ambient/manual submit observations into observe-only `goal_signals`, link them to source `signal_watchers`, update watcher cursors, dedupe/fingerprint occurrences, and minimize persisted payloads.
- Dependency: PR1.
- Completion criteria: Scheduler, ambient, and manual submit observations are persisted without changing task execution behavior.
- Test criteria: Fingerprint, watcher bridge, scheduler/ambient/manual signal insert tests, repeated occurrence tests, watcher singleton tests, bridge failure observability, and no autonomous side effects.
- Agentic capability: Elnath can remember what it noticed.

#### PR4: `feat(triage): convert signals to tasks`

Status: shipped explicit signal-to-agentic-task triage; do not enqueue daemon work or enable policy/tool gates without later PRs.

- Purpose: Turn signal ledger entries into task graph nodes.
- Current related files: `internal/daemon/queue.go`, `internal/scheduler/scheduler.go`.
- Files modified: `internal/agentic/schema.go`, `internal/agentic/store.go`, `internal/agentic/store_test.go`, `internal/agentic/types.go`.
- Files added: `internal/agentic/triage/triage.go`, `internal/agentic/triage/triage_test.go`.
- Core implementation: Convert new `goal_signals` into idempotent `agentic_tasks`; non-queue signals create proposed tasks; queue-backed signals link to existing queue/envelope state or create a pending envelope without queue enqueue; malformed payloads become failed signals so the batch can continue.
- Dependency: PR3.
- Completion criteria: A stored signal produces at most one task; reruns are idempotent; queue-backed repeated observations are absorbed into existing task state.
- Test criteria: Signal-to-task tests for proposed tasks, queue-backed links, duplicate/rerun idempotency, transactional rollback, malformed-payload isolation, no daemon queue enqueue, no autonomous side effects, and no task edges without an explicit relationship.
- Agentic capability: Elnath can move from observation ledger to durable task graph records without executing them.

### Phase 3: Autonomy and approval

#### PR5: `feat(policy): add autonomy decisions`

Status: shipped standalone policy decision foundation; do not enforce policy at runtime without later PRs.

- Purpose: Decide auto/approval/observe/hardline-deny/escalate before execution.
- Current related files: `internal/agent/permission.go`, `internal/config/config.go`.
- Files modified: `internal/agentic/types.go`.
- Files added: `internal/agentic/policy/policy.go`, `internal/agentic/policy/policy_test.go`.
- Core implementation: Persist `policy_decisions`; normalize command/input text before classification; separate `hardline_denied` from `approval_required`; keep evaluator standalone.
- Dependency: PR2, PR4.
- Completion criteria: Read-only actions can be auto-approved; mutating actions require approval unless explicit policy permits; hardline actions are blocked even in bypass/autonomous contexts.
- Test criteria: Policy table tests; deterministic evaluator tests; no-side-effect tests; hardline bash/git/filesystem examples are denied.
- Agentic capability: Elnath gains explicit autonomy boundaries.
- Hermes update: mirror the useful split from Hermes approval hardening, but keep the rule set Elnath-local and testable.

#### PR6: `feat(approvals): link approvals to agentic tasks`

Status: shipped approval provenance foundation; do not enforce approvals at runtime without later PRs.

- Purpose: Make HITL approval provenance complete.
- Current related files: `internal/daemon/approval_store.go`, `internal/telegram/shell.go`.
- Files modified: `internal/daemon/approval_store.go`, `internal/daemon/approval_store_test.go`, `internal/agentic/store.go`, `internal/telegram/shell.go`, `internal/telegram/shell_test.go`.
- Files added: `internal/agentic/approvals/bridge.go`, `internal/agentic/approvals/bridge_test.go`.
- Core implementation: Approval requests can reference task, policy decision, actor, action kind, risk level, reason, policy version, expiry, and decider provenance; legacy approval rows remain compatible; duplicate pending approvals are protected per `policy_decision_id`.
- Dependency: PR5.
- Completion criteria: `/approvals` can show why approval is needed and what task/action it affects when provenance is present; provenance-aware creation can link `approval_required` policy decisions to approval requests and `agentic_tasks.approval_request_id`.
- Test criteria: Approval migration/create/list/decide tests include task/risk/policy fields; duplicate pending migration tests; approval bridge tests; Telegram provenance and legacy rendering tests; no autonomous side-effect tests.
- Agentic capability: Human escalation becomes part of the ledger.

### Phase 4: Tool execution and receipts

#### PR7: `feat(tools): enforce tool receipts through gateway`

Status: shipped context-gated ToolGateway receipt foundation; do not enable globally or add verifier/memory/follow-up gates without later PRs.

- Purpose: Ensure every tool action is policy-gated and receipt-backed.
- Current related files: `internal/tools/registry.go`, `internal/tools/tool.go`, `internal/agent/executor.go`.
- Files modified: `internal/agent/executor.go`, `internal/agentic/schema.go`, `internal/agentic/store.go`, `internal/agentic/store_test.go`, `internal/agentic/types.go`, `internal/tools/tool.go`.
- Files added: `internal/agent/executor_agentic_test.go`, `internal/agentic/tools/context.go`, `internal/agentic/tools/gateway.go`, `internal/agentic/tools/gateway_test.go`, `internal/tools/agentic_context.go`.
- Core implementation: Context-gated ToolGateway wraps explicit agentic tool calls, records policy decisions and tool action receipts, creates receipts before allowed execution, fails closed for `approval_required` and `hardline_denied`, and records raw/visible output hashes plus hook transformation provenance.
- Dependency: PR5, PR6.
- Completion criteria: Explicit agentic tool calls require task context, preserve plain `tools.Registry.Execute` behavior outside the gateway, and make approval-required or hardline-denied actions fail closed without executing the wrapped tool.
- Test criteria: Gateway tests cover read-only auto-allow, mutating approval-required blocking with approval creation/reuse, hardline denial, receipt-before-execution, receipt success/error completion, missing task ID fail-closed, plain registry compatibility, no verifier/memory/follow-up side effects, parallel receipt distinctness, and hook-transformed output preserving raw/visible hashes.
- Agentic capability: Explicit agentic tool use becomes auditable and receipt-backed without globally changing legacy tool execution.
- Hermes update: this is the main v0.10 import. The gateway boundary matters more than any specific managed-tool backend.

### Phase 5: Verification and memory safety

#### PR8: `feat(verification): persist verifier runs`

Status: shipped verifier-run persistence foundation; do not add verifier, memory, or follow-up gates without later PRs.

- Purpose: Make verifier output durable and evidence-addressable.
- Current related files: `internal/orchestrator/ralph.go`, `cmd/elnath/runtime.go`.
- Files modified: `cmd/elnath/runtime.go`, `internal/orchestrator/ralph.go`, `internal/orchestrator/types.go`, `internal/daemon/daemon.go`, `internal/daemon/envelope.go`, `internal/agentic/store.go`, `internal/agentic/types.go`.
- Files added: `internal/agentic/verification/recorder.go`, `internal/agentic/verification/recorder_test.go`.
- Core implementation: Persist criteria, evidence refs, verdict, redacted/truncated reason, and task linkage for Ralph verifier runs when explicit agentic verification context is configured.
- Dependency: PR7.
- Completion criteria: Verifier runs can be written and inspected durably without changing daemon `Queue.MarkDone` behavior.
- Test criteria: Verification pass/fail/inconclusive persistence tests, Ralph verdict persistence tests, daemon-backed Ralph persistence plus queue-done tests, recorder failure non-gating tests, no task-completion gate tests, and no memory/follow-up side-effect tests.
- Agentic capability: Verification becomes durable enough to support later completion and memory gates.
- Non-goal: PR8 must not make verifier pass required for task completion, must not gate memory updates, and must not gate daemon `Queue.MarkDone`.

#### PR9: `feat(memory): gate memory updates on verification`

Status: next dependency-ready planning/fact-pack target.

- Purpose: Prevent unverified outcomes from becoming trusted future context.
- Current related files: `internal/learning/outcome.go`, `internal/learning/outcome_store.go`, `internal/wiki/index.go`, `cmd/elnath/runtime.go`.
- Files to modify: `internal/learning/outcome.go`, `cmd/elnath/runtime.go`, wiki/learning integration points.
- Files to add: `internal/agentic/memory/policy.go`, `internal/agentic/memory/store.go`.
- Core implementation: Write `memory_updates`; allow trusted memory only after verifier pass or explicit user memory command.
- Dependency: PR8.
- Completion criteria: Failed/unverified agentic tasks do not update trusted memory.
- Test criteria: Memory gate tests for pass/fail/user-explicit cases.
- Agentic capability: Memory becomes safer and provenance-aware.

### Phase 6: Follow-up and autonomous continuation

#### PR10: `feat(followup): schedule follow-up tasks from task outcomes`

- Purpose: Let verified outcomes create bounded future work.
- Current related files: `internal/scheduler/scheduler.go`, `internal/ambient/scheduler.go`, `cmd/elnath/cmd_daemon.go`.
- Files to modify: daemon scheduler wiring.
- Files to add: `internal/agentic/followup/store.go`, `internal/agentic/followup/scheduler.go`.
- Core implementation: Create one-shot followups from task outcomes; convert due followups into signals/tasks.
- Dependency: PR8, PR9.
- Completion criteria: A verifier-approved follow-up creates one future task under cooldown/dedupe.
- Test criteria: Follow-up due-time, dedupe, cooldown, and task-creation tests.
- Agentic capability: Elnath can continue work without a fresh user prompt while staying bounded.
- Hermes update: include `wakeAgent=false`-style script/check gates so scheduled checks can record "no action needed" without waking a full agent run.

### Phase 7: Durable actor runtime

#### PR11: `feat(actors): promote team roles to durable actors`

- Purpose: Turn prompt-role subagents into actor records with state/inbox/outbox/budgets.
- Current related files: `internal/orchestrator/team.go`, `internal/orchestrator/types.go`, `internal/agent/agent.go`.
- Files to modify: `internal/orchestrator/team.go`, `cmd/elnath/runtime.go`.
- Files to add: `internal/agentic/actors/store.go`, `internal/agentic/actors/orchestrator.go`, `internal/agentic/actors/handoff.go`.
- Core implementation: Record planner/executor/verifier/critic/memory actor state and handoffs around existing workflow execution.
- Dependency: PR2, PR7, PR8.
- Completion criteria: Team decomposition creates durable actor records and handoff records.
- Test criteria: Actor lifecycle and handoff tests; existing team workflow tests stay green.
- Agentic capability: Agent roles become inspectable actors rather than transient prompts.
- Hermes update: enforce inherited/intersected toolsets and max actor depth. Child actors must not escalate permissions.

### Phase 8: Operator visibility and autonomous push loop

#### PR12: `feat(agentic): add operator status and next-task selection`

- Purpose: Let Elnath and Codex continue the roadmap from the highest-priority dependency-ready PR without guessing.
- Current related files: `docs/roadmap.md`, `cmd/elnath/commands.go`, `cmd/elnath/cmd_daemon.go`.
- Files to modify: `cmd/elnath/commands.go`, `cmd/elnath/cmd_daemon.go`.
- Files to add: `internal/agentic/runtime/next.go`, `internal/agentic/runtime/status.go`.
- Core implementation: Add read-only operator status for goals/signals/tasks/receipts/verification/followups and a deterministic next-work selector.
- Dependency: PR2, PR3, PR4.
- Completion criteria: Operator can ask what the current agentic state is and what the next dependency-ready task is.
- Test criteria: Status rendering tests; next-task selector tests for blocked, ready, and complete phases.
- Agentic capability: Elnath can push itself forward through an inspectable queue instead of relying on ad hoc chat memory.

## 12. Risks and Guardrails

| Risk | Current cause | Why dangerous | Guardrail | Related PR |
|---|---|---|---|---|
| Hallucinated action execution | LLM tool calls can flow into existing executor | A model can act on imagined state | ToolGateway + policy decision + receipt before action | PR5, PR7 |
| Permission overexposure | `internal/agent/permission.go` permits no-prompter default in some paths | Autonomous daemon paths may run mutating tools without HITL | Fail-closed autonomous policy for non-read-only tools | PR5 |
| Approval provenance gap | `approval_requests` stores tool/input but not task/risk/policy | Operators cannot judge why approval is needed | Link approvals to task/action/risk/reason | PR6 |
| Completion without receipts | `Queue.MarkDone` can follow workflow summary | "Done" may not mean action was recorded or verified | Completion gate requires expected receipts | PR7, PR8 |
| Verifier independence gap | Ralph verifier is transcript-oriented | Verification can become self-attestation | Persist evidence refs and independent verifier runs | PR8 |
| Memory pollution | Outcomes and wiki writes can outlive weak verification | Future prompts learn from wrong claims | Verified-only trusted memory policy | PR9 |
| Task explosion | Static/dynamic schedulers can generate repeated work | Autonomous loop can flood queue/user | Dedupe, cooldown, budget, max depth/fanout | PR3, PR4, PR10 |
| Autonomous overreach | Standing goals may be too broad | System may act beyond user intent | Goal autonomy level, risk budget, approval-required mutators | PR1, PR5, PR6 |

## 13. Acceptance Criteria

MVP acceptance:

- One standing goal can be registered and inspected.
- A scheduler/ambient/manual event creates a deduped `goal_signals` record.
- A signal can create an `agentic_tasks` record linked to a daemon queue task.
- Read-only tool calls can execute with receipts.
- Mutating tool calls require approval unless an explicit policy permits them.
- Required verifier pass is recorded before agentic task completion.
- Trusted memory/outcome writes record provenance to verification/receipt IDs.
- One-shot follow-up can create a future bounded task.

Ultimate acceptance:

- Operators can inspect goal → signal → task → actor → action receipt → verification → memory/follow-up lineage.
- Every autonomous mutating action is policy-gated, approval-aware, receipt-backed, and verifier-addressable.
- Actor handoffs are durable enough to resume after daemon restart.
- Follow-up generation is bounded by per-goal budget, cooldown, max fanout, and max depth.
- Existing workflows still run through compatibility wrappers; the roadmap does not require replacing the current agent/tool/workflow runtime.

## 14. Autonomous Execution Protocol

This is the operating contract for "keep pushing agentically" while Elnath is still under construction.

Standing goal:

```text
Build Elnath into a standing-goal-driven agentic runtime without replacing the existing agent/tool/workflow engine.
```

Next-task selection:

1. Read this roadmap first.
2. Select the first incomplete PR whose dependencies are complete.
3. Prefer the smallest PR that increases runtime traceability, policy safety, receipt enforcement, or verification.
4. Do not jump to higher-autonomy features before policy, approval, receipt, and verification gates exist.

Default current next PR:

```text
PR9: feat(memory): gate memory updates on verification
```

Autonomy rules for Codex/Elnath work:

- Documentation-only updates may proceed when they keep this roadmap accurate.
- Read-only inspection and focused tests may proceed without asking.
- Code edits should stay inside the selected PR surface.
- Mutating runtime behavior should start observe-only, then become enforceable in a later PR.
- External actions, destructive commands, credential changes, publishing, GitHub writes, or broad autonomous execution require explicit user approval.
- Memory/wiki updates are allowed only when explicitly requested or when a verified memory-update gate exists.

Per-PR completion gate:

- Roadmap status is updated if the PR changes the planned order or capability state.
- Focused tests for the touched package pass.
- Broader verification is run when runtime, daemon, tool, permission, or scheduler behavior changes.
- `git status --short` is reviewed so unrelated user changes are not mixed in.
- The final report states what changed, what was verified, what remains blocked, and the next dependency-ready PR.

Stop conditions:

- Policy/approval behavior is ambiguous.
- A required test fails for a reason that is not clearly scoped to the current PR.
- The next step would require external side effects or user secrets.
- A planned change would bypass receipt, verification, or approval lineage.
