# Changelog

## v0.6.0 (2026-04-16)

Skill Emergence + Safety + Quality. 6 major features across 5 commits (+6,749 LOC). First release with wiki-native skill CRUD, autonomous skill emergence (Layer 1+3), 3-state Ralph verification, declarative agent profiles, full-surface injection scanning, and greenfield project guidance.

### Skill Emergence MVP (Phase C-2)

- **Skill CRUD**: `elnath skill list/show/create/edit/delete/stats` CLI + Telegram `/skill-list`, `/skill-create`. Wiki pages as single source of truth (`wiki/skills/*.md`).
- **Layer 1 — LLM Hint**: `create_skill` tool lets the agent suggest skill creation during conversation. `SkillGuidanceNode` (priority 64) instructs the agent when to propose skills.
- **Layer 3 — Consolidator**: `DefaultConsolidator` promotes draft skills to active when prevalence threshold is met (default: 5 sessions, 2+ independent patterns). 90-day auto-cleanup for stale drafts. Runs as `skill-promote` scheduled task type (24h interval).
- **Tracker**: JSONL append-only usage and pattern recording (`skill-usage.jsonl`, `skill-patterns.jsonl`).
- **Foundation**: `Status`/`Source` fields on Skill struct, draft filtering in Registry.Load(), Creator with hot-reload via Registry.Add().

### Local Outcomes Ralph Refactor

- **3-state verification**: `VerdictPass` (done), `VerdictNeedsRevision` (retry with feedback), `VerdictFail` (immediate exit, no retry waste). Previously all non-PASS results triggered retry identically.
- **Rubric-based prompt**: Structured evaluation against CORRECTNESS, COMPLETENESS, VERIFICATION criteria.
- **Evidence window expanded**: 4→8 tool results, 1200→2000 chars per result, 4000→6000 assistant chars.
- **Learning integration**: `ralph_fail` finish reason recorded for VerdictFail cases.

### Agent Profile

- **Wiki-native profiles**: `wiki/profiles/*.md` with frontmatter (model, tools, max_iterations). Loaded at startup, referenced by name.
- **Seed profiles**: `code-reviewer` (read-only tools, 20 iterations), `researcher` (full tools, 50 iterations).
- **Seed skill**: `deep-interview` — clarifies ambiguous requests before executing.
- **CLI**: `elnath profile list/show`.

### Safety Completion (Phase D-2)

- **Tool output injection scan**: `SecretScanHook.PostToolUse()` now applies `ScanContent` after secret redaction. Covers bash, file tools, and MCP results.
- **Wiki RAG injection scan**: `BuildRAGContext` accepts `ContentScanner` parameter. Injection in wiki pages is blocked before reaching the agent.
- **Telegram output redaction**: `TelegramSink.WithRedactor()` strips secrets before sending messages.
- **Audit**: `EventInjectionBlocked` events logged to audit trail.

### LB1 Greenfield Path

- **GreenfieldNode** (priority 40): Renders project scaffolding guidance when `ExistingCode == false`. Language-specific sections for Go, TypeScript, Python. Mutually exclusive with BrownfieldNode.

### Other

- **GPT-5.4 model support**: OpenAI/Codex model list updated to GPT-5.4 era (`39a11a2`).
- **Error classifier**: 13-category error classification for tool failures and API errors (`fc04a35`).
- **Context-overflow compression**: `ModelInfo.ContextWindow` + `ShouldCompress` callback wired (`6d01d10`).
- **Dead code cleanup**: W2 dead code removed, HookRegistry type guard added (`0c11d35`).

## v0.5.1 (2026-04-15)

Hermes parity release: 6 of 8 audit items implemented (#3, #4, #8, #9, #11, #13, #15). Item #12 (error classifier) deferred pending dog-food failure taxonomy.

### Hermes Parity

- **#8 Layer 3 historical tool-result attenuation**: Progressive truncation of older tool results across turns (t-2 → 10K, t-3 → 2K, t-4+ → placeholder). Idempotent with marker-based skip. Completes the 3-layer tool-result size control alongside existing per-tool (50K) and per-turn (200K) limits.
- **#9 9-section structured compression**: Replaces free-form Stage 2 auto-compression with a `# Session Summary` template (9 sections: user goal, completed steps, current focus, files touched, outstanding TODOs, blockers, key decisions, open questions, next action). Iterative-update prompt merges new messages into existing summary instead of re-summarizing from scratch. Legacy unstructured prompt preserved as fallback for malformed LLM output.
- **#11 Tool argument type coercion**: Opt-in `ArgsTarget` interface on tools enables automatic coercion of LLM-provided args (string↔int, string↔bool, float64→int for whole numbers, string↔float64). Applied to `read_file`, `bash`, `glob`, `grep`. Tools without `ArgsTarget` are unaffected.
- **#13 Decorrelated jitter backoff**: Retry delay uses AWS-style `min(maxDelay, random(baseDelay, current*3))` instead of deterministic `delay *= 2`. Eliminates synchronized retry storms when multiple gate runs hit the same provider.
- **#15 Plugin lifecycle hooks (4 of 10 stages)**: `PreLLMCall`, `PostLLMCall`, `OnCompression`, `OnIterationStart` as split interfaces (`LLMHook`, `CompressionHook`, `IterationHook`). Existing `Hook` interface unchanged. Compression hook chained after dedup reset in runtime.

### Previously shipped in v0.5.0 cycle

- **#3 Dedup reset on compression** (PR #1, `d169399`)
- **#4 Post-write timestamp refresh** (already in production, `file.go:197/297 RefreshPath`)

### Deferred

- **#12 Error classifier**: Awaiting dog-food failure taxonomy before adoption.

## v0.5.0 (2026-04-16)

Knowledge Assistant OS milestone: Phase 3.2 Gate PASS (v7) and the F-6 feature quartet (portability, fault injection, onboarding UX, locale) ship together. Release-ready per `docs/f6-validation-plan.md` (15/16 scenarios pass, D2 `--help` intercept fixed in the same release cycle).

### F-6 Features

- **LB6 Auth/Credential Portability**: `elnath portability {export,import,list,verify}` produces AES-256-GCM chunked-streaming bundles (`.eln`, 16 MiB chunks). Supports `--scope config,db,wiki,lessons,sessions` for selective exports. Passphrase gate: <8 chars rejected, 8-11 prompts on TTY / warns on non-TTY, 12+ silent. Per-export JSON history under `<data-dir>/portability/history/`. Codex Refresh token now auto-included via the new `RefreshableProvider` interface.
- **LB7 Fault Injection**: `elnath chaos {run,list,report}` harness with 10 scenarios across 3 categories (tool / llm / ipc). 3-stage guard for daemon-runtime injection: `ELNATH_FAULT_PROFILE` env + `fault_injection.enabled` config + 5-second SIGINT-interruptible countdown. Per-scenario thresholds (max-runs / recovery attempts), JSONL + Markdown reports, BurstLimit handling for 429-burst patterns, zero overhead when disabled.
- **F7 Onboarding UX**: `elnath setup --quickstart` non-TUI 5-minute path. 14 `ELN-XXX` error codes with `elnath errors <code|list>` lookup. Top-level `--help` / `-h` now route to man-page-style help at every level. Setup-end demo task, local-only `~/.elnath/data/onboarding_metric.json` (0600, booleans only).
- **F8 Locale**: Unicode block heuristic detection (ko / ja / zh / en) with session-inherited resolver. `LocaleInstructionNode` (Priority 999) and `locale.ResponseDirective` single source of truth applied to both the normal conversation path and the `/skill` execution path. Bilingual mixes (Korean + English technical terms) are preserved in responses.

### F-5 Provider Patch

- **OAuth parity**: Anthropic OAuth added; Codex OAuth + Anthropic OAuth now provide the primary-provider surface without requiring an API key. `RefreshableProvider` interface-only contract — Codex implements refresh, Anthropic deferred.
- **Lesson provider reuse**: Haiku-based lesson extractor reuses the user's main provider credentials instead of a separate key.
- Model IDs stripped of dated suffixes; Telegram progress now shows real tool arguments for `file_*` tools.

### F-1 … F-4 Learning Infrastructure

- `lessons` CLI operational tooling (F-1), agent-task lesson extraction (F-2), redaction pipeline (F-2.5), multi-workflow learning extractor prep (F-3.1), team/ralph/autopilot integration (F-3.2), by-source stats + list filter (F-4).

### Gate 3.2 Benchmark Optimization

- Evidence-based agent-loop tuning (read-dedup `ReadTracker`, budget pressure injection at 70% / 90%, `toolResultPerToolLimit = 50_000` per-tool truncation, ack-continuation detection, configurable `MaxIterations`, `BenchmarkMode` prompt-node skip).
- BrownfieldNode execution discipline (ant P1 comments / P2 verification / P3 collaboration / P4 accuracy) with Go / TS Bugfix specifics (Viper `WatchConfig`, Next.js find-config).
- Corpus v6 / v7 retry cycles archived; final `3eb9291` Gate 3.2 PASS: BUG 9/9 (100%), BF 7/12 (58.3%).
- Gate Retry v8 research complete (`docs/superpowers/plans/2026-04-16-gate-retry-v8-research.md`): corpus 7 → 25 taxonomy locked, Python added, 5 prompt hypotheses deferred until dog-food evidence.

### Subsystems

- **C-1 Skill system**: Registry + executable skill definitions, permission/hook plumbing, `elnath skill` dispatch surface, tool filtering per skill.
- **D-1 Secret hook + audit trail**: Detector patterns (AWS, GitHub, GitLab, Slack, Stripe, Telegram, JWT, etc.) with audit trail for detection events.
- **E-1 Research CLI**: `elnath research` hypothesis → experiment → evaluate → wiki-update loop wired into the `research` workflow.
- **E-2 Ambient scheduler**: package + CLI hooks for recurring background checks.
- **E-3 Self-improvement**: follow-up files for conversation-mediated self-persona adjustments.
- **LB3 Conversation Spine**: cross-surface session resume (CLI ↔ Telegram ↔ daemon-submitted tasks) via canonical `ChatSessionBinder`.
- **Telegram redesign**: two-path architecture (chat direct via `ChatResponder` + task queue via workers). `ProgressReporter` (1.5s tool-dedup) + `StreamConsumer` (0.3s summary). PathGuard write-deny model. 429 inline retry.

### Architecture / Infrastructure

- Wiki preference routing (`internal/routing/` shared package, `RoutingContext.ProjectID`, `Router.Route(intent, ctx, pref)`).
- Full Inclusion Graph in prompt builder (LBB1) + Wiki Ingest Extension (LBB2).
- MC3 H1 Pass Rule + MC4 Month 3 Gate + MC2 metrics completion in `internal/eval`.
- `internal/userfacingerr` with 14 catalog entries and `UserFacingError.Is` / `Code()` for `errors.As` chains.

### Bug Fixes

- Router no longer forces ralph for greenfield tasks with verification hints.
- Top-level `--help` / `-h` flags intercept before the unknown-command branch (release-blocker fix from the F-6 validation run).
- Telegram tool-arg display; daemon worker panic recovery; benchmark recovery timeout capping; benchmark Python3 symlink + timeout hardening.

### Release Notes

- Binary version bumped to `0.5.0`. Portability bundle schema is v2 (AES-256-GCM chunked); legacy v1 bundles are not readable — export a fresh bundle if you had any v1 archives.
- Previous tags: `v0.3.0` → `v0.4.0` was documented in CHANGELOG but never tagged; this release supersedes that intent.
- First release validated via explicit 1-day burst scenario plan (`docs/f6-validation-plan.md`), committed alongside the code.

## v0.4.0 (2026-04-08)

### Execution Parity
- **Shared daemon orchestration seam**: daemon submitted tasks now reuse the same routed conversation/session/wiki path as interactive execution.
- **Workflow visibility**: team and research workflows emit progress/output through `OnText` instead of running silently.
- **Intent tuning**: classifier prompt now gives stronger boundary guidance for `wiki_query`, `research`, `project`, and `complex_task`.

### Safety and Limits
- **Output caps**: bash and `read_file` responses are deterministically truncated to keep tool output bounded.
- **Integration coverage**: added `cmd/elnath` seam tests plus daemon/workflow coverage around the v0.4.0 execution paths.

### Release Notes
- The old daemon bypass path and unbounded bash/read output limitation are now resolved in this release.

## v0.3.0 (2026-04-08)

### Onboarding Wizard
- **Bubbletea TUI wizard**: Polished first-run experience with Charm CLI aesthetics (Lipgloss styling, spinners, progress indicators)
- **Dual-path onboarding**: Quick Start (3 steps, 2 min) and Full Setup (8 steps, 5 min)
- **Full CLI i18n**: English/Korean with locale persistence in config.yaml, `ELNATH_LOCALE` env var
- **Non-interactive fallback**: `--non-interactive` flag + environment variable config for CI/pipe environments
- **MCP catalog**: Curated checkbox list of MCP servers grouped by category
- **API key live validation**: Spinner + real HTTP call to verify keys
- **Rerun mode**: `elnath setup` pre-fills existing config values with "reconfigure" badge

### Architecture Fixes
- **Wiki Karpathy completion**: LLM-based entity/concept extraction pipeline (`extract.go`), auto-creates typed wiki pages from conversations at session end
- **Research workflow wired**: `ResearchDeps` now injected — research intent triggers the full hypothesis→experiment→wiki loop
- **Context window compression activated**: `CompressMessages()` with LLM topic-based summarization (was always hard-snipping). Configurable `compress_threshold` and `max_context_tokens` in config.yaml
- **IntentWikiQuery routing**: Wiki queries now correctly classified and routed
- **RoutingContext heuristic**: File-count estimation enables team workflow for multi-file tasks
- **Session dual-persist**: `SessionPersister` interface for SQLite backup alongside JSONL primary
- **OpenAI output tokens**: Cost tracking now includes completion tokens

### Claude Code Feature Parity
- **Prompt caching**: `cache_control: ephemeral` on system prompt + last tool definition. Cache read/write token tracking
- **Parallel tool execution**: 2-phase pattern — sequential permission check, then goroutine-parallel execution with ordered result collection
- **Web search**: Real DuckDuckGo HTML search (stub removed)
- **Session resume**: `--continue` (latest session) and `--session <id>` CLI flags
- **Extended thinking**: `ThinkingBlock` type, `ThinkingBudget` parameter, SSE thinking delta handling
- **Image support**: `ImageBlock` with base64 source, Anthropic API image format marshaling
- **Token estimation**: JSON/code density detection (~3.5 chars/token vs ~4 for prose), image block estimation

### Distribution
- **goreleaser**: Cross-platform release builds (darwin/linux/windows, amd64/arm64)
- **Homebrew**: `brew install StelloJae/tap/elnath` (via goreleaser auto-generated formula)
- **go install**: `go install github.com/stello/elnath/cmd/elnath@latest`

### Improvements
- Locale validation in config (rejects unsupported locales)
- 70+ new tests across onboarding, config, wiki, and tools packages
- Test coverage: onboarding 76.6%, config 92.6%, wiki 66%+, all packages 60%+

### Known Limitations (carried from v0.2.0)
- Auto-doc session ingestion does not filter sensitive information
- MCP response ID not validated

## v0.2.0 (2026-04-07)

### New Features
- **MCP server integration**: Connect external tool servers via Model Context Protocol (stdio JSON-RPC). Tools are auto-discovered and registered with `mcp_` prefix.
- **Hook system**: Pre/post tool execution hooks for validation, formatting, and custom workflows. Configurable via `hooks` in config.yaml.
- **Ollama provider**: Local LLM support via Ollama with configurable base URL and model.
- **Cross-project intelligence**: Search wiki and conversation history across linked Elnath projects.
- **Persona presets**: Switch agent persona via `--persona` flag for context-appropriate behavior.
- **Hierarchical summarization**: Context window management with multi-level conversation compression.
- **OpenAI tool_use support**: Function calling support for OpenAI and Responses API providers.

### Bug Fixes
- **Permission tool name mismatch** (CRITICAL): `isReadOnly` and `isEditTool` now use actual registered tool names (`read_file`, `write_file`, `edit_file`, `git`) instead of stale names (`read`, `write`, `edit`, `git_log`). Wiki and conversation search tools are now correctly classified.
- **Permission not propagated to orchestrator** (CRITICAL): `WorkflowConfig` now carries a `Permission` field, ensuring all agents created by workflows (single, team, ralph) respect the configured permission mode.
- **Intent routing**: Complex tasks default to team workflow; added `wiki_query` intent.
- **Tool working directory**: Tools now execute in the current working directory, not `dataDir`.
- **Model provider routing**: Model name prefix selects the correct provider automatically.

### Improvements
- Test coverage: all packages at 60%+ (config 92%, mcp 68%, orchestrator 88%, tools 85%)
- 22,500+ Go LOC across 13 packages
- README rewritten for public release with MCP/Hooks/Permission documentation

### Known Limitations
- ~~Research workflow deps not wired~~ → Fixed in v0.3.0
- Auto-doc session ingestion does not filter sensitive information
- MCP response ID not validated
- Daemon bypasses orchestrator/wiki/session layers
- Bash/ReadTool output size unbounded

## v0.1.0 (2026-03-28)

Initial release.

- Agent loop with message-array-only state
- Anthropic Claude and OpenAI providers
- Tool executor: bash, file read/write/edit/glob/grep, git, web fetch/search
- Wiki with FTS5 hybrid search
- 5 workflow modes: single, team, autopilot, ralph, research
- Session persistence (JSONL) with fork support
- Background daemon with Unix socket IPC
- 4 permission modes: default, accept_edits, plan, bypass
- Self model with adaptive system prompt
