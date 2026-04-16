# Phase 8 Seed — Claude Code Tool & Command Parity

> **Status**: Seed document (not yet a phase plan). Captured 2026-04-17 during Phase 7 Stage B so the full Claude Code surface is recorded before momentum fades. Do not execute from this doc directly — turn it into a proper Phase 8.x plan after Stage B closes.
>
> **Source of catalog**: screenshots of Claude Code's internal Tool System (52 tools, 8 categories) and Command Catalog (95 commands, 5 categories) shared by the user on 2026-04-17. Lock icons in the source indicate Anthropic-internal or restricted features.

---

## Goal

Elnath's pitch is "standalone daemon with Claude Code-level execution quality, independent of any proprietary CLI". Phase 8 operationalises this by porting the parts of Claude Code's tool + command surface that make sense for a local, provider-agnostic agent, while skipping or redesigning the parts that are tied to Anthropic's harness/SaaS layer.

**Explicit non-goal**: 1:1 port. Phase 8 is triage + selective porting + divergent evolution where Elnath's architecture (standalone daemon, dual SQLite, wiki-first) invites a different shape.

---

## Triage legend

- **Have** — Elnath already has an equivalent (possibly with a different name/API)
- **Port** — worth bringing over largely as-is; design in Elnath vocabulary
- **Redesign** — concept is valuable but the Claude Code implementation is harness/SaaS-coupled; Elnath needs a different shape
- **Skip** — Claude Code product-specific (Anthropic SaaS hooks, GitHub/Slack installers, etc.) with no direct Elnath analog
- **Defer** — keep on the list, but not in the first Phase 8 slice

---

## Current Elnath inventory (baseline)

**Agent tools** (in `internal/tools/` + a few siblings):

| Elnath name | Purpose | Mirrors Claude Code |
|-|-|-|
| `read_file` | Read file by absolute path | FileRead |
| `write_file` | Overwrite/create file | FileWrite |
| `edit_file` | Exact-string replacement | FileEdit |
| `glob` | Pattern match file paths | Glob |
| `grep` | ripgrep-based content search | Grep |
| `bash` | Shell execution with scope guard | Bash |
| `web_fetch` | URL fetch + convert | WebFetch |
| `web_search` | Web search | WebSearch |
| `git` | Git operations | (not listed as tool in CC image) |
| `create_skill` | Create skill definition | (Skills via /skills in CC) |
| `wiki_search` / `wiki_read` / `wiki_write` | Local wiki (Elnath-unique) | (n/a — Claude Code uses memory files) |
| `conversation_search` / `cross_project_search` | Session transcript search | (n/a — CC has /resume + /session) |
| `mcp` (dynamic) | MCP server integration | mcp family |

**CLI commands** (in `cmd/elnath/`):

`version`, `help`, `chaos`, `run`, `setup`, `errors`, `daemon`, `portability`, `research`, `telegram`, `wiki`, `search`, `eval`, `task`, `lessons`, `skill`, `profile`, `explain`, `debug` (plus `debug` subcommands such as `debug consolidation` planned in Stage B).

---

## Tool System — triage

### FILE OPERATIONS (6)

| Claude Code | Decision | Notes |
|-|-|-|
| FileRead | Have (`read_file`) | Already in Elnath. Evolution: add `pages` param for PDFs + notebook cell support once needed. |
| FileEdit | Have (`edit_file`) | Already in Elnath. Evolution: add `replace_all` flag + multi-edit batching (cf. Claude Code's multi-edit). |
| FileWrite | Have (`write_file`) | Already in Elnath. Evolution: enforce "must read before write" gate similarly to Claude Code. |
| Glob | Have (`glob`) | Already in Elnath. |
| Grep | Have (`grep`) | Already in Elnath. |
| NotebookEdit | Port (deferred) | Low priority; Elnath users are not notebook-heavy yet. Add when first Jupyter need surfaces. |

### EXECUTION (3)

| Claude Code | Decision | Notes |
|-|-|-|
| Bash | Have (`bash`) | Elnath has scope-aware bash with path guard. Evolution: port `run_in_background` + `timeout` semantics. |
| PowerShell | Skip | Not a priority; Elnath is Unix-first per project conventions. Revisit if Windows support becomes a goal. |
| REPL | Port | Interactive language REPL for quick eval. Tie to existing `elnath run` interactive mode or add as a tool. |

### SEARCH & FETCH (4)

| Claude Code | Decision | Notes |
|-|-|-|
| WebBrowser (locked) | Skip | Anthropic-internal experiment. Use `claude-in-chrome` MCP when browser automation is needed. |
| WebFetch | Have (`web_fetch`) | Evolution: honor robots.txt, add HTML→markdown extraction. |
| WebSearch | Have (`web_search`) | Evolution: add provider routing (Brave, DuckDuckGo, etc.). |
| ToolSearch | Port | Useful for deferred-tool pattern. Elnath's tool registry is small, so deferred-tool search is less urgent — but it becomes valuable if Phase 8 ships MCP auto-discovery. |

### AGENTS & TASKS (11)

Claude Code's Agent/Task family is designed around spawning subagents in separate contexts. Elnath runs the agent loop itself in-process; the equivalent is goroutine-scoped sub-sessions with shared persistence.

| Claude Code | Decision | Notes |
|-|-|-|
| Agent | Redesign | Port as `agent.Spawn(spec)` returning a forked conversation view. Backing store: the existing session JSONL with a `parent_session_id` link. Already partially present via `internal/orchestrator`. |
| SendMessage | Redesign | Between forked sub-sessions. Needs a mailbox table in elnath.db. |
| TaskCreate / TaskGet / TaskList / TaskUpdate / TaskStop / TaskOutput | Redesign | Elnath already has `cmd_task.go`; flesh it out into a first-class Task surface that wraps forked sessions. |
| TeamCreate / TeamDelete | Redesign (defer) | OMC teams concept. Port only if multi-pane execution becomes a goal for Elnath. |
| ListPeers (locked) | Skip | Anthropic-internal. |

### PLANNING (5)

| Claude Code | Decision | Notes |
|-|-|-|
| EnterPlanMode / ExitPlanMode | Port | Elnath already has 4 permission modes including Plan; wire tool-level Enter/Exit so the agent can enter planning explicitly. |
| EnterWorktree / ExitWorktree | Port | Git worktree isolation is a natural fit. Backing: `git worktree add/remove` with a path registry in the daemon. Pairs with `elnath debug` transparency. |
| VerifyPlanEx... (locked) | Skip | Anthropic-internal. |

### MCP (4)

| Claude Code | Decision | Notes |
|-|-|-|
| mcp | Have (`internal/mcp`) | Elnath already has MCP support. |
| ListMcpResources / ReadMcpResource | Port | Extend existing MCP wrapper. |
| McpAuth | Port | OAuth/token flows for MCP servers. Already partially needed for Codex OAuth; generalise. |

### SYSTEM (11)

| Claude Code | Decision | Notes |
|-|-|-|
| AskUserQuestion | Port | Structured interaction (Elnath's CLI has basic prompts; formalise as an in-loop tool). |
| TodoWrite | Port | Core productivity surface. Back with JSONL + `elnath debug todo`. |
| Skill | Have (`create_skill`) | Evolution: port skill *invocation* (not just creation) so agent can load skills dynamically. |
| Config | Have (`elnath setup`) | Evolution: expose as an in-loop tool, not just CLI. |
| RemoteTrigger | Redesign (defer) | Webhook-style trigger. Only port if Elnath adds remote control. |
| CronCreate / CronDelete / CronList (locked) | Redesign | Elnath daemon already hosts ambient autonomy (Phase 5.2). Expose cron as first-class tool surface. |
| Snip (locked) | Skip | Anthropic-internal. |
| Workflow (locked) | Skip | Anthropic-internal. |
| TerminalCapt... (locked) | Skip | Anthropic-internal. |

### EXPERIMENTAL (8)

| Claude Code | Decision | Notes |
|-|-|-|
| Sleep | Port | Trivial but useful for polling flows. |
| SendUserMessage | Redesign | Elnath has Telegram notifier (Phase 6.7). Unify as a generic "notify user" tool. |
| StructuredOu... (locked) | Port | Structured JSON output tool. Aligns with Elnath's JSON-first telemetry. |
| LSP (locked) | Port (deferred) | Language server integration. High value long-term; heavy lift. |
| SendUserFile (locked) | Redesign | Telegram file sink already exists; formalise. |
| PushNotifica... (locked) | Redesign | Generic notification sink; Telegram/Discord/Slack routing. |
| Monitor (locked) | Port | Watch a background process for stdout lines. Pair with Elnath's daemon task runner. |
| SubscribePR (locked) | Skip | GitHub-specific; Elnath is git-neutral. |

---

## Command Catalog — triage

### SETUP & CONFIG (12)

| Claude Code | Decision | Notes |
|-|-|-|
| /init | Port | Elnath has `elnath setup`; add in-loop `/init` variant. |
| /login, /logout | Redesign | Elnath's auth is via config file + Codex OAuth. Add `/login <provider>` that wraps OAuth flow. |
| /config | Have (`elnath setup`) | Expose as `/config` inside interactive mode. |
| /permissions | Port | Elnath has 4 permission modes; add `/permissions` to inspect/change them interactively. |
| /model | Port | Switch active LLM provider/model mid-session. |
| /theme | Defer | Nice-to-have; Elnath CLI is not theming-heavy. |
| /terminal-setup | Skip | Claude-Code-CLI-specific. |
| /doctor | Port | Diagnostics: check DB, wiki indices, config, daemon health. |
| /onboarding | Have (`internal/onboarding`) | Expose as `/onboarding`. |
| /mcp | Port | List/add/remove MCP servers interactively. |
| /hooks | Port | PreToolUse/PostToolUse hook management. Elnath already has `internal/fault` + hooks infra. |

### DAILY WORKFLOW (24)

| Claude Code | Decision | Notes |
|-|-|-|
| /compact | Port | Conversation compaction. Tie to existing `internal/conversation` package. |
| /memory | Redesign | Elnath's wiki IS memory; `/memory` becomes a shortcut for `wiki read`. |
| /context | Port | Show current context budget + compaction status. |
| /plan | Port | Enter plan mode with optional prompt. |
| /resume | Have (`cmd_task.go` + session JSONL) | Expose as `/resume`. |
| /session | Port | Session management (list/fork/switch). |
| /files | Port | Show files in agent's read/write set this turn. |
| /add-dir | Port | Add dir to allowed paths (pairs with permission system). |
| /copy / /export | Port | Copy last response / export transcript. |
| /summary | Port | Auto-summarise conversation. |
| /clear | Port | Start fresh session. |
| /brief | Port | Toggle terse response mode. |
| /output-style (locked) | Skip | Anthropic-internal. |
| /color | Defer | CLI styling. |
| /vim | Defer | Editor modality. |
| /keybindings | Port | Configure interactive keybindings. |
| /skills | Have (`cmd_skill.go`) | Expose in-session. |
| /tasks | Have (`cmd_task.go`) | Expose in-session. |
| /agents | Port | Once Agent surface (above) lands. |
| /fast | Port | Switch to fast/cheaper model for current turn. |
| /effort | Port | Set reasoning-effort budget (Elnath supports this via config). |
| /extra-usage / /rate-limit-options | Redesign | Anthropic-SaaS-coupled. Elnath's equivalent: show usage from `elnath.db`. |

### CODE REVIEW & GIT (13)

| Claude Code | Decision | Notes |
|-|-|-|
| /review | Port | Pair with `code-reviewer` agent pattern. |
| /commit | Port | Git commit with message suggestion. |
| /commit-push-pr | Port | Full flow via `gh`. |
| /diff | Port | Show working-tree diff. |
| /pr_comments | Port | Fetch PR comments via `gh`. |
| /branch | Port | Create/switch branches. |
| /issue | Port | View/comment issues via `gh`. |
| /security-review | Port | Security pass via dedicated agent. |
| /autofix-pr (locked) | Skip | Anthropic-SaaS-coupled. |
| /share | Redesign | Share transcript; Elnath could export to markdown/gist via user-triggered action only. |
| /install-github-app (locked) / /install-slack-app (locked) | Skip | Anthropic-product. |
| /tag | Port | Git tag helper. |

### DEBUGGING & DIAGNOSTICS (23)

| Claude Code | Decision | Notes |
|-|-|-|
| /status | Port | Elnath has various `debug` subcommands; unify under `/status`. |
| /stats / /cost / /usage | Port | Expose from `elnath.db` usage table. |
| /version | Have (`cmd_version`). |
| /feedback | Defer | Anthropic-specific; Elnath could open an issue URL. |
| /think-back / /thinkback-play | Port | Replay prior reasoning from session JSONL — natural fit with Elnath's transcript model. |
| /rewind | Port | Revert to earlier session state. Pairs with JSONL replay. |
| /ctx_viz | Port | Visualize context composition. Elnath has `internal/prompt` nodes — expose them. |
| /debug-tool-call | Port | Re-run a specific tool call with different input. |
| /perf-issue / /heapdump | Port | Pprof integration. |
| /ant-trace / /backfill-sessions (locked) / /break-cache (locked) / /bridge-kick (locked) / /mock-limits (locked) / /oauth-refresh (locked) / /reset-limits (locked) | Skip | Anthropic-internal debug flows. |
| /env | Port | Show env vars the agent sees (redacted). |
| /bughunter (locked) | Skip | Anthropic-internal. |
| /passes (locked) | Skip | Anthropic-internal. |

### ADVANCED & EXPERIMENTAL (23)

| Claude Code | Decision | Notes |
|-|-|-|
| /advisor | Port | Elnath has architect/critic agents already — expose as `/advisor`. |
| /ultraplan (locked) | Skip | OMC has its own `ralplan`; no need to port. |
| /remote-control (locked) / /voice (locked) / /desktop (locked) / /chrome (locked) / /mobile (locked) | Defer | All surface-area-large and not core. |
| /teleport | Defer | Remote worktree / env swap. Requires daemon changes. |
| /sandbox | Port | Run agent in ephemeral worktree. Reuse Phase 5.2 wiki/boot substrate. |
| /plugin / /reload-plugins | Port | Elnath's agent extension model (tool registry + MCP) maps to this. |
| /web-setup | Skip | Anthropic-product. |
| /remote-env | Defer | Pair with /teleport. |
| /ide | Defer | IDE bridge — not core. |
| /stickers | Skip | Anthropic-internal easter egg. |
| /good-claude | Skip | Anthropic-internal. |
| /btw | Port | Out-of-band comment from user ("by the way"). Tiny, cheap — useful. |
| /upgrade | Port | `elnath upgrade` that pulls/rebuilds. |
| /release-notes | Port | Show changelog / recent commits. |
| /privacy-settings | Port | Toggle telemetry, redaction levels. Elnath already has `internal/learning/redactor`. |
| /help | Have | |
| /exit | Have (CLI EOF) | Formalise as `/exit`. |
| /rename | Port | Rename session / task. |

---

## Sequencing hint (first Phase 8 slice)

Don't try to land everything at once. A plausible P8.1 — P8.3 breakdown:

- **P8.1 — Inventory + Port gates**: finalise the triage, write a proper plan doc, and decide the owner of each surface (agent tool vs CLI command vs slash command). Land `/doctor`, `/status`, `/version`, `/help`, `/exit`, `/rename` (quick wins).
- **P8.2 — Dev ergonomics**: `/diff`, `/commit`, `/review`, `/branch`, `/tag`, `/pr_comments`, `/security-review`. Pairs natural with existing git tool.
- **P8.3 — Session + planning**: `/session`, `/resume`, `/clear`, `/fork`, `/rewind`, `/think-back`, EnterPlanMode/ExitPlanMode tool-level wiring.

Agent-tool redesign (Agent/Task/Team family) is a full phase on its own (P8.4+). Worktree tools fold in with `/sandbox` and `/teleport`.

---

## Open questions to resolve before Phase 8 kicks off

1. **Slash command owner**: does Elnath intercept `/xxx` in its REPL, or route to the command registry in `cmd/elnath/commands.go`, or both? (Current state: CLI subcommands only — no in-REPL slash commands.)
2. **Session fork storage**: do forked sub-sessions live in the same JSONL directory or a `sessions/<parent>/<child>.jsonl` tree? Impacts Agent/Task redesign.
3. **Notification routing**: unify Telegram / Discord / Slack sinks behind one `notify` tool? (Telegram exists; others don't yet.)
4. **LSP**: is this a priority enough to justify the lift? Probably defer until a concrete user need lands.
5. **Tool naming**: Claude Code uses PascalCase (`FileRead`), Elnath uses snake_case (`read_file`). Keep current Elnath style; add a mapping table for users migrating from Claude Code.

---

## What NOT to do

- Don't port Anthropic-SaaS-coupled features as-is (`/autofix-pr`, `/install-github-app`, `/mock-limits`, etc.) — they don't translate to a local-first daemon.
- Don't try to match Claude Code's exact command names where Elnath's naming is clearer (e.g., keep `elnath wiki search` over `/wiki`).
- Don't ship a 147-row feature matrix without user-validated prioritisation; pick the top ~20 first and iterate.
